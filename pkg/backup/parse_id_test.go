package backup_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestParseIDNeedsMoreThanThePrefix proves that the prefix alone is not a
// backup ID, and that the prefix with one more character is.
func TestParseIDNeedsMoreThanThePrefix(t *testing.T) {
	t.Parallel()

	if _, err := backup.ParseID("bkp-"); !errors.Is(err, backup.ErrInvalidID) {
		t.Fatalf("ParseID(bkp-) = %v, want ErrInvalidID", err)
	}

	if id, err := backup.ParseID("bkp-a"); err != nil || id != "bkp-a" {
		t.Fatalf("ParseID(bkp-a) = %q, %v, want the ID", id, err)
	}
}
