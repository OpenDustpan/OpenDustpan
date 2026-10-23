package postgres

import (
	"testing"
	"time"
)

// TestSwapJitterStaysInItsRange proves that the wait between two attempts of
// the swap lock is from half of the backoff to the backoff, at each step of
// the backoff.
func TestSwapJitterStaysInItsRange(t *testing.T) {
	t.Parallel()

	for _, backoff := range []time.Duration{0, time.Nanosecond, swapLockBackoffStart, swapLockBackoffMax} {
		half := backoff / backoffJitterDivisor

		for range 200 {
			got := swapJitter(backoff)
			if got < half || got > half+half {
				t.Fatalf("swapJitter(%s) = %s, want from %s to %s", backoff, got, half, half+half)
			}
		}
	}
}
