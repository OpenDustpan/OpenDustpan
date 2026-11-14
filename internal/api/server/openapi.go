package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/identity"
)

// openAPIPath is the route of the description of the tool-neutral REST API.
const openAPIPath = "/api/dustpan/v1/openapi.json"

// openAPICacheControl lets a client keep the document for a short time. The
// document changes with the build only, and it holds no data of a caller.
const openAPICacheControl = "public, max-age=300"

// openAPIRoutes returns the row of the description of the API. The route is
// public, so a generator can read the description without a token.
func (h *Handler) openAPIRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet, Path: openAPIPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.openAPIDocument,
		},
	}
}

// openAPIDocument answers the checked-in description. Each caller gets the
// same bytes: the handler builds nothing from the request, and the state of a
// module does not change the document. The route reads no input except the
// method and the path.
func (*Handler) openAPIDocument(w http.ResponseWriter, _ *http.Request, _ identity.Principal, _ identity.Object) {
	doc := apidesc.Document()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", openAPICacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(doc); err != nil {
		// The client has gone; there is nothing to report.
		return
	}
}
