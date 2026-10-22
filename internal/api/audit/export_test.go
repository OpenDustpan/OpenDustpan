package audit //nolint:testpackage // The route wrapper is unexported: a test gives it a row that the route table never holds.

import "net/http"

// ServeRoute returns the enforcement wrapper of route.
func (h *Handler) ServeRoute(rt Route) http.HandlerFunc { return h.enforce(rt) }
