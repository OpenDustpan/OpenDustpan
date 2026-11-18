// Package state serves the masked read of the state of a unit under
// /api/dustpan/v1/units/{ref}/state. A read needs namespace:read on the
// namespace of the unit and unit:read on the unit. The answer holds a marker
// with the JSON Pointer value path of each secret value of the version, and
// never a secret value.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: it reads each tool through the domain package of the tool.
package state

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// ReadTimeout is the time limit of one masked read. A read that passes it
// answers 504 with no bytes of the state.
const ReadTimeout = 30 * time.Second

// MinCursorKeyBytes is the shortest key of the cursors that NewHandler
// accepts.
const MinCursorKeyBytes = 32

// Stacks finds a stack by its reference. stack.Service implements it. Get
// returns stack.ErrNotFound for a stack that does not exist.
type Stacks interface {
	Get(ctx context.Context, ref stack.Ref) (stack.Stack, error)
}

// StackStates reads the masked state of a stack version. stack.Updates
// implements it.
type StackStates interface {
	ReadMasked(ctx context.Context, args stack.ReadMaskedArgs) (stack.MaskedPage, error)
}

// Workspaces finds a workspace by its reference and reads the masked state of
// one of its versions. workspace.Service implements it. Get returns
// workspace.ErrNotFound for a workspace that does not exist.
type Workspaces interface {
	Get(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
	ReadMasked(ctx context.Context, args workspace.ReadMaskedArgs) (workspace.MaskedPage, error)
}

// Config holds the dependencies of a Handler. Every field except Sessions,
// OnAuditFailure, DenialLimiter and Timeout is required.
type Config struct {
	// Namespaces reads the namespace of a refusal for its audit entry.
	Namespaces  refusal.Namespaces
	Stacks      Stacks
	StackStates StackStates
	Workspaces  Workspaces
	// Authenticator and Authorizer check the caller. The read checks
	// unit:read again in the domain package of the tool, with the same
	// Authorizer.
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the server refuses.
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
	// CursorKey signs the cursors of the pages. It has at least
	// MinCursorKeyBytes bytes.
	CursorKey []byte
	Logger    *slog.Logger
	// Timeout is the time limit of one read. Zero means ReadTimeout. A value
	// above ReadTimeout is refused.
	Timeout time.Duration
}

// validate returns ErrMissingDependency when cfg lacks a required dependency.
func (cfg Config) validate() error {
	switch {
	case cfg.Namespaces == nil:
		return fmt.Errorf("%w: namespace reader", ErrMissingDependency)
	case cfg.Stacks == nil:
		return fmt.Errorf("%w: stack service", ErrMissingDependency)
	case cfg.StackStates == nil:
		return fmt.Errorf("%w: stack state reader", ErrMissingDependency)
	case cfg.Workspaces == nil:
		return fmt.Errorf("%w: workspace service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Audit == nil:
		return fmt.Errorf("%w: audit store", ErrMissingDependency)
	case len(cfg.CursorKey) < MinCursorKeyBytes:
		return fmt.Errorf("%w: cursor key of at least %d bytes", ErrMissingDependency, MinCursorKeyBytes)
	case cfg.Logger == nil:
		return fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.Timeout < 0 || cfg.Timeout > ReadTimeout:
		return fmt.Errorf("%w: timeout from 0 to %s", ErrMissingDependency, ReadTimeout)
	}

	return nil
}

// Handler serves the masked read of the state of a unit.
type Handler struct {
	stacks      Stacks
	stackStates StackStates
	workspaces  Workspaces
	authn       identity.Authenticator
	authz       identity.Authorizer
	sessions    *sessionauth.Checker
	guard       *refusal.Recorder
	key         []byte
	timeout     time.Duration
	logger      *slog.Logger
	mux         *route.Mux
	routes      []Route
}

// Route is one row of the route table. The enforcement wrapper runs the
// checks of a row in this order:
//
//  1. identity.ActionNamespaceRead on the namespace of the unit. A refusal
//     answers the 404 of an unknown unit, so a caller learns nothing about a
//     namespace that it cannot read.
//  2. Actions on the unit of the path. A refusal answers 403.
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

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = ReadTimeout
	}

	h := &Handler{
		stacks:      cfg.Stacks,
		stackStates: cfg.StackStates,
		workspaces:  cfg.Workspaces,
		authn:       cfg.Authenticator,
		authz:       cfg.Authorizer,
		sessions:    cfg.Sessions,
		guard:       recorder.WithLimiter(cfg.DenialLimiter).WithSurface("state"),
		key:         slices.Clone(cfg.CursorKey),
		timeout:     timeout,
		logger:      cfg.Logger,
		mux:         route.NewMux(),
		routes:      nil,
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
			Method: http.MethodGet, Path: apidesc.UnitsStatePath, Scope: route.ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveUnit, Serve: h.read,
		},
	}
}

// enforce wraps one route table row in the shared wrapper: it authenticates,
// resolves the unit, authorizes the checks of Route, and only then runs the
// handler.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	guard := route.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}

	return route.Enforce(guard, h.writeError, route.Steps[identity.Object]{
		Scope:   rt.Scope,
		Actions: slices.Concat([]identity.Action{identity.ActionNamespaceRead}, rt.Actions),
		Resolve: rt.Resolve,
		Authorize: func(r *http.Request, p identity.Principal, obj identity.Object) error {
			return guard.AuthorizeObject(r.Context(), r, p, obj, route.Checks{
				Namespace:     identity.NamespaceObject(obj.Namespace()),
				Reads:         nil,
				Actions:       rt.Actions,
				NotFound:      errUnitNotFound,
				RequireAction: true,
			})
		},
		Serve: rt.Serve,
	})
}
