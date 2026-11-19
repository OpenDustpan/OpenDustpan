package backup_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestGitHostRegistrationBackupHoldsTheOldEventSecret proves that the portable
// form of the registration lists the three columns of the old event secret, so
// a restore keeps an open window, and that the form accepts a NULL in each of
// them: a registration with no window holds NULL in all three.
func TestGitHostRegistrationBackupHoldsTheOldEventSecret(t *testing.T) {
	t.Parallel()

	var registrations backup.PortableTable

	for _, table := range backup.PortableTables() {
		if table.Table == backup.TableGitHostAppRegistrations {
			registrations = table
		}
	}

	want := map[string]backup.Kind{
		"old_event_secret_ends_at_ms": backup.KindTime,
		"old_event_secret_set_id":     backup.KindText,
		"old_sealed_event_secret":     backup.KindBytes,
	}

	for name, kind := range want {
		found := false

		for _, column := range registrations.Columns {
			if column.Name == name {
				found = true

				if column.Kind != kind {
					t.Errorf("column %q has kind %d, want %d", name, column.Kind, kind)
				}
			}
		}

		if !found {
			t.Errorf("the portable registration table misses column %q", name)
		}
	}
}

// TestGitHostEventReceiptsAreEmptied proves that a restore leaves the table of
// event receipts empty: a receipt is a hint and not state, so it is in neither
// the backup table list nor the portable list.
func TestGitHostEventReceiptsAreEmptied(t *testing.T) {
	t.Parallel()

	const table = "git_host_event_receipts"

	if backup.TableGitHostEventReceipts != table {
		t.Fatalf("TableGitHostEventReceipts = %q, want %q", backup.TableGitHostEventReceipts, table)
	}

	if !slices.Contains(backup.EmptiedTables, table) {
		t.Errorf("EmptiedTables does not hold %q", table)
	}

	if slices.Contains(backup.Tables, table) || slices.Contains(backup.ControlTables, table) {
		t.Errorf("a backup list holds %q, want it in EmptiedTables only", table)
	}

	for _, portable := range backup.PortableTables() {
		if portable.Table == table {
			t.Errorf("the portable list holds %q", table)
		}
	}
}
