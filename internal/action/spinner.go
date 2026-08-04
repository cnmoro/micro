package action

import (
	"sync/atomic"
	"time"

	"github.com/micro-editor/micro/v2/internal/shell"
)

var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// Spinner is a lightweight, non-blocking "loading" indicator shown in the
// info bar while a background operation (an SSH connect, a Docker action,
// a Docker refresh, ...) is in flight. Starting one never blocks the UI -
// it just periodically posts an updated frame back onto shell.Jobs, the
// same main-goroutine-only channel every other background action in this
// codebase already uses to touch UI state safely.
type Spinner struct {
	stopped atomic.Bool
	// lastMsg is the last frame text this spinner wrote to the info bar.
	// It is only ever read/written from the main goroutine (inside
	// shell.Jobs callbacks), so it needs no synchronization of its own.
	lastMsg string
}

// StartSpinner starts an animated "<frame> <label>" message in the info
// bar. Call Stop() once the operation finishes and you're about to show
// your own result message. Call StopAndClear() instead if the operation
// succeeded silently (no result message of its own) - it removes the
// spinner's last frame, but only if nothing else has changed the info bar
// message since, so it never clobbers something more important that
// happened to be set in the meantime (e.g. by another concurrent
// operation).
func StartSpinner(label string) *Spinner {
	s := &Spinner{}
	go func() {
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			<-ticker.C
			if s.stopped.Load() {
				return
			}
			frame := spinnerFrames[i%len(spinnerFrames)]
			i++
			shell.Jobs <- shell.JobFunction{Function: func(string, []any) {
				if s.stopped.Load() {
					return
				}
				s.lastMsg = string(frame) + " " + label
				InfoBar.Message(s.lastMsg)
			}}
		}
	}()
	return s
}

// Stop ends the spinner animation. Safe to call multiple times.
func (s *Spinner) Stop() {
	s.stopped.Store(true)
}

// StopAndClear ends the spinner animation and, if the info bar is still
// showing this spinner's last frame (i.e. nothing else has posted a
// message since), clears it.
func (s *Spinner) StopAndClear() {
	s.Stop()
	if s.lastMsg != "" && InfoBar.Msg == s.lastMsg {
		InfoBar.Message("")
	}
}
