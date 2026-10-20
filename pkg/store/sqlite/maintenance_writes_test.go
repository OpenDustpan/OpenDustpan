package sqlite_test

import (
	"path/filepath"
	"testing"

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
