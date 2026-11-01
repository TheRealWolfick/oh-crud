package monitors

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// fsnotify only watches the folders it is given, not the folders below them.
// watchTree adds root and every folder below it, skipping hidden ones.
func watchTree(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		if !d.IsDir() { return nil }
		if path != root && strings.HasPrefix(d.Name(), ".") { return filepath.SkipDir }
		return w.Add(path)
	})
}

// newFolder reports whether event created a folder inside one of roots, which then
// needs watching. Files copied in with the folder arrive before the watch exists, so
// the caller should also load the YAML already inside it.
func newFolder(event fsnotify.Event, roots []string) bool {
	if !event.Has(fsnotify.Create) { return false }
	info, err := os.Stat(event.Name)
	if err != nil || !info.IsDir() || strings.HasPrefix(filepath.Base(event.Name), ".") { return false }
	name := filepath.Clean(event.Name)
	for _, r := range roots {
		if strings.HasPrefix(name, filepath.Clean(r)+string(filepath.Separator)) { return true }
	}
	return false
}
