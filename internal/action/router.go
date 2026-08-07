package action

import (
	"github.com/micro-editor/micro/v2/internal/screen"
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
// Set it via setFocusedRegion, not a direct assignment - see that function's
// doc comment for why.
var FocusedRegion Region = RegionEditor

// setFocusedRegion changes which region has keyboard focus, resetting the
// fake cursor (Windows console default; see screen.ResetFakeCursor) on
// every transition. The fake cursor is a single reversed screen cell that
// only self-corrects if whichever region owns it keeps redrawing it every
// frame without fail; each region only does that conditionally (while
// it's active, and for the terminal panel, only while the remote
// program's own cursor-visible state is set) - so a region change is
// exactly when a stale mark from the region focus is leaving can end up
// stuck on screen with nothing left to clean it up or draw a new one.
func setFocusedRegion(r Region) {
	if r != FocusedRegion {
		screen.ResetFakeCursor()
	}
	FocusedRegion = r
}

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
			setFocusedRegion(RegionSidebar)
			Sidebar.HandleEvent(event)
			return
		}
		if TermPanel.visible && TermPanel.Contains(mx, my) {
			setFocusedRegion(RegionTermPanel)
			TermPanel.HandleEvent(event)
			return
		}
		if FileTabs.Contains(mx, my) {
			FileTabs.HandleEvent(event)
			return
		}

		setFocusedRegion(RegionEditor)
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

// handleGlobalToggle intercepts the fixed panel-toggle shortcuts so they
// work no matter which region has keyboard focus (a terminal panel tab,
// the sidebar, or the editor). Returns true if the event was consumed.
//
// Each toggle has three independent ways to trigger it - Alt-<digit>,
// Ctrl-Shift-<digit>, and F1/F5/F6/F8/F9 - because no single one of them
// is reliable across every terminal:
//   - Alt (Option on macOS) only sends a real Alt/Meta modifier if "Use
//     Option as Meta Key" (or "Option Key Sends Esc+") is explicitly
//     enabled in the terminal's own settings; without that, Option+key
//     just types a composed accent character with no modifier at all.
//   - Ctrl-Shift-<digit> has no defined key mapping in Terminal.app on
//     macOS at all - the OS beeps and no bytes are even sent to the
//     terminal program, so there's nothing micro could parse either way.
//   - F-keys can be intercepted by some terminal apps/window managers for
//     their own shortcuts (help, fullscreen, Mission Control, ...) before
//     micro ever sees them.
//
// Cmd is never forwarded to any terminal application layer in the first
// place (macOS reserves it), so it isn't and can't be handled here.
//
// All three are accepted on every OS rather than picking one per
// platform, since which one (if any) actually reaches micro depends on
// the specific terminal/multiplexer, not just the OS. If none of them
// work in a given setup, `Ctrl-e` (command mode, a plain Ctrl+letter -
// the one modifier combo that's actually universal) followed by
// `explorer`/`docker`/`termpanel`/`ssh`/`openfolder` and Enter always
// works regardless of what any of these keybindings do.
var fkeyToggle = map[tcell.Key]rune{
	tcell.KeyF1:  '1',
	tcell.KeyF5:  '2',
	tcell.KeyF6:  '3',
	tcell.KeyF8:  '4',
	tcell.KeyF9:  '5',
	tcell.KeyF11: '6',
}

// shiftedDigit maps the US-layout shifted glyph of each digit to the
// digit itself, since some terminals report Ctrl-Shift-<digit> as Ctrl
// plus the already-shifted symbol rather than as a separate Shift
// modifier bit alongside the bare digit.
var shiftedDigit = map[rune]rune{
	'!': '1', '@': '2', '#': '3', '$': '4', '%': '5', '^': '6',
}

func handleGlobalToggle(e *tcell.EventKey) bool {
	digit := rune(0)
	if d, ok := fkeyToggle[e.Key()]; ok {
		digit = d
	} else if e.Key() == tcell.KeyRune {
		mod := e.Modifiers()
		r := e.Rune()
		switch {
		case mod&tcell.ModAlt != 0:
			digit = r
		case mod&tcell.ModCtrl != 0:
			if d, ok := shiftedDigit[r]; ok {
				digit = d
			} else if mod&tcell.ModShift != 0 {
				digit = r
			}
		}
	}

	switch digit {
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
	case '6':
		Sidebar.Toggle(SidebarGitView)
		return true
	}
	return false
}
