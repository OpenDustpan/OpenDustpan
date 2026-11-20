package sqlite

import (
	"database/sql"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// writeMode makes the file at path with the exact mode, whatever the umask is.
func writeMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	return info.Mode().Perm()
}

// hostileDatabaseFiles lists the states of the three files that the store must
// refuse. The same table drives each layer, so each layer meets each case as if
// the other layers did not exist.
func hostileDatabaseFiles() map[string]func(t *testing.T, db string) {
	wide := func(file string, mode os.FileMode) func(t *testing.T, db string) {
		return func(t *testing.T, db string) {
			t.Helper()

			writeMode(t, db, databaseFileMode)
			writeMode(t, db+file, mode)

			if file == "" {
				if err := os.Chmod(db, mode); err != nil {
					t.Fatalf("chmod: %v", err)
				}
			}
		}
	}

	return map[string]func(t *testing.T, db string){
		"database file 0644":   wide("", 0o644),
		"database file 0640":   wide("", 0o640),
		"database file 0604":   wide("", 0o604),
		"database file 0660":   wide("", 0o660),
		"database file 0666":   wide("", 0o666),
		"wal file 0644":        wide(walSuffix, 0o644),
		"shm file 0640":        wide(shmSuffix, 0o640),
		"wal file 0602":        wide(walSuffix, 0o602),
		"database is a folder": func(t *testing.T, db string) { t.Helper(); mkdir(t, db) },
		"wal is a link": func(t *testing.T, db string) {
			t.Helper()

			writeMode(t, db, databaseFileMode)
			writeMode(t, db+".target", databaseFileMode)

			if err := os.Symlink(db+".target", db+walSuffix); err != nil {
				t.Fatalf("symlink: %v", err)
			}
		},
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()

	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

// Layer: where the path and the mode are chosen.
func TestPrepareDatabaseFileMakesOwnerOnlyFile(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")

	if _, err := prepareDatabaseFile(db, refuseWide); err != nil {
		t.Fatalf("prepareDatabaseFile: %v", err)
	}

	if got := modeOf(t, db); got != databaseFileMode {
		t.Fatalf("mode = %04o, want %04o", got, databaseFileMode)
	}
}

func TestPrepareDatabaseFileRefusesHostileFiles(t *testing.T) {
	t.Parallel()

	for name, setup := range hostileDatabaseFiles() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := filepath.Join(t.TempDir(), "dustpan.db")
			setup(t, db)

			before := snapshotModes(db)

			if _, err := prepareDatabaseFile(db, refuseWide); !errors.Is(err, ErrFileMode) {
				t.Fatalf("prepareDatabaseFile = %v, want ErrFileMode", err)
			}

			// The store tightens nothing: the operator decides.
			if after := snapshotModes(db); !maps.Equal(before, after) {
				t.Fatalf("modes changed: before %v, after %v", before, after)
			}
		})
	}
}

// snapshotModes returns the mode of each of the three database files that exist.
func snapshotModes(db string) map[string]os.FileMode {
	modes := map[string]os.FileMode{}

	for _, f := range databaseFiles(db) {
		if info, err := os.Lstat(f); err == nil {
			modes[f] = info.Mode()
		}
	}

	return modes
}

func TestPrepareDatabaseFileAcceptsOwnerOnlyFiles(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")

	for _, f := range databaseFiles(db) {
		writeMode(t, f, 0o600)
	}

	if _, err := prepareDatabaseFile(db, refuseWide); err != nil {
		t.Fatalf("prepareDatabaseFile: %v", err)
	}
}

// Layer: where the file is opened. openDatabase runs with no Open before it,
// as the restore swap and the reopen run it.
func TestOpenDatabaseRefusesHostileFiles(t *testing.T) {
	t.Parallel()

	for name, setup := range hostileDatabaseFiles() {
		if name == "database is a folder" {
			continue // SQLite refuses a folder by itself; no mode check decides it.
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := filepath.Join(t.TempDir(), "dustpan.db")
			setup(t, db)

			conn, err := openDatabase(t.Context(), db, nil, refuseWide)
			if conn != nil {
				if closeErr := conn.Close(); closeErr != nil {
					t.Errorf("close: %v", closeErr)
				}
			}

			if !errors.Is(err, ErrFileMode) {
				t.Fatalf("openDatabase = %v, want ErrFileMode", err)
			}
		})
	}
}

func TestOpenDatabaseKeepsSidecarFilesOwnerOnly(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")
	writeMode(t, db, databaseFileMode)

	conn, err := openDatabase(t.Context(), db, nil, refuseWide)
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}

	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	if _, err := conn.ExecContext(t.Context(), "CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	for _, f := range databaseFiles(db) {
		if got := modeOf(t, f); got&groupOtherBits != 0 {
			t.Fatalf("%s mode = %04o", filepath.Base(f), got)
		}
	}
}

// Layer: the move of a staged file over the live database.
func TestSwapDatabaseRefusesWideStagedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st := openTestStore(t, filepath.Join(dir, "dustpan.db"))

	staged := filepath.Join(dir, "staged.db")
	writeMode(t, staged, 0o644)

	liveConn := st.conn

	st.mu.Lock()
	err := st.swapDatabaseLocked(t.Context(), staged)
	st.mu.Unlock()

	// A refused swap leaves the live connection untouched: it is not closed
	// and not reopened.
	if st.conn != liveConn {
		t.Fatal("refused swap replaced the live connection")
	}

	if !errors.Is(err, ErrFileMode) {
		t.Fatalf("swapDatabaseLocked = %v, want ErrFileMode", err)
	}

	if got := modeOf(t, st.path); got&groupOtherBits != 0 {
		t.Fatalf("live database mode = %04o", got)
	}

	if _, statErr := os.Stat(staged); statErr != nil {
		t.Fatalf("staged file moved: %v", statErr)
	}

	if err := st.Ping(t.Context()); err != nil {
		t.Fatalf("store unusable after refused swap: %v", err)
	}
}

func TestSnapshotPathIsOwnerOnly(t *testing.T) {
	t.Parallel()

	snap, err := snapshotPath(t.TempDir())
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}

	t.Cleanup(func() { removeSnapshot(snap) })

	if got := modeOf(t, snap); got != databaseFileMode {
		t.Fatalf("snapshot mode = %04o, want %04o", got, databaseFileMode)
	}
}

func TestCaptureIsOwnerOnly(t *testing.T) {
	t.Parallel()

	st := openTestStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	captured, err := st.capture(t.Context())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	t.Cleanup(func() { removeSnapshot(captured) })

	// A reader of the snapshot makes the -wal and -shm files: they stay owner only.
	conn, err := sql.Open("sqlite", "file:"+captured+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	if pingErr := conn.PingContext(t.Context()); pingErr != nil {
		t.Fatalf("ping snapshot: %v", pingErr)
	}

	for _, f := range databaseFiles(captured) {
		if _, statErr := os.Lstat(f); statErr != nil {
			continue
		}

		if got := modeOf(t, f); got&groupOtherBits != 0 {
			t.Fatalf("%s mode = %04o", filepath.Base(f), got)
		}
	}
}

// openTestStore opens a store at path and closes it when the test ends.
func openTestStore(t *testing.T, path string) *Store {
	t.Helper()

	st, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	return st
}

// Layer: the move of a staged file over the live database. The opt-out of the
// operator does not relax it: a store that allows a wide live file still
// refuses a wide staged file, and keeps its live connection.
func TestSwapDatabaseRefusesWideStagedFileWithOptOut(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	live := filepath.Join(dir, "dustpan.db")
	writeMode(t, live, 0o644)

	st, err := Open(t.Context(), live, WithAllowWideFileMode())
	if err != nil {
		t.Fatalf("Open with the opt-out: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	staged := filepath.Join(dir, "staged.db")
	writeMode(t, staged, 0o644)

	liveConn := st.conn

	st.mu.Lock()
	err = st.swapDatabaseLocked(t.Context(), staged)
	st.mu.Unlock()

	if !errors.Is(err, ErrFileMode) {
		t.Fatalf("swapDatabaseLocked = %v, want ErrFileMode", err)
	}

	if st.conn != liveConn {
		t.Fatal("refused swap replaced the live connection")
	}

	if err := st.Ping(t.Context()); err != nil {
		t.Fatalf("store unusable after refused swap: %v", err)
	}
}

// Layer: prepareDatabaseFile with the opt-out. A wide file passes and is
// listed. A new file still gets mode 0600, and a directory still fails.
func TestPrepareDatabaseFileAllowWide(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fresh := filepath.Join(dir, "fresh.db")

	wide, err := prepareDatabaseFile(fresh, listWide)
	if err != nil || len(wide) != 0 {
		t.Fatalf("prepareDatabaseFile(new) = %v, %v, want no wide file", wide, err)
	}

	if got := modeOf(t, fresh); got&groupOtherBits != 0 {
		t.Fatalf("new file mode = %04o, want owner only", got)
	}

	existing := filepath.Join(dir, "existing.db")
	writeMode(t, existing, 0o640)

	wide, err = prepareDatabaseFile(existing, listWide)
	if err != nil || len(wide) != 1 || wide[0].Path != existing || wide[0].Mode != 0o640 {
		t.Fatalf("prepareDatabaseFile(wide) = %v, %v, want the file at 0640", wide, err)
	}

	if got := modeOf(t, existing); got != 0o640 {
		t.Fatalf("mode changed to %04o, want 0640", got)
	}

	notFile := filepath.Join(dir, "dir.db")
	if err := os.Mkdir(notFile, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := prepareDatabaseFile(notFile, listWide); !errors.Is(err, ErrFileMode) {
		t.Fatalf("prepareDatabaseFile(directory) = %v, want ErrFileMode", err)
	}
}

// Layer: openDatabase with the opt-out accepts a wide file and refuses a link
// in place of a sidecar file.
func TestOpenDatabaseAllowWide(t *testing.T) {
	t.Parallel()

	db := filepath.Join(t.TempDir(), "dustpan.db")
	writeMode(t, db, 0o644)

	conn, err := openDatabase(t.Context(), db, nil, listWide)
	if err != nil {
		t.Fatalf("openDatabase with the opt-out: %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := openDatabase(t.Context(), db, nil, refuseWide); !errors.Is(err, ErrFileMode) {
		t.Fatalf("openDatabase without the opt-out = %v, want ErrFileMode", err)
	}

	target := filepath.Join(filepath.Dir(db), "elsewhere")
	writeMode(t, target, 0o600)

	if err := os.Remove(db + "-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove wal: %v", err)
	}

	if err := os.Symlink(target, db+"-wal"); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// SQLite itself refuses to open a -wal link, so the check of the files is
	// tested on its own: it refuses the link even with the opt-out.
	if _, err := inspectFiles(listWide, databaseFiles(db)...); !errors.Is(err, ErrFileMode) {
		t.Fatalf("inspectFiles with a -wal link = %v, want ErrFileMode", err)
	}
}
