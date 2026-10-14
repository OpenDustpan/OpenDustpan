package tfe

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/bodygate"
)

// hold reserves the bytes of one large body. It answers 503 with Retry-After
// when they do not fit. On success the caller runs the returned release once
// the body is served.
func (h *Handler) hold(w http.ResponseWriter, r *http.Request, n int64) (func(), bool) {
	if release, ok := bodygate.Hold(h.gate, n); ok {
		return release, true
	}

	h.writeBusy(w, r)

	return nil, false
}

// writeBusy answers a large body that arrived while the gate is full.
func (h *Handler) writeBusy(w http.ResponseWriter, r *http.Request) {
	bodygate.SetRetryAfter(w)
	h.writeJSONAPIError(
		r.Context(),
		w,
		http.StatusServiceUnavailable,
		"service unavailable",
		"server is busy: try again in one second",
	)
}
