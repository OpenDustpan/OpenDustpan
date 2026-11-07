package job_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// edge is one allowed transition.
type edge struct {
	from job.State
	ev   job.Event
	to   job.State
}

// allowed is the state machine, written out apart from the code under test.
// Each line is a rule that a reviewer can read on its own.
//
//nolint:gochecknoglobals // A fixed table, read only.
var allowed = []edge{
	{job.StateQueued, job.EventDispatch, job.StateDispatched},
	{job.StateQueued, job.EventNeedApproval, job.StateAwaitingApproval},
	{job.StateQueued, job.EventCancel, job.StateCanceled},
	{job.StateQueued, job.EventExpire, job.StateExpired},
	{job.StateQueued, job.EventReplan, job.StateQueued},

	{job.StateAwaitingApproval, job.EventApproved, job.StateQueued},
	{job.StateAwaitingApproval, job.EventApprovalRecorded, job.StateAwaitingApproval},
	{job.StateAwaitingApproval, job.EventRejected, job.StateRejected},
	{job.StateAwaitingApproval, job.EventCancel, job.StateCanceled},
	{job.StateAwaitingApproval, job.EventExpire, job.StateExpired},

	{job.StateDispatched, job.EventClaim, job.StateRunning},
	{job.StateDispatched, job.EventCancel, job.StateCanceled},
	{job.StateDispatched, job.EventExpire, job.StateExpired},
	{job.StateDispatched, job.EventPhaseFailed, job.StateFailed},

	{job.StateRunning, job.EventClaim, job.StateRunning},
	{job.StateRunning, job.EventRelease, job.StateDispatched},
	{job.StateRunning, job.EventPhaseDone, job.StateQueued},
	{job.StateRunning, job.EventLastPhaseDone, job.StateSucceeded},
	{job.StateRunning, job.EventDelivered, job.StateAwaitingState},
	{job.StateRunning, job.EventPhaseFailed, job.StateFailed},
	{job.StateRunning, job.EventCancel, job.StateCanceling},

	{job.StateCanceling, job.EventRelease, job.StateCanceled},
	{job.StateCanceling, job.EventPhaseFailed, job.StateFailed},
	{job.StateCanceling, job.EventPhaseDone, job.StateCanceled},
	{job.StateCanceling, job.EventLastPhaseDone, job.StateSucceeded},
	{job.StateCanceling, job.EventDelivered, job.StateAwaitingState},
	{job.StateCanceling, job.EventCancel, job.StateCanceling},

	{job.StateAwaitingState, job.EventMerged, job.StateAwaitingState},
	{job.StateAwaitingState, job.EventStateObserved, job.StateSucceeded},
	{job.StateAwaitingState, job.EventRejected, job.StateRejected},
	{job.StateAwaitingState, job.EventCancel, job.StateCanceled},
	{job.StateAwaitingState, job.EventExpire, job.StateExpired},
}

func TestNextAllowsExactlyTheTable(t *testing.T) {
	t.Parallel()

	want := make(map[job.State]map[job.Event]job.State)
	for _, e := range allowed {
		if want[e.from] == nil {
			want[e.from] = make(map[job.Event]job.State)
		}

		want[e.from][e.ev] = e.to
	}

	for _, from := range job.States() {
		for _, ev := range job.Events() {
			t.Run(string(from)+"/"+string(ev), func(t *testing.T) {
				t.Parallel()

				if to, ok := want[from][ev]; ok {
					checkAllowed(t, from, ev, to)
				} else {
					checkRefused(t, from, ev)
				}
			})
		}
	}
}

// TestApprovalRecordedOnlyFromAwaitingApproval is the hostile case of the
// event that records one approval: every other state refuses it.
func TestApprovalRecordedOnlyFromAwaitingApproval(t *testing.T) {
	t.Parallel()

	for _, from := range job.States() {
		if from == job.StateAwaitingApproval {
			checkAllowed(t, from, job.EventApprovalRecorded, job.StateAwaitingApproval)

			continue
		}

		checkRefused(t, from, job.EventApprovalRecorded)
	}
}

// checkAllowed fails t when Next(from, ev) does not give to.
func checkAllowed(t *testing.T, from job.State, ev job.Event, to job.State) {
	t.Helper()

	if got, err := job.Next(from, ev); err != nil || got != to {
		t.Fatalf("Next(%s, %s) = %s, %v, want %s", from, ev, got, err, to)
	}
}

// checkRefused fails t when Next(from, ev) does not refuse the event with
// the right error and keep the state.
func checkRefused(t *testing.T, from job.State, ev job.Event) {
	t.Helper()

	got, err := job.Next(from, ev)

	want := job.ErrTransition
	if from.Terminal() {
		want = job.ErrFinished
	}

	if !errors.Is(err, want) || got != from {
		t.Fatalf("Next(%s, %s) = %s, %v, want %s and %v", from, ev, got, err, from, want)
	}
}

func TestEveryOpenStateCanBeCanceled(t *testing.T) {
	t.Parallel()

	for _, s := range job.States() {
		if s.Terminal() {
			continue
		}

		to, err := job.Next(s, job.EventCancel)
		if err != nil {
			t.Errorf("Next(%s, cancel) = %v, want a state", s, err)

			continue
		}

		if to != job.StateCanceled && to != job.StateCanceling {
			t.Errorf("Next(%s, cancel) = %s, want canceled or canceling", s, to)
		}
	}
}

func TestEveryStateIsReachableFromQueued(t *testing.T) {
	t.Parallel()

	seen := map[job.State]bool{job.StateQueued: true}
	todo := []job.State{job.StateQueued}

	for len(todo) > 0 {
		from := todo[0]
		todo = todo[1:]

		for _, ev := range job.Events() {
			to, err := job.Next(from, ev)
			if err == nil && !seen[to] {
				seen[to] = true
				todo = append(todo, to)
			}
		}
	}

	for _, s := range job.States() {
		if !seen[s] {
			t.Errorf("state %s cannot be reached from queued", s)
		}
	}
}

func TestEveryOpenStateCanEnd(t *testing.T) {
	t.Parallel()

	for _, s := range job.States() {
		if s.Terminal() {
			continue
		}

		if !canEnd(s, map[job.State]bool{}) {
			t.Errorf("state %s has no path to a terminal state", s)
		}
	}
}

// canEnd reports whether a terminal state can be reached from s.
func canEnd(s job.State, seen map[job.State]bool) bool {
	if s.Terminal() {
		return true
	}

	seen[s] = true

	for _, ev := range job.Events() {
		to, err := job.Next(s, ev)
		if err == nil && !seen[to] && canEnd(to, seen) {
			return true
		}
	}

	return false
}

func TestHoldsUnit(t *testing.T) {
	t.Parallel()

	holds := map[job.State]bool{
		job.StateDispatched: true,
		job.StateRunning:    true,
		job.StateCanceling:  true,
	}

	for _, s := range job.States() {
		if got := s.HoldsUnit(); got != holds[s] {
			t.Errorf("%s.HoldsUnit() = %t, want %t", s, got, holds[s])
		}

		if s.Terminal() && s.HoldsUnit() {
			t.Errorf("terminal state %s holds the unit", s)
		}
	}
}
