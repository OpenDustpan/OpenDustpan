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

// CheckMark is the first checkpoint mismatch that a check found in one
// checkpoint epoch. While it stands, no instance signs a checkpoint in that
// epoch. A new epoch ends it: a mark of an older epoch stops nothing.
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
	// Mark is the checkpoint mismatch mark, or nil when none stands.
	Mark *CheckMark
	// Invalid is the count of scopes that did not match in the last saved
	// result. Each instance exports it as dustpan_audit_chain_invalid.
	Invalid int
}

// CheckStore is the port of the stored check state: one row for the
// installation, written by the instance that runs the scheduled check and
// read by every instance. It is a control table, so a restore keeps the live
// row. Each method is one small transaction.
type CheckStore interface {
	// CheckState answers the stored state. A database without a row
	// answers the zero CheckState and no error.
	CheckState(ctx context.Context) (CheckState, error)
	// SaveCheckResult stores invalid as the count of scopes that did not
	// match and sets the time of the result to the clock of the store. It
	// keeps the mark. It returns ErrInvalidCheckState for a negative count.
	SaveCheckResult(ctx context.Context, invalid int) error
	// SaveCheckMark stores mark. The first mark of an epoch stays: a later
	// mark of the same epoch, or of an older one, changes nothing and
	// returns no error. A mark of a newer epoch replaces the older mark.
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
