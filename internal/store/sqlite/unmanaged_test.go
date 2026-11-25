package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/store/jobcheck"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/internal/unmanaged/unmanagedtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// unmanagedDomain makes the namespaces and the runner pools that the sweep
// cases need.
type unmanagedDomain struct {
	s *sqlite.Store
}

// Namespace implements unmanagedtest.Harness.Namespace: it makes the namespace
// name with a pool of sweeps and a pool of jobs, and returns its fence.
func (d unmanagedDomain) Namespace(t *testing.T, name string) namespace.Fence {
	t.Helper()

	fence := ensureNamespace(t, d.s, namespace.Name(name))

	ensurePool(t, d.s, fence, unmanagedtest.Pool, runner.PurposeSweeps)
	ensurePool(t, d.s, fence, unmanagedtest.JobPool, runner.PurposeJobs)

	return fence
}

// Pool implements unmanagedtest.Harness.Pool.
func (d unmanagedDomain) Pool(t *testing.T, ns, name, purpose string) {
	t.Helper()

	ensurePool(t, d.s, ensureNamespace(t, d.s, namespace.Name(ns)), name, runner.Purpose(purpose))
}

// Failover implements unmanagedtest.Harness.Failover: it raises the failover
// version of each namespace and returns the new fence of the namespace name.
// It runs off the test goroutine, so it returns its error.
func (d unmanagedDomain) Failover(ctx context.Context, name string) (namespace.Fence, error) {
	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, namespace.Name(name))
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", name, err)
	}

	return ns.Fence(), nil
}

// RenameNamespace implements unmanagedtest.Harness.RenameNamespace: it changes
// the name of the namespace row and keeps its ID.
func (d unmanagedDomain) RenameNamespace(t *testing.T, ns, newName string) {
	t.Helper()

	err := d.s.ExecForTest(t.Context(), `UPDATE namespaces SET name = ? WHERE name = ?`, newName, ns)
	if err != nil {
		t.Fatalf("rename namespace %s: %v", ns, err)
	}
}

// FailAudit implements unmanagedtest.Harness.FailAudit: a trigger aborts each
// later insert of an audit entry, inside the write transaction.
func (d unmanagedDomain) FailAudit(t *testing.T) {
	t.Helper()

	err := d.s.ExecForTest(t.Context(), `CREATE TRIGGER unmanaged_forced_error BEFORE INSERT ON audit_entries
BEGIN SELECT RAISE(ABORT, 'forced error'); END`)
	if err != nil {
		t.Fatalf("create the forced error trigger: %v", err)
	}
}

// PurgeNamespace implements unmanagedtest.Harness.PurgeNamespace: it deletes
// the runner pools of the namespace name, which hold its ID, and then the
// namespace row, as the purge of a namespace does.
func (d unmanagedDomain) PurgeNamespace(t *testing.T, name string) {
	t.Helper()

	err := d.s.ExecForTest(t.Context(),
		`DELETE FROM runner_pools WHERE namespace_id = (SELECT id FROM namespaces WHERE name = ?)`, name)
	if err != nil {
		t.Fatalf("delete the runner pools of %s: %v", name, err)
	}

	if err := d.s.PurgeNamespace(t.Context(), namespace.Name(name)); err != nil {
		t.Fatalf("purge namespace %s: %v", name, err)
	}
}

// SetPool implements unmanagedtest.Harness.SetPool. A change of purpose is a
// direct update, as a restore of another version can bring in. A delete
// passes the foreign key from targets to pools, so it runs on one connection
// with the foreign key checks off, and turns them on again before it releases
// the connection.
func (d unmanagedDomain) SetPool(t *testing.T, ns, name, purpose string) {
	t.Helper()

	const where = `name = ? AND namespace_id = (SELECT id FROM namespaces WHERE name = ?)`

	if purpose != "" {
		err := d.s.ExecForTest(t.Context(), `UPDATE runner_pools SET purpose = ? WHERE `+where, purpose, name, ns)
		if err != nil {
			t.Fatalf("change the purpose of pool %s: %v", name, err)
		}

		return
	}

	pinned, err := sqlite.DB(d.s).Conn(t.Context())
	if err != nil {
		t.Fatalf("take a connection: %v", err)
	}

	defer func() {
		if closeErr := pinned.Close(); closeErr != nil {
			t.Errorf("release the connection: %v", closeErr)
		}
	}()

	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`PRAGMA foreign_keys = OFF`, nil},
		{`DELETE FROM runner_pools WHERE ` + where, []any{name, ns}},
		{`PRAGMA foreign_keys = ON`, nil},
	} {
		if _, err := pinned.ExecContext(t.Context(), stmt.query, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.query, err)
		}
	}
}

// ensurePool makes the runner pool name in the namespace of fence when it is
// not there.
func ensurePool(t *testing.T, s *sqlite.Store, fence namespace.Fence, name string, purpose runner.Purpose) {
	t.Helper()

	ctx := t.Context()

	pools, err := s.Runners().Pools(ctx, fence.Namespace)
	if err != nil {
		t.Fatalf("list runner pools of %s: %v", fence.Namespace, err)
	}

	for i := range pools {
		if pools[i].Name == name {
			return
		}
	}

	at := time.UnixMilli(1_700_000_000_000).UTC()

	_, err = s.Runners().CreatePool(ctx, runner.Pool{
		ID: "", Namespace: fence.Namespace, Name: name, Purpose: purpose, Lease: runner.DefaultLease,
		Concurrency: 2, Isolation: []runner.Isolation{runner.IsolationKubernetes},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsForge, Helper: ""},
		CreatedAt:      at, UpdatedAt: at,
	}, fence, []audit.Entry{poolEntry(fence.Namespace, at)})
	if err != nil {
		t.Fatalf("create runner pool %s: %v", name, err)
	}
}

// poolEntry returns the audit entry of a runner pool that a test makes.
func poolEntry(ns namespace.Name, at time.Time) audit.Entry {
	return audit.Entry{
		At:            at,
		Namespace:     ns,
		Subject:       "user:ada",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "namespace:manage-runners",
		Object:        "namespace:" + ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
		Origin:        nil,
	}
}

// newUnmanagedHarness returns the sweep store of a fresh store for one case.
// The finding store and the index land later, so the harness leaves them nil.
func newUnmanagedHarness(t *testing.T) unmanagedtest.Harness {
	t.Helper()

	s := openTemplated(t)

	return unmanagedtest.Harness{
		Index:           nil,
		Links:           nil,
		Findings:        nil,
		Scans:           s.UnmanagedScans(),
		Namespace:       unmanagedDomain{s: s}.Namespace,
		PurgeNamespace:  unmanagedDomain{s: s}.PurgeNamespace,
		Pool:            unmanagedDomain{s: s}.Pool,
		Failover:        unmanagedDomain{s: s}.Failover,
		SetPool:         unmanagedDomain{s: s}.SetPool,
		RenameNamespace: unmanagedDomain{s: s}.RenameNamespace,
		FailAudit:       unmanagedDomain{s: s}.FailAudit,
	}
}

func TestUnmanagedScanStoreConformance(t *testing.T) {
	t.Parallel()

	unmanagedtest.RunScanStore(t, newUnmanagedHarness)
}

// unmanagedAt returns the time of the fixtures of a sweep.
func unmanagedAt() time.Time {
	return time.UnixMilli(1_800_000_000_000).UTC()
}

// sweepEntry returns the audit entry of an action on a sweep target or a sweep
// of ns.
func sweepEntry(ns namespace.Name, action string, at time.Time) audit.Entry {
	return audit.Entry{
		At:            at,
		Namespace:     ns,
		Subject:       "user:ada",
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
		Object:        "namespace:" + ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
		Origin:        nil,
	}
}

// unmanagedTargetOf returns a valid sweep target of the namespace ns, named
// name, on the pool of sweeps of that namespace.
func unmanagedTargetOf(ns, name string) unmanaged.Target {
	return unmanaged.Target{
		Namespace: namespace.Name(ns), Name: name, Pool: unmanagedtest.Pool,
		Scopes:  []unmanaged.TargetScope{{Provider: "aws", Scope: "111111111111", Regions: []string{"eu-west-1"}}},
		Command: []string{"inventory-sweep"}, Every: 24 * time.Hour, Timeout: 0, Enabled: true,
	}
}

// unmanagedScanOf returns a sweep of the namespace acme: a cloud sweep of the
// target name target, or a state check.
func unmanagedScanOf(k unmanaged.ScanKind, target string) unmanaged.Scan {
	return unmanaged.Scan{
		Namespace: "acme",
		Kind:      k,
		Target:    target,
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

// TestUnmanagedTargetRefusesAPoolOfAnotherNamespace proves the write layer: a
// target that names a pool that exists only in another namespace is refused as
// an unknown pool, on create and on update.
func TestUnmanagedTargetRefusesAPoolOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	s := openTemplated(t)
	acme := ensureNamespace(t, s, "acme")
	other := ensureNamespace(t, s, "other")
	e := sweepEntry("acme", "namespace:manage-sweeps", unmanagedAt())

	ensurePool(t, s, acme, unmanagedtest.Pool, runner.PurposeSweeps)
	ensurePool(t, s, other, "only-other", runner.PurposeSweeps)

	// The pool of other is not foreign in its own namespace.
	otherTarget := unmanagedTargetOf("other", "on-other")
	otherTarget.Pool = "only-other"

	err := s.UnmanagedScans().CreateTarget(t.Context(), otherTarget, other,
		sweepEntry("other", "namespace:manage-sweeps", unmanagedAt()))
	if err != nil {
		t.Fatalf("create the target of other: %v", err)
	}

	foreign := unmanagedTargetOf("acme", "aws")
	foreign.Pool = "only-other"

	err = s.UnmanagedScans().CreateTarget(t.Context(), foreign, acme, e)
	if !errors.Is(err, unmanaged.ErrInvalidTarget) {
		t.Fatalf("create with the pool of another namespace = %v, want ErrInvalidTarget", err)
	}

	err = s.UnmanagedScans().CreateTarget(t.Context(), unmanagedTargetOf("acme", "aws"), acme, e)
	if err != nil {
		t.Fatalf("create the target: %v", err)
	}

	moved := unmanagedTargetOf("acme", "aws")
	moved.Pool = "only-other"

	err = s.UnmanagedScans().UpdateTarget(t.Context(), moved, acme, e)
	if !errors.Is(err, unmanaged.ErrInvalidTarget) {
		t.Fatalf("update to the pool of another namespace = %v, want ErrInvalidTarget", err)
	}
}

// TestUnmanagedDeletePoolOfATarget proves that a delete of a runner pool that
// a sweep target names is refused as a pool in use, and that the pool frees
// after the target is gone.
func TestUnmanagedDeletePoolOfATarget(t *testing.T) {
	t.Parallel()

	s := openTemplated(t)
	fence := ensureNamespace(t, s, "acme")
	ensurePool(t, s, fence, unmanagedtest.Pool, runner.PurposeSweeps)
	e := sweepEntry("acme", "namespace:manage-sweeps", unmanagedAt())

	if err := s.UnmanagedScans().CreateTarget(t.Context(), unmanagedTargetOf("acme", "aws"), fence, e); err != nil {
		t.Fatalf("create the target: %v", err)
	}

	pools, err := s.Runners().Pools(t.Context(), "acme")
	if err != nil {
		t.Fatalf("pools: %v", err)
	}

	var pool runner.Pool

	for i := range pools {
		if pools[i].Name == unmanagedtest.Pool {
			pool = pools[i]
		}
	}

	entries := []audit.Entry{sweepEntry("acme", "namespace:manage-runners", unmanagedAt())}

	err = s.Runners().DeletePool(t.Context(), pool.ID, fence, entries)
	if !errors.Is(err, runner.ErrPoolInUse) {
		t.Fatalf("delete the pool of a target = %v, want ErrPoolInUse", err)
	}

	if err = s.UnmanagedScans().DeleteTarget(t.Context(), "acme", "aws", fence, e); err != nil {
		t.Fatalf("delete the target: %v", err)
	}

	if err = s.Runners().DeletePool(t.Context(), pool.ID, fence, entries); err != nil {
		t.Fatalf("delete the pool after the target is gone: %v", err)
	}
}

// TestUnmanagedSQLTextStaysData sends SQL text as a namespace name, a target
// name and a pool name to the sweep store. The writes of the port refuse each
// name with ErrInvalidTarget before SQL. The reads that bind the text find no
// row of another name. The rows with SQL text in their names are planted with
// placeholders, as a restore of another version could bring them in, and stay
// one row each. TestSweepPoolIDKeepsSQLTextAsData reaches the pool query with
// the text, because no path of the port sends a pool name to SQL unchecked.
func TestUnmanagedSQLTextStaysData(t *testing.T) {
	t.Parallel()

	for i, text := range unmanagedtest.HostileSQLText() {
		c := newSQLTextCase(t, openTemplated(t), text)

		c.namespaceName(t)
		c.targetName(t)
		c.poolName(t, fmt.Sprintf("rpl-hostile-%d", i))

		tables := c.count(t, "tables", `SELECT COUNT(*) FROM sqlite_master WHERE name IN `+
			`('namespaces', 'runner_pools', 'unmanaged_targets', 'unmanaged_scans')`)
		if tables != 4 {
			t.Errorf("%q: %d of the 4 tables are there", text, tables)
		}
	}
}

// sqlTextAcmeID selects the ID of the namespace acme.
const sqlTextAcmeID = `(SELECT id FROM namespaces WHERE name = 'acme')`

// sqlTextCase is one store with the namespace acme and its target aws, and
// one name that holds SQL text.
type sqlTextCase struct {
	s     *sqlite.Store
	fence namespace.Fence
	e     audit.Entry
	text  string
}

// newSQLTextCase makes the namespace acme and its target aws in s.
func newSQLTextCase(t *testing.T, s *sqlite.Store, text string) sqlTextCase {
	t.Helper()

	c := sqlTextCase{
		s:     s,
		fence: unmanagedDomain{s: s}.Namespace(t, "acme"),
		e:     sweepEntry("acme", "namespace:manage-sweeps", unmanagedAt()),
		text:  text,
	}

	if err := s.UnmanagedScans().CreateTarget(t.Context(), unmanagedTargetOf("acme", "aws"), c.fence, c.e); err != nil {
		t.Fatalf("create the target: %v", err)
	}

	return c
}

// count returns the count that query gives.
func (c sqlTextCase) count(t *testing.T, what, query string, args ...any) int {
	t.Helper()

	var n int
	if err := sqlite.DB(c.s).QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: count: %v", what, err)
	}

	return n
}

// namespaceName sends the text as a namespace name: the read binds it and
// finds no namespace, and the delete refuses the name before SQL.
func (c sqlTextCase) namespaceName(t *testing.T) {
	t.Helper()

	ns := namespace.Name(c.text)

	got, err := c.s.UnmanagedScans().Targets(t.Context(), ns)
	if err != nil || len(got) != 0 {
		t.Errorf("%q: targets of the namespace = %+v, %v; want none", c.text, got, err)
	}

	err = c.s.UnmanagedScans().DeleteTarget(t.Context(), ns, "aws", namespace.Fence{Namespace: ns, FailoverVersion: 1},
		sweepEntry(ns, "namespace:manage-sweeps", unmanagedAt()))
	if !errors.Is(err, unmanaged.ErrInvalidTarget) {
		t.Errorf("%q: delete in the namespace = %v, want ErrInvalidTarget", c.text, err)
	}
}

// targetName plants a target with the text as its name. The delete refuses
// the name before SQL, the planted row stays, the target aws stays, and the
// read of the sweeps of that target name binds it and finds none.
func (c sqlTextCase) targetName(t *testing.T) {
	t.Helper()

	plant := `INSERT INTO unmanaged_targets
(namespace_id, name, pool_id, scopes_json, command_json, every_ms, timeout_ms, enabled)
SELECT namespace_id, ?, pool_id, scopes_json, command_json, every_ms, timeout_ms, enabled
FROM unmanaged_targets WHERE name = 'aws' AND namespace_id = ` + sqlTextAcmeID

	err := c.s.ExecForTest(t.Context(), plant, c.text)
	if err != nil {
		t.Fatalf("%q: plant the target: %v", c.text, err)
	}

	err = c.s.UnmanagedScans().DeleteTarget(t.Context(), "acme", c.text, c.fence, c.e)
	if !errors.Is(err, unmanaged.ErrInvalidTarget) {
		t.Errorf("%q: delete of the planted target = %v, want ErrInvalidTarget", c.text, err)
	}

	if n := c.count(t, "planted target", `SELECT COUNT(*) FROM unmanaged_targets WHERE name = ?`, c.text); n != 1 {
		t.Errorf("%q: %d planted targets after the refused delete, want 1", c.text, n)
	}

	sweeps, err := c.s.UnmanagedScans().Scans(t.Context(), "acme", c.text, "", unmanaged.MaxPage)
	if err != nil || len(sweeps) != 0 {
		t.Errorf("%q: sweeps of the target = %+v, %v; want none", c.text, sweeps, err)
	}

	if n := c.count(t, "target aws", `SELECT COUNT(*) FROM unmanaged_targets WHERE name = 'aws'`); n != 1 {
		t.Errorf("%q: %d targets aws, want 1", c.text, n)
	}
}

// poolName plants a pool with the text as its name. The port refuses a
// target on that pool in Target.Validate, before SQL, and the pool stays one
// row. TestSweepPoolIDKeepsSQLTextAsData sends the text to the pool query.
func (c sqlTextCase) poolName(t *testing.T, id string) {
	t.Helper()

	plant := `INSERT INTO runner_pools
(id, namespace_id, name, purpose, lease_ms, concurrency, isolation, git_mode, git_helper, created_at_ms, updated_at_ms)
SELECT ?, namespace_id, ?, purpose, lease_ms, concurrency, isolation, git_mode, git_helper, created_at_ms, updated_at_ms
FROM runner_pools WHERE name = ? AND namespace_id = ` + sqlTextAcmeID

	err := c.s.ExecForTest(t.Context(), plant, id, c.text, unmanagedtest.Pool)
	if err != nil {
		t.Fatalf("%q: plant the pool: %v", c.text, err)
	}

	onText := unmanagedTargetOf("acme", "on-text")
	onText.Pool = c.text

	err = c.s.UnmanagedScans().CreateTarget(t.Context(), onText, c.fence, c.e)
	if !errors.Is(err, unmanaged.ErrInvalidTarget) {
		t.Errorf("%q: create on the pool = %v, want ErrInvalidTarget", c.text, err)
	}

	if n := c.count(t, "planted pool", `SELECT COUNT(*) FROM runner_pools WHERE name = ?`, c.text); n != 1 {
		t.Errorf("%q: %d planted pools, want 1", c.text, n)
	}
}

// TestUnmanagedStartScanStartsTheSweepOperation proves that StartScan starts
// the durable operation of a cloud sweep of the pool of its target, in the
// same store.
func TestUnmanagedStartScanStartsTheSweepOperation(t *testing.T) {
	t.Parallel()

	s := openTemplated(t)
	fence := ensureNamespace(t, s, "acme")
	ensurePool(t, s, fence, unmanagedtest.Pool, runner.PurposeSweeps)
	e := sweepEntry("acme", "namespace:start-sweep", unmanagedAt())

	if err := s.UnmanagedScans().CreateTarget(t.Context(), unmanagedTargetOf("acme", "aws"), fence, e); err != nil {
		t.Fatalf("create the target: %v", err)
	}

	scan, err := s.UnmanagedScans().
		StartScan(t.Context(), unmanagedScanOf(unmanaged.ScanCloud, "aws"), fence, e)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	pools, err := s.Runners().Pools(t.Context(), "acme")
	if err != nil {
		t.Fatalf("pools: %v", err)
	}

	var poolID string

	for i := range pools {
		if pools[i].Name == unmanagedtest.Pool {
			poolID = pools[i].ID
		}
	}

	ops, err := s.DurableOperationsPort().List(t.Context(), durable.Filter{
		Kind: runner.SweepKind(poolID), States: nil, Limit: 0,
	})
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}

	if len(ops) != 1 || ops[0].Key != scan.ID || ops[0].State != durable.StatePending ||
		ops[0].Version != runner.AssignmentVersion {
		t.Fatalf("operations = %+v, want one pending operation for sweep %s", ops, scan.ID)
	}
}

// TestUnmanagedAdvanceScanAppendsTheAuditEntry proves that an advance to an
// end status appends the audit entry of the advance, with its fields.
func TestUnmanagedAdvanceScanAppendsTheAuditEntry(t *testing.T) {
	t.Parallel()

	s := openTemplated(t)
	fence := ensureNamespace(t, s, "acme")

	scan, err := s.UnmanagedScans().StartScan(t.Context(), unmanagedScanOf(unmanaged.ScanState, ""), fence,
		sweepEntry("acme", "namespace:start-sweep", unmanagedAt()))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	end := sweepEntry("acme", "namespace:end-sweep", unmanagedAt().Add(time.Minute))

	_, err = s.UnmanagedScans().AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme", ID: scan.ID, Event: unmanaged.ScanEventCancel,
		Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, fence, end)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}

	found := 0

	for _, entry := range allAuditEntries(t, s, "acme") {
		if entry.Action != end.Action {
			continue
		}

		found++

		if entry.Namespace != end.Namespace || entry.Subject != end.Subject || entry.Object != end.Object ||
			!entry.At.Equal(end.At) {
			t.Errorf("entry = %+v, want the fields of %+v", entry, end)
		}
	}

	if found != 1 {
		t.Errorf("entries with action %q = %d, want one", end.Action, found)
	}
}

// unmanagedSeed is what seedUnmanagedBackup wrote, as the source store reads
// it back.
type unmanagedSeed struct {
	target unmanaged.Target
	scan   unmanaged.Scan
}

// seedUnmanagedBackup makes the namespace acme with a pool of sweeps, a
// disabled sweep target and one cloud sweep of it that ended, with its scopes
// and counts, and returns the target and the sweep as the store reads them.
func seedUnmanagedBackup(t *testing.T, s *sqlite.Store) unmanagedSeed {
	t.Helper()

	ctx := t.Context()
	fence := ensureNamespace(t, s, "acme")
	ensurePool(t, s, fence, unmanagedtest.Pool, runner.PurposeSweeps)

	target := unmanagedTargetOf("acme", "aws")
	target.Enabled = false

	err := s.UnmanagedScans().CreateTarget(ctx, target, fence,
		sweepEntry("acme", "namespace:manage-sweeps", unmanagedAt()))
	if err != nil {
		t.Fatalf("create the sweep target: %v", err)
	}

	scan, err := s.UnmanagedScans().StartScan(ctx, unmanagedScanOf(unmanaged.ScanCloud, "aws"), fence,
		sweepEntry("acme", "namespace:start-sweep", unmanagedAt()))
	if err != nil {
		t.Fatalf("start the sweep: %v", err)
	}

	scopes := []unmanaged.ReportScope{
		{Provider: "aws", Scope: "111111111111", Regions: []string{"eu-west-1"}, Complete: true, Error: ""},
	}
	counts := unmanaged.ScanCounts{
		Records: 3, Managed: 2, Elsewhere: 0, NotInAnyUnit: 0, LeftBehind: 1, ManagedTwice: 0, Resolved: 0,
		Rejected: 0, OutOfScope: 0,
	}
	empty := unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""}

	for _, step := range []struct {
		event  unmanaged.ScanEvent
		update unmanaged.ScanUpdate
	}{
		{unmanaged.ScanEventDispatch, empty},
		{unmanaged.ScanEventClaim, empty},
		{unmanaged.ScanEventUploaded, unmanaged.ScanUpdate{Scopes: scopes, Counts: nil, Reason: ""}},
		{unmanaged.ScanEventMatched, unmanaged.ScanUpdate{Scopes: nil, Counts: &counts, Reason: ""}},
	} {
		scan, err = s.UnmanagedScans().AdvanceScan(ctx, unmanaged.ScanAdvance{
			Namespace: "acme", ID: scan.ID, Event: step.event, Update: step.update,
		}, fence, sweepEntry("acme", "namespace:start-sweep", unmanagedAt()))
		if err != nil {
			t.Fatalf("advance %s: %v", step.event, err)
		}
	}

	if scan.Status != unmanaged.ScanSucceeded {
		t.Fatalf("sweep status = %q, want succeeded", scan.Status)
	}

	targets, err := s.UnmanagedScans().Targets(ctx, "acme")
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets = %+v, %v, want one", targets, err)
	}

	stored, err := s.UnmanagedScans().Scan(ctx, "acme", scan.ID)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	return unmanagedSeed{target: targets[0], scan: stored}
}

// unmanagedBackupServices wires the backup and the restore service over the
// store st, as the server wires them.
func unmanagedBackupServices(
	t *testing.T, st *backupStore, live, target *fs.Store,
) (*backup.Service, *backup.RestoreService) {
	t.Helper()

	backups, err := backup.NewService(backup.Config{
		Jobs:     jobcheck.Checker{},
		Exporter: st, Kinds: st, Registry: st, Blobs: live, Target: target, TargetName: "dir:test",
		Operations: st.DurableOperationsPort(), Version: "test", StateOpener: nil, Owner: "", Lease: 0, Now: nil,
		MaxSQLBytes: 0, Logger: nil,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live: st, Operations: st.DurableOperationsPort(), Backups: backups,
		Maintenance: storetest.StateMaintenance{State: st, Hold: false}, Blobs: live, Leases: st,
		Target: target, Key: nil, Installation: "test", AfterApply: nil, Admit: nil, AllowDevBackups: false,
		AllowDevHint: "", Owner: "", Lease: 0, CleanupLockWait: 0, RetryBase: time.Millisecond, Now: nil, Logger: nil,
		AccessSetting:       nil,
		AccessSettingStatic: nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	return backups, restores
}

// unmanagedDriveRestore runs the due step of restore id until its row ends, in
// a bounded loop: the runner runs one restore in one call, and the bound only
// ends a stuck test.
func unmanagedDriveRestore(t *testing.T, restores *backup.RestoreService, id backup.RestoreID) {
	t.Helper()

	for range 2*len(backup.RestoreSteps()) + 10 {
		row, err := restores.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore %s: %v", id, err)
		}

		if row.State == backup.RestoreSucceeded {
			return
		}

		if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
			t.Fatalf("restore state = %q, want succeeded (error %q)", row.State, row.Error)
		}

		if runErr := restores.RunOnce(t.Context()); runErr != nil && !errors.Is(runErr, durable.ErrNoWork) {
			t.Fatalf("run restore %s: %v", id, runErr)
		}
	}

	t.Fatalf("restore %s never ended", id)
}

// unmanagedBlobs holds the live blob store and the backup target of a backup
// test.
type unmanagedBlobs struct {
	live   *fs.Store
	target *fs.Store
}

// openUnmanagedBlobs opens the live blob store and the backup target, each in
// its own temporary directory.
func openUnmanagedBlobs(t *testing.T) unmanagedBlobs {
	t.Helper()

	live, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open live blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := live.Close(); closeErr != nil {
			t.Errorf("close live blob store: %v", closeErr)
		}
	})

	target, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	return unmanagedBlobs{live: live, target: target}
}

// openUnmanagedBackupStore opens a fresh store of the kind production, as a
// backup and a restore need.
func openUnmanagedBackupStore(t *testing.T) *backupStore {
	t.Helper()

	st, ok := openBackup(t).(*backupStore)
	if !ok {
		t.Fatal("the backup store is not a *backupStore")
	}

	if _, err := st.ClaimKind(t.Context(), installation.KindProduction); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	return st
}

// checkUnmanagedRestored proves that the store dest holds the target and the
// sweep of seed, with their fields.
func checkUnmanagedRestored(t *testing.T, dest *backupStore, seed unmanagedSeed) {
	t.Helper()

	targets, err := dest.UnmanagedScans().Targets(t.Context(), "acme")
	if err != nil {
		t.Fatalf("targets after the restore: %v", err)
	}

	if len(targets) != 1 {
		t.Fatalf("targets after the restore = %+v, want one", targets)
	}

	if !reflect.DeepEqual(targets[0], seed.target) {
		t.Errorf("target after the restore = %+v, want %+v", targets[0], seed.target)
	}

	scan, err := dest.UnmanagedScans().Scan(t.Context(), "acme", seed.scan.ID)
	if err != nil {
		t.Fatalf("sweep after the restore: %v", err)
	}

	if !reflect.DeepEqual(scan, seed.scan) {
		t.Errorf("sweep after the restore = %+v, want %+v", scan, seed.scan)
	}
}

// TestUnmanagedBackupRestore proves that a backup holds the sweep targets and
// sweeps of a namespace, and that a restore into another store brings them
// back: a disabled target and an ended sweep with its scopes and counts.
func TestUnmanagedBackupRestore(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	blobs := openUnmanagedBlobs(t)
	source := openUnmanagedBackupStore(t)
	seed := seedUnmanagedBackup(t, source.Store)

	backups, _ := unmanagedBackupServices(t, source, blobs.live, blobs.target)

	backupID := backup.ID(ident.NewID("bkp-"))

	if _, err := backups.Run(ctx, backupID); err != nil {
		t.Fatalf("run backup %s: %v", backupID, err)
	}

	dest := openUnmanagedBackupStore(t)

	_, restores := unmanagedBackupServices(t, dest, blobs.live, blobs.target)

	restoreID := backup.MintRestoreID()
	request := backup.RestoreRequest{
		BackupID: backupID, PreBackupID: "", CheckOnly: false, Force: false, RequestedBy: "tester",
		KeepAccessTables: false,
	}

	if err := restores.Start(ctx, restoreID, request); err != nil {
		t.Fatalf("start restore %s: %v", restoreID, err)
	}

	unmanagedDriveRestore(t, restores, restoreID)
	checkUnmanagedRestored(t, dest, seed)
}
