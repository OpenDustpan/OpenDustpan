// Package bodygate bounds concurrent large request bodies by bytes in
// flight. A large body reserves its share before the server reads it and
// releases it when served. A body that does not fit answers 503, so a burst
// of large requests waits instead of exhausting memory.
package bodygate

import (
	"net/http"
	"strings"
	"sync"
)

const (
	// DefaultMaxInFlight bounds concurrent large bodies by bytes held at
	// once unless configured otherwise.
	DefaultMaxInFlight = 128 << 20
	// RetryAfterSeconds is the Retry-After delay answered with a 503 when
	// the gate is full.
	RetryAfterSeconds = "1"
	// StreamCost is what a body that streams reserves: the fixed cost of
	// its buffers (the copy buffers, the seal chunk and the decoder floor
	// of the streaming meta reader), not its limit.
	StreamCost = 4 << 20
)

// gzipExpansion is the size factor a gzip body reserves over its wire
// length: JSON checkpoints and secrets compress well, and the decoder holds
// the decompressed form.
const gzipExpansion = 8

// Gate bounds concurrent large bodies by bytes in flight. A large body
// reserves its share before it is read and releases it when served. A body
// that does not fit answers 503, so a burst of large requests waits instead
// of exhausting memory.
type Gate struct {
	mu      sync.Mutex
	current int64
	max     int64
}

// New returns a gate with capacity n. Zero or less means DefaultMaxInFlight.
func New(n int64) *Gate {
	if n <= 0 {
		n = DefaultMaxInFlight
	}

	return &Gate{mu: sync.Mutex{}, current: 0, max: n}
}

// Acquire reserves n bytes. It reports false when they do not fit and never
// blocks.
func (g *Gate) Acquire(n int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.current+n > g.max {
		return false
	}

	g.current += n

	return true
}

// Release frees n bytes. The count never goes below zero.
func (g *Gate) Release(n int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.current -= n
	if g.current < 0 {
		g.current = 0
	}
}

// InFlight reports the bytes currently reserved, for tests and metrics.
func (g *Gate) InFlight() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.current
}

// Reservation returns the bytes one body reserves: its declared length when
// the request carries one (times gzipExpansion for a gzip body), capped by
// limit, else limit. A small body therefore holds a small share of the gate.
func Reservation(r *http.Request, limit int64) int64 {
	if r.ContentLength <= 0 {
		return limit
	}

	n := r.ContentLength
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Encoding")), "gzip") {
		n *= gzipExpansion
	}

	return min(n, limit)
}

// Hold reserves n bytes of the gate. It reports false when they do not fit.
// On success the caller runs the returned release once the body is served.
// The release func frees once; a second call does nothing.
//
//nolint:nonamedreturns // The names document the release-once contract: release frees the hold, ok reports the fit.
func Hold(g *Gate, n int64) (release func(), ok bool) {
	if !g.Acquire(n) {
		return nil, false
	}

	var once sync.Once

	return func() {
		once.Do(func() { g.Release(n) })
	}, true
}

// SetRetryAfter sets the Retry-After header of the 503 that a body answers
// when the gate is full. The adapter writes the status and the error body in
// its own shape.
func SetRetryAfter(w http.ResponseWriter) {
	w.Header().Set("Retry-After", RetryAfterSeconds)
}
