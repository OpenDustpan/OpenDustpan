package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

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
			change: []string{`PRAGMA ignore_check_constraints = ON`, `UPDATE installation SET kind = 'dev ' WHERE id = 1`},
			want:   errDevResetUnknownKind,
		},
		{
			name: "unknown kind", kind: installation.KindDev,
			change: []string{`PRAGMA ignore_check_constraints = ON`, `UPDATE installation SET kind = 'staging' WHERE id = 1`},
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

	opened, err := openStore(ctx, "sqlite:"+filepath.Join(dir, devDBName), slog.New(slog.DiscardHandler), openMigrate, "")
	if err != nil {
		t.Fatalf("open the new database: %v", err)
	}

	defer opened.close()

	st, ok := opened.(sqliteStore)
	if !ok {
		t.Fatal("the new database is no SQLite store")
	}

	kind, err := st.ClaimKind(ctx, installation.KindDev)
	if err != nil || kind != installation.KindDev {
		t.Fatalf("kind of the new database = %q, %v; want dev", kind, err)
	}

	names, err := st.ListNamespaces(ctx)
	if err != nil || len(names) != 0 {
		t.Fatalf("namespaces of the new database = %v, %v; want none", names, err)
	}

	found := false

	for _, entry := range hostServerEntries(ctx, t, st.Store) {
		if entry.Subject != devResetSubject {
			continue
		}

		for _, change := range entry.Changes {
			if change.Key == "dev-reset-kept" && slices.Contains(strings.Split(change.New, ", "), kept) {
				found = true
			}
		}
	}

	if !found {
		t.Fatalf("the new database has no audit entry of the reset that names %s", kept)
	}
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
			!strings.Contains(dev.Error(), devDBName+devResetSuffix) || !errors.Is(dev, cause) {
			t.Fatalf("dev refusal = %q, want the reset command and the kept file", dev)
		}

		serve := schemaRefusal(cause, commandServe, "")
		if strings.Contains(serve.Error(), "reset") || !strings.Contains(serve.Error(), "restore a backup") ||
			!errors.Is(serve, cause) {
			t.Fatalf("serve refusal = %q, want a backup or a new database and no reset", serve)
		}
	}

	other := errors.New("another error")
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
