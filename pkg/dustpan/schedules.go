package dustpan

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/instance"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// DurableStore keeps the durable operations, the job schedules and the
// heartbeats of the workers. Both SQL adapters serve it from the same
// database connection as the other ports.
type DurableStore interface {
	durable.Store
	durable.ScheduleStore
	durable.HeartbeatStore
}

// WithDurableStore sets the store of the durable job schedules. With it the
// server runs each job on a schedule, so one instance runs each job per tick.
// Without it the server runs each job on a ticker on every instance.
func WithDurableStore(s DurableStore) Option {
	return func(o *settings) { o.durableStore = s }
}

// The names, kinds and version of the job schedules. The name and the kind
// of each job match, so the operation key names its schedule and its tick.
const (
	inactivityScheduleName  = "inactivity-disable"
	deletedScheduleName     = "deleted-sweep"
	signInPruneScheduleName = "signin-prune"
	idempotencyScheduleName = "idempotency-sweep"
	auditChainScheduleName  = "audit-chain-check"

	jobScheduleVersion = 1
)

// jobStepName is the only step of each job schedule. Each step runs its job
// body once and records no result.
const jobStepName = "run"

const (
	kindInactivity  durable.Kind = inactivityScheduleName
	kindDeleted     durable.Kind = deletedScheduleName
	kindSignInPrune durable.Kind = signInPruneScheduleName
	kindIdempotency durable.Kind = idempotencyScheduleName
	kindAuditChain  durable.Kind = auditChainScheduleName
)

// durableOwner returns the owner name of the job runner: the instance ID
// when set, else a new ID, so each process still holds its own claims.
func durableOwner(cfg *settings) string {
	if cfg.instanceID != "" {
		return cfg.instanceID
	}

	return instance.NewID()
}

// jobSchedules returns the schedule of each job that s runs, with its
// interval. A job without its service is left out.
func (s *Server) jobSchedules() []durable.ScheduleSpec {
	var specs []durable.ScheduleSpec

	if s.tokens != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: inactivityScheduleName, Kind: kindInactivity, Version: jobScheduleVersion,
			Interval: s.inactivityEvery, Input: nil,
		})
	}

	if s.deleted != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: deletedScheduleName, Kind: kindDeleted, Version: jobScheduleVersion,
			Interval: s.deletedEvery, Input: nil,
		})
	}

	if s.signInRequests != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: signInPruneScheduleName, Kind: kindSignInPrune, Version: jobScheduleVersion,
			Interval: s.signInPruneEvery, Input: nil,
		})
	}

	if s.idempotency != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: idempotencyScheduleName, Kind: kindIdempotency, Version: jobScheduleVersion,
			Interval: s.idempotencyEvery, Input: nil,
		})
	}

	if s.chain != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: auditChainScheduleName, Kind: kindAuditChain, Version: jobScheduleVersion,
			Interval: s.auditChainPeriod(), Input: nil,
		})
	}

	return specs
}

// jobKinds returns the runner kinds of the jobs that s runs. Each step runs
// its job body once and records no result.
func (s *Server) jobKinds() []durable.KindSpec {
	var kinds []durable.KindSpec

	if s.tokens != nil {
		kinds = append(kinds, jobKind(kindInactivity, s.sweepOnce))
	}

	if s.deleted != nil {
		kinds = append(kinds, jobKind(kindDeleted, s.sweepDeletedOnce))
	}

	if s.signInRequests != nil {
		kinds = append(kinds, jobKind(kindSignInPrune, s.pruneSignInRequestsOnce))
	}

	if s.idempotency != nil {
		kinds = append(kinds, jobKind(kindIdempotency, s.sweepIdempotencyOnce))
	}

	if s.chain != nil {
		kinds = append(kinds, jobKind(kindAuditChain, s.chain.check))
	}

	return kinds
}

// jobKind returns the runner kind of a job with one step that runs body.
func jobKind(kind durable.Kind, body func(context.Context)) durable.KindSpec {
	return durable.KindSpec{
		Kind:       kind,
		Version:    jobScheduleVersion,
		MinVersion: jobScheduleVersion,
		Steps: []durable.Step{
			{Name: jobStepName, Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
				body(ctx)

				return nil, nil
			}},
		},
	}
}

// jobPollShare is the part of the shortest schedule interval between two
// empty claims of the job runner: five polls fit in one tick.
const jobPollShare = 5

// jobPollEvery is how often the job runner asks for work after an empty
// claim when a schedule ticks faster than the default poll: a fifth of the
// shortest schedule interval, capped at the default poll. A fast test with
// short intervals claims each tick, while an hourly production schedule
// still polls at the default.
func jobPollEvery(specs []durable.ScheduleSpec) time.Duration {
	shortest := time.Duration(0)

	for _, spec := range specs {
		if shortest == 0 || spec.Interval < shortest {
			shortest = spec.Interval
		}
	}

	if shortest <= 0 {
		return 0
	}

	if poll := shortest / jobPollShare; poll < durable.DefaultPollEvery {
		return max(poll, time.Millisecond)
	}

	return 0
}

// newJobRunner builds the scheduler and the runner of the job schedules of
// s. It returns nil, nil when s runs no job. The runner runs one operation
// at a time, so the jobs never overlap on one instance.
func (s *Server) newJobRunner() (*durable.Scheduler, *durable.Runner, error) {
	specs := s.jobSchedules()
	if len(specs) == 0 {
		return nil, nil, nil
	}

	scheduler, err := durable.NewScheduler(durable.SchedulerConfig{
		Store:         s.tracked(),
		Schedules:     specs,
		MaxFirstDelay: 0,
		ErrorBackoff:  durable.Backoff{Base: 0, Max: 0},
		Logger:        s.logger,
		Rand:          nil,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("job scheduler: %w", err)
	}

	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store:        s.tracked(),
		Owner:        s.durableOwner,
		Kinds:        s.jobKinds(),
		Lease:        s.leaseSetting(),
		RenewEvery:   0,
		PollEvery:    jobPollEvery(specs),
		MaxAttempts:  0,
		ExpiryLimit:  0,
		Backoff:      durable.Backoff{Base: 0, Max: 0},
		ClaimBackoff: durable.Backoff{Base: 0, Max: 0},
		WriteTimeout: 0,
		// Maintenance mode refuses the checked writes of a job. The operation
		// waits, and the wait counts no attempt.
		Deferred: func(err error) bool { return errors.Is(err, maintenance.ErrInMaintenance) },
		Logger:   s.logger,
		Rand:     nil,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("job runner: %w", err)
	}

	return scheduler, runner, nil
}

// leaseSetting returns the configured lease period, or 0 for the default.
func (s *Server) leaseSetting() time.Duration {
	if s.watch == nil {
		return 0
	}

	return s.watch.lease
}

// tracked returns the durable store with the claim recorder around it.
//
//nolint:ireturn // The scheduler and the runner take the port.
func (s *Server) tracked() DurableStore {
	if s.watch == nil {
		return s.durable
	}

	return trackingStore{DurableStore: s.durable, watch: s.watch}
}

// startSweeps starts the background sweeps on ctx: the durable job schedules
// with a durable store, else one ticker per job on this instance. It reports
// the job runner error, if any.
func (s *Server) startSweeps(ctx context.Context, sweeps *sync.WaitGroup) error {
	if s.durable == nil {
		sweeps.Go(func() { s.sweepInactive(ctx) })
		sweeps.Go(func() { s.sweepDeleted(ctx) })
		sweeps.Go(func() { s.pruneSignInRequests(ctx) })
		sweeps.Go(func() { s.sweepIdempotency(ctx) })

		return nil
	}

	return s.startDurableSweeps(ctx, sweeps)
}

// startDurableSweeps logs the deleted units due at the start, as the ticker
// loop did, then starts the scheduler and the runner of the job schedules.
func (s *Server) startDurableSweeps(ctx context.Context, sweeps *sync.WaitGroup) error {
	if s.deleted != nil {
		s.deleted.logDue(ctx)
	}

	scheduler, runner, err := s.newJobRunner()
	if err != nil {
		return err
	}

	if scheduler == nil {
		return nil
	}

	sweeps.Go(func() { scheduler.Run(ctx) })
	sweeps.Go(func() { runner.Run(ctx) })

	return nil
}
