package action

import (
	"github.com/micro-editor/tcell/v2"
)

// Region identifies which top-level UI region currently has keyboard focus.
type Region int

const (
	RegionEditor Region = iota
	RegionSidebar
	RegionTermPanel
)

// FocusedRegion is the region that currently receives non-mouse (keyboard/paste)
// events. Mouse events are routed by position instead, and update FocusedRegion
// as a side effect of a press/drag/wheel event landing in a given region.
var FocusedRegion Region = RegionEditor

// RouteEvent is the single entry point the main loop uses to dispatch a tcell
// event to the correct region (InfoBar prompt, Sidebar, TermPanel, or the
// editor Tabs), taking mouse position and keyboard focus into account.
func RouteEvent(event tcell.Event) {
	if _, resize := event.(*tcell.EventResize); resize {
		InfoBar.HandleEvent(event)
		Tabs.HandleEvent(event)
		return
	}

	if PopupActive() {
		activePopup.HandleEvent(event)
		return
	}

	if InfoBar.HasPrompt {
		InfoBar.HandleEvent(event)
		return
	}

	// Global panel toggles work regardless of which region currently has
	// keyboard focus (unlike ordinary buffer keybindings, which only fire
	// while the editor area is focused).
	if ke, ok := event.(*tcell.EventKey); ok && handleGlobalToggle(ke) {
		return
	}

	if me, ok := event.(*tcell.EventMouse); ok {
		mx, my := me.Position()
		btn := me.Buttons()
		isRelease := btn == tcell.ButtonNone

		if isRelease {
			switch FocusedRegion {
			case RegionSidebar:
				Sidebar.HandleEvent(event)
			case RegionTermPanel:
				TermPanel.HandleEvent(event)
			default:
				Tabs.HandleEvent(event)
			}
			return
		}

		// While a border drag-resize is in progress, keep delivering
		// events to that region even after the mouse has moved past its
		// (pre-drag) boundary - otherwise dragging outward would
		// immediately "escape" the hit-test and get routed elsewhere.
		if Sidebar.resizing {
			Sidebar.HandleEvent(event)
			return
		}
		if TermPanel.resizing {
			TermPanel.HandleEvent(event)
			return
		}

		if Sidebar.visible && Sidebar.Contains(mx, my) {
			FocusedRegion = RegionSidebar
			Sidebar.HandleEvent(event)
			return
		}
		if TermPanel.visible && TermPanel.Contains(mx, my) {
			FocusedRegion = RegionTermPanel
			TermPanel.HandleEvent(event)
			return
		}
		if FileTabs.Contains(mx, my) {
			FileTabs.HandleEvent(event)
			return
		}

		FocusedRegion = RegionEditor
		Tabs.HandleEvent(event)
		return
	}

	switch FocusedRegion {
	case RegionSidebar:
		Sidebar.HandleEvent(event)
	case RegionTermPanel:
		TermPanel.HandleEvent(event)
	default:
		Tabs.HandleEvent(event)
	}
}

// ResetRouterMouse clears any stuck mouse state in the Sidebar/TermPanel
// regions, mirroring TabList.ResetMouse for the editor area.
func ResetRouterMouse() {
	Sidebar.resetMouse()
	TermPanel.resetMouse()
	FileTabs.resetMouse()
}

// handleGlobalToggle intercepts the fixed Alt-1/Alt-2/Alt-3/Alt-4
// panel-toggle shortcuts so they work no matter which region has keyboard
// focus (a terminal panel tab, the sidebar, or the editor). Returns true
// if the event was consumed.
func handleGlobalToggle(e *tcell.EventKey) bool {
	if e.Key() != tcell.KeyRune || e.Modifiers()&tcell.ModAlt == 0 {
		return false
	}
	switch e.Rune() {
	case '1':
		Sidebar.Toggle(SidebarExplorerView)
		return true
	case '2':
		Sidebar.Toggle(SidebarDockerView)
		return true
	case '3':
		TermPanel.Toggle()
		return true
	case '4':
		openSSHWizard()
		return true
	case '5':
		openWorkDirWizard()
		return true
	}
	return false
}
