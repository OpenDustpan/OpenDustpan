package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The schedule of the expiry of the leases of apply claims.
const (
	// claimSweepScheduleName is the name and the kind of the schedule.
	claimSweepScheduleName = "claim-expire"
	// claimSweepEvery is the period of the sweep. The renewal of the local
	// wrapper comes every 30 seconds, and the lease lasts 90, so the sweep
	// ends an abandoned claim within one period after its lease.
	claimSweepEvery = 30 * time.Second
	// claimSweepMaxPerRun bounds the claims that one run ends, over all
	// namespaces. The next run ends the rest.
	claimSweepMaxPerRun = 500
)

const kindClaimSweep = claimSweepScheduleName

// claimTiming holds the lease and the sweep period that a test sets. The zero
// value means the constants of the code: ClaimLease and claimSweepEvery.
type claimTiming struct {
	lease, every time.Duration
}

// sweepEvery returns the period of the sweep.
func (t claimTiming) sweepEvery() time.Duration {
	if t.every > 0 {
		return t.every
	}

	return claimSweepEvery
}

// WithClaimTimingForTests sets the lease of a claim of the local wrapper and
// the period of the expiry sweep, for tests. The values are constants of the
// code otherwise (90 seconds and 30 seconds), and no setting of a user
// reaches them.
func WithClaimTimingForTests(lease, sweepEvery time.Duration) Option {
	return func(o *settings) { o.claimTiming = claimTiming{lease: lease, every: sweepEvery} }
}

// claimNamespaces lists the namespaces that the sweep walks.
type claimNamespaces interface {
	// List returns every namespace.
	List(ctx context.Context) ([]namespace.Namespace, error)
}

// claimExpirer ends the claims of one namespace whose lease ended, at most
// limit of them. The claim registry is one.
type claimExpirer interface {
	ExpireLeases(ctx context.Context, fence namespace.Fence, limit int) (int, error)
}

// claimSweeper ends the apply claims whose lease ended. A claim of a runner
// job has no lease: its job ends it in the store of jobs. The claims with a
// lease are the claims of the local wrapper, which renews them while it runs.
type claimSweeper struct {
	claims     claimExpirer
	namespaces claimNamespaces
	logger     *slog.Logger
	maxPerRun  int
	// every is the period of the sweep.
	every time.Duration
	// cursor counts the runs, so that each run starts at another namespace.
	cursor atomic.Int64
}

// newClaimSweeper returns the sweeper of the server, or nil when the server
// keeps no claims.
func newClaimSweeper(cfg *settings, svc services) (*claimSweeper, error) {
	claims, err := newClaims(cfg, svc)
	if err != nil || claims == nil || svc.namespaces == nil {
		return nil, err
	}

	return &claimSweeper{
		claims: claims, namespaces: svc.namespaces, logger: cfg.logger, maxPerRun: claimSweepMaxPerRun,
		every: cfg.claimTiming.sweepEvery(), cursor: atomic.Int64{},
	}, nil
}

// claimSweepSchedule returns the schedule of the claim expiry, or none when the
// server keeps no claims.
func (s *Server) claimSweepSchedule() []durable.ScheduleSpec {
	if s.claimSweeper == nil {
		return nil
	}

	return []durable.ScheduleSpec{{
		Name: claimSweepScheduleName, Kind: kindClaimSweep, Version: jobScheduleVersion,
		Interval: s.claimSweeper.every, Input: nil,
	}}
}

// sweepOnce ends the claims whose lease ended, in each namespace, until it
// has ended maxPerRun claims. A failure of one namespace is logged without the
// error text, which may hold driver detail, and the sweep goes on; the next run
// tries again.
func (c *claimSweeper) sweepOnce(ctx context.Context) {
	list, err := c.namespaces.List(ctx)
	if err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) {
			c.logger.WarnContext(ctx, "claim expiry could not list the namespaces")
		}

		return
	}

	if len(list) == 0 {
		return
	}

	budget, ended := c.maxPerRun, 0

	// Each run starts at the next namespace of the list, so that a namespace
	// with many claims cannot use the whole budget of every run and starve the
	// namespaces after it.
	start := int((c.cursor.Add(1) - 1) % int64(len(list)))

	for k := range list {
		i := (start + k) % len(list)

		n, expErr := c.claims.ExpireLeases(ctx, list[i].Fence(), budget)
		if expErr != nil && !errors.Is(ctx.Err(), context.Canceled) {
			c.logger.WarnContext(
				ctx,
				"claim expiry failed in a namespace",
				slog.String("namespace", list[i].Name.String()),
			)
		}

		budget -= n
		ended += n
	}

	if ended > 0 {
		c.logger.InfoContext(ctx, "ended the claims whose lease ended", slog.Int("claims", ended))
	}
}

// sweepClaims runs the sweep once each claimSweepEvery, until ctx is done.
func (s *Server) sweepClaims(ctx context.Context) {
	if s.claimSweeper == nil {
		return
	}

	ticker := time.NewTicker(s.claimSweeper.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.claimSweeper.sweepOnce(ctx)
		}
	}
}
