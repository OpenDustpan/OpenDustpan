package backup_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
)

// restoreWithLostObjects deletes the keys lost from the target of a new
// setup, runs the restore id, and returns its final row.
func restoreWithLostObjects(t *testing.T, id backup.RestoreID, checkOnly bool, lost []string) backup.RestoreRecord {
	t.Helper()

	setup := newRestoreServiceSetup(t)

	for _, key := range lost {
		if err := setup.target.Delete(t.Context(), blob.Key(key)); err != nil {
			t.Fatalf("delete %s: %v", key, err)
		}
	}

	err := setup.service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        checkOnly,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}

	return waitRestoreEnd(t, setup.regs, id)
}

// TestRestoreOfAMissingObjectNamesTheObject proves that a restore of a backup
// whose target lost a blob or the table files fails, and that the row names
// the key of the missing object in the backup, as the backup check does. A
// check-only restore names it too.
func TestRestoreOfAMissingObjectNamesTheObject(t *testing.T) {
	t.Parallel()

	const lostBlob = "blobs/namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1"

	tableFiles := make([]string, 0, len(backup.Tables))
	for _, table := range backup.Tables {
		tableFiles = append(tableFiles, sqlPartAllKey("bkp-restore", table))
	}

	for _, tc := range []struct {
		name      string
		id        backup.RestoreID
		checkOnly bool
		lost      []string
		wantKey   string
	}{
		{"blob, restore", "rst-missingobjectaaaaaaa", false, []string{lostBlob}, lostBlob},
		{"blob, check only", "rst-missingobjectbbbbbbb", true, []string{lostBlob}, lostBlob},
		{"table files, restore", "rst-missingobjectccccccc", false, tableFiles, "backups/bkp-restore/"},
		{"table files, check only", "rst-missingobjectddddddd", true, tableFiles, "backups/bkp-restore/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := restoreWithLostObjects(t, tc.id, tc.checkOnly, tc.lost)

			const start = `backup is incomplete: object "`

			if row.State != backup.RestoreFailed || !strings.HasPrefix(row.Error, start+tc.wantKey) ||
				!strings.HasSuffix(row.Error, `" is missing: backup data is corrupt`) {
				t.Fatalf("restore = %s %q, want failed with %q, the key %s and the missing object",
					row.State, row.Error, start, tc.wantKey)
			}
		})
	}
}
