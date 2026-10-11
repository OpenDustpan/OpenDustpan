package pulumi

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	// DefaultMaxBodyBytesInFlight bounds concurrent large bodies by bytes
	// held at once unless configured otherwise.
	DefaultMaxBodyBytesInFlight = 128 << 20
	// retryAfterSeconds is the Retry-After delay answered with a 503 when the
	// gate is full.
	retryAfterSeconds = "1"
)

// bodyGate bounds concurrent large bodies by bytes in flight. A large body
// reserves its limit before it is read and releases it when served. A body
// that does not fit answers 503, so a burst of large requests waits instead
// of exhausting memory.
type bodyGate struct {
	mu      sync.Mutex
	current int64
	max     int64
}

// newBodyGate returns a gate with capacity n. Zero means the default.
func newBodyGate(n int64) *bodyGate {
	if n <= 0 {
		n = DefaultMaxBodyBytesInFlight
	}

	return &bodyGate{mu: sync.Mutex{}, current: 0, max: n}
}

// acquire reserves n bytes. It reports false when they do not fit.
func (g *bodyGate) acquire(n int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.current+n > g.max {
		return false
	}

	g.current += n

	return true
}

// release frees n bytes.
func (g *bodyGate) release(n int64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.current -= n
	if g.current < 0 {
		g.current = 0
	}
}

// gzipExpansion is the size factor a gzip body reserves over its wire
// length: JSON checkpoints and secrets compress well, and the decoder holds
// the decompressed form.
const gzipExpansion = 8

// reservation returns the bytes one body reserves: its declared length when
// the request carries one (times gzipExpansion for a gzip body), capped by
// limit, else limit. A small body therefore holds a small share of the gate.
func reservation(r *http.Request, limit int64) int64 {
	if r.ContentLength <= 0 {
		return limit
	}

	n := r.ContentLength
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Encoding")), "gzip") {
		n *= gzipExpansion
	}

	return min(n, limit)
}

// hold reserves the bytes of one large body. It answers 503 with Retry-After
// when they do not fit. On success the caller runs the returned release once
// the body is served.
func (h *Handler) hold(w http.ResponseWriter, r *http.Request, limit int64) (func(), bool) {
	n := reservation(r, limit)
	if h.gate.acquire(n) {
		return func() { h.gate.release(n) }, true
	}

	h.writeBusy(w, r)

	return nil, false
}

// writeBusy answers a large body that arrived while the gate is full.
func (h *Handler) writeBusy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", retryAfterSeconds)
	h.writeJSON(r.Context(), w, http.StatusServiceUnavailable, errorResponse{
		Code:    http.StatusServiceUnavailable,
		Message: "server is busy: try again in one second",
	})
}

// countingReader counts bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)

	return n, err //nolint:wrapcheck // Transparent wrapper: callers must see io.EOF unchanged.
}

// largeReader returns a reader for one large body that streams gzip
// decompression without keeping both forms. The wire is bounded by limit and
// the decompressed stream by limit too, so a gzip bomb cannot exhaust memory.
// The caller decodes from counted and checks counted.n against limit after.
func (h *Handler) largeReader(
	w http.ResponseWriter, r *http.Request, limit int64,
) (*countingReader, func(), bool) {
	wire := http.MaxBytesReader(w, r.Body, limit)

	if !strings.Contains(strings.ToLower(r.Header.Get("Content-Encoding")), "gzip") {
		return &countingReader{r: io.LimitReader(wire, limit+1), n: 0}, func() {}, true
	}

	gz, err := gzip.NewReader(wire)
	if err != nil {
		h.writeInvalidBody(r.Context(), w)

		return nil, nil, false
	}

	release := func() {
		_ = gz.Close() //nolint:errcheck // Close after a full read reports nothing actionable.
	}

	return &countingReader{r: io.LimitReader(gz, limit+1), n: 0}, release, true
}

// decodeLargeBody decodes one large JSON body without keeping both its wire
// and decompressed forms. It gunzips when the client sent gzip, bounds the
// decompressed size by limit, and maps an oversized body to 413 and invalid
// JSON to 400.
func (h *Handler) decodeLargeBody(w http.ResponseWriter, r *http.Request, limit int64, out any) bool {
	ctx := r.Context()

	counted, release, ok := h.largeReader(w, r, limit)
	if !ok {
		return false
	}

	defer release()

	dec := json.NewDecoder(counted)
	if err := dec.Decode(out); err != nil {
		if counted.n > limit {
			h.writeTooLarge(w, r, limit)
		} else if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok && tooLarge != nil {
			h.writeTooLarge(w, r, limit)
		} else {
			h.writeInvalidBody(ctx, w)
		}

		return false
	}

	if counted.n > limit {
		h.writeTooLarge(w, r, limit)

		return false
	}

	// The whole body must be one JSON value, as Unmarshal requires.
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		h.writeInvalidBody(ctx, w)

		return false
	}

	return true
}
