package blob

import (
	"context"
	"fmt"
	"time"
)

// deleteBackoff holds the waits between the attempts of DeleteRetried: a
// transient object store failure gets four attempts over about two seconds.
func deleteBackoff() []time.Duration {
	return []time.Duration{100 * time.Millisecond, 400 * time.Millisecond, 1600 * time.Millisecond}
}

// DeleteRetried removes the object under key from s, and retries a failed
// delete after a short backoff. Deleting a missing key is not an error. It
// stops when ctx ends and returns the last error.
func DeleteRetried(ctx context.Context, s Store, key Key) error {
	var err error

	for _, wait := range append(deleteBackoff(), 0) {
		if err = s.Delete(ctx, key); err == nil {
			return nil
		}

		if wait == 0 {
			break
		}

		timer := time.NewTimer(wait)

		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf("delete blob: %w", err)
		case <-timer.C:
		}
	}

	return fmt.Errorf("delete blob: %w", err)
}
