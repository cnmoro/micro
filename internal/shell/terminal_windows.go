//go:build windows

package shell

import (
	"strconv"
	"strings"
	"syscall"

	"github.com/UserExistsError/conpty"
	"github.com/micro-editor/terminal"
)

// startProcess spawns execCmd attached to a Windows pseudo console
// (ConPTY - the same mechanism Windows Terminal/VS Code use), available
// on Windows 10 1809 and later. The vendored terminal library has no
// Windows PTY implementation of its own, so process spawning and I/O go
// through a ConPTY handle directly here; terminal.Create wires that
// handle's output into the same VT100 parser used on every other OS.
func (t *Terminal) startProcess(execCmd []string) error {
	if !conpty.IsConPtyAvailable() {
		return errUnsupportedWindows
	}

	commandLine := buildWindowsCommandLine(execCmd)
	cpty, err := conpty.Start(commandLine)
	if err != nil {
		return err
	}

	Term, err := terminal.Create(&t.State, cpty)
	if err != nil {
		cpty.Close()
		return err
	}

	t.Term = Term
	t.winPty = cpty
	t.Status = TTRunning
	t.title = execCmd[0] + ":" + strconv.Itoa(cpty.Pid())

	go t.parseLoop(Term)

	return nil
}

var errUnsupportedWindows = &windowsPTYError{}

type windowsPTYError struct{}

func (*windowsPTYError) Error() string {
	return "the integrated terminal needs ConPTY, which requires Windows 10 1809 or later"
}

// buildWindowsCommandLine joins argv into the single quoted command-line
// string Windows' CreateProcess expects, using the same escaping rules
// Go's own os/exec uses on Windows.
func buildWindowsCommandLine(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = syscall.EscapeArg(a)
	}
	return strings.Join(parts, " ")
}
