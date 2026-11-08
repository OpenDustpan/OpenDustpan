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
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
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

// unmanagedAt returns the time of the fixtures of a sweep.
func unmanagedAt() time.Time {
	return time.UnixMilli(1_800_000_000_000).UTC()
}

// seedForeignPool makes the namespace other with a pool of sweeps (sweepers)
// and returns the pool.
func seedForeignPool(t *testing.T, s *Store) runner.Pool {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "other",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace other: %v", err)
	}

	pool, err := s.Runners().CreatePool(t.Context(), runner.Pool{
		ID: "", Namespace: ns.Name, Name: "sweepers", Purpose: runner.PurposeSweeps, Lease: runner.DefaultLease,
		Concurrency: 2, Isolation: []runner.Isolation{runner.IsolationKubernetes},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsForge, Helper: ""},
		CreatedAt:      unmanagedAt(), UpdatedAt: unmanagedAt(),
	}, ns.Fence(), []audit.Entry{unmanagedEntry(ns.Name, "namespace:manage-runners")})
	if err != nil {
		t.Fatalf("create the pool of other: %v", err)
	}

	return pool
}

// repointTarget points the target aws of the namespace acme at poolID with raw
// SQL, as a damaged row does.
func repointTarget(t *testing.T, s *Store, poolID string) {
	t.Helper()

	acme, err := s.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace acme: %v", err)
	}

	_, err = s.conn.ExecContext(t.Context(),
		`UPDATE unmanaged_targets SET pool_id = ? WHERE namespace_id = ? AND name = 'aws'`,
		poolID, acme.ID)
	if err != nil {
		t.Fatalf("point the target at pool %s: %v", poolID, err)
	}
}

// foreignTarget returns a valid target of the namespace other that names the
// pool sweeps of that namespace.
func foreignTarget() unmanaged.Target {
	return unmanaged.Target{
		Namespace: "other", Name: "aws", Pool: "sweepers",
		Scopes:  []unmanaged.TargetScope{{Provider: "aws", Scope: "111111111111", Regions: nil}},
		Command: []string{"inventory-sweep"}, Every: 24 * time.Hour, Timeout: 0, Enabled: true,
	}
}

// cloudScanIn returns a cloud sweep of the target aws of the namespace ns, as
// a start reads it.
func cloudScanIn(ns string) unmanaged.Scan {
	return unmanaged.Scan{
		Namespace: namespace.Name(ns),
		Kind:      unmanaged.ScanCloud,
		Target:    "aws",
		CreatedAt: unmanagedAt(),
		EndedAt:   time.Time{},
		ID:        "",
		Status:    "",
		StartedBy: "",
		Reason:    "",
		Scopes:    nil,
		Counts: unmanaged.ScanCounts{
			Records: 0, Managed: 0, Elsewhere: 0, NotInAnyUnit: 0, LeftBehind: 0, ManagedTwice: 0, Resolved: 0,
			Rejected: 0, OutOfScope: 0,
		},
	}
}

// TestUnmanagedTargetsRefuseAPoolOfAnotherNamespace proves the read layer: a
// target row written with raw SQL that names the sweep pool of another
// namespace does not read, and a sweep of it does not start.
func TestUnmanagedTargetsRefuseAPoolOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedUnmanaged(t, s)

	foreign := seedForeignPool(t, s)
	repointTarget(t, s, foreign.ID)

	acme, err := s.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace acme: %v", err)
	}

	targets, err := s.UnmanagedScans().Targets(t.Context(), "acme")
	if err != nil {
		t.Fatalf("targets: %v", err)
	}

	if len(targets) != 0 {
		t.Fatalf("targets = %+v, want none", targets)
	}

	_, readErr := unmanagedTargetRow(t.Context(), s.queries, acme.ID.String(), "aws")
	if !errors.Is(readErr, unmanaged.ErrNotFound) {
		t.Fatalf("target read = %v, want ErrNotFound", readErr)
	}

	_, err = s.UnmanagedScans().StartScan(t.Context(), cloudScanIn("acme"), acme.Fence(),
		unmanagedEntry("acme", "namespace:start-sweep"))
	if !errors.Is(err, unmanaged.ErrNotFound) {
		t.Fatalf("start = %v, want ErrNotFound", err)
	}
}

// TestCheckStagedUnmanagedRefusesAForeignPool proves that a staged target
// whose pool belongs to another namespace is corrupt, so a restore never
// brings in a target that names the pool of another namespace.
func TestCheckStagedUnmanagedRefusesAForeignPool(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedUnmanaged(t, s)

	foreign := seedForeignPool(t, s)
	repointTarget(t, s, foreign.ID)

	err := checkStagedUnmanaged(t.Context(), s.conn)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), "another namespace") {
		t.Fatalf("check = %v, want the refusal of a target whose pool is of another namespace", err)
	}
}

// TestUnmanagedScansRefuseAnotherNamespace proves each check that compares a
// value with the fence or the entry of its namespace: a value of another
// namespace is refused by the layer that owns it, so the removal of that layer
// fails this test.
func TestUnmanagedScansRefuseAnotherNamespace(t *testing.T) {
	t.Parallel()

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	acmeEntry := unmanagedEntry("acme", "namespace:manage-sweeps")
	otherEntry := unmanagedEntry("other", "namespace:manage-sweeps")

	for _, tc := range []struct {
		name string
		call func(t *testing.T, s *Store) error
		want error
	}{
		{
			name: "create a target of another namespace",
			call: func(t *testing.T, s *Store) error {
				t.Helper()

				return s.UnmanagedScans().CreateTarget(t.Context(), foreignTarget(), fence, acmeEntry)
			},
			want: namespace.ErrFenced,
		},
		{
			name: "update a target of another namespace",
			call: func(t *testing.T, s *Store) error {
				t.Helper()

				return s.UnmanagedScans().UpdateTarget(t.Context(), foreignTarget(), fence, acmeEntry)
			},
			want: namespace.ErrFenced,
		},
		{
			name: "delete a target of another namespace",
			call: func(t *testing.T, s *Store) error {
				t.Helper()

				return s.UnmanagedScans().DeleteTarget(t.Context(), "other", "aws", fence, acmeEntry)
			},
			want: namespace.ErrFenced,
		},
		{
			name: "start a sweep of another namespace",
			call: func(t *testing.T, s *Store) error {
				t.Helper()

				_, err := s.UnmanagedScans().StartScan(t.Context(), cloudScanIn("other"), fence, acmeEntry)

				return err
			},
			want: namespace.ErrFenced,
		},
		{
			name: "advance a sweep of another namespace",
			call: func(t *testing.T, s *Store) error {
				t.Helper()

				_, err := s.UnmanagedScans().AdvanceScan(t.Context(), unmanaged.ScanAdvance{
					Namespace: "other", ID: "scn-none", Event: unmanaged.ScanEventCancel,
					Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
				}, fence, acmeEntry)

				return err
			},
			want: namespace.ErrFenced,
		},
		{
			name: "advance a sweep with an entry of another namespace",
			call: func(t *testing.T, s *Store) error {
				t.Helper()

				scans, err := s.UnmanagedScans().Scans(t.Context(), "acme", "", "", 10)
				if err != nil || len(scans) != 1 {
					t.Fatalf("sweeps = %+v, %v, want one", scans, err)
				}

				_, err = s.UnmanagedScans().AdvanceScan(t.Context(), unmanaged.ScanAdvance{
					Namespace: "acme", ID: scans[0].ID, Event: unmanaged.ScanEventCancel,
					Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
				}, fence, otherEntry)

				return err
			},
			want: audit.ErrInvalidEntry,
		},
		{
			name: "create a target with an entry of another namespace",
			call: func(t *testing.T, s *Store) error {
				t.Helper()

				target := foreignTarget()
				target.Namespace = "acme"
				target.Name = "second"

				return s.UnmanagedScans().CreateTarget(t.Context(), target, fence, otherEntry)
			},
			want: audit.ErrInvalidEntry,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedUnmanaged(t, s)

			err := tc.call(t, s)
			if !errors.Is(err, tc.want) {
				t.Fatalf("call = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestSweepPoolOfRefusesAJobPool proves the purpose check of a start: a target
// row written with raw SQL that names a jobs pool does not start a sweep.
func TestSweepPoolOfRefusesAJobPool(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedUnmanaged(t, s)

	jobs, err := s.Runners().CreatePool(t.Context(), runner.Pool{
		ID: "", Namespace: "acme", Name: "builders", Purpose: runner.PurposeJobs, Lease: runner.DefaultLease,
		Concurrency: 2, Isolation: []runner.Isolation{runner.IsolationKubernetes},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsForge, Helper: ""},
		CreatedAt:      unmanagedAt(), UpdatedAt: unmanagedAt(),
	}, namespace.Fence{Namespace: "acme", FailoverVersion: 1},
		[]audit.Entry{unmanagedEntry("acme", "namespace:manage-runners")})
	if err != nil {
		t.Fatalf("create the jobs pool: %v", err)
	}

	repointTarget(t, s, jobs.ID)

	_, err = s.UnmanagedScans().StartScan(t.Context(), cloudScanIn("acme"),
		namespace.Fence{Namespace: "acme", FailoverVersion: 1},
		unmanagedEntry("acme", "namespace:start-sweep"))
	if !errors.Is(err, unmanaged.ErrInvalidTarget) {
		t.Fatalf("start = %v, want ErrInvalidTarget", err)
	}
}

// TestCheckTargetPool proves the checks of a start on the stored pool of a
// target: the pool must belong to the namespace of the target and run sweeps.
func TestCheckTargetPool(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		row  db.GetUnmanagedTargetRow
		want error
	}{
		{
			name: "a pool of this namespace that runs sweeps",
			row: db.GetUnmanagedTargetRow{
				PoolID: "rpl-1", Purpose: string(runner.PurposeSweeps), PoolNamespaceID: "ns-1",
			},
			want: nil,
		},
		{
			name: "a pool of another namespace",
			row: db.GetUnmanagedTargetRow{
				PoolID: "rpl-1", Purpose: string(runner.PurposeSweeps), PoolNamespaceID: "ns-2",
			},
			want: unmanaged.ErrInvalidTarget,
		},
		{
			name: "a pool that runs jobs",
			row: db.GetUnmanagedTargetRow{
				PoolID: "rpl-1", Purpose: string(runner.PurposeJobs), PoolNamespaceID: "ns-1",
			},
			want: unmanaged.ErrInvalidTarget,
		},
		{
			name: "a pool with no namespace",
			row: db.GetUnmanagedTargetRow{
				PoolID: "rpl-1", Purpose: string(runner.PurposeSweeps), PoolNamespaceID: "",
			},
			want: unmanaged.ErrInvalidTarget,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkTargetPool(tc.row, "ns-1", "aws")
			if !errors.Is(err, tc.want) {
				t.Fatalf("check = %v, want %v", err, tc.want)
			}
		})
	}
}
