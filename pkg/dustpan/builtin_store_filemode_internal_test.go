package dustpan

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The startup layer: openSQLiteStore refuses a wide database file with its own
// message, before sqlite.Open runs.
func TestOpenSQLiteStoreRefusesWideFiles(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		file string
		mode os.FileMode
	}{
		"database 0644": {"", 0o644},
		"database 0660": {"", 0o660},
		"wal 0644":      {"-wal", 0o644},
		"shm 0604":      {"-shm", 0o604},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := filepath.Join(t.TempDir(), "dustpan.db")
			makeFiles(t, db, tc.file, tc.mode)

			st, err := openSQLiteStore(t.Context(), db, newStoreConfig(nil))
			if st != nil {
				if closeErr := st.Close(); closeErr != nil {
					t.Errorf("close: %v", closeErr)
				}
			}

			if !errors.Is(err, sqlite.ErrFileMode) {
				t.Fatalf("openSQLiteStore = %v, want ErrFileMode", err)
			}

			if !strings.Contains(err.Error(), "check sqlite database file") {
				t.Fatalf("error %q does not come from the startup check", err)
			}
		})
	}
}

// makeFiles makes the database file with mode 0600 and the file db+suffix with mode.
func makeFiles(t *testing.T, db, suffix string, mode os.FileMode) {
	t.Helper()

	for _, f := range []string{db, db + suffix} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if err := os.Chmod(db+suffix, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}

// The startup layer with the operator opt-out: a wide database file starts the
// store and logs a warning with the file and its mode, and the mode stays.
func TestOpenSQLiteStoreAllowWideFileModeStarts(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")
	makeFiles(t, db, "", 0o644)

	var logs bytes.Buffer

	config := newStoreConfig([]StoreOption{
		WithStoreLogger(slog.New(slog.NewTextHandler(&logs, nil))),
		WithStoreAllowWideFileMode(),
	})

	st, err := openSQLiteStore(t.Context(), db, config)
	if err != nil {
		t.Fatalf("openSQLiteStore with the opt-out: %v", err)
	}

	if closeErr := st.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	if text := logs.String(); !strings.Contains(text, db) || !strings.Contains(text, "mode=0644") {
		t.Fatalf("log = %q, want a warning with the file and mode 0644", text)
	}

	info, statErr := os.Lstat(db)
	if statErr != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("database = %v, %v, want mode 0644: the server must not change it", info, statErr)
	}
}

// shmDirectory puts a directory in place of the -shm file.
func shmDirectory(t *testing.T, db string) {
	t.Helper()

	if err := os.Mkdir(db+"-shm", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

// walLink puts a link in place of the -wal file.
func walLink(t *testing.T, db string) {
	t.Helper()

	target := filepath.Join(filepath.Dir(db), "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := os.Symlink(target, db+"-wal"); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

// The startup layer with the opt-out still refuses a file that is not regular
// and a link in place of a sidecar file.
func TestOpenSQLiteStoreAllowWideFileModeRefusesNonRegularFiles(t *testing.T) {
	t.Parallel()

	for name, setup := range map[string]func(t *testing.T, db string){
		"shm is a directory": shmDirectory,
		"wal is a link":      walLink,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := filepath.Join(t.TempDir(), "dustpan.db")
			makeFiles(t, db, "", 0o644)
			setup(t, db)

			st, err := openSQLiteStore(t.Context(), db, newStoreConfig([]StoreOption{WithStoreAllowWideFileMode()}))
			if st != nil {
				if closeErr := st.Close(); closeErr != nil {
					t.Errorf("close: %v", closeErr)
				}
			}

			if err == nil || !errors.Is(err, sqlite.ErrFileMode) ||
				!strings.Contains(err.Error(), "check sqlite database file") {
				t.Fatalf("openSQLiteStore = %v, want ErrFileMode from the startup check", err)
			}
		})
	}
}
