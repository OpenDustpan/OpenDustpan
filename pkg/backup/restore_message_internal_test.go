package backup

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

// TestSecretRestoreMessageNamesTheServerKey proves that the stored text of a
// restore that the key of the server cannot open tells the operator to start
// the server with the key of the backup. The restore command does not read the
// key, so the text must not ask to set it in the shell of the command.
func TestSecretRestoreMessageNamesTheServerKey(t *testing.T) {
	t.Parallel()

	const start = "start the server with the key that made the backup " +
		"(DUSTPAN_KEY_ENCRYPTION_KEY or DUSTPAN_KEY_ENCRYPTION_KEY_FILE)"

	for _, tc := range []struct {
		err  error
		want string
	}{
		{err: secret.ErrKeyNotConfigured, want: "the server has no key-encryption key to open the backup: " + start},
		{err: secret.ErrWrapperMismatch, want: "the key of this server does not open the backup: " + start},
		{err: secret.ErrDecrypt, want: "the key of this server does not open the backup: " + start},
	} {
		if got, ok := secretRestoreMessage(fmt.Errorf("restore: %w", tc.err)); !ok || got != tc.want {
			t.Errorf("secretRestoreMessage(%v) = %q, %v, want %q", tc.err, got, ok, tc.want)
		}
	}
}
