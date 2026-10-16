package durable

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"
)

// Defaults of RunnerConfig.
const (
	DefaultLease           = 30 * time.Second
	DefaultPollEvery       = time.Second
	DefaultMaxAttempts     = 5
	DefaultExpiryLimit     = 3
	DefaultBackoffBase     = time.Second
	DefaultBackoffMax      = 5 * time.Minute
	DefaultClaimBackoffMax = 30 * time.Second
	DefaultWriteTimeout    = 5 * time.Second
	// renewShare is the part of the lease between two renewals by default:
	// three renewals fit in one lease.
	renewShare = 3
)

var (
	// ErrUnknownStep reports an operation with a recorded step that its kind
	// does not define in this binary.
	ErrUnknownStep = errors.New("durable operation has a step that this binary does not define")
	// ErrNewerVersion reports an operation of a version above the versions
	// that this binary runs. The runner releases it for a newer instance.
	ErrNewerVersion = errors.New("durable operation has a version that is newer than this binary runs")
	// ErrOlderVersion reports an operation of a version below the versions
	// that this binary runs.
	ErrOlderVersion = errors.New("durable operation has a version that is older than this binary runs")
	// ErrExpiryLimit reports an operation whose holders stopped without an
	// end write as often as the limit allows.
	ErrExpiryLimit = errors.New("durable operation holders stopped without an end write too often")
	// ErrStepPanic reports a step that panicked.
	ErrStepPanic = errors.New("durable operation step panicked")
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

// KindSpec defines a kind of operation and its ordered steps. Version is
// the version that this binary starts, and MinVersion the oldest version
// that it runs: 0 <= MinVersion <= Version. A change of the steps that an
// older binary cannot run raises Version. A change that makes this binary
// unable to run older operations raises MinVersion.
type KindSpec struct {
	Kind       Kind
	Version    int
	MinVersion int
	Steps      []Step
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
	// MaxAttempts is the number of failures after which a failure is final.
	// It counts step errors only: a release or a takeover does not count.
	MaxAttempts int
	// ExpiryLimit is the number of holders that may stop without an end
	// write. At a claim that reaches it, the runner ends the operation
	// failed.
	ExpiryLimit int
	// Backoff is the delay before the retry of a failed attempt.
	Backoff Backoff
	// ClaimBackoff is the delay after a claim that failed with an error, for
	// example when the database does not answer. The default starts at
	// PollEvery.
	ClaimBackoff Backoff
	// WriteTimeout bounds each write that ends an attempt, also after ctx
	// ended.
	WriteTimeout time.Duration
	Logger       *slog.Logger
	// Rand returns a number in [0, 1) for the jitter of Backoff.
	Rand func() float64
}

// Runner claims operations of its kinds from the store and runs their steps.
// Each instance runs its own Runner on the shared store. A Runner runs one
// operation at a time. An instance that must run kinds in parallel runs one
// Runner for each group of kinds.
type Runner struct {
	cfg   RunnerConfig
	kinds map[Kind]KindSpec
	names []Kind
	// versions limits each claim to the versions that this binary runs.
	versions map[Kind]int
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

	r := &Runner{
		cfg: withDefaults(cfg), kinds: make(map[Kind]KindSpec, len(cfg.Kinds)), names: nil,
		versions: make(map[Kind]int, len(cfg.Kinds)),
	}

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
		r.versions[k.Kind] = k.Version
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

	if cfg.ExpiryLimit <= 0 {
		cfg.ExpiryLimit = DefaultExpiryLimit
	}

	if cfg.Backoff.Base <= 0 || cfg.Backoff.Max <= 0 {
		cfg.Backoff = Backoff{Base: DefaultBackoffBase, Max: DefaultBackoffMax}
	}

	cfg = withWriteDefaults(cfg)

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.Rand == nil {
		cfg.Rand = cryptoFloat64
	}

	return cfg
}

// withWriteDefaults fills the defaults of the claim backoff and the write
// timeout.
func withWriteDefaults(cfg RunnerConfig) RunnerConfig {
	if cfg.ClaimBackoff.Base <= 0 || cfg.ClaimBackoff.Max <= 0 {
		cfg.ClaimBackoff = Backoff{Base: cfg.PollEvery, Max: max(cfg.PollEvery, DefaultClaimBackoffMax)}
	}

	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = DefaultWriteTimeout
	}

	return cfg
}

func checkKind(k KindSpec) error {
	if k.Kind == "" || len(k.Steps) == 0 {
		return fmt.Errorf("%w: kind %q needs a name and at least one step", ErrInvalid, k.Kind)
	}

	if k.MinVersion < 0 || k.MinVersion > k.Version {
		return fmt.Errorf("%w: kind %q needs 0 <= MinVersion (%d) <= Version (%d)", ErrInvalid, k.Kind,
			k.MinVersion, k.Version)
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

// StartRequest returns the request that starts an operation of kind, with
// the version of kind in this binary. It returns ErrInvalid for a kind that
// the runner does not register.
func (r *Runner) StartRequest(kind Kind, key string, input []byte) (StartRequest, error) {
	spec, ok := r.kinds[kind]
	if !ok {
		return StartRequest{Key: "", Kind: "", Version: 0, Input: nil},
			fmt.Errorf("%w: kind %q is not registered", ErrInvalid, kind)
	}

	return StartRequest{Key: key, Kind: kind, Version: spec.Version, Input: input}, nil
}

// Run claims and runs operations, one at a time, until ctx ends. When ctx
// ends during a step, Run releases the claim before it returns, so another
// instance can claim the operation at once. After a claim that found no
// work, Run waits PollEvery. After a claim that failed, it waits the
// ClaimBackoff delay, which grows with each failure in a row.
func (r *Runner) Run(ctx context.Context) {
	failures := 0

	for ctx.Err() == nil {
		err := r.RunOnce(ctx)

		switch {
		case err == nil:
			failures = 0

			continue
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrNoWork), errors.Is(err, ErrNewerVersion):
			failures = 0

			wait(ctx, r.cfg.PollEvery)
		default:
			failures++

			r.cfg.Logger.WarnContext(ctx, "durable operation claim failed",
				slog.String("owner", r.cfg.Owner), slog.String("error", ErrorClass(err)))
			wait(ctx, r.cfg.ClaimBackoff.Delay(failures, r.cfg.Rand))
		}
	}
}

// RunOnce claims one operation and runs it until it ends, until the runner
// loses its claim, or until ctx ends. It returns ErrNoWork when no operation
// of the runner's kinds can run now, ErrNewerVersion when it released an
// operation for a newer instance, and the error of the claim otherwise. The
// outcome of the operation is in the store, not in the return value.
func (r *Runner) RunOnce(ctx context.Context) error {
	// The local deadline of the claim counts from before the claim, so it
	// ends before the expiry that the store sets.
	claimedAt := time.Now()

	op, err := r.cfg.Store.Claim(ctx, ClaimRequest{
		Owner: r.cfg.Owner, Kinds: r.names, MaxVersions: r.versions, Lease: r.cfg.Lease,
	})
	if err != nil {
		return fmt.Errorf("claim a durable operation: %w", err)
	}

	return r.runClaimed(ctx, op, claimedAt)
}

// runClaimed checks op, runs its steps under its claim with a renewal in
// the background, and ends the attempt. It returns ErrNewerVersion when it
// released op for a newer instance.
func (r *Runner) runClaimed(ctx context.Context, op Operation, claimedAt time.Time) error {
	log := r.cfg.Logger.With(slog.String("operation", string(op.ID)), slog.String("kind", string(op.Kind)),
		slog.Int("attempt", op.Attempt), slog.String("owner", r.cfg.Owner))
	claim := op.Claim

	if done, err := r.check(ctx, op, log); done {
		return err
	}

	work, stop := context.WithCancelCause(ctx)

	var wg sync.WaitGroup

	// The renewal always ends, also when a step panics past the recover.
	defer wg.Wait()
	defer stop(errStepsDone)

	wg.Go(func() { r.renew(work, stop, claim, claimedAt, log) })

	outcome, stepErr := r.runSteps(work, &op, log)

	stop(errStepsDone)
	wg.Wait()

	cause := context.Cause(work)

	switch {
	case errors.Is(cause, ErrClaimLost), outcome == outcomeLost:
		log.WarnContext(ctx, "durable operation claim lost; another instance can own the operation")
	case errors.Is(cause, ErrCanceled), outcome == outcomeCanceled, ctx.Err() != nil:
		// After a cancel, Release ends the operation canceled. At shutdown,
		// it returns the operation to pending for another instance.
		r.release(ctx, claim, log)
	case stepErr != nil:
		r.fail(ctx, claim, op, stepErr, log)
	default:
		r.complete(ctx, claim, log)
	}

	return nil
}

// check ends the attempt before any step when op cannot run here: too many
// holders stopped, its version is outside the versions of this binary, or
// it has a recorded step that its kind does not define. done is true when
// check ended the attempt.
func (r *Runner) check(ctx context.Context, op Operation, log *slog.Logger) (bool, error) {
	spec := r.kinds[op.Kind]

	switch {
	case op.Expiries >= r.cfg.ExpiryLimit:
		r.fail(ctx, op.Claim, op, Permanent(fmt.Errorf("%w: %d holders stopped, and the limit is %d",
			ErrExpiryLimit, op.Expiries, r.cfg.ExpiryLimit)), log)

		return true, nil
	case op.Version > spec.Version:
		// The claim limits the version, so only a store that ignores the
		// limit gets here. Release it for a newer instance.
		r.release(ctx, op.Claim, log)

		return true, fmt.Errorf("%w: operation %s has version %d, and this binary runs %d to %d",
			ErrNewerVersion, op.ID, op.Version, spec.MinVersion, spec.Version)
	case op.Version < spec.MinVersion:
		r.fail(ctx, op.Claim, op, Permanent(fmt.Errorf("%w: version %d, and this binary runs %d to %d",
			ErrOlderVersion, op.Version, spec.MinVersion, spec.Version)), log)

		return true, nil
	}

	for _, rec := range op.Steps {
		if !slices.ContainsFunc(spec.Steps, func(s Step) bool { return s.Name == rec.Name }) {
			r.fail(ctx, op.Claim, op, Permanent(fmt.Errorf("%w: step %q of kind %q version %d", ErrUnknownStep,
				rec.Name, op.Kind, op.Version)), log)

			return true, nil
		}
	}

	return false, nil
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
func (r *Runner) runSteps(ctx context.Context, op *Operation, log *slog.Logger) (outcome, error) {
	for _, st := range r.kinds[op.Kind].Steps {
		if _, done := op.Step(st.Name); done {
			continue
		}

		if ctx.Err() != nil {
			// The caller reads the cause of ctx, not this error.
			return outcomeDone, fmt.Errorf("before step %q: %w", st.Name, context.Cause(ctx))
		}

		fence := op.Claim.Step(st.Name)

		result, err := r.runStep(ctx, st, StepContext{Operation: *op, Fence: fence}, log)
		if ctx.Err() != nil {
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
		case err == nil:
		case errors.Is(err, ErrStepDone):
			// The domain write of the step recorded it. The later steps get
			// the result that the store holds.
			result = r.recorded(ctx, op.ID, st.Name)
		case stopOutcome(err) != outcomeDone:
			return stopOutcome(err), nil
		default:
			return outcomeDone, fmt.Errorf("record step %q: %w", st.Name, err)
		}

		op.Steps = append(op.Steps, StepRecord{Name: st.Name, Result: result, DoneAt: time.Time{}})
	}

	return outcomeDone, nil
}

// runStep runs st and turns a panic into a permanent error that names the
// step and the panic value. It logs the stack, not the value.
func (r *Runner) runStep(ctx context.Context, st Step, sc StepContext, log *slog.Logger) ([]byte, error) {
	var (
		result []byte
		err    error
	)

	func() {
		defer func() {
			if v := recover(); v != nil {
				log.ErrorContext(ctx, "durable operation step panicked", slog.String("step", st.Name),
					slog.String("stack", string(debug.Stack())))

				result, err = nil, Permanent(fmt.Errorf("%w: step %q: %v", ErrStepPanic, st.Name, v))
			}
		}()

		result, err = st.Run(ctx, sc)
	}()

	return result, err
}

// recorded returns the result that the store holds for the step called
// name of operation id, or nil when the store does not answer.
func (r *Runner) recorded(ctx context.Context, id ID, name string) []byte {
	got, err := r.cfg.Store.Get(ctx, id)
	if err != nil {
		return nil
	}

	rec, _ := got.Step(name)

	return rec.Result
}

// renew extends the claim every RenewEvery until ctx ends. When the store
// answers that the claim is lost or the operation is canceled, it stops the
// work with that cause. Each Extend has a timeout. When the time since the
// start of the last successful Extend, or of the claim, reaches the lease,
// renew stops the work with ErrClaimLost: by then the store can give the
// operation to another instance, and two holders must not run one step at
// once. The time is monotonic, so a step of the wall clock does not move it.
func (r *Runner) renew(
	ctx context.Context, stop context.CancelCauseFunc, claim Claim, claimedAt time.Time, log *slog.Logger,
) {
	ticker := time.NewTicker(r.cfg.RenewEvery)
	defer ticker.Stop()

	lastStart := claimedAt

	deadline := time.NewTimer(r.cfg.Lease - time.Since(lastStart))
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			log.WarnContext(ctx, "durable operation lease ran out with no renewal")
			stop(ErrClaimLost)

			return
		case <-ticker.C:
		}

		left := r.cfg.Lease - time.Since(lastStart)
		start := time.Now()

		ectx, cancel := context.WithTimeout(ctx, min(r.cfg.RenewEvery, left))
		next, err := r.cfg.Store.Extend(ectx, claim, r.cfg.Lease)

		cancel()

		switch {
		case err == nil:
			claim, lastStart = next, start
			deadline.Reset(r.cfg.Lease - time.Since(lastStart))
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
	// This failure is number Failures+1.
	failure := op.Failures + 1
	final := isPermanent(stepErr) || failure >= r.cfg.MaxAttempts
	delay := r.cfg.Backoff.Delay(failure, r.cfg.Rand)

	log.WarnContext(ctx, "durable operation attempt failed", slog.String("error", ErrorClass(stepErr)),
		slog.Bool("final", final), slog.Duration("retry_after", delay))

	wctx, cancel := r.writeContext(ctx)
	defer cancel()

	err := r.cfg.Store.Fail(wctx, claim, Failure{Message: stepErr.Error(), Final: final, RetryAfter: delay})
	r.end(wctx, log, "fail", err)
}

// complete ends the attempt as succeeded. When the store answers that a
// cancel is requested, it releases the claim, which ends the operation
// canceled.
func (r *Runner) complete(ctx context.Context, claim Claim, log *slog.Logger) {
	wctx, cancel := r.writeContext(ctx)
	defer cancel()

	err := r.cfg.Store.Complete(wctx, claim)
	if errors.Is(err, ErrCanceled) {
		r.release(ctx, claim, log)

		return
	}

	r.end(wctx, log, "complete", err)
}

// release returns the claim at shutdown, after a cancel or for a newer
// instance.
func (r *Runner) release(ctx context.Context, claim Claim, log *slog.Logger) {
	wctx, cancel := r.writeContext(ctx)
	defer cancel()

	r.end(wctx, log, "release", r.cfg.Store.Release(wctx, claim))
}

// writeContext returns the context of a write that ends an attempt: not
// canceled with ctx, because ctx can already be done, and bounded by
// WriteTimeout.
func (r *Runner) writeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), r.cfg.WriteTimeout)
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
		{ErrNewerVersion, "newer_version"},
		{ErrOlderVersion, "older_version"},
		{ErrExpiryLimit, "expiry_limit"},
		{ErrStepPanic, "step_panic"},
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

const (
	// float64Bits is the number of random bits in a float64 in [0, 1).
	float64Bits = 53
	// uint64Bits is the size of the random word.
	uint64Bits = 64
	// jitterMiddle is the middle of [0, 1).
	jitterMiddle = 0.5
)

// cryptoFloat64 returns a number in [0, 1) from crypto/rand. A failed read
// gives 0.5, the middle of the jitter.
func cryptoFloat64() float64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return jitterMiddle
	}

	return float64(binary.BigEndian.Uint64(b[:])>>(uint64Bits-float64Bits)) / (1 << float64Bits)
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
