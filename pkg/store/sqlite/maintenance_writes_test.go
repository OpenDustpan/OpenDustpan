package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit/audittest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// stateFaults returns the ways that the tests make the installation state
// unreadable, each with the statement that breaks it and the statement that
// repairs it.
func stateFaults() map[string][2]string {
	return map[string][2]string{
		"the row is missing": {
			`DELETE FROM installation`,
			`INSERT INTO installation (id, state) VALUES (1, 'normal')`,
		},
		"the table cannot be read": {
			`ALTER TABLE installation RENAME TO installation_hidden`,
			`ALTER TABLE installation_hidden RENAME TO installation`,
		},
	}
}

// TestWriteFailsClosedOnStateReadError proves that a write does not pass when
// the store cannot read the installation state.
func TestWriteFailsClosedOnStateReadError(t *testing.T) {
	t.Parallel()

	faults := stateFaults()
	names := make([]string, 0, len(faults))

	for name := range faults {
		names = append(names, name)
	}

	storetest.RunWritesFailClosed(t, names, func(t *testing.T, name string) (storetest.Store, storetest.StateFault) {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")
		copyTemplate(t, path)

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := s.Close(); closeErr != nil {
				t.Errorf("close store: %v", closeErr)
			}
		})

		conn, err := openRawDB(path)
		if err != nil {
			t.Fatalf("open raw database: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := conn.Close(); closeErr != nil {
				t.Errorf("close raw database: %v", closeErr)
			}
		})

		statements := faults[name]
		exec := func(t *testing.T, statement string) {
			t.Helper()

			if _, execErr := conn.ExecContext(t.Context(), statement); execErr != nil {
				t.Fatalf("%s: %v", statement, execErr)
			}
		}

		return s, storetest.StateFault{
			Break: func(t *testing.T) {
				t.Helper()
				exec(t, statements[0])
			},
			Repair: func(t *testing.T) {
				t.Helper()
				exec(t, statements[1])
			},
		}
	})
}

// TestAuditAppendsPassInMaintenance proves that a standalone audit entry is
// never dropped in maintenance mode.
func TestAuditAppendsPassInMaintenance(t *testing.T) {
	t.Parallel()

	storetest.RunAuditInMaintenance(t, func(t *testing.T) storetest.AuditMaintenanceStore {
		t.Helper()

		return open(t)
	})
}

// TestSinkCursorAdvancesInMaintenance proves that the audit shipper can
// advance its cursor in maintenance mode.
func TestSinkCursorAdvancesInMaintenance(t *testing.T) {
	t.Parallel()

	audittest.RunFeedInMaintenance(t, func(t *testing.T) audittest.FeedMaintenanceStore {
		t.Helper()

		return open(t)
	})
}

// TestAuditCheckSavesPassInMaintenance proves that the scheduled audit chain
// check can save its result and its mark in maintenance mode.
func TestAuditCheckSavesPassInMaintenance(t *testing.T) {
	t.Parallel()

	storetest.RunAuditCheckInMaintenance(t, func(t *testing.T) storetest.AuditCheckMaintenanceStore {
		t.Helper()

		return open(t)
	})
}

// TestEnterBusyConformance runs the busy case of an enter: a write in flight
// that outlasts the bound of the store makes the enter report ErrBusy.
func TestEnterBusyConformance(t *testing.T) {
	t.Parallel()

	storetest.RunEnterBusy(t, func(t *testing.T) (storetest.Store, storetest.BusyHarness) {
		t.Helper()

		s := open(t)
		sqlite.SetEnterWaitForTest(s, 50*time.Millisecond)

		return s, storetest.BusyHarness{
			Hold: func(t *testing.T) func() {
				t.Helper()

				return sqlite.HoldCheckedWriteForTest(t, s)
			},
		}
	})
}
