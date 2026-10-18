package storetest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/stack"
)

// StateAtStore is the update cycle plus the port under test.
type StateAtStore interface {
	UpdatesStore
	stack.StateAtReader
}

// StateAtFactory returns a fresh, empty store for one subtest.
type StateAtFactory func(t *testing.T) StateAtStore

// stateAtFixture is a store holding stack "acme/web/prod".
type stateAtFixture struct {
	fixture

	s StateAtStore
}

// RunStateAt runs the conformance cases of stack.StateAtReader against
// stores produced by newStore.
func RunStateAt(t *testing.T, newStore StateAtFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, f stateAtFixture)
		name string
	}{
		{name: "state at a time follows the end of the update", test: testStateAtBoundary},
		{name: "state at a time names an update that ran then", test: testStateAtRunning},
		{name: "state at a time of a stack without a checkpoint", test: testStateAtNoCheckpoint},
		{name: "state at a time ignores a running update below the found version", test: testStateAtOlderRunning},
		{name: "state at a time ignores a preview and a dry run", test: testStateAtPreview},
		{name: "state at a time of a soft-deleted stack", test: testStateAtSoftDeleted},
		{name: "state at a time of an unknown stack is ErrNotFound", test: testStateAtUnknownStack},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore(t)
			tc.test(t, stateAtFixture{fixture: newFixture(t, s), s: s})
		})
	}
}

// stateAtBase is the start of the timeline of the state-at cases.
func stateAtBase() time.Time {
	return time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
}

// startAt starts u at the time at, with a checkpoint when body is not empty.
// The update stays running.
func (f stateAtFixture) startAt(t *testing.T, id string, at time.Time, body string) stack.Update {
	t.Helper()

	return f.startPlanAt(t, id, mutating(), at, body)
}

// startPlanAt is startAt for the update that plan describes.
func (f stateAtFixture) startPlanAt(t *testing.T, id string, plan stack.Plan, at time.Time, body string) stack.Update {
	t.Helper()

	plan.CreatedBy = "by-" + id

	u := started(t, f.create(t, f.ref, id, plan))
	u.StartedAt = at

	got, err := f.s.StartUpdate(t.Context(), u, f.fence)
	if err != nil {
		t.Fatalf("start update %s: %v", id, err)
	}

	if body != "" {
		if err := f.s.PutCheckpoint(t.Context(), newCheckpoint(got, 0, body), f.fence); err != nil {
			t.Fatalf("put checkpoint of %s: %v", id, err)
		}
	}

	return got
}

// endAt ends u at the time at.
func (f stateAtFixture) endAt(t *testing.T, u stack.Update, at time.Time) {
	t.Helper()

	u = ended(u, stack.StatusSucceeded)
	u.EndedAt = at

	if err := f.s.SaveUpdate(t.Context(), u, f.fence); err != nil {
		t.Fatalf("end update %s: %v", u.ID, err)
	}
}

func stateAtID(t *testing.T, f stateAtFixture) stack.ID {
	t.Helper()

	st, err := f.s.GetStack(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", f.ref, err)
	}

	return st.ID
}

// stateAtOf returns the expected answer of a case. Update "u<n>" has version
// n and the creator "by-u<n>".
func stateAtOf(ended time.Time, version, running int) stack.StateAt {
	return stack.StateAt{EndedAt: ended, CreatedBy: fmt.Sprintf("by-u%d", version), Version: version, Running: running}
}

// stateAtCase is one question and its answer. With wantErr ErrNoStateAt, first
// is the First of the NoStateAtError.
type stateAtCase struct {
	name    string
	at      time.Time
	first   time.Time
	want    stack.StateAt
	wantErr error
}

// noState is the answer that comes with an error.
func noState() stack.StateAt {
	return stack.StateAt{EndedAt: time.Time{}, CreatedBy: "", Version: 0, Running: 0}
}

// answers returns the case with a state and no error.
func answers(name string, at time.Time, want stack.StateAt) stateAtCase {
	return stateAtCase{name: name, at: at, first: time.Time{}, want: want, wantErr: nil}
}

// refused returns the case with an error. first is the First of a
// NoStateAtError.
func refused(name string, at, first time.Time, wantErr error) stateAtCase {
	return stateAtCase{name: name, at: at, first: first, want: noState(), wantErr: wantErr}
}

func checkStateAt(t *testing.T, f stateAtFixture, id stack.ID, cases []stateAtCase) {
	t.Helper()

	for i := range cases {
		tc := &cases[i]

		got, err := f.s.StateAt(t.Context(), id, tc.at)
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)

			continue
		}

		if errors.Is(err, stack.ErrNoStateAt) {
			var none *stack.NoStateAtError
			if !errors.As(err, &none) || !none.First.Equal(tc.first) {
				t.Errorf("%s: err = %#v, want a NoStateAtError with First %s", tc.name, err, tc.first)
			}
		}

		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// testStateAtBoundary checks that an update holds the state from its end
// time on, to the millisecond, and that an update without a checkpoint holds
// none.
func testStateAtBoundary(t *testing.T, f stateAtFixture) {
	t.Helper()

	base := stateAtBase()
	end1 := base.Add(5 * time.Minute)
	end2 := base.Add(15 * time.Minute)

	one := f.startAt(t, "u1", base, `{"v":1}`)
	f.endAt(t, one, end1)

	two := f.startAt(t, "u2", base.Add(10*time.Minute), `{"v":2}`)
	f.endAt(t, two, end2)

	three := f.startAt(t, "u3", base.Add(20*time.Minute), "")
	f.endAt(t, three, base.Add(25*time.Minute))

	ms := time.Millisecond

	checkStateAt(t, f, stateAtID(t, f), []stateAtCase{
		refused("before the first end", end1.Add(-ms), end1, stack.ErrNoStateAt),
		answers("at the first end", end1, stateAtOf(end1, 1, 0)),
		answers("between the two", end1.Add(time.Minute), stateAtOf(end1, 1, 0)),
		answers("just before the second end", end2.Add(-ms), stateAtOf(end1, 1, 2)),
		answers("at the second end", end2, stateAtOf(end2, 2, 0)),
		answers("after an update with no checkpoint", base.Add(time.Hour), stateAtOf(end2, 2, 0)),
	})
}

// testStateAtRunning checks that an update that has not ended names itself
// and does not move the state.
func testStateAtRunning(t *testing.T, f stateAtFixture) {
	t.Helper()

	base := stateAtBase()
	end1 := base.Add(5 * time.Minute)

	one := f.startAt(t, "u1", base, `{"v":1}`)
	f.endAt(t, one, end1)

	startTwo := base.Add(10 * time.Minute)
	f.startAt(t, "u2", startTwo, `{"v":2}`)

	checkStateAt(t, f, stateAtID(t, f), []stateAtCase{
		answers("before the running update", startTwo.Add(-time.Millisecond), stateAtOf(end1, 1, 0)),
		answers("while it runs", startTwo.Add(time.Minute), stateAtOf(end1, 1, 2)),
		answers("after its start, far in the future", base.Add(24*time.Hour), stateAtOf(end1, 1, 2)),
	})
}

// testStateAtOlderRunning checks that Running names only an update above the
// version that holds the state.
func testStateAtOlderRunning(t *testing.T, f stateAtFixture) {
	t.Helper()

	base := stateAtBase()
	end2 := base.Add(15 * time.Minute)

	// The recorded times of u1 overlap those of u2, as after a clock step.
	one := f.startAt(t, "u1", base, "")
	f.endAt(t, one, base.Add(30*time.Minute))

	two := f.startAt(t, "u2", base.Add(10*time.Minute), `{"v":2}`)
	f.endAt(t, two, end2)

	checkStateAt(t, f, stateAtID(t, f), []stateAtCase{
		answers("older update still running", end2.Add(time.Minute), stateAtOf(end2, 2, 0)),
	})
}

// testStateAtPreview checks that a preview and a dry run that run at the time
// do not change the state and are not named in Running.
func testStateAtPreview(t *testing.T, f stateAtFixture) {
	t.Helper()

	base := stateAtBase()
	end1 := base.Add(10 * time.Minute)

	one := f.startAt(t, "u1", base, `{"v":1}`)
	f.endAt(t, one, end1)

	preview := f.startPlanAt(t, "u2", preview(), base.Add(20*time.Minute), "")
	f.endAt(t, preview, base.Add(40*time.Minute))

	dry := f.startPlanAt(t, "u3", dryRun(), base.Add(20*time.Minute), "")
	f.endAt(t, dry, base.Add(40*time.Minute))

	checkStateAt(t, f, stateAtID(t, f), []stateAtCase{
		answers("preview and dry run over the time", base.Add(30*time.Minute), stateAtOf(end1, 1, 0)),
	})
}

// testStateAtNoCheckpoint checks the answer for a new stack and for a stack
// whose only update has no checkpoint.
func testStateAtNoCheckpoint(t *testing.T, f stateAtFixture) {
	t.Helper()

	base := stateAtBase()
	id := stateAtID(t, f)

	checkStateAt(t, f, id, []stateAtCase{
		refused("new stack", base, time.Time{}, stack.ErrNoStateAt),
	})

	u := f.startAt(t, "u1", base, "")
	f.endAt(t, u, base.Add(time.Minute))

	checkStateAt(t, f, id, []stateAtCase{
		refused("update with no checkpoint", base.Add(time.Hour), time.Time{}, stack.ErrNoStateAt),
	})
}

// testStateAtSoftDeleted checks that a stack in the retention window of a
// delete answers as before the delete.
func testStateAtSoftDeleted(t *testing.T, f stateAtFixture) {
	t.Helper()

	base := stateAtBase()
	end1 := base.Add(5 * time.Minute)
	body := `{"v":1}`

	one := f.startAt(t, "u1", base, body)
	f.endAt(t, one, end1)

	id := stateAtID(t, f)
	at := end1.Add(time.Minute)
	want := stateAtOf(end1, 1, 0)

	checkStateAt(t, f, id, []stateAtCase{answers("before the delete", at, want)})

	err := f.s.DeleteStack(
		t.Context(), f.ref, f.fence, countedGuard(one, body),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	checkStateAt(t, f, id, []stateAtCase{answers("after the delete", at, want)})
}

func testStateAtUnknownStack(t *testing.T, f stateAtFixture) {
	t.Helper()

	checkStateAt(t, f, "stk-unknown", []stateAtCase{
		refused("unknown stack", stateAtBase(), time.Time{}, stack.ErrNotFound),
	})
}
