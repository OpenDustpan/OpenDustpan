// Package sourcefilter serves the source filter of a namespace under
// /api/dustpan/v1/namespaces/{namespace}/source-filter. A read needs
// namespace:read-runners and a change needs namespace:manage-runners.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone.
package sourcefilter

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/source"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// Path is the route of the source filter of a namespace.
const Path = "/api/dustpan/v1/namespaces/{namespace}/source-filter"

// Config holds the dependencies of a Handler. Every field except Sessions,
// OnAuditFailure, DenialLimiter and Now is required.
type Config struct {
	// Namespaces gives the namespace of a request and its current fence.
	Namespaces *namespace.Service
	// Filters keeps the source filter of each namespace.
	Filters       source.Filters
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses.
	Audit audit.Store
	// OnAuditFailure, when set, runs once for each refusal whose entry could not
	// be written, so the server can count it.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
	Logger   *slog.Logger
	// Now is the clock of the audit entries. Nil means time.Now.
	Now func() time.Time
}

// validate returns ErrMissingDependency when cfg lacks a required dependency.
func (cfg Config) validate() error {
	switch {
	case cfg.Namespaces == nil:
		return fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Filters == nil:
		return fmt.Errorf("%w: source filter store", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Audit == nil:
		return fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Logger == nil:
		return fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	return nil
}

// Handler serves the source filter routes.
type Handler struct {
	namespaces *namespace.Service
	filters    source.Filters
	authn      identity.Authenticator
	sessions   *sessionauth.Checker
	guard      *refusal.Recorder
	logger     *slog.Logger
	now        func() time.Time
	mux        *route.Mux
	routes     []Route
}

// Route is one row of the route table. The enforcement wrapper runs the
// checks of a row in this order. A refusal of the first answers the 404 of an unknown namespace, so a
// caller learns nothing about a namespace that it cannot read:
//
//  1. identity.ActionNamespaceRead on the namespace of the path.
//  2. Actions on the namespace of the path. A refusal answers 403, because the
//     caller can already read the namespace.
type Route = route.Route[identity.Object]

// NewHandler returns a Handler. It returns ErrMissingDependency when a
// required field of cfg is missing.
func NewHandler(cfg Config) (*Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	h := &Handler{
		namespaces: cfg.Namespaces,
		filters:    cfg.Filters,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("sourcefilter"),
		logger:     cfg.Logger,
		now:        now,
		mux:        route.NewMux(),
		routes:     nil,
	}

	h.routes = h.buildRoutes()
	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Pattern(), h.enforce(rt))
	}

	return h, nil
}

// Routes returns a copy of the route table. The action lists are copied too,
// so callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, rt := range h.routes {
		out[i] = rt
		out[i].Actions = slices.Clone(rt.Actions)
	}

	return out
}

// RegisteredPatterns returns a copy of every ServeMux pattern the handler
// serves, in registration order. The route test compares this with the route
// table in both directions.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet, Path: Path, Scope: route.ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceReadRunners},
			Resolve: resolveNamespace, Serve: h.get,
		},
		{
			Method: http.MethodPut, Path: Path, Scope: route.ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceManageRunners},
			Resolve: resolveNamespace, Serve: h.put,
		},
	}
}

// resolveNamespace returns the namespace object of the path. A name that no
// namespace can have fails before authorization.
func resolveNamespace(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, fmt.Errorf("namespace in the path: %w", err)
	}

	return identity.NamespaceObject(name), nil
}

// enforce wraps one route table row in the shared wrapper: it authenticates,
// resolves the namespace, authorizes the checks of Route, and only then runs
// the handler.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	guard := route.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}

	return route.Enforce(guard, h.writeError, route.Steps[identity.Object]{
		Scope:   rt.Scope,
		Actions: slices.Concat([]identity.Action{identity.ActionNamespaceRead}, rt.Actions),
		Resolve: rt.Resolve,
		Authorize: func(r *http.Request, p identity.Principal, obj identity.Object) error {
			return authorizeRoute(guard, r, p, obj, rt.Actions)
		},
		Serve: rt.Serve,
	})
}

// authorizeRoute checks namespace:read on obj, then each of actions. A
// refusal of namespace:read answers the 404 of an unknown namespace, except
// the refusal of an address that the IP allowlist of the namespace does not
// allow: that one answers 403 with its own code, as on the other routes of the
// namespace.
func authorizeRoute(
	guard route.Guard, r *http.Request, p identity.Principal, obj identity.Object, actions []identity.Action,
) error {
	err := guard.AuthorizeAll(r, p, obj, []identity.Action{identity.ActionNamespaceRead})
	if err != nil {
		if errors.Is(err, identity.ErrDenied) && !errors.Is(err, ipallow.ErrAddressNotAllowed) {
			return namespace.ErrNotFound
		}

		return err
	}

	return guard.AuthorizeAll(r, p, obj, actions)
}
