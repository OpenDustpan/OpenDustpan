package dustpan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/instance"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/stack"
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
	// nameLockPruneScheduleName is the schedule of the prune of the counts
	// of the lockout by name.
	nameLockPruneScheduleName = "name-lock-prune"

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

	kindNameLockPrune durable.Kind = nameLockPruneScheduleName
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

	if s.reconciler != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: job.ReconcileName, Kind: job.KindReconcile, Version: job.ReconcileVersion,
			Interval: job.ReconcileInterval, Input: nil,
		})
	}

	if s.nameLocks.locks != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: nameLockPruneScheduleName, Kind: kindNameLockPrune, Version: jobScheduleVersion,
			Interval: localusersvc.Prune().Interval, Input: nil,
		})
	}

	if s.artifactSweeper != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: job.ArtifactSweepName, Kind: job.KindArtifactSweep, Version: job.ArtifactSweepVersion,
			Interval: job.ArtifactSweepInterval, Input: nil,
		})
	}

	if s.supersededSweeper != nil {
		specs = append(specs, durable.ScheduleSpec{
			Name: stack.SupersededSweepName, Kind: stack.KindSupersededSweep, Version: stack.SupersededSweepVersion,
			Interval: stack.SupersededSweepInterval, Input: nil,
		})
	}

	return append(specs, s.issuerRotateSchedule()...)
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

	if s.reconciler != nil {
		kinds = append(kinds, s.reconcileKind())
	}

	if s.nameLocks.locks != nil {
		kinds = append(kinds, s.nameLockPruneKind())
	}

	if s.artifactSweeper != nil {
		kinds = append(kinds, s.artifactSweepKind())
	}

	if s.supersededSweeper != nil {
		kinds = append(kinds, s.supersededSweepKind())
	}

	return append(kinds, s.issuerRotateKindSpec()...)
}

// reconcileKind returns the runner kind of the reconcile schedule. Its step
// returns the error of a run, so the runner counts a failed run as a failed
// attempt, and it defers a run in maintenance mode: the writes of the reconcile
// wait, as for the other jobs. A job that one run could not change is not an
// error of the run. It is in the log and in the result.
func (s *Server) reconcileKind() durable.KindSpec {
	return durable.KindSpec{
		Kind:       job.KindReconcile,
		Version:    job.ReconcileVersion,
		MinVersion: job.ReconcileVersion,
		Steps: []durable.Step{
			{Name: jobStepName, Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
				_, err := s.reconciler.Reconcile(ctx)
				if err != nil {
					return nil, fmt.Errorf("job reconcile: %w", err)
				}

				return nil, nil
			}},
		},
	}
}

// artifactSweepKind returns the runner kind of the schedule of the artifact
// sweep. Its step returns the error of a run, so the runner counts a failed
// run as a failed attempt. The output of the step is the result of the run in
// JSON: its counts and the keys that it deleted. The sweep also appends one
// audit entry for each run, and the step writes one log line.
func (s *Server) artifactSweepKind() durable.KindSpec {
	return durable.KindSpec{
		Kind:       job.KindArtifactSweep,
		Version:    job.ArtifactSweepVersion,
		MinVersion: job.ArtifactSweepVersion,
		Steps: []durable.Step{
			{Name: jobStepName, Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
				res, err := s.artifactSweeper.Sweep(ctx)
				if err != nil {
					return nil, fmt.Errorf("job artifact sweep: %w", err)
				}

				s.logger.InfoContext(ctx, "job artifact sweep: deleted the blobs that no row names",
					slog.Int("jobs", res.Jobs), slog.Int("deleted", res.Deleted),
					slog.Int("unknown", res.Unknown), slog.Int("failed", res.Failed), slog.Bool("held", res.Held),
					slog.String("skipped", res.Skipped))

				// The output of the step is the record of the run: the counts
				// and the deleted keys.
				out, err := json.Marshal(res)
				if err != nil {
					return nil, fmt.Errorf("job artifact sweep: encode the result: %w", err)
				}

				return out, nil
			}},
		},
	}
}

// supersededSweepKind returns the runner kind of the schedule of the sweep of
// superseded checkpoint blobs. Its step returns the error of a run, so the
// runner counts a failed run as a failed attempt. The output of the step is
// the result of the run in JSON: its counts. The step writes one log line.
func (s *Server) supersededSweepKind() durable.KindSpec {
	return durable.KindSpec{
		Kind:       stack.KindSupersededSweep,
		Version:    stack.SupersededSweepVersion,
		MinVersion: stack.SupersededSweepVersion,
		Steps: []durable.Step{
			{Name: jobStepName, Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
				res, err := s.supersededSweeper.SweepSuperseded(ctx)
				if err != nil {
					return nil, fmt.Errorf("superseded checkpoint sweep: %w", err)
				}

				s.logger.InfoContext(ctx, "superseded checkpoint sweep: deleted the marked blobs that no row names",
					slog.Int("deleted", res.Deleted), slog.Int("kept", res.Kept), slog.Int("failed", res.Failed))

				out, err := json.Marshal(res)
				if err != nil {
					return nil, fmt.Errorf("superseded checkpoint sweep: encode the result: %w", err)
				}

				return out, nil
			}},
		},
	}
}

// nameLockPruneKind returns the runner kind of the schedule of the prune of
// the name counts. Its step returns the error of a run, so the runner counts
// a failed run as a failed attempt. The store appends one audit entry for
// each run, in the transaction of the delete, with the count that it
// deleted. The output of the step is the result of the run in JSON, and the
// step writes one log line.
func (s *Server) nameLockPruneKind() durable.KindSpec {
	return durable.KindSpec{
		Kind:       kindNameLockPrune,
		Version:    jobScheduleVersion,
		MinVersion: jobScheduleVersion,
		Steps: []durable.Step{
			{Name: jobStepName, Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
				res, err := localusersvc.PruneLocks(ctx, s.nameLocks.locks, s.now())
				if err != nil {
					return nil, fmt.Errorf("name lock prune: %w", err)
				}

				s.logger.InfoContext(ctx, "name lock prune: deleted the counts past their age",
					slog.Int64("deleted", res.Deleted), slog.Int64("age_seconds", res.AgeSeconds),
					slog.Int("max_rows", res.MaxRows))

				out, err := json.Marshal(res)
				if err != nil {
					return nil, fmt.Errorf("name lock prune: encode the result: %w", err)
				}

				return out, nil
			}},
		},
	}
}

// nameLockPrune holds the counts of the lockout by name that the schedule
// name-lock-prune prunes. A nil counter runs no prune.
type nameLockPrune struct {
	locks authgate.NameLockCounter
}

// nameLocksOf returns the counts of the lockout by name of the store of cfg.
// A store that keeps no such counts gives a nil counter, and the server runs
// no prune of them.
func nameLocksOf(cfg *settings) nameLockPrune {
	if locks, ok := cfg.namespaceStore.(authgate.NameLockCounter); ok {
		return nameLockPrune{locks: locks}
	}

	return nameLockPrune{locks: nil}
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
		Deferred:           func(err error) bool { return errors.Is(err, maintenance.ErrInMaintenance) },
		StopOnRenewalError: false,
		Logger:             s.logger,
		Rand:               nil,
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
