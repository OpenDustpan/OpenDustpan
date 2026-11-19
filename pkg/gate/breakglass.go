package gate

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/identity"
)

const (
	// BreakGlassPassWindow is the time that a break-glass pass stays valid
	// after its issue.
	BreakGlassPassWindow = 15 * time.Minute

	// breakGlassReasonMax is the longest reason of a pass, in characters.
	breakGlassReasonMax = 500

	// breakGlassRefMax is the longest audit entry ID of a break-glass, in
	// characters.
	breakGlassRefMax = 128
)

// BreakGlassKey is the tuple that a pass is for: the holder, the stable ID of
// the unit and the operation kind. A store matches the whole tuple. It never
// matches a name or a prefix.
type BreakGlassKey struct {
	Holder string
	UnitID string
	Kind   OperationKind
}

// BreakGlassPass is a pass that lets one write through a gate in error. Only
// NewBreakGlassPass makes a valid pass, because the fields are unexported.
type BreakGlassPass struct {
	reason   string
	key      BreakGlassKey
	issuedAt time.Time
}

// NewBreakGlassPass returns the pass of holder for the unit and the operation
// kind, issued at at. It returns ErrInvalid when the reason is empty, longer
// than 500 characters or holds a control character, when the kind is not an
// operation kind, when the holder is empty or holds a control character, when
// unit is not a unit reference with a stable ID, or when at is the zero time.
func NewBreakGlassPass(
	reason string,
	kind OperationKind,
	holder string,
	unit identity.Object,
	at time.Time,
) (BreakGlassPass, error) {
	if err := checkBreakGlassText("the reason", reason, breakGlassReasonMax); err != nil {
		return BreakGlassPass{}, err
	}

	key, err := NewBreakGlassKey(holder, unit, kind)
	if err != nil {
		return BreakGlassPass{}, err
	}

	if at.IsZero() {
		return BreakGlassPass{}, fmt.Errorf("%w: a break-glass pass needs the time of its issue", ErrInvalid)
	}

	return BreakGlassPass{reason: reason, key: key, issuedAt: at}, nil
}

// NewBreakGlassKey returns the key of holder, the stable ID of unit and kind.
// It returns ErrInvalid for the same inputs that NewBreakGlassPass refuses.
func NewBreakGlassKey(holder string, unit identity.Object, kind OperationKind) (BreakGlassKey, error) {
	if !validOperationKind(kind) {
		return BreakGlassKey{}, fmt.Errorf("%w: %q is not an operation kind", ErrInvalid, string(kind))
	}

	if err := checkBreakGlassText("the holder", holder, breakGlassReasonMax); err != nil {
		return BreakGlassKey{}, err
	}

	if !unit.IsUnit() || unit.UnitID() == "" {
		return BreakGlassKey{}, fmt.Errorf("%w: a break-glass pass names a unit by its stable ID", ErrInvalid)
	}

	return BreakGlassKey{Holder: holder, UnitID: unit.UnitID(), Kind: kind}, nil
}

// checkBreakGlassText refuses an empty text, a text of more than max
// characters and a text with a control character.
func checkBreakGlassText(what, s string, limit int) error {
	if s == "" {
		return fmt.Errorf("%w: %s of a break-glass pass is empty", ErrInvalid, what)
	}

	if utf8.RuneCountInString(s) > limit {
		return fmt.Errorf("%w: %s of a break-glass pass is longer than %d characters", ErrInvalid, what, limit)
	}

	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: %s of a break-glass pass is not valid UTF-8", ErrInvalid, what)
	}

	if ident.HasHidden(s) {
		return fmt.Errorf("%w: %s of a break-glass pass holds a control or hidden character", ErrInvalid, what)
	}

	return nil
}

// validOperationKind is true for each operation kind that a change can carry.
func validOperationKind(k OperationKind) bool {
	switch k {
	case OperationApply, OperationDestroy, OperationRefresh, OperationImport, OperationLock, OperationRollback,
		OperationForceWrite, OperationStateImport, OperationRename, OperationDelete, OperationUnlock,
		OperationUnlockedWrite:
		return true
	default:
		return false
	}
}

// Key returns the tuple that the pass is for.
func (p BreakGlassPass) Key() BreakGlassKey { return p.key }

// Reason returns the reason of the pass.
func (p BreakGlassPass) Reason() string { return p.reason }

// IssuedAt returns the time of the issue.
func (p BreakGlassPass) IssuedAt() time.Time { return p.issuedAt }

// ExpiresAt returns the end of the window of the pass.
func (p BreakGlassPass) ExpiresAt() time.Time { return p.issuedAt.Add(BreakGlassPassWindow) }

// ValidAt is true when at is not before the issue and is before the end of
// the window.
func (p BreakGlassPass) ValidAt(at time.Time) bool {
	return !at.Before(p.issuedAt) && at.Before(p.ExpiresAt())
}

// BreakGlassStore keeps the passes of break-glass for gates in error.
//
// A store matches the whole key and never a name or a prefix. A pass is used
// once. Each method returns ErrUnavailable when the store does not answer, and
// its other errors wrapped.
type BreakGlassStore interface {
	// Issue keeps pass. It returns ErrInvalid for a pass that
	// NewBreakGlassPass did not make.
	Issue(ctx context.Context, pass BreakGlassPass) error
	// Consume uses one pass of key that is unused and valid at now, in one
	// atomic step, and returns the pass that it used: of any number of
	// concurrent calls for one pass, exactly one returns a pass. The
	// returned pass has the key and is valid at now; the caller checks both
	// again. Consume returns ErrNotFound when no such pass exists. The error
	// is the same for a missing, a used, an expired and a foreign pass, so
	// that a caller learns nothing about the passes of others.
	Consume(ctx context.Context, key BreakGlassKey, now time.Time) (BreakGlassPass, error)
}

// unwiredBreakGlassStore is the BreakGlassStore of a Service that has none.
// It grants no pass and uses none, so a break-glass fails closed.
type unwiredBreakGlassStore struct{}

var errBreakGlassUnwired = fmt.Errorf("%w: no store keeps break-glass passes in this version", ErrUnavailable)

func (unwiredBreakGlassStore) Issue(context.Context, BreakGlassPass) error {
	return errBreakGlassUnwired
}

func (unwiredBreakGlassStore) Consume(context.Context, BreakGlassKey, time.Time) (BreakGlassPass, error) {
	return BreakGlassPass{}, errBreakGlassUnwired
}

// noBreakGlassKey is the key that a function returns with an error, or when a
// change sets no break-glass.
func noBreakGlassKey() BreakGlassKey { return BreakGlassKey{Holder: "", UnitID: "", Kind: ""} }
