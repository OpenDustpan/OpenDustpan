package server

import "time"

// SetLostMakerRevokeTimeout sets the timeout of the revoke of the service
// tokens whose maker lost the act-as path. A test calls it before it serves a
// request, so a store that never answers ends the revoke in a short time.
func SetLostMakerRevokeTimeout(h *Handler, d time.Duration) {
	h.lostMakerTimeout = d
}
