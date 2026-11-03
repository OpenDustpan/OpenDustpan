package gate

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// The limits of a StateChange. A change above a limit is refused, never cut:
// a list of removed addresses that the server cut would make a gate on
// state.removed pass for a removal it does not see.
const (
	// MaxRemovedAddresses is the largest count of removed addresses.
	MaxRemovedAddresses = 10000
	// MaxAddressBytes is the largest size of one resource address.
	MaxAddressBytes = 1024
	// MaxRemovedBytes is the largest total size of the removed addresses.
	MaxRemovedBytes = 128 << 10
	// MaxVersionIDBytes is the largest size of a state version ID.
	MaxVersionIDBytes = 128
)

// StateChange is what a write changes in the state of its unit, as the server
// read it from the current state and the new state. It is tool-neutral: the
// adapter of a tool computes it from its own state format. Only a rollback
// holds both states in the server, so only a rollback gives a StateChange.
// The facts state.resources and state.removed come from it, and from nothing
// else.
type StateChange struct {
	// Current is the ID of the current state version that the facts were read
	// from. The adapter writes only if the current version is still this one.
	Current string
	// Resources is the count of resources in the new state.
	Resources int64
	// Removed holds the addresses in the current state and not in the new
	// state.
	Removed []string
}

// validate checks the change for operation op. It returns ErrInvalid for a
// value that is not valid and ErrLimit for a value above a limit. It rejects
// and never repairs.
func (sc *StateChange) validate(op OperationKind) error {
	// Defense in depth: the adapter reads the state from the store and the
	// store gives valid values. This check stays so that a new adapter, or a
	// bug in one, cannot put an invented fact into a decision. It does not
	// rely on the adapter.
	if op != OperationRollback {
		return fmt.Errorf("%w: a state change is valid for the operation %q only, not %q",
			ErrInvalid, OperationRollback, op)
	}

	if sc.Current == "" || len(sc.Current) > MaxVersionIDBytes || hasControl(sc.Current) {
		return fmt.Errorf("%w: the state change names no valid current state version", ErrInvalid)
	}

	if sc.Resources < 0 {
		return fmt.Errorf("%w: the state change has a negative resource count", ErrInvalid)
	}

	if len(sc.Removed) > MaxRemovedAddresses {
		return fmt.Errorf("%w: the state change removes more than %d resources", ErrLimit, MaxRemovedAddresses)
	}

	total := 0

	for _, a := range sc.Removed {
		if a == "" || hasControl(a) {
			return fmt.Errorf("%w: the state change holds an address that is not valid", ErrInvalid)
		}

		if len(a) > MaxAddressBytes {
			return fmt.Errorf("%w: the state change holds an address above %d bytes", ErrLimit, MaxAddressBytes)
		}

		total += len(a)
	}

	if total > MaxRemovedBytes {
		return fmt.Errorf("%w: the removed addresses are above %d bytes", ErrLimit, MaxRemovedBytes)
	}

	return nil
}

// facts returns the state facts of the change, with the removed addresses
// sorted and without duplicates, in a copy.
func (sc *StateChange) facts() StateFacts {
	removed := slices.Clone(sc.Removed)
	slices.Sort(removed)

	return StateFacts{Available: true, Resources: sc.Resources, Removed: slices.Compact(removed)}
}

// hasControl reports whether s holds a control character, which includes CR
// and LF, or a character that is not valid UTF-8 text.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == unicode.ReplacementChar })
}
