package fs_test

import (
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/blob"
	"opendustpan.org/opendustpan/blob/blobtest"
	"opendustpan.org/opendustpan/blob/fs"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()

		s, err := fs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return s
	})
}

// TestPingLeavesNoFile proves Ping removes its probe file.
func TestPingLeavesNoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	s, err := fs.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	var files []string

	walkErr := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.IsDir() {
			files = append(files, path)
		}

		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk blob directory: %v", walkErr)
	}

	if len(files) != 0 {
		t.Fatalf("ping left files behind: %v", files)
	}
}
