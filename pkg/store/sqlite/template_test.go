package sqlite_test

// Fresh-store template: one migrated database file built once per test
// binary, copied for each test that needs an empty store. Running every
// migration for every test dominates the package time under the race
// detector; a copied file migrates once and each open only checks the
// recorded versions.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// templateState holds the once, directory, path and build error of the
// migrated empty database file.
//
//nolint:gochecknoglobals // one template per test binary keeps the migrations out of every test
var templateState struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// migratedTemplatePath returns the path of the migrated empty database,
// building it once.
func migratedTemplatePath() (string, error) {
	templateState.once.Do(func() {
		var dir string

		dir, templateState.err = os.MkdirTemp("", "opendustpan-sqlite-template-*")
		if templateState.err != nil {
			return
		}

		templateState.dir = dir

		path := filepath.Join(dir, "template.db")

		var s *sqlite.Store

		s, templateState.err = sqlite.Open(context.Background(), path)
		if templateState.err != nil {
			return
		}

		templateState.err = s.Close()
		if templateState.err != nil {
			return
		}

		templateState.path = path
	})

	return templateState.path, templateState.err
}

// removeTemplates removes the template directory built for this test binary.
// It skips removal when the template was never built.
func removeTemplates() error {
	if templateState.dir == "" {
		return nil
	}

	if err := os.RemoveAll(templateState.dir); err != nil {
		return fmt.Errorf("remove store template: %w", err)
	}

	return nil
}

// copyTemplate copies the migrated empty database to dst, with its
// write-ahead log and shared memory files when present, so the copy opens
// exactly as the template closed.
func copyTemplate(t *testing.T, dst string) {
	t.Helper()

	src, err := migratedTemplatePath()
	if err != nil {
		t.Fatalf("build store template: %v", err)
	}

	copyTemplateFile(t, src, dst)

	for _, ext := range []string{"-wal", "-shm"} {
		if _, statErr := os.Stat(src + ext); statErr == nil {
			copyTemplateFile(t, src+ext, dst+ext)
		}
	}
}

// copyTemplateFile copies one file from src to dst.
func copyTemplateFile(t *testing.T, src, dst string) {
	t.Helper()

	in, err := os.Open(src) //nolint:gosec // Test copies its own template file to a temporary path.
	if err != nil {
		t.Fatalf("open store template: %v", err)
	}

	defer func() {
		if closeErr := in.Close(); closeErr != nil {
			t.Errorf("close store template: %v", closeErr)
		}
	}()

	//nolint:gosec // Test copies its own template file to a temporary path.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("create store copy: %v", err)
	}

	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			t.Errorf("close store copy: %v", closeErr)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy store template: %v", err)
	}
}

// openTemplated opens a fresh store from the template copy in a temporary
// directory.
func openTemplated(t *testing.T) *sqlite.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	return openPath(t, path)
}

// openPath opens a store on the file at path. Each call opens its own
// handle, so two calls give two handles on one file.
func openPath(t *testing.T, path string) *sqlite.Store {
	t.Helper()

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return s
}
