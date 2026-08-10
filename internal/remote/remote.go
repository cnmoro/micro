// Package remote provides SSH-backed file and process primitives used by
// micro's "remote" mode (Explorer/Docker/terminal against a remote host),
// analogous to VS Code's Remote-SSH. It shells out to the system `ssh`
// binary so it transparently reuses the user's existing SSH config,
// known_hosts, agent and keys - no credentials are ever handled directly.
package remote

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const cmdTimeout = 15 * time.Second

// controlDir holds this process's SSH ControlMaster sockets, one per
// target host - so every ssh invocation micro makes for a given host (the
// Explorer, Docker, git, and the terminal panel each shell out to ssh
// independently) reuses a single authenticated connection via OpenSSH's
// own connection multiplexing, instead of each one prompting for a
// password separately. Without this, a password-auth host can easily
// mean typing the same password ten times in a row for what feels like
// one "connect" action.
var controlDir string

func init() {
	if d, err := os.MkdirTemp("", "micro-ssh-*"); err == nil {
		controlDir = d
	}
}

// MultiplexArgs returns the ssh CLI arguments that route an invocation for
// target through this process's shared, kept-alive connection for that
// host (established on whichever call happens to run first, torn down by
// CloseAllMultiplexed on quit). Prepend the result to any ssh argv, before
// the destination. Returns nil if a control directory couldn't be
// created, in which case ssh just falls back to its normal one-connection-
// per-invocation behavior.
//
// ControlPath uses ssh's own "%C" token (a hash of the connection's local
// host/remote host/port/user) rather than something built from target
// directly - %C is always a short, filesystem-safe, unique-per-connection
// name, which a raw target string (arbitrary length, `/`, `@`, `:` and
// all) is not safe to assume.
func MultiplexArgs(target string) []string {
	if controlDir == "" {
		return nil
	}
	return []string{
		"-o", "ControlMaster=auto",
		// Bounded rather than indefinite ("yes") as a safety net: normal
		// quit paths call CloseAllMultiplexed explicitly, but if that
		// somehow doesn't run (a crash, a force-kill), an indefinitely
		// persistent master would otherwise linger as a background
		// process forever instead of just until it's been idle a while.
		"-o", "ControlPersist=10m",
		"-o", "ControlPath=" + filepath.Join(controlDir, "%C"),
	}
}

// CloseAllMultiplexed asks ssh to close every ControlMaster connection
// this process opened. Best-effort: called once on quit, and if it
// doesn't fully succeed (e.g. a control socket ssh itself already cleaned
// up), the background ssh master processes still exit on their own once
// idle for a while, or when the OS reclaims the temp directory.
func CloseAllMultiplexed() {
	if controlDir == "" {
		return
	}
	entries, err := os.ReadDir(controlDir)
	if err == nil {
		for _, e := range entries {
			sockPath := filepath.Join(controlDir, e.Name())
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			exec.CommandContext(ctx, "ssh", "-o", "ControlPath="+sockPath, "-O", "exit", "x").Run()
			cancel()
		}
	}
	os.RemoveAll(controlDir)
}

// Entry is a single file or directory returned by ListDir.
type Entry struct {
	Name  string
	IsDir bool
}

// Quote single-quotes s for safe interpolation into a POSIX remote shell
// command.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Join joins remote path elements with POSIX ("/") semantics, regardless
// of the local OS (unlike filepath.Join, which is OS-specific).
func Join(elem ...string) string {
	return path.Join(elem...)
}

// Dir returns the remote directory containing p (POSIX semantics).
func Dir(p string) string {
	return path.Dir(p)
}

// run executes `ssh target <script>` and returns stdout; stderr (trimmed)
// becomes the error message on failure.
//
// script is passed to ssh as a single argument, not split across several
// exec.Command args: ssh always hands its trailing command arguments to
// the remote login shell as `$SHELL -c "<args joined with spaces>"`, so
// splitting a pre-quoted script across multiple args (e.g. "sh", "-c",
// script) makes it go through two rounds of shell parsing and corrupts
// any quoting inside it.
func run(target, script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	args := append(MultiplexArgs(target), target, script)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), &Error{Target: target, Script: script, Msg: msg}
	}
	return out.Bytes(), nil
}

func runWithStdin(target, script string, stdin []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	args := append(MultiplexArgs(target), target, script)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return &Error{Target: target, Script: script, Msg: msg}
	}
	return nil
}

// Error wraps a failed remote command with its target/script/stderr.
type Error struct {
	Target string
	Script string
	Msg    string
}

func (e *Error) Error() string {
	return "ssh " + e.Target + ": " + e.Msg
}

// Reachable checks that target is reachable over SSH and returns a short
// identifying string (uname -sr) on success.
func Reachable(target string) (string, error) {
	out, err := run(target, "uname -sr")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolvePath resolves p on the remote host to an absolute path. If p is
// empty, it resolves to the remote user's home directory.
func ResolvePath(target, p string) (string, error) {
	dest := "\"$HOME\""
	if p != "" {
		dest = Quote(p)
	}
	script := "cd " + dest + " 2>/dev/null && pwd"
	out, err := run(target, script)
	if err != nil {
		return "", err
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" {
		return "", &Error{Target: target, Script: script, Msg: "no such directory: " + p}
	}
	return resolved, nil
}

// ListDir lists the contents of dir on the remote host, directories first
// then files, both alphabetically - matching the local Explorer's sort.
func ListDir(target, dir string) ([]Entry, error) {
	// %f = filename, %y = type (d, f, l, ...); one entry per line, NUL is
	// not available portably here so we rely on filenames not containing
	// newlines (matches the local os.ReadDir-based Explorer's assumption).
	script := "find " + Quote(dir) + " -mindepth 1 -maxdepth 1 -printf '%y\\t%f\\n' 2>/dev/null"
	out, err := run(target, script)
	if err != nil {
		return nil, err
	}

	var dirs, files []Entry
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		isDir := parts[0] == "d"
		e := Entry{Name: parts[1], IsDir: isDir}
		if isDir {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	return append(dirs, files...), nil
}

// ReadFile fetches the contents of a remote file.
func ReadFile(target, p string) ([]byte, error) {
	return run(target, "cat "+Quote(p))
}

// WriteFile overwrites (or creates) a remote file with data.
func WriteFile(target, p string, data []byte) error {
	return runWithStdin(target, "cat > "+Quote(p), data)
}

// Mkdir creates a remote directory (and parents).
func Mkdir(target, p string) error {
	_, err := run(target, "mkdir -p "+Quote(p))
	return err
}

// CreateFile creates an empty remote file if it doesn't already exist.
func CreateFile(target, p string) error {
	_, err := run(target, "[ -e "+Quote(p)+" ] || : > "+Quote(p))
	return err
}

// Remove deletes a remote file or (recursively) directory.
func Remove(target, p string) error {
	_, err := run(target, "rm -rf "+Quote(p))
	return err
}

// Rename moves/renames a remote file or directory.
func Rename(target, oldPath, newPath string) error {
	_, err := run(target, "mv "+Quote(oldPath)+" "+Quote(newPath))
	return err
}
