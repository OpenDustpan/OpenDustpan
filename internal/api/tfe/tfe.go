// Package tfe serves the HTTP API that OpenTofu speaks to its remote
// backend (the TFE-compatible API). The web console uses the same API.
//
// Service discovery, ping, the entitlement set, the workspace read, create
// and list routes, the state read routes (current state, one state
// version, its download, its outputs, the masked resource list with one
// masked resource, and the reveal of one masked value with its audit entry)
// and the state write routes (create a state version and upload its content
// through a capability) exist so far, with the masked state of a workspace
// at a time. Every further endpoint lands with its
// e2e step first, so no other route is served until its issue adds it with
// the CLI coverage.
package tfe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	// bearerScheme is the Authorization scheme the adapter reads.
	bearerScheme = "Bearer "
	// maxBodyBytes bounds a body the resolver reads before the handler.
	maxBodyBytes = 1 << 20
)

// WorkspaceService is the workspace use cases the TFE routes need: the
// workspace documents, the locks, the state reads and the state writes. Each
// part is its own small interface; workspace.Service implements all four.
type WorkspaceService interface {
	WorkspaceReader
	WorkspaceDeleter
	WorkspaceLocker
	OverrideWriter
	StateReader
	StateLocator
	StateWriter
}

// StateLocator is what the resolver of a state version route needs. It is
// apart from StateReader because it must run before authorization: it reads
// the version row only and opens no output.
type StateLocator interface {
	// StateVersionWorkspace returns the workspace of the finalized state
	// version id, or workspace.ErrStateVersionNotFound. It opens no output,
	// so the guard can authorize before any decryption.
	//
	// Defense in depth: an implementation must refuse a version that is not
	// finalized, so that a resolver never authorizes a pending version and a
	// new caller cannot skip the check. It does not rely on the later
	// ReadState of the handler.
	StateVersionWorkspace(ctx context.Context, id workspace.StateVersionID) (workspace.ID, error)
}

// WorkspaceDeleter is what the workspace delete route needs.
type WorkspaceDeleter interface {
	// Delete soft-deletes the workspace called ref (ADR 47). It returns
	// workspace.ErrLocked while the workspace is locked,
	// workspace.ErrPendingState while it has a pending state version, and
	// workspace.ErrNotFound if no live workspace has ref. The store appends
	// the audit entry e in the delete transaction (ADR 60, fail closed).
	Delete(ctx context.Context, ref workspace.Ref, e audit.Entry) error
}

// OverrideWriter is what the override switch route needs.
type OverrideWriter interface {
	// SetOverrides replaces the override switches of the unit (ADR 44,
	// decision 6).
	SetOverrides(ctx context.Context, id workspace.ID, ov namespace.Overrides) (workspace.Workspace, error)
}

// WorkspaceReader is what the workspace routes need: create, read and list.
type WorkspaceReader interface {
	// Create creates an unlocked workspace without state.
	Create(ctx context.Context, ref workspace.Ref, p workspace.Placement) (workspace.Workspace, error)
	// Get returns the workspace called ref, or workspace.ErrNotFound.
	Get(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
	// GetByID returns the workspace with id, or workspace.ErrNotFound.
	GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error)
	// List returns the namespace's workspaces, ordered by name.
	List(ctx context.Context, ns namespace.Name) ([]workspace.Workspace, error)
	// Query returns one page of the namespace's workspaces that match q, in
	// the order of List, with the count of every match.
	Query(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error)
}

// WorkspaceLocker is what the lock routes need.
type WorkspaceLocker interface {
	// LockForJobInCollection locks the workspace for holder, the subject of the
	// calling principal. jobID is the job whose job token made the request, or
	// empty: a job whose phase started before a manual lock passes that lock.
	// A workspace that no collection holds takes no lock: the error matches
	// collection.ErrNoCollection.
	LockForJobInCollection(
		ctx context.Context,
		id workspace.ID,
		holder, reason, jobID string,
	) (workspace.Workspace, error)
	// Unlock releases holder's lock.
	Unlock(ctx context.Context, id workspace.ID, holder string) (workspace.Workspace, error)
	// ForceUnlockLock releases the lock whoever holds it, when it matches
	// expect (see workspace.Service.ForceUnlockLock). The bool result reports
	// an unfenced release under the forced-write override (ADR 44, decision 4).
	// entry holds the request fields of the audit entry of the release.
	ForceUnlockLock(
		ctx context.Context, id workspace.ID, expect workspace.Lock, by string, allow namespace.Overrides,
		record func() error, entry audit.Entry,
	) (workspace.Workspace, bool, error)
}

// StateReader is what the state read routes and the current-state-version
// relationship need.
type StateReader interface {
	// Current returns the workspace's current state version, or
	// workspace.ErrNoState.
	Current(ctx context.Context, id workspace.ID) (workspace.StateVersion, error)
	// ReadState returns the finalized state version with id, or
	// workspace.ErrStateVersionNotFound when it does not exist or is not
	// finalized.
	ReadState(ctx context.Context, id workspace.StateVersionID) (workspace.StateVersion, error)
	// Open returns the content of sv. The caller closes it.
	Open(ctx context.Context, sv workspace.StateVersion) (io.ReadCloser, error)
	// History returns the finalized state versions of the workspace, newest
	// first, with the total count of finalized versions.
	History(ctx context.Context, id workspace.ID, page workspace.Page) ([]workspace.StateVersion, int, error)
	// ReadResources returns one page of the masked resource list of the
	// finalized state version id. A state the client encrypted fails with
	// workspace.ErrClientEncrypted. Size is the page size, or zero for the
	// default page; cursor is "" or the NextCursor of an earlier page.
	// Filter is a substring of the address or of the resource type, or ""
	// for no filter: it never matches attribute values. Paging applies after
	// the filter, so a page holds only matching resources.
	ReadResources(
		ctx context.Context, id workspace.StateVersionID, cursor string, size int, filter string,
	) (workspace.StatePage, error)
	// ReadResource returns one resource of the finalized state version id
	// with its instances masked. The address matches verbatim. A state the
	// client encrypted fails with workspace.ErrClientEncrypted; an unknown
	// address fails with workspace.ErrResourceNotFound.
	ReadResource(
		ctx context.Context, id workspace.StateVersionID, address string,
	) (workspace.StateDetail, error)
	// ReadStateAt returns the masked state that the workspace held at the
	// time req.At, with the state version and its actor. A time before the first
	// finalized state version fails with workspace.ErrNoStateAt, and the
	// text names the time of the first one. A state the client encrypted
	// answers as opaque, with no resource. The cursor, the size and the filter
	// of req work as in ReadResources.
	ReadStateAt(ctx context.Context, id workspace.ID, req workspace.StateAtRequest) (workspace.StateAtRead, error)
	// DiffStates returns the diff of two finalized state versions: to is the
	// newer side, from is the older one. Both versions must belong to the
	// same workspace; a from version of another workspace fails with
	// workspace.ErrStateVersionNotFound as if it did not exist. A state
	// either side encrypted fails with workspace.ErrClientEncrypted.
	// Cursor is "" or the NextCursor of an earlier page; size is the page
	// size, or zero for the default page.
	DiffStates(
		ctx context.Context, to, from workspace.StateVersionID, cursor string, size int,
	) (workspace.StateDiff, error)
}

// StateWriter is what the state write routes need: declare a version,
// upload its content and restore an old version. The CLI uploads the state
// itself and its JSON rendering through separate single-use capabilities
// minted at creation.
type StateWriter interface {
	// BeginStateInCollection declares a new state version for the workspace.
	// The caller must hold the lock; holder is the subject of the calling
	// principal. It returns an error that matches collection.ErrNoCollection
	// when no collection holds the workspace. The interface has no unchecked
	// declare, so a handler cannot skip the collection check.
	BeginStateInCollection(
		ctx context.Context,
		id workspace.ID,
		holder string,
		in workspace.NewState,
		auth workspace.Authority,
	) (workspace.PendingState, error)
	// Upload stores the content of a pending state version and makes it
	// current.
	Upload(
		ctx context.Context,
		id workspace.StateVersionID,
		tok token.Token,
		content io.Reader,
	) (workspace.StateVersion, error)
	// CheckUpload reports whether the capability tok may upload state version
	// id now, without reading any body and without writing anything. On
	// success it also returns the namespace of the workspace of the version.
	CheckUpload(
		ctx context.Context, id workspace.StateVersionID, tok token.Token,
	) (namespace.Name, error)
	// CheckJSONRenderingUpload does the same for the JSON rendering upload.
	CheckJSONRenderingUpload(
		ctx context.Context, id workspace.StateVersionID, tok token.Token,
	) (namespace.Name, error)
	// DiscardJSONRendering accepts the JSON rendering uploaded with a state
	// version and discards it: the server keeps the state itself and the
	// outputs declared at creation.
	DiscardJSONRendering(
		ctx context.Context,
		id workspace.StateVersionID,
		tok token.Token,
		content io.Reader,
	) error
	// ReadRollbackChange reads what a restore of the version to changes in the
	// workspace: the current version, the count of resources in the restored
	// state and the addresses that the restore removes. It fails when the
	// server cannot read either state.
	ReadRollbackChange(
		ctx context.Context, id workspace.ID, to workspace.StateVersionID,
	) (workspace.RollbackChange, error)
	// RestoreExpecting works as Restore, and writes only if expectCurrent is
	// still the current version; otherwise it fails with
	// workspace.ErrConcurrentModification.
	RestoreExpecting(
		ctx context.Context,
		id workspace.ID,
		from workspace.StateVersionID,
		holder string,
		expectCurrent workspace.StateVersionID,
	) (workspace.StateVersion, error)
	// Restore makes the finalized state version from current again as a new,
	// forced version. The workspace must be unlocked or locked by holder.
	Restore(
		ctx context.Context,
		id workspace.ID,
		from workspace.StateVersionID,
		holder string,
	) (workspace.StateVersion, error)
}

// AddressGate checks the client address against the IP allowlist of a
// namespace. Nil means no check.
type AddressGate interface {
	CheckAddress(ctx context.Context, ns namespace.Name) error
}

// Config holds the dependencies of a Handler.
type Config struct {
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	AddressGate   AddressGate
	Logger        *slog.Logger
	Namespaces    *namespace.Service
	// Workspaces serves the workspace and state routes. It is required:
	// routes by ID resolve the ID through it.
	Workspaces WorkspaceService
	// OutputChecker decides each read of outputs on the outputs routes. It is
	// optional so that a store without the output sharing ports still serves
	// the rest of the API. With nil, the outputs routes answer 500 and no
	// output: they fail closed.
	OutputChecker outputshare.Checker
	// BaseURL reports the external base URL for a request, for absolute
	// URLs. The zero value derives it from each request.
	BaseURL baseurl.Resolver
	// Audit is the audit store that the reveal call, the state download and
	// the single output route append to. Nil means no audit store is
	// configured: the routes answer without an entry.
	Audit audit.Store
	// OnAuditFailure runs when an audit append fails. The server wires it
	// to the dustpan_audit_append_failures_total counter. Nil means no
	// counter: the routes still fail closed with a 500 and no value.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// Gate bounds concurrent large bodies by bytes held at once.
	Gate *bodygate.Gate
	// MaxStateBytes bounds one OpenTofu state version. Zero means
	// workspace.DefaultMaxStateBytes.
	MaxStateBytes int64
	// Registry holds the settings of the provider registry protocol. A zero
	// value serves no registry route.
	Registry *RegistryConfig
	// Sessions checks the console session cookie of a request with no
	// Authorization header, on the routes that the console calls. Nil accepts
	// access tokens only, so a route that needs a fresh authentication refuses
	// every request.
	Sessions *sessionauth.Checker
	// SessionPolicy gives the token freshness window of the session policy.
	// Nil means session.DefaultTokenFreshness.
	SessionPolicy SessionPolicy
	// StateGuard checks each write of state against the access limits and
	// the gates of state.write. It is required. A server whose store keeps
	// no gates gives the guard of stategate.Off with its reason.
	StateGuard stategate.Guard
	// Placer chooses the collection of each new workspace from the first
	// placement rule that matches it. Nil means the server keeps no placement
	// rules: it then refuses to make a workspace.
	Placer *tofuunit.Placer
}

// Handler serves the TFE-compatible API under /api/v2/, with service
// discovery at /.well-known/terraform.json.
type Handler struct {
	authn          identity.Authenticator
	sessions       *sessionauth.Checker
	sessionPolicy  SessionPolicy
	authz          identity.Authorizer
	addressGate    AddressGate
	logger         *slog.Logger
	namespaces     *namespace.Service
	workspaces     WorkspaceService
	outputs        outputshare.Checker
	baseURL        baseurl.Resolver
	audit          audit.Store
	onAuditFailure func()
	// guard checks the declared actions of the routes and writes the entry of
	// each refusal.
	guard    *refusal.Recorder
	gate     *bodygate.Gate
	maxState int64
	// registry is nil when the handler serves no provider registry protocol.
	registry *registryState
	// admit asks the state.write guard before each checked write.
	admit  *stategate.Admitter
	placer *tofuunit.Placer
	// freshGuard asks for a fresh authentication when the policy requires
	// one. NewHandler sets a guard that always requires the step (fail
	// closed); SetFreshPolicy replaces it.
	freshGuard freshstep.Guard
	mux        *patternMux
	routes     []Route
}

// newGuards returns the recorder of the refusals and the admission of the
// writes of state of cfg. A missing state guard is ErrMissingDependency.
func newGuards(cfg Config) (*refusal.Recorder, *stategate.Admitter, error) {
	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, nil, fmt.Errorf("denial recorder: %w", err)
	}

	admit, err := stategate.New(cfg.StateGuard, cfg.Logger)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: state guard: %w", ErrMissingDependency, err)
	}

	return recorder, admit, nil
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
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Workspaces == nil:
		return nil, fmt.Errorf("%w: workspace service", ErrMissingDependency)
	}

	reg, err := newRegistryState(cfg.Registry)
	if err != nil {
		return nil, err
	}

	recorder, admit, err := newGuards(cfg)
	if err != nil {
		return nil, err
	}

	h := &Handler{
		registry:       reg,
		authn:          cfg.Authenticator,
		sessions:       cfg.Sessions,
		sessionPolicy:  cfg.SessionPolicy,
		authz:          cfg.Authorizer,
		addressGate:    cfg.AddressGate,
		logger:         cfg.Logger,
		namespaces:     cfg.Namespaces,
		workspaces:     cfg.Workspaces,
		outputs:        cfg.OutputChecker,
		baseURL:        cfg.BaseURL,
		audit:          cfg.Audit,
		onAuditFailure: cfg.OnAuditFailure,
		guard:          recorder.WithLimiter(cfg.DenialLimiter).WithSurface("tfe"),
		gate:           cfg.Gate,
		maxState:       cfg.MaxStateBytes,
		admit:          admit,
		placer:         cfg.Placer,
		freshGuard:     freshstep.Guard{},
		mux:            newPatternMux(),
		routes:         nil,
	}

	// A handler with no SetFreshPolicy call requires the fresh step.
	h.SetFreshPolicy(nil)

	if h.gate == nil {
		h.gate = bodygate.New(0)
	}

	if h.maxState == 0 {
		h.maxState = workspace.DefaultMaxStateBytes
	}

	h.routes = h.buildRoutes()
	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// ServeHTTP implements http.Handler. A path the route table does not hold
// answers 404 as a JSON:API error, so callers can tell it reached this
// adapter.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	catch := &notFoundWriter{
		ResponseWriter: w,
		notFound:       false,
		registry: strings.HasPrefix(r.URL.Path, registryPrefix) || strings.HasPrefix(r.URL.Path, modulesPrefix) ||
			strings.HasPrefix(r.URL.Path, mirrorPrefix),
	}

	h.mux.ServeHTTP(catch, r)

	if !catch.notFound {
		return
	}

	w.Header().Del("Content-Length")

	// A path of the registry protocol answers in the registry error document, so
	// that a client of the protocol meets one error shape.
	if catch.registry {
		h.writeRegistryError(r.Context(), w, http.StatusNotFound, notFoundDetail, errcode.ServerRouteNotFound)

		return
	}

	h.writeClassError(r.Context(), w, errorClass{
		status: http.StatusNotFound, title: notFoundTitle, detail: notFoundTitle,
		code: errcode.ServerRouteNotFound, args: nil,
	})
}

// ExternalBaseURL returns the external base URL for r: the --public-url
// value when one is set, else the scheme and host of the request.
func (h *Handler) ExternalBaseURL(r *http.Request) string {
	return h.baseURL.BaseURL(r)
}

// notFoundWriter swallows the default 404 of the mux, so ServeHTTP can
// replace it with a JSON:API error. A 404 that already carries the JSON:API
// media type comes from a route handler and passes through untouched. So does
// a 404 with the JSON media type of the registry protocol, but only on a path
// of that protocol: on any other path the JSON:API 404 replaces it.
type notFoundWriter struct {
	http.ResponseWriter

	notFound bool
	registry bool
}

// WriteHeader swallows a 404 without the JSON:API or the JSON media type and
// records it. Every other status passes through.
func (c *notFoundWriter) WriteHeader(status int) {
	media := c.Header().Get("Content-Type")
	passes := media == jsonAPIMediaType || (c.registry && media == registryMediaType)

	if status == http.StatusNotFound && !passes {
		c.notFound = true

		return
	}

	c.ResponseWriter.WriteHeader(status)
}

// Write swallows the body of a swallowed 404. Every other body passes
// through.
func (c *notFoundWriter) Write(p []byte) (int, error) {
	if c.notFound {
		return len(p), nil
	}

	return c.ResponseWriter.Write(p) //nolint:wrapcheck // Transparent wrapper: callers must see the original error.
}

// discovery serves GET /.well-known/terraform.json. It advertises tfe.v2
// and tfe.v2.1 at /api/v2/, and providers.v1 and modules.v1 when the server
// serves the registry protocols, and nothing else: the cloud block reads tfe.v2,
// the remote backend reads tfe.v2.1, and there is no login.v1 service.
// OpenTofu requires the application/json media type.
func (h *Handler) discovery(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	doc := map[string]string{"tfe.v2": "/api/v2/", "tfe.v2.1": "/api/v2/"}
	if h.registry != nil {
		doc["providers.v1"] = registryPrefix
		doc["modules.v1"] = modulesPrefix
	}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		h.logger.WarnContext(r.Context(), "write response failed", slog.String("error", err.Error()))
	}
}

// ping serves GET /api/v2/ping. It sets TFP-API-Version 2.5, the minimum
// the cloud block accepts. The body is empty: callers read the header.
func (h *Handler) ping(w http.ResponseWriter, _ *http.Request, _ identity.Principal, _ identity.Object) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Tfp-Api-Version", "2.5")
	w.WriteHeader(http.StatusOK)
}

// entitlementAttributes holds the organization entitlements OpenTofu reads.
// Operations stays false, so OpenTofu runs plans locally and uses the server
// for state and locking only.
type entitlementAttributes struct {
	Operations bool `json:"operations"`
}

// entitlementData is one entitlement set as a JSON:API resource.
type entitlementData struct {
	ID         string                `json:"id"`
	Type       string                `json:"type"`
	Attributes entitlementAttributes `json:"attributes"`
}

// entitlementResponse is the entitlement-set document.
type entitlementResponse struct {
	Data entitlementData `json:"data"`
}

// entitlementSet serves GET /api/v2/organizations/{organization}/entitlement-set.
// An unknown organization answers 404. The server never creates a namespace.
func (h *Handler) entitlementSet(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	name, err := namespace.ParseName(r.PathValue("organization"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, err := h.namespaces.Get(ctx, name); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, entitlementResponse{
		Data: entitlementData{
			ID:         name.String(),
			Type:       "entitlement-sets",
			Attributes: entitlementAttributes{Operations: false},
		},
	})
}

// authenticateRoute resolves the credential of a request to a principal. A
// route of ScopeConsole takes the console session cookie when the request has
// no Authorization header, and the Caller reports
// it. Any other request uses the access token only, so a cookie never signs in
// a route that the console does not call. A request with an Authorization
// header never uses the cookie.
func (h *Handler) authenticateRoute(
	r *http.Request, scope Scope,
) (identity.Principal, *sessionauth.Caller, error) {
	if scope == ScopeConsole && r.Header.Get("Authorization") == "" {
		p, caller, err := h.sessions.Authenticate(r)
		if err != nil {
			return p, nil, err
		}

		return p, &caller, nil
	}

	p, err := h.authenticate(r)

	return p, nil, err
}

// authenticate resolves the "Authorization: Bearer <token>" header to a
// principal.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerScheme)
	if !ok || raw == "" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return h.authn.AuthenticateToken(r.Context(), raw)
}

// unitObject maps a namespace and workspace name to its tool-neutral unit
// reference.
func unitObject(ns workspace.Ref) (identity.Object, error) {
	return tofuunit.Object(ns)
}

// enforceDiscovery serves the entitlement set: it authenticates, then answers
// when the caller may read the namespace, or when the caller holds a grant
// on a collection or on a unit in it. The answer holds only that the
// namespace exists and its entitlements. Any other caller gets the same 404
// as for an unknown namespace, with the same body, so the answer is no
// existence oracle. Unknown organizations answer through the namespace read,
// before any authorization runs. The check reads the grants at each request,
// so a revoked grant loses the discovery read on the next request.
func (h *Handler) enforceDiscovery(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		p, _, err := h.authenticateRoute(r, ScopeAuthorized)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		ns, err := namespace.ParseName(r.PathValue("organization"))
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		if _, getErr := h.namespaces.Get(ctx, ns); getErr != nil {
			h.writeError(ctx, w, getErr)

			return
		}

		h.serveDiscovery(w, r, p, ns, route)
	}
}

// serveDiscovery serves a discovery route for a namespace that exists: it
// answers when the caller may read the namespace, or when the caller holds a
// grant on a collection or on a unit in it. Any other caller gets the same
// 404 as for an unknown namespace, with the same body, so the answer is no
// existence oracle. A served answer that a grant allowed names the grant in
// the response header X-Dustpan-Grant-Id. It decides
// first and records only the final refusal, so a served request writes no
// denial entry.
func (h *Handler) serveDiscovery(
	w http.ResponseWriter, r *http.Request, p identity.Principal, ns namespace.Name, route Route,
) {
	ctx := r.Context()
	obj := identity.NamespaceObject(ns)

	err := h.guard.Decide(ctx, p, identity.ActionNamespaceRead, obj)
	if err == nil {
		route.Serve(w, r, p, obj)

		return
	}

	// Defense in depth: the IP allowlist refusal is checked first and
	// never falls through to the grant fallback, so a blocked address
	// cannot discover the namespace through a collection grant.
	if errors.Is(err, ipallow.ErrAddressNotAllowed) {
		h.denyDiscovery(ctx, r, p, obj, err)
		h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", identity.ActionNamespaceRead, err))

		return
	}

	if !errors.Is(err, identity.ErrDenied) {
		h.denyDiscovery(ctx, r, p, obj, err)
		h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", identity.ActionNamespaceRead, err))

		return
	}

	holds, grantID, holdsErr := h.holdsGrantInNamespace(ctx, p, ns)
	if holdsErr != nil {
		h.denyDiscovery(ctx, r, p, obj, holdsErr)
		h.writeError(ctx, w, holdsErr)

		return
	}

	if !holds {
		// The same 404 as for an unknown namespace: the error text is
		// what the namespace read above answers for one, so the two
		// answers do not differ.
		h.denyDiscovery(ctx, r, p, obj, err)
		h.writeError(ctx, w, fmt.Errorf("get namespace %q: %w", ns.String(), namespace.ErrNotFound))

		return
	}

	if grantID != "" {
		w.Header().Set(access.HeaderDiscoveryGrant, grantID)
	}

	route.Serve(w, r, p, obj)
}

// denyDiscovery records the denial entry of a refused discovery request.
func (h *Handler) denyDiscovery(
	ctx context.Context, r *http.Request, p identity.Principal, obj identity.Object, err error,
) {
	h.guard.Record(ctx, r, p, refusal.Denial{
		Action: identity.ActionNamespaceRead, Object: obj, Reason: refusal.ReasonFor(err),
		ReasonWithin: nil, Window: 0,
	})
}

// holdsGrantInNamespace reports whether p holds a grant on a collection in
// ns or on a unit in it, and the stable ID of one such grant (never a name).
// It reads the grants at each request, so a revoked grant is gone on the
// next request. A grant on the namespace itself, or on the installation, is
// decided by the Authorizer before this runs: this check only opens
// discovery from inside. An error of the authorizer is returned. An
// authorizer that names the grant but returns an empty ID is refused, so a
// held answer from it always has an ID. An authorizer without the method, for
// example a custom authorizer without the grant list, takes the scope path:
// the answer is held when the scope names the namespace and it carries no
// grant header.
func (h *Handler) holdsGrantInNamespace(
	ctx context.Context, p identity.Principal, ns namespace.Name,
) (bool, string, error) {
	// An administrator is decided by the Authorizer itself: a test double
	// that denies an administrator keeps the old answer below.
	if p.Admin {
		return false, "", nil
	}

	if id, found, err := access.DiscoveryGrantID(ctx, h.authz, p, ns); found || err != nil {
		if err != nil {
			return false, "", err
		}

		return id != "", id, nil
	}

	// The authorizer cannot name its grants: the scope check decides, and
	// the answer carries no grant header.
	for _, action := range []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadOutputs} {
		scope, err := h.authz.ListObjects(ctx, p, action)
		if err != nil {
			return false, "", fmt.Errorf("list readable objects: %w", err)
		}

		if scopeHoldsNamespace(scope, ns) {
			return true, "", nil
		}
	}

	return false, "", nil
}

// scopeHoldsNamespace reports whether scope names a collection in the
// namespace ns or a unit in it. A grant on the namespace itself is decided by
// the Authorizer before this runs, so scope.Namespaces does not count here.
// An every-object scope alone proves no grant in ns: only the gated namespace
// check above may open that path.
func scopeHoldsNamespace(scope identity.Scope, ns namespace.Name) bool {
	for _, objs := range [][]identity.Object{scope.Collections, scope.Units} {
		for i := range objs {
			if objs[i].Namespace() == ns {
				return true
			}
		}
	}

	return false
}
