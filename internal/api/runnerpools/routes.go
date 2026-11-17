package runnerpools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Scope names how a route is protected. Every route declares one explicitly
// and the route test enforces the meaning of each value.
type Scope int

const (
	// ScopeAuthorized needs a signed-in principal and the Authorizer to allow
	// the actions of the route on the namespace of the path.
	ScopeAuthorized Scope = iota + 1
)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs and how to serve it. NewHandler registers every route
// from the table that buildRoutes returns, so a route without a row is never
// served.
//
// A route runs the checks in this order:
//
//  1. The credential is authenticated. A request without a valid credential
//     answers 401, a session that fails the CSRF check or comes from
//     another site answers 403, and too many failed sign-ins answer 429.
//  2. The client address is checked against the IP allowlist of the namespace
//     of the path. A refused address answers 403.
//  3. The namespace of the path is read. An unknown namespace answers 404.
//  4. identity.ActionNamespaceRead on the namespace. A refusal answers 404,
//     the answer of an unknown namespace, so a caller learns nothing about a
//     namespace that it cannot read. This read check applies the IP allowlist
//     a second time, and an allowlist refusal there keeps its 403: it is not
//     mapped to 404.
//  5. Actions on the namespace of the path. A refusal answers 403.
type Route struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Actions lists the actions that the route needs.
	Actions []identity.Action
	// Serve handles the request after the wrapper has enforced the row.
	Serve routeHandler
}

// Path patterns of the management API.
const (
	poolsPath       = "/api/dustpan/v1/namespaces/{namespace}/runner-pools"
	poolPath        = poolsPath + "/{pool}"
	joinTokensPath  = poolPath + "/join-tokens"
	joinTokenPath   = joinTokensPath + "/{id}/revoke"
	poolRunnersPath = poolPath + "/runners"
	drainPath       = "/api/dustpan/v1/namespaces/{namespace}/runners/{runner}/drain"
	retirePath      = "/api/dustpan/v1/namespaces/{namespace}/runners/{runner}/retire"
)

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	return append(h.managementRoutes(), h.proxySettingsRoutes()...)
}

// managementRoutes returns the routes of the management API.
func (h *Handler) managementRoutes() []Route {
	read := []identity.Action{identity.ActionNamespaceReadRunners}
	manage := []identity.Action{identity.ActionNamespaceManageRunners}

	return []Route{
		{Method: http.MethodGet, Path: poolsPath, Scope: ScopeAuthorized, Actions: read, Serve: h.listPools},
		{Method: http.MethodPost, Path: poolsPath, Scope: ScopeAuthorized, Actions: manage, Serve: h.createPool},
		{Method: http.MethodGet, Path: poolPath, Scope: ScopeAuthorized, Actions: read, Serve: h.getPool},
		{Method: http.MethodPatch, Path: poolPath, Scope: ScopeAuthorized, Actions: manage, Serve: h.updatePool},
		{Method: http.MethodDelete, Path: poolPath, Scope: ScopeAuthorized, Actions: manage, Serve: h.deletePool},
		{Method: http.MethodGet, Path: joinTokensPath, Scope: ScopeAuthorized, Actions: read, Serve: h.listJoinTokens},
		{
			Method:  http.MethodPost,
			Path:    joinTokensPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Serve:   h.createJoinToken,
		},
		{
			Method:  http.MethodPost,
			Path:    joinTokenPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Serve:   h.revokeJoinToken,
		},
		{Method: http.MethodGet, Path: poolRunnersPath, Scope: ScopeAuthorized, Actions: read, Serve: h.listRunners},
		{Method: http.MethodPost, Path: drainPath, Scope: ScopeAuthorized, Actions: manage, Serve: h.drainRunner},
		{Method: http.MethodPost, Path: retirePath, Scope: ScopeAuthorized, Actions: manage, Serve: h.retireRunner},
	}
}

// maxAdminBody bounds the body of a request to the management API.
const maxAdminBody = 64 << 10

// call is the checked request that a route handler gets. It holds the
// principal, the namespace of the path and the body, which is read once.
type call struct {
	principal identity.Principal
	ns        namespace.Namespace
	body      []byte
}

// routeHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type routeHandler func(w http.ResponseWriter, r *http.Request, c *call)

// enforce wraps one route table row: it authenticates, runs the checks of
// its scope, and only then runs the handler.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		c := new(call)

		var err error

		if rt.Scope == ScopeAuthorized {
			r, err = h.checkAuthorized(r, rt, c)
		} else {
			err = fmt.Errorf("%w: route %s has no scope", errNotFound, rt.Path)
		}

		if err != nil {
			h.writeError(ctx, w, rt.Scope, err)

			return
		}

		rt.Serve(w, r, c)
	}
}

// checkAuthorized authenticates the principal, finds the namespace of the
// path, and checks the actions of route. It returns the request, which holds
// the session mark of a request that the console cookie signed in.
func (h *Handler) checkAuthorized(r *http.Request, rt Route, c *call) (*http.Request, error) {
	ctx := r.Context()

	p, session, err := h.authenticate(r)
	if err != nil {
		return r, err
	}

	if session {
		r = r.WithContext(sessionauth.WithSession(ctx))
	}

	if addrErr := h.checkAddress(ctx, r, p); addrErr != nil {
		return r, addrErr
	}

	ns, err := h.namespaces.Get(ctx, namespace.Name(r.PathValue("namespace")))
	if errors.Is(err, namespace.ErrNotFound) {
		return r, errNotFound
	}

	if err != nil {
		return r, fmt.Errorf("get namespace: %w", err)
	}

	obj := identity.NamespaceObject(ns.Name)

	g := route.Guard{Tokens: nil, Sessions: nil, Refusals: h.guard}
	checks := route.Checks{Namespace: obj, Reads: nil, Actions: rt.Actions, NotFound: errNotFound, RequireAction: true}
	if authErr := g.AuthorizeObject(ctx, r, p, obj, checks); authErr != nil {
		return r, authErr
	}

	body, err := readBody(r, maxAdminBody)
	if err != nil {
		return r, err
	}

	c.principal, c.ns, c.body = p, ns, body

	return r, nil
}

// checkAddress refuses a client address that is outside the IP allowlist of
// the namespace in the path, before the lookup of the namespace. The answer is
// the same 403 for a namespace that exists and for one that does not exist.
// Every other result, also an invalid name, continues. An authorizer error
// stops the request here and writes one entry, also for a namespace that does
// not exist. A policy refusal continues, so that the later check records it
// once. It asks through Decide, which writes no entry.
//
// Defense in depth: checkAuthorized checks the allowlist again on the found
// namespace. This check stays so that the order of the lookup never decides
// the answer.
func (h *Handler) checkAddress(ctx context.Context, r *http.Request, p identity.Principal) error {
	action := identity.ActionNamespaceRead

	// An invalid name names no namespace and has no allowlist: the lookup that
	// follows answers 404 for it.
	if name, parseErr := namespace.ParseName(r.PathValue("namespace")); parseErr == nil {
		obj := identity.NamespaceObject(name)

		err := h.guard.Decide(ctx, p, action, obj)
		if errors.Is(err, ipallow.ErrAddressNotAllowed) {
			h.guard.Record(ctx, r, p, refusal.Denial{
				Action: action, Object: obj, Reason: refusal.ReasonFor(err), ReasonWithin: nil, Window: 0,
			})

			return fmt.Errorf("authorize %s: %w", action, err)
		}

		if err != nil && !errors.Is(err, identity.ErrDenied) {
			h.guard.Record(ctx, r, p, refusal.Denial{
				Action: action, Object: obj, Reason: refusal.ReasonFor(err), ReasonWithin: nil, Window: 0,
			})

			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}

// authenticate resolves the "Authorization: token <t>" or the
// "Authorization: Bearer <t>" header to a principal. A request with no
// Authorization header uses the console session cookie, and the bool
// reports that the cookie signed it in.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, bool, error) {
	header := apidesc.Header(r, "Authorization")
	if header == "" {
		p, _, err := h.sessions.Authenticate(r)

		return p, err == nil, err
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok {
		raw, ok = strings.CutPrefix(header, bearerScheme)
	}

	if !ok || raw == "" {
		return identity.Principal{}, false, identity.ErrUnauthenticated
	}

	p, err := h.authn.AuthenticateToken(r.Context(), raw)

	return p, false, err
}

// readBody reads the body of r up to limit bytes. A longer body is an
// invalid request.
func readBody(r *http.Request, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: the body has more than %d bytes", errBadBody, limit)
	}

	return body, nil
}
