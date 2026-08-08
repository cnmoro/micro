package action

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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

// gitDiffTarget is stashed in a diff view's working-tree buffer (via its
// Settings map, the only per-buffer store handy for this) so StageHunk
// (Alt-s) knows which file/repo/host a cursor position in that specific
// buffer refers to - and, since it's only ever set on the working-tree
// side (not the HEAD side), doubles as how StageHunk tells the two apart.
type gitDiffTarget struct {
	host     string
	repoRoot string
	relPath  string
}

// gitRowKind identifies what kind of row a gitRow represents.
type gitRowKind int

const (
	gitRowSection gitRowKind = iota
	gitRowFile
)

// gitRow is a single flattened, displayable row in the Git view: either a
// "STAGED CHANGES"/"CHANGES" section header, or one file under one of
// those sections. A file with both staged and unstaged changes gets two
// separate rows, one per section, matching VS Code's source control view.
type gitRow struct {
	kind   gitRowKind
	label  string // section headers only
	file   git.FileStatus
	staged bool // which side of file this row is - meaningless for section headers
}

// gitToolbarButton is a single clickable action in the fixed toolbar row.
type gitToolbarButton struct {
	label string
	run   func()
}

// GitView is the git-status sidebar view: a small toolbar (refresh,
// commit) followed by every changed file in the current repository - the
// local working directory, or (while an SSH session is active) the
// repository at the remote session's path - split into "Staged Changes"
// and "Changes" sections, VS Code-style, each file colored by status and
// with a +/- button to stage/unstage it. Clicking a file (rather than its
// +/- button) opens a side-by-side HEAD-vs-working-tree diff.
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
	rows  []gitRow

	selected        int
	scroll          int
	followSelection bool

	// width is the content width from the most recent Display() call,
	// needed by HandleClick (which doesn't receive it directly) to find
	// the +/- badge column.
	width int
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
			g.rebuildRows()
			g.clampSelection()
		}}
	}()
}

// rebuildRows splits g.files into the "Staged Changes"/"Changes" sections,
// flattened into g.rows the same way Explorer/Docker flatten their trees.
func (g *GitView) rebuildRows() {
	var staged, unstaged []git.FileStatus
	for _, f := range g.files {
		if f.StagedCode != 0 {
			staged = append(staged, f)
		}
		if f.UnstagedCode != 0 {
			unstaged = append(unstaged, f)
		}
	}

	g.rows = g.rows[:0]
	if len(staged) > 0 {
		g.rows = append(g.rows, gitRow{kind: gitRowSection, label: fmt.Sprintf("STAGED CHANGES (%d)", len(staged))})
		for _, f := range staged {
			g.rows = append(g.rows, gitRow{kind: gitRowFile, file: f, staged: true})
		}
	}
	if len(unstaged) > 0 {
		g.rows = append(g.rows, gitRow{kind: gitRowSection, label: fmt.Sprintf("CHANGES (%d)", len(unstaged))})
		for _, f := range unstaged {
			g.rows = append(g.rows, gitRow{kind: gitRowFile, file: f, staged: false})
		}
	}
}

func (g *GitView) clampSelection() {
	if g.selected < 0 {
		g.selected = 0
	}
	if g.selected >= len(g.rows) {
		g.selected = len(g.rows) - 1
	}
	// Section headers aren't actionable - if clamping (e.g. after a
	// refresh reshuffled the rows) landed on one, nudge to the nearest
	// file row instead of leaving a dead selection sitting there.
	for g.selected >= 0 && g.selected < len(g.rows) && g.rows[g.selected].kind == gitRowSection {
		if g.selected+1 < len(g.rows) {
			g.selected++
		} else {
			g.selected--
		}
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
	max := len(g.rows) - h
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

// toolbarButtons returns the toolbar's buttons in display order - shared
// by drawToolbar and toolbarButtonAt so their column math can't drift
// apart.
func (g *GitView) toolbarButtons() []gitToolbarButton {
	return []gitToolbarButton{
		{"↻ Refresh", g.Refresh},
		{"✓ Commit", g.promptCommit},
	}
}

func (g *GitView) toolbarButtonAt(x int) int {
	col := 0
	for i, b := range g.toolbarButtons() {
		label := " " + b.label + " "
		w := len([]rune(label))
		if x >= col && x < col+w {
			return i
		}
		col += w
	}
	return -1
}

func (g *GitView) drawToolbar(x, y, w int) {
	style := config.DefStyle
	if s, ok := config.Colorscheme["statusline"]; ok {
		style = s
	}
	col := 0
	for _, b := range g.toolbarButtons() {
		label := " " + b.label + " "
		for _, r := range label {
			if col >= w {
				break
			}
			screen.SetContent(x+col, y, r, nil, style)
			col++
		}
	}
	for ; col < w; col++ {
		screen.SetContent(x+col, y, ' ', nil, style)
	}
}

func (g *GitView) Display(x, y, w, h int) {
	g.width = w
	if !g.loaded && !g.loading {
		g.Refresh()
	}

	style := config.DefStyle
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			screen.SetContent(x+col, y+row, ' ', nil, style)
		}
	}

	if h <= 0 {
		return
	}
	g.drawToolbar(x, y, w)
	listY, listH := y+1, h-1
	if listH <= 0 {
		return
	}

	if g.loading && !g.loaded {
		drawWrapped(x, listY, w, listH, "Loading...", style)
		return
	}
	if !g.inRepo {
		msg := "Not a git repository: " + g.checkedDir
		if g.notRepoDetail != "" {
			msg += " (" + g.notRepoDetail + ")"
		}
		drawWrapped(x, listY, w, listH, msg, style)
		return
	}
	if g.errMsg != "" {
		drawWrapped(x, listY, w, listH, g.errMsg, style)
		return
	}
	if len(g.rows) == 0 {
		drawWrapped(x, listY, w, listH, "No changes", style)
		return
	}

	if g.followSelection {
		g.scrollToSelected(listH)
		g.followSelection = false
	} else {
		g.clampScroll(listH)
	}

	categoryStyle := style
	if s, ok := config.Colorscheme["constant"]; ok {
		categoryStyle = s
	}

	badgeCol := w - 1
	nameW := badgeCol - 2
	if nameW < 0 {
		nameW = 0
	}

	for row := 0; row < listH; row++ {
		idx := g.scroll + row
		if idx < 0 || idx >= len(g.rows) {
			continue
		}
		r := g.rows[idx]
		rowY := listY + row

		if r.kind == gitRowSection {
			runes := []rune(r.label)
			for i := 0; i < w; i++ {
				c := ' '
				if i < len(runes) {
					c = runes[i]
				}
				screen.SetContent(x+i, rowY, c, nil, categoryStyle)
			}
			continue
		}

		code := r.file.UnstagedCode
		if r.staged {
			code = r.file.StagedCode
		}
		st := statusStyle(code)
		if idx == g.selected {
			st = st.Reverse(true)
		}

		screen.SetContent(x, rowY, rune(code), nil, st)
		screen.SetContent(x+1, rowY, ' ', nil, st)

		runes := []rune(r.file.Path)
		for i := 0; i < nameW; i++ {
			c := ' '
			if i < len(runes) {
				c = runes[i]
			} else if idx != g.selected {
				break
			}
			screen.SetContent(x+2+i, rowY, c, nil, st)
		}

		if badgeCol > 1 {
			badge := '+'
			badgeStyle := statusStyle('A')
			if r.staged {
				badge = '-'
				badgeStyle = statusStyle('D')
			}
			if idx == g.selected {
				badgeStyle = badgeStyle.Reverse(true)
			}
			screen.SetContent(x+badgeCol, rowY, badge, nil, badgeStyle)
		}
	}
}

func (g *GitView) HandleClick(x, y int, button tcell.ButtonMask) {
	if y == 0 {
		if idx := g.toolbarButtonAt(x); idx >= 0 {
			g.toolbarButtons()[idx].run()
		}
		return
	}
	if !g.inRepo || len(g.rows) == 0 {
		return
	}
	idx := g.scroll + (y - 1)
	if idx < 0 || idx >= len(g.rows) {
		return
	}
	if g.rows[idx].kind != gitRowFile {
		return
	}
	g.selected = idx

	if x == g.width-1 {
		g.toggleStageRow(idx)
		return
	}
	g.openDiff(g.rows[idx].file)
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

	moveSel := func(delta int) {
		next := g.selected + delta
		// Skip over section headers - they're not actionable, so landing
		// on one just to have to press the same key again is friction
		// for no benefit.
		for next >= 0 && next < len(g.rows) && g.rows[next].kind == gitRowSection {
			next += delta
		}
		if next < 0 || next >= len(g.rows) {
			return
		}
		g.selected = next
		g.followSelection = true
	}

	switch e.Key() {
	case tcell.KeyUp:
		moveSel(-1)
		return true
	case tcell.KeyDown:
		moveSel(1)
		return true
	case tcell.KeyEnter:
		g.openSelected()
		return true
	case tcell.KeyRune:
		switch e.Rune() {
		case 'j':
			moveSel(1)
			return true
		case 'k':
			moveSel(-1)
			return true
		case 'r', 'R':
			g.Refresh()
			return true
		case 'o', 'O':
			g.openWorkingFile(g.selected)
			return true
		case 's', 'S', 'u', 'U':
			g.toggleStageRow(g.selected)
			return true
		case 'c', 'C':
			g.promptCommit()
			return true
		}
	}
	return false
}

func (g *GitView) openSelected() {
	if g.selected < 0 || g.selected >= len(g.rows) || g.rows[g.selected].kind != gitRowFile {
		return
	}
	g.openDiff(g.rows[g.selected].file)
}

// toggleStageRow stages the file at idx if that row is on the unstaged
// side, or unstages it if it's on the staged side.
func (g *GitView) toggleStageRow(idx int) {
	if idx < 0 || idx >= len(g.rows) || g.rows[idx].kind != gitRowFile {
		return
	}
	r := g.rows[idx]
	if r.staged {
		g.unstageFile(r.file.Path)
	} else {
		g.stageFile(r.file.Path)
	}
}

func (g *GitView) stageFile(relPath string) {
	host, root := g.host, g.repoRoot
	sp := StartSpinner("Staging " + relPath + "...")
	go func() {
		err := git.Stage(host, root, relPath)
		shell.Jobs <- shell.JobFunction{Function: func(string, []any) {
			sp.Stop()
			if err != nil {
				InfoBar.Error(err)
			}
			g.Refresh()
		}}
	}()
}

func (g *GitView) unstageFile(relPath string) {
	host, root := g.host, g.repoRoot
	sp := StartSpinner("Unstaging " + relPath + "...")
	go func() {
		err := git.Unstage(host, root, relPath)
		shell.Jobs <- shell.JobFunction{Function: func(string, []any) {
			sp.Stop()
			if err != nil {
				InfoBar.Error(err)
			}
			g.Refresh()
		}}
	}()
}

// promptCommit asks for a commit message (Enter to confirm, Esc to
// cancel) and commits the currently staged changes.
func (g *GitView) promptCommit() {
	if !g.inRepo {
		return
	}
	InfoBar.Prompt("Commit message: ", "", "Command", nil, func(resp string, canceled bool) {
		if canceled || strings.TrimSpace(resp) == "" {
			return
		}
		g.commit(resp)
	})
}

func (g *GitView) commit(message string) {
	host, root := g.host, g.repoRoot
	sp := StartSpinner("Committing...")
	go func() {
		err := git.Commit(host, root, message)
		shell.Jobs <- shell.JobFunction{Function: func(string, []any) {
			sp.Stop()
			if err != nil {
				InfoBar.Error(err)
			} else {
				InfoBar.Message("Committed: " + message)
			}
			g.Refresh()
		}}
	}()
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
	if idx < 0 || idx >= len(g.rows) || g.rows[idx].kind != gitRowFile {
		return
	}
	full := joinPath(g.host, g.repoRoot, g.rows[idx].file.Path)
	if g.host != "" {
		openRemoteFileInEditor(g.host, full)
		return
	}
	openFileInEditor(full)
}

// primaryCode picks whichever status is more relevant to show for a diff
// (the unstaged one, since that's what the working-tree pane displays -
// falling back to the staged one for a file that's fully staged with no
// further unstaged changes).
func primaryCode(f git.FileStatus) byte {
	if f.UnstagedCode != 0 {
		return f.UnstagedCode
	}
	return f.StagedCode
}

// openDiff opens a side-by-side HEAD-vs-working-tree diff for f: a new tab
// with the HEAD content on the left and the current working tree content
// (which may be unsaved-on-disk, staged, or both) on the right, both
// read-only scratch buffers.
func (g *GitView) openDiff(f git.FileStatus) {
	full := joinPath(g.host, g.repoRoot, f.Path)

	head, err := git.Show(g.host, g.repoRoot, f.Path)
	if err != nil {
		InfoBar.Error(err)
		return
	}

	code := primaryCode(f)
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
	if readErr != nil && code != 'D' {
		InfoBar.Error(readErr)
		return
	}

	diffViewSeq++
	seq := strconv.Itoa(diffViewSeq)

	leftBuf := buffer.NewBufferFromString(head, path.Join("diff-"+seq, "HEAD", f.Path), btDiffView)
	leftBuf.SetName(f.Path + " (HEAD)")
	rightBuf := buffer.NewBufferFromString(working, path.Join("diff-"+seq, "working-tree", f.Path), btDiffView)
	rightBuf.SetName(f.Path + " (working tree, " + git.StatusName(code) + ")")
	rightBuf.Settings["gitDiffTarget"] = &gitDiffTarget{host: g.host, repoRoot: g.repoRoot, relPath: f.Path}

	// Highlight which lines actually changed, reusing the same
	// diffBase/diffgutter mechanism micro's own gutter git-diff markers
	// use elsewhere (see diff-added/diff-modified/diff-deleted in
	// bufwindow.go) - diffing the working-tree buffer against the HEAD
	// content just displayed on the left. diffgutter defaults to off
	// globally, so force it on for just this buffer rather than depending
	// on the user's own settings.
	rightBuf.Settings["diffgutter"] = true
	rightBuf.SetDiffBase([]byte(head))
	rightBuf.UpdateDiff()

	w, h := screen.Screen.Size()
	iOffset := config.GetInfoBarOffset()
	tp := NewTabFromBuffer(0, 0, w, h-iOffset, leftBuf)
	tp.IsDiffView = true
	Tabs.AddTab(tp)
	Tabs.SetActive(len(Tabs.List) - 1)

	if bp := tp.CurPane(); bp != nil {
		bp.VSplitIndex(rightBuf, true)
	}
	setFocusedRegion(RegionEditor)

	// Opening another file from the Explorer while this tab is active
	// collapses it automatically, but there's no other visible way to
	// dismiss it (this is a plain tab, and micro's tab bar has no close
	// button) - so say so explicitly, once, right when it'd matter.
	InfoBar.Message("Diff view opened - Alt-s stages the hunk at the cursor, Ctrl-q closes the view")
}

// StageHunk stages just the change block (hunk) at the cursor's line, when
// the current pane is the working-tree side of a Git-panel diff view
// (Alt-s) - a no-op (returning false) everywhere else, including the
// HEAD side of the same diff view.
func (h *BufPane) StageHunk() bool {
	target, ok := h.Buf.Settings["gitDiffTarget"].(*gitDiffTarget)
	if !ok {
		return false
	}
	line := h.Cursor.Y + 1
	sp := StartSpinner("Staging hunk...")
	go func() {
		staged, err := git.StageHunkAtLine(target.host, target.repoRoot, target.relPath, line)
		shell.Jobs <- shell.JobFunction{Function: func(string, []any) {
			sp.Stop()
			if err != nil {
				InfoBar.Error(err)
			} else if !staged {
				InfoBar.Message("No change block at the cursor to stage")
			} else {
				InfoBar.Message("Staged hunk in " + target.relPath)
			}
			if Sidebar.active == SidebarGitView {
				Sidebar.git.Refresh()
			} else {
				Sidebar.git.Invalidate()
			}
		}}
	}()
	return true
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
