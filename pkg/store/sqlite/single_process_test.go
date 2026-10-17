package sqlite_test

import (
	"errors"
	"os"
	"path/filepath"
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
	real := filepath.Join(dir, "real.db")

	first, err := sqlite.Open(t.Context(), real)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}

	link := filepath.Join(dir, "link.db")

	if err := os.Symlink(real, link); err != nil {
		_ = first.Close() //nolint:errcheck // The test skips below; the close only frees the handle.

		t.Skipf("create symbolic link: %v", err)
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

	if _, execErr := conn.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version = ?`, newest); execErr != nil {
		_ = conn.Close() //nolint:errcheck // The test fails below; the close only frees the handle.

		t.Fatalf("drop recorded migration: %v", execErr)
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	insertFutureVersion(t, path, newest+1, "")
}
