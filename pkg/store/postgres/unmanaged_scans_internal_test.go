package postgres

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/internal/unmanagedstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// unmanagedSeedStatements returns the SQL that writes one runner pool of sweeps
// (sweepers), one sweep target (aws) that names the pool and one cloud sweep of
// that target in the namespace acme. The port of the sweep store arrives with
// its own adapter, so this test writes the rows with SQL.
func unmanagedSeedStatements() []string {
	return []string{
		`INSERT INTO runner_pools (id, namespace_id, name, purpose, lease_ms, concurrency, isolation, git_mode, ` +
			`git_helper, created_at_ms, updated_at_ms) SELECT 'rpl-1', id, 'sweepers', 'sweeps', 90000, 2, ` +
			`'["kubernetes"]', 'forge', '', 1800000000000, 1800000000000 FROM namespaces WHERE name = 'acme'`,
		`INSERT INTO unmanaged_targets (namespace_id, name, pool_id, scopes_json, command_json, every_ms, ` +
			`timeout_ms, enabled) SELECT id, 'aws', 'rpl-1', ` +
			`'[{"provider":"aws","scope":"111111111111","regions":[]}]', '["inventory-sweep"]', 86400000, 0, ` +
			`true FROM namespaces WHERE name = 'acme'`,
		`INSERT INTO unmanaged_scans (id, namespace_id, seq, target, kind, status, started_by, reason, ` +
			`scopes_json, counts_json, created_at_ms, ended_at_ms) SELECT 'scn-1', id, 1, 'aws', 'cloud', ` +
			`'queued', 'user:ada', '', '[]', '{"records":0,"managed":0,"elsewhere":0,"notInAnyUnit":0,` +
			`"leftBehind":0,"managedTwice":0,"resolved":0,"rejected":0,"outOfScope":0}', 1800000000000, 0 ` +
			`FROM namespaces WHERE name = 'acme'`,
	}
}

// seedUnmanaged makes the namespace acme and the rows of the sweep tables.
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

	for _, stmt := range unmanagedSeedStatements() {
		if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
			t.Fatalf("seed a sweep row: %v", execErr)
		}
	}
}

// unmanagedEntry returns a valid audit entry of an action on the sweep rows
// of ns.
func unmanagedEntry(ns namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_800_000_000_000).UTC(), Namespace: ns, Subject: "user:ada", IDPSubject: "",
		TokenID: "", Action: action, Object: "namespace:" + ns.String(), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
		Origin: nil,
	}
}

// TestCheckStagedUnmanaged proves the restore check: it accepts the rows that
// the store wrote, and it refuses each row that a stored sweep target or sweep
// would not pass.
func TestCheckStagedUnmanaged(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openForSavedViews(t)
		seedUnmanaged(t, s)

		if err := checkStagedUnmanaged(t.Context(), s.pool, currentSchema(t, s)); err != nil {
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

			s := openForSavedViews(t)
			seedUnmanaged(t, s)

			if _, err := s.pool.Exec(t.Context(), tc.update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			err := checkStagedUnmanaged(t.Context(), s.pool, currentSchema(t, s))
			if !errors.Is(err, backup.ErrCorrupt) || !errors.Is(err, unmanagedstore.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt of the backup and of the store", err)
			}
		})
	}
}

// TestCheckStagedUnmanagedRefusesAMissingNamespace proves that a target or a
// sweep whose namespace the backup does not hold is corrupt. An inner join
// would skip such a row, and the restore would bring it in.
func TestCheckStagedUnmanagedRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, constraint, update string
	}{
		{
			name:       "a target",
			constraint: `ALTER TABLE unmanaged_targets DROP CONSTRAINT unmanaged_targets_namespace_id_fkey`,
			update:     `UPDATE unmanaged_targets SET namespace_id = 'ns-gone'`,
		},
		{
			name:       "a sweep",
			constraint: `ALTER TABLE unmanaged_scans DROP CONSTRAINT unmanaged_scans_namespace_id_fkey`,
			update:     `UPDATE unmanaged_scans SET namespace_id = 'ns-gone'`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)
			seedUnmanaged(t, s)

			// The foreign key refuses this change, so the test drops the key
			// in its own schema first.
			for _, stmt := range []string{tc.constraint, tc.update} {
				if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
					t.Fatalf("%s: %v", stmt, execErr)
				}
			}

			err := checkStagedUnmanaged(t.Context(), s.pool, currentSchema(t, s))
			if err == nil || !errors.Is(err, backup.ErrCorrupt) {
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

	s := openForSavedViews(t)
	seedUnmanaged(t, s)

	// The foreign key refuses this change, so the test drops the key in its
	// own schema first.
	for _, stmt := range []string{
		`ALTER TABLE unmanaged_targets DROP CONSTRAINT unmanaged_targets_pool_id_fkey`,
		`UPDATE unmanaged_targets SET pool_id = 'rpl-gone'`,
	} {
		if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	err := checkStagedUnmanaged(t.Context(), s.pool, currentSchema(t, s))
	if err == nil || !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), "has no runner pool") {
		t.Fatalf("check = %v, want the refusal of a target with no runner pool", err)
	}
}

// TestCheckStagedUnmanagedRefusesAForeignPool proves that a staged target
// whose pool belongs to another namespace is corrupt, so a restore never
// brings in a target that names the pool of another namespace.
func TestCheckStagedUnmanagedRefusesAForeignPool(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedUnmanaged(t, s)

	for _, stmt := range []string{
		`INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id) ` +
			`VALUES ('other', 'eu-west', 1, 0, 'ns-other')`,
		`INSERT INTO runner_pools (id, namespace_id, name, purpose, lease_ms, concurrency, isolation, ` +
			`git_mode, git_helper, created_at_ms, updated_at_ms) ` +
			`VALUES ('rpl-other', 'ns-other', 'sweepers', 'sweeps', 90000, 2, '["kubernetes"]', 'forge', '', ` +
			`1800000000000, 1800000000000)`,
		`UPDATE unmanaged_targets SET pool_id = 'rpl-other'`,
	} {
		if _, execErr := s.pool.Exec(t.Context(), stmt); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}

	err := checkStagedUnmanaged(t.Context(), s.pool, currentSchema(t, s))
	if err == nil || !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(err.Error(), "another namespace") {
		t.Fatalf("check = %v, want the refusal of a target whose pool is of another namespace", err)
	}
}

// TestUnmanagedDeletePoolOfATarget proves that a delete of a runner pool that
// a sweep target names is refused as a pool in use, and that the pool stays.
func TestUnmanagedDeletePoolOfATarget(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedUnmanaged(t, s)

	ns, err := s.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace acme: %v", err)
	}

	fence := ns.Fence()
	entries := []audit.Entry{unmanagedEntry(fence.Namespace, "namespace:manage-runners")}

	err = s.Runners().DeletePool(t.Context(), "rpl-1", fence, entries)
	if !errors.Is(err, runner.ErrPoolInUse) {
		t.Fatalf("delete the pool of a target = %v, want ErrPoolInUse", err)
	}

	if _, err = s.Runners().Pool(t.Context(), "rpl-1"); err != nil {
		t.Fatalf("the pool after the refused delete: %v", err)
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

// unmanagedScansURLEnv names the variable that holds the PostgreSQL URL of
// the unmanaged scan tests. The tests skip when it is empty.
const unmanagedScansURLEnv = "DUSTPAN_TEST_POSTGRES_URL"

// unmanagedScansURL returns the URL of the test database, or skips the test
// when the environment names no database.
func unmanagedScansURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv(unmanagedScansURLEnv)
	if base == "" {
		t.Skipf("set %s to run the unmanaged scan tests", unmanagedScansURLEnv)
	}

	return base
}

// openUnmanagedScansInternalStore opens a store in a migrated schema of a
// database of its own, cloned from the template database.
func openUnmanagedScansInternalStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), pgtest.MigratedURL(t, unmanagedScansURL(t)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// repointTargetPg points the target aws of the namespace acme at poolID with
// raw SQL, as a damaged row does.
func repointTargetPg(t *testing.T, s *Store, poolID string) {
	t.Helper()

	acme, err := s.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace acme: %v", err)
	}

	_, err = s.pool.Exec(t.Context(),
		`UPDATE unmanaged_targets SET pool_id = $1 WHERE namespace_id = $2 AND name = 'aws'`,
		poolID, acme.ID)
	if err != nil {
		t.Fatalf("point the target at pool %s: %v", poolID, err)
	}
}

// cloudScanInPg returns a cloud sweep of the target aws of the namespace ns.
func cloudScanInPg(ns string) unmanaged.Scan {
	return unmanaged.Scan{
		Namespace: namespace.Name(ns),
		Kind:      unmanaged.ScanCloud,
		Target:    "aws",
		CreatedAt: time.UnixMilli(1_800_000_000_000).UTC(),
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

// TestSweepPoolOfRefusesAJobPool proves the purpose check of a start.
func TestSweepPoolOfRefusesAJobPool(t *testing.T) {
	t.Parallel()

	s := openUnmanagedScansInternalStore(t)
	seedUnmanaged(t, s)

	jobs, err := s.Runners().CreatePool(t.Context(), runner.Pool{
		ID: "", Namespace: "acme", Name: "builders", Purpose: runner.PurposeJobs, Lease: runner.DefaultLease,
		Concurrency: 2, Isolation: []runner.Isolation{runner.IsolationKubernetes},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsForge, Helper: ""},
		CreatedAt:      time.UnixMilli(1_800_000_000_000).UTC(), UpdatedAt: time.UnixMilli(1_800_000_000_000).UTC(),
	}, namespace.Fence{Namespace: "acme", FailoverVersion: 1},
		[]audit.Entry{unmanagedEntry("acme", "namespace:manage-runners")})
	if err != nil {
		t.Fatalf("create the jobs pool: %v", err)
	}

	repointTargetPg(t, s, jobs.ID)

	_, err = s.UnmanagedScans().StartScan(t.Context(), cloudScanInPg("acme"),
		namespace.Fence{Namespace: "acme", FailoverVersion: 1},
		unmanagedEntry("acme", "namespace:start-sweep"))
	if !errors.Is(err, unmanaged.ErrInvalidTarget) {
		t.Fatalf("start = %v, want ErrInvalidTarget", err)
	}
}
