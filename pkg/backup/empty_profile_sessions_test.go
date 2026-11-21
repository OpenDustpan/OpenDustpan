package backup_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestEmptiedTables proves that a restore leaves the table of open sessions
// empty: it is in EmptiedTables and in no list that a backup copies.
func TestEmptiedTables(t *testing.T) {
	t.Parallel()

	const table = "profile_open_sessions"

	if backup.TableProfileOpenSessions != table {
		t.Fatalf("TableProfileOpenSessions = %q, want %q", backup.TableProfileOpenSessions, table)
	}

	if !slices.Contains(backup.EmptiedTables, table) {
		t.Errorf("EmptiedTables does not hold %q", table)
	}

	if slices.Contains(backup.Tables, table) || slices.Contains(backup.ControlTables, table) {
		t.Errorf("a backup list holds %q, want it in EmptiedTables only", table)
	}
}

// TestBackupHoldsNoProfileSession proves that no table list of a backup, the
// portable list included, names the table of open sessions. A session holds a
// result that ends within hours.
func TestBackupHoldsNoProfileSession(t *testing.T) {
	t.Parallel()

	for _, table := range backup.TablesCopy() {
		if table == backup.TableProfileOpenSessions {
			t.Errorf("the backup table list holds %q", table)
		}
	}

	for _, portable := range backup.PortableTables() {
		if portable.Table == backup.TableProfileOpenSessions {
			t.Errorf("the portable list holds %q", portable.Table)
		}
	}
}
