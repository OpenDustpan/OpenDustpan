package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ unitimport.UploadStore = (*Imports)(nil)

// CommitUploadVersion implements unitimport.UploadStore. The fence and the
// state of the import, and the attempt deadline by the clock of the database,
// are in the WHERE clause of the one INSERT, as a second layer next to the
// fence check of the transaction.
func (i *Imports) CommitUploadVersion(
	ctx context.Context, fence namespace.Fence, c unitimport.UploadCommit, e audit.Entry,
) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("upload commit: %w", err)
	}

	return i.store.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		n, err := q.InsertImportUploadCommit(ctx, db.InsertImportUploadCommitParams{
			UnitDir: c.UnitDir, Sequence: int64(c.Sequence), Facts: c.Facts, CommittedAtMs: c.CommittedAt.UnixMilli(),
			Namespace: fence.Namespace.String(), FailoverVersion: fence.FailoverVersion, ImportID: c.ImportID,
			StartedAtMs: c.AttemptStartedAt.UnixMilli(), DeadlineMs: unitimport.UploadAttemptDeadline.Milliseconds(),
		})
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: version %d of unit %q", unitimport.ErrUploadExists, c.Sequence, c.UnitDir)
			}

			return fmt.Errorf("insert upload commit: %w", err)
		}

		if n > 0 {
			return nil
		}

		// No row went in. Find out why, for the error. The transaction has
		// checked the fence already, so the import is missing, it ended, or
		// the attempt passed its deadline by the clock of the database.
		nsID, err := importNamespaceID(ctx, q, fence.Namespace)
		if err != nil {
			return err
		}

		row, err := findImport(ctx, q, nsID, c.ImportID)
		if err != nil {
			return err
		}

		if unitimport.State(row.State).Ended() {
			return fmt.Errorf(
				"%w: import is %q, and an ended import takes no upload",
				unitimport.ErrTransition,
				row.State,
			)
		}

		return fmt.Errorf("%w: the attempt for version %d of unit %q started at %s",
			unitimport.ErrAttemptExpired, c.Sequence, c.UnitDir, c.AttemptStartedAt.UTC().Format(time.RFC3339))
	})
}

// UploadCommits implements unitimport.UploadStore.
func (i *Imports) UploadCommits(
	ctx context.Context, ns namespace.Name, importID, unitDir string,
) ([]unitimport.UploadCommit, error) {
	var rows []db.ImportUploadCommit

	// One snapshot, so the statements of the read see one state.
	err := i.store.inSnapshotTx(ctx, func(q *db.Queries) error {
		nsID, _, readErr := readImport(ctx, q, ns, importID)
		if readErr != nil {
			return readErr
		}

		var listErr error

		rows, listErr = q.ListImportUploadCommits(ctx, db.ListImportUploadCommitsParams{
			NamespaceID: nsID, ImportID: importID, UnitDir: unitDir,
		})
		if listErr != nil {
			return fmt.Errorf("select upload commits: %w", listErr)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]unitimport.UploadCommit, 0, len(rows))
	for n := range rows {
		out = append(out, unitimport.UploadCommit{
			ImportID: rows[n].ImportID, UnitDir: rows[n].UnitDir, Sequence: int(rows[n].Sequence),
			Facts: rows[n].Facts, CommittedAt: time.UnixMilli(rows[n].CommittedAtMs).UTC(),
			AttemptStartedAt: time.Time{},
		})
	}

	return out, nil
}
