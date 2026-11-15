// Package admit holds the worker check that a route runs before it starts a
// durable operation. A durable operation runs only in a process that has the
// worker component. With no worker, the operation waits with no end, so the
// route refuses the start with a text that names the command that helps.
package admit

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNoWorker reports a request that starts a durable operation while no
// worker claimed or renewed a claim for 2 lease periods of the worker. The
// request answers 503.
var ErrNoWorker = errors.New("no worker claimed work")

// Message is the answer to a request that ErrNoWorker refuses. It names the
// command that helps.
const Message = "no worker claimed or renewed a claim for 2 lease periods: " +
	"start a worker with opendustpan serve --components worker"

// ErrDraining reports a request that starts a durable operation while the
// instance drains: the process answers the readiness probe with 503 and takes
// no new claim, so another instance serves the start. The request answers 503.
var ErrDraining = errors.New("the instance drains")

// DrainingMessage is the answer to a request that ErrDraining refuses. It
// names the next step.
const DrainingMessage = "the instance drains: try again on another instance after the Retry-After time"

// minDrainWait is the shortest wait that a draining answer names in
// Retry-After. A drain of no length still answers the header, so a client
// always reads a number of seconds.
const minDrainWait = time.Second

// DrainingError is an ErrDraining with the drain period of the instance. An
// instance returns it through Draining. RetryAfter reads the wait.
type DrainingError struct {
	// Wait is the drain period of the instance, which the answer names in
	// Retry-After. It is at least one second.
	Wait time.Duration
}

// Error returns the text of ErrDraining.
func (e *DrainingError) Error() string { return ErrDraining.Error() }

// Unwrap returns ErrDraining, so that errors.Is finds it.
func (e *DrainingError) Unwrap() error { return ErrDraining }

// Draining returns a DrainingError with the drain period period. A period
// below one second gives the shortest wait that a drain answer names.
func Draining(period time.Duration) error {
	return &DrainingError{Wait: max(period, minDrainWait)}
}

// RetryAfter returns the drain period that err names, and whether err holds a
// DrainingError. The wait is at least one second.
func RetryAfter(err error) (time.Duration, bool) {
	if draining, ok := errors.AsType[*DrainingError](err); ok {
		return max(draining.Wait, minDrainWait), true
	}

	return 0, false
}

// Source tells a route whether a worker runs.
type Source interface {
	// AdmitStart answers nil when a request may start a durable operation, an
	// error wrapping ErrDraining when the instance drains, and an error
	// wrapping ErrNoWorker when no worker claimed or renewed a claim for 2
	// lease periods of the worker.
	AdmitStart(ctx context.Context) error
}

// Start answers nil when a route may start a durable operation. A nil source
// admits every start: a server with no durable store has no operation to
// start. An error of src that wraps ErrDraining or ErrNoWorker is the refusal.
func Start(ctx context.Context, src Source) error {
	if src == nil {
		return nil
	}

	if err := src.AdmitStart(ctx); err != nil {
		return fmt.Errorf("admit start: %w", err)
	}

	return nil
}
