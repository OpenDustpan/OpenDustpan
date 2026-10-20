package postgres_test

import (
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit/audittest"
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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	faults := stateFaults()
	names := make([]string, 0, len(faults))

	for name := range faults {
		names = append(names, name)
	}

	storetest.RunWritesFailClosed(t, names, func(t *testing.T, name string) (storetest.Store, storetest.StateFault) {
		t.Helper()

		schemaURL := isolatedURL(t, base)
		conn := controlConn(t, schemaURL)
		statements := faults[name]

		return openURL(t, schemaURL), storetest.StateFault{
			Break: func(t *testing.T) {
				t.Helper()
				execControl(t, conn, statements[0])
			},
			Repair: func(t *testing.T) {
				t.Helper()
				execControl(t, conn, statements[1])
			},
		}
	})
}

// controlConn connects to the control schema of the schema that schemaURL
// names: the installation table is a control table, and the store appends
// _control to the name of its live schema.
func controlConn(t *testing.T, schemaURL string) *pgx.Conn {
	t.Helper()

	parsed, err := url.Parse(schemaURL)
	if err != nil {
		t.Fatalf("parse schema URL: %v", err)
	}

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	control := pgx.Identifier{parsed.Query().Get("search_path") + "_control"}.Sanitize()
	execControl(t, conn, "SET search_path TO "+control)

	return conn
}

func execControl(t *testing.T, conn *pgx.Conn, statement string) {
	t.Helper()

	if _, err := conn.Exec(t.Context(), statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// TestAuditAppendsPassInMaintenance proves that a standalone audit entry is
// never dropped in maintenance mode.
func TestAuditAppendsPassInMaintenance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	storetest.RunAuditInMaintenance(t, func(t *testing.T) storetest.AuditMaintenanceStore {
		t.Helper()

		return open(t, base)
	})
}

// TestSinkCursorAdvancesInMaintenance proves that the audit shipper can
// advance its cursor in maintenance mode.
func TestSinkCursorAdvancesInMaintenance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	audittest.RunFeedInMaintenance(t, func(t *testing.T) audittest.FeedMaintenanceStore {
		t.Helper()

		return open(t, base)
	})
}
