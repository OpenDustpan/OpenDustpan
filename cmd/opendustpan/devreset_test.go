package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// ignoreChecks lets a test write a value that the CHECK constraints refuse.
const ignoreChecks = `PRAGMA ignore_check_constraints = ON`

// errDevResetTestOther is an error that is not a schema refusal.
var errDevResetTestOther = errors.New("another error")

// resetTestDir makes a data directory of dev with a key file and a database
// that the command of kind made. An empty kind leaves the kind unclaimed.
func resetTestDir(t *testing.T, kind installation.Kind) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "trial")
	if _, err := claimDevDir(dir); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if _, err := makeDevKey(filepath.Join(dir, devKeyName)); err != nil {
		t.Fatalf("make key: %v", err)
	}

	st, err := openStore(t.Context(), "sqlite:"+filepath.Join(dir, devDBName),
		slog.New(slog.DiscardHandler), openMigrate, kind)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	if kind != "" {
		if _, claimErr := st.ClaimKind(t.Context(), kind); claimErr != nil {
			t.Fatalf("claim the kind: %v", claimErr)
		}
	}

	st.close()

	return dir
}

// runReset runs dev reset on dir and answers its error and its standard
// error.
func runReset(t *testing.T, dir string) (string, error) {
	t.Helper()

	stderr := newSyncBuffer()
	proc := newDevTestProcess(false).process(t.TempDir(), make(chan string, 1))
	err := devWithProcess(t.Context(), []string{devResetCommand, "--data-dir", dir}, stderr, proc)

	return stderr.String(), err
}

// execDevDB runs the statements on the database of dir, outside the store.
func execDevDB(t *testing.T, dir string, statements ...string) {
	t.Helper()

	conn, err := sql.Open("sqlite", filepath.Join(dir, devDBName))
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	for _, stmt := range statements {
		if _, execErr := conn.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("run %q: %v", stmt, execErr)
		}
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}
}

// devDirEntries answers the names in dir, sorted.
func devDirEntries(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	slices.Sort(names)

	return names
}

// TestDevResetRefusesADatabaseThatIsNotDev sends databases that dev did not
// make, or whose kind dev reset cannot read, to dev reset. Each refusal leaves
// the database in place and keeps no copy.
func TestDevResetRefusesADatabaseThatIsNotDev(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		kind   installation.Kind
		change []string
		want   error
	}{
		{name: "serve", kind: installation.KindProduction, change: nil, want: errDevResetKind},
		{name: "no kind", kind: "", change: nil, want: errDevResetUnknownKind},
		{
			name: "garbled kind", kind: installation.KindDev,
			change: []string{ignoreChecks, `UPDATE installation SET kind = 'dev ' WHERE id = 1`},
			want:   errDevResetUnknownKind,
		},
		{
			name: "unknown kind", kind: installation.KindDev,
			change: []string{ignoreChecks, `UPDATE installation SET kind = 'staging' WHERE id = 1`},
			want:   errDevResetUnknownKind,
		},
		{
			name: "no installation row", kind: installation.KindDev,
			change: []string{`PRAGMA foreign_keys = OFF`, `DELETE FROM installation`},
			want:   errDevResetUnknownKind,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := resetTestDir(t, tc.kind)
			if len(tc.change) > 0 {
				execDevDB(t, dir, tc.change...)
			}

			_, err := runReset(t, dir)
			if !errors.Is(err, tc.want) {
				t.Fatalf("dev reset = %v, want %v", err, tc.want)
			}

			checkNothingKept(t, dir)
		})
	}
}

// checkNothingKept fails the test unless the database of dir is in place and
// dir holds no copy of a reset.
func checkNothingKept(t *testing.T, dir string) {
	t.Helper()

	names := devDirEntries(t, dir)
	if !slices.Contains(names, devDBName) {
		t.Fatalf("files after the refusal = %v, want the database in place", names)
	}

	for _, name := range names {
		if strings.Contains(name, devResetSuffix) {
			t.Fatalf("files after the refusal = %v, want no copy of a reset", names)
		}
	}
}

// TestDevResetRefusesAPathThatIsNotADevDatabase sends a directory without the
// key file of dev, a directory with no database, and a database that is a
// symbolic link to dev reset.
func TestDevResetRefusesAPathThatIsNotADevDatabase(t *testing.T) {
	t.Parallel()

	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatalf("make the directory: %v", err)
	}

	if _, err := runReset(t, foreign); !errors.Is(err, errDevResetNoDir) {
		t.Fatalf("dev reset of a directory with no key file = %v, want %v", err, errDevResetNoDir)
	}

	empty := filepath.Join(t.TempDir(), "trial")
	if _, err := claimDevDir(empty); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if _, err := makeDevKey(filepath.Join(empty, devKeyName)); err != nil {
		t.Fatalf("make key: %v", err)
	}

	if _, err := runReset(t, empty); !errors.Is(err, errDevResetNoDatabase) {
		t.Fatalf("dev reset of a directory with no database = %v, want %v", err, errDevResetNoDatabase)
	}

	serveDir := resetTestDir(t, installation.KindProduction)
	if err := os.Symlink(filepath.Join(serveDir, devDBName), filepath.Join(empty, devDBName)); err != nil {
		t.Fatalf("link: %v", err)
	}

	if _, err := runReset(t, empty); !errors.Is(err, errDevResetNotFile) {
		t.Fatalf("dev reset of a linked database = %v, want %v", err, errDevResetNotFile)
	}

	if _, err := os.Stat(filepath.Join(serveDir, devDBName)); err != nil {
		t.Fatalf("the linked database after the refusal: %v", err)
	}
}

// TestDevResetRefusesADirectoryThatCannotBeStatted checks that a data
// directory whose parent denies search is refused with the error of Lstat,
// not accepted. Root can search any directory, so the test is skipped for it.
func TestDevResetRefusesADirectoryThatCannotBeStatted(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root can search a directory with mode 000")
	}

	parent := filepath.Join(t.TempDir(), "parent")
	dir := filepath.Join(parent, "data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make the directory: %v", err)
	}

	before, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat the parent: %v", err)
	}

	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatalf("deny search: %v", err)
	}

	// Restore the mode before the temporary directory is removed.
	t.Cleanup(func() {
		if err := os.Chmod(parent, before.Mode().Perm()); err != nil {
			t.Errorf("restore the mode: %v", err)
		}
	})

	if _, err := runReset(t, dir); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("dev reset of a directory that cannot be statted = %v, want %v", err, fs.ErrPermission)
	}
}

// TestDevResetLinkCheckFailsClosedOnLstatError makes Lstat fail on a valid
// dev directory. The later checks would accept that directory, so the link
// check must refuse it on its own.
func TestDevResetLinkCheckFailsClosedOnLstatError(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)
	proc := newDevTestProcess(false).process(t.TempDir(), make(chan string, 1))

	if err := checkDevResetDirWithLstat(dir, proc, os.Lstat); err != nil {
		t.Fatalf("check of a valid dev directory = %v, want nil", err)
	}

	failing := func(name string) (fs.FileInfo, error) {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: fs.ErrPermission}
	}

	if err := checkDevResetDirWithLstat(dir, proc, failing); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("check with a failing Lstat = %v, want %v", err, fs.ErrPermission)
	}
}

// TestDevResetKeepsTheOldData resets a dev database twice. Each reset keeps
// the data that it replaces as dev.db.reset.bak, its log files and
// blobs.reset.bak, names
// them in the log line and in the audit entry, and makes an empty database of
// the kind dev. The second reset replaces the copy of the first reset only.
func TestDevResetKeepsTheOldData(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)
	addNamespace(t, dir, "first")

	if err := os.MkdirAll(filepath.Join(dir, devBlobsName), 0o700); err != nil {
		t.Fatalf("make the blob directory: %v", err)
	}

	out, err := runReset(t, dir)
	if err != nil {
		t.Fatalf("first reset: %v", err)
	}

	kept := filepath.Join(dir, devDBName+devResetSuffix)
	if !strings.Contains(out, "dev reset moved the development data aside") || !strings.Contains(out, kept) {
		t.Fatalf("output of the reset = %q, want the log line and the path %s", out, kept)
	}

	checkKeptNamespaces(t, kept, []string{"first"})
	checkResetDatabase(t, dir, kept)

	addNamespace(t, dir, "second")

	if _, err := runReset(t, dir); err != nil {
		t.Fatalf("second reset: %v", err)
	}

	checkKeptNamespaces(t, kept, []string{"second"})
	checkResetDatabase(t, dir, kept)

	var copies []string

	for _, name := range devDirEntries(t, dir) {
		if strings.Contains(name, devResetSuffix) {
			copies = append(copies, name)
		}
	}

	if len(copies) == 0 || copies[0] != devDBName+devResetSuffix || slices.ContainsFunc(copies, func(name string) bool {
		return !strings.HasPrefix(name, devDBName+devResetSuffix) && name != devBlobsName+devResetSuffix
	}) {
		t.Fatalf("copies after the second reset = %v, want the database of the second reset only", copies)
	}
}

// TestDevResetRefusesADirectoryWithAnOldCopy leaves a copy under the old suffix
// in a dev directory. dev reset refuses the directory before it moves any
// data, names the copy, and keeps the database and the copy.
func TestDevResetRefusesADirectoryWithAnOldCopy(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		devDBName + devResetSuffix + devResetOldSuffix,
		devBlobsName + devResetSuffix + devResetOldSuffix,
	} {
		dir := resetTestDir(t, installation.KindDev)
		writeResetFiles(t, dir, map[string]string{name: "last good backup"})

		_, err := runReset(t, dir)
		if err == nil {
			t.Fatalf("reset with the copy %s = nil, want a refusal that names it", name)
		}

		if !errors.Is(err, errDevResetOldCopy) || !strings.Contains(err.Error(), filepath.Join(dir, name)) {
			t.Fatalf("reset with the copy %s = %v, want a refusal that names it", name, err)
		}

		const keepText = "Move each copy out of the data directory to keep it, do not delete it. " +
			"Then run opendustpan dev reset again"
		if !strings.Contains(err.Error(), keepText) || strings.Contains(err.Error(), "remove") {
			t.Fatalf("refusal = %q, want the move-out text and no remove", err)
		}

		if got := readResetFile(t, filepath.Join(dir, name)); got != "last good backup" {
			t.Fatalf("copy %s = %q after the refusal, want it kept", name, got)
		}

		if _, statErr := os.Lstat(filepath.Join(dir, devDBName)); statErr != nil {
			t.Fatalf("database after the refusal: %v, want it in place", statErr)
		}
	}
}

// TestDevResetRefusesASymlinkedOldCopy makes the copy under the old suffix a
// symbolic link to a file that stays. dev reset refuses the directory before
// it moves any data, names the link, and keeps the link and its target.
func TestDevResetRefusesASymlinkedOldCopy(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		devDBName + devResetSuffix + devResetOldSuffix,
		devBlobsName + devResetSuffix + devResetOldSuffix,
	} {
		assertSymlinkedOldCopyRefused(t, name)
	}
}

// assertSymlinkedOldCopyRefused links the copy name to a file outside the data
// directory and checks that dev reset refuses the directory and keeps the link
// and its target.
func assertSymlinkedOldCopyRefused(t *testing.T, name string) {
	t.Helper()

	dir := resetTestDir(t, installation.KindDev)

	target := filepath.Join(t.TempDir(), "backup")
	if err := os.WriteFile(target, []byte("last good backup"), 0o600); err != nil {
		t.Fatalf("write the target of %s: %v", name, err)
	}

	link := filepath.Join(dir, name)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("link %s: %v", name, err)
	}

	_, err := runReset(t, dir)
	if err == nil {
		t.Fatalf("reset with the link %s = nil, want a refusal that names it", name)
	}

	if !errors.Is(err, errDevResetOldCopy) || !strings.Contains(err.Error(), filepath.Join(dir, name)) {
		t.Fatalf("reset with the link %s = %v, want a refusal that names it", name, err)
	}

	info, statErr := os.Lstat(link)
	if statErr != nil {
		t.Fatalf("link %s after the refusal: %v, want it in place", name, statErr)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link %s after the refusal = %v, want a symbolic link", name, info.Mode())
	}

	if got := readResetFile(t, target); got != "last good backup" {
		t.Fatalf("target of %s = %q after the refusal, want it kept", name, got)
	}
}

// errInjectedRename is the error of a rename that a test makes fail.
var errInjectedRename = errors.New("injected rename failure")

// writeResetFiles writes each file of files under dir with its body. A name
// with a slash is a file in a directory of dir.
func writeResetFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		path := filepath.Join(dir, name)

		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("make the directory of %s: %v", path, err)
		}

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

// readResetFile answers the body of the file at path.
func readResetFile(t *testing.T, path string) string {
	t.Helper()

	body, err := fs.ReadFile(os.DirFS(filepath.Dir(path)), filepath.Base(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(body)
}

// resetBeforeFiles answers the data of a dev directory: a new database and blob
// file, and the copies that a reset before kept.
func resetBeforeFiles() map[string]string {
	return map[string]string{
		devDBName:                                 "new db",
		devBlobsName + "/object":                  "new blob",
		devDBName + devResetSuffix:                "old db",
		devBlobsName + devResetSuffix + "/object": "old blob",
	}
}

// TestMoveDevDataKeepsTheCopiesUntilTheNewDatabaseExists proves that the copies
// of the reset before stay under the old suffix while the data moves, and that
// they are deleted only after the new database exists.
func TestMoveDevDataKeepsTheCopiesUntilTheNewDatabaseExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeResetFiles(t, dir, resetBeforeFiles())

	moved, err := moveDevData(dir, os.Rename)
	if err != nil {
		t.Fatalf("move the data aside: %v", err)
	}

	if len(moved) != 2 {
		t.Fatalf("moved = %v, want the database and the blobs", moved)
	}

	oldDB := filepath.Join(dir, devDBName+devResetSuffix+devResetOldSuffix)
	oldBlobs := filepath.Join(dir, devBlobsName+devResetSuffix+devResetOldSuffix)

	if got := readResetFile(t, oldDB); got != "old db" {
		t.Fatalf("copy of the reset before = %q, want the old database", got)
	}

	if err := dropOldDevCopies(dir); err != nil {
		t.Fatalf("drop the copies: %v", err)
	}

	for _, path := range []string{oldDB, oldBlobs} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s after the drop: %v, want it gone", path, err)
		}
	}

	if got := readResetFile(t, filepath.Join(dir, devDBName+devResetSuffix)); got != "new db" {
		t.Fatalf("kept database = %q, want the data that the reset moved", got)
	}
}

// TestMoveDevDataFailedRenameKeepsEveryCopy makes the rename of the blob
// directory fail. The database moved, and every copy of the reset before stays
// under the old suffix. The error names the failed path, the moved path and the
// copies.
func TestMoveDevDataFailedRenameKeepsEveryCopy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeResetFiles(t, dir, resetBeforeFiles())

	failBlobs := func(from, to string) error {
		if filepath.Base(from) == devBlobsName {
			return errInjectedRename
		}

		return os.Rename(from, to)
	}

	moved, err := moveDevData(dir, failBlobs)
	if !errors.Is(err, errInjectedRename) {
		t.Fatalf("move with a failed rename = %v, want the injected error", err)
	}

	failed := filepath.Join(dir, devBlobsName)
	movedDB := filepath.Join(dir, devDBName+devResetSuffix)
	oldDB := movedDB + devResetOldSuffix
	oldBlobs := filepath.Join(dir, devBlobsName+devResetSuffix+devResetOldSuffix)

	if len(moved) != 1 || moved[0] != movedDB {
		t.Fatalf("moved = %v, want %s", moved, movedDB)
	}

	if got := readResetFile(t, oldBlobs+"/object"); got != "old blob" {
		t.Fatalf("copy of the blobs of the reset before = %q, want the old blob", got)
	}

	if got := readResetFile(t, oldDB); got != "old db" {
		t.Fatalf("copy of the database of the reset before = %q, want the old database", got)
	}

	const keepText = "Move each copy out of the data directory to keep it, do not delete it. " +
		"Then run opendustpan dev reset again"

	for _, want := range []string{
		"Failed path: " + failed, "Moved: " + movedDB, "Copies of the reset before: " + oldDB, keepText,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}

	if !strings.Contains(err.Error(), oldBlobs) {
		t.Fatalf("error = %q, want it to name %s", err, oldBlobs)
	}
}

// TestMoveDevDataReplacesAStaleCopy proves that a copy under the old suffix that
// a reset which stopped left behind is replaced by the copy of the reset before.
func TestMoveDevDataReplacesAStaleCopy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := resetBeforeFiles()
	files[devDBName+devResetSuffix+devResetOldSuffix] = "stale db"
	writeResetFiles(t, dir, files)

	if _, err := moveDevData(dir, os.Rename); err != nil {
		t.Fatalf("move the data aside: %v", err)
	}

	oldDB := filepath.Join(dir, devDBName+devResetSuffix+devResetOldSuffix)
	if got := readResetFile(t, oldDB); got != "old db" {
		t.Fatalf("copy of the reset before = %q, want the old database and not the stale copy", got)
	}
}

// TestFailedRemakeKeepsTheCopiesOfTheResetBefore makes the remake of the
// database fail. The copies of the reset before stay, and the error names them.
func TestFailedRemakeKeepsTheCopiesOfTheResetBefore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeResetFiles(t, dir, resetBeforeFiles())

	moved, err := moveDevData(dir, os.Rename)
	if err != nil {
		t.Fatalf("move the data aside: %v", err)
	}

	proc := newDevTestProcess(false).process(t.TempDir(), make(chan string, 1))

	remakeErr := remakeDevDatabase(t.Context(), filepath.Join(dir, "missing", devDBName),
		slog.New(slog.DiscardHandler), proc, moved)
	if remakeErr == nil {
		t.Fatal("remake of a database in a missing directory succeeded")
	}

	err = keptCopiesError(dir, remakeErr)
	if !errors.Is(err, remakeErr) {
		t.Fatalf("error = %v, want it to wrap the remake error", err)
	}

	for _, name := range []string{devDBName, devBlobsName} {
		old := filepath.Join(dir, name+devResetSuffix+devResetOldSuffix)
		if !strings.Contains(err.Error(), old) {
			t.Fatalf("error = %q, want it to name %s", err, old)
		}

		if _, statErr := os.Lstat(old); statErr != nil {
			t.Fatalf("copy %s after the failed remake: %v, want it kept", old, statErr)
		}
	}
}

// errInjectedRemake is the error of a remake that a test makes fail.
var errInjectedRemake = errors.New("injected remake failure")

// TestFinishDevResetFailedRemakeKeepsCopies makes the remake of the database
// fail after the data moved aside. The copies of the reset before stay, so that
// the error names them and wraps the remake error. The copies are deleted only
// after a successful remake: deleting them first would fail the content check.
func TestFinishDevResetFailedRemakeKeepsCopies(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)
	writeResetFiles(t, dir, map[string]string{
		devDBName + devResetSuffix:                "old db",
		devBlobsName + devResetSuffix + "/object": "old blob",
	})

	moved, err := moveDevData(dir, os.Rename)
	if err != nil {
		t.Fatalf("move the data aside: %v", err)
	}

	failRemake := func(context.Context, string, *slog.Logger, devProcess, []string) error {
		return errInjectedRemake
	}

	proc := newDevTestProcess(false).process(t.TempDir(), make(chan string, 1))

	err = finishDevReset(t.Context(), devFinish{
		dir: dir, db: filepath.Join(dir, devDBName), moved: moved, stderr: io.Discard,
		logger: slog.New(slog.DiscardHandler), proc: proc,
	}, failRemake)
	if err == nil {
		t.Fatal("finish with a failed remake = nil, want it to wrap the remake error")
	}

	if !errors.Is(err, errInjectedRemake) {
		t.Fatalf("finish with a failed remake = %v, want it to wrap the remake error", err)
	}

	oldDB := filepath.Join(dir, devDBName+devResetSuffix+devResetOldSuffix)
	oldBlobs := filepath.Join(dir, devBlobsName+devResetSuffix+devResetOldSuffix)

	for _, want := range []string{oldDB, oldBlobs} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to name %s", err, want)
		}
	}

	if got := readResetFile(t, oldDB); got != "old db" {
		t.Fatalf("copy of the database after the failed remake = %q, want the old database", got)
	}

	if got := readResetFile(t, oldBlobs+"/object"); got != "old blob" {
		t.Fatalf("copy of the blobs after the failed remake = %q, want the old blob", got)
	}
}

// addNamespace makes the namespace name in the dev database of dir.
func addNamespace(t *testing.T, dir, name string) {
	t.Helper()

	st, err := openStore(t.Context(), "sqlite:"+filepath.Join(dir, devDBName),
		slog.New(slog.DiscardHandler), openMigrate, "")
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	defer st.close()

	svc, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, err := svc.Create(t.Context(), namespace.Name(name)); err != nil {
		t.Fatalf("make the namespace %s: %v", name, err)
	}
}

// checkKeptNamespaces fails the test unless the kept database at path holds
// exactly the namespaces want.
func checkKeptNamespaces(t *testing.T, path string, want []string) {
	t.Helper()

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open the kept database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close the kept database: %v", closeErr)
		}
	}()

	rows, err := conn.QueryContext(t.Context(), `SELECT name FROM namespaces ORDER BY name`)
	if err != nil {
		t.Fatalf("read the kept namespaces: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows: %v", closeErr)
		}
	}()

	var got []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("read a kept namespace: %v", scanErr)
		}

		got = append(got, name)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read the kept namespaces: %v", rowsErr)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("kept namespaces = %v, want %v", got, want)
	}
}

// checkResetDatabase fails the test unless the new database of dir holds the
// kind dev, no namespace, and the audit entry of the reset that names kept.
func checkResetDatabase(t *testing.T, dir, kept string) {
	t.Helper()

	ctx := context.WithoutCancel(t.Context())

	st, err := sqlite.Open(ctx, filepath.Join(dir, devDBName), sqlite.WithLogger(slog.New(slog.DiscardHandler)),
		sqlite.WithBinaryVersion(version), sqlite.WithInstallationKind(""))
	if err != nil {
		t.Fatalf("open the new database: %v", err)
	}

	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the new database: %v", closeErr)
		}
	}()

	kind, err := st.ClaimKind(ctx, installation.KindDev)
	if err != nil || kind != installation.KindDev {
		t.Fatalf("kind of the new database = %q, %v; want dev", kind, err)
	}

	names, err := st.ListNamespaces(ctx)
	if err != nil || len(names) != 0 {
		t.Fatalf("namespaces of the new database = %v, %v; want none", names, err)
	}

	if !namesKept(hostServerEntries(ctx, t, st), kept) {
		t.Fatalf("the new database has no audit entry of the reset that names %s", kept)
	}
}

// namesKept reports whether an audit entry of a reset in entries names the
// kept path.
func namesKept(entries []audit.Entry, kept string) bool {
	for i := range entries {
		if entries[i].Subject != devResetSubject {
			continue
		}

		for _, change := range entries[i].Changes {
			if change.Key == "dev-reset-kept" && slices.Contains(strings.Split(change.New, ", "), kept) {
				return true
			}
		}
	}

	return false
}

// TestSchemaRefusalNamesTheStepOfTheCommand proves that the refusal of a
// database with another schema names dev reset for dev, and never for serve.
func TestSchemaRefusalNamesTheStepOfTheCommand(t *testing.T) {
	t.Parallel()

	for _, cause := range []error{
		fmt.Errorf("check schema: %w", schema.ErrSchemaDiverged),
		fmt.Errorf("check schema versions: %w", schema.ErrSchemaBeforeBaseline),
	} {
		dev := schemaRefusal(cause, commandDev, "/tmp/the trial")
		if !strings.Contains(dev.Error(), "run opendustpan dev reset --data-dir '/tmp/the trial' to start again") ||
			!strings.Contains(dev.Error(), "moves the old data aside") ||
			!strings.Contains(dev.Error(), devDBName+devResetSuffix) || !errors.Is(dev, cause) ||
			strings.Contains(dev.Error(), "is kept as") {
			t.Fatalf("dev refusal = %q, want the reset command and the move of the old data", dev)
		}

		serve := schemaRefusal(cause, commandServe, "")
		if strings.Contains(serve.Error(), "reset") || !strings.Contains(serve.Error(), "restore a backup") ||
			!errors.Is(serve, cause) {
			t.Fatalf("serve refusal = %q, want a backup or a new database and no reset", serve)
		}
	}

	other := errDevResetTestOther
	if got := schemaRefusal(other, commandDev, ""); !errors.Is(got, other) || got.Error() != other.Error() {
		t.Fatalf("schemaRefusal of another error = %v, want it unchanged", got)
	}
}

// TestDevRefusesAChangedSchemaWithTheResetCommand starts dev on a dev
// database whose recorded baseline differs from the binary, and proves that
// dev refuses it and names dev reset.
func TestDevRefusesAChangedSchemaWithTheResetCommand(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)
	execDevDB(t, dir, `UPDATE schema_migrations SET sha256 = '`+strings.Repeat("0", 64)+`'`)

	_, err := startDev(t, newDevTestProcess(false), t.TempDir(), "--data-dir", dir, "--listen", "127.0.0.1:0")
	if !errors.Is(err, schema.ErrSchemaDiverged) || !strings.Contains(err.Error(), "run opendustpan dev reset") {
		t.Fatalf("dev on a changed schema = %v, want a refusal that names dev reset", err)
	}
}

// TestDevResetRefusesADirectoryOfALiveServer proves that dev reset changes
// nothing while dev.addr names a process that runs: the test process.
func TestDevResetRefusesADirectoryOfALiveServer(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)

	addr := devAddr{Started: time.Now().UTC(), Addr: "127.0.0.1:1", Cert: "", PID: os.Getpid()}
	if err := writeDevAddr(dir, addr); err != nil {
		t.Fatalf("write dev.addr: %v", err)
	}

	if _, err := runReset(t, dir); !errors.Is(err, errDevResetLive) {
		t.Fatalf("dev reset with a live server = %v, want %v", err, errDevResetLive)
	}

	checkNothingKept(t, dir)
}

// TestDevResetRefusesAHardLinkOfTheDatabase links the database of serve as
// dev.db. The link-count layer refuses it, and the database of serve stays.
func TestDevResetRefusesAHardLinkOfTheDatabase(t *testing.T) {
	t.Parallel()

	serveDir := resetTestDir(t, installation.KindProduction)
	dir := resetTestDir(t, installation.KindDev)

	if err := os.Remove(filepath.Join(dir, devDBName)); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if err := os.Link(filepath.Join(serveDir, devDBName), filepath.Join(dir, devDBName)); err != nil {
		t.Fatalf("link: %v", err)
	}

	if _, err := runReset(t, dir); !errors.Is(err, errDevResetHardLink) {
		t.Fatalf("dev reset of a hard link = %v, want %v", err, errDevResetHardLink)
	}

	checkNothingKept(t, dir)
	checkNothingKept(t, serveDir)
}

// TestDevResetRefusesALinkedBlobDirectory makes blobs a symbolic link to
// another directory. The blob layer refuses it.
func TestDevResetRefusesALinkedBlobDirectory(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)

	if err := os.Symlink(t.TempDir(), filepath.Join(dir, devBlobsName)); err != nil {
		t.Fatalf("link: %v", err)
	}

	if _, err := runReset(t, dir); !errors.Is(err, errDevResetBlobs) {
		t.Fatalf("dev reset with a linked blob directory = %v, want %v", err, errDevResetBlobs)
	}

	checkNothingKept(t, dir)
}

// TestDevResetRefusesAnOpenBlobDirectory makes a blob directory that the group
// can read. The blob layer refuses it.
func TestDevResetRefusesAnOpenBlobDirectory(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)

	if err := os.Mkdir(filepath.Join(dir, devBlobsName), 0o750); err != nil {
		t.Fatalf("make the blob directory: %v", err)
	}

	if _, err := runReset(t, dir); !errors.Is(err, errDevResetBlobs) {
		t.Fatalf("dev reset with an open blob directory = %v, want %v", err, errDevResetBlobs)
	}

	checkNothingKept(t, dir)
}

// TestDevResetRefusesALinkedDataDirectory passes a symbolic link of a data
// directory as --data-dir. The directory layer refuses it.
func TestDevResetRefusesALinkedDataDirectory(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)

	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, linked); err != nil {
		t.Fatalf("link: %v", err)
	}

	if _, err := runReset(t, linked); !errors.Is(err, errDevResetLinkedDir) {
		t.Fatalf("dev reset of a linked data directory = %v, want %v", err, errDevResetLinkedDir)
	}

	checkNothingKept(t, dir)
}

// TestDevResetRefusesADatabaseInUse holds the database open in a store, with
// no dev.addr, and proves that dev reset says that a server holds it.
func TestDevResetRefusesADatabaseInUse(t *testing.T) {
	t.Parallel()

	dir := resetTestDir(t, installation.KindDev)

	st, err := openStore(t.Context(), "sqlite:"+filepath.Join(dir, devDBName),
		slog.New(slog.DiscardHandler), openMigrate, installation.KindDev)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	defer st.close()

	if _, err := runReset(t, dir); !errors.Is(err, errDevResetInUse) {
		t.Fatalf("dev reset of a database in use = %v, want %v", err, errDevResetInUse)
	}

	checkNothingKept(t, dir)
}
