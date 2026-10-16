package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

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
