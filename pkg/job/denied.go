package job

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
)

// DeniedError is the refusal of a start by the authorizer: it names the
// action and the object that the authorizer refused. Err is the error of the
// authorizer, which wraps identity.ErrDenied. So errors.Is(err,
// identity.ErrDenied) holds for a DeniedError, also with a nil Err.
type DeniedError struct {
	Action identity.Action
	Object identity.Object
	Err    error
}

// Error implements error.
func (e *DeniedError) Error() string {
	return fmt.Sprintf("%s: %s on %s", e.Unwrap(), e.Action, e.Object.String())
}

// Unwrap returns Err, or identity.ErrDenied when Err is nil.
func (e *DeniedError) Unwrap() error {
	if e.Err == nil {
		return identity.ErrDenied
	}

	return e.Err
}
