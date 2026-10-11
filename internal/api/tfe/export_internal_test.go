package tfe

import (
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// testPublic serves the public test route.
func (h *Handler) testPublic(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	h.writeJSON(r.Context(), w, http.StatusOK, map[string]string{"status": "ok"})
}

// testAuthorized serves the authorized test routes after the wrapper has
// enforced the row.
func (h *Handler) testAuthorized(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	h.writeJSON(r.Context(), w, http.StatusOK, map[string]string{"status": "ok"})
}

// ExtraRoutes returns test-only routes that prove the registration
// wrapper. They live in test code so the production table stays free of
// test paths. The route test runs every check on them and on the real table.
func ExtraRoutes(h *Handler) []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/v2/test-public",
			Scope:   ScopePublic,
			Actions: nil,
			Resolve: nil,
			Serve:   h.testPublic,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/v2/test-by-name/{organization}/{workspace}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveWorkspace,
			Serve:   h.testAuthorized,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/v2/test-by-id/{id}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.testAuthorized,
		},
	}
}

// NewHandlerWithRoutes returns a Handler with the production routes plus the
// extra routes that extra builds. Only tests reach it: it lives in a _test.go
// file, so production builds never include it.
func NewHandlerWithRoutes(cfg Config, extra func(*Handler) []Route) (*Handler, error) {
	h, err := NewHandler(cfg)
	if err != nil {
		return nil, err
	}

	if extra == nil {
		return h, nil
	}

	routes := extra(h)
	for _, route := range routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	h.routes = append(h.routes, routes...)

	return h, nil
}
