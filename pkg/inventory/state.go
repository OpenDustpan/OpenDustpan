package inventory

import "slices"

// State is one state of a unit, as the inventory shows it to a person. A
// unit can have several at once, for example locked and drifted: a list shows
// all of them and picks no winner. The values are the stable wire strings.
// They are tool-neutral. A page of one tool shows the tool's own words.
//
// No code stores or serves a State yet. The spelling of every value except
// StateAwaitingApproval is proposed and can change.
type State string

// The unit states.
const (
	// StateInSync means that the latest drift check found no difference.
	StateInSync State = "in-sync"
	// StateRunning means that a job of the unit runs.
	StateRunning State = "running"
	// StateQueued means that a job of the unit waits to start.
	StateQueued State = "queued"
	// StateLocked means that a lock or a manual lock covers the unit. A lock
	// is a flag, so the flag stays the source of this state.
	StateLocked State = "locked"
	// StateAwaitingApproval means that a job of the unit waits for an
	// approval. The human word is "approval wait".
	StateAwaitingApproval State = "awaiting-approval"
	// StateApplyNeeded means that a change is ready and not applied.
	StateApplyNeeded State = "apply-needed"
	// StateDrifted means that the latest drift check found a difference.
	StateDrifted State = "drifted"
	// StateFailed means that the latest change job of the unit failed.
	StateFailed State = "failed"
	// StateUnknown means that the drift state of the unit is not known.
	StateUnknown State = "unknown"
)

// States returns the unit states, in the vocabulary order of the owner's
// decision. It returns a new slice on each call.
func States() []State {
	return []State{
		StateInSync, StateRunning, StateQueued, StateLocked,
		StateAwaitingApproval, StateApplyNeeded, StateDrifted, StateFailed,
		StateUnknown,
	}
}

// Valid reports whether s is a unit state. An empty string is not one.
func (s State) Valid() bool {
	return slices.Contains(States(), s)
}
