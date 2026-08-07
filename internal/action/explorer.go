package action

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/config"
	"github.com/micro-editor/micro/v2/internal/remote"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/tcell/v2"
)

// explorerEntry is a single row in the flattened file tree.
type explorerEntry struct {
	path     string
	name     string
	depth    int
	isDir    bool
	expanded bool
}

// explorerMenuItem is a single action in a folder row's right-click
// context menu.
type explorerMenuItem struct {
	label string
	run   func()
}

// Explorer is the file/folder browser sidebar view. When remoteTarget is
// non-empty, it browses that SSH host's filesystem (rooted at `root`,
// a remote path) instead of the local one.
type Explorer struct {
	root         string
	remoteTarget string // SSH destination, e.g. "user@host"; "" = local

	expanded map[string]bool
	flat     []explorerEntry

	// selected indexes into flat, or -1 for the fixed ".." (up) row shown
	// above the tree whenever root has a parent.
	selected int
	scroll   int
	// followSelection is set whenever the selection changes via the
	// keyboard, so Display scrolls it into view exactly once; mouse-wheel
	// scrolling never touches the selection, so it isn't affected.
	followSelection bool

	lastClickRow  int
	lastClickTime time.Time

	// context menu (opened via right-click or the 'm' key on a folder row)
	menuRow      int // index into flat this menu belongs to; -1 = closed
	menuItems    []explorerMenuItem
	menuSel      int
	menuStartRow int // row local to the tree area the menu starts on
	menuWidth    int
}

// NewExplorer creates a new Explorer rooted at the current working directory.
func NewExplorer() *Explorer {
	e := new(Explorer)
	e.menuRow = -1
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	e.root = wd
	e.expanded = map[string]bool{wd: true}
	e.rebuild()
	return e
}

// SetRemote points the Explorer at a directory on a remote SSH host.
func (e *Explorer) SetRemote(target, root string) {
	e.closeMenu()
	e.remoteTarget = target
	e.root = root
	e.expanded = map[string]bool{root: true}
	e.selected, e.scroll = 0, 0
	e.rebuild()
}

// SetLocal switches the Explorer back to browsing the local filesystem at
// the current working directory.
func (e *Explorer) SetLocal() {
	e.closeMenu()
	e.remoteTarget = ""
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	e.root = wd
	e.expanded = map[string]bool{wd: true}
	e.selected, e.scroll = 0, 0
	e.rebuild()
}

// showUpRow reports whether root has a parent directory to navigate to,
// i.e. whether the fixed ".." row should be shown above the tree.
func (e *Explorer) showUpRow() bool {
	return e.dirOf(e.root) != e.root
}

// navigateUp re-roots the Explorer at root's parent directory. For a
// local explorer this also changes micro's working directory (kept in
// sync with the Explorer's root, same as Alt-5); for a remote explorer it
// only moves the remote browse position, no local state changes.
func (e *Explorer) navigateUp() {
	parent := e.dirOf(e.root)
	if parent == e.root {
		return
	}
	if e.isRemote() {
		target := e.remoteTarget
		e.SetRemote(target, parent)
		if Remote != nil {
			Remote.Path = parent
		}
		Sidebar.git.Invalidate()
		return
	}
	if bp := MainTab().CurPane(); bp != nil {
		bp.CdCmd([]string{parent})
	} else if err := os.Chdir(parent); err != nil {
		InfoBar.Error(err)
		return
	}
	e.SetLocal()
	Sidebar.git.Invalidate()
}

// openFolderHere re-roots the Explorer (and, if local, micro's working
// directory) at path - the "Open folder here" context-menu action.
func (e *Explorer) openFolderHere(path string) {
	if e.isRemote() {
		target := e.remoteTarget
		e.SetRemote(target, path)
		if Remote != nil {
			Remote.Path = path
		}
		Sidebar.git.Invalidate()
		return
	}
	changeWorkDir(path)
}

func (e *Explorer) isRemote() bool { return e.remoteTarget != "" }

// join/dirOf do path arithmetic with POSIX semantics for remote paths
// (which are always POSIX regardless of the local OS) or OS-native
// semantics for local paths.
func (e *Explorer) join(a, b string) string {
	if e.isRemote() {
		return remote.Join(a, b)
	}
	return filepath.Join(a, b)
}

func (e *Explorer) dirOf(p string) string {
	if e.isRemote() {
		return remote.Dir(p)
	}
	return filepath.Dir(p)
}

func (e *Explorer) Title() string {
	if e.isRemote() {
		return "EXPLORER (" + e.remoteTarget + nestSuffix() + ")"
	}
	return "EXPLORER"
}

// rebuild recomputes the flattened entry list from the expanded-state map.
func (e *Explorer) rebuild() {
	e.flat = e.flat[:0]
	e.walk(e.root, 0)
}

func (e *Explorer) walk(dir string, depth int) {
	if e.isRemote() {
		entries, err := remote.ListDir(e.remoteTarget, dir)
		if err != nil {
			InfoBar.Error(err)
			return
		}
		for _, ent := range entries {
			p := path.Join(dir, ent.Name)
			exp := ent.IsDir && e.expanded[p]
			e.flat = append(e.flat, explorerEntry{path: p, name: ent.Name, depth: depth, isDir: ent.IsDir, expanded: exp})
			if exp {
				e.walk(p, depth+1)
			}
		}
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	dirs := make([]os.DirEntry, 0)
	files := make([]os.DirEntry, 0)
	for _, ent := range entries {
		if ent.IsDir() {
			dirs = append(dirs, ent)
		} else {
			files = append(files, ent)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name()) < strings.ToLower(dirs[j].Name()) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name()) < strings.ToLower(files[j].Name()) })

	for _, ent := range dirs {
		p := filepath.Join(dir, ent.Name())
		exp := e.expanded[p]
		e.flat = append(e.flat, explorerEntry{path: p, name: ent.Name(), depth: depth, isDir: true, expanded: exp})
		if exp {
			e.walk(p, depth+1)
		}
	}
	for _, ent := range files {
		p := filepath.Join(dir, ent.Name())
		e.flat = append(e.flat, explorerEntry{path: p, name: ent.Name(), depth: depth, isDir: false})
	}
}

func (e *Explorer) Refresh() {
	e.closeMenu()
	if e.selected >= len(e.flat) {
		e.selected = len(e.flat) - 1
	}
	e.rebuild()
}

func (e *Explorer) clampSelection() {
	min := 0
	if e.showUpRow() {
		min = -1
	}
	if e.selected < min {
		e.selected = min
	}
	if e.selected >= len(e.flat) {
		e.selected = len(e.flat) - 1
	}
}

// scrollToSelected snaps scroll so the selected row is visible. Only call
// this right after the selection changed (e.g. via keyboard navigation) -
// calling it unconditionally from Display() would fight mouse-wheel
// scrolling, since a wheel scroll doesn't move the selection and would
// otherwise get snapped straight back to wherever the selection is.
func (e *Explorer) scrollToSelected(h int) {
	if e.selected < e.scroll {
		e.scroll = e.selected
	}
	if e.selected >= e.scroll+h {
		e.scroll = e.selected - h + 1
	}
	if e.scroll < 0 {
		e.scroll = 0
	}
}

// clampScroll keeps scroll within the valid range for the current content
// height, without regard to where the selection is. This is what Display
// calls every frame, so wheel-scrolling (which only changes scroll, not
// the selection) isn't undone on the next redraw.
func (e *Explorer) clampScroll(h int) {
	max := len(e.flat) - h
	if max < 0 {
		max = 0
	}
	if e.scroll > max {
		e.scroll = max
	}
	if e.scroll < 0 {
		e.scroll = 0
	}
}

func (e *Explorer) Display(x, y, w, h int) {
	e.clampSelection()

	style := config.DefStyle
	dirStyle := style
	if s, ok := config.Colorscheme["constant"]; ok {
		dirStyle = s
	}

	treeY, treeH := y, h
	if e.showUpRow() {
		st := style
		if e.selected == -1 {
			st = st.Reverse(true)
		}
		label := ".. (up a folder)"
		runes := []rune(label)
		for col := 0; col < w; col++ {
			r := ' '
			if col < len(runes) {
				r = runes[col]
			}
			screen.SetContent(x+col, y, r, nil, st)
		}
		treeY++
		treeH--
	}

	if e.followSelection {
		e.scrollToSelected(treeH)
		e.followSelection = false
	} else {
		e.clampScroll(treeH)
	}

	for row := 0; row < treeH; row++ {
		idx := e.scroll + row
		for col := 0; col < w; col++ {
			screen.SetContent(x+col, treeY+row, ' ', nil, style)
		}
		if idx < 0 || idx >= len(e.flat) {
			continue
		}
		ent := e.flat[idx]

		st := style
		if ent.isDir {
			st = dirStyle
		}
		if idx == e.selected {
			// Reverse (rather than swap in an unrelated style) so the
			// selection always has readable contrast against whatever
			// foreground/background this row already had, regardless of
			// the active colorscheme or terminal theme.
			st = st.Reverse(true)
		}

		col := ent.depth * 2
		if col >= w {
			continue
		}

		prefix := "  "
		if ent.isDir {
			if ent.expanded {
				prefix = "v "
			} else {
				prefix = "> "
			}
		}
		label := prefix + ent.name
		runes := []rune(label)
		for i := 0; i < len(runes) && col+i < w; i++ {
			screen.SetContent(x+col+i, treeY+row, runes[i], nil, st)
		}
		// paint rest of the selected row
		if idx == e.selected {
			for c := col + len(runes); c < w; c++ {
				screen.SetContent(x+c, treeY+row, ' ', nil, st)
			}
		}
	}

	e.displayMenu(x, treeY, w, treeH)
}

// displayMenu draws the right-click context menu for a folder row, if one
// is open, as a small overlay directly below (or, if there's no room,
// above) the row it belongs to. It records the local row range/width it
// occupies (relative to the tree area) so HandleClick can hit-test
// against it.
func (e *Explorer) displayMenu(x, y, w, h int) {
	if e.menuRow < 0 || e.menuRow < e.scroll || e.menuRow >= e.scroll+h {
		e.menuStartRow, e.menuWidth = -1, 0
		return
	}

	maxLabelLen := 0
	for _, it := range e.menuItems {
		if l := len([]rune(it.label)); l > maxLabelLen {
			maxLabelLen = l
		}
	}
	menuW := maxLabelLen + 2
	if menuW > w {
		menuW = w
	}

	anchorRow := e.menuRow - e.scroll
	startRow := anchorRow + 1
	if startRow+len(e.menuItems) > h {
		startRow = anchorRow - len(e.menuItems)
		if startRow < 0 {
			startRow = 0
		}
	}

	menuStyle := config.DefStyle
	if s, ok := config.Colorscheme["statusline"]; ok {
		menuStyle = s
	}

	for i, it := range e.menuItems {
		row := startRow + i
		if row < 0 || row >= h {
			continue
		}
		st := menuStyle
		if i == e.menuSel {
			st = st.Reverse(true)
		}
		label := " " + it.label
		runes := []rune(label)
		for c := 0; c < menuW; c++ {
			r := ' '
			if c < len(runes) {
				r = runes[c]
			}
			screen.SetContent(x+c, y+row, r, nil, st)
		}
	}

	e.menuStartRow, e.menuWidth = startRow, menuW
}

func (e *Explorer) toggleDir(idx int) {
	if idx < 0 || idx >= len(e.flat) || !e.flat[idx].isDir {
		return
	}
	p := e.flat[idx].path
	e.expanded[p] = !e.expanded[p]
	e.rebuild()
}

func (e *Explorer) openEntry(idx int) {
	if idx == -1 {
		e.navigateUp()
		return
	}
	if idx < 0 || idx >= len(e.flat) {
		return
	}
	ent := e.flat[idx]
	if ent.isDir {
		e.toggleDir(idx)
		return
	}
	if e.isRemote() {
		openRemoteFileInEditor(e.remoteTarget, ent.path)
	} else {
		openFileInEditor(ent.path)
	}
}

func (e *Explorer) HandleClick(x, y int, button tcell.ButtonMask) {
	if e.showUpRow() {
		if y == 0 {
			if button != tcell.ButtonSecondary {
				e.selected = -1
				e.navigateUp()
			}
			return
		}
		y--
	}

	if e.menuRow >= 0 {
		if y >= e.menuStartRow && y < e.menuStartRow+len(e.menuItems) && x < e.menuWidth {
			item := e.menuItems[y-e.menuStartRow]
			e.closeMenu()
			item.run()
		} else {
			e.closeMenu()
		}
		return
	}

	if button == tcell.ButtonSecondary {
		e.openMenu(e.scroll + y)
		return
	}

	idx := e.scroll + y
	if idx < 0 || idx >= len(e.flat) {
		return
	}
	now := time.Now()
	double := idx == e.lastClickRow && time.Since(e.lastClickTime)/time.Millisecond < config.DoubleClickThreshold
	e.lastClickRow = idx
	e.lastClickTime = now

	e.selected = idx

	if e.flat[idx].isDir {
		e.toggleDir(idx)
	} else if double {
		if e.isRemote() {
			openRemoteFileInEditor(e.remoteTarget, e.flat[idx].path)
		} else {
			openFileInEditor(e.flat[idx].path)
		}
	}
}

// openMenu opens the right-click/'m'-key context menu for the folder row
// at idx; does nothing if idx isn't a folder row.
func (e *Explorer) openMenu(idx int) {
	items := e.buildMenuItems(idx)
	if len(items) == 0 {
		return
	}
	e.selected = idx
	e.menuRow = idx
	e.menuItems = items
	e.menuSel = 0
}

func (e *Explorer) closeMenu() {
	e.menuRow = -1
	e.menuItems = nil
	e.menuSel = 0
}

// buildMenuItems returns the context-menu actions for the row at idx -
// currently just "open folder here" for folder rows, nothing for files.
func (e *Explorer) buildMenuItems(idx int) []explorerMenuItem {
	if idx < 0 || idx >= len(e.flat) || !e.flat[idx].isDir {
		return nil
	}
	p := e.flat[idx].path
	return []explorerMenuItem{
		{label: "Open folder here", run: func() { e.openFolderHere(p) }},
		{label: "Cancel", run: func() {}},
	}
}

func (e *Explorer) HandleWheel(up bool) {
	if up {
		e.scroll -= 3
	} else {
		e.scroll += 3
	}
	if e.scroll < 0 {
		e.scroll = 0
	}
}

func (e *Explorer) HandleKey(ev *tcell.EventKey) bool {
	if e.menuRow >= 0 {
		switch ev.Key() {
		case tcell.KeyUp:
			e.menuSel--
			if e.menuSel < 0 {
				e.menuSel = len(e.menuItems) - 1
			}
		case tcell.KeyDown:
			e.menuSel++
			if e.menuSel >= len(e.menuItems) {
				e.menuSel = 0
			}
		case tcell.KeyEnter:
			item := e.menuItems[e.menuSel]
			e.closeMenu()
			item.run()
		default:
			e.closeMenu()
		}
		return true
	}

	switch ev.Key() {
	case tcell.KeyUp:
		e.selected--
		e.clampSelection()
		e.followSelection = true
		return true
	case tcell.KeyDown:
		e.selected++
		e.clampSelection()
		e.followSelection = true
		return true
	case tcell.KeyEnter:
		e.openEntry(e.selected)
		return true
	case tcell.KeyLeft:
		if e.selected >= 0 && e.selected < len(e.flat) {
			ent := e.flat[e.selected]
			if ent.isDir && ent.expanded {
				e.toggleDir(e.selected)
			} else if ent.depth > 0 {
				// jump to parent
				for i := e.selected - 1; i >= 0; i-- {
					if e.flat[i].depth < ent.depth {
						e.selected = i
						e.followSelection = true
						break
					}
				}
			}
		}
		return true
	case tcell.KeyRight:
		if e.selected >= 0 && e.selected < len(e.flat) {
			ent := e.flat[e.selected]
			if ent.isDir && !ent.expanded {
				e.toggleDir(e.selected)
			}
		}
		return true
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'j':
			e.selected++
			e.clampSelection()
			e.followSelection = true
			return true
		case 'k':
			e.selected--
			e.clampSelection()
			e.followSelection = true
			return true
		case 'r', 'R':
			if ev.Rune() == 'R' {
				e.startRename()
			} else {
				e.Refresh()
			}
			return true
		case 'n':
			e.startCreate(false)
			return true
		case 'N':
			e.startCreate(true)
			return true
		case 'd', 'D':
			e.startDelete()
			return true
		case 'm', 'M':
			e.openMenu(e.selected)
			return true
		}
	}
	return false
}

func (e *Explorer) targetDir() string {
	if e.selected < 0 || e.selected >= len(e.flat) {
		return e.root
	}
	ent := e.flat[e.selected]
	if ent.isDir {
		return ent.path
	}
	return e.dirOf(ent.path)
}

func (e *Explorer) startCreate(dir bool) {
	base := e.targetDir()
	prompt := "New file name: "
	if dir {
		prompt = "New folder name: "
	}
	InfoBar.Prompt(prompt, "", "Command", nil, func(resp string, canceled bool) {
		if canceled || resp == "" {
			return
		}
		full := e.join(base, resp)
		var err error
		if e.isRemote() {
			if dir {
				err = remote.Mkdir(e.remoteTarget, full)
			} else {
				err = remote.CreateFile(e.remoteTarget, full)
			}
		} else if dir {
			err = os.MkdirAll(full, 0755)
		} else {
			var f *os.File
			f, err = os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if f != nil {
				f.Close()
			}
		}
		if err != nil {
			InfoBar.Error(err)
			return
		}
		e.expanded[base] = true
		e.Refresh()
	})
}

func (e *Explorer) startRename() {
	if e.selected < 0 || e.selected >= len(e.flat) {
		return
	}
	ent := e.flat[e.selected]
	InfoBar.Prompt("Rename to: ", ent.name, "Command", nil, func(resp string, canceled bool) {
		if canceled || resp == "" {
			return
		}
		newPath := e.join(e.dirOf(ent.path), resp)
		var err error
		if e.isRemote() {
			err = remote.Rename(e.remoteTarget, ent.path, newPath)
		} else {
			err = os.Rename(ent.path, newPath)
		}
		if err != nil {
			InfoBar.Error(err)
			return
		}
		e.Refresh()
	})
}

func (e *Explorer) startDelete() {
	if e.selected < 0 || e.selected >= len(e.flat) {
		return
	}
	ent := e.flat[e.selected]
	InfoBar.YNPrompt("Delete "+ent.name+"? (y,n,esc)", func(yes, canceled bool) {
		if canceled || !yes {
			return
		}
		var err error
		if e.isRemote() {
			err = remote.Remove(e.remoteTarget, ent.path)
		} else if ent.isDir {
			err = os.RemoveAll(ent.path)
		} else {
			err = os.Remove(ent.path)
		}
		if err != nil {
			InfoBar.Error(err)
			return
		}
		e.Refresh()
	})
}

// openFileInEditor opens the given file path as a new (or reused) file
// tab in the currently active editor pane, or a new tab if there is no
// active buffer pane (e.g. a term pane is focused). See FileTabs.
func openFileInEditor(path string) {
	b, err := buffer.NewBufferFromFile(path, buffer.BTDefault)
	if err != nil {
		InfoBar.Error(err)
		return
	}
	switchToFileBuffer(b)
}

// switchToFileBuffer opens b as a file tab in the active editor pane (see
// FileTabs), or in a new micro Tab if there is no active buffer pane.
func switchToFileBuffer(b *buffer.Buffer) {
	bp := MainTab().CurPane()
	if bp != nil {
		if MainTab().IsDiffView {
			// Unsplit only drops the pane from the split tree - it never
			// closes the buffer that pane was showing, and neither does
			// the SwitchBuffer FileTabs.Open below does to the survivor.
			// Left alone, both of the diff view's scratch buffers (one
			// with a live diffBase/async diff-update timer wired up)
			// leak forever in buffer.OpenBuffers. Close them explicitly
			// before tearing down the split.
			for _, p := range MainTab().Panes {
				if diffPane, ok := p.(*BufPane); ok {
					diffPane.Buf.Close()
				}
			}
			bp.Unsplit()
			MainTab().IsDiffView = false
			bp = MainTab().CurPane()
		}
		FileTabs.Open(bp, b)
	} else {
		w, h := screen.Screen.Size()
		iOffset := config.GetInfoBarOffset()
		tp := NewTabFromBuffer(0, 0, w, h-iOffset, b)
		Tabs.AddTab(tp)
		Tabs.SetActive(len(Tabs.List) - 1)
	}
	setFocusedRegion(RegionEditor)
}
