package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/internal/unmanagedstore"
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
	if !errors.Is(err, backup.ErrCorrupt) {
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
