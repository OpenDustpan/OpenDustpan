package blob

import (
	"context"
	"errors"
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
// stops when ctx ends and returns the last error. It does not retry an error
// that a retry cannot change: a key outside the scope of a view
// (ErrOutOfScope), a key that is not valid (ErrInvalidKey) and an ended
// context.
func DeleteRetried(ctx context.Context, s Store, key Key) error {
	var err error

	for _, wait := range append(deleteBackoff(), 0) {
		if err = s.Delete(ctx, key); err == nil {
			return nil
		}

		if wait == 0 || permanent(err) {
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

// permanent reports whether err is an error that the same call returns again,
// so a retry only waits.
func permanent(err error) bool {
	return errors.Is(err, ErrOutOfScope) || errors.Is(err, ErrInvalidKey) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
