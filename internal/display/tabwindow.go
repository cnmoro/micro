package display

import (
	runewidth "github.com/mattn/go-runewidth"
	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/config"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/micro/v2/internal/util"
	"github.com/micro-editor/tcell/v2"
)

type TabWindow struct {
	Names []string
	// Closable marks, per tab (same indexing as Names), whether it should
	// show a close "×" and respond to clicks on it - used for diff-view
	// tabs, which otherwise have no visible way to close beyond Ctrl-q.
	Closable []bool
	active   int
	X        int
	Y        int
	Width    int
	hscroll  int
}

func NewTabWindow(w int, y int) *TabWindow {
	tw := new(TabWindow)
	tw.Width = w
	tw.Y = y
	return tw
}

func (w *TabWindow) Resize(width, height int) {
	w.Width = width
}

// LocFromVisual maps an absolute screen position to the index of the tab
// drawn there, or -1. vloc.X must be converted to Display's local
// (w.X-relative) coordinate space before the column walk - Display draws
// at w.X+x for its own local x (starting at -w.hscroll, not
// -w.hscroll-w.X), so comparing a raw absolute vloc.X against that same
// local x, or double-subtracting w.X, both misidentify every tab whenever
// w.X != 0 (i.e. whenever something - the sidebar - occupies screen space
// to the tab bar's left; previously always 0 in practice, which is why
// this went unnoticed).
func (w *TabWindow) LocFromVisual(vloc buffer.Loc) int {
	if vloc.Y != w.Y {
		return -1
	}
	lx := vloc.X - w.X
	x := -w.hscroll

	for i, n := range w.Names {
		x++
		s := util.CharacterCountInString(n)
		if lx < x+s {
			return i
		}
		x += s
		x += 3
		if x >= w.Width {
			break
		}
	}
	return -1
}

// CloseLocFromVisual returns the index of the closable tab whose × glyph is
// at vloc, or -1 if vloc isn't on a × (including on a non-closable tab's
// trailing space, where a × would be if it had one). Mirrors
// LocFromVisual's column stepping exactly - each tab occupies "[name] ×"
// (open bracket, name, close bracket, space, close-glyph), and the ×
// is always the last of those 3 trailing columns.
func (w *TabWindow) CloseLocFromVisual(vloc buffer.Loc) int {
	if vloc.Y != w.Y {
		return -1
	}
	lx := vloc.X - w.X
	x := -w.hscroll

	for i, n := range w.Names {
		x++
		s := util.CharacterCountInString(n)
		x += s
		closeCol := x + 2 // ']'(x), ' '(x+1), '×'(x+2)
		if lx == closeCol && i < len(w.Closable) && w.Closable[i] {
			return i
		}
		x += 3
		if x >= w.Width {
			break
		}
	}
	return -1
}

func (w *TabWindow) Scroll(amt int) {
	w.hscroll += amt
	s := w.TotalSize()
	w.hscroll = util.Clamp(w.hscroll, 0, s-w.Width)

	if s-w.Width <= 0 {
		w.hscroll = 0
	}
}

func (w *TabWindow) TotalSize() int {
	sum := 2
	for _, n := range w.Names {
		sum += runewidth.StringWidth(n) + 4
	}
	return sum - 4
}

func (w *TabWindow) Active() int {
	return w.active
}

func (w *TabWindow) SetActive(a int) {
	w.active = a
	x := 2
	s := w.TotalSize()

	for i, n := range w.Names {
		c := util.CharacterCountInString(n)
		if i == a {
			if x+c >= w.hscroll+w.Width {
				w.hscroll = util.Clamp(x+c+1-w.Width, 0, s-w.Width)
			} else if x < w.hscroll {
				w.hscroll = util.Clamp(x-4, 0, s-w.Width)
			}
			break
		}
		x += c + 4
	}

	if s-w.Width <= 0 {
		w.hscroll = 0
	}
}

func (w *TabWindow) Display() {
	x := -w.hscroll
	done := false

	globalTabReverse := config.GetGlobalOption("tabreverse").(bool)
	globalTabHighlight := config.GetGlobalOption("tabhighlight").(bool)

	// xor of reverse and tab highlight to get tab character (as in filename and surrounding characters) reverse state
	tabCharHighlight := (globalTabReverse || globalTabHighlight) && !(globalTabReverse && globalTabHighlight)

	reverseStyles := func(reverse bool) (tcell.Style, tcell.Style) {
		tabBarStyle := config.DefStyle.Reverse(reverse)
		if style, ok := config.Colorscheme["tabbar"]; ok {
			tabBarStyle = style
		}
		tabBarActiveStyle := tabBarStyle
		if style, ok := config.Colorscheme["tabbar.active"]; ok {
			tabBarActiveStyle = style
		}
		return tabBarStyle, tabBarActiveStyle
	}

	draw := func(r rune, n int, active bool, reversed bool) {
		tabBarStyle, tabBarActiveStyle := reverseStyles(reversed)

		style := tabBarStyle
		if active {
			style = tabBarActiveStyle
		}
		for i := 0; i < n; i++ {
			rw := runewidth.RuneWidth(r)
			for j := 0; j < rw; j++ {
				c := r
				if j > 0 {
					c = ' '
				}
				if x == w.Width-1 && !done {
					screen.SetContent(w.X+w.Width-1, w.Y, '>', nil, tabBarStyle)
					x++
					break
				} else if x == 0 && w.hscroll > 0 {
					screen.SetContent(w.X+0, w.Y, '<', nil, tabBarStyle)
				} else if x >= 0 && x < w.Width {
					screen.SetContent(w.X+x, w.Y, c, nil, style)
				}
				x++
			}
		}
	}

	for i, n := range w.Names {
		if i == w.active {
			draw('[', 1, true, tabCharHighlight)
		} else {
			draw(' ', 1, false, tabCharHighlight)
		}

		for _, c := range n {
			draw(c, 1, i == w.active, tabCharHighlight)
		}

		if i == len(w.Names)-1 {
			done = true
		}

		closeGlyph := ' '
		if i < len(w.Closable) && w.Closable[i] {
			closeGlyph = '×'
		}
		if i == w.active {
			draw(']', 1, true, tabCharHighlight)
			draw(' ', 1, true, globalTabReverse)
			draw(closeGlyph, 1, true, globalTabReverse)
		} else {
			draw(' ', 1, false, tabCharHighlight)
			draw(' ', 1, false, globalTabReverse)
			draw(closeGlyph, 1, false, globalTabReverse)
		}

		if x >= w.Width {
			break
		}
	}

	if x < w.Width {
		draw(' ', w.Width-x, false, globalTabReverse)
	}
}
