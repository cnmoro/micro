package action

import (
	"github.com/micro-editor/micro/v2/internal/config"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/tcell/v2"
)

// PopupItem is a single selectable entry in a PopupMenu.
type PopupItem struct {
	Label string
	Run   func()
}

// PopupMenu is a small centered modal list (a "wizard step") used for
// short guided flows like SSH connect/disconnect. Only one can be active
// at a time; while active it captures all input (see RouteEvent), the
// same way an InfoBar prompt does.
type PopupMenu struct {
	title string
	items []PopupItem
	sel   int

	x, y, w, h int
}

// activePopup is the currently displayed popup menu, or nil.
var activePopup *PopupMenu

// ShowPopup opens a centered popup menu with the given title and items.
func ShowPopup(title string, items []PopupItem) {
	w := len([]rune(title))
	for _, it := range items {
		if l := len([]rune(it.Label)); l > w {
			w = l
		}
	}
	w += 4              // borders + padding
	h := len(items) + 3 // top border+title, items, bottom border

	sw, sh := screen.Screen.Size()
	if w > sw-4 {
		w = sw - 4
	}
	if h > sh-4 {
		h = sh - 4
	}
	x := (sw - w) / 2
	y := (sh - h) / 3
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	activePopup = &PopupMenu{title: title, items: items, x: x, y: y, w: w, h: h}
}

// ClosePopup dismisses the active popup menu, if any.
func ClosePopup() {
	activePopup = nil
}

// PopupActive reports whether a popup menu is currently shown.
func PopupActive() bool {
	return activePopup != nil
}

// DisplayPopup draws the active popup menu, if any. Called last each
// frame so it overlays everything else already drawn.
func DisplayPopup() {
	if activePopup != nil {
		activePopup.Display()
	}
}

func (p *PopupMenu) clamp() {
	if p.sel < 0 {
		p.sel = len(p.items) - 1
	}
	if p.sel >= len(p.items) {
		p.sel = 0
	}
}

// Display draws the popup as a bordered box over whatever else was drawn
// this frame.
func (p *PopupMenu) Display() {
	style := config.DefStyle
	if s, ok := config.Colorscheme["statusline"]; ok {
		style = s
	}
	titleStyle := style.Bold(true)
	selStyle := style.Reverse(true)

	// clear + border
	for row := 0; row < p.h; row++ {
		for col := 0; col < p.w; col++ {
			r := ' '
			switch {
			case row == 0 && col == 0:
				r = '┌'
			case row == 0 && col == p.w-1:
				r = '┐'
			case row == p.h-1 && col == 0:
				r = '└'
			case row == p.h-1 && col == p.w-1:
				r = '┘'
			case row == 0 || row == p.h-1:
				r = '─'
			case col == 0 || col == p.w-1:
				r = '│'
			}
			screen.SetContent(p.x+col, p.y+row, r, nil, style)
		}
	}

	// title
	title := " " + p.title + " "
	for i, r := range []rune(title) {
		if 1+i >= p.w-1 {
			break
		}
		screen.SetContent(p.x+1+i, p.y, r, nil, titleStyle)
	}

	// items
	for i, it := range p.items {
		row := 1 + i
		if row >= p.h-1 {
			break
		}
		st := style
		if i == p.sel {
			st = selStyle
		}
		for col := 1; col < p.w-1; col++ {
			screen.SetContent(p.x+col, p.y+row, ' ', nil, st)
		}
		label := " " + it.Label
		for i2, r := range []rune(label) {
			if 1+i2 >= p.w-1 {
				break
			}
			screen.SetContent(p.x+1+i2, p.y+row, r, nil, st)
		}
	}
}

// HandleEvent handles a tcell event while the popup has input focus (it
// captures everything, the same way an InfoBar prompt does).
func (p *PopupMenu) HandleEvent(event tcell.Event) {
	switch e := event.(type) {
	case *tcell.EventKey:
		switch e.Key() {
		case tcell.KeyUp:
			p.sel--
			p.clamp()
		case tcell.KeyDown:
			p.sel++
			p.clamp()
		case tcell.KeyEnter:
			it := p.items[p.sel]
			ClosePopup()
			it.Run()
		case tcell.KeyEscape:
			ClosePopup()
		}
	case *tcell.EventMouse:
		if e.Buttons() != tcell.Button1 {
			return
		}
		mx, my := e.Position()
		lx, ly := mx-p.x, my-p.y
		if lx <= 0 || lx >= p.w-1 {
			return
		}
		idx := ly - 1
		if idx < 0 || idx >= len(p.items) {
			return
		}
		it := p.items[idx]
		ClosePopup()
		it.Run()
	case *tcell.EventResize:
		ClosePopup()
	}
}
