package action

import (
	"path/filepath"

	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/config"
	"github.com/micro-editor/micro/v2/internal/display"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/tcell/v2"
)

// DefaultFileTabsHeight is the height, in rows, of the recently-viewed
// file tab strip.
const DefaultFileTabsHeight = 1

// FileTab is a single entry in the file tab strip: a buffer that stays
// alive (and keeps its own cursor/scroll position) even while hidden
// behind another tab, VS Code-style.
type FileTab struct {
	buf *buffer.Buffer
}

func (t *FileTab) label() string {
	name := filepath.Base(t.buf.GetName())
	if name == "" || name == "." {
		name = "No name"
	}
	if t.buf.Modified() {
		return "● " + name // "● name"
	}
	return name
}

// FileTabStrip is the "recently viewed files" tab strip shown above the
// editor area: every file opened via the Explorer, `> open`, or Ctrl-o
// gets a closable tab here. Switching tabs never discards unsaved changes
// or loses your place in the file - the underlying buffer (and therefore
// its cursor/scroll position) is kept alive until its tab is explicitly
// closed. Like Sidebar/TermPanel, it is a fixed-region sibling of TabList,
// not part of any Tab's Node tree.
type FileTabStrip struct {
	*display.View

	id     uint64
	tabs   []*FileTab
	active int

	mousePressed bool
}

// FileTabs is the global file tab strip singleton.
var FileTabs *FileTabStrip

// InitFileTabs creates the global FileTabs singleton.
func InitFileTabs() {
	FileTabs = &FileTabStrip{View: new(display.View), active: -1}
}

func (ft *FileTabStrip) ID() uint64                               { return ft.id }
func (ft *FileTabStrip) SetID(i uint64)                           { ft.id = i }
func (ft *FileTabStrip) Name() string                             { return "File tabs" }
func (ft *FileTabStrip) Close()                                   {}
func (ft *FileTabStrip) SetTab(t *Tab)                            {}
func (ft *FileTabStrip) Tab() *Tab                                { return nil }
func (ft *FileTabStrip) Relocate() bool                           { return false }
func (ft *FileTabStrip) SetActive(b bool)                         {}
func (ft *FileTabStrip) IsActive() bool                           { return false }
func (ft *FileTabStrip) GetView() *display.View                   { return ft.View }
func (ft *FileTabStrip) SetView(v *display.View)                  { ft.View = v }
func (ft *FileTabStrip) LocFromVisual(vloc buffer.Loc) buffer.Loc { return vloc }
func (ft *FileTabStrip) HandleCommand(input string)               {}

// Contains reports whether the given absolute screen coordinate is inside
// the file tab strip's region.
func (ft *FileTabStrip) Contains(x, y int) bool {
	return len(ft.tabs) > 0 && x >= ft.X && x < ft.X+ft.Width && y >= ft.Y && y < ft.Y+ft.Height
}

// neededHeight returns how many rows the strip currently needs (0 when
// empty, so it takes up no space until at least one file has been opened).
func (ft *FileTabStrip) neededHeight() int {
	if len(ft.tabs) == 0 {
		return 0
	}
	return DefaultFileTabsHeight
}

func (ft *FileTabStrip) Resize(w, h int) {
	ft.Width, ft.Height = w, h
}

func (ft *FileTabStrip) resetMouse() {
	ft.mousePressed = false
}

// indexOf returns the index of the tab already displaying b (matched by
// AbsPath, so re-opening the same file reuses its tab), or -1.
func (ft *FileTabStrip) indexOf(b *buffer.Buffer) int {
	if b.AbsPath == "" {
		return -1
	}
	for i, t := range ft.tabs {
		if t.buf.AbsPath == b.AbsPath {
			return i
		}
	}
	return -1
}

// Open switches bp to b, adding a new tab for it (or reusing/activating
// its existing tab if b's file is already open).
func (ft *FileTabStrip) Open(bp *BufPane, b *buffer.Buffer) {
	if i := ft.indexOf(b); i != -1 {
		if ft.tabs[i].buf != b {
			b.Close()
		}
		ft.switchTo(bp, i)
		return
	}
	ft.tabs = append(ft.tabs, &FileTab{buf: b})
	ft.switchTo(bp, len(ft.tabs)-1)
}

func (ft *FileTabStrip) switchTo(bp *BufPane, i int) {
	if i < 0 || i >= len(ft.tabs) {
		return
	}
	ft.active = i
	if bp != nil {
		bp.SwitchBuffer(ft.tabs[i].buf)
	}
	FocusedRegion = RegionEditor
	Tabs.Resize()
}

// CloseTab closes the tab at index i. If it was the active tab, the pane
// switches to a neighboring tab, or a blank buffer if none are left.
func (ft *FileTabStrip) CloseTab(i int) {
	if i < 0 || i >= len(ft.tabs) {
		return
	}
	b := ft.tabs[i].buf
	wasActive := i == ft.active

	ft.tabs = append(ft.tabs[:i], ft.tabs[i+1:]...)

	switch {
	case len(ft.tabs) == 0:
		ft.active = -1
		blank := buffer.NewBufferFromString("", "", buffer.BTDefault)
		if bp := MainTab().CurPane(); bp != nil {
			bp.SwitchBuffer(blank)
		}
	case !wasActive && i < ft.active:
		ft.active--
	case wasActive:
		if ft.active >= len(ft.tabs) {
			ft.active = len(ft.tabs) - 1
		}
		if bp := MainTab().CurPane(); bp != nil {
			bp.SwitchBuffer(ft.tabs[ft.active].buf)
		}
	}

	b.Close()
	Tabs.Resize()
}

func (ft *FileTabStrip) tabLabels() []string {
	labels := make([]string, len(ft.tabs))
	for i, t := range ft.tabs {
		labels[i] = t.label()
	}
	return labels
}

// tabAtCol returns the index of the tab whose strip label contains column
// x, and whether x landed on that tab's close glyph.
func (ft *FileTabStrip) tabAtCol(x int) (idx int, onClose bool) {
	col := 0
	for i, label := range ft.tabLabels() {
		w := len([]rune(label)) + 4 // " label × " (leading space + label + " × ")
		if x >= col && x < col+w {
			return i, x-col == w-2
		}
		col += w
	}
	return -1, false
}

// Display draws the tab strip.
func (ft *FileTabStrip) Display() {
	if len(ft.tabs) == 0 || ft.Width <= 0 || ft.Height <= 0 {
		return
	}

	tabStyle := config.DefStyle
	if s, ok := config.Colorscheme["tabbar"]; ok {
		tabStyle = s
	}
	activeStyle := config.DefStyle.Reverse(true)
	if s, ok := config.Colorscheme["tabbar.active"]; ok {
		activeStyle = s
	}

	for x := 0; x < ft.Width; x++ {
		screen.SetContent(ft.X+x, ft.Y, ' ', nil, tabStyle)
	}

	col := 0
	for i, label := range ft.tabLabels() {
		text := " " + label + " × " // " label × "
		style := tabStyle
		if i == ft.active {
			style = activeStyle
		}
		for _, r := range text {
			if col >= ft.Width {
				break
			}
			screen.SetContent(ft.X+col, ft.Y, r, nil, style)
			col++
		}
		if col >= ft.Width {
			break
		}
	}
}

// HandleEvent handles a mouse event that landed on the file tab strip.
func (ft *FileTabStrip) HandleEvent(event tcell.Event) {
	e, ok := event.(*tcell.EventMouse)
	if !ok {
		return
	}
	mx, _ := e.Position()
	lx := mx - ft.X

	switch e.Buttons() {
	case tcell.Button1:
		idx, onClose := ft.tabAtCol(lx)
		if idx == -1 {
			return
		}
		ft.mousePressed = true
		if onClose {
			ft.CloseTab(idx)
		} else {
			ft.switchTo(MainTab().CurPane(), idx)
		}
	case tcell.ButtonNone:
		ft.mousePressed = false
	}
}
