package unitimport

import (
	"fmt"
	"slices"
)

// stateMoves is the transition table of an import: for each state, the
// states that it can move to. A state with no entry is final. A new dry run
// moves a planned import back to StatePlanning, and a resume of a holding
// import moves it to StateRunning again.
var stateMoves = map[State][]State{ //nolint:gochecknoglobals // Frozen table, like job.Next.
	StatePlanning: {StatePlanned, StateFailed, StateCanceled},
	StatePlanned:  {StatePlanning, StateRunning, StateCanceled},
	StateRunning:  {StatePaused, StateHolding, StateFailed, StateCanceled},
	StatePaused:   {StateRunning, StateCanceled},
	StateHolding:  {StateRunning, StateReleased, StateCanceled},
	StateFailed:   {StateCanceled},
}

// unitMoves is the transition table of an import unit.
var unitMoves = map[UnitState][]UnitState{ //nolint:gochecknoglobals // Frozen table, like job.Next.
	UnitPlanned:  {UnitSkipped, UnitRunning, UnitCanceled},
	UnitSkipped:  {UnitCanceled},
	UnitRunning:  {UnitPaused, UnitImported, UnitFailed, UnitCanceled},
	UnitPaused:   {UnitRunning, UnitFailed, UnitCanceled},
	UnitImported: {UnitRunning, UnitCanceled},
	UnitFailed:   {UnitRunning, UnitCanceled},
}

// Valid reports whether s is a state of an import.
func (s State) Valid() bool {
	switch s {
	case StatePlanning, StatePlanned, StateRunning, StatePaused,
		StateHolding, StateReleased, StateFailed, StateCanceled:
		return true
	}

	return false
}

// Ended reports whether the import ended: it was released or canceled.
func (s State) Ended() bool { return s == StateReleased || s == StateCanceled }

// Valid reports whether s is a state of an import unit.
func (s UnitState) Valid() bool {
	switch s {
	case UnitPlanned, UnitSkipped, UnitRunning, UnitPaused,
		UnitImported, UnitFailed, UnitCanceled:
		return true
	}

	return false
}

// NextState checks a change of state of an import. It returns nil when the
// transition table allows the move, and an error that
// wraps ErrTransition if not.
func NextState(from, to State) error {
	if slices.Contains(stateMoves[from], to) {
		return nil
	}

	return fmt.Errorf("%w: from %q to %q", ErrTransition, from, to)
}

// NextUnitState checks a change of state of an import unit in the same way
// as NextState.
func NextUnitState(from, to UnitState) error {
	if slices.Contains(unitMoves[from], to) {
		return nil
	}

	return fmt.Errorf("%w: unit from %q to %q", ErrTransition, from, to)
}
