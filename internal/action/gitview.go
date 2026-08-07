package action

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/config"
	"github.com/micro-editor/micro/v2/internal/git"
	"github.com/micro-editor/micro/v2/internal/remote"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/micro/v2/internal/shell"
	"github.com/micro-editor/tcell/v2"
)

// btDiffView is like buffer.BTScratch (can't be saved) but with syntax
// highlighting turned on - filetype detection matches on a buffer's Path,
// so the diff view's buffers keep the file's real extension in Path (a
// synthetic, made-unique-per-open directory prefix ahead of it, so two
// diff buffers for the same file don't collide as "the same buffer" -
// see openDiff) and use SetName for the human-readable tab label instead.
var btDiffView = buffer.BufType{Kind: 7, Readonly: true, Scratch: true, Syntax: true}

// diffViewSeq makes each openDiff call's buffer paths unique so opening a
// diff for the same file twice never resolves to an already-open buffer
// (buffer.NewBuffer dedupes same-Path buffers by design) and shows a stale
// first look instead of the fresh content just read.
var diffViewSeq int

// GitView is the git-status sidebar view: every changed file in the current
// repository - the local working directory, or (while an SSH session is
// active) the repository at the remote session's path - colored by status,
// opening a side-by-side HEAD-vs-working-tree diff on click/Enter.
type GitView struct {
	host     string // SSH target this data was loaded from, or "" if local
	repoRoot string
	branch   string
	inRepo   bool
	loaded   bool
	loading  bool
	errMsg   string

	// checkedDir/notRepoDetail record which directory was last checked and,
	// if it wasn't a repo, why (git's own error, or "git isn't runnable at
	// all") - shown in the panel so a wrong-looking result is diagnosable
	// instead of always reading the same generic message.
	checkedDir    string
	notRepoDetail string

	files []git.FileStatus

	selected        int
	scroll          int
	followSelection bool
}

func NewGitView() *GitView {
	return new(GitView)
}

func (g *GitView) Title() string {
	title := "GIT"
	if g.branch != "" {
		title += " (" + g.branch + nestSuffix() + ")"
	}
	if Remote != nil {
		title += " @ " + Remote.Target
	}
	return title
}

// Invalidate marks the currently loaded status as stale (e.g. the working
// directory changed) without fetching anything - see DockerView.Invalidate
// for why this is preferable to an eager Refresh in most call sites.
func (g *GitView) Invalidate() {
	g.loaded = false
}

// Refresh reloads the git status of the current directory - the active SSH
// session's path if one is connected, otherwise the local working
// directory - in the background.
func (g *GitView) Refresh() {
	g.loading = true

	host, dir := "", ""
	if Remote != nil {
		host, dir = Remote.Target, Remote.Path
	} else {
		dir, _ = os.Getwd()
	}

	go func() {
		inRepo, notRepoDetail := git.IsRepo(host, dir)
		var root, branch, errMsg string
		var files []git.FileStatus
		if inRepo {
			var err error
			root, err = git.RepoRoot(host, dir)
			if err != nil {
				errMsg = err.Error()
			} else {
				branch = git.Branch(host, root)
				files, err = git.Status(host, root)
				if err != nil {
					errMsg = err.Error()
				}
			}
		}

		shell.Jobs <- shell.JobFunction{Function: func(string, []any) {
			g.loading = false
			g.loaded = true
			g.host = host
			g.inRepo = inRepo
			g.repoRoot = root
			g.branch = branch
			g.errMsg = errMsg
			g.checkedDir = dir
			g.notRepoDetail = notRepoDetail
			g.files = files
			sort.Slice(g.files, func(i, j int) bool { return g.files[i].Path < g.files[j].Path })
			g.clampSelection()
		}}
	}()
}

func (g *GitView) clampSelection() {
	if g.selected < 0 {
		g.selected = 0
	}
	if g.selected >= len(g.files) {
		g.selected = len(g.files) - 1
	}
}

func (g *GitView) scrollToSelected(h int) {
	if g.selected < g.scroll {
		g.scroll = g.selected
	}
	if g.selected >= g.scroll+h {
		g.scroll = g.selected - h + 1
	}
	if g.scroll < 0 {
		g.scroll = 0
	}
}

func (g *GitView) clampScroll(h int) {
	max := len(g.files) - h
	if max < 0 {
		max = 0
	}
	if g.scroll > max {
		g.scroll = max
	}
	if g.scroll < 0 {
		g.scroll = 0
	}
}

// statusStyle returns the display style for a status code, falling back to
// the plain foreground if the active colorscheme doesn't define the
// relevant diff-* group (the same ones micro's gutter git-diff markers use).
func statusStyle(code byte) tcell.Style {
	key := "diff-modified"
	switch code {
	case 'A', 'C', 'U':
		key = "diff-added"
	case 'D':
		key = "diff-deleted"
	case '!':
		key = "error"
	}
	if s, ok := config.Colorscheme[key]; ok {
		return s
	}
	return config.DefStyle
}

func (g *GitView) Display(x, y, w, h int) {
	if !g.loaded && !g.loading {
		g.Refresh()
	}

	style := config.DefStyle
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			screen.SetContent(x+col, y+row, ' ', nil, style)
		}
	}

	if g.loading && !g.loaded {
		drawWrapped(x, y, w, h, "Loading...", style)
		return
	}
	if !g.inRepo {
		msg := "Not a git repository: " + g.checkedDir
		if g.notRepoDetail != "" {
			msg += " (" + g.notRepoDetail + ")"
		}
		drawWrapped(x, y, w, h, msg, style)
		return
	}
	if g.errMsg != "" {
		drawWrapped(x, y, w, h, g.errMsg, style)
		return
	}
	if len(g.files) == 0 {
		drawWrapped(x, y, w, h, "No changes", style)
		return
	}

	if g.followSelection {
		g.scrollToSelected(h)
		g.followSelection = false
	} else {
		g.clampScroll(h)
	}

	for row := 0; row < h; row++ {
		idx := g.scroll + row
		if idx < 0 || idx >= len(g.files) {
			continue
		}
		f := g.files[idx]

		st := statusStyle(f.Code)
		if idx == g.selected {
			st = st.Reverse(true)
		}

		screen.SetContent(x, y+row, rune(f.Code), nil, st)
		screen.SetContent(x+1, y+row, ' ', nil, st)

		runes := []rune(f.Path)
		for i := 0; i < len(runes) && 2+i < w; i++ {
			screen.SetContent(x+2+i, y+row, runes[i], nil, st)
		}
		if idx == g.selected {
			for c := 2 + len(runes); c < w; c++ {
				screen.SetContent(x+c, y+row, ' ', nil, st)
			}
		}
	}
}

func (g *GitView) HandleClick(x, y int, button tcell.ButtonMask) {
	if !g.inRepo || len(g.files) == 0 {
		return
	}
	idx := g.scroll + y
	if idx < 0 || idx >= len(g.files) {
		return
	}
	g.selected = idx
	g.openDiff(idx)
}

func (g *GitView) HandleWheel(up bool) {
	if up {
		g.scroll -= 3
	} else {
		g.scroll += 3
	}
	if g.scroll < 0 {
		g.scroll = 0
	}
}

func (g *GitView) HandleKey(e *tcell.EventKey) bool {
	if !g.inRepo {
		if e.Key() == tcell.KeyRune && (e.Rune() == 'r' || e.Rune() == 'R') {
			g.Refresh()
			return true
		}
		return false
	}

	switch e.Key() {
	case tcell.KeyUp:
		g.selected--
		g.clampSelection()
		g.followSelection = true
		return true
	case tcell.KeyDown:
		g.selected++
		g.clampSelection()
		g.followSelection = true
		return true
	case tcell.KeyEnter:
		g.openDiff(g.selected)
		return true
	case tcell.KeyRune:
		switch e.Rune() {
		case 'j':
			g.selected++
			g.clampSelection()
			g.followSelection = true
			return true
		case 'k':
			g.selected--
			g.clampSelection()
			g.followSelection = true
			return true
		case 'r', 'R':
			g.Refresh()
			return true
		case 'o', 'O':
			g.openWorkingFile(g.selected)
			return true
		}
	}
	return false
}

// joinPath joins root and rel with POSIX semantics when host is a remote
// target (repoRoot/rel are always remote paths there, regardless of the
// local OS), or OS-native semantics when working locally.
func joinPath(host, root, rel string) string {
	if host != "" {
		return path.Join(root, rel)
	}
	return filepath.Join(root, rel)
}

// openWorkingFile opens the selected file directly in the editor (not the
// diff view) - handy once you already know what changed and just want to
// go fix it.
func (g *GitView) openWorkingFile(idx int) {
	if idx < 0 || idx >= len(g.files) {
		return
	}
	full := joinPath(g.host, g.repoRoot, g.files[idx].Path)
	if g.host != "" {
		openRemoteFileInEditor(g.host, full)
		return
	}
	openFileInEditor(full)
}

// openDiff opens a side-by-side HEAD-vs-working-tree diff for the file at
// idx: a new tab with the HEAD content on the left and the current working
// tree content (which may be unsaved-on-disk, staged, or both) on the
// right, both read-only scratch buffers.
func (g *GitView) openDiff(idx int) {
	if idx < 0 || idx >= len(g.files) {
		return
	}
	f := g.files[idx]
	full := joinPath(g.host, g.repoRoot, f.Path)

	head, err := git.Show(g.host, g.repoRoot, f.Path)
	if err != nil {
		InfoBar.Error(err)
		return
	}

	var working string
	var readErr error
	if g.host != "" {
		var data []byte
		data, readErr = remote.ReadFile(g.host, full)
		working = string(data)
	} else {
		var data []byte
		data, readErr = os.ReadFile(full)
		working = string(data)
	}
	if readErr != nil && f.Code != 'D' {
		InfoBar.Error(readErr)
		return
	}

	diffViewSeq++
	seq := strconv.Itoa(diffViewSeq)

	leftBuf := buffer.NewBufferFromString(head, path.Join("diff-"+seq, "HEAD", f.Path), btDiffView)
	leftBuf.SetName(f.Path + " (HEAD)")
	rightBuf := buffer.NewBufferFromString(working, path.Join("diff-"+seq, "working-tree", f.Path), btDiffView)
	rightBuf.SetName(f.Path + " (working tree, " + git.StatusName(f.Code) + ")")

	w, h := screen.Screen.Size()
	iOffset := config.GetInfoBarOffset()
	tp := NewTabFromBuffer(0, 0, w, h-iOffset, leftBuf)
	tp.IsDiffView = true
	Tabs.AddTab(tp)
	Tabs.SetActive(len(Tabs.List) - 1)

	if bp := tp.CurPane(); bp != nil {
		bp.VSplitIndex(rightBuf, true)
	}
	FocusedRegion = RegionEditor
}

// ToggleGit toggles the Git view in the sidebar (Alt-6).
func (h *BufPane) ToggleGit() bool {
	Sidebar.Toggle(SidebarGitView)
	return true
}

// GitCmd is the `git` command: toggles the Git sidebar view.
func (h *BufPane) GitCmd(args []string) {
	h.ToggleGit()
}
