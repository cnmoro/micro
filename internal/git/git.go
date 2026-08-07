// Package git provides thin wrappers around the `git` CLI used by the Git
// sidebar view: detecting a repository, listing changed files with their
// status, and reading a file's content as of HEAD for side-by-side diffing.
package git

import (
	"os/exec"
	"strings"
)

// FileStatus is a single changed file as reported by `git status`.
type FileStatus struct {
	Path string
	Code byte // 'A', 'M', 'D', 'R', 'C', 'U' (untracked), '!' (conflict)
	// Staged reports whether the change is staged (index) rather than
	// only in the working tree - the same "M" can be either.
	Staged bool
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// RepoRoot returns the top-level directory of the repository dir is inside.
func RepoRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Branch returns the current branch name, or a short description of a
// detached HEAD if not on a branch.
func Branch(dir string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		if b := strings.TrimSpace(string(out)); b != "" {
			return b
		}
	}
	cmd = exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		return "detached@" + strings.TrimSpace(string(out))
	}
	return ""
}

// Status returns every changed (staged, unstaged, or untracked) file in the
// repository rooted at dir, relative to that root.
func Status(dir string) ([]FileStatus, error) {
	cmd := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=all")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var result []FileStatus
	for _, line := range strings.Split(string(out), "\n") {
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
func Show(repoRoot, relPath string) (string, error) {
	cmd := exec.Command("git", "show", "HEAD:"+relPath)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	return string(out), nil
}
