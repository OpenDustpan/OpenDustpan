package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/unmanagedstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// scanIDPrefix starts the ID of a sweep. The ID is the key of the durable
// operation of a cloud sweep, and that key is unique in the installation.
const scanIDPrefix = "scn-"

// UnmanagedScans returns the store of sweep targets and sweeps. Each write
// checks the installation state and the fence of its namespace, and appends its
// audit entry in its own transaction.
func (s *Store) UnmanagedScans() *UnmanagedScans {
	return &UnmanagedScans{s: s}
}

// UnmanagedScans is unmanaged.ScanStore over the database of a Store.
type UnmanagedScans struct {
	s *Store
}

var _ unmanaged.ScanStore = (*UnmanagedScans)(nil)

// CreateTarget implements unmanaged.ScanStore.
func (u *UnmanagedScans) CreateTarget(
	ctx context.Context, t unmanaged.Target, fence namespace.Fence, e audit.Entry,
) error {
	if err := checkTargetWrite(t, fence); err != nil {
		return err
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	return u.s.inFencedEntryTx(ctx, fence, &e, func(q *db.Queries) error {
		nsID, err := unmanagedNamespaceID(ctx, q, fence)
		if err != nil {
			return err
		}

		poolID, err := sweepPoolID(ctx, q, nsID, t.Pool)
		if err != nil {
			return err
		}

		row, err := unmanagedstore.EncodeTarget(t, t.Pool)
		if err != nil {
			return err
		}

		n, err := q.InsertUnmanagedTarget(ctx, db.InsertUnmanagedTargetParams{
			NamespaceID: nsID, Name: t.Name, PoolID: poolID, ScopesJson: row.ScopesJSON,
			CommandJson: row.CommandJSON, EveryMs: row.EveryMs, TimeoutMs: row.TimeoutMs, Enabled: row.Enabled,
		})
		if err != nil {
			return fmt.Errorf("insert sweep target: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: target %q of %s", unmanaged.ErrAlreadyExists, t.Name, t.Namespace)
		}

		// The write lock of the transaction is held, so the count is exact: no
		// other target of the namespace is written between the insert and the
		// commit.
		count, err := q.CountUnmanagedTargets(ctx, nsID)
		if err != nil {
			return fmt.Errorf("count sweep targets: %w", err)
		}

		if count > unmanaged.MaxTargets {
			return fmt.Errorf("%w: %s has %d sweep targets", unmanaged.ErrLimit, t.Namespace, unmanaged.MaxTargets)
		}

		return nil
	})
}

// UpdateTarget implements unmanaged.ScanStore.
func (u *UnmanagedScans) UpdateTarget(
	ctx context.Context, t unmanaged.Target, fence namespace.Fence, e audit.Entry,
) error {
	if err := checkTargetWrite(t, fence); err != nil {
		return err
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	return u.s.inFencedEntryTx(ctx, fence, &e, func(q *db.Queries) error {
		nsID, err := unmanagedNamespaceID(ctx, q, fence)
		if err != nil {
			return err
		}

		if _, targetErr := unmanagedTargetRow(ctx, q, nsID, t.Name); targetErr != nil {
			return targetErr
		}

		poolID, err := sweepPoolID(ctx, q, nsID, t.Pool)
		if err != nil {
			return err
		}

		row, err := unmanagedstore.EncodeTarget(t, t.Pool)
		if err != nil {
			return err
		}

		n, err := q.UpdateUnmanagedTarget(ctx, db.UpdateUnmanagedTargetParams{
			PoolID: poolID, ScopesJson: row.ScopesJSON, CommandJson: row.CommandJSON, EveryMs: row.EveryMs,
			TimeoutMs: row.TimeoutMs, Enabled: row.Enabled, NamespaceID: nsID, Name: t.Name,
		})
		if err != nil {
			return fmt.Errorf("update sweep target: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: target %q of %s", unmanaged.ErrNotFound, t.Name, t.Namespace)
		}

		return nil
	})
}

// DeleteTarget implements unmanaged.ScanStore.
func (u *UnmanagedScans) DeleteTarget(
	ctx context.Context, ns namespace.Name, name string, fence namespace.Fence, e audit.Entry,
) error {
	// Defense in depth: the fence check below compares the namespace of the
	// fence with the namespace row. This check stays so that a delete in one
	// namespace cannot run under the fence of another namespace. It does not
	// rely on the service.
	if ns != fence.Namespace {
		return fmt.Errorf("%w: target names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	return u.s.inFencedEntryTx(ctx, fence, &e, func(q *db.Queries) error {
		nsID, err := unmanagedNamespaceID(ctx, q, fence)
		if err != nil {
			return err
		}

		n, err := q.DeleteUnmanagedTarget(ctx, db.DeleteUnmanagedTargetParams{NamespaceID: nsID, Name: name})
		if err != nil {
			return fmt.Errorf("delete sweep target: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: target %q of %s", unmanaged.ErrNotFound, name, ns)
		}

		// The sweeps of the target that did not end are canceled now, with the
		// time of the entry that this transaction appends. A sweep in the match
		// keeps its status: the match ends on its own.
		_, err = q.CancelUnmanagedScansOfTarget(ctx, db.CancelUnmanagedScansOfTargetParams{
			EndedAtMs: codec.Millis(e.At), NamespaceID: nsID, Target: name,
		})
		if err != nil {
			return fmt.Errorf("cancel the sweeps of the target: %w", err)
		}

		return nil
	})
}

// Targets implements unmanaged.ScanStore. A row that fails its checks fails
// the read, so a damaged target never reads as a target that does not exist.
func (u *UnmanagedScans) Targets(ctx context.Context, ns namespace.Name) ([]unmanaged.Target, error) {
	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	rows, err := u.s.queries.ListUnmanagedTargets(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select sweep targets: %w", err)
	}

	out := make([]unmanaged.Target, 0, len(rows))

	for _, row := range rows {
		t, tErr := unmanagedstore.Target(ns, unmanagedstore.TargetRow{
			Namespace: row.Namespace, Name: row.Name, Pool: row.PoolName, ScopesJSON: row.ScopesJson,
			CommandJSON: row.CommandJson, EveryMs: row.EveryMs, TimeoutMs: row.TimeoutMs, Enabled: row.Enabled,
		})
		if tErr != nil {
			return nil, tErr
		}

		out = append(out, t)
	}

	return out, nil
}

// StartScan implements unmanaged.ScanStore.
func (u *UnmanagedScans) StartScan(
	ctx context.Context, s unmanaged.Scan, fence namespace.Fence, e audit.Entry,
) (unmanaged.Scan, error) {
	// Defense in depth: the fence check below compares the namespace of the
	// fence with the namespace row. This check stays so that a sweep of one
	// namespace cannot start under the fence of another namespace. It does not
	// rely on the service.
	if s.Namespace != fence.Namespace {
		return unmanaged.Scan{}, fmt.Errorf("%w: sweep names %q, fence names %q",
			namespace.ErrFenced, s.Namespace, fence.Namespace)
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	var out unmanaged.Scan

	err := u.s.inFencedEntryTx(ctx, fence, &e, func(q *db.Queries) error {
		started, err := startScan(ctx, q, fence, s)
		out = started

		return err
	})
	if err != nil {
		return unmanaged.Scan{}, err
	}

	return out, nil
}

// startScan stores s as queued in the transaction of q and starts the durable
// operation that runs a cloud sweep. It returns the stored sweep.
func startScan(
	ctx context.Context, q *db.Queries, fence namespace.Fence, s unmanaged.Scan,
) (unmanaged.Scan, error) {
	nsID, err := unmanagedNamespaceID(ctx, q, fence)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	pool, err := sweepPoolOf(ctx, q, nsID, s)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	err = checkScanIdle(ctx, q, nsID, s)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	seq, err := q.NextUnmanagedScanSeq(ctx, nsID)
	if err != nil {
		return unmanaged.Scan{}, fmt.Errorf("select the next sweep sequence number: %w", err)
	}

	// The ID is new, so it is the key of the durable operation of a cloud
	// sweep below, and that operation is new too.
	s.ID, s.Status = ident.NewID(scanIDPrefix), unmanaged.ScanQueued

	err = insertScan(ctx, q, nsID, s, seq)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	if pool == "" {
		return s, nil
	}

	return s, startSweepOperation(ctx, q, s, pool)
}

// sweepPoolOf returns the ID of the runner pool of the target of s, or "" for a
// state check, which reads the index in the server. It returns ErrNotFound for
// a target that is not there, and ErrInvalidTarget for a pool that does not run
// sweeps or that belongs to another namespace.
func sweepPoolOf(ctx context.Context, q *db.Queries, nsID string, s unmanaged.Scan) (string, error) {
	if s.Kind != unmanaged.ScanCloud {
		return "", nil
	}

	target, err := unmanagedTargetRow(ctx, q, nsID, s.Target)
	if err != nil {
		return "", err
	}

	// Defense in depth: the read query joins the pool by namespace too; this
	// check stays so that a damaged row cannot start a sweep on a pool of
	// another namespace.
	if target.PoolNamespaceID != nsID {
		return "", fmt.Errorf("%w: the pool of target %q belongs to another namespace",
			unmanaged.ErrInvalidTarget, s.Target)
	}

	// Defense in depth: the purpose of a pool is set when the pool is made and
	// never changes, so a stored target names a sweep pool. This check stays so
	// that a row of a restore cannot start a sweep on a pool that applies
	// changes.
	if target.Purpose != string(runner.PurposeSweeps) {
		return "", fmt.Errorf("%w: the pool of target %q runs %s, not sweeps",
			unmanaged.ErrInvalidTarget, s.Target, target.Purpose)
	}

	return target.PoolID, nil
}

// checkScanIdle refuses a start while the target of s has a sweep of the same
// kind that did not end.
func checkScanIdle(ctx context.Context, q *db.Queries, nsID string, s unmanaged.Scan) error {
	busy, err := q.CountOpenUnmanagedScansOfTarget(ctx, db.CountOpenUnmanagedScansOfTargetParams{
		NamespaceID: nsID, Target: s.Target, Kind: string(s.Kind),
	})
	if err != nil {
		return fmt.Errorf("count the sweeps of the target that did not end: %w", err)
	}

	if busy > 0 {
		return fmt.Errorf("%w: target %q of %s", unmanaged.ErrScanBusy, s.Target, s.Namespace)
	}

	return nil
}

// insertScan stores s with the sequence number seq.
func insertScan(ctx context.Context, q *db.Queries, nsID string, s unmanaged.Scan, seq int64) error {
	row, err := unmanagedstore.EncodeScan(s, seq)
	if err != nil {
		return err
	}

	err = q.InsertUnmanagedScan(ctx, db.InsertUnmanagedScanParams{
		ID: row.ID, NamespaceID: nsID, Seq: row.Seq, Target: row.Target, Kind: row.Kind, Status: row.Status,
		StartedBy: row.StartedBy, Reason: row.Reason, ScopesJson: row.ScopesJSON, CountsJson: row.CountsJSON,
		CreatedAtMs: row.CreatedAtMs, EndedAtMs: row.EndedAtMs,
	})
	if err != nil {
		return fmt.Errorf("insert sweep: %w", err)
	}

	return nil
}

// startSweepOperation starts the durable operation that a runner of the pool of
// the target of s claims. It runs in the transaction that stores the sweep, so
// a cloud sweep is never stored without the operation that runs it.
func startSweepOperation(ctx context.Context, q *db.Queries, s unmanaged.Scan, pool string) error {
	_, created, err := startDurable(ctx, q, durable.StartRequest{
		Key: s.ID, Kind: runner.SweepKind(pool), Version: runner.AssignmentVersion, Input: nil,
	})
	if err != nil {
		return err
	}

	if !created {
		return fmt.Errorf("start the operation of sweep %q: %w", s.ID, durable.ErrKeyConflict)
	}

	return nil
}

// AdvanceScan implements unmanaged.ScanStore. A status in the middle of the
// life of a sweep appends no entry: the sweep ends once, and the entry of its
// end holds its counts.
func (u *UnmanagedScans) AdvanceScan(
	ctx context.Context, a unmanaged.ScanAdvance, fence namespace.Fence, e audit.Entry,
) (unmanaged.Scan, error) {
	// Defense in depth: the fence check below compares the namespace of the
	// fence with the namespace row. This check stays so that a sweep of one
	// namespace cannot advance under the fence of another namespace. It does
	// not rely on the service.
	if a.Namespace != fence.Namespace {
		return unmanaged.Scan{}, fmt.Errorf("%w: sweep names %q, fence names %q",
			namespace.ErrFenced, a.Namespace, fence.Namespace)
	}

	if err := checkEntry(e, fence); err != nil {
		return unmanaged.Scan{}, err
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	var out unmanaged.Scan

	err := u.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		next, err := advanceScan(ctx, q, fence, scanAdvance{
			ns: a.Namespace, id: a.ID, ev: a.Event, upd: a.Update, e: e,
		})
		out = next

		return err
	})
	if err != nil {
		return unmanaged.Scan{}, err
	}

	return out, nil
}

// scanAdvance is one advance of a sweep: the sweep, the event, the update of
// the event, and the entry that the end of the sweep appends.
type scanAdvance struct {
	ns  namespace.Name
	id  string
	ev  unmanaged.ScanEvent
	upd unmanaged.ScanUpdate
	e   audit.Entry
}

// advanceScan applies the event of a with its update to the sweep, and appends
// the entry when the new status ends the sweep. It returns the new sweep.
func advanceScan(
	ctx context.Context, q *db.Queries, fence namespace.Fence, a scanAdvance,
) (unmanaged.Scan, error) {
	nsID, err := unmanagedNamespaceID(ctx, q, fence)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	s, err := storedScan(ctx, q, a.ns, a.id)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	to, err := unmanaged.NextScan(s.Kind, s.Status, a.ev)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	s.Status = to

	if a.upd.Scopes != nil {
		s.Scopes = slices.Clone(a.upd.Scopes)
	}

	if a.upd.Counts != nil {
		s.Counts = *a.upd.Counts
	}

	if a.upd.Reason != "" {
		s.Reason = a.upd.Reason
	}

	if to.Terminal() {
		s.EndedAt = a.e.At
	}

	err = saveScan(ctx, q, nsID, a.id, s)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	if !to.Terminal() {
		return s, nil
	}

	_, err = appendAuditEntry(ctx, q, a.e)
	if err != nil {
		return unmanaged.Scan{}, fmt.Errorf("append audit entry: %w", err)
	}

	return s, nil
}

// storedScan returns the sweep id of ns. It returns ErrNotFound for a sweep
// that is not there, and ErrCorrupt for a row that fails its checks.
func storedScan(ctx context.Context, q *db.Queries, ns namespace.Name, id string) (unmanaged.Scan, error) {
	row, err := q.GetUnmanagedScan(ctx, db.GetUnmanagedScanParams{Namespace: ns.String(), ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return unmanaged.Scan{}, fmt.Errorf("%w: sweep %q of %s", unmanaged.ErrNotFound, id, ns)
	}

	if err != nil {
		return unmanaged.Scan{}, fmt.Errorf("select sweep: %w", err)
	}

	return unmanagedstore.Scan(ns, scanRowOfGet(row))
}

// saveScan writes the status, the reason, the scopes, the counts and the end of
// the sweep id.
func saveScan(ctx context.Context, q *db.Queries, nsID, id string, s unmanaged.Scan) error {
	scopes, err := unmanagedstore.EncodeReportScopes(s.Scopes)
	if err != nil {
		return err
	}

	counts, err := unmanagedstore.EncodeCounts(s.Counts)
	if err != nil {
		return err
	}

	err = q.UpdateUnmanagedScan(ctx, db.UpdateUnmanagedScanParams{
		Status: string(s.Status), Reason: s.Reason, ScopesJson: scopes, CountsJson: counts,
		EndedAtMs: codec.Millis(s.EndedAt), NamespaceID: nsID, ID: id,
	})
	if err != nil {
		return fmt.Errorf("update sweep: %w", err)
	}

	return nil
}

// Scan implements unmanaged.ScanStore. A row that fails its checks fails the
// read, so a damaged sweep never reads as a sweep that does not exist.
func (u *UnmanagedScans) Scan(ctx context.Context, ns namespace.Name, id string) (unmanaged.Scan, error) {
	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	row, err := u.s.queries.GetUnmanagedScan(ctx, db.GetUnmanagedScanParams{Namespace: ns.String(), ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return unmanaged.Scan{}, fmt.Errorf("%w: sweep %q of %s", unmanaged.ErrNotFound, id, ns)
	}

	if err != nil {
		return unmanaged.Scan{}, fmt.Errorf("select sweep: %w", err)
	}

	return unmanagedstore.Scan(ns, scanRowOfGet(row))
}

// Scans implements unmanaged.ScanStore. A row that fails its checks fails the
// read.
func (u *UnmanagedScans) Scans(
	ctx context.Context, ns namespace.Name, target, after string, limit int,
) ([]unmanaged.Scan, error) {
	if limit < 1 || limit > unmanaged.MaxPage {
		return nil, fmt.Errorf("%w: page size %d", unmanaged.ErrLimit, limit)
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	rows, err := u.s.queries.ListUnmanagedScans(ctx, db.ListUnmanagedScansParams{
		Namespace: ns.String(), Target: target, After: after, PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select sweeps: %w", err)
	}

	out := make([]unmanaged.Scan, 0, len(rows))

	for i := range rows {
		s, sErr := unmanagedstore.Scan(ns, scanRowOfList(rows[i]))
		if sErr != nil {
			return nil, sErr
		}

		out = append(out, s)
	}

	return out, nil
}

// scanRowOfGet returns the row of a sweep that GetUnmanagedScan read.
func scanRowOfGet(row db.GetUnmanagedScanRow) unmanagedstore.ScanRow {
	return unmanagedstore.ScanRow{
		ID: row.ID, Namespace: row.Namespace, Seq: row.Seq, Target: row.Target, Kind: row.Kind,
		Status: row.Status, StartedBy: row.StartedBy, Reason: row.Reason, ScopesJSON: row.ScopesJson,
		CountsJSON: row.CountsJson, CreatedAtMs: row.CreatedAtMs, EndedAtMs: row.EndedAtMs,
	}
}

// scanRowOfList returns the row of a sweep that ListUnmanagedScans read.
func scanRowOfList(row db.ListUnmanagedScansRow) unmanagedstore.ScanRow {
	return unmanagedstore.ScanRow{
		ID: row.ID, Namespace: row.Namespace, Seq: row.Seq, Target: row.Target, Kind: row.Kind,
		Status: row.Status, StartedBy: row.StartedBy, Reason: row.Reason, ScopesJSON: row.ScopesJson,
		CountsJSON: row.CountsJson, CreatedAtMs: row.CreatedAtMs, EndedAtMs: row.EndedAtMs,
	}
}

// checkTargetWrite validates t and checks that it belongs to the namespace of
// fence.
func checkTargetWrite(t unmanaged.Target, fence namespace.Fence) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("write sweep target: %w", err)
	}

	// Defense in depth: the fence check below compares the namespace of the
	// fence with the namespace row. This check stays so that a target of one
	// namespace cannot be written under the fence of another namespace. It does
	// not rely on the service.
	if t.Namespace != fence.Namespace {
		return fmt.Errorf("%w: target names %q, fence names %q", namespace.ErrFenced, t.Namespace, fence.Namespace)
	}

	return nil
}

// inFencedEntryTx runs fn and appends e to the chain of the namespace of fence
// in one fenced transaction, so the change and its entry commit together, or
// neither does. It checks that e names the namespace of the fence. The caller
// builds the other fields of e, and the store keeps them as they are.
func (s *Store) inFencedEntryTx(
	ctx context.Context, fence namespace.Fence, e *audit.Entry, fn func(q *db.Queries) error,
) error {
	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, *e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// checkEntry returns audit.ErrInvalidEntry when e names another namespace than
// fence.
func checkEntry(e audit.Entry, fence namespace.Fence) error {
	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return nil
}

// unmanagedNamespaceID returns the ID of the namespace of fence, in the
// transaction of q. It returns namespace.ErrNotFound for a namespace that is
// not there.
func unmanagedNamespaceID(ctx context.Context, q *db.Queries, fence namespace.Fence) (string, error) {
	nsID, err := q.GetNamespaceID(ctx, fence.Namespace.String())
	if errors.Is(err, sql.ErrNoRows) {
		return "", namespace.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("select namespace ID: %w", err)
	}

	return nsID, nil
}

// unmanagedTargetRow returns the stored pool of the sweep target name of the
// namespace nsID. It returns ErrNotFound for a target that is not there.
func unmanagedTargetRow(ctx context.Context, q *db.Queries, nsID, name string) (db.GetUnmanagedTargetRow, error) {
	row, err := q.GetUnmanagedTarget(ctx, db.GetUnmanagedTargetParams{NamespaceID: nsID, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return db.GetUnmanagedTargetRow{}, fmt.Errorf("%w: sweep target %q", unmanaged.ErrNotFound, name)
	}

	if err != nil {
		return db.GetUnmanagedTargetRow{}, fmt.Errorf("select sweep target: %w", err)
	}

	return row, nil
}

// sweepPoolID returns the ID of the runner pool named pool in the namespace
// nsID. It returns ErrInvalidTarget for a pool that is not there, or that does
// not run sweeps.
func sweepPoolID(ctx context.Context, q *db.Queries, nsID, pool string) (string, error) {
	row, err := q.GetRunnerPoolByName(ctx, db.GetRunnerPoolByNameParams{NamespaceID: nsID, Name: pool})
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: the namespace has no runner pool %q", unmanaged.ErrInvalidTarget, pool)
	}

	if err != nil {
		return "", fmt.Errorf("select runner pool by name: %w", err)
	}

	if row.Purpose != string(runner.PurposeSweeps) {
		return "", fmt.Errorf("%w: pool %q runs %s", unmanaged.ErrInvalidTarget, pool, row.Purpose)
	}

	return row.ID, nil
}
