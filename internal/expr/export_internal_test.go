package expr

import "time"

// ErrWork is errWork, for the tests of the black-box package expr_test.
var ErrWork = errWork

// NewWithTimeout returns an Evaluator with the time limit d. The black-box
// tests use a high d to prove that the work budget ends a run.
func NewWithTimeout(l *Limiter, d time.Duration) (*Evaluator, error) {
	e, err := New(l)
	if err != nil {
		return nil, err
	}

	e.timeout = d

	return e, nil
}
