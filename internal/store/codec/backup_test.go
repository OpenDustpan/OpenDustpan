package codec_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/internal/store/codec"
)

// TestFromBackupRowRefusesABadAttempt proves the decode layer: a stored
// attempt outside the allowlist is corrupt, also when the database CHECK
// would allow it.
func TestFromBackupRowRefusesABadAttempt(t *testing.T) {
	t.Parallel()

	base := codec.BackupRow{
		ID:          "bkp-aaa",
		State:       string(backup.StateRunning),
		Target:      "dir:/var/backups",
		Error:       "",
		Attempt:     "",
		StartedAtMs: codec.Millis(time.Unix(1700000000, 0).UTC()),
		EndedAtMs:   0,
	}

	if _, err := codec.FromBackupRow(base); err != nil {
		t.Fatalf("decode a row with no attempt = %v, want nil", err)
	}

	for _, bad := range []string{
		strings.Repeat("a", 65),
		"../x",
		"a/b",
		"A",
		"a\nb",
	} {
		row := base
		row.Attempt = bad

		_, err := codec.FromBackupRow(row)
		if err == nil {
			t.Fatalf("decode a row with attempt %q succeeded, want corrupt", bad)
		}

		if !errors.Is(err, codec.ErrCorruptRow) {
			t.Fatalf("decode a row with attempt %q = %v, want ErrCorruptRow", bad, err)
		}
	}
}
