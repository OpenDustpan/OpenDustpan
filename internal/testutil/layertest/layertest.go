// Package layertest holds the table helper for the tests of one layer.
//
// Write one table for each layer: the port adapter, the service and the HTTP
// handler. Each guard has a hostile case at its own layer. The case calls the
// layer directly, as if every other layer had failed. A guard that only a
// test of a higher layer covers has no test of its own: when a change removes
// the guard, the test of the higher layer can still pass because another
// layer stops the input.
package layertest

import (
	"context"
	"errors"
	"testing"
)

// Case is one row of the table of a layer.
type Case[In any] struct {
	// Name names the subtest. It says what is hostile in the input.
	Name string
	// In is the input that the call receives.
	In In
	// Want is the sentinel error that the call must return. The check is
	// errors.Is. A nil Want means that the call must return no error.
	Want error
}

// Run runs each case in a parallel subtest. The subtest passes t.Context() and
// the input of the case to call. The case fails when errors.Is(err, c.Want) is
// false, or, for a nil Want, when call returns an error.
func Run[In any](t *testing.T, cases []Case[In], call func(context.Context, In) error) {
	t.Helper()

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()

			err := call(t.Context(), c.In)

			if c.Want == nil {
				if err != nil {
					t.Fatalf("error = %v, want no error", err)
				}

				return
			}

			if !errors.Is(err, c.Want) {
				t.Fatalf("error = %v, want %v", err, c.Want)
			}
		})
	}
}
