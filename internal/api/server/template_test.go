package server_test

// Fresh-store template: one migrated database file built once per test
// binary, copied for each test that needs an empty registry. Running every
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

// serverTemplateState holds the once, directory, path and build error of the
// migrated empty database file.
//
//nolint:gochecknoglobals // one template per test binary keeps the migrations out of every test
var serverTemplateState struct {
	once sync.Once
	dir  string
	path string
	err  error
}

// serverTemplate returns the path of the migrated empty database, building
// it once.
func serverTemplate() (string, error) {
	serverTemplateState.once.Do(func() {
		var dir string

		dir, serverTemplateState.err = os.MkdirTemp("", "opendustpan-server-template-*")
		if serverTemplateState.err != nil {
			return
		}

		serverTemplateState.dir = dir

		path := filepath.Join(dir, "template.db")

		var s *sqlite.Store

		s, serverTemplateState.err = sqlite.Open(context.Background(), path)
		if serverTemplateState.err != nil {
			return
		}

		serverTemplateState.err = s.Close()
		if serverTemplateState.err != nil {
			return
		}

		serverTemplateState.path = path
	})

	return serverTemplateState.path, serverTemplateState.err
}

// removeTemplates removes the template directory built for this test binary.
// It skips removal when the template was never built.
func removeTemplates() error {
	if serverTemplateState.dir == "" {
		return nil
	}

	if err := os.RemoveAll(serverTemplateState.dir); err != nil {
		return fmt.Errorf("remove server template: %w", err)
	}

	return nil
}

// copyServerTemplate copies the migrated empty database to dst, with its
// write-ahead log and shared memory files when present.
func copyServerTemplate(t *testing.T, dst string) {
	t.Helper()

	src, err := serverTemplate()
	if err != nil {
		t.Fatalf("build store template: %v", err)
	}

	copyServerFile(t, src, dst)

	for _, ext := range []string{"-wal", "-shm"} {
		if _, statErr := os.Stat(src + ext); statErr == nil {
			copyServerFile(t, src+ext, dst+ext)
		}
	}
}

// copyServerFile copies one file from src to dst.
func copyServerFile(t *testing.T, src, dst string) {
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

// openServerTemplateStore opens a fresh registry store from the template
// copy at path.
func openServerTemplateStore(t *testing.T, path string) *sqlite.Store {
	t.Helper()

	copyServerTemplate(t, path)

	st, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}
