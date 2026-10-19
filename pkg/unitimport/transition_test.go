package unitimport_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/unitimport"
)

func TestNextStateMatrix(t *testing.T) {
	t.Parallel()

	const (
		planning = unitimport.StatePlanning
		planned  = unitimport.StatePlanned
		running  = unitimport.StateRunning
		paused   = unitimport.StatePaused
		holding  = unitimport.StateHolding
		released = unitimport.StateReleased
		failed   = unitimport.StateFailed
		canceled = unitimport.StateCanceled
	)

	states := []unitimport.State{planning, planned, running, paused, holding, released, failed, canceled}

	allowed := map[unitimport.State][]unitimport.State{
		planning: {planned, paused, failed, canceled},
		planned:  {planning, running, canceled},
		running:  {paused, holding, failed, canceled},
		paused:   {planning, running, canceled},
		holding:  {running, released, canceled},
		released: {},
		failed:   {canceled},
		canceled: {},
	}

	for _, from := range append(states, "unknown") {
		for _, to := range append(states, "unknown") {
			want := false

			for _, ok := range allowed[from] {
				want = want || ok == to
			}

			err := unitimport.NextState(from, to)

			switch {
			case want && err != nil:
				t.Errorf("NextState(%q, %q) = %v, want nil", from, to, err)
			case !want && !errors.Is(err, unitimport.ErrTransition):
				t.Errorf("NextState(%q, %q) = %v, want ErrTransition", from, to, err)
			default:
			}
		}
	}
}

func TestNextUnitStateMatrix(t *testing.T) {
	t.Parallel()

	const (
		planned  = unitimport.UnitPlanned
		skipped  = unitimport.UnitSkipped
		running  = unitimport.UnitRunning
		paused   = unitimport.UnitPaused
		imported = unitimport.UnitImported
		failed   = unitimport.UnitFailed
		canceled = unitimport.UnitCanceled
	)

	states := []unitimport.UnitState{planned, skipped, running, paused, imported, failed, canceled}

	allowed := map[unitimport.UnitState][]unitimport.UnitState{
		planned:  {skipped, running, canceled},
		skipped:  {canceled},
		running:  {paused, imported, failed, canceled},
		paused:   {running, failed, canceled},
		imported: {running, canceled},
		failed:   {running, canceled},
		canceled: {},
	}

	for _, from := range append(states, "unknown") {
		for _, to := range append(states, "unknown") {
			want := false

			for _, ok := range allowed[from] {
				want = want || ok == to
			}

			err := unitimport.NextUnitState(from, to)

			switch {
			case want && err != nil:
				t.Errorf("NextUnitState(%q, %q) = %v, want nil", from, to, err)
			case !want && !errors.Is(err, unitimport.ErrTransition):
				t.Errorf("NextUnitState(%q, %q) = %v, want ErrTransition", from, to, err)
			default:
			}
		}
	}
}

func TestCheckUnitMove(t *testing.T) {
	t.Parallel()

	resume := unitimport.UnitMove{From: unitimport.UnitFailed, To: unitimport.UnitRunning, Code: ""}

	cases := []struct {
		name  string
		state unitimport.UnitState
		code  string
		move  unitimport.UnitMove
		want  bool
	}{
		{"resume of a failed unit", unitimport.UnitFailed, "import.source-unreachable", resume, true},
		{
			"resume of a unit with a changed source history",
			unitimport.UnitFailed,
			unitimport.CodeSourceHistoryChanged,
			resume,
			false,
		},
		{
			"cancel of a unit with a changed source history",
			unitimport.UnitFailed,
			unitimport.CodeSourceHistoryChanged,
			unitimport.UnitMove{From: unitimport.UnitFailed, To: unitimport.UnitCanceled, Code: ""},
			true,
		},
		{"a unit in another state than from", unitimport.UnitPaused, "", resume, false},
	}

	for _, tc := range cases {
		err := unitimport.CheckUnitMove(tc.state, tc.code, tc.move)

		switch {
		case tc.want && err != nil:
			t.Errorf("%s: error = %v, want nil", tc.name, err)
		case !tc.want && !errors.Is(err, unitimport.ErrTransition):
			t.Errorf("%s: error = %v, want ErrTransition", tc.name, err)
		default:
		}
	}
}
