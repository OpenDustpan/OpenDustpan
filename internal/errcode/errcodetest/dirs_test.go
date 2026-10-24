package errcodetest_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

func TestSourceDirs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files []string
		want  []string
	}{
		{"no subdirectory", []string{"adapter.go", "adapter_test.go"}, nil},
		{"a package", []string{"adapter.go", "helper/helper.go"}, []string{"helper"}},
		{"a test-only package", []string{"adapter.go", "e2e/e2e_test.go"}, nil},
		{"a package at depth", []string{"a/b/c.go", "a/b/d.go"}, []string{filepath.Join("a", "b")}},
		{"test data", []string{"testdata/fixture/fixture.go"}, nil},
		{"no Go file", []string{"docs/readme.md"}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := writeFiles(t, tc.files)

			got, err := errcodetest.SourceDirs(root)
			if err != nil {
				t.Fatalf("SourceDirs: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("dirs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSourceDirsRefusesAMissingRoot(t *testing.T) {
	t.Parallel()

	if _, err := errcodetest.SourceDirs(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("SourceDirs accepted a missing root")
	}
}

// writeFiles writes each file of files, a path relative to a new directory,
// and returns the directory.
func writeFiles(t *testing.T, files []string) string {
	t.Helper()

	root := t.TempDir()

	for _, f := range files {
		path := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if err := os.WriteFile(path, []byte("package x\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	return root
}
