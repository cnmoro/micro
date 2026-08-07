package action

import (
	"fmt"
	"time"

	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/config"
	"github.com/micro-editor/micro/v2/internal/display"
	"github.com/micro-editor/micro/v2/internal/remote"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/micro/v2/internal/shell"
	"github.com/micro-editor/tcell/v2"
)

// DefaultTermPanelHeight is the default height (in rows, including the tab
// strip) of the bottom terminal panel.
const DefaultTermPanelHeight = 14

// minTermPanelHeight is the shortest the terminal panel can be dragged to
// (tab strip plus a few rows of content).
const minTermPanelHeight = 4

// PanelTerm is a single terminal session living inside the TerminalPanel.
type PanelTerm struct {
	name   string
	term   *shell.Terminal
	win    *display.TermWindow
	closed bool
}

// TerminalPanel is the bottom terminal panel: a tab strip plus a set of
// independently running shells, one of which is active/visible at a time.
// Like SidebarPane, it is a fixed-region sibling of TabList/InfoBar and is
// not part of any Tab's Node tree.
type TerminalPanel struct {
	*display.View

	id      uint64
	visible bool
	height  int

	tabs   []*PanelTerm
	active int

	mousePressed bool
	resizing     bool
	resizeBottom int // tp.Y+tp.Height captured when a drag-resize starts

	// lastKeyTime/lastKeyRune implement the "double leader key" combos
	// (mirroring the <Ctrl-x><Ctrl-x> convention used by TermBindings)
	// used for panel-level actions like new/close/rename tab so that a
	// single Ctrl combo still reaches the shell running inside.
	pendingLeader rune
	pendingSince  time.Time
}

// TermPanel is the global terminal panel singleton.
var TermPanel *TerminalPanel

// InitTermPanel creates the global TermPanel singleton (without opening any
// terminal tabs yet).
func InitTermPanel() {
	tp := new(TerminalPanel)
	tp.View = new(display.View)
	tp.height = DefaultTermPanelHeight
	TermPanel = tp
}

func (tp *TerminalPanel) ID() uint64                               { return tp.id }
func (tp *TerminalPanel) SetID(i uint64)                           { tp.id = i }
func (tp *TerminalPanel) Name() string                             { return "Terminal" }
func (tp *TerminalPanel) Close()                                   {}
func (tp *TerminalPanel) SetTab(t *Tab)                            {}
func (tp *TerminalPanel) Tab() *Tab                                { return nil }
func (tp *TerminalPanel) Relocate() bool                           { return false }
func (tp *TerminalPanel) SetActive(b bool)                         {}
func (tp *TerminalPanel) IsActive() bool                           { return FocusedRegion == RegionTermPanel }
func (tp *TerminalPanel) GetView() *display.View                   { return tp.View }
func (tp *TerminalPanel) SetView(v *display.View)                  { tp.View = v }
func (tp *TerminalPanel) LocFromVisual(vloc buffer.Loc) buffer.Loc { return vloc }
func (tp *TerminalPanel) HandleCommand(input string)               {}

// Contains reports whether the given absolute screen coordinate is inside
// the terminal panel's region.
func (tp *TerminalPanel) Contains(x, y int) bool {
	return tp.visible && x >= tp.X && x < tp.X+tp.Width && y >= tp.Y && y < tp.Y+tp.Height
}

func (tp *TerminalPanel) Visible() bool { return tp.visible }

// Show makes the terminal panel visible, opening a first tab if none exist.
func (tp *TerminalPanel) Show() {
	if len(tp.tabs) == 0 {
		tp.NewTab("")
		if len(tp.tabs) == 0 {
			// NewTab failed (e.g. unsupported platform, already reported
			// to the info bar) - don't show an empty, unusable panel.
			return
		}
	}
	tp.visible = true
	Tabs.Resize()
	setFocusedRegion(RegionTermPanel)
}

func (tp *TerminalPanel) Hide() {
	tp.visible = false
	if FocusedRegion == RegionTermPanel {
		setFocusedRegion(RegionEditor)
	}
	Tabs.Resize()
}

func (tp *TerminalPanel) Toggle() {
	if tp.visible {
		tp.Hide()
		return
	}
	tp.Show()
}

// Resize sets the terminal panel's absolute geometry and propagates it to
// the content area of every open terminal tab. Called from TabList.Resize.
func (tp *TerminalPanel) Resize(w, h int) {
	tp.Width, tp.Height = w, h
	for _, t := range tp.tabs {
		if t.win != nil {
			t.win.SetView(&display.View{X: tp.X, Y: tp.Y + 1, Width: w, Height: h - 1})
			t.win.Resize(w, h-1)
		}
	}
}

func (tp *TerminalPanel) Clear() {
	for y := 0; y < tp.Height; y++ {
		for x := 0; x < tp.Width; x++ {
			screen.SetContent(tp.X+x, tp.Y+y, ' ', nil, config.DefStyle)
		}
	}
}

func (tp *TerminalPanel) resetMouse() {
	tp.mousePressed = false
	tp.resizing = false
}

// NewTab opens a new shell and adds it as a tab; if name is empty a default
// "Terminal N" name is used. While an SSH session is active, this opens
// another remote shell on that host (matching VS Code Remote-SSH's "new
// terminal" behavior) instead of a local one - a plain local shell is very
// rarely what's wanted here, since it can't reach the remote files/Docker
// the rest of the UI is currently pointed at.
func (tp *TerminalPanel) NewTab(name string) {
	if Remote != nil {
		if name == "" {
			name = "ssh:" + Remote.Target
		}
		remoteShell := "cd " + remote.Quote(Remote.Path) + " 2>/dev/null; exec \"${SHELL:-/bin/sh}\" -l"
		tp.NewTabWithCommand(name, []string{"ssh", "-t", Remote.Target, remoteShell})
		return
	}
	tp.NewTabWithCommand(name, defaultShellArgs())
}

// NewTabWithCommand opens a new tab running the given argv (e.g. `docker
// logs -f <id>`) instead of the user's shell; if name is empty a default
// "Terminal N" name is used.
func (tp *TerminalPanel) NewTabWithCommand(name string, argv []string) {
	if !TermEmuSupported {
		InfoBar.Error("The integrated terminal isn't available on this platform (no PTY support here yet - this affects Windows). SSH file browsing and Docker management still work; use an external terminal for an interactive remote shell.")
		return
	}
	t := new(shell.Terminal)
	if err := t.Start(argv, false, true, nil, nil); err != nil {
		InfoBar.Error(err)
		return
	}
	if name == "" {
		name = fmt.Sprintf("Terminal %d", len(tp.tabs)+1)
	}

	w := 1
	h := 1
	if tp.Width > 0 {
		w = tp.Width
	}
	if tp.Height > 1 {
		h = tp.Height - 1
	}
	win := display.NewTermWindow(tp.X, tp.Y+1, w, h, t)
	win.SetActive(true)

	pt := &PanelTerm{name: name, term: t, win: win}
	tp.tabs = append(tp.tabs, pt)
	tp.active = len(tp.tabs) - 1
}

// stopTerm forcibly ends a panel terminal's shell process by closing its
// pty, matching what shell.Terminal.Stop does on natural exit.
func stopTerm(t *shell.Terminal) {
	if t.Status == shell.TTRunning {
		t.ForceStop()
	}
}

// CloseTab closes the terminal tab at index i, killing its shell if it is
// still running.
func (tp *TerminalPanel) CloseTab(i int) {
	if i < 0 || i >= len(tp.tabs) {
		return
	}
	stopTerm(tp.tabs[i].term)

	tp.tabs = append(tp.tabs[:i], tp.tabs[i+1:]...)
	if len(tp.tabs) == 0 {
		tp.Hide()
		return
	}
	if tp.active >= len(tp.tabs) {
		tp.active = len(tp.tabs) - 1
	}
}

func (tp *TerminalPanel) RenameTab(i int, name string) {
	if i < 0 || i >= len(tp.tabs) || name == "" {
		return
	}
	tp.tabs[i].name = name
}

func (tp *TerminalPanel) NextTab() {
	if len(tp.tabs) == 0 {
		return
	}
	tp.active = (tp.active + 1) % len(tp.tabs)
}

func (tp *TerminalPanel) PreviousTab() {
	if len(tp.tabs) == 0 {
		return
	}
	tp.active = (tp.active - 1 + len(tp.tabs)) % len(tp.tabs)
}

// closeAllProcesses forcibly ends every open terminal session's shell. Used
// on program exit.
func (tp *TerminalPanel) closeAllProcesses() {
	for _, t := range tp.tabs {
		stopTerm(t.term)
	}
}

func (tp *TerminalPanel) tabLabels() []string {
	labels := make([]string, len(tp.tabs))
	for i, t := range tp.tabs {
		labels[i] = t.name
	}
	return labels
}

// tabAtCol returns the index of the tab whose strip label contains column
// x (relative to the panel's left edge), or -1, along with whether x landed
// on that tab's close glyph, or whether x landed on the trailing "+" button.
func (tp *TerminalPanel) tabAtCol(x int) (idx int, onClose bool, onNew bool) {
	col := 0
	for i, t := range tp.tabs {
		label := " " + t.name + " x "
		w := len([]rune(label))
		if x >= col && x < col+w {
			relative := x - col
			return i, relative == w-2, false
		}
		col += w
	}
	if x >= col && x < col+3 {
		return -1, false, true
	}
	return -1, false, false
}

// Display draws the tab strip and the active tab's terminal content.
func (tp *TerminalPanel) Display() {
	if !tp.visible || tp.Width <= 0 || tp.Height <= 0 {
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

	for x := 0; x < tp.Width; x++ {
		screen.SetContent(tp.X+x, tp.Y, ' ', nil, tabStyle)
	}

	col := 0
	for i, t := range tp.tabs {
		label := " " + t.name + " x "
		style := tabStyle
		if i == tp.active {
			style = activeStyle
		}
		for _, r := range label {
			if col >= tp.Width {
				break
			}
			screen.SetContent(tp.X+col, tp.Y, r, nil, style)
			col++
		}
	}
	for _, r := range " + " {
		if col >= tp.Width {
			break
		}
		screen.SetContent(tp.X+col, tp.Y, r, nil, tabStyle)
		col++
	}

	if tp.active >= 0 && tp.active < len(tp.tabs) {
		active := tp.tabs[tp.active]
		for i, t := range tp.tabs {
			if t.win != nil {
				t.win.SetActive(i == tp.active)
			}
		}
		if active.win != nil {
			active.win.Display()
		}
	}
}

// HandleEvent handles a tcell event that landed within the terminal panel's
// region, or (for keyboard events) that was routed here because the panel
// currently has focus.
func (tp *TerminalPanel) HandleEvent(event tcell.Event) {
	switch e := event.(type) {
	case *tcell.EventMouse:
		tp.handleMouse(e)
	case *tcell.EventKey:
		tp.handleKey(e)
	case *tcell.EventPaste:
		if active := tp.activeTerm(); active != nil {
			active.WriteString(event.EscSeq())
		}
	}
}

func (tp *TerminalPanel) activeTerm() *shell.Terminal {
	if tp.active < 0 || tp.active >= len(tp.tabs) {
		return nil
	}
	return tp.tabs[tp.active].term
}

func (tp *TerminalPanel) handleMouse(e *tcell.EventMouse) {
	mx, my := e.Position()
	lx, ly := mx-tp.X, my-tp.Y

	switch e.Buttons() {
	case tcell.WheelUp, tcell.WheelDown:
		return
	case tcell.Button1:
		if tp.resizing {
			newHeight := tp.resizeBottom - my
			_, h := screen.Screen.Size()
			maxHeight := h - 8
			if maxHeight < minTermPanelHeight {
				maxHeight = minTermPanelHeight
			}
			if newHeight < minTermPanelHeight {
				newHeight = minTermPanelHeight
			}
			if newHeight > maxHeight {
				newHeight = maxHeight
			}
			tp.height = newHeight
			Tabs.Resize()
			return
		}
		if ly == 0 {
			idx, onClose, onNew := tp.tabAtCol(lx)
			if onNew {
				tp.NewTab("")
			} else if idx != -1 {
				if onClose {
					tp.CloseTab(idx)
				} else {
					tp.active = idx
				}
			} else {
				// empty space on the tab strip: start a drag-resize
				// instead of doing nothing
				tp.resizing = true
				tp.resizeBottom = tp.Y + tp.Height
			}
			return
		}
	case tcell.ButtonNone:
		tp.mousePressed = false
		tp.resizing = false
		return
	}

}

// leaderTimeout is how long to wait for the second key of a double-leader
// panel shortcut (e.g. Ctrl-T Ctrl-T for a new tab) before giving up and
// forwarding both keys to the shell.
const leaderTimeout = 600 * time.Millisecond

func (tp *TerminalPanel) handleKey(e *tcell.EventKey) {
	active := tp.activeTerm()

	if active != nil && active.Status == shell.TTDone {
		switch e.Key() {
		case tcell.KeyEnter, tcell.KeyEscape:
			tp.CloseTab(tp.active)
			return
		}
	}

	// Double-leader shortcuts: Ctrl-T Ctrl-T = new tab, Ctrl-W Ctrl-W =
	// close tab, Ctrl-N Ctrl-N = rename tab, Ctrl-Right/Left = next/prev tab
	if e.Key() == tcell.KeyRight && e.Modifiers()&tcell.ModCtrl != 0 {
		tp.NextTab()
		return
	}
	if e.Key() == tcell.KeyLeft && e.Modifiers()&tcell.ModCtrl != 0 {
		tp.PreviousTab()
		return
	}

	isLeaderKey := func(k tcell.Key) rune {
		switch k {
		case tcell.KeyCtrlT:
			return 't'
		case tcell.KeyCtrlW:
			return 'w'
		case tcell.KeyCtrlN:
			return 'n'
		case tcell.KeyCtrlO:
			return 'o'
		}
		return 0
	}

	if r := isLeaderKey(e.Key()); r != 0 {
		if tp.pendingLeader == r && time.Since(tp.pendingSince) < leaderTimeout {
			tp.pendingLeader = 0
			switch r {
			case 't':
				tp.NewTab("")
			case 'w':
				tp.CloseTab(tp.active)
			case 'n':
				tp.promptRename()
			case 'o':
				setFocusedRegion(RegionEditor)
			}
			return
		}
		tp.pendingLeader = r
		tp.pendingSince = time.Now()
		return
	}
	if tp.pendingLeader != 0 && time.Since(tp.pendingSince) >= leaderTimeout {
		tp.pendingLeader = 0
	}

	if active == nil {
		return
	}
	if active.Status != shell.TTDone {
		active.WriteString(keyBytes(e))
	}
}

// ToggleTermPanel toggles the bottom terminal panel. It is bindable as a
// "buffer" action (e.g. from a global keybinding).
func (h *BufPane) ToggleTermPanel() bool {
	TermPanel.Toggle()
	return true
}

// TermPanelCmd is the `termpanel` command: toggles the bottom terminal panel.
func (h *BufPane) TermPanelCmd(args []string) {
	h.ToggleTermPanel()
}

func (tp *TerminalPanel) promptRename() {
	idx := tp.active
	name := ""
	if idx >= 0 && idx < len(tp.tabs) {
		name = tp.tabs[idx].name
	}
	InfoBar.Prompt("Rename terminal: ", name, "Command", nil, func(resp string, canceled bool) {
		if canceled || resp == "" {
			return
		}
		tp.RenameTab(idx, resp)
	})
}
