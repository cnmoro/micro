// Package git provides thin wrappers around the `git` CLI used by the Git
// sidebar view: detecting a repository, listing changed files with their
// status, and reading a file's content as of HEAD for side-by-side diffing.
//
// Every function takes a host: "" runs git locally (cmd.Dir = dir); a
// non-empty host runs it entirely over `ssh <host> git ...` instead - dir
// is then a path on that remote machine, not the local one - so browsing a
// repository over SSH needs nothing but git installed on the remote host,
// matching how the Explorer/terminal/Docker panels already work.
package git

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const cmdTimeout = 8 * time.Second
const remoteCmdTimeout = 20 * time.Second

// FileStatus is a single changed file as reported by `git status`.
type FileStatus struct {
	Path string
	Code byte // 'A', 'M', 'D', 'R', 'C', 'U' (untracked), '!' (conflict)
	// Staged reports whether the change is staged (index) rather than
	// only in the working tree - the same "M" can be either.
	Staged bool
}

// run executes `git <args...>` rooted at dir, either locally or (if host is
// non-empty) over ssh, returning trimmed stdout/stderr separately so
// callers can tell "ran fine" apart from "ran, but with an empty result"
// and still get at the failure reason on error.
func run(host, dir string, args ...string) (string, string, error) {
	timeout := cmdTimeout
	var cmd *exec.Cmd
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if host == "" {
		cmd = exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
	} else {
		timeout = remoteCmdTimeout
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
		script := "cd " + shQuote(dir) + " && " + remoteScript("git", args...)
		cmd = exec.CommandContext(ctx, "ssh", host, script)
	}

	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	stdout, stderr := strings.TrimSpace(out.String()), strings.TrimSpace(errOut.String())
	if err != nil {
		if stderr == "" {
			stderr = err.Error()
		}
		return stdout, stderr, err
	}
	return stdout, stderr, nil
}

func remoteScript(name string, args ...string) string {
	script := shQuote(name)
	for _, a := range args {
		script += " " + shQuote(a)
	}
	return script
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// IsRepo reports whether dir is inside a git working tree. detail is empty
// when it is; when it isn't, detail explains why - either git's own message
// (e.g. the usual "fatal: not a git repository...") or, if git itself
// couldn't be run at all (not installed / not on PATH, locally or on the
// remote host), a message saying so - so a caller can tell "there's
// genuinely no repo here" apart from "git isn't usable" instead of
// collapsing both into the same generic answer.
func IsRepo(host, dir string) (bool, string) {
	out, errOut, err := run(host, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false, errOut
	}
	return out == "true", ""
}

// RepoRoot returns the top-level directory of the repository dir is inside.
func RepoRoot(host, dir string) (string, error) {
	out, errOut, err := run(host, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", &CmdError{errOut}
	}
	return out, nil
}

// Branch returns the current branch name, or a short description of a
// detached HEAD if not on a branch.
func Branch(host, dir string) string {
	if out, _, err := run(host, dir, "branch", "--show-current"); err == nil && out != "" {
		return out
	}
	if out, _, err := run(host, dir, "rev-parse", "--short", "HEAD"); err == nil {
		return "detached@" + out
	}
	return ""
}

// Status returns every changed (staged, unstaged, or untracked) file in the
// repository rooted at dir, relative to that root.
func Status(host, dir string) ([]FileStatus, error) {
	out, errOut, err := run(host, dir, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, &CmdError{errOut}
	}
	if out == "" {
		return nil, nil
	}

	var result []FileStatus
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		x, y := line[0], line[1]
		path := line[3:]
		if idx := strings.Index(path, " -> "); idx != -1 {
			// renames are reported as "old -> new"; only the new path
			// still exists on disk / is worth diffing.
			path = path[idx+4:]
		}
		result = append(result, FileStatus{
			Path:   path,
			Code:   classify(x, y),
			Staged: x != ' ' && x != '?',
		})
	}
	return result, nil
}

func classify(x, y byte) byte {
	switch {
	case x == '?' && y == '?':
		return 'U'
	case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
		return '!'
	case x == 'A':
		return 'A'
	case x == 'D' || y == 'D':
		return 'D'
	case x == 'R':
		return 'R'
	case x == 'C':
		return 'C'
	default:
		return 'M'
	}
}

// StatusName returns a human-readable label for a status code.
func StatusName(code byte) string {
	switch code {
	case 'A':
		return "Added"
	case 'M':
		return "Modified"
	case 'D':
		return "Deleted"
	case 'R':
		return "Renamed"
	case 'C':
		return "Copied"
	case 'U':
		return "Untracked"
	case '!':
		return "Conflict"
	}
	return "Changed"
}

// Show returns relPath's content as of HEAD, relative to repoRoot. A file
// with no HEAD version (untracked, or newly added and not yet committed)
// simply has no history to show, so this returns "" rather than an error.
func Show(host, repoRoot, relPath string) (string, error) {
	out, _, err := run(host, repoRoot, "show", "HEAD:"+relPath)
	if err != nil {
		return "", nil
	}
	return out, nil
}

// CmdError wraps a failed git invocation with its stderr.
type CmdError struct {
	Msg string
}

func (e *CmdError) Error() string { return e.Msg }
