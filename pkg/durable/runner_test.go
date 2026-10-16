package durable_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
)

const (
	testKind   durable.Kind = "export"
	testLease               = 300 * time.Millisecond
	testRenew               = 50 * time.Millisecond
	testPoll                = 10 * time.Millisecond
	settleWait              = 10 * time.Second
)

var (
	errStepBroke = errors.New("step broke")
	errNetwork   = errors.New("network unreachable")
)

// recorder counts the runs of each step.
type recorder struct {
	mu   sync.Mutex
	runs map[string]int
}

func newRecorder() *recorder {
	return &recorder{mu: sync.Mutex{}, runs: map[string]int{}}
}

func (r *recorder) step(name string, fn durable.StepFunc) durable.Step {
	return durable.Step{Name: name, Run: func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
		r.mu.Lock()
		r.runs[name]++
		r.mu.Unlock()

		if fn == nil {
			return []byte("result-" + name), nil
		}

		return fn(ctx, sc)
	}}
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.runs[name]
}

// testConfig is the config of the test runners: short durations, 3
// attempts, no jitter.
func testConfig(store durable.Store, owner string, steps ...durable.Step) durable.RunnerConfig {
	return durable.RunnerConfig{
		Store:        store,
		Owner:        owner,
		Kinds:        []durable.KindSpec{{Kind: testKind, Version: 1, MinVersion: 1, Steps: steps}},
		Lease:        testLease,
		RenewEvery:   testRenew,
		PollEvery:    testPoll,
		MaxAttempts:  3,
		ExpiryLimit:  3,
		Backoff:      durable.Backoff{Base: 10 * time.Millisecond, Max: 20 * time.Millisecond},
		ClaimBackoff: durable.Backoff{Base: 0, Max: 0},
		WriteTimeout: time.Second,
		Logger:       nil,
		Rand:         func() float64 { return 0 },
	}
}

func newRunner(t *testing.T, cfg durable.RunnerConfig) *durable.Runner {
	t.Helper()

	r, err := durable.NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	return r
}

func newTestRunner(t *testing.T, store durable.Store, owner string, steps ...durable.Step) *durable.Runner {
	t.Helper()

	return newRunner(t, testConfig(store, owner, steps...))
}

// runInBackground runs r until the test ends, and waits for Run to return
// before the test ends. The returned function stops r and waits.
func runInBackground(t *testing.T, r *durable.Runner) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	var wg sync.WaitGroup

	wg.Go(func() { r.Run(ctx) })

	stop := func() {
		cancel()
		wg.Wait()
	}

	t.Cleanup(stop)

	return stop
}

func startVersion(t *testing.T, store durable.Store, key string, version int) durable.Operation {
	t.Helper()

	req := durable.StartRequest{Key: key, Kind: testKind, Version: version, Input: []byte("in")}

	op, _, err := store.Start(t.Context(), req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	return op
}

func start(t *testing.T, store durable.Store, key string) durable.Operation {
	t.Helper()

	return startVersion(t, store, key, 1)
}

// waitFor polls the operation until cond holds, and fails after settleWait.
func waitFor(
	t *testing.T, store durable.Store, id durable.ID, what string, cond func(durable.Operation) bool,
) durable.Operation {
	t.Helper()

	deadline := time.Now().Add(settleWait)

	for {
		op, err := store.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}

		if cond(op) {
			return op
		}

		if time.Now().After(deadline) {
			t.Fatalf("operation did not become %s within %v: %+v", what, settleWait, op)
		}

		pause(t, testPoll)
	}
}

func inState(s durable.State) func(durable.Operation) bool {
	return func(op durable.Operation) bool { return op.State == s }
}

func mustGet(t *testing.T, store durable.Store, id durable.ID) durable.Operation {
	t.Helper()

	op, err := store.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	return op
}

func pause(t *testing.T, d time.Duration) {
	t.Helper()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
	case <-timer.C:
	}
}

// blockUntilDone is a step that signals started and blocks until its
// context ends.
func blockUntilDone(name string, started chan<- struct{}) durable.Step {
	return durable.Step{Name: name, Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		close(started)
		<-ctx.Done()

		return nil, ctx.Err()
	}}
}

// countingStore counts the writes that end an attempt and can replace
// Extend and Complete.
type countingStore struct {
	durable.Store

	extend    func(context.Context, durable.Claim, time.Duration) (durable.Claim, error)
	complete  func(context.Context, durable.Claim) error
	completes atomic.Int32
	fails     atomic.Int32
	releases  atomic.Int32
	failures  chan durable.Failure
}

func newCountingStore(inner durable.Store) *countingStore {
	return &countingStore{
		Store: inner, extend: nil, complete: nil,
		completes: atomic.Int32{}, fails: atomic.Int32{}, releases: atomic.Int32{},
		failures: make(chan durable.Failure, 16),
	}
}

func (s *countingStore) Extend(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	if s.extend != nil {
		return s.extend(ctx, c, lease)
	}

	return s.Store.Extend(ctx, c, lease)
}

func (s *countingStore) Complete(ctx context.Context, c durable.Claim) error {
	s.completes.Add(1)

	if s.complete != nil {
		return s.complete(ctx, c)
	}

	return s.Store.Complete(ctx, c)
}

func (s *countingStore) Fail(ctx context.Context, c durable.Claim, f durable.Failure) error {
	s.fails.Add(1)
	s.failures <- f

	return s.Store.Fail(ctx, c, f)
}

func (s *countingStore) Release(ctx context.Context, c durable.Claim) error {
	s.releases.Add(1)

	return s.Store.Release(ctx, c)
}

func (s *countingStore) ends() int32 {
	return s.completes.Load() + s.fails.Load() + s.releases.Load()
}

func TestNewRunnerValidates(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	ok := durable.Step{Name: "a", Run: func(context.Context, durable.StepContext) ([]byte, error) { return nil, nil }}

	with := func(change func(*durable.RunnerConfig)) durable.RunnerConfig {
		cfg := testConfig(store, "a", ok)
		change(&cfg)

		return cfg
	}

	cases := map[string]durable.RunnerConfig{
		"no store": with(func(c *durable.RunnerConfig) { c.Store = nil }),
		"no owner": with(func(c *durable.RunnerConfig) { c.Owner = "" }),
		"no kind":  with(func(c *durable.RunnerConfig) { c.Kinds = nil }),
		"no steps": with(func(c *durable.RunnerConfig) { c.Kinds[0].Steps = nil }),
		"step no func": with(func(c *durable.RunnerConfig) {
			c.Kinds[0].Steps = []durable.Step{{Name: "a", Run: nil}}
		}),
		"duplicate step": with(func(c *durable.RunnerConfig) { c.Kinds[0].Steps = []durable.Step{ok, ok} }),
		"duplicate kind": with(func(c *durable.RunnerConfig) { c.Kinds = append(slices.Clone(c.Kinds), c.Kinds...) }),
		"min version above version": with(func(c *durable.RunnerConfig) {
			c.Kinds[0].Version, c.Kinds[0].MinVersion = 1, 2
		}),
		"negative min version": with(func(c *durable.RunnerConfig) { c.Kinds[0].MinVersion = -1 }),
		"renewal not below half the lease": with(func(c *durable.RunnerConfig) {
			c.Lease, c.RenewEvery = time.Second, time.Second/2
		}),
	}

	for name, cfg := range cases {
		if _, err := durable.NewRunner(cfg); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("%s: NewRunner error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestRunnerStartRequestCarriesTheVersion(t *testing.T) {
	t.Parallel()

	r := newTestRunner(t, durabletest.NewMem().Store(), "a", newRecorder().step("one", nil))

	req, err := r.StartRequest(testKind, "k", []byte("in"))
	if err != nil || req.Version != 1 || req.Kind != testKind || req.Key != "k" {
		t.Fatalf("StartRequest = %+v, %v; want version 1 of the registered kind", req, err)
	}

	if _, err := r.StartRequest("other", "k", nil); !errors.Is(err, durable.ErrInvalid) {
		t.Fatalf("StartRequest of a kind that is not registered: error = %v, want ErrInvalid", err)
	}
}

func TestRunnerRunsStepsInOrder(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()

	var seen []string

	second := rec.step("two", func(_ context.Context, sc durable.StepContext) ([]byte, error) {
		got, ok := sc.Operation.Step("one")
		seen = append(seen, fmt.Sprintf("%v %s %s", ok, got.Result, sc.Fence.Step))

		return []byte("result-two"), nil
	})

	r := newTestRunner(t, store, "a", rec.step("one", nil), second)
	op := start(t, store, "k")

	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))

	if names := stepNames(got); strings.Join(names, ",") != "one,two" {
		t.Fatalf("recorded steps = %v, want one,two", names)
	}

	if len(seen) != 1 || seen[0] != "true result-one two" {
		t.Fatalf("step two saw %v, want the result of step one and its own fence", seen)
	}

	if err := r.RunOnce(t.Context()); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("RunOnce with no work: error = %v, want ErrNoWork", err)
	}
}

func TestRunnerLeavesAnUnregisteredKindPending(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	req := durable.StartRequest{Key: "k", Kind: "other", Version: 1, Input: nil}

	op, _, err := store.Start(t.Context(), req)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	r := newTestRunner(t, store, "a", newRecorder().step("one", nil))

	if err := r.RunOnce(t.Context()); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("RunOnce with only an operation of another kind: error = %v, want ErrNoWork", err)
	}

	if got := mustGet(t, store, op.ID); got.State != durable.StatePending || got.Attempt != 0 {
		t.Fatalf("operation of a kind that is not registered = %+v, want pending and never claimed", got)
	}
}

func TestRunnerSkipsRecordedStepAfterTakeover(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	op := start(t, store, "k")

	// An older holder recorded step one and stopped.
	old, err := store.Claim(t.Context(), durable.ClaimRequest{
		Owner: "old", Kinds: []durable.Kind{testKind}, MaxVersions: nil, Lease: testLease,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := store.RecordStep(t.Context(), old.Claim.Step("one"), []byte("old-one")); err != nil {
		t.Fatalf("RecordStep: %v", err)
	}

	rec := newRecorder()
	runInBackground(t, newTestRunner(t, store, "new", rec.step("one", nil), rec.step("two", nil)))

	got := waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))

	if rec.count("one") != 0 || rec.count("two") != 1 {
		t.Fatalf("runs after takeover: one %d, two %d; want 0 and 1", rec.count("one"), rec.count("two"))
	}

	if res, _ := got.Step("one"); string(res.Result) != "old-one" {
		t.Fatalf("result of step one = %q, want the result of the older holder", res.Result)
	}
}

func TestRunnerUsesTheRecordedResultOfAStepThatItsWriteRecorded(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	store := mem.Store()
	domain := mem.Harness().Domain

	ns, err := domain.CreateNamespace(t.Context(), "ns")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	write := durable.Step{Name: "one", Run: func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
		err := domain.WriteStep(ctx, sc.Fence, ns, "v", durabletest.Hooks{Before: nil, After: nil})

		return []byte("not-recorded"), err
	}}

	var seen []byte

	read := durable.Step{Name: "two", Run: func(_ context.Context, sc durable.StepContext) ([]byte, error) {
		rec, _ := sc.Operation.Step("one")
		seen = rec.Result

		return nil, nil
	}}

	op := start(t, store, "k")

	if err := newTestRunner(t, store, "a", write, read).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))

	if seen != nil {
		t.Fatalf("step two saw result %q of step one, want the recorded result (none)", seen)
	}
}

// failingGet answers every Get with a network error.
type failingGet struct {
	durable.Store
}

func (failingGet) Get(context.Context, durable.ID) (durable.Operation, error) {
	return durable.Operation{}, errNetwork
}

func TestRunnerFailsTheAttemptWhenTheRecordedResultCannotBeRead(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	inner := mem.Store()
	domain := mem.Harness().Domain

	ns, err := domain.CreateNamespace(t.Context(), "ns")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	rec := newRecorder()
	write := rec.step("one", func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
		return nil, domain.WriteStep(ctx, sc.Fence, ns, "v", durabletest.Hooks{Before: nil, After: nil})
	})

	op := start(t, inner, "k")

	err = newTestRunner(t, failingGet{Store: inner}, "a", write, rec.step("two", nil)).RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := mustGet(t, inner, op.ID)
	if got.State != durable.StatePending || got.Failures != 1 || rec.count("two") != 0 {
		t.Fatalf("operation = %+v, two ran %d times; want a retry that is not final, with step two not run", got,
			rec.count("two"))
	}
}

func TestRunnerRetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()

	var calls atomic.Int32

	flaky := newRecorder().step("one", func(context.Context, durable.StepContext) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, errStepBroke
		}

		return nil, nil
	})

	op := start(t, store, "k")
	runInBackground(t, newTestRunner(t, store, "a", flaky))

	got := waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))
	if got.Failures != 1 || !strings.Contains(got.LastError, errStepBroke.Error()) {
		t.Fatalf("operation after a retry = %+v, want 1 failure and the first error kept", got)
	}
}

func TestRunnerPassesTheBackoffDelayToFail(t *testing.T) {
	t.Parallel()

	store := newCountingStore(durabletest.NewMem().Store())
	broken := newRecorder().step("one", func(context.Context, durable.StepContext) ([]byte, error) {
		return nil, errStepBroke
	})

	start(t, store, "k")

	if err := newTestRunner(t, store, "a", broken).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Backoff 10 ms for the first failure, and the jitter source 0 gives
	// the lower half: 5 ms.
	if f := <-store.failures; f.RetryAfter != 5*time.Millisecond || f.Final {
		t.Fatalf("Fail got %+v, want a retry after 5 ms that is not final", f)
	}
}

func TestRunnerFailsAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	broken := rec.step("one", func(context.Context, durable.StepContext) ([]byte, error) { return nil, errStepBroke })

	op := start(t, store, "k")
	runInBackground(t, newTestRunner(t, store, "a", broken))

	got := waitFor(t, store, op.ID, "failed", inState(durable.StateFailed))
	if got.Failures != 3 || rec.count("one") != 3 || !strings.Contains(got.LastError, "step broke") {
		t.Fatalf("operation = %+v after %d runs, want failed after 3 failures with the step error", got,
			rec.count("one"))
	}
}

func TestRunnerReleasesDoNotUseUpAttempts(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	op := start(t, store, "k")

	// Five holders release the operation, as at five restarts.
	for range 5 {
		held, err := store.Claim(t.Context(), durable.ClaimRequest{
			Owner: "restarting", Kinds: []durable.Kind{testKind}, MaxVersions: nil, Lease: testLease,
		})
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		if err := store.Release(t.Context(), held.Claim); err != nil {
			t.Fatalf("Release: %v", err)
		}
	}

	var calls atomic.Int32

	flaky := newRecorder().step("one", func(context.Context, durable.StepContext) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, errStepBroke
		}

		return nil, nil
	})

	runInBackground(t, newTestRunner(t, store, "a", flaky))

	got := waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))
	if got.Attempt != 7 || got.Failures != 1 {
		t.Fatalf("operation = %+v, want success after 5 releases and 1 failure", got)
	}
}

func TestRunnerPermanentErrorFailsAtOnce(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	final := rec.step("one", func(context.Context, durable.StepContext) ([]byte, error) {
		return nil, durable.Permanent(errStepBroke)
	})

	op := start(t, store, "k")

	if err := newTestRunner(t, store, "a", final).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := waitFor(t, store, op.ID, "failed", inState(durable.StateFailed))
	if got.Failures != 1 || rec.count("one") != 1 {
		t.Fatalf("operation = %+v, want failed after one attempt", got)
	}
}

func TestRunnerFailsAPanicAndStops(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	panicking := rec.step("one", func(context.Context, durable.StepContext) ([]byte, error) {
		panic("index out of range")
	})

	op := start(t, store, "k")

	if err := newTestRunner(t, store, "a", panicking, rec.step("two", nil)).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce after a panic: %v", err)
	}

	got := mustGet(t, store, op.ID)
	if got.State != durable.StateFailed || !strings.Contains(got.LastError, `"one"`) ||
		!strings.Contains(got.LastError, "index out of range") || rec.count("two") != 0 {
		t.Fatalf("operation after a panic = %+v, want failed with the step and the panic value", got)
	}
}

func TestRunnerFailsAfterTheExpiryLimit(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	op := start(t, store, "k")

	// Three holders claim with a short lease and stop with no end write.
	for range 3 {
		waitFor(t, store, op.ID, "claimable", func(durable.Operation) bool {
			_, err := store.Claim(t.Context(), durable.ClaimRequest{
				Owner: "crashing", Kinds: []durable.Kind{testKind}, MaxVersions: nil, Lease: 50 * time.Millisecond,
			})

			return err == nil
		})
	}

	runInBackground(t, newTestRunner(t, store, "a", rec.step("one", nil)))

	got := waitFor(t, store, op.ID, "failed", inState(durable.StateFailed))
	if got.Expiries != 3 || rec.count("one") != 0 || !strings.Contains(got.LastError, "3 holders stopped") {
		t.Fatalf("operation = %+v, want failed at the third expiry with no step run", got)
	}
}

func TestRunnerDoesNotClaimANewerVersion(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	op := startVersion(t, store, "k", 2)

	err := newTestRunner(t, store, "a", rec.step("one", nil)).RunOnce(t.Context())
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("RunOnce with only a newer version: error = %v, want ErrNoWork", err)
	}

	if got := mustGet(t, store, op.ID); got.State != durable.StatePending || got.Attempt != 0 || rec.count("one") != 0 {
		t.Fatalf("operation of a newer version = %+v, want pending and never claimed", got)
	}
}

// unlimitedClaims drops the version limits of each claim, as a store that
// does not follow them.
type unlimitedClaims struct {
	durable.Store
}

func (s unlimitedClaims) Claim(ctx context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	req.MaxVersions = nil

	return s.Store.Claim(ctx, req)
}

func TestRunnerReleasesANewerVersionThatTheStoreGave(t *testing.T) {
	t.Parallel()

	store := unlimitedClaims{Store: durabletest.NewMem().Store()}
	rec := newRecorder()
	op := startVersion(t, store, "k", 2)

	err := newTestRunner(t, store, "a", rec.step("one", nil)).RunOnce(t.Context())
	if !errors.Is(err, durable.ErrNewerVersion) {
		t.Fatalf("RunOnce of a newer version: error = %v, want ErrNewerVersion", err)
	}

	got := mustGet(t, store, op.ID)
	if got.State != durable.StatePending || got.Failures != 0 || rec.count("one") != 0 {
		t.Fatalf("operation of a newer version = %+v, want pending with no failure and no step run", got)
	}
}

func TestRunnerFailsAnOlderVersion(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	op := startVersion(t, store, "k", 0)

	if err := newTestRunner(t, store, "a", rec.step("one", nil)).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := mustGet(t, store, op.ID)
	if got.State != durable.StateFailed || !strings.Contains(got.LastError, "version 0") ||
		!strings.Contains(got.LastError, "runs 1 to 1") || rec.count("one") != 0 {
		t.Fatalf("operation of an older version = %+v, want failed with both versions named", got)
	}
}

func TestRunnerFailsUnknownStep(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	op := start(t, store, "k")

	old, err := store.Claim(t.Context(), durable.ClaimRequest{
		Owner: "old", Kinds: []durable.Kind{testKind}, MaxVersions: nil, Lease: testLease,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := store.RecordStep(t.Context(), old.Claim.Step("renamed"), nil); err != nil {
		t.Fatalf("RecordStep: %v", err)
	}

	rec := newRecorder()
	runInBackground(t, newTestRunner(t, store, "new", rec.step("one", nil)))

	got := waitFor(t, store, op.ID, "failed", inState(durable.StateFailed))
	if got.Failures != 1 || rec.count("one") != 0 || !strings.Contains(got.LastError, `"renamed"`) {
		t.Fatalf("operation = %+v, want failed at once with an error that names the step", got)
	}
}

func TestRunnerStopsAtCancel(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	started := make(chan struct{})

	op := start(t, store, "k")
	runInBackground(t, newTestRunner(t, store, "a", blockUntilDone("one", started), rec.step("two", nil)))

	<-started

	if err := store.Cancel(t.Context(), op.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got := waitFor(t, store, op.ID, "canceled", inState(durable.StateCanceled))
	if len(got.Steps) != 0 || rec.count("two") != 0 {
		t.Fatalf("operation after cancel = %+v, two ran %d times; want no step", got, rec.count("two"))
	}
}

func TestRunnerEndsCanceledWhenAStepWriteIsRefused(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	store := newCountingStore(mem.Store())
	domain := mem.Harness().Domain

	ns, err := domain.CreateNamespace(t.Context(), "ns")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	write := durable.Step{Name: "one", Run: func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
		if err := store.Cancel(ctx, sc.Operation.ID); err != nil {
			return nil, err
		}

		return nil, domain.WriteStep(ctx, sc.Fence, ns, "v", durabletest.Hooks{Before: nil, After: nil})
	}}

	op := start(t, store, "k")

	if err := newTestRunner(t, store, "a", write).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := mustGet(t, store, op.ID)
	if got.State != durable.StateCanceled || got.Failures != 0 || store.fails.Load() != 0 || len(got.Steps) != 0 {
		t.Fatalf("operation = %+v, %d Fail calls; want canceled with no failure and no step", got,
			store.fails.Load())
	}
}

func TestRunnerReleasesWhenCompleteFindsACancel(t *testing.T) {
	t.Parallel()

	inner := durabletest.NewMem().Store()
	store := newCountingStore(inner)

	// A cancel arrives between the last step and Complete.
	store.complete = func(ctx context.Context, c durable.Claim) error {
		if err := inner.Cancel(ctx, c.Operation); err != nil {
			return err
		}

		return inner.Complete(ctx, c)
	}

	op := start(t, store, "k")

	if err := newTestRunner(t, store, "a", newRecorder().step("one", nil)).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := mustGet(t, store, op.ID); got.State != durable.StateCanceled || store.releases.Load() != 1 {
		t.Fatalf("operation = %+v after %d releases, want canceled by one release", got, store.releases.Load())
	}
}

func TestRunnerDoesNotFailWhenAStepWriteFindsTheClaimLost(t *testing.T) {
	t.Parallel()

	store := newCountingStore(durabletest.NewMem().Store())
	lost := newRecorder().step("one", func(context.Context, durable.StepContext) ([]byte, error) {
		return nil, fmt.Errorf("write state: %w", durable.ErrClaimLost)
	})

	op := start(t, store, "k")

	if err := newTestRunner(t, store, "a", lost).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := mustGet(t, store, op.ID); store.ends() != 0 || got.Failures != 0 {
		t.Fatalf("after a step write found the claim lost: %d end writes, operation %+v; want no write", store.ends(),
			got)
	}
}

func TestRunnerStopsWhenClaimLost(t *testing.T) {
	t.Parallel()

	store := newCountingStore(durabletest.NewMem().Store())
	store.extend = func(context.Context, durable.Claim, time.Duration) (durable.Claim, error) {
		return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}, durable.ErrClaimLost
	}

	rec := newRecorder()
	started := make(chan struct{})
	op := start(t, store, "k")

	err := newTestRunner(t, store, "a", blockUntilDone("one", started), rec.step("two", nil)).RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := mustGet(t, store, op.ID); store.ends() != 0 || len(got.Steps) != 0 || rec.count("two") != 0 {
		t.Fatalf("after a lost claim: %d end writes, steps %+v, two ran %d times; want no write", store.ends(),
			got.Steps, rec.count("two"))
	}
}

func TestRunnerStopsAStepBeforeTheLeaseEndsWithNoRenewal(t *testing.T) {
	t.Parallel()

	inner := durabletest.NewMem().Store()
	store := newCountingStore(inner)
	store.extend = func(context.Context, durable.Claim, time.Duration) (durable.Claim, error) {
		return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}, errNetwork
	}

	stopped := make(chan time.Time, 1)
	step := durable.Step{Name: "one", Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		<-ctx.Done()
		stopped <- time.Now()

		return nil, ctx.Err()
	}}

	op := start(t, store, "k")
	began := time.Now()

	// RunOnce answers ErrClaimLost, so that Run backs off before its next
	// claim.
	if err := newTestRunner(t, store, "a", step).RunOnce(t.Context()); !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("RunOnce with no renewal: error = %v, want ErrClaimLost", err)
	}

	// The step must stop before the lease ends, so that a new holder never
	// runs it at the same time.
	if took := (<-stopped).Sub(began); took >= testLease {
		t.Fatalf("the step stopped %v after the claim, want before the end of the lease %v", took, testLease)
	}

	// The runner releases the claim while it is live, so the stop is no
	// expiry and no failure.
	got := mustGet(t, inner, op.ID)
	if store.releases.Load() != 1 || store.completes.Load()+store.fails.Load() != 0 ||
		got.State != durable.StatePending || got.Expiries != 0 || got.Failures != 0 {
		t.Fatalf("after a stop near the end of the lease: %d releases, operation %+v; want one release, pending, "+
			"no expiry and no failure", store.releases.Load(), got)
	}
}

func TestRunnerKeepsAStepWithASlowRenewal(t *testing.T) {
	t.Parallel()

	inner := durabletest.NewMem().Store()
	store := newCountingStore(inner)

	// Each Extend takes longer than the renewal interval, and much less
	// than the lease.
	store.extend = func(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
		timer := time.NewTimer(testRenew + testRenew/2)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}, ctx.Err()
		case <-timer.C:
		}

		return inner.Extend(ctx, c, lease)
	}

	long := durable.Step{Name: "one", Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		timer := time.NewTimer(2 * testLease)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, nil
		}
	}}

	op := start(t, store, "k")

	if err := newTestRunner(t, store, "a", long).RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := mustGet(t, inner, op.ID); got.State != durable.StateSucceeded || got.Expiries != 0 {
		t.Fatalf("operation with slow renewals = %+v, want succeeded with no expiry", got)
	}
}

func TestOneHolderRunsAStepWhenOneRunnerCannotRenew(t *testing.T) {
	t.Parallel()

	inner := durabletest.NewMem().Store()
	broken := newCountingStore(inner)
	broken.extend = func(context.Context, durable.Claim, time.Duration) (durable.Claim, error) {
		return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}, errNetwork
	}

	var active, most atomic.Int32

	// The step runs longer than the lease, so the runner that cannot renew
	// never finishes it.
	long := durable.Step{Name: "one", Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		now := active.Add(1)
		defer active.Add(-1)

		for {
			old := most.Load()
			if now <= old || most.CompareAndSwap(old, now) {
				break
			}
		}

		timer := time.NewTimer(testLease + testLease/2)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, nil
		}
	}}

	op := start(t, inner, "k")

	runInBackground(t, newTestRunner(t, broken, "a", long))
	runInBackground(t, newTestRunner(t, inner, "b", long))

	waitFor(t, inner, op.ID, "succeeded", inState(durable.StateSucceeded))

	if most.Load() > 1 {
		t.Fatalf("%d holders ran the step at one time, want at most 1", most.Load())
	}
}

func TestRunnerReleasesAtShutdown(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	started := make(chan struct{})

	op := start(t, store, "k")
	stop := runInBackground(t, newTestRunner(t, store, "a", blockUntilDone("one", started)))

	<-started
	stop()

	got := mustGet(t, store, op.ID)
	if got.State != durable.StatePending || got.Claim.Token != 0 || got.Attempt != 1 || got.Failures != 0 {
		t.Fatalf("operation after shutdown = %+v, want pending with no claim and no failure", got)
	}
}

// failingClaims answers the first n claims with a network error.
type failingClaims struct {
	durable.Store

	n     atomic.Int32
	times chan time.Time
}

func (s *failingClaims) Claim(ctx context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	if s.n.Add(-1) >= 0 {
		s.times <- time.Now()

		return durable.Operation{}, errNetwork
	}

	return s.Store.Claim(ctx, req)
}

func TestRunnerBacksOffAfterClaimErrors(t *testing.T) {
	t.Parallel()

	const failures = 4

	store := &failingClaims{
		Store: durabletest.NewMem().Store(),
		n:     atomic.Int32{},
		times: make(chan time.Time, failures),
	}
	store.n.Store(failures)

	cfg := testConfig(store, "a", newRecorder().step("one", nil))
	cfg.ClaimBackoff = durable.Backoff{Base: 40 * time.Millisecond, Max: 400 * time.Millisecond}
	cfg.Rand = func() float64 { return 0.999 }

	runInBackground(t, newRunner(t, cfg))

	at := make([]time.Time, 0, failures)
	for range failures {
		at = append(at, <-store.times)
	}

	// The delays before jitter are 40, 80 and 160 ms, and the jitter source
	// keeps them close to that.
	if gap := at[3].Sub(at[2]); gap < 120*time.Millisecond {
		t.Fatalf("delay after the third claim error = %v, want about 160 ms", gap)
	}
}

func TestTwoRunnersRunEachStepOnce(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	steps := []durable.Step{rec.step("one", nil), rec.step("two", nil)}

	const ops = 6

	ids := make([]durable.ID, 0, ops)
	for i := range ops {
		ids = append(ids, start(t, store, fmt.Sprintf("k%d", i)).ID)
	}

	runInBackground(t, newTestRunner(t, store, "a", steps...))
	runInBackground(t, newTestRunner(t, store, "b", steps...))

	for _, id := range ids {
		waitFor(t, store, id, "succeeded", inState(durable.StateSucceeded))
	}

	if rec.count("one") != ops || rec.count("two") != ops {
		t.Fatalf("runs: one %d, two %d; want %d each", rec.count("one"), rec.count("two"), ops)
	}
}

func stepNames(op durable.Operation) []string {
	names := make([]string, 0, len(op.Steps))
	for _, s := range op.Steps {
		names = append(names, s.Name)
	}

	return names
}
