package storetest

import (
	"errors"
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

	u := started(t, f.create(t, f.ref, id, mutating()))
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

// stateAtOf returns the expected answer of a case.
func stateAtOf(ended time.Time, version, running int) stack.StateAt {
	return stack.StateAt{EndedAt: ended, Version: version, Running: running}
}

type stateAtCase struct {
	name    string
	at      time.Time
	want    stack.StateAt
	wantErr error
}

func checkStateAt(t *testing.T, f stateAtFixture, id stack.ID, cases []stateAtCase) {
	t.Helper()

	for _, tc := range cases {
		got, err := f.s.StateAt(t.Context(), id, tc.at)
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)

			continue
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
		{
			name:    "before the first end",
			at:      end1.Add(-ms),
			want:    stateAtOf(end1, 0, 0),
			wantErr: stack.ErrNoStateAt,
		},
		{name: "at the first end", at: end1, want: stateAtOf(end1, 1, 0), wantErr: nil},
		{
			name:    "between the two",
			at:      end1.Add(time.Minute),
			want:    stateAtOf(end1, 1, 0),
			wantErr: nil,
		},
		{
			name:    "just before the second end",
			at:      end2.Add(-ms),
			want:    stateAtOf(end1, 1, 2),
			wantErr: nil,
		},
		{name: "at the second end", at: end2, want: stateAtOf(end2, 2, 0), wantErr: nil},
		{
			name:    "after an update with no checkpoint",
			at:      base.Add(time.Hour),
			want:    stateAtOf(end2, 2, 0),
			wantErr: nil,
		},
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
		{
			name:    "before the running update",
			at:      startTwo.Add(-time.Millisecond),
			want:    stateAtOf(end1, 1, 0),
			wantErr: nil,
		},
		{
			name:    "while it runs",
			at:      startTwo.Add(time.Minute),
			want:    stateAtOf(end1, 1, 2),
			wantErr: nil,
		},
		{
			name:    "after its start, far in the future",
			at:      base.Add(24 * time.Hour),
			want:    stateAtOf(end1, 1, 2),
			wantErr: nil,
		},
	})
}

// testStateAtNoCheckpoint checks the answer for a new stack and for a stack
// whose only update has no checkpoint.
func testStateAtNoCheckpoint(t *testing.T, f stateAtFixture) {
	t.Helper()

	base := stateAtBase()
	id := stateAtID(t, f)

	checkStateAt(t, f, id, []stateAtCase{
		{name: "new stack", at: base, want: stateAtOf(time.Time{}, 0, 0), wantErr: stack.ErrNoStateAt},
	})

	u := f.startAt(t, "u1", base, "")
	f.endAt(t, u, base.Add(time.Minute))

	checkStateAt(t, f, id, []stateAtCase{
		{
			name:    "update with no checkpoint",
			at:      base.Add(time.Hour),
			want:    stateAtOf(time.Time{}, 0, 0),
			wantErr: stack.ErrNoStateAt,
		},
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

	checkStateAt(t, f, id, []stateAtCase{{name: "before the delete", at: at, want: want, wantErr: nil}})

	err := f.s.DeleteStack(
		t.Context(), f.ref, f.fence, countedGuard(one, body),
		inventory.Deletion{Entry: deleteEntry(f.fence.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	checkStateAt(t, f, id, []stateAtCase{{name: "after the delete", at: at, want: want, wantErr: nil}})
}

func testStateAtUnknownStack(t *testing.T, f stateAtFixture) {
	t.Helper()

	checkStateAt(t, f, "stk-unknown", []stateAtCase{
		{name: "unknown stack", at: stateAtBase(), want: stateAtOf(time.Time{}, 0, 0), wantErr: stack.ErrNotFound},
	})
}
