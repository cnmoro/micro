package action

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/docker"
	"github.com/micro-editor/micro/v2/internal/remote"
	"github.com/micro-editor/micro/v2/internal/shell"
	"github.com/micro-editor/micro/v2/internal/util"
)

// RemoteSession describes an active "remote window": an SSH destination
// and the directory on it the Explorer/terminal/Docker are working
// against.
type RemoteSession struct {
	Target string // SSH destination, e.g. "user@host" or a ~/.ssh/config alias
	Path   string // resolved absolute remote path
}

// Remote is the currently active remote session (the top of remoteStack),
// or nil if working locally. remoteStack holds sessions "below" it, so
// that connecting to another host while already remote nests instead of
// replacing: disconnecting pops back to the previous session instead of
// going straight to local.
var Remote *RemoteSession
var remoteStack []*RemoteSession

// ToggleSSH opens the SSH connect/disconnect wizard (Alt-4).
func (h *BufPane) ToggleSSH() bool {
	openSSHWizard()
	return true
}

// SSHCmd is the `ssh` command: `ssh <target> [remote-path]` connects to a
// remote host (nesting on top of any session already active), and `ssh`
// with no arguments opens the same connect/disconnect wizard as Alt-4.
func (h *BufPane) SSHCmd(args []string) {
	if len(args) == 0 {
		openSSHWizard()
		return
	}

	target := args[0]
	remotePath := ""
	if len(args) > 1 {
		remotePath = args[1]
	}
	connectRemote(target, remotePath)
}

// openSSHWizard is the Alt-4 entry point. If not connected, it walks
// through picking a host and a remote path; if already connected, it
// offers to disconnect (back one level) or nest another connection on top.
func openSSHWizard() {
	if Remote == nil {
		pickHostThenConnect()
		return
	}

	items := []PopupItem{
		{Label: "Disconnect from " + Remote.Target, Run: disconnectRemote},
		{Label: "Connect to another host (nested)...", Run: pickHostThenConnect},
	}
	if len(remoteStack) > 0 {
		items = append(items, PopupItem{Label: "Disconnect all (back to local)", Run: disconnectAllRemote})
	}
	ShowPopup("SSH - connected to "+Remote.Target, items)
}

// pickHostThenConnect shows a popup listing ~/.ssh/config Host aliases
// (plus a "custom target" entry), then prompts for a remote path and
// connects - the "mini wizard" both the fresh-connect and nested-connect
// flows share.
func pickHostThenConnect() {
	hosts := sshConfigHosts()
	items := make([]PopupItem, 0, len(hosts)+1)
	for _, h := range hosts {
		host := h
		items = append(items, PopupItem{Label: host, Run: func() { promptRemotePath(host) }})
	}
	items = append(items, PopupItem{Label: "Custom target...", Run: promptCustomTarget})

	title := "SSH - connect to..."
	if Remote != nil {
		title = "SSH - nested connection from " + Remote.Target
	}
	ShowPopup(title, items)
}

func promptCustomTarget() {
	InfoBar.Prompt("SSH target (user@host or a ~/.ssh/config alias): ", "", "Command", nil, func(resp string, canceled bool) {
		if canceled || resp == "" {
			return
		}
		promptRemotePath(resp)
	})
}

func promptRemotePath(target string) {
	InfoBar.Prompt("Remote path (blank = home directory): ", "", "Command", nil, func(resp string, canceled bool) {
		if canceled {
			return
		}
		connectRemote(target, resp)
	})
}

func connectRemote(target, remotePath string) {
	sp := StartSpinner("Connecting to " + target + "...")
	go func() {
		resolved, err := remote.ResolvePath(target, remotePath)
		shell.Jobs <- shell.JobFunction{Function: func(string, []any) {
			sp.Stop()
			if err != nil {
				InfoBar.Error("Could not connect to " + target + ": " + err.Error())
				return
			}

			if Remote != nil {
				remoteStack = append(remoteStack, Remote)
			}
			Remote = &RemoteSession{Target: target, Path: resolved}
			docker.RemoteHost = target
			Sidebar.explorer.SetRemote(target, resolved)
			Sidebar.docker.Invalidate()
			Sidebar.Show(SidebarExplorerView)

			if TermEmuSupported {
				remoteShell := "cd " + remote.Quote(resolved) + " 2>/dev/null; exec \"${SHELL:-/bin/sh}\" -l"
				TermPanel.NewTabWithCommand("ssh:"+target, []string{"ssh", "-t", target, remoteShell})
				TermPanel.Show()
				InfoBar.Message("Connected to " + target + ":" + resolved)
			} else {
				// No PTY support on this platform (currently Windows) -
				// Explorer/Docker still work fine since they're just
				// one-shot `ssh`/`docker` subprocess calls, but an
				// interactive remote shell needs a real terminal
				// emulator, which isn't available here.
				InfoBar.Message("Connected to " + target + ":" + resolved + " (no integrated terminal on this platform - use an external SSH client for a shell)")
			}
		}}
	}()
}

// disconnectRemote pops the active remote session, falling back to
// whatever session (if any) was nested underneath it, or to local.
func disconnectRemote() {
	if Remote == nil {
		return
	}
	target := Remote.Target

	if n := len(remoteStack); n > 0 {
		Remote = remoteStack[n-1]
		remoteStack = remoteStack[:n-1]
		docker.RemoteHost = Remote.Target
		Sidebar.explorer.SetRemote(Remote.Target, Remote.Path)
		InfoBar.Message("Disconnected from " + target + ", back to " + Remote.Target)
	} else {
		Remote = nil
		docker.RemoteHost = ""
		Sidebar.explorer.SetLocal()
		InfoBar.Message("Disconnected from " + target + ", back to local")
	}
	Sidebar.docker.Refresh()
}

// disconnectAllRemote drops every nested session at once and returns to
// local.
func disconnectAllRemote() {
	if Remote == nil {
		return
	}
	Remote = nil
	remoteStack = nil
	docker.RemoteHost = ""
	Sidebar.explorer.SetLocal()
	Sidebar.docker.Refresh()
	InfoBar.Message("Disconnected, back to local")
}

// SSHComplete autocompletes SSH targets from Host aliases in ~/.ssh/config.
func SSHComplete(b *buffer.Buffer) ([]string, []string) {
	c := b.GetActiveCursor()
	input, argstart := b.GetArg()

	var suggestions []string
	for _, host := range sshConfigHosts() {
		if strings.HasPrefix(host, input) {
			suggestions = append(suggestions, host)
		}
	}

	sort.Strings(suggestions)
	completions := make([]string, len(suggestions))
	for i := range suggestions {
		completions[i] = util.SliceEndStr(suggestions[i], c.X-argstart)
	}
	return completions, suggestions
}

func sshConfigHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	f, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var hosts []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(strings.ToLower(line), "host ") {
			continue
		}
		for _, alias := range strings.Fields(line)[1:] {
			if strings.ContainsAny(alias, "*?") || seen[alias] {
				continue
			}
			seen[alias] = true
			hosts = append(hosts, alias)
		}
	}
	return hosts
}
