package errcodetest

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// SourceDirs returns each subdirectory of root, at any depth, that holds a Go
// file that is not a test, as a path relative to root, sorted. It does not
// look in a directory named testdata, which the go tool does not build.
func SourceDirs(root string) ([]string, error) {
	var out []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}

			return nil
		}

		dir := filepath.Dir(path)
		if dir == filepath.Clean(root) || !strings.HasSuffix(d.Name(), ".go") ||
			strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}

		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", dir, err)
		}

		if !slices.Contains(out, rel) {
			out = append(out, rel)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}

	slices.Sort(out)

	return out, nil
}
