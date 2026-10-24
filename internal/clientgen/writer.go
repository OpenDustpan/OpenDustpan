package clientgen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

// ErrNotGenerated means that the output directory holds a file with the path
// of a generated file, and that file has no generated header. The writer never
// changes such a file.
var ErrNotGenerated = errors.New("clientgen: the file exists and has no generated header")

// The modes of the files and directories that the writer makes. Generated
// source is public, so its mode is the usual mode of source.
const (
	dirMode  = 0o750
	fileMode = 0o644
)

// headerScanBytes is how far into a file the writer looks for the header.
const headerScanBytes = 1024

// NewWriter returns the Writer that writes into a directory with os.Root.
// The language says which files are generated, because each language has its
// own form of comment.
func NewWriter(lang Language) Writer { //nolint:ireturn // The port is the contract; callers do not depend on the type.
	return dirWriter{lang: lang}
}

type dirWriter struct{ lang Language }

// Write implements Writer.
func (w dirWriter) Write(dir string, files []File) error {
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

	other, err := otherFiles(root, w.lang, want)
	if err != nil {
		return err
	}

	if len(other.stray) > 0 {
		return fmt.Errorf("%w: %s: remove it", ErrStray, strings.Join(other.stray, ", "))
	}

	for _, f := range files {
		if err := writeOne(root, w.lang, f); err != nil {
			return err
		}
	}

	return removeStale(root, w.lang, want)
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

// readHead reads the first bytes of a file, where the header is. It never
// reads the whole file, so a large file that clientgen did not write costs
// no more than a small one.
func readHead(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}

	defer f.Close() //nolint:errcheck // Nothing was written through the close.

	head, err := io.ReadAll(io.LimitReader(f, headerScanBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	return head, nil
}

// writeOne writes one file, unless a file without a header is there.
func writeOne(root *os.Root, lang Language, f File) error {
	if head, err := readHead(root, f.Path); err == nil {
		if !lang.IsGenerated(head) {
			return fmt.Errorf("%w: %s", ErrNotGenerated, f.Path)
		}

		if old, readErr := root.ReadFile(f.Path); readErr == nil && bytes.Equal(old, f.Content) {
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

// skippedDirs are the directories that otherFiles does not enter. A build
// writes them, and a compiler keeps the leading comment of a source file, so a
// compiled file of a generated file also carries the header.
func skippedDirs() []string { return []string{"node_modules", "dist", ".git"} }

// others are the files in the output directory that the new run does not
// give, in the order of the walk.
type others struct {
	// stale files carry the header of the language.
	stale []string
	// stray files have no such header and lie in a path that the language
	// owns.
	stray []string
}

// otherFiles lists the files that the new run does not give. It does not
// enter the directories of skippedDirs.
func otherFiles(root *os.Root, lang Language, want map[string]bool) (others, error) {
	var out others

	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if slices.Contains(skippedDirs(), d.Name()) {
				return fs.SkipDir
			}

			return nil
		}

		if want[p] {
			return nil
		}

		head, readErr := readHead(root, p)
		if readErr != nil {
			return readErr
		}

		switch {
		case lang.IsGenerated(head):
			out.stale = append(out.stale, p)
		case lang.Owned(p):
			out.stray = append(out.stray, p)
		default:
			// A file with no header outside the owned paths is hand-written,
			// and it stays.
		}

		return nil
	})
	if err != nil {
		return others{stale: nil, stray: nil}, fmt.Errorf("look for stale files: %w", err)
	}

	return out, nil
}

// removeStale removes the files that carry the header of the language and
// that the new run does not give. Any other file stays, also a file with the
// header of another generator, and a stray file. It does not enter the
// directories of skippedDirs.
func removeStale(root *os.Root, lang Language, want map[string]bool) error {
	other, err := otherFiles(root, lang, want)
	if err != nil {
		return err
	}

	for _, p := range other.stale {
		if err = root.Remove(p); err != nil {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}

	return nil
}
