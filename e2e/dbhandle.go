//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// procSelfFD lists the open file descriptors of the test process on Linux.
const procSelfFD = "/proc/self/fd"

// checkNoOwnSQLiteHandle fails tb when the test process holds the SQLite
// file at dbPath, or one of its -wal, -shm or -journal files, open. The
// binary server owns that file: a handle in the test process makes it a
// second writer. Other platforms have no /proc/self/fd, so the check skips
// there.
func checkNoOwnSQLiteHandle(tb testing.TB, dbPath string) {
	tb.Helper()

	if runtime.GOOS != "linux" {
		return
	}

	want := dbPath
	if dir, err := filepath.EvalSymlinks(filepath.Dir(dbPath)); err == nil {
		want = filepath.Join(dir, filepath.Base(dbPath))
	}

	fds, err := os.ReadDir(procSelfFD)
	if err != nil {
		tb.Fatalf("list open files of the test process: %v", err)
	}

	for _, fd := range fds {
		// A descriptor can close between the list and the read; skip it.
		target, readErr := os.Readlink(filepath.Join(procSelfFD, fd.Name()))
		if readErr != nil {
			continue
		}

		if target == want || strings.HasPrefix(target, want+"-") {
			tb.Errorf("the test process holds %s open while the server binary owns it", target)
		}
	}
}

// writeStopped opens the database of the stopped binary server, runs fn on
// it and closes it, so no handle stays open when the binary starts again.
// It fails the test while the binary runs.
func (e *Env) writeStopped(ctx context.Context, tb testing.TB, fn func(st namespace.Store) error) {
	tb.Helper()

	if e.backendCmd != nil {
		tb.Fatalf("a direct write needs the binary server stopped")
	}

	var (
		st   namespace.Store
		shut func() error
	)

	switch e.store {
	case StoreSQLite:
		opened, err := sqlite.Open(ctx, strings.TrimPrefix(e.backendCfg.dbURL, "sqlite:"))
		if err != nil {
			tb.Fatalf("open the stopped database: %v", err)
		}

		st, shut = opened, opened.Close
	case StorePostgres:
		opened, err := postgres.Open(ctx, e.backendCfg.dbURL)
		if err != nil {
			tb.Fatalf("open the stopped database: %v", err)
		}

		st, shut = opened, func() error { opened.CloseContext(ctx); return nil }
	default:
		tb.Fatalf("unknown store: %s", e.store)
	}

	err := fn(st)

	if closeErr := shut(); closeErr != nil {
		tb.Errorf("close the stopped database: %v", closeErr)
	}

	if err != nil {
		tb.Fatalf("write the stopped database: %v", err)
	}
}
