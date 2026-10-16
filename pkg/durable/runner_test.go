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

var errStepBroke = errors.New("step broke")

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

func newTestRunner(t *testing.T, store durable.Store, owner string, steps ...durable.Step) *durable.Runner {
	t.Helper()

	r, err := durable.NewRunner(durable.RunnerConfig{
		Store:       store,
		Owner:       owner,
		Kinds:       []durable.KindSpec{{Kind: testKind, Steps: steps}},
		Lease:       testLease,
		RenewEvery:  testRenew,
		PollEvery:   testPoll,
		MaxAttempts: 3,
		Backoff:     durable.Backoff{Base: 10 * time.Millisecond, Max: 20 * time.Millisecond},
		Logger:      nil,
		Rand:        func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	return r
}

// runInBackground runs r until the test ends, and waits for Run to return
// before the test ends.
func runInBackground(t *testing.T, r *durable.Runner) context.CancelFunc {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	var wg sync.WaitGroup

	wg.Go(func() {
		r.Run(ctx)
	})

	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	return func() {
		cancel()
		wg.Wait()
	}
}

func start(t *testing.T, store durable.Store, key string) durable.Operation {
	t.Helper()

	op, _, err := store.Start(t.Context(), durable.StartRequest{Key: key, Kind: testKind, Input: []byte("in")})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	return op
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

func pause(t *testing.T, d time.Duration) {
	t.Helper()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
	case <-timer.C:
	}
}

func TestNewRunnerValidates(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	ok := durable.Step{Name: "a", Run: func(context.Context, durable.StepContext) ([]byte, error) { return nil, nil }}
	one := []durable.KindSpec{{Kind: testKind, Steps: []durable.Step{ok}}}

	cfg := func(s durable.Store, owner string, kinds []durable.KindSpec) durable.RunnerConfig {
		return durable.RunnerConfig{
			Store: s, Owner: owner, Kinds: kinds, Lease: 0, RenewEvery: 0, PollEvery: 0, MaxAttempts: 0,
			Backoff: durable.Backoff{Base: 0, Max: 0}, Logger: nil, Rand: nil,
		}
	}

	slow := cfg(store, "a", one)
	slow.Lease, slow.RenewEvery = time.Second, time.Second

	cases := map[string]durable.RunnerConfig{
		"no store": cfg(nil, "a", one),
		"no owner": cfg(store, "", one),
		"no kind":  cfg(store, "a", nil),
		"no steps": cfg(store, "a", []durable.KindSpec{{Kind: testKind, Steps: nil}}),
		"step no func": cfg(
			store,
			"a",
			[]durable.KindSpec{{Kind: testKind, Steps: []durable.Step{{Name: "a", Run: nil}}}},
		),
		"duplicate step":          cfg(store, "a", []durable.KindSpec{{Kind: testKind, Steps: []durable.Step{ok, ok}}}),
		"duplicate kind":          cfg(store, "a", append(slices.Clone(one), one...)),
		"renewal not below lease": slow,
	}

	for name, c := range cases {
		if _, err := durable.NewRunner(c); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("%s: NewRunner error = %v, want ErrInvalid", name, err)
		}
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

func TestRunnerSkipsRecordedStepAfterTakeover(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	store := mem.Store()
	op := start(t, store, "k")

	// An older holder recorded step one and stopped.
	old, err := store.Claim(t.Context(), durable.ClaimRequest{
		Owner: "old", Kinds: []durable.Kind{testKind}, Lease: testLease,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := store.RecordStep(t.Context(), old.Claim.Step("one"), []byte("old-one")); err != nil {
		t.Fatalf("RecordStep: %v", err)
	}

	rec := newRecorder()
	r := newTestRunner(t, mem.Store(), "new", rec.step("one", nil), rec.step("two", nil))
	runInBackground(t, r)

	got := waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))

	if rec.count("one") != 0 || rec.count("two") != 1 {
		t.Fatalf("runs after takeover: one %d, two %d; want 0 and 1", rec.count("one"), rec.count("two"))
	}

	if res, _ := got.Step("one"); string(res.Result) != "old-one" {
		t.Fatalf("result of step one = %q, want the result of the older holder", res.Result)
	}
}

func TestRunnerRetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()

	var calls atomic.Int32

	flaky := rec.step("one", func(context.Context, durable.StepContext) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, errStepBroke
		}

		return nil, nil
	})

	r := newTestRunner(t, store, "a", flaky)
	op := start(t, store, "k")
	runInBackground(t, r)

	got := waitFor(t, store, op.ID, "succeeded", inState(durable.StateSucceeded))
	if got.Attempt != 2 || !strings.Contains(got.LastError, errStepBroke.Error()) {
		t.Fatalf("operation after a retry = %+v, want attempt 2 and the first error kept", got)
	}
}

func TestRunnerFailsAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	broken := rec.step("one", func(context.Context, durable.StepContext) ([]byte, error) { return nil, errStepBroke })

	r := newTestRunner(t, store, "a", broken)
	op := start(t, store, "k")
	runInBackground(t, r)

	got := waitFor(t, store, op.ID, "failed", inState(durable.StateFailed))
	if got.Attempt != 3 || rec.count("one") != 3 || !strings.Contains(got.LastError, "step broke") {
		t.Fatalf("operation = %+v after %d runs, want failed after 3 attempts with the step error", got,
			rec.count("one"))
	}
}

func TestRunnerPermanentErrorFailsAtOnce(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	final := rec.step("one", func(context.Context, durable.StepContext) ([]byte, error) {
		return nil, durable.Permanent(errStepBroke)
	})

	r := newTestRunner(t, store, "a", final)
	op := start(t, store, "k")

	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := waitFor(t, store, op.ID, "failed", inState(durable.StateFailed))
	if got.Attempt != 1 || rec.count("one") != 1 {
		t.Fatalf("operation = %+v, want failed after one attempt", got)
	}
}

func TestRunnerStopsAtCancel(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	rec := newRecorder()
	started := make(chan struct{})

	blocking := rec.step("one", func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		close(started)
		<-ctx.Done()

		return nil, ctx.Err()
	})

	r := newTestRunner(t, store, "a", blocking, rec.step("two", nil))
	op := start(t, store, "k")
	runInBackground(t, r)

	<-started

	if err := store.Cancel(t.Context(), op.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got := waitFor(t, store, op.ID, "canceled", inState(durable.StateCanceled))
	if len(got.Steps) != 0 || rec.count("two") != 0 {
		t.Fatalf("operation after cancel = %+v, two ran %d times; want no step", got, rec.count("two"))
	}
}

// lossyStore answers every renewal with ErrClaimLost and counts the writes
// that end an attempt.
type lossyStore struct {
	durable.Store

	ends atomic.Int32
}

func (s *lossyStore) Extend(context.Context, durable.Claim, time.Duration) (durable.Claim, error) {
	return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}, durable.ErrClaimLost
}

func (s *lossyStore) Complete(ctx context.Context, c durable.Claim) error {
	s.ends.Add(1)

	return s.Store.Complete(ctx, c)
}

func (s *lossyStore) Fail(ctx context.Context, c durable.Claim, f durable.Failure) error {
	s.ends.Add(1)

	return s.Store.Fail(ctx, c, f)
}

func (s *lossyStore) Release(ctx context.Context, c durable.Claim) error {
	s.ends.Add(1)

	return s.Store.Release(ctx, c)
}

func TestRunnerStopsWhenClaimLost(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	store := &lossyStore{Store: mem.Store(), ends: atomic.Int32{}}
	rec := newRecorder()

	blocking := rec.step("one", func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		<-ctx.Done()

		return nil, ctx.Err()
	})

	r := newTestRunner(t, store, "a", blocking, rec.step("two", nil))
	op := start(t, store, "k")

	if err := r.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got, err := mem.Store().Get(t.Context(), op.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if store.ends.Load() != 0 || len(got.Steps) != 0 || rec.count("two") != 0 {
		t.Fatalf("after a lost claim: %d end writes, steps %+v, two ran %d times; want no write", store.ends.Load(),
			got.Steps, rec.count("two"))
	}
}

func TestRunnerReleasesAtShutdown(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	started := make(chan struct{})

	blocking := durable.Step{Name: "one", Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		close(started)
		<-ctx.Done()

		return nil, ctx.Err()
	}}

	r := newTestRunner(t, store, "a", blocking)
	op := start(t, store, "k")
	stop := runInBackground(t, r)

	<-started
	stop()

	got, err := store.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.State != durable.StatePending || got.Claim.Token != 0 || got.Attempt != 1 {
		t.Fatalf("operation after shutdown = %+v, want pending with no claim and attempt 1", got)
	}
}

func TestRunnerFailsUnknownStep(t *testing.T) {
	t.Parallel()

	store := durabletest.NewMem().Store()
	op := start(t, store, "k")

	old, err := store.Claim(t.Context(), durable.ClaimRequest{
		Owner: "old", Kinds: []durable.Kind{testKind}, Lease: testLease,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := store.RecordStep(t.Context(), old.Claim.Step("renamed"), nil); err != nil {
		t.Fatalf("RecordStep: %v", err)
	}

	rec := newRecorder()
	r := newTestRunner(t, store, "new", rec.step("one", nil))
	runInBackground(t, r)

	got := waitFor(t, store, op.ID, "failed", inState(durable.StateFailed))
	if got.Attempt != 2 || rec.count("one") != 0 || !strings.Contains(got.LastError, `"renamed"`) {
		t.Fatalf("operation = %+v, want failed at once with an error that names the step", got)
	}
}

func TestTwoRunnersRunEachStepOnce(t *testing.T) {
	t.Parallel()

	mem := durabletest.NewMem()
	rec := newRecorder()
	steps := []durable.Step{rec.step("one", nil), rec.step("two", nil)}

	const ops = 6

	ids := make([]durable.ID, 0, ops)
	for i := range ops {
		ids = append(ids, start(t, mem.Store(), fmt.Sprintf("k%d", i)).ID)
	}

	runInBackground(t, newTestRunner(t, mem.Store(), "a", steps...))
	runInBackground(t, newTestRunner(t, mem.Store(), "b", steps...))

	for _, id := range ids {
		waitFor(t, mem.Store(), id, "succeeded", inState(durable.StateSucceeded))
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
