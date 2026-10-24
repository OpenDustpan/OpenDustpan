package backup_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// manifestPinEdit is an edit to a whole manifest that makes its totals
// disagree with its table file descriptors. wantText is a part of the error.
type manifestPinEdit struct {
	name     string
	edit     func(m *backup.Manifest)
	wantText string
}

func manifestPinEdits() []manifestPinEdit {
	return []manifestPinEdit{
		{
			name:     "wrong row count",
			edit:     func(m *backup.Manifest) { m.RowCounts["namespaces"] = 9999 },
			wantText: `row count of table "namespaces" is 9999`,
		},
		{
			name:     "missing row count of a table with rows",
			edit:     func(m *backup.Manifest) { delete(m.RowCounts, "namespaces") },
			wantText: `row count of table "namespaces" is 0`,
		},
		{
			name:     "row count of a table with no table file",
			edit:     func(m *backup.Manifest) { m.RowCounts["ghost_table"] = 1 },
			wantText: `table "ghost_table", which has no table file`,
		},
		{
			name: "changed SQL checksum",
			edit: func(m *backup.Manifest) {
				sum := sha256.Sum256([]byte("other table files"))
				m.SQLSHA256 = hex.EncodeToString(sum[:])
			},
			wantText: "SQL checksum does not match",
		},
		{
			name:     "SQL checksum that is not hex",
			edit:     func(m *backup.Manifest) { m.SQLSHA256 = "zz" },
			wantText: "SQL checksum is not valid",
		},
		{
			name:     "wrong SQL size",
			edit:     func(m *backup.Manifest) { m.SQLSize++ },
			wantText: "SQL size is",
		},
	}
}

// TestCheckRefusesManifestTotalsThatDoNotMatch proves that backup check
// compares the row counts, the SQL size and the SQL checksum of the manifest
// with its table file descriptors. Each table file is whole, so only this
// comparison can find the edit.
func TestCheckRefusesManifestTotalsThatDoNotMatch(t *testing.T) {
	t.Parallel()

	for _, tc := range manifestPinEdits() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fix := checkedBackup(t)
			key := "backups/bkp-sum/manifest.json"

			var manifest backup.Manifest
			if err := json.Unmarshal(fix.target.readObject(t, key), &manifest); err != nil {
				t.Fatalf("decode the manifest: %v", err)
			}

			tc.edit(&manifest)

			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("encode the manifest: %v", err)
			}

			mustDelete(t, fix.target, key)
			fix.target.storeObject(t, key, data)

			err = backup.Check(t.Context(), fix.target, "bkp-sum")
			if !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("check = %v, want ErrCorrupt that contains %q", err, tc.wantText)
			}
		})
	}
}

// TestCheckRestoreRefusesManifestTotalsThatDoNotMatch proves that a restore
// check refuses the same manifests before it stages data. The stub stage
// accepts the edited manifest, so only the manifest check can refuse it.
func TestCheckRestoreRefusesManifestTotalsThatDoNotMatch(t *testing.T) {
	t.Parallel()

	for _, tc := range manifestPinEdits() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newCheckSetup(t)
			tc.edit(&s.manifest)
			s.rewriteCheckManifest(t)

			_, _, err := s.check(t, testWrapper(), "")
			if !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("check restore = %v, want ErrCorrupt that contains %q", err, tc.wantText)
			}

			s.assertNoStage(t)
		})
	}
}
