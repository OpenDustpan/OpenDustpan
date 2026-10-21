package conformance

import (
	"context"
	"errors"
	"time"
)

// DefaultCaseTimeout bounds one case.
const DefaultCaseTimeout = 2 * time.Minute

// Status is the outcome of one case.
type Status int

// The outcomes of a case.
const (
	// StatusPassed means that the case ran and the client did what it expects.
	StatusPassed Status = iota + 1
	// StatusFailed means that the client broke an expectation, or that the
	// harness or the adapter failed.
	StatusFailed
	// StatusNotImplemented means that the case has no body. It fails a release.
	StatusNotImplemented
	// StatusNotApplicable means that the client does not cover an operation that
	// the case needs.
	StatusNotApplicable
)

// String implements fmt.Stringer.
func (s Status) String() string {
	switch s {
	case StatusPassed:
		return "passed"
	case StatusFailed:
		return "failed"
	case StatusNotImplemented:
		return "not implemented"
	case StatusNotApplicable:
		return "not applicable"
	default:
		return "unknown"
	}
}

// Result is the outcome of one case.
type Result struct {
	Group  Group
	Name   string
	Status Status
	// Err is the reason of a failure, or of a case that does not apply.
	Err error
}

// Run runs the cases one after the other, and returns one result for each. It
// resets the proxy before each case. A case that fails does not stop the run,
// except when the adapter breaks the protocol: the run then fails every case
// that is left, because the session is lost.
func Run(ctx context.Context, env Env, cases []Case, timeout time.Duration) []Result {
	if timeout <= 0 {
		timeout = DefaultCaseTimeout
	}

	env.app = new(appFixture)

	results := make([]Result, 0, len(cases))

	var lost error

	for _, c := range cases {
		r := Result{Group: c.Group, Name: c.Name, Status: StatusPassed, Err: nil}

		switch {
		case c.Run == nil:
			r.Status = StatusNotImplemented
		case lost != nil:
			r.Status, r.Err = StatusFailed, lost
		default:
			env.Proxy.Reset()

			caseCtx, cancel := context.WithTimeout(ctx, timeout)
			err := c.Run(caseCtx, env)

			cancel()

			r.Status, r.Err = classify(err)
			if errors.Is(err, ErrProtocol) || errors.Is(err, ErrAdapterExited) {
				lost = err
			}
		}

		results = append(results, r)
	}

	return results
}

// classify turns the error of a case into a status.
func classify(err error) (Status, error) {
	switch {
	case err == nil:
		return StatusPassed, nil
	case errors.Is(err, ErrNotApplicable):
		return StatusNotApplicable, err
	default:
		return StatusFailed, err
	}
}
