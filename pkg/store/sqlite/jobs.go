package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
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
//nolint:ireturn // Store-agnostic wiring takes the ports.
func (s *Store) Jobs() JobStore {
	return jobstore.New(jobRunner{s: s})
}

// jobRunner opens the transactions of the job stores.
type jobRunner struct {
	s *Store
}

var _ jobstore.Runner = jobRunner{s: nil}

// Logger implements jobstore.Runner.
func (r jobRunner) Logger() *slog.Logger { return r.s.logger }

// Write implements jobstore.Runner.
func (r jobRunner) Write(ctx context.Context, fence namespace.Fence, fn func(jobstore.Tx) error) error {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	release, holdErr := r.s.holdWrite(ctx)
	if holdErr != nil {
		return holdErr
	}
	defer release()

	// The write can make item key records (jobTx.ItemKeys), so the
	// transaction has an attempt deadline.
	attempt, txCtx, cancel := r.s.newAttempt(ctx)
	defer cancel()

	err := r.s.jobTx(txCtx, false, func(tx *sql.Tx, q *db.Queries) error {
		if err := r.s.checkWritable(txCtx, q); err != nil {
			return err
		}

		version, err := q.GetFailoverVersion(txCtx, fence.Namespace.String())
		if errors.Is(err, sql.ErrNoRows) {
			return namespace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("read failover version: %w", err)
		}

		if version != fence.FailoverVersion {
			return namespace.ErrFenced
		}

		if err := fn(&jobTx{tx: tx, q: q, attempt: attempt}); err != nil {
			return err
		}

		// An item key record that this transaction wrote names a blob that
		// the reaper may delete after the deadline, so a late commit fails.
		return attempt.beforeCommit()
	})

	return attempt.attemptError(ctx, err)
}

// Read implements jobstore.Runner.
func (r jobRunner) Read(ctx context.Context, fn func(jobstore.Tx) error) error {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	return r.s.jobTx(ctx, true, func(tx *sql.Tx, q *db.Queries) error {
		return fn(&jobTx{tx: tx, q: q, attempt: nil})
	})
}

// jobTx runs fn in a transaction, and gives it the transaction for the
// hand-written queries. A write transaction is IMMEDIATE, as every write of
// the store is; a read transaction takes no write lock.
func (s *Store) jobTx(ctx context.Context, readOnly bool, fn func(tx *sql.Tx, q *db.Queries) error) error {
	opts := &sql.TxOptions{Isolation: sql.LevelDefault, ReadOnly: readOnly}

	tx, err := s.conn.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx, s.queries.WithTx(tx)); err != nil {
		return errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// jobTx is the jobstore.Tx over one SQLite transaction.
type jobTx struct {
	tx *sql.Tx
	q  *db.Queries
	// attempt is the attempt deadline of a write transaction, and nil in a
	// read transaction, which writes no item key record.
	attempt *itemKeyAttempt
}

var _ jobstore.Tx = (*jobTx)(nil)

// Now implements jobstore.Tx.
func (t *jobTx) Now(ctx context.Context) (time.Time, error) {
	return databaseNow(ctx, t.q)
}

// Job implements jobstore.Tx.
func (t *jobTx) Job(ctx context.Context, id job.ID) (jobstore.Row, bool, error) {
	row, err := t.q.GetJob(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return zeroRow(), false, nil
	}

	if err != nil {
		return zeroRow(), false, fmt.Errorf("select job: %w", err)
	}

	return jobRow(row), true, nil
}

// JobByKey implements jobstore.Tx.
func (t *jobTx) JobByKey(ctx context.Context, ns namespace.Name, key string) (jobstore.Row, bool, error) {
	row, err := t.q.GetJobByKey(ctx, db.GetJobByKeyParams{Namespace: ns.String(), IdempotencyKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return zeroRow(), false, nil
	}

	if err != nil {
		return zeroRow(), false, fmt.Errorf("select job by key: %w", err)
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
	switch {
	case err == nil:
		return nil
	case isUniqueViolation(err) && strings.Contains(err.Error(), "jobs.unit_id"):
		return jobstore.ErrHolderIndex
	case isUniqueViolation(err) && strings.Contains(err.Error(), "jobs.idempotency_key"):
		return jobstore.ErrKeyIndex
	case isUniqueViolation(err):
		return fmt.Errorf("%s: %w: %s", what, job.ErrConflict, err.Error())
	default:
		return fmt.Errorf("%s: %w", what, err)
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
	if errors.Is(err, sql.ErrNoRows) {
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
	query, args := listJobsQuery(q)

	rows, err := t.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select jobs: %w", err)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // rows.Err below reports a read failure; a close error adds nothing.
	}()

	var out []jobstore.Row

	for rows.Next() {
		var g db.GetJobRow

		err := rows.Scan(&g.ID, &g.Namespace, &g.NamespaceID, &g.UnitID, &g.Seq, &g.State, &g.Wait, &g.CurrentPhase,
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

// listJobsQuery builds the list query of q: the filters that q names, newest
// first, with the limit of q. Every value is an argument.
func listJobsQuery(q jobstore.Query) (string, []any) {
	var (
		where []string
		args  []any
	)

	if q.Namespace != "" {
		where = append(where, "n.name = ?")
		args = append(args, q.Namespace.String())
	}

	if q.UnitID != "" {
		where = append(where, "j.unit_id = ?")
		args = append(args, q.UnitID)
	}

	if len(q.States) > 0 {
		marks := make([]string, len(q.States))
		for i, st := range q.States {
			marks[i] = "?"

			args = append(args, string(st))
		}

		where = append(where, "j.state IN ("+strings.Join(marks, ", ")+")")
	}

	if q.After != "" {
		where = append(where, "(j.seq, j.id) < (SELECT a.seq, a.id FROM jobs AS a WHERE a.id = ?)")
		args = append(args, string(q.After))
	}

	if q.StateChangedBeforeMs > 0 {
		where = append(where, "j.state_changed_at_ms < ?")
		args = append(args, q.StateChangedBeforeMs)
	}

	if q.StateChangedAfterMs > 0 {
		where = append(where, "j.state_changed_at_ms > ?")
		args = append(args, q.StateChangedAfterMs)
	}

	query := `SELECT j.id, n.name, j.namespace_id, j.unit_id, j.seq, j.state, j.wait, j.current_phase, j.current_changes,
		j.current_operation, j.idempotency_key, j.version, j.body, j.created_at_ms, j.updated_at_ms,
		j.state_changed_at_ms
		FROM jobs AS j JOIN namespaces AS n ON n.id = j.namespace_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(
			where,
			" AND ",
		) // #nosec G202 // Fixed fragments only; every value is an argument.
	}

	query += " ORDER BY j.seq DESC, j.id DESC LIMIT ?"

	return query, append(args, q.Limit)
}

// Orphans implements jobstore.Tx.
func (t *jobTx) Orphans(ctx context.Context, limit int) ([]jobstore.OrphanRow, error) {
	rows, err := t.q.ListOrphanJobs(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("select orphan jobs: %w", err)
	}

	out := make([]jobstore.OrphanRow, len(rows))

	for i := range rows {
		r := &rows[i]
		out[i] = jobstore.OrphanRow{
			Row: jobRow(db.GetJobRow{
				ID: r.ID, Namespace: r.Namespace, NamespaceID: r.NamespaceID, UnitID: r.UnitID, Seq: r.Seq,
				State: r.State, Wait: r.Wait, CurrentPhase: r.CurrentPhase, CurrentChanges: r.CurrentChanges,
				CurrentOperation: r.CurrentOperation, IdempotencyKey: r.IdempotencyKey, Version: r.Version,
				Body: r.Body, CreatedAtMs: r.CreatedAtMs, UpdatedAtMs: r.UpdatedAtMs,
				StateChangedAtMs: r.StateChangedAtMs,
			}),
			Missing: !r.OpState.Valid,
		}
	}

	return out, nil
}

func jobRow(r db.GetJobRow) jobstore.Row {
	return jobstore.Row{
		ID:             job.ID(r.ID),
		Namespace:      namespace.Name(r.Namespace),
		NamespaceID:    namespace.ID(r.NamespaceID),
		UnitID:         r.UnitID,
		Seq:            r.Seq,
		State:          job.State(r.State),
		Wait:           job.WaitReason(r.Wait),
		Phase:          int(r.CurrentPhase),
		Changes:        r.CurrentChanges,
		Operation:      r.CurrentOperation,
		Key:            r.IdempotencyKey,
		Version:        r.Version,
		Body:           r.Body,
		CreatedMs:      r.CreatedAtMs,
		UpdatedMs:      r.UpdatedAtMs,
		StateChangedMs: r.StateChangedAtMs,
	}
}

// ActiveFreeze implements jobstore.Tx.
func (t *jobTx) ActiveFreeze(ctx context.Context, ns namespace.Name, at time.Time) (string, bool, error) {
	id, err := t.q.GetActiveJobFreeze(ctx, db.GetActiveJobFreezeParams{Namespace: ns.String(), NowMs: at.UnixMilli()})
	if errors.Is(err, sql.ErrNoRows) {
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
	row, ok, err := unitState(ctx, t.q, ns.String(), unitID)
	if err != nil {
		return zeroFinal(), false, err
	}

	if !ok || row.FinalVersionID == "" {
		return zeroFinal(), false, nil
	}

	return jobstore.FinalVersion{
		UnitID: unitID, VersionID: row.FinalVersionID, Commit: row.FinalCommit, Job: job.ID(row.FinalJob),
		At: time.UnixMilli(row.FinalAtMs).UTC(),
	}, true, nil
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
	r, err := t.q.GetJobFreeze(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return zeroFreeze(), false, nil
	}

	if err != nil {
		return zeroFreeze(), false, fmt.Errorf("select freeze: %w", err)
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
	_, _, err := liveDurable(ctx, t.q, c.Operation, c.Token)

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

// zeroRow returns the row that a lookup answers when it finds none.
func zeroRow() jobstore.Row {
	return jobstore.Row{
		ID: "", Namespace: "", NamespaceID: "", UnitID: "", Seq: 0, State: "", Wait: "", Phase: 0, Changes: false,
		Operation: "", Key: "", Version: 0, Body: "", CreatedMs: 0, UpdatedMs: 0, StateChangedMs: 0,
	}
}

// zeroFinal returns the final version that a lookup answers when it finds
// none.
func zeroFinal() jobstore.FinalVersion {
	return jobstore.FinalVersion{UnitID: "", VersionID: "", Commit: "", Job: "", At: time.Time{}}
}

// zeroFreeze returns the freeze that a lookup answers when it finds none.
func zeroFreeze() jobstore.FreezeRow {
	return jobstore.FreezeRow{
		ID: "", Namespace: "", Reason: "", StartsMs: 0, EndsMs: 0, CreatedBy: "", EndedBy: "",
	}
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
	return itemKeyTx{q: t.q, fence: fence, attempt: t.attempt}
}

// Plan implements jobstore.Tx.
func (t *jobTx) Plan(ctx context.Context, ns namespace.Name, id string) (jobstore.PlanRow, bool, error) {
	row, err := t.q.GetJobPlan(ctx, db.GetJobPlanParams{Namespace: ns.String(), ID: id})
	if errors.Is(err, sql.ErrNoRows) {
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
	if errors.Is(err, sql.ErrNoRows) {
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
	if errors.Is(err, sql.ErrNoRows) {
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
