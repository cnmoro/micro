package action

import (
	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/config"
	"github.com/micro-editor/micro/v2/internal/display"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/micro/v2/internal/views"
	"github.com/micro-editor/tcell/v2"
)

// SidebarViewKind identifies which content is currently shown in the sidebar.
type SidebarViewKind int

const (
	SidebarExplorerView SidebarViewKind = iota
	SidebarDockerView
)

// activityStripWidth is the width, in columns, of the VSCode-style icon
// strip on the left edge of the sidebar used to switch between views.
const activityStripWidth = 2

// DefaultSidebarWidth is the default width of the sidebar (including the
// activity strip) when it is first shown.
const DefaultSidebarWidth = 32

// SidebarContent is implemented by anything that can be displayed inside
// the sidebar's content area (to the right of the activity strip).
type SidebarContent interface {
	Display(x, y, w, h int)
	HandleKey(e *tcell.EventKey) bool
	HandleClick(x, y int, button tcell.ButtonMask)
	HandleWheel(up bool)
	Title() string
	Refresh()
}

// SidebarPane is the left sidebar: a 1-2 column activity strip plus
// whichever SidebarContent (Explorer or Docker) is currently active. It is
// a fixed-region sibling of TabList/InfoBar, not part of any Tab's Node
// tree, so it is unaffected by vsplit/hsplit/unsplit and terminal resizes.
type SidebarPane struct {
	*display.View

	id      uint64
	visible bool
	active  SidebarViewKind

	explorer *Explorer
	docker   *DockerView

	mousePressed bool
	widthCols    int
	resizing     bool
}

// minSidebarWidth is the narrowest the sidebar can be dragged to (enough
// room for the activity strip plus a few characters of content).
const minSidebarWidth = activityStripWidth + 6

// width returns the sidebar's configured width in columns.
func (s *SidebarPane) width() int {
	if s.widthCols <= 0 {
		return DefaultSidebarWidth
	}
	return s.widthCols
}

// Sidebar is the global sidebar singleton.
var Sidebar *SidebarPane

// InitSidebar creates the global Sidebar singleton.
func InitSidebar() {
	s := new(SidebarPane)
	s.View = new(display.View)
	s.id = views.NewID()
	s.active = SidebarExplorerView
	s.explorer = NewExplorer()
	s.docker = NewDockerView()
	Sidebar = s
}

func (s *SidebarPane) ID() uint64                               { return s.id }
func (s *SidebarPane) SetID(i uint64)                           { s.id = i }
func (s *SidebarPane) Name() string                             { return "Sidebar" }
func (s *SidebarPane) Close()                                   {}
func (s *SidebarPane) SetTab(t *Tab)                            {}
func (s *SidebarPane) Tab() *Tab                                { return nil }
func (s *SidebarPane) Relocate() bool                           { return false }
func (s *SidebarPane) SetActive(b bool)                         {}
func (s *SidebarPane) IsActive() bool                           { return FocusedRegion == RegionSidebar }
func (s *SidebarPane) GetView() *display.View                   { return s.View }
func (s *SidebarPane) SetView(v *display.View)                  { s.View = v }
func (s *SidebarPane) LocFromVisual(vloc buffer.Loc) buffer.Loc { return vloc }

func (s *SidebarPane) HandleCommand(input string) {}

// Contains reports whether the given absolute screen coordinate is inside
// the sidebar's region.
func (s *SidebarPane) Contains(x, y int) bool {
	return s.visible && x >= s.X && x < s.X+s.Width && y >= s.Y && y < s.Y+s.Height
}

// Visible reports whether the sidebar is currently shown.
func (s *SidebarPane) Visible() bool { return s.visible }

// Show makes the sidebar visible, optionally switching to the given view,
// and triggers a resize/refresh.
func (s *SidebarPane) Show(kind SidebarViewKind) {
	wasVisible := s.visible
	sameView := s.active == kind
	s.visible = true
	s.active = kind
	if !wasVisible || !sameView {
		s.content().Refresh()
	}
	Tabs.Resize()
	FocusedRegion = RegionSidebar
}

// Hide hides the sidebar.
func (s *SidebarPane) Hide() {
	s.visible = false
	if FocusedRegion == RegionSidebar {
		FocusedRegion = RegionEditor
	}
	Tabs.Resize()
}

// Toggle shows the sidebar with the given view if it's hidden or showing a
// different view; hides it if it's already showing that view.
func (s *SidebarPane) Toggle(kind SidebarViewKind) {
	if s.visible && s.active == kind {
		s.Hide()
		return
	}
	s.Show(kind)
}

func (s *SidebarPane) content() SidebarContent {
	if s.active == SidebarDockerView {
		return s.docker
	}
	return s.explorer
}

// Resize sets the sidebar's absolute geometry. Called from TabList.Resize.
func (s *SidebarPane) Resize(w, h int) {
	s.Width, s.Height = w, h
}

func (s *SidebarPane) Clear() {
	for y := 0; y < s.Height; y++ {
		for x := 0; x < s.Width; x++ {
			screen.SetContent(s.X+x, s.Y+y, ' ', nil, config.DefStyle)
		}
	}
}

func (s *SidebarPane) resetMouse() {
	s.mousePressed = false
	s.resizing = false
}

// Display draws the activity strip and the active content view.
func (s *SidebarPane) Display() {
	if !s.visible || s.Width <= 0 {
		return
	}
	s.Clear()

	dividerStyle := config.DefStyle
	if style, ok := config.Colorscheme["divider"]; ok {
		dividerStyle = style
	}
	activeStyle := config.DefStyle.Reverse(true)
	if style, ok := config.Colorscheme["tabbar.active"]; ok {
		activeStyle = style
	}

	// Activity strip
	explorerStyle, dockerStyle := dividerStyle, dividerStyle
	if s.active == SidebarExplorerView {
		explorerStyle = activeStyle
	} else {
		dockerStyle = activeStyle
	}
	screen.SetContent(s.X, s.Y, 'E', nil, explorerStyle)
	screen.SetContent(s.X, s.Y+1, 'D', nil, dockerStyle)
	for y := 2; y < s.Height; y++ {
		screen.SetContent(s.X, s.Y+y, ' ', nil, dividerStyle)
	}

	// Vertical divider between activity strip and content, and between
	// sidebar and editor area
	divchar := '|'
	if opt, ok := config.GetGlobalOption("divchars").(string); ok && len(opt) > 0 {
		divchar = []rune(opt)[0]
	}
	for y := 0; y < s.Height; y++ {
		screen.SetContent(s.X+activityStripWidth-1, s.Y+y, ' ', nil, dividerStyle)
		screen.SetContent(s.X+s.Width-1, s.Y+y, divchar, nil, dividerStyle)
	}

	contentX := s.X + activityStripWidth
	contentW := s.Width - activityStripWidth - 1
	if contentW < 0 {
		contentW = 0
	}

	// Title row
	title := s.content().Title()
	titleStyle := dividerStyle
	for x := 0; x < contentW; x++ {
		r := ' '
		if x < len([]rune(title)) {
			r = []rune(title)[x]
		}
		screen.SetContent(contentX+x, s.Y, r, nil, titleStyle)
	}

	s.content().Display(contentX, s.Y+1, contentW, s.Height-1)
}

// HandleEvent handles a tcell event that landed within the sidebar's region.
func (s *SidebarPane) HandleEvent(event tcell.Event) {
	switch e := event.(type) {
	case *tcell.EventMouse:
		mx, my := e.Position()
		lx, ly := mx-s.X, my-s.Y

		switch e.Buttons() {
		case tcell.WheelUp:
			s.content().HandleWheel(true)
		case tcell.WheelDown:
			s.content().HandleWheel(false)
		case tcell.Button1:
			if s.resizing {
				w, _ := screen.Screen.Size()
				newWidth := mx - s.X + 1
				maxWidth := w - 20
				if maxWidth < minSidebarWidth {
					maxWidth = minSidebarWidth
				}
				if newWidth < minSidebarWidth {
					newWidth = minSidebarWidth
				}
				if newWidth > maxWidth {
					newWidth = maxWidth
				}
				s.widthCols = newWidth
				Tabs.Resize()
				return
			}
			if lx == s.Width-1 {
				// clicked the divider on the sidebar's right edge: start a
				// drag-resize instead of forwarding the click to content
				s.resizing = true
				return
			}
			if lx < activityStripWidth {
				if ly == 0 {
					s.Show(SidebarExplorerView)
				} else if ly == 1 {
					s.Show(SidebarDockerView)
				}
				return
			}
			if ly == 0 {
				return // title row
			}
			s.mousePressed = true
			s.content().HandleClick(lx-activityStripWidth, ly-1, e.Buttons())
		case tcell.ButtonSecondary:
			if lx < activityStripWidth || ly == 0 {
				return
			}
			s.content().HandleClick(lx-activityStripWidth, ly-1, e.Buttons())
		case tcell.ButtonNone:
			s.mousePressed = false
			s.resizing = false
		}
	case *tcell.EventKey:
		if e.Key() == tcell.KeyEscape {
			FocusedRegion = RegionEditor
			return
		}
		if s.content().HandleKey(e) {
			return
		}
	}
}

// ToggleExplorer toggles the Explorer view in the sidebar. It is bindable
// as a "buffer" action (e.g. from a global keybinding).
func (h *BufPane) ToggleExplorer() bool {
	Sidebar.Toggle(SidebarExplorerView)
	return true
}

// ToggleDocker toggles the Docker view in the sidebar.
func (h *BufPane) ToggleDocker() bool {
	Sidebar.Toggle(SidebarDockerView)
	return true
}

// ExplorerCmd is the `explorer` command: toggles the Explorer sidebar view.
func (h *BufPane) ExplorerCmd(args []string) {
	h.ToggleExplorer()
}

// DockerCmd is the `docker` command: toggles the Docker sidebar view.
func (h *BufPane) DockerCmd(args []string) {
	h.ToggleDocker()
}
