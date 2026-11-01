package datamodel

import (
	"io/fs"
	"path/filepath"
	"sort"
)

// YAMLFiles returns the .yaml files in dir, sorted by path. With recursive set it also
// searches every folder below dir, so a service can keep its models in its own folder.
// Hidden folders (starting with '.') are skipped.
func YAMLFiles(dir string, recursive bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		if d.IsDir() {
			if path == dir { return nil }
			if !recursive || (len(d.Name()) > 1 && d.Name()[0] == '.') { return filepath.SkipDir }
			return nil
		}
		if filepath.Ext(d.Name()) == ".yaml" { out = append(out, path) }
		return nil
	})
	sort.Strings(out)
	return out, err
}
