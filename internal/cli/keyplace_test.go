package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestKeyNextToData proves the check of the place of the key file: the same
// directory as the SQLite file, the blob directory and a directory under it
// warn, a link to a file in the data directory warns too, and another
// directory does not.
func TestKeyNextToData(t *testing.T) {
	t.Parallel()

	data := t.TempDir()
	elsewhere := t.TempDir()

	inData := filepath.Join(data, "dustpan.key")
	inElsewhere := filepath.Join(elsewhere, "dustpan.key")
	blobs := filepath.Join(data, "blobs")
	underBlobs := filepath.Join(blobs, "keys")
	linkToData := filepath.Join(elsewhere, "link.key")

	for _, dir := range []string{blobs, underBlobs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make directory: %v", err)
		}
	}

	for _, path := range []string{inData, inElsewhere, filepath.Join(underBlobs, "a.key")} {
		if err := os.WriteFile(path, []byte("x"), cli.KeyFileMode); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	if err := os.Symlink(inData, linkToData); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}

	database := "sqlite:" + filepath.Join(data, "dustpan.db")

	cases := []struct {
		name, key, database, blobDir string
		want                         bool
	}{
		{"same directory as the SQLite file", inData, database, "", true},
		{"another directory", inElsewhere, database, "", false},
		{"link to a file in the data directory", linkToData, database, "", true},
		{"under the blob directory", filepath.Join(underBlobs, "a.key"), "postgres://h/db", blobs, true},
		{"PostgreSQL, another directory", inElsewhere, "postgres://h/db", blobs, false},
		{"file that does not exist", filepath.Join(data, "missing", "x.key"), database, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := cli.KeyNextToData(tc.key, tc.database, tc.blobDir); got != tc.want {
				t.Fatalf("cli.KeyNextToData(%q, %q, %q) = %t, want %t", tc.key, tc.database, tc.blobDir, got, tc.want)
			}
		})
	}
}

func TestDefaultBlobDir(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, database, flag, want string
	}{
		{"explicit flag wins", "sqlite:/data/dustpan.db", "/custom/blobs", "/custom/blobs"},
		{"sqlite next to file", "sqlite:/data/dustpan.db", "", "/data/blobs"},
		{"sqlite bare filename", "sqlite:dustpan.db", "", "blobs"},
		{"postgres uses working directory", "postgres://user@localhost/db", "", "blobs"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := cli.DefaultBlobDir(tc.database, tc.flag); got != tc.want {
				t.Fatalf("cli.DefaultBlobDir(%q, %q) = %q, want %q", tc.database, tc.flag, got, tc.want)
			}
		})
	}
}
