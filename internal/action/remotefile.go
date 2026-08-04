package action

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/micro-editor/micro/v2/internal/buffer"
	"github.com/micro-editor/micro/v2/internal/remote"
)

// remoteFileRef records where a local mirror file came from, so it can be
// pushed back to the remote host on save.
type remoteFileRef struct {
	target string
	path   string
}

// remoteMirrors maps a local mirror file's absolute path to the remote
// host/path it was downloaded from. Editing and saving a remote file never
// touches the real remote file directly: it downloads into a local mirror
// file under the OS temp dir, opens that as a completely ordinary local
// buffer (so undo/backups/sudo-save/autosave all keep working unmodified),
// and pushes the new content back over SSH via buffer.OnSave whenever that
// mirror file is saved.
var remoteMirrors = map[string]remoteFileRef{}

func init() {
	buffer.OnSave = func(b *buffer.Buffer) {
		ref, ok := remoteMirrors[b.AbsPath]
		if !ok {
			return
		}
		data, err := os.ReadFile(b.AbsPath)
		if err != nil {
			InfoBar.Error(err)
			return
		}
		if err := remote.WriteFile(ref.target, ref.path, data); err != nil {
			InfoBar.Error("Failed to push to " + ref.target + ": " + err.Error())
			return
		}
		InfoBar.Message("Pushed to " + ref.target + ":" + ref.path)
	}
}

// remoteMirrorPath returns the local mirror path for a remote file, nested
// under the OS temp dir by target so files from different hosts/sessions
// don't collide.
func remoteMirrorPath(target, remotePath string) string {
	safeTarget := strings.NewReplacer("@", "_at_", ":", "_", "/", "_").Replace(target)
	return filepath.Join(os.TempDir(), "micro-remote", safeTarget, remotePath)
}

// openRemoteFileInEditor downloads a remote file to its local mirror path
// and opens that mirror in the active editor pane (see remoteMirrors).
func openRemoteFileInEditor(target, remotePath string) {
	data, err := remote.ReadFile(target, remotePath)
	if err != nil {
		InfoBar.Error(err)
		return
	}

	local := remoteMirrorPath(target, remotePath)
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		InfoBar.Error(err)
		return
	}
	if err := os.WriteFile(local, data, 0644); err != nil {
		InfoBar.Error(err)
		return
	}

	remoteMirrors[local] = remoteFileRef{target: target, path: remotePath}
	openFileInEditor(local)
}
