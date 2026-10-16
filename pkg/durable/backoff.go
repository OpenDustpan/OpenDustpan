package durable

import "time"

// halves splits the capped delay: jitter keeps the upper half.
const halves = 2

// Backoff is the retry policy for a failed attempt: capped exponential
// growth with jitter.
type Backoff struct {
	// Base is the delay after the first failed attempt, before jitter.
	Base time.Duration
	// Max caps the delay before jitter.
	Max time.Duration
}

// Delay returns the wait before the claim that follows failed attempt
// number attempt (1 for the first attempt). The delay before jitter is
// Base * 2^(attempt-1), capped at Max. Jitter takes a value from the upper
// half: the result is in [d/2, d], where d is the capped delay. rnd returns a
// number in [0, 1). An attempt below 1 counts as 1. A Base or Max that is not
// positive gives 0.
func (b Backoff) Delay(attempt int, rnd func() float64) time.Duration {
	if b.Base <= 0 || b.Max <= 0 {
		return 0
	}

	d := b.Base
	for i := 1; i < attempt && d < b.Max; i++ {
		d *= 2
	}

	d = min(d, b.Max)

	half := d / halves

	return half + time.Duration(rnd()*float64(d-half))
}
