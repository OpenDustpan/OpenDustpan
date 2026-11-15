package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// unitStateKind is the kind of a write of unit_state: a state write that
// publishes a version, the end of an update that marks the current version
// final, a lock release that marks the current version final, or a restore
// derive that writes the versions of the restored units.
type unitStateKind int

const (
	// unitStateWriteState publishes a version as the current state version.
	unitStateWriteState unitStateKind = iota + 1
	// unitStateWriteEnd marks the current version final at the end of an update.
	unitStateWriteEnd
	// unitStateWriteRelease marks the current version final at a lock release.
	unitStateWriteRelease
	// unitStateWriteRestore writes the state version of every restored unit.
	unitStateWriteRestore
)

// errUnitStateKind reports a write of a kind that the store does not know.
var errUnitStateKind = errors.New("unit state write kind is unknown")

// valid reports whether k is one of the write kinds.
func (k unitStateKind) valid() bool {
	return k >= unitStateWriteState && k <= unitStateWriteRestore
}

// unitStateWrite is one write of unit_state: its kind, the unit, the version
// that a state write makes current, whether that write also makes it the last
// final version, the origin of the write and its time on the clock of the
// store. A restore derive carries the transaction that it writes in, and every
// write carries the logger that records a damaged job row.
type unitStateWrite struct {
	kind    unitStateKind
	nsName  string
	unitID  string
	version string
	final   bool
	origin  identity.StateOrigin
	atMs    int64
	exec    *sql.Tx
	logger  *slog.Logger
}

// unitStateTx carries the queries and the logger of a write of unit_state
// through the helpers of the state write.
type unitStateTx struct {
	q      *db.Queries
	logger *slog.Logger
}

// publishUnitState records the write of w in unit_state. It is the only writer
// of unit_state.
//
// A state write records the version of w as the current state version of its
// unit, and, when w.final is true, as the last final state version of the
// unit, with the origin and the time of w. The end of an update marks the
// current version final, at the clock of the store. A lock release marks the
// current version final, at the clock of the store, and does nothing when it
// is final already. A final version closes the jobs that wait for one, in the
// same transaction.
//
// A restore derive writes the state version of every restored unit with one
// statement for the workspaces and one for the stacks, in the transaction of
// w. A restored version is not final: it predates the delivery of any waiting
// job, so the final fields stay empty and the jobs that wait for a state
// version keep waiting.
//
// An origin that names a job and no commit takes the commit of the spec of
// that job, read in the same transaction. The commit stays empty when the
// store holds no such job: a write outlives the job that made it, for example
// after a purge. It also stays empty when the job row is damaged or its read
// fails: the state write goes on, and the store writes one log record with the
// job and the error.
//
// The stored final fields stay as they are when a state write is not final.
// sqlc rewrites no query parameter in the DO UPDATE SET clause of this
// dialect, so stateWriteParams reads the stored row and gives its final fields
// to the upsert. The write transaction of the store is IMMEDIATE, so no other
// write runs between the read and the upsert.
func publishUnitState(ctx context.Context, q *db.Queries, w unitStateWrite) error {
	if !w.kind.valid() {
		return fmt.Errorf("%w: %d", errUnitStateKind, w.kind)
	}

	resolveOriginCommit(ctx, q, w.logger, &w.origin)

	switch w.kind {
	case unitStateWriteState:
		params, err := stateWriteParams(ctx, q, w)
		if err != nil {
			return err
		}

		if err := q.PublishUnitState(ctx, params); err != nil {
			return fmt.Errorf("publish unit state: %w", err)
		}

		return closePublishedState(ctx, q, w)
	case unitStateWriteEnd:
		row, err := q.FinalizeUnitState(ctx, db.FinalizeUnitStateParams{
			Namespace: w.nsName, UnitID: w.unitID, FinalCommit: w.origin.Commit, FinalJob: w.origin.Job,
		})
		if err != nil {
			return fmt.Errorf("finalize unit state: %w", err)
		}

		return closeAwaitingState(ctx, q, w.nsName, jobstore.FinalVersion{
			UnitID: w.unitID, VersionID: row.VersionID, Commit: w.origin.Commit, Job: job.ID(w.origin.Job),
			At: time.UnixMilli(row.FinalAtMs).UTC(),
		})
	case unitStateWriteRelease:
		row, err := q.FinalizeReleasedUnitState(ctx, db.FinalizeReleasedUnitStateParams{
			Namespace: w.nsName, UnitID: w.unitID, FinalCommit: w.origin.Commit, FinalJob: w.origin.Job,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("finalize released unit state: %w", err)
		}

		return closeAwaitingState(ctx, q, w.nsName, jobstore.FinalVersion{
			UnitID: w.unitID, VersionID: row.VersionID, Commit: w.origin.Commit, Job: job.ID(w.origin.Job),
			At: time.UnixMilli(row.FinalAtMs).UTC(),
		})
	case unitStateWriteRestore:
		return restoreUnitState(ctx, w.exec)
	default:
		return fmt.Errorf("%w: %d", errUnitStateKind, w.kind)
	}
}

// closePublishedState closes the jobs that wait for the version of a state
// write, when the write makes it final.
func closePublishedState(ctx context.Context, q *db.Queries, w unitStateWrite) error {
	if !w.final {
		return nil
	}

	return closeAwaitingState(ctx, q, w.nsName, jobstore.FinalVersion{
		UnitID: w.unitID, VersionID: w.version, Commit: w.origin.Commit, Job: job.ID(w.origin.Job),
		At: time.UnixMilli(w.atMs).UTC(),
	})
}

// stateWriteParams returns the parameters of the upsert of a state write. The
// stored final fields stay as they are when w.final is false: this dialect
// reads the stored row and gives its final fields to the upsert.
func stateWriteParams(
	ctx context.Context, q *db.Queries, w unitStateWrite,
) (db.PublishUnitStateParams, error) {
	finalVersionID, finalCommit, finalJob, finalAtMs := "", "", "", int64(0)

	if w.final {
		finalVersionID, finalCommit, finalJob, finalAtMs = w.version, w.origin.Commit, w.origin.Job, w.atMs
	} else {
		stored, ok, err := unitState(ctx, q, w.nsName, w.unitID)
		if err != nil {
			return db.PublishUnitStateParams{}, err
		}

		if ok {
			finalVersionID, finalCommit, finalJob, finalAtMs = stored.FinalVersionID, stored.FinalCommit,
				stored.FinalJob, stored.FinalAtMs
		}
	}

	return db.PublishUnitStateParams{
		UnitID:         w.unitID,
		Namespace:      w.nsName,
		VersionID:      w.version,
		FinalVersionID: finalVersionID,
		FinalCommit:    finalCommit,
		FinalJob:       finalJob,
		FinalAtMs:      finalAtMs,
	}, nil
}

// jobSpecCommit returns the commit that the spec of the job jobID names, or
// the empty commit when the store holds no such job, its row is damaged or its
// read fails. A damaged row never fails a state write: the store writes one
// log record with the job and the error, and the write goes on.
func jobSpecCommit(ctx context.Context, q *db.Queries, logger *slog.Logger, jobID string) string {
	row, err := q.GetJob(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}

	if err != nil {
		logUnreadableJob(ctx, logger, jobID, err)

		return ""
	}

	damaged := jobRow(row)

	j, err := jobstore.Decode(&damaged)
	if err != nil {
		logUnreadableJob(ctx, logger, jobID, err)

		return ""
	}

	return j.Spec.Location.Revision
}

// logUnreadableJob writes one record about a job row that a state write could
// not read. A write without a logger stays silent.
func logUnreadableJob(ctx context.Context, logger *slog.Logger, jobID string, err error) {
	if logger == nil {
		return
	}

	logger.WarnContext(ctx, "the job of a state write is unreadable: the commit stays empty",
		slog.String("job", jobID), slog.String("error", err.Error()))
}

// resolveOriginCommit fills the commit of the spec of the job that the origin
// names, when the origin has no commit.
func resolveOriginCommit(ctx context.Context, q *db.Queries, logger *slog.Logger, origin *identity.StateOrigin) {
	if origin.Job == "" || origin.Commit != "" {
		return
	}

	origin.Commit = jobSpecCommit(ctx, q, logger, origin.Job)
}

// restoreUnitState writes the state version of every unit of the staged file
// in unit_state, with one statement for the workspaces and one for the stacks.
// It is part of the only writer of unit_state: only publishUnitState calls it,
// because a portable restore derives the versions of all restored units at
// once and one call per unit would not scale. A restored version is not final:
// it predates the delivery of any waiting job, so the final fields are empty.
func restoreUnitState(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("%w: restore without a transaction", errUnitStateKind)
	}

	const (
		columns = "unit_id, namespace_id, version_id, final_version_id, final_commit, final_job, final_at_ms"
		update  = " ON CONFLICT (unit_id) DO UPDATE SET namespace_id = excluded.namespace_id," +
			" version_id = excluded.version_id, final_version_id = excluded.final_version_id," +
			" final_commit = excluded.final_commit, final_job = excluded.final_job," +
			" final_at_ms = excluded.final_at_ms"
	)

	workspaceVersion := "w.current_state_version_id"

	// The WHERE true keeps the parser from reading ON CONFLICT as the ON
	// clause of the join.
	workspaces := "INSERT INTO main.unit_state (" + columns + ")" +
		" SELECT w.id, n.id, " + workspaceVersion + ", '', '', '', 0" +
		" FROM main.workspaces AS w" +
		" JOIN main.namespaces AS n ON n.name = w.namespace" +
		" WHERE true" + update

	version := "lower(hex(c.digest)) || CASE WHEN c.invalid <> 0 THEN ':invalid' ELSE '' END"

	stacks := "INSERT INTO main.unit_state (" + columns + ")" +
		" SELECT c.stack_id, n.id, " + version + ", '', '', '', 0" +
		" FROM main.checkpoints AS c" +
		" JOIN main.updates AS u ON u.stack_id = c.stack_id AND u.id = c.update_id" +
		" JOIN main.stacks AS st ON st.id = c.stack_id" +
		" JOIN main.namespaces AS n ON n.name = st.namespace" +
		" WHERE u.version = (SELECT MAX(v.version) FROM main.updates AS v" +
		" JOIN main.checkpoints AS last ON last.stack_id = v.stack_id AND last.update_id = v.id" +
		" WHERE v.stack_id = c.stack_id)" + update

	for _, stmt := range []string{workspaces, stacks} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("derive unit state: %w", err)
		}
	}

	return nil
}

// closeAwaitingState closes the jobs of the unit of the namespace nsName that
// wait for the final state version v.
func closeAwaitingState(ctx context.Context, q *db.Queries, nsName string, v jobstore.FinalVersion) error {
	_, err := jobstore.CloseAwaiting(ctx, stateCloseTx{q: q}, namespace.Name(nsName), v, nil)
	if err != nil {
		return fmt.Errorf("close the jobs that wait for the state version: %w", err)
	}

	return nil
}

// unitState returns the stored row of the unit, and false when it has none.
func unitState(ctx context.Context, q *db.Queries, nsName, unitID string) (db.GetUnitStateRow, bool, error) {
	row, err := q.GetUnitState(ctx, db.GetUnitStateParams{Namespace: nsName, UnitID: unitID})
	if errors.Is(err, sql.ErrNoRows) {
		return zeroUnitState(), false, nil
	}

	if err != nil {
		return zeroUnitState(), false, fmt.Errorf("read unit state: %w", err)
	}

	return row, true, nil
}

// zeroUnitState returns the row that a lookup answers when it finds none.
func zeroUnitState() db.GetUnitStateRow {
	return db.GetUnitStateRow{VersionID: "", FinalVersionID: "", FinalCommit: "", FinalJob: "", FinalAtMs: 0}
}

// checkpointDigest is the digest of a checkpoint and whether its stored row
// marks it invalid.
type checkpointDigest struct {
	digest  []byte
	invalid bool
}

// invalidStateVersionSuffix marks the state version of an invalid checkpoint.
// A digest holds hexadecimal digits only, so a version with the suffix never
// equals the version of a valid write.
const invalidStateVersionSuffix = ":invalid"

// stateVersion returns the state version of the checkpoint: the digest of its
// deployment, with invalidStateVersionSuffix when the row marks the checkpoint
// invalid. A checkpoint that a write of an update replaced carries another
// digest, so a plan of the replaced state is stale; a write of the same digest
// keeps the version.
func (d checkpointDigest) stateVersion() string {
	suffix := ""
	if d.invalid {
		suffix = invalidStateVersionSuffix
	}

	return hex.EncodeToString(d.digest) + suffix
}
