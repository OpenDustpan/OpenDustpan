package durable_test

import (
	"math"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

func TestBackoffDelay(t *testing.T) {
	t.Parallel()

	b := durable.Backoff{Base: time.Second, Max: 30 * time.Second}
	low := func() float64 { return 0 }
	high := func() float64 { return 0.999999 }

	cases := []struct {
		attempt int
		want    time.Duration // the delay before jitter
	}{
		{0, time.Second},
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{5, 16 * time.Second},
		{6, 30 * time.Second},
		{1000, 30 * time.Second},
	}

	for _, tc := range cases {
		gotLow := b.Delay(tc.attempt, low)
		gotHigh := b.Delay(tc.attempt, high)

		if gotLow != tc.want/2 {
			t.Errorf("attempt %d: lowest delay is %v, want %v", tc.attempt, gotLow, tc.want/2)
		}

		if gotHigh > tc.want || gotHigh < tc.want-time.Millisecond {
			t.Errorf("attempt %d: highest delay is %v, want just below %v", tc.attempt, gotHigh, tc.want)
		}
	}
}

func TestBackoffDelayGrowsAndStaysInBounds(t *testing.T) {
	t.Parallel()

	b := durable.Backoff{Base: 100 * time.Millisecond, Max: 5 * time.Second}
	mid := func() float64 { return 0.5 }

	prev := time.Duration(0)

	for attempt := 1; attempt <= 20; attempt++ {
		d := b.Delay(attempt, mid)
		if d < prev {
			t.Fatalf("attempt %d: delay %v is less than the delay %v before it", attempt, d, prev)
		}

		if d > b.Max {
			t.Fatalf("attempt %d: delay %v is more than the cap %v", attempt, d, b.Max)
		}

		prev = d
	}
}

func TestBackoffDelayZeroPolicy(t *testing.T) {
	t.Parallel()

	one := func() float64 { return 0.5 }

	for _, b := range []durable.Backoff{{Base: 0, Max: 0}, {Base: time.Second, Max: 0}, {Base: 0, Max: time.Second}, {Base: -1, Max: time.Second}} {
		if d := b.Delay(3, one); d != 0 {
			t.Errorf("Backoff%+v.Delay = %v, want 0", b, d)
		}
	}
}

func TestBackoffDelayDoesNotOverflow(t *testing.T) {
	t.Parallel()

	b := durable.Backoff{Base: time.Hour, Max: time.Duration(math.MaxInt64)}
	low := func() float64 { return 0 }

	for attempt := 1; attempt <= 200; attempt++ {
		if d := b.Delay(attempt, low); d < 0 {
			t.Fatalf("attempt %d: delay %v is negative", attempt, d)
		}
	}

	if d := b.Delay(200, low); d < b.Max/2-time.Second {
		t.Fatalf("attempt 200: delay %v, want about half of the cap %v", d, b.Max)
	}
}
