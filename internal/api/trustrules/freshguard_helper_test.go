package trustrules_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/freshstep"
)

// errNoFreshStep is the refusal of the guard of noFreshStepGuard.
var errNoFreshStep = errors.New("the test gives no fresh step")

// noFreshStepGuard returns the fresh step guard of the setting
// freshAuthEnabled off: it asks for no step.
func noFreshStepGuard(tb testing.TB) freshstep.Guard {
	tb.Helper()

	g, err := freshstep.NewGuard(freshstep.PolicyFunc(func(context.Context) bool { return false }),
		func(context.Context, string) error { return errNoFreshStep })
	if err != nil {
		tb.Fatalf("fresh step guard: %v", err)
	}

	return g
}
