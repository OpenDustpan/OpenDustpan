package audit

import "net/http"

// Enforce exposes the enforcement wrapper, so that a test can give it a row
// that the route table never holds.
func (h *Handler) Enforce(route Route) http.HandlerFunc { return h.enforce(route) }
