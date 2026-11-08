package principal

import (
	"context"
	"time"
)

// The floors of the break-glass accounts of an installation.
const (
	// FloorDisable is the lowest number of enabled break-glass accounts that
	// a disable, a delete or a sweep may leave. A change that leaves no
	// enabled break-glass account is refused.
	FloorDisable = 1
	// FloorWorkingPaths is the lowest number of working break-glass accounts
	// that a change may leave when it lowers the count. A change that does
	// not lower the count is allowed, even below the floor, so that an
	// installation can recover.
	FloorWorkingPaths = 2
)

// WorkingBreakGlassCounter counts the working break-glass accounts.
type WorkingBreakGlassCounter interface {
	// CountWorkingBreakGlass returns the number of working break-glass
	// accounts at the time at. It returns ErrBreakGlassUnavailable when the
	// store cannot count.
	//
	// An installation-level IP allowlist must join this count when one
	// exists. The current allowlists are per namespace and do not apply to
	// installation sign-in.
	CountWorkingBreakGlass(ctx context.Context, at time.Time) (int64, error)
}
