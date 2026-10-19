package unitimport

import (
	"fmt"
	"slices"
)

// stateMoves is the transition table of an import: for each state, the
// states that it can move to. A state with no entry is final. A new dry run
// moves a planned import back to StatePlanning, and a resume of a holding
// import moves it to StateRunning again. A planning import pauses when the
// check of its profile fails at the create or when a write meets the fence
// error, and a resume moves it back to StatePlanning.
var stateMoves = map[State][]State{ //nolint:gochecknoglobals // Frozen table, like job.Next.
	StatePlanning: {StatePlanned, StatePaused, StateFailed, StateCanceled},
	StatePlanned:  {StatePlanning, StateRunning, StateCanceled},
	StateRunning:  {StatePaused, StateHolding, StateFailed, StateCanceled},
	StatePaused:   {StatePlanning, StateRunning, StateCanceled},
	StateHolding:  {StateRunning, StateReleased, StateCanceled},
	StateFailed:   {StateCanceled},
}

// unitMoves is the transition table of an import unit. The table decides by
// state only. A unit that failed with the code CodeSourceHistoryChanged
// never moves to UnitRunning again, because the source holds a version with
// another digest than the ledger. CheckUnitMove encodes that rule, and the
// stores use it for each change of a unit.
var unitMoves = map[UnitState][]UnitState{ //nolint:gochecknoglobals // Frozen table, like job.Next.
	UnitPlanned:  {UnitSkipped, UnitRunning, UnitCanceled},
	UnitSkipped:  {UnitCanceled},
	UnitRunning:  {UnitPaused, UnitImported, UnitFailed, UnitCanceled},
	UnitPaused:   {UnitRunning, UnitFailed, UnitCanceled},
	UnitImported: {UnitRunning, UnitCanceled},
	UnitFailed:   {UnitRunning, UnitCanceled},
}

// CodeSourceHistoryChanged is the error code of a unit that failed because
// the source holds a version with another digest than the ledger.
const CodeSourceHistoryChanged = "import.source-history-changed"

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

// CheckUnitMove checks the change m of an import unit. The unit is in the
// state cur and holds the error code code. It applies the transition table, and it
// refuses a move of a unit that failed with CodeSourceHistoryChanged to
// UnitRunning. The error wraps ErrTransition.
func CheckUnitMove(cur UnitState, code string, m UnitMove) error {
	if cur != m.From {
		return fmt.Errorf("%w: unit is %q, not %q", ErrTransition, cur, m.From)
	}

	if err := NextUnitState(m.From, m.To); err != nil {
		return err
	}

	if m.From == UnitFailed && m.To == UnitRunning && code == CodeSourceHistoryChanged {
		return fmt.Errorf("%w: a unit with the code %q cannot resume", ErrTransition, code)
	}

	return nil
}
