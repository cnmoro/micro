package action

import (
	"os"
	"path/filepath"

	"github.com/micro-editor/micro/v2/internal/util"
)

// OpenFolder opens the Alt-5 "change working directory" wizard.
func (h *BufPane) OpenFolder() bool {
	openWorkDirWizard()
	return true
}

// OpenFolderCmd is the `openfolder` command: same as Alt-5.
func (h *BufPane) OpenFolderCmd(args []string) {
	if len(args) > 0 {
		changeWorkDir(args[0])
		return
	}
	openWorkDirWizard()
}

// openWorkDirWizard prompts for a new local working directory (folder
// completion via Tab, prefilled with the current one) and switches to it.
func openWorkDirWizard() {
	wd, _ := os.Getwd()
	InfoBar.Prompt("Open folder: ", wd, "OpenFolder", nil, func(resp string, canceled bool) {
		if canceled || resp == "" {
			return
		}
		changeWorkDir(resp)
	})
}

// changeWorkDir changes micro's working directory to path and points the
// Explorer at it, the local equivalent of VS Code's "Open Folder". Any
// active SSH session is disconnected first, since browsing a local folder
// while Docker/terminal are still pointed at a remote host would be
// confusing.
func changeWorkDir(path string) {
	path, err := util.ReplaceHome(path)
	if err != nil {
		InfoBar.Error(err)
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		InfoBar.Error(err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		InfoBar.Error(err)
		return
	}
	if !info.IsDir() {
		InfoBar.Error(abs + " is not a directory")
		return
	}

	if bp := MainTab().CurPane(); bp != nil {
		bp.CdCmd([]string{abs})
	} else if err := os.Chdir(abs); err != nil {
		InfoBar.Error(err)
		return
	}

	if Remote != nil {
		disconnectAllRemote()
	}
	Sidebar.explorer.SetLocal()
	Sidebar.git.Invalidate()
	Sidebar.Show(SidebarExplorerView)
	InfoBar.Message("Working directory: " + abs)
}
