package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrFreezeActive reports a create of a change freeze on a namespace that
// already has an active freeze. The first freeze stays unchanged. The
// store checks it in the transaction of the create.
var ErrFreezeActive = errors.New("a change freeze of the namespace is already active")

// ErrNoActiveFreeze reports an end of a freeze on a namespace with no active
// freeze.
var ErrNoActiveFreeze = errors.New(
	"no change freeze is active in the namespace; list the freezes with GET /api/dustpan/v1/namespaces/{namespace}/freezes",
)

// CheckReason checks the reason of a freeze or of a break-glass start. It
// returns ErrInvalid for a reason of fewer than 1 or more than 500 characters,
// or for text that the audit log refuses.
func CheckReason(reason string) error {
	if n := []rune(reason); len(n) < 1 || len(n) > audit.MaxReasonLen {
		return fmt.Errorf("%w: the reason of a freeze has 1 to %d characters", ErrInvalid, audit.MaxReasonLen)
	}

	if err := audit.ValidateReason(reason); err != nil {
		return fmt.Errorf("%w: reason: %w", ErrInvalid, err)
	}

	return nil
}

// ErrNoFreezeStore reports a freeze gate that has no store to read. The gate
// fails closed: it never reads a missing store as "no freeze".
var ErrNoFreezeStore = errors.New("no freeze store: the freeze gate cannot read the freezes of the namespace")

// freezeGate holds the active freeze of one namespace at one time. Only
// NewFreezeGate makes one.
type freezeGate struct {
	active Freeze
	found  bool
}

// NewFreezeGate loads the active freeze of ns through freezes at the time at.
// It returns ErrNoFreezeStore for a nil freezes.
func NewFreezeGate(ctx context.Context, freezes FreezeStore, ns namespace.Name, at time.Time) (*freezeGate, error) {
	if freezes == nil {
		return nil, ErrNoFreezeStore
	}

	freezesOfNS, err := freezes.ListFreezes(ctx, ns, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("list freezes: %w", err)
	}

	for i := range freezesOfNS {
		if freezesOfNS[i].ActiveAt(at) {
			return &freezeGate{active: freezesOfNS[i], found: true}, nil
		}
	}

	return &freezeGate{active: noFreeze(), found: false}, nil
}

func noFreeze() Freeze {
	return Freeze{
		ID: "", Namespace: "", Reason: "", StartsAt: time.Time{}, EndsAt: time.Time{}, CreatedBy: "", EndedBy: "",
	}
}

// Active returns the active freeze of the gate.
func (g *freezeGate) Active() (Freeze, bool) {
	return g.active, g.found
}

// Allow refuses a phase that changes the unit when the gate holds an active
// freeze and the job has no break-glass.
func (g *freezeGate) Allow(kind PhaseKind, bg *BreakGlass) error {
	if !g.found || bg != nil || !kind.Changes() {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrFrozen, g.active.ID)
}
