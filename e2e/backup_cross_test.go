//go:build e2e

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestBackupCrossDialect proves that a portable backup restores into the other
// database (ADR 42, decisions 8 and 9): a backup of SQLite restores into
// PostgreSQL, and a backup of PostgreSQL restores into SQLite. Both CLIs read
// the same state after the restore, the Pulumi secret decrypts, and the history
// is complete, as the exit state checks prove on each side.
func TestBackupCrossDialect(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux; elsewhere it uses the system store.
		t.Skipf("the cross-dialect restore test needs Linux, not %s", runtime.GOOS)
	}

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

	if e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the cross-dialect restore test")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	pulumiVersions := e2e.Pulumi().Versions()
	tofuVersions := e2e.OpenTofu().Versions()

	if len(pulumiVersions) == 0 || len(tofuVersions) == 0 {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiCLIs := installBackupCLIs(installCtx, t, fetcher, e2e.Pulumi(), pulumiVersions)
	tofuCLIs := installBackupCLIs(installCtx, t, fetcher, e2e.OpenTofu(), tofuVersions)

	directions := []struct {
		name     string
		from, to e2e.Store
	}{
		{name: "sqlite to postgres", from: e2e.StoreSQLite, to: e2e.StorePostgres},
		{name: "postgres to sqlite", from: e2e.StorePostgres, to: e2e.StoreSQLite},
	}

	for _, p := range pairBackupCLIs(pulumiCLIs, tofuCLIs) {
		for _, dir := range directions {
			t.Run(dir.name+" "+p.name, func(t *testing.T) {
				t.Parallel()
				runBackupCrossDialect(t, p, dir.from, dir.to)
			})
		}
	}
}

// runBackupCrossDialect backs up a server on from, copies the backup into the
// target of a server on to, restores it there, and proves the exit state of the
// source is the state of the destination. The destination then backs itself up,
// and that backup checks as complete.
func runBackupCrossDialect(t *testing.T, tools backupPair, from, to e2e.Store) {
	t.Helper()

	ctx := t.Context()

	source := e2e.NewEnv(ctx, t, tools.pulumi, from)
	writeExitTestState(ctx, t, source, tools.pulumi, tools.tofu)
	before := captureExitState(ctx, t, source, tools, "before cross backup")

	id := runBackupCommand(ctx, t, source)
	runBackupCheck(ctx, t, source, id)

	dest := e2e.NewEnv(ctx, t, tools.pulumi, to)
	copyBackupTree(t, source.BackupTargetDir(t), dest.BackupTargetDir(t))

	runRestoreCommand(ctx, t, dest, id)
	dest.NoteStep(t, "restore across dialects", "restored "+id+" from "+string(from)+" into "+string(to))

	restored := captureExitState(ctx, t, dest, tools, "after cross restore")
	checkExitStateMatches(t, before, restored)

	again := runBackupCommand(ctx, t, dest)
	runBackupCheck(ctx, t, dest, again)
}

// copyBackupTree copies every object of the backup target src into dst, so the
// destination server reads the backup from its own target. Keys map to paths in
// the directory store, so copying the tree copies the keys.
func copyBackupTree(t *testing.T, src, dst string) {
	t.Helper()

	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return fmt.Errorf("relative path of %s: %w", path, relErr)
		}

		target := filepath.Join(dst, rel)

		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}

		return copyFile(path, target)
	})
	if err != nil {
		t.Fatalf("copy the backup from %s into %s: %v", src, dst, err)
	}
}

// copyFile copies one file, creating its parent directory.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // The test copies the backup it wrote.
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}

	defer func() {
		if closeErr := in.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	if mkErr := os.MkdirAll(filepath.Dir(dst), 0o750); mkErr != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), mkErr)
	}

	out, err := os.Create(dst) //nolint:gosec // The test writes into its own backup target.
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()

	return errors.Join(copyErr, closeErr)
}
