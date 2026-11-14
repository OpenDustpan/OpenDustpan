package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/triggerstore"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// seedTriggers makes the namespace acme with one follows trigger (f1) and one
// schedule trigger (s1).
func seedTriggers(t *testing.T, s *Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	a := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "a"}
	b := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "b"}
	none := graph.Unit{Namespace: "", Tool: "", ID: ""}

	for _, tr := range []*trigger.Trigger{
		{
			ID: "f1", Unit: b, Kind: trigger.KindFollows, Frequency: trigger.Frequency{Preset: "", Cron: ""},
			Source: a, Enabled: true, LastSaver: "user:ada", LastSaverID: "prn-user:ada",
		},
		{
			ID: "s1", Unit: b, Kind: trigger.KindSchedule, Frequency: trigger.Frequency{Preset: cron.PresetDaily, Cron: ""},
			Source: none, Enabled: true, LastSaver: "user:ada", LastSaverID: "prn-user:ada",
		},
	} {
		if err := s.Triggers().Put(t.Context(), *tr, ns.Fence(), triggerAuditEntry()); err != nil {
			t.Fatalf("put trigger %s: %v", tr.ID, err)
		}
	}
}

// TestCheckStagedTriggers proves the restore check: it accepts the rows that the
// store wrote, and it refuses each row that a stored trigger would not pass.
func TestCheckStagedTriggers(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedTriggers(t, s)

		if err := checkStagedTriggers(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range []struct{ name, update string }{
		{"unknown preset", `UPDATE triggers SET preset = 'yearly' WHERE id = 's1'`},
		{"preset and cron", `UPDATE triggers SET cron = '0 6 * * *' WHERE id = 's1'`},
		{"cron too fast", `UPDATE triggers SET preset = '', cron = '*/5 * * * *' WHERE id = 's1'`},
		{"cron with a newline", `UPDATE triggers SET preset = '', cron = '0 6 * * *' || char(10) WHERE id = 's1'`},
		{
			"source on a schedule trigger",
			`UPDATE triggers SET source_namespace = 'acme', source_tool = 'opentofu', source_unit_id = 'a' WHERE id = 's1'`,
		},
		{"empty saver", `UPDATE triggers SET last_saver_subject = '' WHERE id = 's1'`},
		{"empty saver ID", `UPDATE triggers SET last_saver_id = '' WHERE id = 's1'`},
		{"saver is a service principal", `UPDATE triggers SET last_saver_subject = 'system:namespace-service:acme'`},
		{"source in another namespace", `UPDATE triggers SET source_namespace = 'other' WHERE id = 'f1'`},
		{"follows trigger with no source", `UPDATE triggers SET source_namespace = '', source_tool = '', source_unit_id = '' WHERE id = 'f1'`},
		{"trigger that follows its own unit", `UPDATE triggers SET source_unit_id = 'b' WHERE id = 'f1'`},
		{"empty unit tool", `UPDATE triggers SET unit_tool = '' WHERE id = 's1'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedTriggers(t, s)

			if _, err := s.conn.ExecContext(t.Context(), tc.update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			err := checkStagedTriggers(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) || !errors.Is(err, triggerstore.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt of the backup and of the store", err)
			}
		})
	}
}

// TestCheckStagedTriggersRefusesANextSlotInTheFuture proves the restore check:
// an enabled hourly trigger whose next slot is in 2100 would never be due, so
// the schedule would stop. The staged row is corrupt.
func TestCheckStagedTriggersRefusesANextSlotInTheFuture(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedTriggers(t, s)

	next := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

	_, err := s.conn.ExecContext(t.Context(),
		`UPDATE triggers SET preset = '', cron = '0 * * * *', next_slot_ms = ? WHERE id = 's1'`, next)
	if err != nil {
		t.Fatalf("change the row: %v", err)
	}

	err = checkStagedTriggers(t.Context(), s.conn)
	if !errors.Is(err, backup.ErrCorrupt) || !errors.Is(err, triggerstore.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt of the backup and of the store", err)
	}
}

// TestCheckStagedTriggersReadsTheClockOfTheStore proves the layer of the clock:
// the restore check takes its time from the store, through the read of
// DatabaseNowMs, and not from the clock of the process. A canceled context
// fails the read of the clock of the store; the clock of the process never
// fails.
func TestCheckStagedTriggersReadsTheClockOfTheStore(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedTriggers(t, s)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := checkStagedTriggers(ctx, s.conn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("check = %v, want the canceled context of the clock read", err)
	}

	if !strings.Contains(fmt.Sprint(err), "read the clock of the store") {
		t.Fatalf("check = %v, want the refusal of the clock read", err)
	}
}

// TestCheckStagedTriggersRefusesAMissingNamespace proves that a trigger whose
// namespace the backup does not hold is corrupt. An inner join would skip such a
// row, and the restore would bring it in.
func TestCheckStagedTriggersRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedTriggers(t, s)

	// The foreign key refuses this change, so the connection that makes it
	// switches the key check off. It is closed before the check runs.
	pinned, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("take a connection: %v", err)
	}

	// The deferred close releases the connection on a failed step. The close
	// below still runs before the check, and a second close is a no-op.
	defer func() {
		_ = pinned.Close() //nolint:errcheck // The connection is closed below on the success path.
	}()

	for _, stmt := range []string{
		`PRAGMA foreign_keys = OFF`,
		`UPDATE triggers SET namespace_id = 'ns-gone'`,
	} {
		if _, execErr := pinned.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	if closeErr := pinned.Close(); closeErr != nil {
		t.Fatalf("release the connection: %v", closeErr)
	}

	err = checkStagedTriggers(t.Context(), s.conn)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(fmt.Sprint(err), "has no namespace") {
		t.Fatalf("check = %v, want the refusal of a trigger with no namespace", err)
	}
}

// TestScanStagedTriggerReturnsTheZeroRowOnError proves that a staged row that
// fails to scan or names no namespace comes back as the zero row with its error,
// so no caller reads a partial trigger from a refusal.
func TestScanStagedTriggerReturnsTheZeroRowOnError(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)

	cases := map[string]struct {
		query string
		want  error
	}{
		"no namespace": {
			query: `SELECT NULL, 'f1', 'opentofu', 'b', 'follows', '', '', 'acme', 'opentofu', 'a', 1,
'user:ada', 'prn-user:ada', 1, 2, 0`,
			want: backup.ErrCorrupt,
		},
		"a row with too few columns": {query: `SELECT 'acme', 'f1'`, want: backup.ErrExportRead},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rows, err := s.conn.QueryContext(t.Context(), tc.query)
			if err != nil {
				t.Fatalf("query: %v", err)
			}

			defer func() {
				_ = rows.Close() //nolint:errcheck // Read-only test handle.
			}()

			if !rows.Next() {
				t.Fatalf("query gave no row: %v", rows.Err())
			}

			row, scanErr := scanStagedTrigger(rows)
			if !errors.Is(scanErr, tc.want) {
				t.Fatalf("scan = %v, want %v", scanErr, tc.want)
			}

			var zero triggerstore.Row
			if row != zero {
				t.Errorf("row = %+v, want the zero row", row)
			}
		})
	}
}

// triggerAuditEntry returns a valid audit entry for the save of a trigger in acme.
func triggerAuditEntry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: "acme", Subject: "user:ada", IDPSubject: "", TokenID: "",
		Action: "trigger:put", Object: "opentofu:acme/b", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}
