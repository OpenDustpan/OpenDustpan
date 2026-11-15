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

// ErrDraining reports a request that starts a durable operation while the
// instance drains. The request answers 503 with a Retry-After header, so the
// caller retries on another instance.
var ErrDraining = errors.New("the instance drains")

// DrainingMessage is the answer to a request that ErrDraining refuses. It
// names the retry on another instance.
const DrainingMessage = "the instance drains: retry on another instance after the Retry-After time"

// drainError carries the drain period of the refusal with ErrDraining, so
// the answer names the Retry-After wait of this drain.
type drainError struct {
	period time.Duration
}

func (e *drainError) Error() string { return ErrDraining.Error() }

// Unwrap reports ErrDraining, so errors.Is finds it.
func (e *drainError) Unwrap() error { return ErrDraining }

// Draining returns an error that wraps ErrDraining and carries period, the
// drain period of the instance, for the Retry-After header of the answer.
func Draining(period time.Duration) error {
	return fmt.Errorf("admit start: %w", &drainError{period: period})
}

// DrainRetryAfter returns the Retry-After wait that the answer to err names,
// or 0 when err wraps no drain refusal.
func DrainRetryAfter(err error) time.Duration {
	if d, ok := errors.AsType[*drainError](err); ok {
		return d.period
	}

	return 0
}

// Message is the answer to a request that ErrNoWorker refuses. It names the
// command that helps.
const Message = "no worker claimed or renewed a claim for 2 lease periods: " +
	"start a worker with opendustpan serve --components worker"

// Source tells a route whether a worker runs and whether the instance drains.
type Source interface {
	// AdmitStart answers nil when a request may start a durable operation, an
	// error wrapping ErrNoWorker when no worker claimed or renewed a claim for
	// 2 lease periods of the worker, and an error wrapping ErrDraining while
	// the instance drains.
	AdmitStart(ctx context.Context) error
}

// Start answers nil when a route may start a durable operation. A nil source
// admits every start: a server with no durable store has no operation to
// start. An error of src that wraps ErrNoWorker or ErrDraining is the refusal.
func Start(ctx context.Context, src Source) error {
	if src == nil {
		return nil
	}

	if err := src.AdmitStart(ctx); err != nil {
		return fmt.Errorf("admit start: %w", err)
	}

	return nil
}
