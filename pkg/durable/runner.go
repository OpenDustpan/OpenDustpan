package durable

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"
)

// Defaults of RunnerConfig.
const (
	DefaultLease       = 30 * time.Second
	DefaultPollEvery   = time.Second
	DefaultMaxAttempts = 5
	DefaultBackoffBase = time.Second
	DefaultBackoffMax  = 5 * time.Minute
	// renewShare is the part of the lease between two renewals by default:
	// three renewals fit in one lease.
	renewShare = 3
	// releaseTimeout bounds the release of a claim at shutdown.
	releaseTimeout = 5 * time.Second
)

var (
	// ErrUnknownStep reports an operation with a recorded step that its kind
	// does not define in this binary.
	ErrUnknownStep = errors.New("durable operation has a step that this binary does not define")
	// errStepsDone stops the renewal after the last step.
	errStepsDone = errors.New("steps done")
)

// StepFunc runs one step. It returns the result to record, or an error. A
// step with a domain write passes sc.Fence to the store method of that
// write, so the write records the step in its own transaction. A step
// without a domain write must be safe to run again: the runner records it
// after it returns, and a stop between the two runs it again. ctx ends when
// the runner loses its claim, when a cancel arrives, or at shutdown.
type StepFunc func(ctx context.Context, sc StepContext) ([]byte, error)

// Step is one named step of a kind. The name is part of the stored record,
// so a rename makes older operations fail with ErrUnknownStep.
type Step struct {
	Name string
	Run  StepFunc
}

// StepContext is what a step gets: the operation as the runner knows it,
// with the steps recorded before this one, and the fence of this step.
type StepContext struct {
	Operation Operation
	Fence     StepFence
}

// KindSpec defines a kind of operation and its ordered steps.
type KindSpec struct {
	Kind  Kind
	Steps []Step
}

// RunnerConfig configures a Runner. A zero duration, count or policy takes
// its default.
type RunnerConfig struct {
	Store Store
	// Owner names this instance in the claims it takes, for status.
	Owner string
	Kinds []KindSpec
	// Lease is the time from a claim or a renewal to its expiry.
	Lease time.Duration
	// RenewEvery is the interval of the renewal. It must be shorter than
	// Lease. The default is one third of Lease.
	RenewEvery time.Duration
	// PollEvery is the wait after a claim that found no work.
	PollEvery time.Duration
	// MaxAttempts is the attempt after which a failure is final.
	MaxAttempts int
	Backoff     Backoff
	Logger      *slog.Logger
	// Rand returns a number in [0, 1) for the jitter of Backoff.
	Rand func() float64
}

// Runner claims operations of its kinds from the store and runs their steps.
// Each instance runs its own Runner on the shared store.
type Runner struct {
	cfg   RunnerConfig
	kinds map[Kind]KindSpec
	names []Kind
}

// permanentError marks an error that no retry can fix.
type permanentError struct {
	err error
}

func (e permanentError) Error() string { return e.err.Error() }

func (e permanentError) Unwrap() error { return e.err }

// Permanent marks err as final: the runner ends the operation failed at
// once, with no retry.
func Permanent(err error) error {
	if err == nil {
		return nil
	}

	return permanentError{err: err}
}

// isPermanent reports whether err holds an error that Permanent marked.
func isPermanent(err error) bool {
	perm, ok := errors.AsType[permanentError](err)

	return ok && perm.err != nil
}

// NewRunner checks cfg, fills its defaults and returns a Runner. It returns
// ErrInvalid, wrapped with the reason, for a config without a store, an
// owner or a kind, for a kind without steps, for a duplicate kind or step
// name, for a step without a function, and for RenewEvery not below Lease.
func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Store == nil || cfg.Owner == "" || len(cfg.Kinds) == 0 {
		return nil, fmt.Errorf("%w: a runner needs a store, an owner and at least one kind", ErrInvalid)
	}

	r := &Runner{cfg: withDefaults(cfg), kinds: make(map[Kind]KindSpec, len(cfg.Kinds)), names: nil}

	if r.cfg.RenewEvery >= r.cfg.Lease {
		return nil, fmt.Errorf("%w: renewal every %v is not shorter than the lease %v", ErrInvalid,
			r.cfg.RenewEvery, r.cfg.Lease)
	}

	for _, k := range cfg.Kinds {
		if err := checkKind(k); err != nil {
			return nil, err
		}

		if _, dup := r.kinds[k.Kind]; dup {
			return nil, fmt.Errorf("%w: kind %q is defined twice", ErrInvalid, k.Kind)
		}

		r.kinds[k.Kind] = k
		r.names = append(r.names, k.Kind)
	}

	return r, nil
}

func withDefaults(cfg RunnerConfig) RunnerConfig {
	if cfg.Lease <= 0 {
		cfg.Lease = DefaultLease
	}

	if cfg.RenewEvery <= 0 {
		cfg.RenewEvery = cfg.Lease / renewShare
	}

	if cfg.PollEvery <= 0 {
		cfg.PollEvery = DefaultPollEvery
	}

	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}

	if cfg.Backoff.Base <= 0 || cfg.Backoff.Max <= 0 {
		cfg.Backoff = Backoff{Base: DefaultBackoffBase, Max: DefaultBackoffMax}
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}

	return cfg
}

func checkKind(k KindSpec) error {
	if k.Kind == "" || len(k.Steps) == 0 {
		return fmt.Errorf("%w: kind %q needs a name and at least one step", ErrInvalid, k.Kind)
	}

	seen := make(map[string]bool, len(k.Steps))

	for _, st := range k.Steps {
		if st.Name == "" || st.Run == nil || seen[st.Name] {
			return fmt.Errorf("%w: kind %q has a step %q with no name, no function or a second definition",
				ErrInvalid, k.Kind, st.Name)
		}

		seen[st.Name] = true
	}

	return nil
}

// Run claims and runs operations, one at a time, until ctx ends. When ctx
// ends during a step, Run releases the claim before it returns, so another
// instance can claim the operation at once.
func (r *Runner) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := r.RunOnce(ctx)

		switch {
		case err == nil:
			continue
		case errors.Is(err, ErrNoWork), ctx.Err() != nil:
		default:
			r.cfg.Logger.WarnContext(ctx, "durable operation claim failed",
				slog.String("owner", r.cfg.Owner), slog.String("error", ErrorClass(err)))
		}

		wait(ctx, r.cfg.PollEvery)
	}
}

// RunOnce claims one operation and runs it until it ends, until the runner
// loses its claim, or until ctx ends. It returns ErrNoWork when no operation
// of the runner's kinds can run now, and the error of the claim otherwise.
// The outcome of the operation is in the store, not in the return value.
func (r *Runner) RunOnce(ctx context.Context) error {
	op, err := r.cfg.Store.Claim(ctx, ClaimRequest{Owner: r.cfg.Owner, Kinds: r.names, Lease: r.cfg.Lease})
	if err != nil {
		return fmt.Errorf("claim a durable operation: %w", err)
	}

	r.runClaimed(ctx, op)

	return nil
}

// runClaimed runs the steps of op under its claim, with a renewal in the
// background, and ends the attempt.
func (r *Runner) runClaimed(ctx context.Context, op Operation) {
	log := r.cfg.Logger.With(slog.String("operation", string(op.ID)), slog.String("kind", string(op.Kind)),
		slog.Int("attempt", op.Attempt), slog.String("owner", r.cfg.Owner))
	claim := op.Claim

	work, stop := context.WithCancelCause(ctx)
	defer stop(nil)

	var wg sync.WaitGroup

	wg.Go(func() { r.renew(work, stop, claim, log) })

	outcome, stepErr := r.runSteps(work, &op)

	stop(errStepsDone)
	wg.Wait()

	cause := context.Cause(work)

	switch {
	case errors.Is(cause, ErrClaimLost), outcome == outcomeLost:
		log.WarnContext(ctx, "durable operation claim lost; another instance owns the operation")
	case errors.Is(cause, ErrCanceled), outcome == outcomeCanceled, ctx.Err() != nil:
		// After a cancel, Release ends the operation canceled. At shutdown,
		// it returns the operation to pending for another instance.
		r.release(ctx, claim, log)
	case stepErr != nil:
		r.fail(ctx, claim, op, stepErr, log)
	default:
		err := r.cfg.Store.Complete(ctx, claim)
		if errors.Is(err, ErrCanceled) {
			r.release(ctx, claim, log)

			return
		}

		r.end(ctx, log, "complete", err)
	}
}

type outcome int

const (
	// outcomeDone means every step is recorded, or a step failed.
	outcomeDone outcome = iota
	// outcomeLost means the claim is lost: the runner writes nothing more.
	outcomeLost
	// outcomeCanceled means a write answered that a cancel is requested.
	outcomeCanceled
)

// stopOutcome returns the outcome for err from a write of the store, or
// outcomeDone when err does not stop the attempt.
func stopOutcome(err error) outcome {
	switch {
	case errors.Is(err, ErrClaimLost):
		return outcomeLost
	case errors.Is(err, ErrCanceled):
		return outcomeCanceled
	default:
		return outcomeDone
	}
}

// runSteps runs the steps of op that are not recorded, in order. It adds
// each recorded step to op.
func (r *Runner) runSteps(ctx context.Context, op *Operation) (outcome, error) {
	spec := r.kinds[op.Kind]

	for _, rec := range op.Steps {
		if !slices.ContainsFunc(spec.Steps, func(s Step) bool { return s.Name == rec.Name }) {
			return outcomeDone, Permanent(fmt.Errorf("%w: step %q of kind %q", ErrUnknownStep, rec.Name, op.Kind))
		}
	}

	for _, st := range spec.Steps {
		if _, done := op.Step(st.Name); done {
			continue
		}

		fence := op.Claim.Step(st.Name)

		result, err := st.Run(ctx, StepContext{Operation: *op, Fence: fence})
		if ctx.Err() != nil {
			// The caller reads the cause of ctx, not this error.
			return outcomeDone, fmt.Errorf("step %q stopped: %w", st.Name, context.Cause(ctx))
		}

		if err != nil {
			// A domain write of the step can answer that the claim is lost
			// or that a cancel is requested. That is no failure of the step.
			if stop := stopOutcome(err); stop != outcomeDone {
				return stop, nil
			}

			return outcomeDone, fmt.Errorf("step %q: %w", st.Name, err)
		}

		err = r.cfg.Store.RecordStep(ctx, fence, result)

		switch {
		case err == nil, errors.Is(err, ErrStepDone):
			// ErrStepDone: the domain write of the step recorded it.
		case stopOutcome(err) != outcomeDone:
			return stopOutcome(err), nil
		default:
			return outcomeDone, fmt.Errorf("record step %q: %w", st.Name, err)
		}

		op.Steps = append(op.Steps, StepRecord{Name: st.Name, Result: result, DoneAt: time.Time{}})
	}

	return outcomeDone, nil
}

// renew extends the claim every RenewEvery until ctx ends. When the store
// answers that the claim is lost or the operation is canceled, it stops the
// work with that cause. Other errors leave the work running: the store
// fences every write, so a late write fails on its own.
func (r *Runner) renew(ctx context.Context, stop context.CancelCauseFunc, claim Claim, log *slog.Logger) {
	ticker := time.NewTicker(r.cfg.RenewEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		next, err := r.cfg.Store.Extend(ctx, claim, r.cfg.Lease)

		switch {
		case err == nil:
			claim = next
		case errors.Is(err, ErrClaimLost), errors.Is(err, ErrCanceled):
			stop(err)

			return
		case ctx.Err() != nil:
			return
		default:
			log.WarnContext(ctx, "durable operation claim renewal failed", slog.String("error", ErrorClass(err)))
		}
	}
}

func (r *Runner) fail(ctx context.Context, claim Claim, op Operation, stepErr error, log *slog.Logger) {
	final := isPermanent(stepErr) || op.Attempt >= r.cfg.MaxAttempts
	delay := r.cfg.Backoff.Delay(op.Attempt, r.cfg.Rand)

	log.WarnContext(ctx, "durable operation attempt failed", slog.String("error", ErrorClass(stepErr)),
		slog.Bool("final", final), slog.Duration("retry_after", delay))

	err := r.cfg.Store.Fail(ctx, claim, Failure{Message: stepErr.Error(), Final: final, RetryAfter: delay})
	r.end(ctx, log, "fail", err)
}

// release returns the claim at shutdown or after a cancel. It uses its own
// deadline, because ctx can already be done.
func (r *Runner) release(ctx context.Context, claim Claim, log *slog.Logger) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	r.end(rctx, log, "release", r.cfg.Store.Release(rctx, claim))
}

// end logs the result of the write that ends an attempt.
func (r *Runner) end(ctx context.Context, log *slog.Logger, what string, err error) {
	if err != nil {
		log.WarnContext(ctx, "durable operation attempt did not end", slog.String("end", what),
			slog.String("error", ErrorClass(err)))

		return
	}

	log.DebugContext(ctx, "durable operation attempt ended", slog.String("end", what))
}

// ErrorClass names the class of err for a log line. A log line can go to a
// remote sink, so it never holds the text of a step, store or driver error.
// The full text of a step error is the last error of the operation.
func ErrorClass(err error) string {
	classes := []struct {
		err  error
		name string
	}{
		{ErrClaimLost, "claim_lost"},
		{ErrCanceled, "canceled"},
		{ErrStepDone, "step_done"},
		{ErrUnknownStep, "unknown_step"},
		{ErrInvalid, "invalid"},
		{ErrNotFound, "not_found"},
		{ErrNoWork, "no_work"},
		{context.DeadlineExceeded, "deadline"},
		{context.Canceled, "context_canceled"},
	}

	for _, c := range classes {
		if errors.Is(err, c.err) {
			return c.name
		}
	}

	if isPermanent(err) {
		return "permanent"
	}

	return "error"
}

// wait returns after d or when ctx ends.
func wait(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
