package sqlite_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// logCapture returns a logger that writes text records to the buffer.
func logCapture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer

	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// With the opt-out, Open starts on a wide database file, logs one warning for
// each wide file with its path and mode, and changes no mode.
func TestOpenAllowWideFileModeStartsAndWarns(t *testing.T) {
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
			chmodFile(t, db, 0o600)
			chmodFile(t, db+tc.file, tc.mode)

			logger, logs := logCapture()

			st, err := sqlite.Open(t.Context(), db, sqlite.WithLogger(logger), sqlite.WithAllowWideFileMode())
			if err != nil {
				t.Fatalf("Open with the opt-out: %v", err)
			}

			// SQLite removes the -wal and -shm files at close, so check them first.
			got := fileMode(t, db+tc.file)

			if err := st.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			text := logs.String()
			if !strings.Contains(text, "allows group or other access") ||
				!strings.Contains(text, db+tc.file) ||
				!strings.Contains(text, fmt.Sprintf("mode=%04o", tc.mode)) {
				t.Fatalf("log = %q, want a warning with the file %q and mode %04o", text, db+tc.file, tc.mode)
			}

			// SQLite recreates -wal and -shm with the mode of the database file,
			// so only the mode of the database file itself stays as it was.
			if tc.file == "" && got != tc.mode {
				t.Fatalf("mode changed to %04o, want %04o: the store must not tighten", got, tc.mode)
			}
		})
	}
}

// Without the opt-out, the same files are refused, and the error names the
// chmod command and the mount permissions.
func TestOpenWithoutAllowWideFileModeRefusesAndNamesTheFix(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")
	chmodFile(t, db, 0o644)

	st, err := sqlite.Open(t.Context(), db)
	closeAfter(t, st)

	if err == nil || !errors.Is(err, sqlite.ErrFileMode) {
		t.Fatalf("Open = %v, want ErrFileMode", err)
	}

	for _, want := range []string{db, "0644", "run chmod 600 " + strconv.Quote(db), "mount permissions", "Docker or Kubernetes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to hold %q", err, want)
		}
	}
}

// closeAfter closes st when the test ends. A nil store has nothing to close.
func closeAfter(t *testing.T, st *sqlite.Store) {
	t.Helper()

	if st == nil {
		return
	}

	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
}

// linkWal puts a link in place of the -wal file.
func linkWal(t *testing.T, db string) {
	t.Helper()

	target := filepath.Join(filepath.Dir(db), "elsewhere")
	chmodFile(t, target, 0o600)

	if err := os.Symlink(target, db+"-wal"); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

// dirShm puts a directory in place of the -shm file.
func dirShm(t *testing.T, db string) {
	t.Helper()

	if err := os.Mkdir(db+"-shm", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

// widenThenLinkWal makes the database file wide and links the -wal file.
func widenThenLinkWal(t *testing.T, db string) {
	t.Helper()

	chmodFile(t, db, 0o644)
	linkWal(t, db)
}

// The opt-out does not relax the refusal of a file that is not regular, of a
// link in place of a sidecar file, or of a database path that is a directory.
func TestOpenAllowWideFileModeStillRefusesNonRegularFiles(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, db string){
		"wal is a link":                         linkWal,
		"shm is a directory":                    dirShm,
		"wal is a link next to a wide database": widenThenLinkWal,
	}

	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := filepath.Join(t.TempDir(), "dustpan.db")
			chmodFile(t, db, 0o600)
			setup(t, db)

			st, err := sqlite.Open(t.Context(), db, sqlite.WithAllowWideFileMode())
			closeAfter(t, st)

			if err == nil || !errors.Is(err, sqlite.ErrFileMode) ||
				!strings.Contains(err.Error(), "is not a regular file") {
				t.Fatalf("Open = %v, want ErrFileMode with the not-regular-file text", err)
			}
		})
	}
}

// The opt-out does not change the mode of a file that the store makes.
func TestOpenAllowWideFileModeStillMakesOwnerOnlyFiles(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")

	st, err := sqlite.Open(t.Context(), db, sqlite.WithAllowWideFileMode())
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

// CheckFileModeAllowWide lists the wide files, passes them, and still fails
// on a file that is not regular.
func TestCheckFileModeAllowWide(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := filepath.Join(dir, "dustpan.db")
	chmodFile(t, db, 0o644)
	chmodFile(t, db+"-shm", 0o660)

	wide, err := sqlite.CheckFileModeAllowWide(db)
	if err != nil {
		t.Fatalf("CheckFileModeAllowWide: %v", err)
	}

	if len(wide) != 2 || wide[0].Path != db || wide[0].Mode != 0o644 ||
		wide[1].Path != db+"-shm" || wide[1].Mode != 0o660 {
		t.Fatalf("wide = %+v, want the database at 0644 and -shm at 0660", wide)
	}

	// The strict check of the same files still fails.
	if err := sqlite.CheckFileMode(db); !errors.Is(err, sqlite.ErrFileMode) {
		t.Fatalf("CheckFileMode = %v, want ErrFileMode", err)
	}

	notFile := filepath.Join(dir, "dir.db")
	if err := os.Mkdir(notFile, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := sqlite.CheckFileModeAllowWide(notFile); err == nil || !errors.Is(err, sqlite.ErrFileMode) {
		t.Fatalf("directory = %v, want ErrFileMode", err)
	}
}

// A database file alone, with mode 0644, gets a -wal and a -shm file from
// SQLite when it connects, with the same mode. Open warns for each of the three
// files, once for each path, though no sidecar file existed before Open.
func TestOpenAllowWideFileModeWarnsForSidecarFilesThatSQLiteMakes(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")
	chmodFile(t, db, 0o644)

	logger, logs := logCapture()

	st, err := sqlite.Open(t.Context(), db, sqlite.WithLogger(logger), sqlite.WithAllowWideFileMode())
	if err != nil {
		t.Fatalf("Open with the opt-out: %v", err)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	text := logs.String()

	for _, name := range []string{db, db + "-wal", db + "-shm"} {
		if got := strings.Count(text, "file="+name+" "); got != 1 {
			t.Fatalf("log has %d warnings for %s, want 1:\n%s", got, name, text)
		}
	}
}
