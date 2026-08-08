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
	"regexp"
	"strconv"
	"strings"
	"time"
)

const cmdTimeout = 8 * time.Second
const remoteCmdTimeout = 20 * time.Second

// FileStatus is a single changed file as reported by `git status`, with its
// staged (index-vs-HEAD) and unstaged (worktree-vs-index) status tracked
// independently, since git allows a file to be both at once (some hunks
// staged, others not) - StagedCode/UnstagedCode are 0 when that side has
// no change, so a caller can tell "not staged" apart from "staged clean".
type FileStatus struct {
	Path         string
	StagedCode   byte // 'A', 'M', 'D', 'R', 'C', '!' (conflict), or 0
	UnstagedCode byte // 'M', 'D', 'U' (untracked), '!' (conflict), or 0
}

// run executes `git <args...>` rooted at dir, either locally or (if host is
// non-empty) over ssh. stdout is returned exactly as git produced it (only
// stderr is trimmed, for a clean single-line error message) - callers that
// want a single trimmed value (IsRepo, RepoRoot, Branch) trim it
// themselves; Status must not, since git status --porcelain's leading
// column is meaningful whitespace (trimming it off the first line, if that
// line happens to start with a space, silently eats the first character of
// that line's path instead - a real bug this comment exists to prevent
// reintroducing).
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
	stdout, stderr := out.String(), strings.TrimSpace(errOut.String())
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
	return strings.TrimSpace(out) == "true", ""
}

// RepoRoot returns the top-level directory of the repository dir is inside.
func RepoRoot(host, dir string) (string, error) {
	out, errOut, err := run(host, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", &CmdError{errOut}
	}
	return strings.TrimSpace(out), nil
}

// Branch returns the current branch name, or a short description of a
// detached HEAD if not on a branch.
func Branch(host, dir string) string {
	if out, _, err := run(host, dir, "branch", "--show-current"); err == nil {
		if b := strings.TrimSpace(out); b != "" {
			return b
		}
	}
	if out, _, err := run(host, dir, "rev-parse", "--short", "HEAD"); err == nil {
		return "detached@" + strings.TrimSpace(out)
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
	// Only strip the trailing newline git always appends - not leading
	// whitespace, which (unlike here) is meaningful data on every other
	// line (see the comment on run).
	out = strings.TrimRight(out, "\n")
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
			Path:         path,
			StagedCode:   classifyStaged(x, y),
			UnstagedCode: classifyUnstaged(x, y),
		})
	}
	return result, nil
}

func classifyStaged(x, y byte) byte {
	if x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D') {
		return '!' // conflict - shown once, not split staged/unstaged
	}
	switch x {
	case 'A':
		return 'A'
	case 'D':
		return 'D'
	case 'R':
		return 'R'
	case 'C':
		return 'C'
	case 'M':
		return 'M'
	}
	return 0
}

func classifyUnstaged(x, y byte) byte {
	if x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D') {
		return 0 // already reported as a conflict on the staged side
	}
	if x == '?' && y == '?' {
		return 'U'
	}
	switch y {
	case 'M':
		return 'M'
	case 'D':
		return 'D'
	}
	return 0
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
	// Deliberately not trimmed - this is the file's exact stored content,
	// and trimming would silently misrepresent leading/trailing blank
	// lines in the diff view.
	return out, nil
}

// Stage adds relPath's current working-tree content to the index (`git add
// -- relPath`) - stages the whole file, including any untracked file.
func Stage(host, repoRoot, relPath string) error {
	_, errOut, err := run(host, repoRoot, "add", "--", relPath)
	if err != nil {
		return &CmdError{errOut}
	}
	return nil
}

// Unstage removes relPath's staged changes from the index without
// touching the working tree (`git reset -- relPath`) - safe to call even
// on a file that was never committed (reset with no commit-ish defaults
// to HEAD, and unstaging a newly-added file just un-adds it).
func Unstage(host, repoRoot, relPath string) error {
	_, errOut, err := run(host, repoRoot, "reset", "--", relPath)
	if err != nil {
		return &CmdError{errOut}
	}
	return nil
}

// Commit commits the currently staged changes with the given message.
func Commit(host, repoRoot, message string) error {
	_, errOut, err := run(host, repoRoot, "commit", "-m", message)
	if err != nil {
		return &CmdError{errOut}
	}
	return nil
}

// DiffFile returns the unified diff of relPath's unstaged changes (index
// vs. working tree) - the same diff `git apply --cached` needs a hunk
// from to stage just that hunk.
func DiffFile(host, repoRoot, relPath string) (string, error) {
	out, errOut, err := run(host, repoRoot, "diff", "--", relPath)
	if err != nil {
		return "", &CmdError{errOut}
	}
	return out, nil
}

// StageHunkAtLine stages just the hunk of relPath's unstaged diff that
// covers the given 1-indexed working-tree line number (e.g. where the
// cursor is in the diff view's working-tree pane), by extracting that one
// hunk from `git diff` and feeding it to `git apply --cached`. Returns
// false (with a nil error) if no hunk covers that line - most likely the
// line is unchanged, or the file's on-disk content has since diverged
// from what's displayed.
func StageHunkAtLine(host, repoRoot, relPath string, line int) (bool, error) {
	diff, err := DiffFile(host, repoRoot, relPath)
	if err != nil {
		return false, err
	}
	patch, ok := extractHunkAtLine(diff, line)
	if !ok {
		return false, nil
	}

	timeout := cmdTimeout
	var cmd *exec.Cmd
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if host == "" {
		cmd = exec.CommandContext(ctx, "git", "apply", "--cached", "-")
		cmd.Dir = repoRoot
	} else {
		timeout = remoteCmdTimeout
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
		script := "cd " + shQuote(repoRoot) + " && " + remoteScript("git", "apply", "--cached", "-")
		cmd = exec.CommandContext(ctx, "ssh", host, script)
	}
	cmd.Stdin = strings.NewReader(patch)
	var errOut strings.Builder
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return false, &CmdError{msg}
	}
	return true, nil
}

// hunkHeaderRe matches a unified diff hunk header, e.g.
// "@@ -12,5 +14,7 @@ optional trailing context".
var hunkHeaderRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// extractHunkAtLine finds the hunk in diffText (a unified diff for a
// single file, as `git diff` produces) whose new-file line range contains
// line, and returns a standalone patch (the file header plus just that
// hunk) suitable for `git apply`.
func extractHunkAtLine(diffText string, line int) (string, bool) {
	lines := strings.Split(diffText, "\n")

	i := 0
	for i < len(lines) && !strings.HasPrefix(lines[i], "@@") {
		i++
	}
	header := strings.Join(lines[:i], "\n")

	for i < len(lines) {
		m := hunkHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		newStart, _ := strconv.Atoi(m[1])
		newCount := 1
		if m[2] != "" {
			newCount, _ = strconv.Atoi(m[2])
		}

		hunkStart := i
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@") {
			i++
		}
		// A pure-deletion hunk has newCount 0 (nothing added on the new
		// side to place a cursor on) - newStart there is the line after
		// which the deletion happened, so treat landing exactly on it as
		// a match too, or such a hunk could never be targeted at all.
		if (line >= newStart && line < newStart+newCount) || (newCount == 0 && line == newStart) {
			hunk := strings.Join(lines[hunkStart:i], "\n")
			return header + "\n" + hunk + "\n", true
		}
	}
	return "", false
}

// CmdError wraps a failed git invocation with its stderr.
type CmdError struct {
	Msg string
}

func (e *CmdError) Error() string { return e.Msg }
