package audit

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrInvalidCheckState reports a check state that the store refuses: a
// negative count of invalid scopes, or a mark without an epoch, a scope or a
// sequence that can exist.
var ErrInvalidCheckState = errors.New("audit chain check state is invalid")

// CheckMark is the first checkpoint mismatch that a check found in one scope
// in one checkpoint epoch. While it stands, no instance signs a checkpoint of
// that scope: it stops signing of that scope only. A new epoch ends it: a
// mark of an older epoch stops nothing.
type CheckMark struct {
	// At is the time on the clock of the store when the mark was set. The
	// store sets it. A caller leaves it zero.
	At time.Time
	// Scope is the scope whose checkpoint did not match.
	Scope namespace.Name
	// Sequence is the sequence of the checkpoint that did not match, or 0
	// when no sequence names it.
	Sequence int64
	// Epoch is the checkpoint epoch in which the mismatch was found.
	Epoch int64
}

// CheckState is the stored result of the scheduled chain check. The zero
// value means that no check ran and no mismatch is marked.
type CheckState struct {
	// CheckedAt is the time on the clock of the store of the last result
	// that was saved, or zero when none was.
	CheckedAt time.Time
	// Marks holds the checkpoint mismatch marks, one for each scope, in
	// the order of the scope. It is empty when no mark stands.
	Marks []CheckMark
	// Invalid is the count of scopes that did not match in the last saved
	// result. Each instance exports it as dustpan_audit_chain_invalid.
	Invalid int
}

// Standing answers the count of marks of the newest epoch that Marks holds.
// An older mark stops nothing, so it does not count. A caller that knows the
// current epoch uses StandingFrom.
func (s CheckState) Standing() int {
	var newest int64

	for _, m := range s.Marks {
		newest = max(newest, m.Epoch)
	}

	return s.StandingFrom(newest)
}

// StandingFrom answers the count of marks of epoch or of a newer epoch. A
// mark of an older epoch stops nothing, so it does not count.
func (s CheckState) StandingFrom(epoch int64) int {
	n := 0

	for _, m := range s.Marks {
		if m.Epoch >= epoch {
			n++
		}
	}

	return n
}

// CheckStore is the port of the stored check state: the result of the last
// check and one mark for each scope that did not match, written by the
// instances that check and read by every instance. They are control tables,
// so a restore keeps the live rows. Each method is one small transaction.
type CheckStore interface {
	// CheckState answers the stored state. A database without a row
	// answers the zero CheckState and no error.
	CheckState(ctx context.Context) (CheckState, error)
	// SaveCheckResult stores invalid as the count of scopes that did not
	// match and sets the time of the result to the clock of the store. It
	// keeps the marks. It returns ErrInvalidCheckState for a negative count.
	SaveCheckResult(ctx context.Context, invalid int) error
	// SaveCheckMark stores mark for its scope. The first mark of a scope in
	// an epoch stays: a later mark of the same scope in the same epoch, or
	// in an older one, changes nothing and returns no error. A mark of a
	// newer epoch replaces the older mark of that scope. A mark of one
	// scope never changes the mark of another scope.
	// It returns ErrInvalidCheckState for a mark with a negative epoch or
	// sequence, or with an empty scope.
	SaveCheckMark(ctx context.Context, mark CheckMark) error
}

// Validate returns ErrInvalidCheckState when m is not a mark that a store
// can keep.
func (m CheckMark) Validate() error {
	if m.Epoch < 0 || m.Sequence < 0 || m.Scope == "" {
		return ErrInvalidCheckState
	}

	return nil
}
