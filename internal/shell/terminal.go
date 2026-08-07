package shell

import (
	"bytes"
	"sync"

	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/screen"
	"github.com/micro-editor/terminal"
)

type TermType int
type CallbackFunc func(string)

const (
	TTClose   = iota // Should be closed
	TTRunning        // Currently running a command
	TTDone           // Finished running a command
)

var CloseTerms chan bool

func init() {
	CloseTerms = make(chan bool)
}

// winPTY is implemented by the Windows ConPTY handle (see
// terminal_windows.go). It's declared here, in a platform-agnostic file,
// so Terminal can hold one without every other file needing a
// Windows-only import; it stays nil on every other OS.
//
// It exists because, unlike a Unix PTY (one bidirectional file
// descriptor), ConPTY's input and output are separate pipes, and its
// resize call is a real Win32 call rather than the ioctl the vendored
// terminal library issues on Unix (a no-op stub on Windows) - so writes
// and resizes need to go through it explicitly instead of through
// Term.File(), which is nil for a ConPTY-backed Terminal.
type winPTY interface {
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Close() error
}

// A Terminal holds information for the terminal emulator
type Terminal struct {
	State     terminal.State
	Term      *terminal.VT
	title     string
	Status    TermType
	Selection [2]buffer.Loc
	wait      bool
	getOutput bool
	output    *bytes.Buffer
	callback  CallbackFunc

	winPty winPTY // non-nil only on Windows, once Start has run

	closeOnce sync.Once // guards teardown (see doStop)
}

// HasSelection returns whether this terminal has a valid selection
func (t *Terminal) HasSelection() bool {
	return t.Selection[0] != t.Selection[1]
}

func (t *Terminal) Name() string {
	return t.title
}

// GetSelection returns the selected text
func (t *Terminal) GetSelection(width int) string {
	start := t.Selection[0]
	end := t.Selection[1]
	if start.GreaterThan(end) {
		start, end = end, start
	}
	var ret string
	var l buffer.Loc
	for y := start.Y; y <= end.Y; y++ {
		for x := 0; x < width; x++ {
			l.X, l.Y = x, y
			if l.GreaterEqual(start) && l.LessThan(end) {
				c, _, _ := t.State.Cell(x, y)
				ret += string(c)
			}
		}
	}
	return ret
}

// Start begins a new command in this terminal with a given view. The
// actual process/pty setup is platform-specific - see startProcess in
// terminal_posix.go (a real PTY via github.com/creack/pty, through the
// vendored terminal library) and terminal_windows.go (a ConPTY pseudo
// console).
func (t *Terminal) Start(execCmd []string, getOutput bool, wait bool, callback func(out string, userargs []any), userargs []any) error {
	if len(execCmd) <= 0 {
		return nil
	}

	t.output = nil
	if getOutput {
		t.output = bytes.NewBuffer([]byte{})
	}
	t.getOutput = getOutput
	t.wait = wait
	t.callback = func(out string) {
		callback(out, userargs)
	}

	return t.startProcess(execCmd)
}

// parseLoop continuously parses output from Term (updating t.State, and
// triggering a redraw on every chunk) until the process exits or the pty/
// ConPTY is closed, then stops the terminal. Shared by every platform's
// startProcess, which only differs in how Term/the underlying process
// were created.
func (t *Terminal) parseLoop(Term *terminal.VT) {
	for {
		err := Term.Parse()
		if err != nil {
			Term.Write([]byte("Press enter to close"))
			screen.Redraw()
			break
		}
		screen.NoteRate("terminal-parseLoop-redraw")
		screen.Redraw()
	}
	t.Stop()
}

// Resize propagates a size change to both the VT100 parser state and, on
// Windows, the ConPTY itself - unlike a Unix PTY, resizing that isn't a
// side effect of Term.Resize (see the winPTY doc comment above).
func (t *Terminal) ResizePTY(cols, rows int) {
	if t.Term != nil {
		t.Term.Resize(cols, rows)
	}
	if t.winPty != nil {
		t.winPty.Resize(cols, rows)
	}
}

// doStop tears down the pty/ConPTY exactly once, however many times or
// from however many goroutines Stop/ForceStop get called. Without this,
// a user-initiated ForceStop (terminal panel closing a tab) races with
// parseLoop's own Stop call (fired when that same close unblocks Term.Parse
// with an error) and both independently close the same handles again. On
// a Unix *os.File that second Close is a harmless no-op error, but on
// Windows it's a second raw CloseHandle/ClosePseudoConsole on values the
// OS is free to have already recycled for something else in the process by
// then - which can take down far more than just this terminal.
func (t *Terminal) doStop() {
	t.closeOnce.Do(func() {
		if t.winPty != nil {
			// On Windows, Term.File() is always nil (no separate pty
			// file - see winPTY's doc comment) and Term.Close() closes
			// this same winPty, so closing it once here is sufficient.
			t.winPty.Close()
			return
		}
		if t.Term != nil {
			if f := t.Term.File(); f != nil {
				f.Close()
			}
			t.Term.Close()
		}
	})
}

// Stop stops execution of the terminal and sets the Status
// to TTDone
func (t *Terminal) Stop() {
	t.doStop()
	if t.wait {
		t.Status = TTDone
	} else {
		t.Close()
		CloseTerms <- true
	}
}

// ForceStop closes the pty/ConPTY of a still-running terminal directly,
// without waiting for the process to exit on its own or touching Status/
// CloseTerms - for callers (like the terminal panel closing a tab the
// user explicitly asked to close) that manage their own bookkeeping and
// just need the underlying process gone.
func (t *Terminal) ForceStop() {
	t.doStop()
}

// Close sets the Status to TTClose indicating that the terminal
// is done and should be closed
func (t *Terminal) Close() {
	t.Status = TTClose
	// call the lua function that the user has given as a callback
	if t.getOutput {
		if t.callback != nil {
			Jobs <- JobFunction{
				Function: func(out string, args []any) {
					t.callback(out)
				},
				Output: t.output.String(),
				Args:   nil,
			}
		}
	}
}

// WriteString writes a given string to this terminal's pty
func (t *Terminal) WriteString(str string) {
	if t.winPty != nil {
		t.winPty.Write([]byte(str))
		return
	}
	t.Term.File().WriteString(str)
}
