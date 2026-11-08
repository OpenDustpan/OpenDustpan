package stack

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// The name, the kind, the version and the interval of the schedule of the
// sweep of superseded checkpoint blobs. The name and the kind match, as for
// the other schedules of the server.
const (
	// SupersededSweepName is the name of the schedule of the sweep.
	SupersededSweepName = "stack.superseded-checkpoint-sweep"
	// KindSupersededSweep is the durable kind of the operation that one tick
	// of the schedule starts.
	KindSupersededSweep durable.Kind = SupersededSweepName
	// SupersededSweepVersion is the version of KindSupersededSweep.
	SupersededSweepVersion = 1
	// SupersededSweepInterval is the time between two ticks of the schedule.
	SupersededSweepInterval = 15 * time.Minute
)

// supersededSweepPage is the number of marks that one page of the sweep reads.
const supersededSweepPage = 100

// SupersededSweepResult is what one run of the sweep of superseded checkpoint
// blobs did. The durable step of the schedule stores it, in JSON, as its
// output.
type SupersededSweepResult struct {
	// Deleted is the number of marked blobs that the run deleted, including
	// the blobs that the blob store did not hold.
	Deleted int `json:"deleted"`
	// Kept is the number of marked blobs that the run kept: the mark was
	// missing, because another sweep dropped it and owns the delete, or a
	// checkpoint row or a journal batch row of the stack names the key again.
	Kept int `json:"kept"`
	// Failed is the number of marked blobs whose delete failed for another
	// reason. Their marks are gone, so the sweep of unnamed blobs deletes
	// them later.
	Failed int `json:"failed"`
}

// SweepSuperseded deletes the checkpoint blobs that a later save of the same
// update replaced, after the grace period of a mark. It takes now from the
// clock of the database. A clock that it cannot trust deletes nothing and
// returns the error.
//
// The sweep reads the marks page by page and holds one blob delete lease for
// each page. A running backup or restore returns an error that matches
// blobguard.ErrWait, and then the run deletes nothing and drops no mark. For
// each mark it drops the mark and deletes the blob only when the drop removed
// the mark and no checkpoint row and no journal batch row of the stack names
// the key. A mark that another sweep dropped first keeps the blob, because
// that sweep owns the delete. A blob that the blob store does not hold counts
// as deleted. A delete that fails for another reason counts as failed, and its
// mark is gone, so the sweep of unnamed blobs deletes the blob later. A lease
// that ends during a page stops the page before the next drop, so the marks
// that are left stay for the next run. No log line and no error names a blob
// key.
func (s *Updates) SweepSuperseded(ctx context.Context) (SupersededSweepResult, error) {
	var res SupersededSweepResult

	now, err := s.sweepNow(ctx)
	if err != nil {
		return res, fmt.Errorf("sweep superseded checkpoint blobs: %w", err)
	}

	olderThan := now.Add(-blob.UnnamedGracePeriod)

	var after SupersededCursor

	for {
		marks, err := s.checkpoints.SupersededMarks(ctx, after, olderThan, supersededSweepPage)
		if err != nil {
			return res, fmt.Errorf("sweep superseded checkpoint blobs: %w", err)
		}

		if len(marks) == 0 {
			break
		}

		if err := s.sweepSupersededPage(ctx, marks, &res); err != nil {
			return res, err
		}

		if len(marks) < supersededSweepPage {
			break
		}

		last := marks[len(marks)-1]
		after = SupersededCursor{StackID: last.StackID, Key: last.Key}
	}

	if res.Failed > 0 {
		s.logger.WarnContext(ctx, "sweep superseded checkpoint blobs",
			slog.Int("deleted", res.Deleted), slog.Int("kept", res.Kept), slog.Int("failed", res.Failed))
	}

	return res, nil
}

// sweepSupersededPage drops the marks of one page and deletes the blobs that
// no row names, under one blob delete lease for the page. A lease that ends
// during the page stops it before the next drop.
func (s *Updates) sweepSupersededPage(
	ctx context.Context, marks []SupersededMark, res *SupersededSweepResult,
) error {
	leaseCtx, release, err := blobguard.HoldLease(ctx, s.guard, ident.NewID("dlt-"), s.now)
	if err != nil {
		return fmt.Errorf("sweep superseded checkpoint blobs: %w", err)
	}

	var pageErr error

	views := make(map[ID]*scopedBlobs, 1)

	for _, mark := range marks {
		// The lease context ends when the lease is lost, for example when its
		// window closes. A delete must not run without a live lease, and a
		// dropped mark would leave its blob to no owner, so stop before the
		// next drop. The marks that are left stay for the next run, which
		// takes a new lease.
		keep, err := s.dropSupersededMark(leaseCtx, ctx, mark)
		if err != nil {
			pageErr = err

			break
		}

		if keep {
			res.Kept++

			continue
		}

		bl, ok := views[mark.StackID]
		if !ok {
			view, scopeErr := s.blobs.scope(mark.NamespaceID, mark.StackID)
			if scopeErr != nil {
				res.Failed++

				continue
			}

			bl = view
			views[mark.StackID] = bl
		}

		if err := s.deleteBlobRetried(leaseCtx, bl, mark.Key); err != nil {
			res.Failed++

			continue
		}

		res.Deleted++
	}

	if releaseErr := release(ctx); releaseErr != nil {
		s.logger.WarnContext(ctx, "blob delete lease not released",
			slog.String("error", releaseErr.Error()))
	}

	return pageErr
}

// dropSupersededMark drops one mark of a page. It refuses the drop when the
// lease of the page ended, so the marks that are left keep their blobs and
// stay for the next run.
func (s *Updates) dropSupersededMark(
	leaseCtx, ctx context.Context, mark SupersededMark,
) (bool, error) {
	if err := leaseCtx.Err(); err != nil {
		return false, fmt.Errorf("sweep superseded checkpoint blobs: %w", err)
	}

	keep, err := s.checkpoints.DropSupersededMark(ctx, mark.StackID, mark.Key)
	if err != nil {
		return false, fmt.Errorf("sweep superseded checkpoint blobs: %w", err)
	}

	return keep, nil
}
