package dustpan

import (
	"context"
	"fmt"
	"sync"

	"opendustpan.org/opendustpan/internal/instance"
	"opendustpan.org/opendustpan/pkg/durable"
)

// DurableStore keeps the durable operations and the job schedules. Both SQL
// adapters serve it from the same database connection as the other ports.
type DurableStore interface {
	durable.Store
	durable.ScheduleStore
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
	inactivityScheduleName = "inactivity-disable"
	deletedScheduleName    = "deleted-sweep"
	signInPruneScheduleName = "signin-prune"

	jobScheduleVersion = 1
)

const (
	kindInactivity  durable.Kind = inactivityScheduleName
	kindDeleted     durable.Kind = deletedScheduleName
	kindSignInPrune durable.Kind = signInPruneScheduleName
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

	return specs
}

// jobKinds returns the runner kinds of the jobs that s runs. Each step runs
// its job body once and records no result.
func (s *Server) jobKinds() []durable.KindSpec {
	var kinds []durable.KindSpec

	if s.tokens != nil {
		kinds = append(kinds, durable.KindSpec{
			Kind: kindInactivity, Version: jobScheduleVersion, MinVersion: jobScheduleVersion,
			Steps: []durable.Step{{Name: "run", Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
				s.sweepOnce(ctx)

				return nil, nil
			}}},
		})
	}

	if s.deleted != nil {
		kinds = append(kinds, durable.KindSpec{
			Kind: kindDeleted, Version: jobScheduleVersion, MinVersion: jobScheduleVersion,
			Steps: []durable.Step{{Name: "run", Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
				s.sweepDeletedOnce(ctx)

				return nil, nil
			}}},
		})
	}

	if s.signInRequests != nil {
		kinds = append(kinds, durable.KindSpec{
			Kind: kindSignInPrune, Version: jobScheduleVersion, MinVersion: jobScheduleVersion,
			Steps: []durable.Step{{Name: "run", Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
				s.pruneSignInRequestsOnce(ctx)

				return nil, nil
			}}},
		})
	}

	return kinds
}

// newJobRunner builds the scheduler and the runner of the job schedules of
// s. It returns nil, nil when s runs no job. The runner runs one operation
// at a time, so the jobs never overlap on one instance.
func (s *Server) newJobRunner() (*durable.Scheduler, *durable.Runner, error) {
	specs := s.jobSchedules()
	if len(specs) == 0 {
		return nil, nil, nil //nolint:nilnil // No job is a valid setup: the server runs no schedule.
	}

	scheduler, err := durable.NewScheduler(durable.SchedulerConfig{
		Store: s.durable, Schedules: specs, Logger: s.logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("job scheduler: %w", err)
	}

	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store: s.durable, Owner: s.durableOwner, Kinds: s.jobKinds(), Logger: s.logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("job runner: %w", err)
	}

	return scheduler, runner, nil
}

// runJobSchedules runs the job schedules until ctx is done. It logs the
// deleted units due at the start, as the ticker loop did.
func (s *Server) runJobSchedules(ctx context.Context) error {
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

	var wait sync.WaitGroup

	wait.Go(func() { scheduler.Run(ctx) })
	wait.Go(func() { runner.Run(ctx) })
	wait.Wait()

	return nil
}
