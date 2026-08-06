package action

import (
	"os"
	"runtime"
)

// defaultShellArgs returns the argv to spawn when opening a terminal with
// no explicit command (the terminal panel's "+"/new tab, and `> term`
// with no arguments). $SHELL is the standard on Unix-likes but is rarely
// set on Windows, which uses %COMSPEC% (always set by the OS, normally
// cmd.exe) for the same purpose instead; falling through to the literal
// "/bin/sh" on Windows would try to spawn a path that doesn't exist there.
func defaultShellArgs() []string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return []string{sh}
	}
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		return []string{comspec}
	}
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe"}
	}
	return []string{"/bin/sh"}
}
