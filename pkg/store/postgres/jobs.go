package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// JobStore is the persistence of jobs and change freezes: job.Store and
// job.FreezeStore over one database.
type JobStore interface {
	job.Store
	job.FreezeStore
	job.RecordStore
	job.ArtifactKeyStore
	job.FinalVersionReader
}

// Jobs returns the stores of jobs and change freezes. Each write checks the
// installation state and the fence of its namespace, and appends its audit
// entries in its own transaction.
//
//nolint:ireturn // The stores of jobs and freezes are two ports over one adapter.
func (s *Store) Jobs() JobStore {
	return jobstore.New(jobRunner{s: s})
}

// PutUnitStateVersion records version as the current state version of the unit
// unitID of the fence namespace, as the state write of its tool does. A plan
// phase reads it at its claim, and an apply phase dispatches only while it is
// the same.
func (s *Store) PutUnitStateVersion(ctx context.Context, fence namespace.Fence, unitID, version string) error {
	if unitID == "" {
		return fmt.Errorf("%w: a state version needs a unit ID", job.ErrInvalid)
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		now, err := storeNow(ctx, q)
		if err != nil {
			return err
		}

		return publishUnitState(ctx, q, unitStateWrite{
			nsName:  fence.Namespace.String(),
			unitID:  unitID,
			version: version,
			final:   false,
			origin:  identity.StateOrigin{Job: "", Commit: ""},
			atMs:    now,
		})
	})
}

// jobRunner opens the transactions of the job stores.
type jobRunner struct {
	s *Store
}

var _ jobstore.Runner = jobRunner{s: nil}

// Logger implements jobstore.Runner.
func (r jobRunner) Logger() *slog.Logger { return r.s.logger }

// Write implements jobstore.Runner. It takes the shared write lock, reads the
// installation state, and holds the namespace row FOR UPDATE before fn reads
// any rule. A manual lock write and the audit append of every fenced change
// take the same row, so a lock that committed before is seen by the dispatch,
// and a lock write that starts later waits for the dispatch.
func (r jobRunner) Write(ctx context.Context, fence namespace.Fence, fn func(jobstore.Tx) error) error {
	err := pgx.BeginFunc(ctx, r.s.txs, func(tx pgx.Tx) error {
		if _, err := readWritable(ctx, tx, readStateSQL, nil, r.s.binaryVersion); err != nil {
			return err
		}

		q := r.s.queries.WithTx(tx)
		if err := checkFenceForUpdate(ctx, q, fence); err != nil {
			return err
		}

		return fn(&jobTx{tx: tx, q: q})
	})
	if err != nil {
		return fmt.Errorf("transaction: %w", err)
	}

	return nil
}

// Read implements jobstore.Runner.
func (r jobRunner) Read(ctx context.Context, fn func(jobstore.Tx) error) error {
	err := pgx.BeginTxFunc(ctx, r.s.txs, r.s.snapshotOptions(), func(tx pgx.Tx) error {
		return fn(&jobTx{tx: tx, q: r.s.queries.WithTx(tx)})
	})
	if err != nil {
		return fmt.Errorf("snapshot transaction: %w", readConflict(err))
	}

	return nil
}

// jobTx is the jobstore.Tx over one PostgreSQL transaction.
type jobTx struct {
	tx pgx.Tx
	q  *db.Queries
}

var _ jobstore.Tx = (*jobTx)(nil)

// Now implements jobstore.Tx.
func (t *jobTx) Now(ctx context.Context) (time.Time, error) {
	return databaseNow(ctx, t.q)
}

// Job implements jobstore.Tx.
func (t *jobTx) Job(ctx context.Context, id job.ID) (jobstore.Row, bool, error) {
	var none jobstore.Row

	row, err := t.q.GetJob(ctx, string(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select job: %w", err)
	}

	return jobRow(row), true, nil
}

// JobByKey implements jobstore.Tx.
func (t *jobTx) JobByKey(ctx context.Context, ns namespace.Name, key string) (jobstore.Row, bool, error) {
	var none jobstore.Row

	row, err := t.q.GetJobByKey(ctx, db.GetJobByKeyParams{Namespace: ns.String(), IdempotencyKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select job by key: %w", err)
	}

	return jobRow(db.GetJobRow(row)), true, nil
}

// InsertJob implements jobstore.Tx.
func (t *jobTx) InsertJob(ctx context.Context, r jobstore.Row) error {
	seq, err := t.q.NextJobSeq(ctx, r.Namespace.String())
	if err != nil {
		return fmt.Errorf("read next job sequence: %w", err)
	}

	err = t.q.InsertJob(ctx, db.InsertJobParams{
		ID: string(r.ID), Namespace: r.Namespace.String(), UnitID: r.UnitID, Seq: seq, State: string(r.State),
		Wait: string(r.Wait), CurrentPhase: int64(r.Phase), CurrentChanges: r.Changes,
		CurrentOperation: r.Operation, IdempotencyKey: r.Key, Version: r.Version, Body: r.Body,
		CreatedAtMs: r.CreatedMs, UpdatedAtMs: r.UpdatedMs, StateChangedAtMs: r.StateChangedMs,
	})

	return jobIndexError("insert job", err)
}

// UpdateJob implements jobstore.Tx.
func (t *jobTx) UpdateJob(ctx context.Context, r jobstore.Row, prev int64) (bool, error) {
	return updateJobRow(ctx, t.q, r, prev)
}

// jobIndexError turns the unique violation of an index of the jobs table
// into the error of its rule, and wraps any other error of the driver. A
// unique violation of another index is job.ErrConflict. It keeps the text of
// the driver error, but not the error itself, so no caller can reach it.
func jobIndexError(what string, err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError

	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		return fmt.Errorf("%s: %w", what, err)
	}

	switch pgErr.ConstraintName {
	case "jobs_one_change":
		return jobstore.ErrHolderIndex
	case "jobs_key":
		return jobstore.ErrKeyIndex
	default:
		return fmt.Errorf("%s: %w: %s", what, job.ErrConflict, err.Error())
	}
}

// CountOpen implements jobstore.Tx.
func (t *jobTx) CountOpen(ctx context.Context, ns namespace.Name, unitID string) (int, error) {
	var (
		n   int64
		err error
	)

	if unitID == "" {
		n, err = t.q.CountOpenJobsOfNamespace(ctx, ns.String())
	} else {
		n, err = t.q.CountOpenJobsOfUnit(ctx, db.CountOpenJobsOfUnitParams{Namespace: ns.String(), UnitID: unitID})
	}

	if err != nil {
		return 0, fmt.Errorf("count open jobs: %w", err)
	}

	return int(n), nil
}

// HolderOf implements jobstore.Tx.
func (t *jobTx) HolderOf(
	ctx context.Context, ns namespace.Name, unitID string, except job.ID,
) (job.ID, bool, error) {
	id, err := t.q.GetJobHolderOfUnit(
		ctx,
		db.GetJobHolderOfUnitParams{Namespace: ns.String(), UnitID: unitID, ExceptID: string(except)},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select job that holds the unit: %w", err)
	}

	return job.ID(id), true, nil
}

// Awaiting implements jobstore.Tx.
func (t *jobTx) Awaiting(ctx context.Context, ns namespace.Name, unitID string) ([]jobstore.Row, error) {
	return awaitingRows(ctx, t.q, ns, unitID)
}

// List implements jobstore.Tx.
func (t *jobTx) List(ctx context.Context, q jobstore.Query) ([]jobstore.Row, error) {
	var (
		where []string
		args  []any
	)

	arg := func(v any) string {
		args = append(args, v)

		return "$" + strconv.Itoa(len(args))
	}

	if q.Namespace != "" {
		where = append(where, "n.name = "+arg(q.Namespace.String()))
	}

	if q.UnitID != "" {
		where = append(where, "j.unit_id = "+arg(q.UnitID))
	}

	if len(q.States) > 0 {
		marks := make([]string, len(q.States))
		for i, st := range q.States {
			marks[i] = arg(string(st))
		}

		where = append(where, "j.state IN ("+strings.Join(marks, ", ")+")")
	}

	if q.After != "" {
		where = append(
			where,
			"(j.seq, j.id) < (SELECT a.seq, a.id FROM jobs AS a WHERE a.id = "+arg(string(q.After))+")",
		)
	}

	if q.StateChangedBeforeMs > 0 {
		where = append(where, "j.state_changed_at_ms < "+arg(q.StateChangedBeforeMs))
	}

	if q.StateChangedAfterMs > 0 {
		where = append(where, "j.state_changed_at_ms > "+arg(q.StateChangedAfterMs))
	}

	query := `SELECT j.id, n.name, j.unit_id, j.seq, j.state, j.wait, j.current_phase, j.current_changes,
		j.current_operation, j.idempotency_key, j.version, j.body, j.created_at_ms, j.updated_at_ms,
		j.state_changed_at_ms
		FROM jobs AS j JOIN namespaces AS n ON n.id = j.namespace_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY j.seq DESC, j.id DESC LIMIT " + arg(q.Limit)

	rows, err := t.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select jobs: %w", err)
	}

	defer rows.Close()

	return scanJobRows(rows)
}

// scanJobRows reads every row of a job query.
func scanJobRows(rows pgx.Rows) ([]jobstore.Row, error) {
	var out []jobstore.Row

	for rows.Next() {
		var g db.GetJobRow

		err := rows.Scan(&g.ID, &g.Namespace, &g.UnitID, &g.Seq, &g.State, &g.Wait, &g.CurrentPhase,
			&g.CurrentChanges, &g.CurrentOperation, &g.IdempotencyKey, &g.Version, &g.Body, &g.CreatedAtMs,
			&g.UpdatedAtMs, &g.StateChangedAtMs)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}

		out = append(out, jobRow(g))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read jobs: %w", err)
	}

	return out, nil
}

// Orphans implements jobstore.Tx.
func (t *jobTx) Orphans(ctx context.Context, limit int) ([]jobstore.OrphanRow, error) {
	// The limit is at most jobstore.MaxLimit, so it fits in an int32.
	capped := int32(min(limit, jobstore.MaxLimit)) //nolint:gosec // See the line above.

	rows, err := t.q.ListOrphanJobs(ctx, capped)
	if err != nil {
		return nil, fmt.Errorf("select orphan jobs: %w", err)
	}

	out := make([]jobstore.OrphanRow, len(rows))

	for i := range rows {
		r := &rows[i]
		out[i] = jobstore.OrphanRow{
			Row: jobRow(db.GetJobRow{
				ID: r.ID, Namespace: r.Namespace, UnitID: r.UnitID, Seq: r.Seq, State: r.State, Wait: r.Wait,
				CurrentPhase: r.CurrentPhase, CurrentChanges: r.CurrentChanges, CurrentOperation: r.CurrentOperation,
				IdempotencyKey: r.IdempotencyKey, Version: r.Version, Body: r.Body, CreatedAtMs: r.CreatedAtMs,
				UpdatedAtMs: r.UpdatedAtMs, StateChangedAtMs: r.StateChangedAtMs,
			}),
			Missing: !r.OpState.Valid,
		}
	}

	return out, nil
}

func jobRow(r db.GetJobRow) jobstore.Row {
	return jobstore.Row{
		ID: job.ID(r.ID), Namespace: namespace.Name(r.Namespace), UnitID: r.UnitID, Seq: r.Seq,
		State: job.State(r.State), Wait: job.WaitReason(r.Wait), Phase: int(r.CurrentPhase), Changes: r.CurrentChanges,
		Operation: r.CurrentOperation, Key: r.IdempotencyKey, Version: r.Version, Body: r.Body,
		CreatedMs: r.CreatedAtMs, UpdatedMs: r.UpdatedAtMs, StateChangedMs: r.StateChangedAtMs,
	}
}

// ActiveFreeze implements jobstore.Tx.
func (t *jobTx) ActiveFreeze(ctx context.Context, ns namespace.Name, at time.Time) (string, bool, error) {
	id, err := t.q.GetActiveJobFreeze(ctx, db.GetActiveJobFreezeParams{Namespace: ns.String(), NowMs: at.UnixMilli()})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select active freeze: %w", err)
	}

	return id, true, nil
}

// CoveringLocks implements jobstore.Tx.
func (t *jobTx) CoveringLocks(ctx context.Context, ns namespace.Name, unitID string) ([]manuallock.Lock, error) {
	locks, err := coveringLocksOfUnitID(ctx, t.q, ns, unitID)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil, nil
	}

	return locks, err
}

// StateVersion implements jobstore.Tx.
func (t *jobTx) StateVersion(ctx context.Context, ns namespace.Name, unitID string) (string, error) {
	row, ok, err := unitState(ctx, t.q, ns.String(), unitID)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", nil
	}

	return row.VersionID, nil
}

// FinalVersion implements jobstore.Tx.
func (t *jobTx) FinalVersion(
	ctx context.Context, ns namespace.Name, unitID string,
) (jobstore.FinalVersion, bool, error) {
	var none jobstore.FinalVersion

	row, ok, err := unitState(ctx, t.q, ns.String(), unitID)
	if err != nil {
		return none, false, err
	}

	if !ok || row.FinalVersionID == "" {
		return none, false, nil
	}

	return jobstore.FinalVersion{
		UnitID: unitID, VersionID: row.FinalVersionID, Commit: row.FinalCommit, Job: job.ID(row.FinalJob),
		At: time.UnixMilli(row.FinalAtMs).UTC(),
	}, true, nil
}

// PutFinalVersion implements jobstore.Tx.
func (t *jobTx) PutFinalVersion(ctx context.Context, ns namespace.Name, v jobstore.FinalVersion) error {
	return publishUnitState(ctx, t.q, unitStateWrite{
		nsName:  ns.String(),
		unitID:  v.UnitID,
		version: v.VersionID,
		final:   true,
		origin:  identity.StateOrigin{Job: string(v.Job), Commit: v.Commit},
		atMs:    v.At.UnixMilli(),
	})
}

// InsertFreeze implements jobstore.Tx.
func (t *jobTx) InsertFreeze(ctx context.Context, f jobstore.FreezeRow) error {
	n, err := t.q.InsertJobFreeze(ctx, db.InsertJobFreezeParams{
		ID: f.ID, Namespace: f.Namespace.String(), Reason: f.Reason, StartsAtMs: f.StartsMs, EndsAtMs: f.EndsMs,
		CreatedBy: f.CreatedBy, EndedBy: f.EndedBy,
	})
	if err != nil {
		return fmt.Errorf("insert freeze: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("%w: freeze %s exists", job.ErrConflict, f.ID)
	}

	return nil
}

// Freeze implements jobstore.Tx.
func (t *jobTx) Freeze(ctx context.Context, id string) (jobstore.FreezeRow, bool, error) {
	var none jobstore.FreezeRow

	r, err := t.q.GetJobFreeze(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select freeze: %w", err)
	}

	return freezeRow(db.ListJobFreezesRow(r)), true, nil
}

// SetFreezeEnd implements jobstore.Tx.
func (t *jobTx) SetFreezeEnd(ctx context.Context, id string, endsMs int64, by string) error {
	if err := t.q.EndJobFreeze(ctx, db.EndJobFreezeParams{EndsAtMs: endsMs, EndedBy: by, ID: id}); err != nil {
		return fmt.Errorf("end freeze: %w", err)
	}

	return nil
}

// Freezes implements jobstore.Tx.
func (t *jobTx) Freezes(ctx context.Context, ns namespace.Name, sinceMs int64) ([]jobstore.FreezeRow, error) {
	rows, err := t.q.ListJobFreezes(ctx, db.ListJobFreezesParams{Namespace: ns.String(), SinceMs: sinceMs})
	if err != nil {
		return nil, fmt.Errorf("select freezes: %w", err)
	}

	out := make([]jobstore.FreezeRow, len(rows))
	for i := range rows {
		out[i] = freezeRow(rows[i])
	}

	return out, nil
}

func freezeRow(r db.ListJobFreezesRow) jobstore.FreezeRow {
	return jobstore.FreezeRow{
		ID: r.ID, Namespace: namespace.Name(r.Namespace), Reason: r.Reason, StartsMs: r.StartsAtMs,
		EndsMs: r.EndsAtMs, CreatedBy: r.CreatedBy, EndedBy: r.EndedBy,
	}
}

// StartOperation implements jobstore.Tx.
func (t *jobTx) StartOperation(ctx context.Context, req *durable.StartRequest) (string, error) {
	return startDurableOperation(ctx, t.q, req)
}

// CancelOperation implements jobstore.Tx.
func (t *jobTx) CancelOperation(ctx context.Context, id durable.ID) error {
	return cancelDurable(ctx, t.q, id)
}

// CheckClaim implements jobstore.Tx.
func (t *jobTx) CheckClaim(ctx context.Context, c durable.Claim) error {
	_, _, err := lockLive(ctx, t.q, c.Operation, c.Token)

	return err
}

// EndOperation implements jobstore.Tx.
func (t *jobTx) EndOperation(ctx context.Context, end *job.OperationEnd) error {
	var rule endRule

	switch end.Kind {
	case job.EndComplete:
		rule = completeEnd
	case job.EndFail:
		rule = failEnd(end.Failure)
	case job.EndRelease:
		rule = releaseEnd
	default:
		return fmt.Errorf("%w: unknown end %q", job.ErrInvalid, end.Kind)
	}

	return endClaim(ctx, t.q, end.Claim, rule)
}

// AppendAudit implements jobstore.Tx.
func (t *jobTx) AppendAudit(ctx context.Context, entries []audit.Entry) error {
	for i := range entries {
		if _, err := appendAuditEntry(ctx, t.q, entries[i]); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}
	}

	return nil
}

// InsertPlan implements jobstore.Tx.
func (t *jobTx) InsertPlan(ctx context.Context, ns namespace.Name, r jobstore.PlanRow) error {
	err := t.q.InsertJobPlan(ctx, db.InsertJobPlanParams{
		ID: r.ID, Namespace: ns.String(), JobID: string(r.Job), Body: r.Body, CreatedAtMs: r.CreatedMs,
	})
	if err != nil {
		return jobIndexError("insert plan", err)
	}

	return nil
}

// UpdatePlan implements jobstore.Tx.
func (t *jobTx) UpdatePlan(ctx context.Context, ns namespace.Name, r jobstore.PlanRow) error {
	params := db.UpdateJobPlanBodyParams{ID: r.ID, Namespace: ns.String(), Body: r.Body}

	if err := t.q.UpdateJobPlanBody(ctx, params); err != nil {
		return jobIndexError("update plan", err)
	}

	return nil
}

// ItemKeys implements jobstore.Tx.
//
//nolint:ireturn // The method of jobstore.Tx returns the interface.
func (t *jobTx) ItemKeys(fence namespace.Fence) secret.ItemKeyTx {
	return itemKeyTx{q: t.q, fence: fence}
}

// Plan implements jobstore.Tx.
func (t *jobTx) Plan(ctx context.Context, ns namespace.Name, id string) (jobstore.PlanRow, bool, error) {
	row, err := t.q.GetJobPlan(ctx, db.GetJobPlanParams{Namespace: ns.String(), ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobstore.PlanRow{ID: "", Job: "", Body: "", CreatedMs: 0}, false, nil
	}

	if err != nil {
		return jobstore.PlanRow{ID: "", Job: "", Body: "", CreatedMs: 0}, false, fmt.Errorf("select plan: %w", err)
	}

	return jobstore.PlanRow{ID: row.ID, Job: job.ID(row.JobID), Body: row.Body, CreatedMs: row.CreatedAtMs}, true, nil
}

// InsertLog implements jobstore.Tx.
func (t *jobTx) InsertLog(ctx context.Context, ns namespace.Name, r jobstore.LogRow) error {
	err := t.q.InsertJobLog(ctx, db.InsertJobLogParams{
		ID: r.ID, Namespace: ns.String(), JobID: string(r.Job), Phase: int64(r.Phase), Step: r.Step, Token: r.Token,
		CreatedAtMs: r.CreatedMs,
	})
	if err != nil {
		return jobIndexError("insert log", err)
	}

	return nil
}

// Log implements jobstore.Tx.
func (t *jobTx) Log(ctx context.Context, ns namespace.Name, id string) (jobstore.LogRow, bool, error) {
	none := jobstore.LogRow{ID: "", Job: "", Phase: 0, Step: "", Token: 0, CreatedMs: 0}

	row, err := t.q.GetJobLog(ctx, db.GetJobLogParams{Namespace: ns.String(), ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select log: %w", err)
	}

	return jobstore.LogRow{
		ID: row.ID, Job: job.ID(row.JobID), Phase: int(row.Phase), Step: row.Step, Token: row.Token,
		CreatedMs: row.CreatedAtMs,
	}, true, nil
}

// Chunk implements jobstore.Tx.
func (t *jobTx) Chunk(ctx context.Context, ns namespace.Name, log string, seq int) (job.ChunkRow, bool, error) {
	row, err := t.q.GetJobLogChunk(ctx, db.GetJobLogChunkParams{Namespace: ns.String(), LogID: log, Seq: int64(seq)})
	if errors.Is(err, pgx.ErrNoRows) {
		return job.ChunkRow{SHA256: "", Size: 0, Confirmed: false, BlobKey: ""}, false, nil
	}

	if err != nil {
		return job.ChunkRow{
			SHA256:    "",
			Size:      0,
			Confirmed: false,
			BlobKey:   "",
		}, false, fmt.Errorf(
			"select log chunk: %w",
			err,
		)
	}

	return job.ChunkRow{
		SHA256: row.Sha256, Size: row.Size, Confirmed: row.Confirmed == 1, BlobKey: row.BlobKey,
	}, true, nil
}

// InsertChunk implements jobstore.Tx.
func (t *jobTx) InsertChunk(ctx context.Context, ns namespace.Name, log string, seq int, c job.ChunkRow) error {
	err := t.q.InsertJobLogChunk(ctx, db.InsertJobLogChunkParams{
		LogID: log, Seq: int64(seq), Namespace: ns.String(), Sha256: c.SHA256, Size: c.Size,
	})
	if err != nil {
		return fmt.Errorf("insert log chunk: %w", err)
	}

	return nil
}

// ConfirmChunk implements jobstore.Tx.
func (t *jobTx) ConfirmChunk(
	ctx context.Context, ns namespace.Name, log string, seq int, stored jobstore.StoredChunk,
) error {
	n, err := t.q.ConfirmJobLogChunk(
		ctx,
		db.ConfirmJobLogChunkParams{
			LogID:      log,
			Seq:        int64(seq),
			Namespace:  ns.String(),
			StoredSize: stored.Digest.Size,
			StoredMd5:  stored.Digest.MD5[:],
			BlobKey:    stored.BlobKey,
		},
	)
	if err != nil {
		return fmt.Errorf("confirm log chunk: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("%w: chunk %d of log %s is not reserved", job.ErrInvalid, seq, log)
	}

	return nil
}

// ChunkKeys implements jobstore.Tx.
func (t *jobTx) ChunkKeys(ctx context.Context, ns namespace.Name, log string) ([]jobstore.ChunkKey, error) {
	rows, err := t.q.ListJobLogChunkKeys(ctx, db.ListJobLogChunkKeysParams{Namespace: ns.String(), LogID: log})
	if err != nil {
		return nil, fmt.Errorf("select log chunk keys: %w", err)
	}

	out := make([]jobstore.ChunkKey, 0, len(rows))
	for _, r := range rows {
		out = append(out, jobstore.ChunkKey{Seq: int(r.Seq), BlobKey: r.BlobKey})
	}

	return out, nil
}

// JobPlanIDs implements jobstore.ChunkTx.
func (t *jobTx) JobPlanIDs(ctx context.Context, ns namespace.Name, id job.ID) ([]string, error) {
	ids, err := t.q.ListJobPlanIDs(ctx, db.ListJobPlanIDsParams{Namespace: ns.String(), JobID: string(id)})
	if err != nil {
		return nil, fmt.Errorf("select plan IDs of job: %w", err)
	}

	return ids, nil
}

// JobChunkKeys implements jobstore.ChunkTx.
func (t *jobTx) JobChunkKeys(ctx context.Context, ns namespace.Name, id job.ID) ([]string, error) {
	keys, err := t.q.ListJobChunkBlobKeys(ctx, db.ListJobChunkBlobKeysParams{Namespace: ns.String(), JobID: string(id)})
	if err != nil {
		return nil, fmt.Errorf("select chunk keys of job: %w", err)
	}

	return keys, nil
}
