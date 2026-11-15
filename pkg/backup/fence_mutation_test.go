package backup_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestCheckRefusesHostileAttemptManifest is the public mutation test of the
// attempt validation: a manifest that names an attempt outside the allowlist,
// or another prefix than the one that holds it, never counts. Removing the
// attempt validation from the manifest read lets such a manifest through and
// fails this test.
func TestCheckRefusesHostileAttemptManifest(t *testing.T) {
	t.Parallel()

	for _, attempt := range []string{"../evil", "1/2", "", "2"} {
		t.Run("attempt "+attempt, func(t *testing.T) {
			t.Parallel()

			fix := checkedBackup(t)
			key := attemptManifestKey(t, fix.target, "bkp-sum")

			var manifest map[string]any
			if err := json.Unmarshal(fix.target.readObject(t, key), &manifest); err != nil {
				t.Fatalf("decode the manifest: %v", err)
			}

			manifest["attempt"] = attempt

			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("encode the manifest: %v", err)
			}

			mustDelete(t, fix.target, key)
			fix.target.storeObject(t, key, data)

			err = backup.Check(t.Context(), fix.target, "bkp-sum")
			if !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), "attempt") {
				t.Fatalf("check = %v, want ErrCorrupt that names the attempt", err)
			}
		})
	}
}
