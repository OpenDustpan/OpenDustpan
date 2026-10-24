package backup

import (
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
)

// TestMissingObjectTextRefusesKeysThatAreNotBackupObjects sends hostile keys
// straight to the check of the stored text, as if every place that makes the
// error had skipped blob.ParseKey. A key that is not a valid key of a blob or
// of a file of a backup falls back to the text of the sentinel, so the row
// never holds a line break, a path outside the backup or text of any length.
func TestMissingObjectTextRefusesKeysThatAreNotBackupObjects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		key  blob.Key
		want string
	}{
		{"a blob", "blobs/namespaces/ns-a/tofu/ws1/sv1/a1", `backup is incomplete: object ` +
			`"blobs/namespaces/ns-a/tofu/ws1/sv1/a1" is missing: backup data is corrupt`},
		{"a file of a backup", "backups/bkp-a/sql/stacks.jsonl.gz", `backup is incomplete: object ` +
			`"backups/bkp-a/sql/stacks.jsonl.gz" is missing: backup data is corrupt`},
		{"a line break", "blobs/a\nlevel=ERROR msg=forged", ErrCorrupt.Error()},
		{"a parent segment", "blobs/../secrets/key", ErrCorrupt.Error()},
		{"an upper case letter", "blobs/Token", ErrCorrupt.Error()},
		{"another prefix", "uploads/tok-secret", ErrCorrupt.Error()},
		{"an empty key", "", ErrCorrupt.Error()},
		{"a long key", blob.Key("blobs/" + strings.Repeat("a", 600)), ErrCorrupt.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := fmt.Errorf("stage: driver detail: %w", &MissingObjectError{Key: tc.key})
			if got := sanitizeRestoreError(err); got != tc.want {
				t.Fatalf("sanitizeRestoreError = %q, want %q", got, tc.want)
			}
		})
	}
}
