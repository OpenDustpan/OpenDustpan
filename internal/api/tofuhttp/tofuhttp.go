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

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

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
	Create(ctx context.Context, ref workspace.Ref, p workspace.Placement) (workspace.Workspace, error)
	// AdmitFirstWrite returns the error that WriteState would return for a
	// workspace called ref that does not exist yet, and nil when the write could
	// go on. It writes nothing.
	AdmitFirstWrite(ctx context.Context, ref workspace.Ref, in workspace.StreamInput) error
	// AdmitFirstLock returns the error that LockWithIDForJob would return for a
	// workspace called ref that does not exist yet, and nil when the lock could
	// go on. It writes nothing.
	AdmitFirstLock(ctx context.Context, ref workspace.Ref, req workspace.LockRequest) error
	// LockWithIDForJob locks the workspace for holder under lockID, the ID the
	// client chose (ADR 49, decision 6). jobID is the job whose job token made
	// the request, or empty: a job whose phase started before a manual lock
	// passes that lock.
	LockWithIDForJob(ctx context.Context, id workspace.ID, req workspace.LockRequest) (workspace.Workspace, error)
	// UnlockLock releases holder's lock when it is lockID (ADR 49, decision 6). A
	// lock that changed after the caller read the unit is refused, not released.
	// On a free workspace, a request that repeats the last normal release of
	// holder succeeds without a change.
	UnlockLock(ctx context.Context, id workspace.ID, lockID, holder string) (workspace.Workspace, error)
	// RepeatsRelease reports whether q repeats the last release of w, at the
	// time of the clock of the service (see workspace.Workspace.RepeatsRelease).
	RepeatsRelease(w workspace.Workspace, q workspace.ReleaseRepeat) bool
	// ForceUnlockLock releases the lock expect names, whoever holds it, writing
	// the fence version of a unit with state (ADR 44, decision 1). It reports an
	// unfenced release.
	ForceUnlockLock(
		ctx context.Context, id workspace.ID, expect workspace.Lock, by string,
		allow namespace.Overrides, record func() error, entry audit.Entry,
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
	// Audit receives the entry of each override a write uses. It is required.
	Audit audit.Store
	// Gate bounds concurrent large bodies by bytes held at once.
	Gate *bodygate.Gate
	// OnAuditFailure runs when an audit append fails. Nil means no counter.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// StateGuard checks each LOCK, each force-unlock and each state version
	// with no lock against the access limits and the gates of state.write.
	// It is required. A server whose store keeps no gates gives the guard of
	// stategate.Off with its reason.
	StateGuard stategate.Guard
	// Placer chooses the collection of each new workspace from the first
	// placement rule that matches it. Nil means the server keeps no placement
	// rules: it then refuses to make a workspace.
	Placer *tofuunit.Placer
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
	// guard checks the declared actions of the routes and writes the entry of
	// each refusal.
	guard  *refusal.Recorder
	gate   *bodygate.Gate
	mux    *patternMux
	routes []Route
	// onAuditFailure counts a download whose audit entry was lost. It may be nil.
	onAuditFailure func()
	// admit asks the state.write guard before each checked write.
	admit  *stategate.Admitter
	placer *tofuunit.Placer
}

// call is the authorized request that a route handler serves.
type call struct {
	principal identity.Principal
	ref       workspace.Ref
	// request is the HTTP request, which the refusal audit entry reads.
	request *http.Request
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
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
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

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	admit, err := stategate.New(cfg.StateGuard, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("%w: state guard: %w", ErrMissingDependency, err)
	}

	h := &Handler{
		authn:      cfg.Authenticator,
		authz:      cfg.Authorizer,
		logger:     cfg.Logger,
		ws:         cfg.Workspaces,
		namespaces: cfg.Namespaces,
		audit:      cfg.Audit,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("tofu-http"),
		gate:       cfg.Gate,
		mux:        newPatternMux(),
		routes:     nil,

		onAuditFailure: cfg.OnAuditFailure,
		admit:          admit,
		placer:         cfg.Placer,
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

// Routes returns a copy of the route table. The Actions lists are copied
// too, so callers cannot change handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, route := range h.routes {
		out[i] = route
		out[i].Actions = slices.Clone(route.Actions)
	}

	return out
}

// RegisteredPatterns returns a copy of every ServeMux pattern the handler
// serves, in registration order.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
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
			if authErr := h.guard.Authorize(ctx, r, p, action, obj); authErr != nil {
				h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, authErr))

				return
			}
		}

		route.Serve(w, r, call{principal: p, ref: ref, request: r})
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
	return tofuunit.Object(ref)
}

// writeError answers an error with a plain status and a message that holds no
// driver or request detail. The log keeps the status, a cause from the closed
// list and the request ID.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	status, message := classify(err)
	if status == http.StatusInternalServerError {
		faultlog.LogInternal(ctx, h.logger, "request failed", faultlog.Cause(err), "")
	}

	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="OpenDustpan"`)
	}

	switch {
	case status == http.StatusServiceUnavailable && errors.Is(err, namespace.ErrUnavailable):
		problem.SetStoreRetryAfter(w)
	case status == http.StatusServiceUnavailable:
		bodygate.SetRetryAfterJitter(w)
	default:
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

	if status, message, ok := classifyPlacement(err); ok {
		return status, message
	}

	if status, message, ok := classifyNamespace(err); ok {
		return status, message
	}

	return http.StatusInternalServerError, "internal error"
}

// classifyPlacement maps the errors of the placement of a unit to a status. The
// message names the cause and the step that helps. ok is false for any other
// error.
func classifyPlacement(err error) (int, string, bool) {
	switch {
	case errors.Is(err, collection.ErrNoPlacement):
		return http.StatusUnprocessableEntity, err.Error(), true
	case errors.Is(err, tofuunit.ErrPlacementOff):
		return http.StatusNotImplemented, tofuunit.ErrPlacementOff.Error(), true
	case errors.Is(err, collection.ErrNotFound):
		return http.StatusConflict, err.Error(), true
	case errors.Is(err, collection.ErrNoCollection):
		return http.StatusConflict, collection.ErrNoCollection.Error(), true
	default:
		return 0, "", false
	}
}

// classifyNamespace maps the namespace errors of a write to a status. The
// message is the fixed text of the error, never the wrapped detail. ok is
// false for any other error.
func classifyNamespace(err error) (int, string, bool) {
	switch {
	case errors.Is(err, namespace.ErrFenced):
		return http.StatusConflict, namespace.ErrFenced.Error(), true
	case errors.Is(err, namespace.ErrNotHome):
		return http.StatusMisdirectedRequest, namespace.ErrNotHome.Error(), true
	case errors.Is(err, maintenance.ErrInMaintenance):
		return http.StatusServiceUnavailable, maintenance.ErrInMaintenance.Error() + ": try again after the Retry-After time", true
	case errors.Is(err, schema.ErrBinaryBelowMinimum):
		if below, ok := errors.AsType[*schema.MinimumError](err); ok {
			return http.StatusServiceUnavailable, below.WriteText(), true
		}

		return http.StatusServiceUnavailable, schema.ErrBinaryBelowMinimum.Error(), true
	case errors.Is(err, namespace.ErrUnavailable):
		return http.StatusServiceUnavailable, namespace.ErrUnavailable.Error() + ": try again after the Retry-After time", true
	default:
		return 0, "", false
	}
}

// classifyAccess maps the errors of the sign-in, the names and the lock to a
// status. ok is false for any other error.
func classifyAccess(err error) (int, string, bool) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return http.StatusTooManyRequests, "too many failed sign-ins from this address: try again later", true
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

// The texts of a refused write or unlock. Each names the cause and the step
// that helps. They are fixed texts, so no text of the store reaches the
// client.
const (
	notLockedText = "workspace is not locked: send LOCK first (OpenTofu sends it when locking is on). " +
		"To write without the lock, turn on the unlocked-write switch of the namespace " +
		"(PUT /api/server/namespaces/{namespace}/override-switches) and of the workspace " +
		"(PUT /api/v2/workspaces/{id}/override-switches), and hold unit:write-unlocked"
	forcedText = "write refused: a forced write needs the forced-write switch of the namespace " +
		"(PUT /api/server/namespaces/{namespace}/override-switches) and of the workspace " +
		"(PUT /api/v2/workspaces/{id}/override-switches) on, and unit:write-forced"
	clientEncryptedText = "unlock refused: the state is encrypted by the client, so the server cannot fence it. " +
		"Let the run that holds the lock finish, or force-unlock with unit:write-forced and both " +
		"forced-write switches on"
	overrideText = "write refused: an override is off or not granted"
)

// refusedWriteText returns the text of a 403 for a write or an unlock that
// the lock or an override refuses: the client-encrypted state, the forced
// write, the write without the lock, or else the general text.
func refusedWriteText(err error) string {
	override, _ := workspace.RefusedOverride(err)

	switch {
	case errors.Is(err, workspace.ErrClientEncrypted):
		return clientEncryptedText
	case override == workspace.OverrideForced:
		return forcedText
	case override == workspace.OverrideUnlocked, errors.Is(err, workspace.ErrNotLocked):
		return notLockedText
	default:
		return overrideText
	}
}

// classifyState maps the errors of a state write to a status. ok is false for
// any other error.
func classifyState(err error) (int, string, bool) {
	switch {
	case errors.Is(err, workspace.ErrBudgetTooSmall):
		return http.StatusRequestEntityTooLarge, err.Error(), true
	case errors.Is(err, workspace.ErrBusy):
		return http.StatusServiceUnavailable, "server is busy: try again after the Retry-After time", true
	case errors.Is(err, workspace.ErrConcurrentModification), errors.Is(err, workspace.ErrLockMismatch),
		errors.Is(err, workspace.ErrStaleSerial), errors.Is(err, workspace.ErrLineageMismatch):
		return http.StatusConflict, "state conflicts with the current state or lock", true
	case errors.Is(err, workspace.ErrNotLocked), errors.Is(err, workspace.ErrOverrideRefused),
		errors.Is(err, workspace.ErrClientEncrypted):
		return http.StatusForbidden, refusedWriteText(err), true
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
