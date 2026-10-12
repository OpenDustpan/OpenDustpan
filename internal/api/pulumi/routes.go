package pulumi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// Scope names how a route is protected. Every route declares one explicitly
// and the route test enforces the meaning of each value.
type Scope int

const (
	// ScopePublic serves without sign-in. Only routes on the public list may
	// use it.
	ScopePublic Scope = iota + 1
	// ScopeAuthorized needs a signed-in principal and the Authorizer to allow
	// every declared action on the resolved object before the handler runs.
	ScopeAuthorized
	// ScopeUpdateLease needs the update's lease token in the Authorization
	// header instead of a signed-in principal. The lease stands in for the
	// declared actions: start granted it after authorizing them, and it
	// scopes to one update alone. The Authorizer does not run.
	ScopeUpdateLease
)

// ObjectResolver returns the authorization object for a request: the
// namespace or unit that the request acts on. Resolvers for routes with a
// body read it with http.MaxBytesReader and restore r.Body before they
// return, so the handler can read it again.
type ObjectResolver func(w http.ResponseWriter, r *http.Request) (identity.Object, error)

// RouteHandler serves a route after the registration wrapper has
// authenticated (except for public routes) and authorized it. Public routes
// get zero principal and object. Authorized routes get the principal and the
// resolved object.
type RouteHandler func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs, how to resolve the object of each action from the
// request, and how to serve it. NewHandler registers every route from the
// table that buildRoutes returns, so a route without a row is never served.
// The registration wrapper is the single place that enforces the table: for
// every non-public route it authenticates, resolves the object, authorizes
// every declared action on that object, and only then calls the handler.
type Route struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern, for example /api/user.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Actions lists the actions the route needs. Only authorized routes set
	// it, and the wrapper authorizes every entry on the resolved object.
	Actions []identity.Action
	// Resolve returns the object that Actions apply to. Only authorized
	// routes set it.
	Resolve ObjectResolver
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	routes := h.baseRoutes()
	routes = append(routes, h.stackRoutes()...)
	routes = append(routes, h.secretRoutes()...)
	routes = append(routes, h.updateCreateRoutes()...)
	routes = append(routes, h.updateRunRoutes()...)
	routes = append(routes, h.updateImportExportRoutes()...)
	routes = append(routes, h.stateRoutes()...)
	routes = append(routes, h.updateCancelRoute(), h.updateRestoreRoute())

	return routes
}

// baseRoutes returns the login and listing routes that predate updates.
func (h *Handler) baseRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/capabilities",
			Scope:   ScopePublic,
			Actions: nil,
			Resolve: nil,
			Serve:   h.capabilities,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/user",
			// GET /api/user reads the caller's own record, so its object is
			// the caller itself and caller:read stays the gate. The
			// organizations list holds the readable namespaces (ADR 18): the
			// handler filters them with the Authorizer for namespace:read,
			// which this route declares for the filter.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCallerRead, identity.ActionNamespaceRead},
			Resolve: resolveCurrentUser,
			Serve:   h.currentUser,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/user/organizations/default",
			// The default organization is the only readable namespace, so its
			// object is the caller itself and the handler filters with the
			// Authorizer for namespace:read.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCallerRead, identity.ActionNamespaceRead},
			Resolve: resolveCurrentUser,
			Serve:   h.defaultOrganization,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/user/stacks",
			// With an organization the object is that namespace. Without one
			// the object is the caller itself and the handler lists every
			// readable namespace, filtered with the Authorizer.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead, identity.ActionNamespaceRead},
			Resolve: resolveListStacks,
			Serve:   h.listStacks,
		},
	}
}

// stackRoutes returns the stack management routes that predate updates.
func (h *Handler) stackRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitManage},
			Resolve: resolveCreateStack,
			Serve:   h.createStack,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.getStack,
		},
		{
			Method:  http.MethodDelete,
			Path:    "/api/stacks/{namespace}/{project}/{stack}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitManage},
			Resolve: resolveGetStack,
			Serve:   h.deleteStack,
		},
		{
			Method: http.MethodPost,
			Path:   "/api/stacks/{namespace}/{project}/{stack}/rename",
			// The wrapper authorizes unit:manage on the source unit in the
			// path. The handler also authorizes unit:manage on the
			// destination unit in the body before it renames.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitManage},
			Resolve: resolveGetStack,
			Serve:   h.renameStack,
		},
		{
			Method: http.MethodPatch,
			Path:   "/api/stacks/{namespace}/{project}/{stack}/tags",
			// PATCH replaces all tags with the map the Pulumi CLI sends, so
			// `stack tag set` and `stack tag rm` both land here. It writes
			// the stack's record without changing its resources.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.updateStackTags,
		},
	}
}

// secretRoutes returns the encryption routes of the service secrets
// provider. Encrypt routes need unit:encrypt; decrypt routes need
// unit:decrypt, so reading a stack alone never reveals its plaintext secrets
// (ADR 23). The audit routes always answer 204 and write one reveal entry
// each to the audit store (ADR 41 decision 13).
func (h *Handler) secretRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/encrypt",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitEncrypt},
			Resolve: resolveGetStack,
			Serve:   h.encryptValue,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/batch-encrypt",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitEncrypt},
			Resolve: resolveGetStack,
			Serve:   h.batchEncrypt,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/decrypt",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitDecrypt},
			Resolve: resolveGetStack,
			Serve:   h.decryptValue,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/batch-decrypt",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitDecrypt},
			Resolve: resolveGetStack,
			Serve:   h.batchDecrypt,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/decrypt/log-decryption",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitDecrypt},
			Resolve: resolveGetStack,
			Serve:   h.logDecryption,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/decrypt/log-batch-decryption",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitDecrypt},
			Resolve: resolveGetStack,
			Serve:   h.logBatchDecryption,
		},
	}
}

// updateCreateRoutes returns one create route per update kind. The four kinds
// share the create shape; each has its own route. Stack import has its own
// route (updateImportExportRoutes): the server runs the whole import update,
// so the CLI sends the deployment once instead of driving the update cycle.
func (h *Handler) updateCreateRoutes() []Route {
	kinds := []struct {
		path string
		kind stack.UpdateKind
	}{
		{"update", stack.KindUpdate},
		{"preview", stack.KindPreview},
		{"refresh", stack.KindRefresh},
		{"destroy", stack.KindDestroy},
	}

	routes := make([]Route, 0, len(kinds))
	for _, k := range kinds {
		routes = append(routes, Route{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/" + k.path,
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.createUpdateHandler(k.kind),
		})
	}

	return routes
}

// updateRunRoutes returns the start, lease, checkpoint, read, history and
// export routes for an update. Reads need unit:read; writes need unit:write.
// Cancel has its own route (updateCancelRoute): it serves the access token,
// not the lease.
func (h *Handler) updateRunRoutes() []Route {
	routes := h.updateLeaseRoutes()
	routes = append(routes, h.updateCheckpointRoutes()...)
	routes = append(routes, h.updateReadRoutes()...)

	return routes
}

// updateLeaseRoutes returns the start, lease renewal, event batch and
// complete routes of an update.
func (h *Handler) updateLeaseRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.startUpdate,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}/renew_lease",
			Scope:   ScopeUpdateLease,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.renewLease,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}/events/batch",
			Scope:   ScopeUpdateLease,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.recordEvents,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}/complete",
			Scope:   ScopeUpdateLease,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.completeUpdate,
		},
	}
}

// updateCheckpointRoutes returns the whole, verbatim and delta checkpoint
// routes of an update. Writes need unit:write.
func (h *Handler) updateCheckpointRoutes() []Route {
	return []Route{
		{
			// The CLI sends whole checkpoints, and the last write wins. The
			// server advertises no delta capability, so no sequence number
			// arrives.
			Method:  http.MethodPatch,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpoint",
			Scope:   ScopeUpdateLease,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.saveCheckpoint,
		},
		{
			// The first save of an update with the delta capability is a
			// full body with a sequence number. The deployment travels
			// exactly as sent, so later deltas apply to these bytes.
			Method:  http.MethodPatch,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpointverbatim",
			Scope:   ScopeUpdateLease,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.saveCheckpointVerbatim,
		},
		{
			// Later saves with the delta capability carry only the edits
			// since the last save. An invalid delta answers 409 and saves
			// nothing, so the CLI falls back to a full verbatim save.
			Method:  http.MethodPatch,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}/checkpointdelta",
			Scope:   ScopeUpdateLease,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.saveCheckpointDelta,
		},
	}
}

// updateReadRoutes returns the read, history and export routes of an
// update. Reads need unit:read.
func (h *Handler) updateReadRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.getUpdate,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/updates",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.listHistory,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/export",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.exportStack,
		},
	}
}

// updateImportExportRoutes returns the stack import route and the versioned
// export route. An import replaces the stack's state, so it needs
// unit:write; either export reads state, so it needs unit:read.
func (h *Handler) updateImportExportRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/import",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: resolveGetStack,
			Serve:   h.importStack,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}/export/{version}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.exportStackVersion,
		},
	}
}

// updateCancelRoute returns the cancel route: it ends the update named by the
// update ID without its lease, so it frees a stack locked by a vanished
// client. The CLI sends its access token, not the lease, so anyone with the
// right to update the stack may cancel.
func (h *Handler) updateCancelRoute() Route {
	return Route{
		Method:  http.MethodPost,
		Path:    "/api/stacks/{namespace}/{project}/{stack}/update/{update}/cancel",
		Scope:   ScopeAuthorized,
		Actions: []identity.Action{identity.ActionUnitWrite},
		Resolve: resolveGetStack,
		Serve:   h.cancelUpdate,
	}
}

// updateRestoreRoute returns the restore route: it copies the checkpoint
// of one stack version into a new update, so it writes state and needs
// unit:write.
func (h *Handler) updateRestoreRoute() Route {
	return Route{
		Method:  http.MethodPost,
		Path:    "/api/stacks/{namespace}/{project}/{stack}/restore/{version}",
		Scope:   ScopeAuthorized,
		Actions: []identity.Action{identity.ActionUnitWrite},
		Resolve: resolveGetStack,
		Serve:   h.restoreStack,
	}
}

// Routes returns a copy of the route table. The Actions lists are copied
// too, so callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, route := range h.routes {
		out[i] = route
		out[i].Actions = append([]identity.Action(nil), route.Actions...)
	}

	return out
}

// RegisteredPatterns returns a copy of every ServeMux pattern the handler
// serves, in registration order. The route test compares this with the route
// table in both directions.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}

// patternMux is an http.ServeMux that records every pattern registered on
// it, so the route test can list every served route. All handler routes go
// through Handle or HandleFunc, which both record. The inner mux stays
// private and is built only by newPatternMux, so no code outside this file
// can serve a pattern without recording it. TestNoMuxBypass enforces this:
// it fails if any other file in this package builds a mux or touches the
// inner mux.
type patternMux struct {
	servemux *http.ServeMux

	patterns []string
}

// newPatternMux returns a patternMux ready to register routes.
func newPatternMux() *patternMux {
	return &patternMux{servemux: http.NewServeMux(), patterns: nil}
}

// Handle registers pattern and records it.
func (m *patternMux) Handle(pattern string, handler http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.servemux.Handle(pattern, handler)
}

// HandleFunc registers pattern and records it.
func (m *patternMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.servemux.HandleFunc(pattern, handler)
}

// ServeHTTP implements http.Handler.
func (m *patternMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.servemux.ServeHTTP(w, r)
}

// Patterns returns a copy of the recorded patterns.
func (m *patternMux) Patterns() []string {
	return append([]string(nil), m.patterns...)
}

// resolveCurrentUser returns the caller itself: GET /api/user reads the
// caller's own record and acts on no namespace or unit.
func resolveCurrentUser(_ http.ResponseWriter, _ *http.Request) (identity.Object, error) {
	return identity.CallerObject(), nil
}

// resolveListStacks returns the namespace in the organization query value, or
// the caller itself when no organization is given (list every readable
// namespace). The project query value only filters the list, so the object
// stays the namespace or caller. It validates the project value the way
// listStacks does, so an invalid filter fails closed before authorization.
func resolveListStacks(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	if raw := r.URL.Query().Get("project"); raw != "" {
		if _, err := stack.ParseProjectName(raw); err != nil {
			return identity.Object{}, err
		}
	}

	if org := r.URL.Query().Get("organization"); org != "" {
		ns, err := namespace.ParseName(org)
		if err != nil {
			return identity.Object{}, err
		}

		return identity.NamespaceObject(ns), nil
	}

	return identity.CallerObject(), nil
}

// parseCreateStackRequest decodes a stack create body. The resolver and the
// handler share it, so both accept the same inputs: the whole body must be
// one JSON value.
func parseCreateStackRequest(body []byte) (createStackRequest, error) {
	var req createStackRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return createStackRequest{}, errInvalidBody
	}

	return req, nil
}

// resolveCreateStack returns the unit that the request creates. The stack
// name comes from the JSON body, so this reads the body once with
// http.MaxBytesReader and restores r.Body before returning.
func resolveCreateStack(w http.ResponseWriter, r *http.Request) (identity.Object, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return identity.Object{}, errInvalidBody
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	req, err := parseCreateStackRequest(body)
	if err != nil {
		return identity.Object{}, err
	}

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), req.StackName)
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ref)
}

// resolveGetStack returns the unit in the path values.
func resolveGetStack(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ref)
}

// enforce wraps one route table row. Public routes run unwrapped. Lease
// routes need the update's lease token and resolve the object, then run the
// handler: the lease stands in for the declared actions. Every other route
// authenticates, resolves the object, authorizes every declared action on
// it, and only then runs the handler.
func (h *Handler) enforce(route Route) http.HandlerFunc {
	switch route.Scope {
	case ScopePublic:
		return h.enforcePublic(route)
	case ScopeUpdateLease:
		return h.enforceLease(route)
	case ScopeAuthorized:
		return h.enforceAuthorized(route)
	}

	return h.enforceAuthorized(route)
}

// enforcePublic serves a route that needs no sign-in.
func (h *Handler) enforcePublic(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var guest identity.Principal

		var noObject identity.Object

		route.Serve(w, r, guest, noObject)
	}
}

// enforceLease serves a lease route: the update lease token stands in for the
// declared actions, so the Authorizer does not run.
func (h *Handler) enforceLease(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if _, ok := strings.CutPrefix(r.Header.Get("Authorization"), updateTokenScheme); !ok {
			h.writeError(ctx, w, identity.ErrUnauthenticated)

			return
		}

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		var guest identity.Principal

		route.Serve(w, r, guest, obj)
	}
}

// enforceAuthorized serves a route that needs a signed-in principal and the
// Authorizer to allow every declared action on the resolved object.
func (h *Handler) enforceAuthorized(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		p, err := h.authenticate(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		for _, action := range route.Actions {
			if err := h.authz.Authorize(ctx, p, action, obj); err != nil {
				h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, err))

				return
			}
		}

		route.Serve(w, r, p, obj)
	}
}
