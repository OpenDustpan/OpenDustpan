package pulumi

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/bodygate"
)

// DefaultMaxBodyBytesInFlight bounds concurrent large bodies by bytes held
// at once unless configured otherwise.
const DefaultMaxBodyBytesInFlight = bodygate.DefaultMaxInFlight

// hold reserves the bytes of one large body. It answers 503 with Retry-After
// when they do not fit. On success the caller runs the returned release once
// the body is served.
func (h *Handler) hold(w http.ResponseWriter, r *http.Request, limit int64) (func(), bool) {
	n := bodygate.Reservation(r, limit)
	if release, ok := bodygate.Hold(h.gate, n); ok {
		return release, true
	}

	h.writeBusy(w, r)

	return nil, false
}

// writeBusy answers a large body that arrived while the gate is full.
func (h *Handler) writeBusy(w http.ResponseWriter, r *http.Request) {
	bodygate.SetRetryAfter(w)
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
