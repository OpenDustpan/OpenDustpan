package issuer

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Route declares one endpoint: the shared row (its ServeMux pattern, its
// scope, the actions it needs, its resolver and its handler).
type Route = route.Route[identity.Object]

const (
	issuerPath     = v1Prefix + "/namespaces/{namespace}/issuer"
	keysPath       = issuerPath + "/keys"
	rotatePath     = keysPath + "/rotate"
	checkCopyPath  = keysPath + "/check-copy"
	exportPath     = issuerPath + "/export"
	discoveryPath  = oidcPrefix + "/{nsid}/.well-known/openid-configuration"
	jwksPath       = oidcPrefix + "/{nsid}/jwks"
	merge          = "application/merge-patch+json"
	jsonType       = "application/json"
	maxPatchBytes  = 16 << 10
	maxRotateBytes = 4 << 10
)

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces. The
// two routes under /oidc/ are the only routes without a sign-in: they answer
// public data of the issuer, outside the versioned API.
func (h *Handler) buildRoutes() []Route {
	read := []identity.Action{identity.ActionNamespaceRead}
	manage := []identity.Action{identity.ActionNamespaceManageIssuer}

	return []Route{
		{
			Method:  http.MethodGet,
			Path:    discoveryPath,
			Scope:   route.ScopePublic,
			Actions: nil,
			Resolve: nil,
			Serve:   h.discovery,
		},
		{Method: http.MethodGet, Path: jwksPath, Scope: route.ScopePublic, Actions: nil, Resolve: nil, Serve: h.jwks},
		authorized(http.MethodGet, issuerPath, read, h.get),
		authorized(http.MethodPatch, issuerPath, manage, h.patch),
		authorized(http.MethodPost, keysPath, manage, h.createKeys),
		authorized(http.MethodPost, rotatePath, manage, h.rotate),
		authorized(http.MethodPost, checkCopyPath, manage, h.checkCopy),
		authorized(http.MethodGet, exportPath, manage, h.export),
	}
}

type serveFunc = func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object)

// authorized makes one row that needs a sign-in and the actions on the
// namespace of the path.
func authorized(method, path string, actions []identity.Action, serve serveFunc) Route {
	return Route{
		Method: method, Path: path, Scope: route.ScopeAuthorized, Actions: actions, Resolve: resolveNamespace,
		Serve: serve,
	}
}

// Routes returns a copy of the route table.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, rt := range h.routes {
		out[i] = rt
		out[i].Actions = append([]identity.Action(nil), rt.Actions...)
	}

	return out
}

// RegisteredPatterns returns a copy of every ServeMux pattern the handler
// serves, in registration order. The route test compares this with the route
// table in both directions.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}

// kit returns the route steps that this API shares with the other
// tool-neutral APIs.
func (h *Handler) kit() route.Guard {
	return route.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}
}

// enforce wraps one route table row in the shared wrapper: it authenticates,
// resolves the object, authorizes every declared action on it, and only then
// runs the handler.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	return route.Enforce(h.kit(), h.writeError, route.StepsOf(h.kit(), rt, route.Self))
}
