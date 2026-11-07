package launcher_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
)

func TestCopyBinary(t *testing.T) {
	t.Parallel()

	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("binary"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	dest := t.TempDir()

	// A link with the name of the binary points outside the destination:
	// the copy replaces the link and never writes through it.
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := os.Symlink(outside, filepath.Join(dest, launcher.Binary)); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := launcher.CopyBinary(src, dest); err != nil {
		t.Fatalf("CopyBinary: %v", err)
	}

	info, err := os.Lstat(filepath.Join(dest, launcher.Binary))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != launcher.ModeBinary {
		t.Fatalf("copy = %v, %v; want a regular file with mode 0755", info, err)
	}

	if got, err := fs.ReadFile(os.DirFS(filepath.Dir(outside)), "outside"); err != nil || string(got) != "keep" {
		t.Fatalf("the file outside = %q, %v; want it unchanged", got, err)
	}

	cases := []struct{ name, src, dest string }{
		{"relative destination", src, "bin"},
		{"relative source", "src", dest},
		{"missing source", filepath.Join(dest, "none"), dest},
		{"directory as source", dest, dest},
		{"missing destination", src, filepath.Join(dest, "none")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := launcher.CopyBinary(tc.src, tc.dest); !errors.Is(err, launcher.ErrCopy) {
				t.Fatalf("CopyBinary = %v, want ErrCopy", err)
			}
		})
	}
}
