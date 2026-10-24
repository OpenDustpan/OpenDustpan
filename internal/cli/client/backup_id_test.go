package client_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
	"opendustpan.org/opendustpan/pkg/backup"
)

// TestBackupCheckNamesAnInvalidID proves that a backup ID with a wrong shape
// gets the error of an invalid ID, not the error of a missing one, and that
// the text says what to set.
func TestBackupCheckNamesAnInvalidID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"BKP-AAA", "bkp-", "bkp-../x"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			err := client.BackupCheck(t.Context(), []string{"--server", "https://x.example", "--backup", id},
				&stdout, &stderr)
			if !errors.Is(err, backup.ErrInvalidID) || errors.Is(err, client.ErrBackupID) {
				t.Fatalf("backup check = %v, want ErrInvalidID and not ErrBackupID", err)
			}

			if strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "set --backup") {
				t.Fatalf("backup check = %v, want a text that says what to set and not that the ID is missing", err)
			}
		})
	}
}
