package storetest

import (
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
)

// SignInFailureFactory returns a fresh store that counts failed sign-ins.
type SignInFailureFactory func(t *testing.T) authgate.FailureCounter

// RunSignInFailures runs the conformance suite of authgate.FailureCounter
// against stores from newStore.
func RunSignInFailures(t *testing.T, newStore SignInFailureFactory) {
	t.Helper()

	tests := []struct {
		test func(t *testing.T, s authgate.FailureCounter)
		name string
	}{
		{name: "an add answers the count of its window", test: testFailuresCount},
		{name: "sources and windows count apart", test: testFailuresApart},
		{name: "concurrent adds each count once", test: testFailuresConcurrent},
		{name: "a prune deletes the windows before a time", test: testFailuresPrune},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.test(t, newStore(t))
		})
	}
}

// failureWindow returns the window start of the suite, in whole
// milliseconds.
func failureWindow() time.Time {
	return time.Date(2026, time.October, 16, 12, 0, 0, 0, time.UTC)
}

func testFailuresCount(t *testing.T, s authgate.FailureCounter) {
	t.Helper()

	ctx := t.Context()

	if got, err := s.Failures(ctx, "192.0.2.1", failureWindow()); err != nil || got != 0 {
		t.Fatalf("failures of a new source = %d, %v, want 0", got, err)
	}

	for want := int64(1); want <= 3; want++ {
		got, err := s.AddFailure(ctx, "192.0.2.1", failureWindow())
		if err != nil || got != want {
			t.Fatalf("add failure %d = %d, %v, want %d", want, got, err, want)
		}
	}

	if got, err := s.Failures(ctx, "192.0.2.1", failureWindow()); err != nil || got != 3 {
		t.Fatalf("failures after three adds = %d, %v, want 3", got, err)
	}
}

func testFailuresApart(t *testing.T, s authgate.FailureCounter) {
	t.Helper()

	ctx := t.Context()
	next := failureWindow().Add(time.Minute)

	adds := []struct {
		source string
		start  time.Time
	}{
		{source: "192.0.2.1", start: failureWindow()},
		{source: "192.0.2.1", start: failureWindow()},
		{source: "2001:db8::/64", start: failureWindow()},
		{source: "192.0.2.1", start: next},
	}

	for _, a := range adds {
		if _, err := s.AddFailure(ctx, a.source, a.start); err != nil {
			t.Fatalf("add failure of %s at %v: %v", a.source, a.start, err)
		}
	}

	wants := []struct {
		source string
		start  time.Time
		want   int64
	}{
		{source: "192.0.2.1", start: failureWindow(), want: 2},
		{source: "2001:db8::/64", start: failureWindow(), want: 1},
		{source: "192.0.2.1", start: next, want: 1},
		{source: "2001:db8::/64", start: next, want: 0},
		{source: "192.0.2.2", start: failureWindow(), want: 0},
	}

	for _, w := range wants {
		if got, err := s.Failures(ctx, w.source, w.start); err != nil || got != w.want {
			t.Fatalf("failures of %s at %v = %d, %v, want %d", w.source, w.start, got, err, w.want)
		}
	}
}

func testFailuresConcurrent(t *testing.T, s authgate.FailureCounter) {
	t.Helper()

	const adds = 16

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		got   []int64
		fails []error
	)

	for range adds {
		wg.Go(func() {
			n, err := s.AddFailure(t.Context(), "192.0.2.1", failureWindow())

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				fails = append(fails, err)

				return
			}

			got = append(got, n)
		})
	}

	wg.Wait()

	if len(fails) > 0 {
		t.Fatalf("concurrent adds: %v", fails)
	}

	slices.Sort(got)

	for i, n := range got {
		if n != int64(i+1) {
			t.Fatalf("concurrent adds answered %v, want each count from 1 to %d once", got, adds)
		}
	}

	if n, err := s.Failures(t.Context(), "192.0.2.1", failureWindow()); err != nil || n != adds {
		t.Fatalf("failures after concurrent adds = %d, %v, want %d", n, err, adds)
	}
}

func testFailuresPrune(t *testing.T, s authgate.FailureCounter) {
	t.Helper()

	ctx := t.Context()
	starts := []time.Time{failureWindow(), failureWindow().Add(time.Minute), failureWindow().Add(2 * time.Minute)}

	for _, start := range starts {
		for _, source := range []string{"192.0.2.1", "192.0.2.2"} {
			if _, err := s.AddFailure(ctx, source, start); err != nil {
				t.Fatalf("add failure of %s at %v: %v", source, start, err)
			}
		}
	}

	deleted, err := s.PruneFailures(ctx, starts[1])
	if err != nil || deleted != 2 {
		t.Fatalf("prune before %v = %d, %v, want 2", starts[1], deleted, err)
	}

	for i, want := range []int64{0, 1, 1} {
		if got, err := s.Failures(ctx, "192.0.2.1", starts[i]); err != nil || got != want {
			t.Fatalf("failures at %v after the prune = %d, %v, want %d", starts[i], got, err, want)
		}
	}

	if deleted, err := s.PruneFailures(ctx, starts[1]); err != nil || deleted != 0 {
		t.Fatalf("second prune = %d, %v, want 0", deleted, err)
	}
}
