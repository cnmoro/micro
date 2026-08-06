package action

import (
	"github.com/micro-editor/tcell/v2"
)

// keyBytes returns the byte sequence that should be written to a terminal's
// pty for the given key event. It prefers the raw escape sequence tcell
// captured while parsing the terminal's own input stream (EscSeq) - that is
// always correct and is how this worked before this function existed. But
// tcell's Windows console backend builds every EventKey directly from
// structured Win32 console records, which never populate EscSeq (there is
// no raw byte stream to preserve there - see console_win.go), so on Windows
// EscSeq is always empty and something has to synthesize the bytes a shell
// actually expects instead.
func keyBytes(e *tcell.EventKey) string {
	if esc := e.EscSeq(); esc != "" {
		return esc
	}

	mod := e.Modifiers()
	switch e.Key() {
	case tcell.KeyRune:
		r := e.Rune()
		if mod&tcell.ModAlt != 0 {
			return "\x1b" + string(r)
		}
		return string(r)
	case tcell.KeyUp:
		return "\x1b[A"
	case tcell.KeyDown:
		return "\x1b[B"
	case tcell.KeyRight:
		return "\x1b[C"
	case tcell.KeyLeft:
		return "\x1b[D"
	case tcell.KeyHome:
		return "\x1b[H"
	case tcell.KeyEnd:
		return "\x1b[F"
	case tcell.KeyPgUp:
		return "\x1b[5~"
	case tcell.KeyPgDn:
		return "\x1b[6~"
	case tcell.KeyDelete:
		return "\x1b[3~"
	case tcell.KeyInsert:
		return "\x1b[2~"
	case tcell.KeyBacktab:
		return "\x1b[Z"
	case tcell.KeyF1:
		return "\x1bOP"
	case tcell.KeyF2:
		return "\x1bOQ"
	case tcell.KeyF3:
		return "\x1bOR"
	case tcell.KeyF4:
		return "\x1bOS"
	case tcell.KeyF5:
		return "\x1b[15~"
	case tcell.KeyF6:
		return "\x1b[17~"
	case tcell.KeyF7:
		return "\x1b[18~"
	case tcell.KeyF8:
		return "\x1b[19~"
	case tcell.KeyF9:
		return "\x1b[20~"
	case tcell.KeyF10:
		return "\x1b[21~"
	case tcell.KeyF11:
		return "\x1b[23~"
	case tcell.KeyF12:
		return "\x1b[24~"
	}

	// Enter/Tab/Esc/Backspace and every Ctrl-<letter> combo (KeyCtrlA..Z,
	// KeyCtrlSpace, etc) share tcell's Key type with the literal ASCII
	// control-code values 0-31, and Backspace2/KeyDEL is 0x7F - so for
	// everything not already handled above, the key code itself already
	// is the byte the shell expects.
	if k := e.Key(); (k >= 0 && k <= 31) || k == 0x7F {
		return string(rune(k))
	}

	return ""
}
