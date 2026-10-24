package backup_test

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestMissingObjectErrorIsCorrupt proves that a missing object matches
// ErrCorrupt, also when it is wrapped, and that its text is the text that the
// backup check answered before the type existed.
func TestMissingObjectErrorIsCorrupt(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("check backup bkp-a: %w", &backup.MissingObjectError{Key: "blobs/a/b"})

	missing, ok := errors.AsType[*backup.MissingObjectError](err)
	if !errors.Is(err, backup.ErrCorrupt) || !ok || missing.Key != "blobs/a/b" {
		t.Fatalf("err = %v, want a *MissingObjectError for blobs/a/b that matches ErrCorrupt", err)
	}

	const want = `check backup bkp-a: backup is incomplete: object "blobs/a/b" is missing: backup data is corrupt`
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}
