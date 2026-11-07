// Package selfservice serves the self-service blueprint API.
//
// Grants apply at namespace level today: a blueprint action is checked on the
// namespace that holds the blueprint. Grants on one blueprint come in a later
// change.
package selfservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservicesvc"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The paths of the blueprint API routes.
const (
	BlueprintsPath = "/api/dustpan/v1/self-service/blueprints/{namespace}"
	DraftPath      = "/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/draft"
	VersionsPath   = "/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/versions"
	VersionPath    = "/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/versions/{version}"
	RetirePath     = "/api/dustpan/v1/self-service/blueprints/{namespace}/{blueprint}/versions/{version}/retire"
)

// ErrMissingDependency indicates that a required dependency is missing.
var ErrMissingDependency = errors.New("missing dependency")

// Config contains the dependencies of the blueprint HTTP adapter.
type Config struct {
	// Blueprints is the blueprint service. Its authorizer is a
	// NewNamespaceAuthorizer over Authorizer below.
	Blueprints    *selfservicesvc.Blueprints
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Audit         audit.Store
	// Namespaces gives the namespace of each refused request, so that its
	// denial is written in that namespace.
	Namespaces     refusal.Namespaces
	OnAuditFailure func()
	DenialLimiter  *denylimit.Limiter
	Sessions       *sessionauth.Checker
	Logger         *slog.Logger
	// Now is the clock of the audit entries. Nil means time.Now.
	Now func() time.Time
}

// Handler serves the blueprint API.
type Handler struct {
	blueprints *selfservicesvc.Blueprints
	authn      identity.Authenticator
	sessions   *sessionauth.Checker
	guard      *refusal.Recorder
	logger     *slog.Logger
	now        func() time.Time
	mux        *route.Mux
}

// NewHandler constructs the blueprint HTTP adapter. It returns
// ErrMissingDependency when a required field of cfg is missing.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Blueprints == nil {
		return nil, fmt.Errorf("%w: blueprints", ErrMissingDependency)
	}
	if cfg.Authenticator == nil {
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	}
	if cfg.Authorizer == nil {
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	}
	if cfg.Audit == nil {
		return nil, fmt.Errorf("%w: audit", ErrMissingDependency)
	}
	if cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: namespaces", ErrMissingDependency)
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
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
		blueprints: cfg.Blueprints,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("selfservice"),
		logger:     cfg.Logger,
		now:        now,
		mux:        route.NewMux(),
	}

	rows := h.Routes()
	for i := range rows {
		h.mux.HandleFunc(rows[i].Method+" "+rows[i].Path, h.enforce(rows[i]))
	}

	return h, nil
}

// ServeHTTP serves a blueprint API request.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// RegisteredPatterns returns the ServeMux patterns that the handler serves, in
// registration order. The route test compares them with the route table.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}

// target is what a row acts on: the namespace of the path, the blueprint
// reference, and the version number where the path names one.
type target struct {
	ns      namespace.Name
	ref     selfservice.BlueprintRef
	version int
}

// Route is one row of the route table. Reads are the read actions of the row:
// a refused read answers 404, so that the API does not reveal that a blueprint
// exists. Actions are the write actions of the row: a refused write answers
// 403. Every action is checked on the namespace of the path.
type Route struct {
	Method string
	Path   string
	Scope  route.Scope
	Reads  []identity.Action
	// Actions are the write actions of the row.
	Actions []identity.Action

	resolve func(http.ResponseWriter, *http.Request) (target, error)
	serve   func(http.ResponseWriter, *http.Request, identity.Principal, target)
}

// Routes returns the route table. The rows are built on each call, so a caller
// can not change the handler through the result.
func (h *Handler) Routes() []Route {
	return []Route{
		{
			Method: http.MethodGet, Path: BlueprintsPath, Scope: route.ScopeAuthorized,
			Reads: []identity.Action{selfservice.ActionBlueprintRead}, Actions: nil,
			resolve: resolveNamespace, serve: h.list,
		},
		{
			Method: http.MethodGet, Path: DraftPath, Scope: route.ScopeAuthorized,
			Reads: []identity.Action{selfservice.ActionBlueprintRead}, Actions: nil,
			resolve: resolveBlueprint, serve: h.readDraft,
		},
		{
			Method: http.MethodPut, Path: DraftPath, Scope: route.ScopeAuthorized,
			Reads: nil, Actions: []identity.Action{selfservice.ActionBlueprintPublish},
			resolve: resolveBlueprint, serve: h.writeDraft,
		},
		{
			Method: http.MethodPost, Path: VersionsPath, Scope: route.ScopeAuthorized,
			Reads: nil, Actions: []identity.Action{selfservice.ActionBlueprintPublish},
			resolve: resolveBlueprint, serve: h.publish,
		},
		{
			Method: http.MethodGet, Path: VersionPath, Scope: route.ScopeAuthorized,
			Reads: []identity.Action{selfservice.ActionBlueprintRead}, Actions: nil,
			resolve: resolveVersion, serve: h.readVersion,
		},
		{
			Method: http.MethodPost, Path: RetirePath, Scope: route.ScopeAuthorized,
			Reads: nil, Actions: []identity.Action{selfservice.ActionBlueprintRetire},
			resolve: resolveVersion, serve: h.retire,
		},
	}
}

// enforce wraps one row in the shared route enforcement. The namespace read
// comes first for every row, as in the other APIs.
func (h *Handler) enforce(rw Route) http.HandlerFunc {
	guard := route.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}

	return route.Enforce(guard, h.writeError, route.Steps[target]{
		Scope:   rw.Scope,
		Actions: slices.Concat([]identity.Action{identity.ActionNamespaceRead}, rw.Reads, rw.Actions),
		Resolve: rw.resolve,
		Authorize: func(r *http.Request, p identity.Principal, t target) error {
			obj := identity.NamespaceObject(t.ns)

			return guard.AuthorizeObject(r.Context(), r, p, obj, route.Checks{
				Namespace: obj,
				Reads:     append([]identity.Action{identity.ActionNamespaceRead}, rw.Reads...),
				Actions:   rw.Actions,
				NotFound:  selfservice.ErrNotFound,
				// Defense in depth: a write row declares its action in Actions, so
				// an empty list fails closed here. A read row declares its action in
				// Reads, which the helper checks first; Actions is empty for it, so
				// the requirement applies to write rows only.
				RequireAction: len(rw.Actions) > 0,
			})
		},
		Serve: rw.serve,
	})
}

// noTarget is the zero target of a request that failed to resolve.
func noTarget() target {
	var ns namespace.Name

	return target{ns: ns, ref: selfservice.BlueprintRef{Namespace: ns, Name: ""}, version: 0}
}

// resolveNamespace reads the namespace of the path.
func resolveNamespace(_ http.ResponseWriter, r *http.Request) (target, error) {
	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return noTarget(), fmt.Errorf("namespace in the path: %w", err)
	}

	return target{ns: ns, ref: selfservice.BlueprintRef{Namespace: ns, Name: ""}, version: 0}, nil
}

// resolveBlueprint reads the namespace and the blueprint name of the path.
func resolveBlueprint(w http.ResponseWriter, r *http.Request) (target, error) {
	t, err := resolveNamespace(w, r)
	if err != nil {
		return noTarget(), err
	}

	name := r.PathValue("blueprint")
	if !selfservice.ValidName(name) {
		return noTarget(), fmt.Errorf("blueprint name in the path: %w", selfservice.ErrInvalid)
	}

	t.ref.Name = name

	return t, nil
}

// resolveVersion reads the blueprint and the version number of the path.
func resolveVersion(w http.ResponseWriter, r *http.Request) (target, error) {
	t, err := resolveBlueprint(w, r)
	if err != nil {
		return noTarget(), err
	}

	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		return noTarget(), fmt.Errorf("version in the path: %w", selfservice.ErrInvalid)
	}

	t.version = n

	return t, nil
}

// NamespaceAuthorizer checks blueprint actions on the namespace that holds
// them. It implements selfservicesvc.Authorizer.
//
// Defense in depth: the route layer checks the same namespace before the
// service runs; this adapter stays so that a new caller of the service cannot
// skip the check.
type NamespaceAuthorizer struct {
	authorizer identity.Authorizer
}

// NewNamespaceAuthorizer returns the authorizer that checks blueprint actions
// on the namespace of each blueprint.
func NewNamespaceAuthorizer(a identity.Authorizer) NamespaceAuthorizer {
	return NamespaceAuthorizer{authorizer: a}
}

// Authorize checks action for p on the object that object names for the
// authorizer. A blueprint is checked on its namespace, and a principal on its
// own reference, so that principal:manage-tokens on a source principal is
// checked on that principal.
func (a NamespaceAuthorizer) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, object selfservice.Object,
) error {
	target, err := authorizedObject(object)
	if err != nil {
		return err
	}

	return a.authorizer.Authorize(ctx, p, action, target)
}

// authorizedObject returns the object of the authorizer for object. It refuses
// a kind that this adapter does not map, so that a new kind reaches the
// authorizer only after a decision here.
func authorizedObject(object selfservice.Object) (identity.Object, error) {
	if object.Kind == selfservice.ObjectBlueprint || object.Kind == selfservice.ObjectNamespace {
		return identity.NamespaceObject(object.Namespace), nil
	}

	// Defense in depth: both conditions must hold. A principal unit under an
	// unmapped kind, or the principal kind under a unit that is not a principal,
	// is refused below. Neither check relies on the other.
	if object.Kind == selfservice.ObjectPrincipal && object.Unit.IsPrincipal() {
		return object.Unit, nil
	}

	// The blueprint service maps no unit or collection, and no other kind.
	return identity.Object{}, unmappedKind(object.Kind)
}

// unmappedKind is the denial of an object kind that the adapter does not map.
func unmappedKind(kind selfservice.ObjectKind) error {
	return fmt.Errorf("object kind %q has no authorization here: %w", kind, identity.ErrDenied)
}
