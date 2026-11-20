package sqlite

// Fresh-store template for the white-box tests: one migrated database file
// built once per test binary, copied for each test that needs an empty
// store. Running every migration for every test dominates the package time
// under the race detector; a copied file migrates once and each open only
// checks the recorded versions.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// internalTemplateState holds the once, directory, path and build error of
// the migrated empty database file.
//
//nolint:gochecknoglobals // one template per test binary keeps the migrations out of every test
var internalTemplateState struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// internalTemplate returns the path of the migrated empty database,
// building it once.
func internalTemplate() (string, error) {
	internalTemplateState.once.Do(func() {
		var dir string

		dir, internalTemplateState.err = os.MkdirTemp("", "opendustpan-sqlite-internal-*")
		if internalTemplateState.err != nil {
			return
		}

		internalTemplateState.dir = dir

		path := filepath.Join(dir, "template.db")

		var s *Store

		s, internalTemplateState.err = Open(context.Background(), path)
		if internalTemplateState.err != nil {
			return
		}

		internalTemplateState.err = s.Close()
		if internalTemplateState.err != nil {
			return
		}

		internalTemplateState.path = path
	})

	return internalTemplateState.path, internalTemplateState.err
}

// copyInternalTemplate copies the migrated empty database to dst, with its
// write-ahead log and shared memory files when present.
func copyInternalTemplate(tb testing.TB, dst string) {
	tb.Helper()

	src, err := internalTemplate()
	if err != nil {
		tb.Fatalf("build store template: %v", err)
	}

	copyInternalFile(tb, src, dst)

	for _, ext := range []string{"-wal", "-shm"} {
		if _, statErr := os.Stat(src + ext); statErr == nil {
			copyInternalFile(tb, src+ext, dst+ext)
		}
	}
}

// copyInternalFile copies one file from src to dst.
func copyInternalFile(tb testing.TB, src, dst string) {
	tb.Helper()

	in, err := os.Open(src) //nolint:gosec // Test copies its own template file to a temporary path.
	if err != nil {
		tb.Fatalf("open store template: %v", err)
	}

	defer func() {
		if closeErr := in.Close(); closeErr != nil {
			tb.Errorf("close store template: %v", closeErr)
		}
	}()

	//nolint:gosec // Test copies its own template file to a temporary path.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		tb.Fatalf("create store copy: %v", err)
	}

	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			tb.Errorf("close store copy: %v", closeErr)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		tb.Fatalf("copy store template: %v", err)
	}
}

// openInternalTemplated opens a fresh store from the template copy in a
// temporary directory.
func openInternalTemplated(tb testing.TB) *Store {
	tb.Helper()

	path := filepath.Join(tb.TempDir(), "dustpan.db")

	copyInternalTemplate(tb, path)

	s, err := Open(tb.Context(), path)
	if err != nil {
		tb.Fatalf("open store: %v", err)
	}

	tb.Cleanup(func() {
		if err := s.Close(); err != nil {
			tb.Errorf("close store: %v", err)
		}
	})

	return s
}
