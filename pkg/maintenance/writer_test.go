package maintenance_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/stack"
)

// errClaimGone is the error of a writer whose claim is lost.
var errClaimGone = errors.New("claim is lost")

// fakeWriter is a StateWriter over the fake store of a fixture. It counts
// its writes and checks, and fails them when lost is true.
type fakeWriter struct {
	store  *fakeStateStore
	lost   bool
	writes int
	checks int
}

func (w *fakeWriter) SetStateIf(ctx context.Context, expected, next maintenance.State) error {
	if w.lost {
		return errClaimGone
	}

	w.writes++

	return w.store.SetStateIf(ctx, expected, next)
}

func (w *fakeWriter) Check(context.Context, string) error {
	w.checks++

	if w.lost {
		return errClaimGone
	}

	return nil
}

func (*fakeWriter) Actor() string { return "restore rst-a token 2" }

func newWriter(f *fixture) *fakeWriter {
	return &fakeWriter{store: f.state, lost: false, writes: 0, checks: 0}
}

func TestEnterWithWritesThroughTheWriter(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	w := newWriter(f)
	ctx := t.Context()

	if _, err := f.service.EnterWith(ctx, maintenance.EnterOptions{Force: false, Writer: w}); err != nil {
		t.Fatalf("enter with a writer: %v", err)
	}

	if w.writes != 1 || f.state.state != maintenance.StateMaintenance {
		t.Fatalf("after the enter: %d writes, state %s, want 1 write and maintenance", w.writes, f.state.state)
	}

	// A second enter finds maintenance and writes nothing.
	plain := maintenance.EnterOptions{Force: false, Writer: w}
	if _, err := f.service.EnterWith(ctx, plain); err != nil || w.writes != 1 {
		t.Fatalf("second enter: %v, %d writes, want no new write", err, w.writes)
	}

	if err := f.service.LeaveWith(ctx, w); err != nil || w.writes != 2 || f.state.state != maintenance.StateNormal {
		t.Fatalf("leave: %v, %d writes, state %s, want 2 writes and normal", err, w.writes, f.state.state)
	}

	// A leave that finds normal writes nothing.
	if err := f.service.LeaveWith(ctx, w); err != nil || w.writes != 2 {
		t.Fatalf("second leave: %v, %d writes, want no new write", err, w.writes)
	}
}

func TestEnterWithALostClaimChangesNothing(t *testing.T) {
	t.Parallel()

	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "force"}[force], func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			seedRunningUpdate(t, f, mustRef(t))

			w := newWriter(f)
			w.lost = true

			opts := maintenance.EnterOptions{Force: force, Writer: w}
			if _, err := f.service.EnterWith(t.Context(), opts); err == nil {
				t.Fatal("enter with a lost claim: no error, want one")
			}

			// Defense in depth: the claim check before the cancel stops the
			// cancel, and the write refuses the state change on its own.
			if f.state.state != maintenance.StateNormal {
				t.Fatalf("state after an enter with a lost claim = %s, want normal", f.state.state)
			}

			if u := f.updates.updates["u1"]; u.Status == stack.StatusCanceled {
				t.Fatal("an enter with a lost claim canceled the update")
			}
		})
	}
}

func TestEnterWithChecksTheClaimBeforeEachCancel(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	seedRunningUpdate(t, f, mustRef(t))

	w := newWriter(f)

	res, err := f.service.EnterWith(t.Context(), maintenance.EnterOptions{Force: true, Writer: w})
	if err != nil {
		t.Fatalf("force enter with a writer: %v", err)
	}

	if w.checks != 1 || len(res.Canceled) != 1 {
		t.Fatalf("force enter: %d checks, canceled %v, want one check before the one cancel", w.checks, res.Canceled)
	}
}

func TestLeaveWithALostClaimKeepsMaintenance(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	w := newWriter(f)

	if _, err := f.service.EnterWith(t.Context(), maintenance.EnterOptions{Force: false, Writer: w}); err != nil {
		t.Fatalf("enter: %v", err)
	}

	w.lost = true

	if err := f.service.LeaveWith(t.Context(), w); !errors.Is(err, errClaimGone) {
		t.Fatalf("leave with a lost claim: error = %v, want the claim error", err)
	}

	if f.state.state != maintenance.StateMaintenance {
		t.Fatalf("state after a leave with a lost claim = %s, want maintenance", f.state.state)
	}
}

func TestRecheckWith(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		active   bool
		force    bool
		wantErr  error
		canceled int
		state    maintenance.State
	}{
		{
			name: "no active work", active: false, force: false, wantErr: nil, canceled: 0,
			state: maintenance.StateMaintenance,
		},
		{
			name: "active work with force", active: true, force: true, wantErr: nil, canceled: 1,
			state: maintenance.StateMaintenance,
		},
		{
			name: "active work without force", active: true, force: false, wantErr: maintenance.ErrActiveWork,
			canceled: 0, state: maintenance.StateNormal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			if tc.active {
				seedRunningUpdate(t, f, mustRef(t))
			}

			// A holder entered maintenance mode and stopped before its
			// confirm. Work raced its write.
			f.state.state = maintenance.StateMaintenance

			res, err := f.service.RecheckWith(
				t.Context(),
				maintenance.EnterOptions{Force: tc.force, Writer: newWriter(f)},
			)
			if !errors.Is(err, tc.wantErr) || len(res.Canceled) != tc.canceled || f.state.state != tc.state {
				t.Fatalf("recheck = %+v, %v, state %s; want %d canceled, error %v, state %s",
					res, err, f.state.state, tc.canceled, tc.wantErr, tc.state)
			}
		})
	}
}
