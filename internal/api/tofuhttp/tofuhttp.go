// Package tofuhttp serves OpenTofu's generic http backend (ADR 49). The
// backend has one address for each workspace, /tofu/state/{namespace}/{workspace},
// and that address serves GET, POST, LOCK and UNLOCK. The adapter calls the
// workspace domain. It does not call the TFE-compatible adapter in
// internal/api/tfe.
//
// The routes are GET, POST, LOCK and UNLOCK.
package tofuhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// unitTool names this adapter's tool in unit references. The console shows
// one tool for both OpenTofu adapters (ADR 49, decision 11).
const unitTool = "opentofu"

// WorkspaceService is the workspace use cases that the http backend routes
// need. workspace.Service implements it.
//
//nolint:interfacebloat // One port for the workspace use cases of the http backend: each route calls one of them.
type WorkspaceService interface {
	// Get returns the workspace called ref, or workspace.ErrNotFound.
	Get(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
	// GetByID returns the workspace with id, or workspace.ErrNotFound.
	GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error)
	// Create creates an unlocked workspace without state.
	Create(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
	// LockWithID locks the workspace for holder under lockID, the ID the
	// client chose (ADR 49, decision 6).
	LockWithID(ctx context.Context, id workspace.ID, holder, reason, lockID string) (workspace.Workspace, error)
	// UnlockLock releases holder's lock when it is lockID (ADR 49, decision 6). A
	// lock that changed after the caller read the unit is refused, not released.
	UnlockLock(ctx context.Context, id workspace.ID, lockID, holder string) (workspace.Workspace, error)
	// ForceUnlockLock releases the lock expect names, whoever holds it, writing
	// the fence version of a unit with state (ADR 44, decision 1). It reports an
	// unfenced release.
	ForceUnlockLock(
		ctx context.Context, id workspace.ID, expect workspace.Lock, by string,
		allow namespace.Overrides, record func() error,
	) (workspace.Workspace, bool, error)
	// Current returns the workspace's current state version, or
	// workspace.ErrNoState.
	Current(ctx context.Context, id workspace.ID) (workspace.StateVersion, error)
	// Open returns the content of sv. The caller closes it.
	Open(ctx context.Context, sv workspace.StateVersion) (io.ReadCloser, error)
	// WriteState stores one state version streamed from body (ADR 49, decision 7).
	WriteState(
		ctx context.Context, id workspace.ID, in workspace.StreamInput,
		auth workspace.StreamAuthority, body io.Reader,
	) (workspace.StateVersion, error)
}

// Config holds the dependencies of a Handler.
type Config struct {
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
	Workspaces    WorkspaceService
	Namespaces    NamespaceService
	// Audit receives the entry of each override a write uses. Nil writes none.
	Audit audit.Store
	// Gate bounds concurrent large bodies by bytes held at once.
	Gate *bodygate.Gate
}

// Handler serves the http backend under /tofu/state/.
type Handler struct {
	authn  identity.Authenticator
	authz  identity.Authorizer
	logger *slog.Logger
	ws     WorkspaceService
	// namespaces and audit serve the override allowance and its audit entries.
	namespaces NamespaceService
	audit      audit.Store
	gate       *bodygate.Gate
	mux        *http.ServeMux
	routes     []Route
}

// call is the authorized request that a route handler serves.
type call struct {
	principal identity.Principal
	ref       workspace.Ref
}

// Route declares one endpoint: its method and pattern, the actions that the
// caller needs on the unit of the path, and the handler. NewHandler registers
// every row of buildRoutes, so a route without a row is never served. Every
// row declares its actions (ADR 49, decision 6).
type Route struct {
	Method  string
	Path    string
	Actions []identity.Action
	Serve   func(w http.ResponseWriter, r *http.Request, c call)
}

// NewHandler returns a Handler. Every Config field is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.Workspaces == nil:
		return nil, fmt.Errorf("%w: workspaces", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespaces", ErrMissingDependency)
	}

	h := &Handler{
		authn:      cfg.Authenticator,
		authz:      cfg.Authorizer,
		logger:     cfg.Logger,
		ws:         cfg.Workspaces,
		namespaces: cfg.Namespaces,
		audit:      cfg.Audit,
		gate:       cfg.Gate,
		mux:        http.NewServeMux(),
		routes:     nil,
	}

	if h.gate == nil {
		h.gate = bodygate.New(0)
	}

	h.routes = h.buildRoutes()

	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// ServeHTTP serves the routes of the table.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// buildRoutes returns the route table. It is the single source of the patterns
// that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	return slices.Concat(h.stateRoutes(), h.writeStateRoutes(), h.lockRoutes())
}

// Patterns of the one address of each workspace.
const statePath = "/tofu/state/{namespace}/{workspace}"

// enforce wraps one route row. It authenticates the caller, parses the names
// of the path, authorizes every declared action on the unit they name, and
// only then runs the handler. The unit is named by the path, so the check does
// not read the database first.
func (h *Handler) enforce(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		p, err := h.authenticate(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		ref, err := parseRef(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		obj, err := unitObject(ref)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		for _, action := range route.Actions {
			if authErr := h.authz.Authorize(ctx, p, action, obj); authErr != nil {
				h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, authErr))

				return
			}
		}

		route.Serve(w, r, call{principal: p, ref: ref})
	}
}

// authenticate reads the token of the request. The OpenTofu backend sends
// HTTP basic auth, with the token as the password; the username is ignored
// (ADR 49, decision 4). An "Authorization: Bearer" header also carries the
// token.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, error) {
	token := ""

	if _, password, ok := r.BasicAuth(); ok {
		token = password
	} else if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		token = raw
	}

	if token == "" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return h.authn.AuthenticateToken(r.Context(), token)
}

// parseRef reads the namespace and the workspace of the path.
func parseRef(r *http.Request) (workspace.Ref, error) {
	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return workspace.Ref{}, err
	}

	name, err := workspace.ParseName(r.PathValue("workspace"))
	if err != nil {
		return workspace.Ref{}, err
	}

	return workspace.Ref{Namespace: ns, Name: name}, nil
}

// unitObject maps a workspace reference to its tool-neutral unit reference.
func unitObject(ref workspace.Ref) (identity.Object, error) {
	return identity.UnitObject(unitTool, ref.Namespace, ref.Name.String())
}

// writeError answers an error with a plain status and a message that holds no
// driver or request detail. The log keeps the status only.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	status, message := classify(err)
	if status == http.StatusInternalServerError {
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", status))
	}

	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="OpenDustpan"`)
	}

	if status == http.StatusServiceUnavailable {
		bodygate.SetRetryAfter(w)
	}

	http.Error(w, message, status)
}

// classify maps an error to its status and a message that is safe to show.
func classify(err error) (int, string) {
	if status, message, ok := classifyAccess(err); ok {
		return status, message
	}

	if status, message, ok := classifyState(err); ok {
		return status, message
	}

	return http.StatusInternalServerError, "internal error"
}

// classifyAccess maps the errors of the sign-in, the names and the lock to a
// status. ok is false for any other error.
func classifyAccess(err error) (int, string, bool) {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated", true
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "permission denied", true
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, workspace.ErrNoState),
		errors.Is(err, namespace.ErrNotFound):
		return http.StatusNotFound, "not found", true
	case errors.Is(err, workspace.ErrInvalidName), errors.Is(err, namespace.ErrInvalidName),
		errors.Is(err, workspace.ErrInvalidLockID):
		return http.StatusBadRequest, "invalid request", true
	case errors.Is(err, workspace.ErrLocked), errors.Is(err, workspace.ErrLockedByOther):
		return http.StatusLocked, "workspace is locked", true
	case errors.Is(err, workspace.ErrAlreadyExists):
		return http.StatusConflict, "workspace already exists", true
	default:
		return 0, "", false
	}
}

// classifyState maps the errors of a state write to a status. ok is false for
// any other error.
func classifyState(err error) (int, string, bool) {
	switch {
	case errors.Is(err, workspace.ErrBusy):
		return http.StatusServiceUnavailable, "server is busy: try again in one second", true
	case errors.Is(err, workspace.ErrConcurrentModification), errors.Is(err, workspace.ErrLockMismatch),
		errors.Is(err, workspace.ErrStaleSerial), errors.Is(err, workspace.ErrLineageMismatch):
		return http.StatusConflict, "state conflicts with the current state or lock", true
	case errors.Is(err, workspace.ErrNotLocked), errors.Is(err, workspace.ErrOverrideRefused),
		errors.Is(err, workspace.ErrClientEncrypted):
		return http.StatusForbidden, "write refused: an override is off or not granted", true
	case errors.Is(err, workspace.ErrChecksumMismatch), errors.Is(err, workspace.ErrInvalidState):
		return http.StatusBadRequest, "invalid state", true
	case errors.Is(err, workspace.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "state is too large", true
	case errors.Is(err, workspace.ErrTooManyPending):
		return http.StatusTooManyRequests, "too many pending state versions", true
	default:
		return 0, "", false
	}
}
