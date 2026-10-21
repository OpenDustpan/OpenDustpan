package clientgen

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
)

// ErrNotGenerated means that the output directory holds a file with the path
// of a generated file, and that file has no generated header. The writer never
// changes such a file.
var ErrNotGenerated = errors.New("clientgen: the file exists and has no generated header")

// generatedHeader is the standard form of the header of a generated Go file.
var generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// The modes of the files and directories that the writer makes. Generated
// source is public, so its mode is the usual mode of source.
const (
	dirMode  = 0o750
	fileMode = 0o644
)

// headerScanBytes is how far into a file the writer looks for the header.
const headerScanBytes = 1024

// NewWriter returns the Writer that writes into a directory with os.Root.
func NewWriter() Writer { //nolint:ireturn // The port is the contract; callers do not depend on the type.
	return dirWriter{}
}

type dirWriter struct{}

// Write implements Writer.
func (dirWriter) Write(dir string, files []File) error {
	want, err := checkPaths(files)
	if err != nil {
		return err
	}

	if err = os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("make the output directory: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open the output directory: %w", err)
	}

	defer root.Close() //nolint:errcheck // Nothing was written through the close.

	for _, f := range files {
		if err := writeOne(root, f); err != nil {
			return err
		}
	}

	return removeStale(root, want)
}

// checkPaths checks every path before the first write, so that a bad path
// leaves the directory as it was. It returns the set of paths.
func checkPaths(files []File) (map[string]bool, error) {
	want := make(map[string]bool, len(files))

	for _, f := range files {
		// Defense in depth: os.Root also refuses a path outside the directory.
		// This check stands alone, so that a file is never half written.
		if f.Path == "" || strings.ContainsRune(f.Path, '\\') || path.IsAbs(f.Path) ||
			path.Clean(f.Path) != f.Path || f.Path == ".." || strings.HasPrefix(f.Path, "../") {
			return nil, fmt.Errorf("%w: %q", ErrUnsafePath, f.Path)
		}

		if want[f.Path] {
			return nil, fmt.Errorf("%w: %q is in the list two times", ErrUnsafePath, f.Path)
		}

		want[f.Path] = true
	}

	return want, nil
}

// hasHeader says that the content starts with a generated header.
func hasHeader(content []byte) bool {
	if len(content) > headerScanBytes {
		content = content[:headerScanBytes]
	}

	return generatedHeader.Match(content)
}

// writeOne writes one file, unless a file without a header is there.
func writeOne(root *os.Root, f File) error {
	if old, err := root.ReadFile(f.Path); err == nil {
		if !hasHeader(old) {
			return fmt.Errorf("%w: %s", ErrNotGenerated, f.Path)
		}

		if bytes.Equal(old, f.Content) {
			return nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", f.Path, err)
	}

	if dir := path.Dir(f.Path); dir != "." {
		if err := root.MkdirAll(dir, dirMode); err != nil {
			return fmt.Errorf("make the directory of %s: %w", f.Path, err)
		}
	}

	if err := root.WriteFile(f.Path, f.Content, fileMode); err != nil {
		return fmt.Errorf("write %s: %w", f.Path, err)
	}

	return nil
}

// removeStale removes the generated files that the new run does not give. A
// file without a header stays.
func removeStale(root *os.Root, want map[string]bool) error {
	var stale []string

	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || want[p] {
			return walkErr
		}

		content, readErr := root.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", p, readErr)
		}

		if hasHeader(content) {
			stale = append(stale, p)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("look for stale files: %w", err)
	}

	for _, p := range stale {
		if err = root.Remove(p); err != nil {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}

	return nil
}
