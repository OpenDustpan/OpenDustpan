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

// StartRefusalError is a refusal of a start after the job service planned the
// job: an enforced gate, the agent mode, the policy, or the store for a
// freeze or a limit. It names the start action of the planned job and its
// unit, so that the record of the refusal names what the caller tried to
// start. Err is the cause, and the text of the error is the text of Err.
type StartRefusalError struct {
	Action identity.Action
	Object identity.Object
	Err    error
}

// Error implements error. It gives the text of Err, so a start refusal reads
// as its cause.
func (e *StartRefusalError) Error() string {
	if e.Err == nil {
		return "the start of the job is refused"
	}

	return e.Err.Error()
}

// Unwrap returns Err.
func (e *StartRefusalError) Unwrap() error {
	return e.Err
}

// startRefusal returns err as a *StartRefusalError that names the start
// action of spec on its unit. It returns err as it is when spec has no start
// action.
func startRefusal(spec *Spec, err error) error {
	action, actionErr := startActionFor(spec.Executor, spec.Operation, spec.Purpose)
	if actionErr != nil {
		return err
	}

	return &StartRefusalError{Action: action, Object: spec.Unit, Err: err}
}
