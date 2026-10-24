package tfe

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/errcode"
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

// holdLease reserves the bytes of one large body that can grow while it
// streams. It answers 503 with Retry-After when they do not fit. On success
// the caller releases the lease once the body is served.
func (h *Handler) holdLease(w http.ResponseWriter, r *http.Request, n int64) (*bodygate.Lease, bool) {
	lease, ok := bodygate.NewLease(h.gate, n)
	if !ok {
		h.writeBusy(w, r)

		return nil, false
	}

	return lease, true
}

// writeBusy answers a large body that arrived while the gate is full.
func (h *Handler) writeBusy(w http.ResponseWriter, r *http.Request) {
	bodygate.SetRetryAfter(w)
	h.writeClassError(r.Context(), w, errorClass{
		status: http.StatusServiceUnavailable, title: titleServiceUnavailable,
		detail: "server is busy: try again in one second", code: errcode.StateVersionBusy, args: nil,
	})
}
