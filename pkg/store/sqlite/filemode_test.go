package sqlite_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	return info.Mode().Perm()
}

func chmodFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func TestOpenMakesOwnerOnlyFiles(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")

	st, err := sqlite.Open(t.Context(), db)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	for _, name := range []string{db, db + "-wal", db + "-shm", db + "-lock"} {
		if got := fileMode(t, name); got&0o077 != 0 {
			t.Errorf("%s mode = %04o, want no group or other bits", filepath.Base(name), got)
		}
	}
}

func TestOpenRefusesWideFiles(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		file string
		mode os.FileMode
	}{
		"database 0644": {"", 0o644},
		"database 0640": {"", 0o640},
		"database 0606": {"", 0o606},
		"wal 0644":      {"-wal", 0o644},
		"shm 0660":      {"-shm", 0o660},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := filepath.Join(t.TempDir(), "dustpan.db")
			chmodFile(t, db, 0o600)
			chmodFile(t, db+tc.file, tc.mode)

			requireFileModeRefused(t, db, tc.file, tc.mode)

			// The refusal releases the guard: the operator fixes the mode and opens at once.
			if err := os.Chmod(db+tc.file, 0o600); err != nil {
				t.Fatalf("chmod: %v", err)
			}

			again, err := sqlite.Open(t.Context(), db)
			if err != nil {
				t.Fatalf("Open after chmod 600: %v", err)
			}

			if err := again.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})

		t.Run(name+" without migrate", func(t *testing.T) {
			t.Parallel()

			db := filepath.Join(t.TempDir(), "dustpan.db")
			chmodFile(t, db, 0o600)
			chmodFile(t, db+tc.file, tc.mode)

			requireFileModeRefused(t, db, tc.file, tc.mode, sqlite.WithoutMigrate())
		})
	}
}

// requireFileModeRefused opens db and requires ErrFileMode with the mode of file unchanged.
func requireFileModeRefused(t *testing.T, db, file string, mode os.FileMode, opts ...sqlite.Option) {
	t.Helper()

	st, err := sqlite.Open(t.Context(), db, opts...)
	if st != nil {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}

	if !errors.Is(err, sqlite.ErrFileMode) {
		t.Fatalf("Open = %v, want ErrFileMode", err)
	}

	if got := fileMode(t, db+file); got != mode {
		t.Fatalf("mode changed to %04o, want %04o: the store must not tighten", got, mode)
	}
}

func TestCheckFileMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := sqlite.CheckFileMode(filepath.Join(dir, "absent.db")); err != nil {
		t.Fatalf("absent file: %v", err)
	}

	wide := filepath.Join(dir, "wide.db")
	chmodFile(t, wide, 0o644)

	err := sqlite.CheckFileMode(wide)
	if err == nil || !errors.Is(err, sqlite.ErrFileMode) {
		t.Fatalf("wide file = %v, want ErrFileMode", err)
	}

	if want := "run chmod 600 " + wide; !strings.Contains(err.Error(), want) {
		t.Fatalf("wide file error = %q, want it to hold %q", err, want)
	}

	// A link to a wide file is followed, so the link cannot hide the mode.
	link := filepath.Join(dir, "link.db")
	if linkErr := os.Symlink(wide, link); linkErr != nil {
		t.Fatalf("symlink: %v", linkErr)
	}

	if err = sqlite.CheckFileMode(link); !errors.Is(err, sqlite.ErrFileMode) {
		t.Fatalf("link to wide file = %v, want ErrFileMode", err)
	}

	narrow := filepath.Join(dir, "narrow.db")
	chmodFile(t, narrow, 0o600)
	chmodFile(t, narrow+"-wal", 0o644)

	if err = sqlite.CheckFileMode(narrow); !errors.Is(err, sqlite.ErrFileMode) {
		t.Fatalf("wide wal = %v, want ErrFileMode", err)
	}
}

// TestCheckFileModeNotRegular requires a text for a file that is not regular
// that names the failure and gives no chmod command.
func TestCheckFileModeNotRegular(t *testing.T) {
	t.Parallel()

	notFile := filepath.Join(t.TempDir(), "dir.db")
	if err := os.Mkdir(notFile, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := sqlite.CheckFileMode(notFile)
	if err == nil || !errors.Is(err, sqlite.ErrFileMode) {
		t.Fatalf("directory = %v, want ErrFileMode", err)
	}

	if !strings.Contains(err.Error(), "is not a regular file: replace it with the database file or remove it") ||
		strings.Contains(err.Error(), "chmod") {
		t.Fatalf("directory error = %q", err)
	}
}
