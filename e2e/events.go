//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/stack"
)

// Limits of the read of stored events: the history updates of one stack, and
// the events of one update. A scenario stays far below both.
const (
	storedUpdatesLimit = 100
	storedEventsLimit  = 1000
)

// ErrNeedsInProcess reports a read of stored rows on a binary server, which
// holds its database open.
var ErrNeedsInProcess = errors.New("this read needs the in-process server")

// StoredEventPayloads returns the stored payload of each event of the history
// updates of ref, as the event store keeps it at rest. It reads the store of
// the in-process server, and returns ErrNeedsInProcess on a binary server.
func (e *Env) StoredEventPayloads(ctx context.Context, ref stack.Ref) ([][]byte, error) {
	if e.binPath != "" || e.inprocess == nil {
		return nil, ErrNeedsInProcess
	}

	updates, err := e.inprocess.ListUpdates(ctx, ref, stack.Page{Limit: storedUpdatesLimit, Offset: 0})
	if err != nil {
		return nil, fmt.Errorf("updates of %s: %w", ref, err)
	}

	var out [][]byte

	for i := range updates {
		id := updates[i].ID

		events, err := e.inprocess.ListEvents(ctx, ref, id, -1, storedEventsLimit)
		if err != nil {
			return nil, fmt.Errorf("events of update %s: %w", id, err)
		}

		for _, ev := range events {
			out = append(out, ev.Payload)
		}
	}

	return out, nil
}

// InProcess reports whether the server of e runs in the test process.
func (e *Env) InProcess() bool {
	return e.binPath == "" && e.inprocess != nil
}
