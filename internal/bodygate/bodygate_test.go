package bodygate_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/bodygate"
)

// TestAcquireRelease proves the gate bounds bytes in flight: Acquire fails
// when full and succeeds after Release.
func TestAcquireRelease(t *testing.T) {
	t.Parallel()

	g := bodygate.New(10)
	if !g.Acquire(6) {
		t.Fatal("Acquire(6) of 10 fails, want success")
	}

	if g.Acquire(5) {
		t.Fatal("Acquire(5) with 6 in flight of 10 succeeds, want failure")
	}

	if got := g.InFlight(); got != 6 {
		t.Fatalf("InFlight() = %d, want 6", got)
	}

	g.Release(6)

	if !g.Acquire(10) {
		t.Fatal("Acquire(10) after Release fails, want success")
	}

	g.Release(10)

	if got := g.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d, want 0", got)
	}
}

// TestDefaultCapacity proves zero means the 128 MiB default.
func TestDefaultCapacity(t *testing.T) {
	t.Parallel()

	if bodygate.DefaultMaxInFlight != 128<<20 {
		t.Fatalf("DefaultMaxInFlight = %d, want %d", bodygate.DefaultMaxInFlight, 128<<20)
	}

	g := bodygate.New(0)
	if !g.Acquire(128 << 20) {
		t.Fatal("Acquire(128 MiB) of the default gate fails, want success")
	}

	if g.Acquire(1) {
		t.Fatal("Acquire(1) of the full default gate succeeds, want failure")
	}

	g.Release(128 << 20)
}

// TestReleaseBelowZero proves the count never goes below zero.
func TestReleaseBelowZero(t *testing.T) {
	t.Parallel()

	g := bodygate.New(10)
	g.Release(4)

	if got := g.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d, want 0", got)
	}

	if !g.Acquire(10) {
		t.Fatal("Acquire(10) after over-release fails, want success")
	}

	g.Release(10)
}

// TestReservation proves a body reserves its declared length, scaled for
// gzip and capped by the limit, and the whole limit when the length is
// unknown.
func TestReservation(t *testing.T) {
	t.Parallel()

	const limit = 32 << 20

	tests := []struct {
		name     string
		length   int64
		encoding string
		limit    int64
		want     int64
	}{
		{name: "no length returns limit", length: 0, encoding: "", limit: limit, want: limit},
		{name: "unknown length returns limit", length: -1, encoding: "", limit: limit, want: limit},
		{name: "1 MiB with limit 32 MiB", length: 1 << 20, encoding: "", limit: limit, want: 1 << 20},
		{name: "40 MiB with limit 32 MiB caps at limit", length: 40 << 20, encoding: "", limit: limit, want: limit},
		{name: "gzip with 1 MiB reserves 8 MiB", length: 1 << 20, encoding: "gzip", limit: limit, want: 8 << 20},
		{name: "gzip with 8 MiB caps at limit", length: 8 << 20, encoding: "gzip", limit: limit, want: limit},
		{name: "gzip header in upper case", length: 1024, encoding: "GZIP", limit: 1 << 20, want: 1024 * 8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(""))
			r.ContentLength = tc.length

			if tc.encoding != "" {
				r.Header.Set("Content-Encoding", tc.encoding)
			}

			if got := bodygate.Reservation(r, tc.limit); got != tc.want {
				t.Fatalf("Reservation = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestHoldReleaseOnce proves the release func frees once and a second call
// does nothing.
func TestHoldReleaseOnce(t *testing.T) {
	t.Parallel()

	g := bodygate.New(10)

	release, ok := bodygate.Hold(g, 6)
	if !ok {
		t.Fatal("Hold(6) of 10 fails, want success")
	}

	if _, ok := bodygate.Hold(g, 5); ok {
		t.Fatal("Hold(5) with 6 in flight of 10 succeeds, want failure")
	}

	release()
	release()

	if got := g.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d after double release, want 0", got)
	}

	if _, ok := bodygate.Hold(g, 10); !ok {
		t.Fatal("Hold(10) after release fails, want success")
	}
}

// TestHoldConcurrent proves concurrent Hold and release pairs never exceed
// the max and end at zero.
func TestHoldConcurrent(t *testing.T) {
	t.Parallel()

	const capacity = 10

	g := bodygate.New(capacity)

	var current atomic.Int64

	var peak atomic.Int64

	var wg sync.WaitGroup

	for range 64 {
		wg.Go(func() {
			hammerGate(g, &current, &peak)
		})
	}

	wg.Wait()

	if got := peak.Load(); got > capacity {
		t.Fatalf("peak in flight = %d, want at most %d", got, capacity)
	}

	if got := g.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d, want 0", got)
	}
}

// hammerGate runs Hold and release pairs, tracking the peak in flight.
func hammerGate(g *bodygate.Gate, current, peak *atomic.Int64) {
	for range 1000 {
		release, ok := bodygate.Hold(g, 1)
		if !ok {
			continue
		}

		notePeak(peak, current.Add(1))
		current.Add(-1)
		release()
	}
}

// notePeak records n when it exceeds the peak so far.
func notePeak(peak *atomic.Int64, n int64) {
	for {
		old := peak.Load()
		if n <= old || peak.CompareAndSwap(old, n) {
			return
		}
	}
}

// TestStreamReservation proves a streaming body reserves its declared length
// capped at StreamCost, and StreamCost when the length is unknown or empty.
func TestStreamReservation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		length int64
		want   int64
	}{
		{name: "length 1 KiB returns 1 KiB", length: 1 << 10, want: 1 << 10},
		{name: "length 100 MiB returns StreamCost", length: 100 << 20, want: bodygate.StreamCost},
		{name: "unknown length returns StreamCost", length: -1, want: bodygate.StreamCost},
		{name: "length 0 returns StreamCost", length: 0, want: bodygate.StreamCost},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(""))
			r.ContentLength = tc.length

			if got := bodygate.StreamReservation(r); got != tc.want {
				t.Fatalf("StreamReservation = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSetRetryAfter proves the helper sets the Retry-After header and nothing
// else: the adapter writes the status and the body.
func TestSetRetryAfter(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	bodygate.SetRetryAfter(rec)

	if got := rec.Header().Get("Retry-After"); got != bodygate.RetryAfterSeconds {
		t.Fatalf("Retry-After = %q, want %q", got, bodygate.RetryAfterSeconds)
	}

	if rec.Body.Len() != 0 {
		t.Fatalf("body holds %d bytes, want none", rec.Body.Len())
	}
}
