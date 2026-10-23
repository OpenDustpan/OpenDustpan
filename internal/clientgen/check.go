package clientgen

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
)

// Diff compares generated files with the files in a directory, and writes
// nothing. It returns one line for each difference, in the order of the paths:
// a file that is missing, a file with other content, and a file with the
// header of the language that the new run does not give. A file without the
// header is not a difference, as in Write. A directory that does not exist
// has every file missing. The result is empty when Write would change nothing.
func Diff(lang Language, dir string, files []File) ([]string, error) {
	want, err := checkPaths(files)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return missingAll(files), nil
	}

	if err != nil {
		return nil, fmt.Errorf("open the output directory: %w", err)
	}

	defer root.Close() //nolint:errcheck // Nothing was written through the close.

	var out []string

	for _, f := range files {
		line, diffErr := diffOne(root, f)
		if diffErr != nil {
			return nil, diffErr
		}

		if line != "" {
			out = append(out, line)
		}
	}

	stale, err := staleFiles(root, lang, want)
	if err != nil {
		return nil, err
	}

	for _, p := range stale {
		out = append(out, "stale: "+p)
	}

	slices.Sort(out)

	return out, nil
}

func missingAll(files []File) []string {
	out := make([]string, 0, len(files))

	for _, f := range files {
		out = append(out, "missing: "+f.Path)
	}

	slices.Sort(out)

	return out
}

// diffOne compares one file with the file on disk.
func diffOne(root *os.Root, f File) (string, error) {
	old, err := root.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return "missing: " + f.Path, nil
	}

	if err != nil {
		return "", fmt.Errorf("read %s: %w", f.Path, err)
	}

	if !bytes.Equal(old, f.Content) {
		return "differs: " + f.Path, nil
	}

	return "", nil
}
