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

// deletedSweepEvery is the period of the retention sweep. The
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
		Tools:      map[string]inventory.SweepTool{pulumiTool: stacks, opentofuTool: workspaces},
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

	if errors.Is(err, inventory.ErrSweepRunning) {
		return res, fmt.Errorf("retention sweep: %w", err)
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		d.observed.ObserveDeletedSweep(metrics.DeletedSweepFailed, 1)
		d.logger.WarnContext(ctx, "retention sweep could not read the store",
			slog.String("error_kind", inventory.ErrorKind(err)))
	}

	d.logPurged(ctx, res)

	if err != nil {
		return res, fmt.Errorf("retention sweep: %w", err)
	}

	return res, nil
}

// logPurged writes one INFO line when the sweep purged units: the count and
// the oldest purge time among them. The first sweep after an upgrade can
// purge many units whose retention ended long ago, and this line shows it.
func (d *deletedSweeper) logPurged(ctx context.Context, res inventory.SweepResult) {
	if len(res.Purged) == 0 {
		return
	}

	oldest := res.Purged[0].PurgeAfter
	for _, u := range res.Purged[1:] {
		if u.PurgeAfter.Before(oldest) {
			oldest = u.PurgeAfter
		}
	}

	d.logger.InfoContext(ctx, "retention sweep purged deleted units",
		slog.Int("purged", len(res.Purged)), slog.Time("oldest_purge_after", oldest.UTC()))
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

// sweepDeleted runs the retention sweep once each deletedEvery, until ctx
// is done. It does not purge at the start: after an upgrade or an
// installation restore, an operator gets one period to see and restore the
// units that are due. At the start it logs their count.
func (s *Server) sweepDeleted(ctx context.Context) {
	if s.deleted == nil {
		return
	}

	s.deleted.logDue(ctx)

	every := s.deletedEvery
	if every <= 0 {
		every = deletedSweepEvery
	}

	ticker := time.NewTicker(every)
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
// deletedSweepTimeout. The sweeper logs the purges, and counts and logs
// each failure. While a change of the chain waits, it skips one run.
func (s *Server) sweepDeletedOnce(ctx context.Context) {
	if s.chainChangeWaits(ctx) {
		return
	}

	bounded, cancel := context.WithTimeout(ctx, deletedSweepTimeout)
	defer cancel()

	//nolint:errcheck // The sweeper counts and logs a failed sweep, and the next tick tries again.
	_, _ = s.deleted.Sweep(bounded, scheduledSweepEntry())
}

// logDue logs one INFO line at the start of the server with the count of
// deleted units whose retention period has ended: the first sweep, one
// period later, purges them unless a restore takes them first.
func (d *deletedSweeper) logDue(ctx context.Context) {
	due, err := d.sweeper.CountDue(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			d.logger.WarnContext(ctx, "retention sweep could not count the units due",
				slog.String("error_kind", inventory.ErrorKind(err)))
		}

		return
	}

	d.logger.InfoContext(ctx, "retention sweep starts after one period",
		slog.Int("units_due", due), slog.Duration("first_sweep_in", deletedSweepEvery))
}

// DeletePurgedBlobs implements inventory.SweepTool for Pulumi stacks,
// within purgeBlobTimeout.
func (l pulumiLifecycle) DeletePurgedBlobs(ctx context.Context, unitID string) error {
	bounded, cancel := context.WithTimeout(ctx, purgeBlobTimeout)
	defer cancel()

	if err := l.updates.DeleteStackBlobs(bounded, stack.ID(unitID)); err != nil {
		return fmt.Errorf("delete blobs of stack %s: %w", unitID, err)
	}

	return nil
}

// DeletePurgedBlobs implements inventory.SweepTool for OpenTofu
// workspaces, within purgeBlobTimeout.
func (l opentofuLifecycle) DeletePurgedBlobs(ctx context.Context, unitID string) error {
	bounded, cancel := context.WithTimeout(ctx, purgeBlobTimeout)
	defer cancel()

	if err := l.workspaces.DeletePurgedBlobs(bounded, workspace.ID(unitID)); err != nil {
		return fmt.Errorf("delete blobs of workspace %s: %w", unitID, err)
	}

	return nil
}
