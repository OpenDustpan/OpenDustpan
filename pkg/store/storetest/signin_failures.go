package storetest

import (
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
)

// SignInFailureHarness opens handles on one store that counts failed
// sign-ins. Two handles act as two instances.
type SignInFailureHarness struct {
	// Open returns a new handle on the store of the harness. skew is the
	// offset of the clock of the instance that holds the handle. A store
	// that reads the time in Go must give the handle a clock at time.Now
	// plus skew, so the skew case catches a window that comes from the
	// clock of the instance instead of the clock of the store. A store that
	// reads no time in Go can ignore skew.
	Open func(t *testing.T, skew time.Duration) authgate.FailureCounter
}

// SignInFailureFactory returns a fresh harness for one case.
type SignInFailureFactory func(t *testing.T) SignInFailureHarness

// Lengths and waits of the sign-in failure cases.
const (
	// failureLongWindow does not end more than once inside one case.
	failureLongWindow = time.Minute
	// failureShortWindow ends many times inside the edge case.
	failureShortWindow = 20 * time.Millisecond
	// failureEdgeRun is how long the edge case adds misses.
	failureEdgeRun = 150 * time.Millisecond
	// failureSkew is the clock offset of the instances in the skew case.
	failureSkew = time.Hour
	// failureAdds is the count of concurrent adds.
	failureAdds = 16
)

// RunSignInFailures runs the conformance suite of authgate.FailureCounter
// against harnesses from newHarness.
func RunSignInFailures(t *testing.T, newHarness SignInFailureFactory) {
	t.Helper()

	tests := []struct {
		test func(t *testing.T, h SignInFailureHarness)
		name string
	}{
		{name: "an add answers the count of its window", test: testFailuresCount},
		{name: "sources count apart", test: testFailuresApart},
		{name: "a window ends on the clock of the store", test: testFailuresWindowEdge},
		{name: "an instance clock ahead or behind does not move the window", test: testFailuresSkew},
		{name: "concurrent adds each count once", test: testFailuresConcurrent},
		{name: "a prune deletes old windows and keeps current ones", test: testFailuresPrune},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.test(t, newHarness(t))
		})
	}
}

// addFailure adds one miss of source and fails the test on an error or on
// an answer outside its window.
func addFailure(
	t *testing.T, s authgate.FailureCounter, source string, length time.Duration,
) authgate.FailureCount {
	t.Helper()

	got, err := s.AddFailure(t.Context(), source, length)
	if err != nil {
		t.Fatalf("add failure of %s: %v", source, err)
	}

	end := got.End.UnixMilli()
	if end%length.Milliseconds() != 0 || !got.At.Before(got.End) || got.At.Before(got.End.Add(-length)) {
		t.Fatalf("add failure of %s = %+v, want a window of %v that holds its time", source, got, length)
	}

	return got
}

// wantRuns fails the test unless the counts of each window in got, in
// order, run from 1 by one.
func wantRuns(t *testing.T, got []authgate.FailureCount) {
	t.Helper()

	next := map[int64]int64{}

	for _, c := range got {
		end := c.End.UnixMilli()

		next[end]++
		if c.Count != next[end] {
			t.Fatalf("counts %+v: want each window to count from 1 by one", got)
		}
	}
}

func testFailuresCount(t *testing.T, h SignInFailureHarness) {
	t.Helper()

	s := h.Open(t, 0)

	got := make([]authgate.FailureCount, 0, 3)
	for range 3 {
		got = append(got, addFailure(t, s, "192.0.2.1", failureLongWindow))
	}

	wantRuns(t, got)
}

func testFailuresApart(t *testing.T, h SignInFailureHarness) {
	t.Helper()

	s := h.Open(t, 0)

	first := []authgate.FailureCount{
		addFailure(t, s, "192.0.2.1", failureLongWindow),
		addFailure(t, s, "192.0.2.1", failureLongWindow),
	}
	other := []authgate.FailureCount{addFailure(t, s, "2001:db8::/64", failureLongWindow)}

	wantRuns(t, first)
	wantRuns(t, other)
}

// testFailuresWindowEdge adds misses through several short windows. Each
// add holds a time from the end of its window minus the length, inclusive,
// to the end, exclusive: an add 1 ms before the end counts in the window,
// and an add at the end counts 1 in the next window.
func testFailuresWindowEdge(t *testing.T, h SignInFailureHarness) {
	t.Helper()

	s := h.Open(t, 0)
	stop := time.Now().Add(failureEdgeRun)

	var got []authgate.FailureCount

	for time.Now().Before(stop) {
		got = append(got, addFailure(t, s, "192.0.2.1", failureShortWindow))
	}

	wantRuns(t, got)

	windows := map[int64]bool{}
	for _, c := range got {
		windows[c.End.UnixMilli()] = true
	}

	if len(windows) < 2 {
		t.Fatalf("adds over %v saw %d windows of %v, want 2 or more", failureEdgeRun, len(windows), failureShortWindow)
	}
}

func testFailuresSkew(t *testing.T, h SignInFailureHarness) {
	t.Helper()

	ahead := h.Open(t, failureSkew)
	behind := h.Open(t, -failureSkew)

	got := []authgate.FailureCount{
		addFailure(t, ahead, "192.0.2.1", failureLongWindow),
		addFailure(t, behind, "192.0.2.1", failureLongWindow),
	}

	wantRuns(t, got)

	if gap := got[1].At.Sub(got[0].At).Abs(); gap >= failureSkew {
		t.Fatalf("adds from instances %v apart are %v apart, want the clock of the store", 2*failureSkew, gap)
	}
}

func testFailuresConcurrent(t *testing.T, h SignInFailureHarness) {
	t.Helper()

	s := h.Open(t, 0)

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		got   = map[int64][]int64{}
		fails []error
	)

	for range failureAdds {
		wg.Go(func() {
			c, err := s.AddFailure(t.Context(), "192.0.2.1", failureLongWindow)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				fails = append(fails, err)

				return
			}

			got[c.End.UnixMilli()] = append(got[c.End.UnixMilli()], c.Count)
		})
	}

	wg.Wait()

	if len(fails) > 0 {
		t.Fatalf("concurrent adds: %v", fails)
	}

	for end, counts := range got {
		slices.Sort(counts)

		for i, n := range counts {
			if n != int64(i+1) {
				t.Fatalf("concurrent adds in the window that ends at %d answered %v, want each count once", end, counts)
			}
		}
	}
}

// testFailuresPrune adds one miss in a short window, waits until that window
// ended more than the prune age ago, adds misses of other sources in a long
// window that is current, and prunes. Only the old window goes.
func testFailuresPrune(t *testing.T, h SignInFailureHarness) {
	t.Helper()

	const age = 500 * time.Millisecond

	s := h.Open(t, 0)
	old := addFailure(t, s, "192.0.2.1", failureShortWindow)

	wait := time.NewTimer(time.Until(old.End.Add(age + 500*time.Millisecond)))
	defer wait.Stop()

	select {
	case <-t.Context().Done():
		t.Fatalf("wait for the window to age: %v", t.Context().Err())
	case <-wait.C:
	}

	sources := []string{"192.0.2.2", "192.0.2.3"}
	current := make([]authgate.FailureCount, 0, len(sources))

	for _, source := range sources {
		current = append(current, addFailure(t, s, source, failureLongWindow))
	}

	deleted, err := s.PruneFailures(t.Context(), age)
	if err != nil || deleted != 1 {
		t.Fatalf("prune of windows that ended %v ago = %d, %v, want 1", age, deleted, err)
	}

	for i, source := range sources {
		again := addFailure(t, s, source, failureLongWindow)
		if again.End.Equal(current[i].End) && again.Count != 2 {
			t.Fatalf("add of %s after the prune = %+v, want its current window kept", source, again)
		}
	}
}
