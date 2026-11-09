package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// unitStateWrite is the state version that one write of a unit publishes: the
// unit, the version that the write makes current, whether the write also
// makes it the last final version of the unit, the origin of the write and
// its time on the clock of the store.
type unitStateWrite struct {
	nsName  string
	unitID  string
	version string
	final   bool
	origin  identity.StateOrigin
	atMs    int64
}

// publishUnitState records the version of w as the current state version of
// its unit, and, when w.final is true, as the last final state version of the
// unit, with the origin and the time of w. It is the only writer of
// unit_state: every point that moves the state of a unit calls it in the
// transaction of the write, so the version and the state commit together. A
// final version closes the jobs that wait for one, in the same transaction.
//
// An origin that names a job and no commit takes the commit of the spec of
// that job, read in the same transaction. The commit stays empty when the
// store holds no such job: a write outlives the job that made it, for example
// after a purge.
//
// The stored final fields stay as they are when w.final is false. sqlc
// rewrites no query parameter in the DO UPDATE SET clause of this dialect, so
// this function reads the stored row and gives its final fields to the
// upsert. The write transaction of the store is IMMEDIATE, so no other write
// runs between the read and the upsert.
func publishUnitState(ctx context.Context, q *db.Queries, w unitStateWrite) error {
	if w.origin.Job != "" && w.origin.Commit == "" {
		commit, err := jobSpecCommit(ctx, q, w.origin.Job)
		if err != nil {
			return err
		}

		w.origin.Commit = commit
	}

	finalVersionID, finalCommit, finalJob, finalAtMs := "", "", "", int64(0)

	if w.final {
		finalVersionID, finalCommit, finalJob, finalAtMs = w.version, w.origin.Commit, w.origin.Job, w.atMs
	} else {
		stored, ok, err := unitState(ctx, q, w.nsName, w.unitID)
		if err != nil {
			return err
		}

		if ok {
			finalVersionID, finalCommit, finalJob, finalAtMs = stored.FinalVersionID, stored.FinalCommit,
				stored.FinalJob, stored.FinalAtMs
		}
	}

	err := q.PublishUnitState(ctx, db.PublishUnitStateParams{
		UnitID:         w.unitID,
		Namespace:      w.nsName,
		VersionID:      w.version,
		FinalVersionID: finalVersionID,
		FinalCommit:    finalCommit,
		FinalJob:       finalJob,
		FinalAtMs:      finalAtMs,
	})
	if err != nil {
		return fmt.Errorf("publish unit state: %w", err)
	}

	if !w.final {
		return nil
	}

	_, err = jobstore.CloseAwaiting(ctx, stateCloseTx{q: q}, namespace.Name(w.nsName), jobstore.FinalVersion{
		UnitID: w.unitID, VersionID: w.version, Commit: w.origin.Commit, Job: job.ID(w.origin.Job),
		At: time.UnixMilli(w.atMs).UTC(),
	}, nil)
	if err != nil {
		return fmt.Errorf("close the jobs that wait for the state version: %w", err)
	}

	return nil
}

// jobSpecCommit returns the commit that the spec of the job jobID names, or
// the empty commit when the store holds no such job. A job row that does not
// decode is an error: the store refuses a damaged row everywhere, so it never
// guesses the commit of one.
func jobSpecCommit(ctx context.Context, q *db.Queries, jobID string) (string, error) {
	row, err := q.GetJob(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
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

// publishFinalState records the current state version of the unit as final,
// with the origin of the write that ends it. A unit with no state version row
// records the empty version: it holds no state, and the end of its update is
// still final.
func publishFinalState(
	ctx context.Context, q *db.Queries, nsName, unitID string, origin identity.StateOrigin,
) error {
	stored, ok, err := unitState(ctx, q, nsName, unitID)
	if err != nil {
		return err
	}

	version := ""
	if ok {
		version = stored.VersionID
	}

	return publishFinalStateAt(ctx, q, unitStateWrite{
		nsName: nsName, unitID: unitID, version: version, final: true, origin: origin, atMs: 0,
	})
}

// publishReleasedState records the current state version of the unit as final,
// with the origin of the releasing request, and does nothing when it is final
// already: the release of a lock marks the state final for the write-back and
// the triggers, and a release with no state write since the last final version
// makes no new final version. A unit with no state version row holds no
// version that a release can make final.
func publishReleasedState(
	ctx context.Context, q *db.Queries, nsName, unitID string, origin identity.StateOrigin,
) error {
	stored, ok, err := unitState(ctx, q, nsName, unitID)
	if err != nil {
		return err
	}

	if !ok || stored.VersionID == stored.FinalVersionID {
		return nil
	}

	return publishFinalStateAt(ctx, q, unitStateWrite{
		nsName: nsName, unitID: unitID, version: stored.VersionID, final: true, origin: origin, atMs: 0,
	})
}

// publishFinalStateAt records w.version as the last final state version of the
// unit, at the clock of the store.
func publishFinalStateAt(ctx context.Context, q *db.Queries, w unitStateWrite) error {
	atMs, err := storeNow(ctx, q)
	if err != nil {
		return err
	}

	w.final = true
	w.atMs = atMs

	return publishUnitState(ctx, q, w)
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
