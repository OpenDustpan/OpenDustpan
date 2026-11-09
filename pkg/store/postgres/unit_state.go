package postgres

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// unitStateKind is the kind of a write of unit_state: a state write that
// publishes a version, the end of an update that marks the current version
// final, or a lock release that marks the current version final.
type unitStateKind int

const (
	// unitStateWriteState publishes a version as the current state version.
	unitStateWriteState unitStateKind = iota + 1
	// unitStateWriteEnd marks the current version final at the end of an update.
	unitStateWriteEnd
	// unitStateWriteRelease marks the current version final at a lock release.
	unitStateWriteRelease
)

// errUnitStateKind reports a write of a kind that the store does not know.
var errUnitStateKind = errors.New("unit state write kind is unknown")

// valid reports whether k is one of the write kinds.
func (k unitStateKind) valid() bool {
	return k >= unitStateWriteState && k <= unitStateWriteRelease
}

// unitStateWrite is one write of unit_state: its kind, the unit, the version
// that a state write makes current, whether that write also makes it the last
// final version, the origin of the write and its time on the clock of the
// store.
type unitStateWrite struct {
	kind    unitStateKind
	nsName  string
	unitID  string
	version string
	final   bool
	origin  identity.StateOrigin
	atMs    int64
}

// publishUnitState records the write of w in unit_state. It is the only writer
// of unit_state except the restore derive in restore.go, which writes the
// versions of all restored units with one statement.
//
// A state write records the version of w as the current state version of its
// unit, and, when w.final is true, as the last final state version of the
// unit, with the origin and the time of w. The end of an update marks the
// current version final, at the clock of the store. A lock release marks the
// current version final, at the clock of the store, and does nothing when it
// is final already. A final version closes the jobs that wait for one, in the
// same transaction.
//
// An origin that names a job and no commit takes the commit of the spec of
// that job, read in the same transaction. The commit stays empty when the
// store holds no such job: a write outlives the job that made it, for example
// after a purge.
//
// The upsert keeps the stored final fields when a state write is not final,
// and a new row of a write that is not final holds no final version.
func publishUnitState(ctx context.Context, q *db.Queries, w unitStateWrite) error {
	if !w.kind.valid() {
		return fmt.Errorf("%w: %d", errUnitStateKind, w.kind)
	}

	if err := resolveOriginCommit(ctx, q, &w.origin); err != nil {
		return err
	}

	switch w.kind {
	case unitStateWriteState:
		params := stateWriteParams(w)

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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("finalize released unit state: %w", err)
		}

		return closeAwaitingState(ctx, q, w.nsName, jobstore.FinalVersion{
			UnitID: w.unitID, VersionID: row.VersionID, Commit: w.origin.Commit, Job: job.ID(w.origin.Job),
			At: time.UnixMilli(row.FinalAtMs).UTC(),
		})
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
// first write of a unit inserts its row, and the upsert keeps the stored final
// fields only on a conflict. So a write that is not final gives empty final
// fields: the new row then holds no final version.
func stateWriteParams(w unitStateWrite) db.PublishUnitStateParams {
	finalVersionID, finalCommit, finalJob, finalAtMs := "", "", "", int64(0)
	if w.final {
		finalVersionID, finalCommit, finalJob, finalAtMs = w.version, w.origin.Commit, w.origin.Job, w.atMs
	}

	return db.PublishUnitStateParams{
		UnitID:         w.unitID,
		Namespace:      w.nsName,
		VersionID:      w.version,
		FinalVersionID: finalVersionID,
		FinalCommit:    finalCommit,
		FinalJob:       finalJob,
		FinalAtMs:      finalAtMs,
		Final:          w.final,
	}
}

// jobSpecCommit returns the commit that the spec of the job jobID names, or
// the empty commit when the store holds no such job. A job row that does not
// decode is an error: the store refuses a damaged row everywhere, so it never
// guesses the commit of one.
func jobSpecCommit(ctx context.Context, q *db.Queries, jobID string) (string, error) {
	row, err := q.GetJob(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("read the job of a state write: %w", err)
	}

	damaged := jobRow(row)

	j, err := jobstore.Decode(&damaged)
	if err != nil {
		return "", fmt.Errorf("read the commit of job %s: %w", jobID, err)
	}

	return j.Spec.Location.Revision, nil
}

// resolveOriginCommit fills the commit of the spec of the job that the origin
// names, when the origin has no commit.
func resolveOriginCommit(ctx context.Context, q *db.Queries, origin *identity.StateOrigin) error {
	if origin.Job == "" || origin.Commit != "" {
		return nil
	}

	commit, err := jobSpecCommit(ctx, q, origin.Job)
	if err != nil {
		return err
	}

	origin.Commit = commit

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
	if errors.Is(err, pgx.ErrNoRows) {
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
