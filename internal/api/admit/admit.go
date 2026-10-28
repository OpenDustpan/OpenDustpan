// Package admit holds the worker check that a route runs before it starts a
// durable operation. A durable operation runs only in a process that has the
// worker component. With no worker, the operation waits with no end, so the
// route refuses the start with a text that names the command that helps.
package admit

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoWorker reports a request that starts a durable operation while no
// worker claimed or renewed a claim for 2 lease periods of the worker. The
// request answers 503.
var ErrNoWorker = errors.New("no worker claimed work")

// Message is the answer to a request that ErrNoWorker refuses. It names the
// command that helps.
const Message = "no worker claimed or renewed a claim for 2 lease periods: " +
	"start a worker with opendustpan serve --components worker"

// Source tells a route whether a worker runs.
type Source interface {
	// AdmitStart answers nil when a request may start a durable operation,
	// and an error wrapping ErrNoWorker when no worker claimed or renewed a
	// claim for 2 lease periods of the worker.
	AdmitStart(ctx context.Context) error
}

// Start answers nil when a route may start a durable operation. A nil source
// admits every start: a server with no durable store has no operation to
// start. An error of src that wraps ErrNoWorker is the refusal.
func Start(ctx context.Context, src Source) error {
	if src == nil {
		return nil
	}

	if err := src.AdmitStart(ctx); err != nil {
		return fmt.Errorf("admit start: %w", err)
	}

	return nil
}
