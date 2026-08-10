package action

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/micro-editor/micro/v2/internal/remote"
	"github.com/micro-editor/micro/v2/internal/util"
)

// OpenFolder opens the Alt-5 "change working directory" wizard.
func (h *BufPane) OpenFolder() bool {
	openWorkDirWizard()
	return true
}

// OpenFolderCmd is the `openfolder` command: same as Alt-5.
func (h *BufPane) OpenFolderCmd(args []string) {
	if len(args) > 0 {
		if Remote != nil {
			changeRemoteWorkDir(args[0])
		} else {
			changeWorkDir(args[0])
		}
		return
	}
	openWorkDirWizard()
}

// openWorkDirWizard prompts for a new working directory - on the active
// SSH host if one is connected, otherwise locally - and switches to it.
// While connected, this stays on that host rather than backing out to the
// local filesystem: Alt-5 is "change folder", not "disconnect", and
// changing directory is something that very much should happen inside
// the remote machine you're already working in, the same as every other
// remote-aware action (Explorer, Docker, git, the terminal panel).
func openWorkDirWizard() {
	if Remote != nil {
		InfoBar.Prompt("Open remote folder: ", Remote.Path, "Command", nil, func(resp string, canceled bool) {
			if canceled || resp == "" {
				return
			}
			changeRemoteWorkDir(resp)
		})
		return
	}
	wd, _ := os.Getwd()
	InfoBar.Prompt("Open folder: ", wd, "OpenFolder", nil, func(resp string, canceled bool) {
		if canceled || resp == "" {
			return
		}
		changeWorkDir(resp)
	})
}

// changeRemoteWorkDir re-roots the Explorer (and Docker/Git, which follow
// the active session rather than a path) at path on the currently
// connected SSH host - a path relative to neither `/` nor `~` is resolved
// against the session's current remote directory, matching how a plain
// relative path typed into the local Open Folder prompt resolves against
// the local working directory rather than always meaning "from home".
func changeRemoteWorkDir(path string) {
	target := Remote.Target
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "~") {
		path = remote.Join(Remote.Path, path)
	}
	resolved, err := remote.ResolvePath(target, path)
	if err != nil {
		InfoBar.Error(err)
		return
	}
	Remote.Path = resolved
	Sidebar.explorer.SetRemote(target, resolved)
	Sidebar.git.Invalidate()
	Sidebar.Show(SidebarExplorerView)
	InfoBar.Message("Remote working directory: " + target + ":" + resolved)
}

// changeWorkDir changes micro's working directory to path and points the
// Explorer at it, the local equivalent of VS Code's "Open Folder". Both
// callers (openWorkDirWizard and OpenFolderCmd) only reach this when
// there's no active SSH session - disconnecting first is still handled
// here (rather than assumed unreachable) as a defensive fallback in case
// Remote somehow becomes non-nil between that check and this call.
func changeWorkDir(path string) {
	path, err := util.ReplaceHome(path)
	if err != nil {
		InfoBar.Error(err)
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		InfoBar.Error(err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		InfoBar.Error(err)
		return
	}
	if !info.IsDir() {
		InfoBar.Error(abs + " is not a directory")
		return
	}

	if bp := MainTab().CurPane(); bp != nil {
		bp.CdCmd([]string{abs})
	} else if err := os.Chdir(abs); err != nil {
		InfoBar.Error(err)
		return
	}

	if Remote != nil {
		disconnectAllRemote()
	}
	Sidebar.explorer.SetLocal()
	Sidebar.git.Invalidate()
	Sidebar.Show(SidebarExplorerView)
	InfoBar.Message("Working directory: " + abs)
}
