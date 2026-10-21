package stepup_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/stepup"
)

// TestErrorsAreDistinct checks that no sentinel matches another, because an
// adapter maps each one to its own error code.
func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []error{
		stepup.ErrNotFound, stepup.ErrExpired, stepup.ErrUsed, stepup.ErrMismatch,
		stepup.ErrBadAnswer, stepup.ErrFactorRefused, stepup.ErrCLIStepUpOff, stepup.ErrLocked,
		stepup.ErrNotPerson, stepup.ErrInvalid, stepup.ErrLimit, stepup.ErrConflict,
	}

	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v matches %v", a, b)
			}
		}
	}
}
