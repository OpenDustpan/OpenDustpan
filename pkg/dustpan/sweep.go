package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// deletedSweepEvery is the period of the retention sweep (ADR 47). The
// retention period counts at least one hour, so one run each hour is ample.
const deletedSweepEvery = time.Hour

// deletedSweepTimeout bounds one scheduled retention sweep. A sweep that
// stops at the bound leaves the rest of its work to the next run.
const deletedSweepTimeout = 30 * time.Minute

// deletedSweeper runs the retention sweep and counts each outcome on the
// server metrics, for the schedule and for the installation API alike.
type deletedSweeper struct {
	sweeper  *inventory.Sweeper
	observed *metrics.ServerMetrics
	logger   *slog.Logger
}

// newDeletedSweeper builds the retention sweep over the store of the
// deleted units. Every SQL adapter keeps the expired units and the blob
// purge records. A custom inventory reader that keeps neither fails here,
// so that no server keeps deleted units that it never purges.
func newDeletedSweeper(
	svc services, cfg *settings, observed *metrics.ServerMetrics,
) (*deletedSweeper, error) {
	expired, ok := cfg.inventoryReader.(inventory.ExpiredDeleted)
	if !ok {
		return nil, fmt.Errorf("inventory reader lists no expired deleted units: %w", ErrMissingOption)
	}

	records, ok := cfg.inventoryReader.(inventory.BlobPurges)
	if !ok {
		return nil, fmt.Errorf("inventory reader keeps no blob purge records: %w", ErrMissingOption)
	}

	stacks, workspaces := unitLifecycles(svc, cfg)

	sweeper, err := inventory.NewSweeper(inventory.SweeperConfig{
		Expired:    expired,
		BlobPurges: records,
		Lifecycles: map[string]inventory.Lifecycle{pulumiTool: stacks, opentofuTool: workspaces},
		Blobs:      map[string]inventory.PurgedBlobs{pulumiTool: stacks, opentofuTool: workspaces},
		Logger:     cfg.logger,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("retention sweep: %w", err)
	}

	return &deletedSweeper{sweeper: sweeper, observed: observed, logger: cfg.logger}, nil
}

// Sweep runs one retention sweep with the audit entry e and counts its
// outcome. A sweep that cannot read the store counts as one failure and logs
// a warning with the error kind, never the error text. A sweep that the
// server stops is not a failure.
func (d *deletedSweeper) Sweep(ctx context.Context, e audit.Entry) (inventory.SweepResult, error) {
	res, err := d.sweeper.Sweep(ctx, e)

	d.observed.ObserveDeletedSweep(metrics.DeletedSweepPurged, len(res.Purged))
	d.observed.ObserveDeletedSweep(metrics.DeletedSweepSkipped, res.Skipped)
	d.observed.ObserveDeletedSweep(metrics.DeletedSweepFinished, res.FinishedBlobPurges)
	d.observed.ObserveDeletedSweep(metrics.DeletedSweepFailed, res.Failed)

	if err != nil && !errors.Is(err, context.Canceled) {
		d.observed.ObserveDeletedSweep(metrics.DeletedSweepFailed, 1)
		d.logger.WarnContext(ctx, "retention sweep could not read the store",
			slog.String("error_kind", inventory.ErrorKind(err)))
	}

	if err != nil {
		return res, fmt.Errorf("retention sweep: %w", err)
	}

	return res, nil
}

// scheduledSweepEntry is the audit entry of each purge of a scheduled
// retention sweep: the server subject and the channel of the scheduled
// inactivity disable (accesstoken.SweepEntry), with the action unit:purge.
func scheduledSweepEntry() audit.Entry {
	e := accesstoken.SweepEntry()
	e.Action = string(identity.ActionUnitPurge)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	return e
}

// sweepDeleted runs the retention sweep once at the start and then once
// each deletedSweepEvery, until ctx is done.
func (s *Server) sweepDeleted(ctx context.Context) {
	if s.deleted == nil {
		return
	}

	s.sweepDeletedOnce(ctx)

	ticker := time.NewTicker(deletedSweepEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepDeletedOnce(ctx)
		}
	}
}

// sweepDeletedOnce runs one scheduled retention sweep within
// deletedSweepTimeout and logs what it purged. The sweeper counts and logs
// each failure.
func (s *Server) sweepDeletedOnce(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, deletedSweepTimeout)
	defer cancel()

	res, err := s.deleted.Sweep(bounded, scheduledSweepEntry())
	if err != nil {
		return
	}

	if len(res.Purged) > 0 || res.FinishedBlobPurges > 0 {
		s.logger.InfoContext(ctx, "retention sweep",
			slog.Int("purged", len(res.Purged)), slog.Int("finished_blob_purges", res.FinishedBlobPurges))
	}
}

// DeletePurgedBlobs implements inventory.PurgedBlobs for Pulumi stacks,
// within purgeBlobTimeout.
func (l pulumiLifecycle) DeletePurgedBlobs(ctx context.Context, unitID string) error {
	bounded, cancel := context.WithTimeout(ctx, purgeBlobTimeout)
	defer cancel()

	if err := l.updates.DeleteStackBlobs(bounded, stack.ID(unitID)); err != nil {
		return fmt.Errorf("delete blobs of stack %s: %w", unitID, err)
	}

	return nil
}

// DeletePurgedBlobs implements inventory.PurgedBlobs for OpenTofu
// workspaces, within purgeBlobTimeout.
func (l opentofuLifecycle) DeletePurgedBlobs(ctx context.Context, unitID string) error {
	bounded, cancel := context.WithTimeout(ctx, purgeBlobTimeout)
	defer cancel()

	if err := l.workspaces.DeletePurgedBlobs(bounded, workspace.ID(unitID)); err != nil {
		return fmt.Errorf("delete blobs of workspace %s: %w", unitID, err)
	}

	return nil
}
