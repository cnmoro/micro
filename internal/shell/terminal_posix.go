//go:build !windows

package shell

import (
	"os/exec"
	"strconv"

	"github.com/micro-editor/terminal"
)

// startProcess spawns execCmd attached to a real Unix PTY (via the
// vendored terminal library, which uses github.com/creack/pty).
func (t *Terminal) startProcess(execCmd []string) error {
	cmd := exec.Command(execCmd[0], execCmd[1:]...)
	if t.getOutput {
		cmd.Stdout = t.output
	}

	Term, _, err := terminal.Start(&t.State, cmd)
	if err != nil {
		return err
	}
	t.Term = Term
	t.Status = TTRunning
	t.title = execCmd[0] + ":" + strconv.Itoa(cmd.Process.Pid)

	go t.parseLoop(Term)

	return nil
}
