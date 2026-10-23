package inventory

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrUnknownState reports a string that is not the wire value of a unit
// state. The match is exact: the case, the spaces and the separators of the
// value count.
var ErrUnknownState = errors.New("unknown unit state")

// State is one state of a unit, as the inventory shows it to a person. A
// unit can have several at once, for example locked and drifted: a list shows
// all of them and picks no winner. The values are the stable wire strings.
// They are tool-neutral. A page of one tool shows the tool's own words.
//
// Only locked is served so far, and the server derives it at read time. The
// values are fixed. A change to a value, or to the word set, needs a new
// decision.
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
	// StateFailed means that the latest change to end failed: an apply or destroy
	// job, or a Pulumi CLI update.
	StateFailed State = "failed"
	// StateUnknown means that the drift state of the unit is not known.
	StateUnknown State = "unknown"
)

// States returns the unit states in the order of the badges: failed,
// awaiting-approval, apply-needed, drifted, running, queued, locked, unknown,
// in-sync. The order picks no winner. This function is the one place that
// holds the order. It returns a new slice on each call.
func States() []State {
	return []State{
		StateFailed, StateAwaitingApproval, StateApplyNeeded, StateDrifted,
		StateRunning, StateQueued, StateLocked, StateUnknown, StateInSync,
	}
}

// Valid reports whether s is a unit state. An empty string is not one.
func (s State) Valid() bool {
	return slices.Contains(States(), s)
}

// ParseState returns the unit state whose wire value is raw. The match is
// exact: it does not trim, fold case or change a separator. A value that is
// not a state gives an error that wraps ErrUnknownState and names the valid
// values.
func ParseState(raw string) (State, error) {
	s := State(raw)
	if !s.Valid() {
		return "", fmt.Errorf("%w %q: use one of %s", ErrUnknownState, raw, ValidStateList())
	}

	return s, nil
}

// ValidStateList returns the wire values in the order of the badges, joined
// with a comma and a space. An error text that names the valid values uses it,
// so the list has one place.
func ValidStateList() string {
	states := States()
	names := make([]string, len(states))

	for i, s := range states {
		names[i] = string(s)
	}

	return strings.Join(names, ", ")
}

// StateSince is one state that a unit has, and when it began.
type StateSince struct {
	Since time.Time
	State State
}

// since returns when the state s began for the unit e, and whether e has the
// state. This function is the one place that derives a state from an entry.
// Only locked is derived so far. No other state is stored yet, so an entry
// has none of them.
func (e Entry) since(s State) (time.Time, bool) {
	if s != StateLocked {
		return time.Time{}, false
	}

	switch {
	case e.Lock != nil && e.ManualLock != nil:
		return earlier(e.Lock.Since, e.ManualLock.Since), true
	case e.Lock != nil:
		return e.Lock.Since, true
	case e.ManualLock != nil:
		return e.ManualLock.Since, true
	default:
		return time.Time{}, false
	}
}

// derived reports whether since can find the state s in an entry. A store
// that filters on the states in its own query keeps this list and since in
// step.
func derived(s State) bool {
	return s == StateLocked
}

// earlier returns the earlier of two times.
func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}

	return a
}

// HasState reports whether the unit e has the state s. The answer comes from
// the fields of e alone, never from a second read.
func (e Entry) HasState(s State) bool {
	_, ok := e.since(s)

	return ok
}

// StatesOf returns the states of the unit e in the order of the badges. A unit
// with no state that needs attention gives an empty list that is not nil. The
// lock is a flag, so a locked unit keeps every other state.
func (e Entry) StatesOf() []StateSince {
	out := []StateSince{}

	for _, s := range States() {
		if since, ok := e.since(s); ok {
			out = append(out, StateSince{Since: since, State: s})
		}
	}

	return out
}
