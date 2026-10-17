package sqlite_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestSecondOpenFails proves that a second Open of one database file fails
// with ErrInUse while the first store is open, and that Close of the first
// store lets the next Open succeed.
func TestSecondOpenFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	first, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	second, err := sqlite.Open(t.Context(), path)
	if !errors.Is(err, sqlite.ErrInUse) {
		if second != nil {
			_ = second.Close() //nolint:errcheck // The test fails below; the close only frees the handle.
		}

		t.Fatalf("second open: err = %v, want ErrInUse", err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close first: %v", closeErr)
	}

	third, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open after close: %v", err)
	}

	if closeErr := third.Close(); closeErr != nil {
		t.Fatalf("close third: %v", closeErr)
	}
}

// TestSecondOpenOfAnotherPathSpellingFails opens one file through a relative
// path and an absolute path, so the guard does not depend on the spelling.
func TestSecondOpenOfAnotherPathSpellingFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "dustpan.db")

	first, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := first.Close(); closeErr != nil {
			t.Errorf("close first: %v", closeErr)
		}
	})

	other := filepath.Join(dir, "sub", "..", "dustpan.db")

	second, err := sqlite.Open(t.Context(), other)
	if !errors.Is(err, sqlite.ErrInUse) {
		if second != nil {
			_ = second.Close() //nolint:errcheck // The test fails below; the close only frees the handle.
		}

		t.Fatalf("second open through %s: err = %v, want ErrInUse", other, err)
	}
}

// TestSecondOpenThroughSymlinkFails proves that the guard follows symbolic
// links: with a store open on real.db, an Open of link.db, which points at
// real.db, fails with ErrInUse. Closing the first store lets an Open through
// the link succeed.
func TestSecondOpenThroughSymlinkFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "real.db")

	first, err := sqlite.Open(t.Context(), target)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	link := filepath.Join(dir, "link.db")

	if linkErr := os.Symlink(target, link); linkErr != nil {
		_ = first.Close() //nolint:errcheck // The test skips below; the close only frees the handle.

		t.Skipf("create symbolic link: %v", linkErr)
	}

	second, err := sqlite.Open(t.Context(), link)
	if !errors.Is(err, sqlite.ErrInUse) {
		if second != nil {
			_ = second.Close() //nolint:errcheck // The test fails below; the close only frees the handle.
		}

		t.Fatalf("open through %s: err = %v, want ErrInUse", link, err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close first: %v", closeErr)
	}

	third, err := sqlite.Open(t.Context(), link)
	if err != nil {
		t.Fatalf("open through the link after close: %v", err)
	}

	if closeErr := third.Close(); closeErr != nil {
		t.Fatalf("close third: %v", closeErr)
	}
}

// TestSecondOpenThroughDanglingSymlinkFails proves that the guard holds
// through a dangling symbolic link: link.db points at real.db before real.db
// exists. An Open of link.db succeeds, and an Open of real.db while the first
// store is open fails with ErrInUse, because both spell the same file.
func TestSecondOpenThroughDanglingSymlinkFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "real.db")
	link := filepath.Join(dir, "link.db")

	if linkErr := os.Symlink(target, link); linkErr != nil {
		t.Skipf("create symbolic link: %v", linkErr)
	}

	first, err := sqlite.Open(t.Context(), link)
	if err != nil {
		t.Fatalf("first open through the dangling link: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := first.Close(); closeErr != nil {
			t.Errorf("close first: %v", closeErr)
		}
	})

	second, err := sqlite.Open(t.Context(), target)
	if !errors.Is(err, sqlite.ErrInUse) {
		if second != nil {
			_ = second.Close() //nolint:errcheck // The test fails below; the close only frees the handle.
		}

		t.Fatalf("second open of %s: err = %v, want ErrInUse", target, err)
	}
}

// TestOpenThroughLongSymlinkChainFails proves that an Open through a chain of
// more than 40 symbolic links fails with an error instead of looping.
func TestOpenThroughLongSymlinkChainFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	next := filepath.Join(dir, "final.db")

	for i := 40; i >= 0; i-- {
		path := filepath.Join(dir, fmt.Sprintf("link-%02d.db", i))

		if linkErr := os.Symlink(next, path); linkErr != nil {
			t.Skipf("create symbolic link: %v", linkErr)
		}

		next = path
	}

	store, err := sqlite.Open(t.Context(), next)
	if err == nil {
		_ = store.Close() //nolint:errcheck // The test fails below; the close only frees the handle.

		t.Fatal("open through a chain of more than 40 links: err = nil, want an error")
	}

	if errors.Is(err, sqlite.ErrInUse) {
		t.Fatalf("open through a chain of more than 40 links: err = %v, want a resolve error, not ErrInUse", err)
	}
}

// TestOpenRefusesSymlinkAtGuardFile proves that an Open refuses a guard file
// that is a symbolic link: with a link at dustpan.db-lock, Open of dustpan.db
// fails without ErrInUse, leaves the link target unchanged, and leaves the
// link in place.
func TestOpenRefusesSymlinkAtGuardFile(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the guard symlink test needs a POSIX open without link following")
	}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}

	db := filepath.Join(dir, "dustpan.db")
	guard := db + "-lock"
	target := filepath.Join(dir, "target")

	before := []byte("guard target")

	if writeErr := os.WriteFile(target, before, 0o600); writeErr != nil {
		t.Fatalf("write target: %v", writeErr)
	}

	if linkErr := os.Symlink(target, guard); linkErr != nil {
		t.Skipf("create symbolic link: %v", linkErr)
	}

	store, err := sqlite.Open(t.Context(), db)
	if err == nil {
		_ = store.Close() //nolint:errcheck // The test fails below; the close only frees the handle.

		t.Fatal("open with a guard symlink: err = nil, want an error")
	}

	if errors.Is(err, sqlite.ErrInUse) {
		t.Fatalf("open with a guard symlink: err = %v, want a guard error, not ErrInUse", err)
	}

	after, readErr := os.ReadFile(guard) //nolint:gosec // Test reads its own temp file.
	if readErr != nil {
		// Read through the link on purpose: the target must keep its contents.
		t.Fatalf("read target: %v", readErr)
	}

	if !bytes.Equal(after, before) {
		t.Fatalf("target contents = %q, want %q", after, before)
	}

	info, statErr := os.Lstat(guard)
	if statErr != nil {
		t.Fatalf("stat guard link: %v", statErr)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("guard at %s is not a symbolic link", guard)
	}
}

// TestFailedMigrateReleasesGuard proves that an Open whose migration check
// fails releases the guard: a second Open of the same file fails with the
// schema error again, not with ErrInUse.
func TestFailedMigrateReleasesGuard(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)
	divergeSchema(t, path)

	first, err := sqlite.Open(t.Context(), path)
	if !errors.Is(err, schema.ErrSchemaDiverged) {
		if first != nil {
			_ = first.Close() //nolint:errcheck // The test fails below; the close only frees the handle.
		}

		t.Fatalf("first open of diverged schema: err = %v, want ErrSchemaDiverged", err)
	}

	second, err := sqlite.Open(t.Context(), path)
	if second != nil {
		_ = second.Close() //nolint:errcheck // The close only frees the handle; the error decides the test.
	}

	if errors.Is(err, sqlite.ErrInUse) {
		t.Fatalf("second open after a failed migrate: err = %v, want the schema error, not ErrInUse", err)
	}

	if !errors.Is(err, schema.ErrSchemaDiverged) {
		t.Fatalf("second open of diverged schema: err = %v, want ErrSchemaDiverged", err)
	}
}

// divergeSchema records one migration the binary does not embed and drops the
// newest recorded migration the binary embeds, so the next Open finds unknown
// and pending migrations together and fails with ErrSchemaDiverged.
func divergeSchema(t *testing.T, path string) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	var newest int64

	rowErr := conn.QueryRowContext(t.Context(), `SELECT MAX(version) FROM schema_migrations`).Scan(&newest)
	if rowErr != nil {
		_ = conn.Close() //nolint:errcheck // The test fails below; the close only frees the handle.

		t.Fatalf("read newest version: %v", rowErr)
	}

	_, execErr := conn.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version = ?`, newest)
	if execErr != nil {
		_ = conn.Close() //nolint:errcheck // The test fails below; the close only frees the handle.

		t.Fatalf("drop recorded migration: %v", execErr)
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	insertFutureVersion(t, path, newest+1, "")
}
