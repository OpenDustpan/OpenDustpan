package durable

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Defaults of SchedulerConfig.
const (
	// DefaultMaxFirstDelay caps the random delay before the first claim of
	// a schedule.
	DefaultMaxFirstDelay = time.Minute
	// firstDelayShare: the first delay is at most a tenth of the interval.
	firstDelayShare = 10
	// minWait keeps a loop from spinning when the store answers a wait of 0
	// for a tick that is not due.
	minWait = time.Millisecond
)

// SchedulerConfig configures a Scheduler. A zero duration or policy takes
// its default.
type SchedulerConfig struct {
	Store     ScheduleStore
	Schedules []ScheduleSpec
	// MaxFirstDelay caps the random delay before the first claim of each
	// schedule. The delay is uniform from 0 to the smaller of MaxFirstDelay
	// and a tenth of the interval.
	MaxFirstDelay time.Duration
	// ErrorBackoff is the delay after a store error, which grows with each
	// error in a row.
	ErrorBackoff Backoff
	Logger       *slog.Logger
	// Rand returns a number in [0, 1) for the first delay and the jitter.
	Rand func() float64
}

// Scheduler claims the ticks of its schedules. Each instance runs its own
// Scheduler. When a tick is due, the store gives it to one instance, which
// starts the operation of the tick in the same transaction. A Runner then
// runs that operation.
type Scheduler struct {
	cfg SchedulerConfig
}

// NewScheduler checks cfg, fills its defaults and returns a Scheduler. It
// returns ErrInvalid, wrapped with the reason, for a config without a store
// or a schedule, for a schedule that fails Validate, and for two schedules
// with one name.
func NewScheduler(cfg SchedulerConfig) (*Scheduler, error) {
	if cfg.Store == nil || len(cfg.Schedules) == 0 {
		return nil, fmt.Errorf("%w: a scheduler needs a store and at least one schedule", ErrInvalid)
	}

	seen := make(map[string]bool, len(cfg.Schedules))

	for _, spec := range cfg.Schedules {
		if err := spec.Validate(); err != nil {
			return nil, err
		}

		if seen[spec.Name] {
			return nil, fmt.Errorf("%w: schedule %q is defined twice", ErrInvalid, spec.Name)
		}

		seen[spec.Name] = true
	}

	if cfg.MaxFirstDelay <= 0 {
		cfg.MaxFirstDelay = DefaultMaxFirstDelay
	}

	if cfg.ErrorBackoff.Base <= 0 || cfg.ErrorBackoff.Max <= 0 {
		cfg.ErrorBackoff = Backoff{Base: DefaultPollEvery, Max: DefaultClaimBackoffMax}
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.Rand == nil {
		cfg.Rand = cryptoFloat64
	}

	return &Scheduler{cfg: cfg}, nil
}

// FirstDelay returns the random delay before the first claim of spec: uniform
// from 0 to the smaller of MaxFirstDelay and a tenth of the interval. Each
// schedule draws its own delay, so a start of many instances does not claim
// every schedule at the same moment.
func (s *Scheduler) FirstDelay(spec ScheduleSpec) time.Duration {
	bound := min(s.cfg.MaxFirstDelay, spec.Interval/firstDelayShare)

	return time.Duration(s.cfg.Rand() * float64(bound))
}

// Run claims the ticks of every schedule until ctx ends. It runs one loop
// for each schedule and returns when all of them ended.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup

	for _, spec := range s.cfg.Schedules {
		wg.Go(func() { s.loop(ctx, spec) })
	}

	wg.Wait()
}

// loop waits the first delay, then claims each tick of spec when it is due.
// It waits on the clock of the store: the store answers how long the next
// tick is away.
func (s *Scheduler) loop(ctx context.Context, spec ScheduleSpec) {
	log := s.cfg.Logger.With(slog.String("schedule", spec.Name), slog.String("kind", string(spec.Kind)))

	wait(ctx, s.FirstDelay(spec))

	ensured, errorsInRow := false, 0

	for ctx.Err() == nil {
		next, err := s.step(ctx, spec, &ensured, log)

		switch {
		case err == nil:
			errorsInRow = 0

			wait(ctx, max(next, minWait))
		case ctx.Err() != nil:
			return
		default:
			errorsInRow++

			log.WarnContext(ctx, "durable schedule store error", slog.String("error", ErrorClass(err)))
			wait(ctx, s.cfg.ErrorBackoff.Delay(errorsInRow, s.cfg.Rand))
		}
	}
}

// step ensures the schedule once, then claims its tick. It returns the wait
// until the next tick.
func (s *Scheduler) step(
	ctx context.Context,
	spec ScheduleSpec,
	ensured *bool,
	log *slog.Logger,
) (time.Duration, error) {
	if !*ensured {
		if _, err := s.cfg.Store.EnsureSchedule(ctx, spec); err != nil {
			return 0, fmt.Errorf("ensure schedule %q: %w", spec.Name, err)
		}

		*ensured = true
	}

	tick, err := s.cfg.Store.ClaimTick(ctx, spec)

	switch {
	case err == nil:
		log.InfoContext(ctx, "durable schedule tick claimed", slog.String("operation", string(tick.Operation.ID)))

		return tick.Schedule.Wait(), nil
	case errors.Is(err, ErrNoWork):
		return tick.Schedule.Wait(), nil
	case errors.Is(err, ErrNotFound):
		// The schedule row is gone, for example after a restore. Create it
		// again at the next step.
		*ensured = false

		return 0, fmt.Errorf("claim a tick of schedule %q: %w", spec.Name, err)
	default:
		return 0, fmt.Errorf("claim a tick of schedule %q: %w", spec.Name, err)
	}
}
