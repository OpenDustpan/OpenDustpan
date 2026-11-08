package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/internal/unmanagedstore"
)

// seedUnmanaged makes the namespace acme with a pool of sweeps (sweepers), one
// sweep target (aws) and one cloud sweep of it.
func seedUnmanaged(t *testing.T, s *Store) {
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

	at := time.UnixMilli(1_800_000_000_000).UTC()
	fence := ns.Fence()
	e := unmanagedEntry(ns.Name, "namespace:manage-sweeps")

	_, err := s.Runners().CreatePool(t.Context(), runner.Pool{
		ID: "", Namespace: ns.Name, Name: "sweepers", Purpose: runner.PurposeSweeps, Lease: runner.DefaultLease,
		Concurrency: 2, Isolation: []runner.Isolation{runner.IsolationKubernetes},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsForge, Helper: ""},
		CreatedAt:      at, UpdatedAt: at,
	}, fence, []audit.Entry{unmanagedEntry(ns.Name, "namespace:manage-runners")})
	if err != nil {
		t.Fatalf("create runner pool: %v", err)
	}

	target := unmanaged.Target{
		Namespace: ns.Name, Name: "aws", Pool: "sweepers",
		Scopes:  []unmanaged.TargetScope{{Provider: "aws", Scope: "111111111111", Regions: nil}},
		Command: []string{"inventory-sweep"}, Every: 24 * time.Hour, Timeout: 0, Enabled: true,
	}
	if targetErr := s.UnmanagedScans().CreateTarget(t.Context(), target, fence, e); targetErr != nil {
		t.Fatalf("create sweep target: %v", targetErr)
	}

	_, err = s.UnmanagedScans().StartScan(t.Context(), unmanaged.Scan{
		CreatedAt: at, EndedAt: time.Time{}, ID: "", Namespace: ns.Name, Target: "aws",
		Kind: unmanaged.ScanCloud, Status: "", StartedBy: "user:ada", Reason: "",
		Scopes: nil,
		Counts: unmanaged.ScanCounts{
			Records: 0, Managed: 0, Elsewhere: 0, NotInAnyUnit: 0, LeftBehind: 0, ManagedTwice: 0, Resolved: 0,
			Rejected: 0, OutOfScope: 0,
		},
	}, fence, unmanagedEntry(ns.Name, "namespace:start-sweep"))
	if err != nil {
		t.Fatalf("start sweep: %v", err)
	}
}

// unmanagedEntry returns a valid audit entry of an action in ns.
func unmanagedEntry(ns namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_800_000_000_000).UTC(), Namespace: ns, Subject: "user:ada", IDPSubject: "",
		TokenID: "", Action: action, Object: "namespace:" + ns.String(), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// TestCheckStagedUnmanaged proves the restore check: it accepts the rows that
// the store wrote, and it refuses each row that a stored sweep target or sweep
// would not pass.
func TestCheckStagedUnmanaged(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedUnmanaged(t, s)

		if err := checkStagedUnmanaged(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range []struct{ name, update string }{
		{"scopes of a target that are not JSON", `UPDATE unmanaged_targets SET scopes_json = 'not json'`},
		{"a scope of a target with an unknown field", `UPDATE unmanaged_targets SET scopes_json = '[{"x":1}]'`},
		{"a target with no scope", `UPDATE unmanaged_targets SET scopes_json = '[]'`},
		{"a target with a schedule below an hour", `UPDATE unmanaged_targets SET every_ms = 60000`},
		{"a target with a timeout above a day", `UPDATE unmanaged_targets SET timeout_ms = 90000000`},
		{"a target with no command", `UPDATE unmanaged_targets SET command_json = '[]'`},
		{"a command that names a credential", `UPDATE unmanaged_targets SET command_json = '["sweep","--token=x"]'`},
		{"scopes of a sweep that are not JSON", `UPDATE unmanaged_scans SET scopes_json = '{'`},
		{"counts of a sweep with an unknown field", `UPDATE unmanaged_scans SET counts_json = '{"records":1,"x":2}'`},
		{"counts of a sweep that are not an object", `UPDATE unmanaged_scans SET counts_json = '[]'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedUnmanaged(t, s)

			if _, err := s.conn.ExecContext(t.Context(), tc.update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			err := checkStagedUnmanaged(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) || !errors.Is(err, unmanagedstore.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt of the backup and of the store", err)
			}
		})
	}
}

// TestCheckStagedUnmanagedRefusesAMissingNamespace proves that a target or a
// sweep whose namespace the backup does not hold is corrupt. An inner join would
// skip such a row, and the restore would bring it in.
func TestCheckStagedUnmanagedRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, table, update string
	}{
		{"a target", "unmanaged_targets", `UPDATE unmanaged_targets SET namespace_id = 'ns-gone'`},
		{"a sweep", "unmanaged_scans", `UPDATE unmanaged_scans SET namespace_id = 'ns-gone'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedUnmanaged(t, s)

			execWithoutForeignKeys(t, s, tc.update)

			err := checkStagedUnmanaged(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}

			if !strings.Contains(err.Error(), "has no namespace") {
				t.Fatalf("check = %v, want the refusal of a %s with no namespace", err, tc.name)
			}
		})
	}
}

// TestCheckStagedUnmanagedRefusesAMissingPool proves that a target whose runner
// pool the backup does not hold is corrupt, so a restore never brings in a
// target that names no pool.
func TestCheckStagedUnmanagedRefusesAMissingPool(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedUnmanaged(t, s)

	execWithoutForeignKeys(t, s, `UPDATE unmanaged_targets SET pool_id = 'rpl-gone'`)

	err := checkStagedUnmanaged(t.Context(), s.conn)
	if err == nil || !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), "has no runner pool") {
		t.Fatalf("check = %v, want the refusal of a target with no runner pool", err)
	}
}

// execWithoutForeignKeys runs the statement that changes a row outside the
// foreign keys of the schema. The foreign key refuses such a change, so the
// connection that makes it switches the key check off. It is closed before the
// check runs.
func execWithoutForeignKeys(t *testing.T, s *Store, stmt string) {
	t.Helper()

	pinned, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("take a connection: %v", err)
	}

	for _, stmt := range []string{`PRAGMA foreign_keys = OFF`, stmt} {
		if _, execErr := pinned.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	if closeErr := pinned.Close(); closeErr != nil {
		t.Fatalf("release the connection: %v", closeErr)
	}
}
