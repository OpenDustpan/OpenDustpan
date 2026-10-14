// Package tfe serves the HTTP API that OpenTofu speaks to its remote
// backend (the TFE-compatible API). The web console uses the same API.
//
// Service discovery, ping, the entitlement set, the workspace read, create
// and list routes, the state read routes (current state, one state
// version, its download, its outputs, the masked resource list with one
// masked resource, and the reveal of one masked value with its audit entry)
// and the state write routes (create a state version and upload its content
// through a capability) exist so far. Every further endpoint lands with its
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

	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	// bearerScheme is the Authorization scheme the adapter reads.
	bearerScheme = "Bearer "
	// unitTool names this API's tool in unit references.
	unitTool = "opentofu"
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
	StateWriter
}

// WorkspaceDeleter is what the workspace delete route needs.
type WorkspaceDeleter interface {
	// Delete soft-deletes the workspace called ref (ADR 47). It returns
	// workspace.ErrLocked while the workspace is locked, and
	// workspace.ErrNotFound if no live workspace has ref.
	Delete(ctx context.Context, ref workspace.Ref) error
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
	Create(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
	// Get returns the workspace called ref, or workspace.ErrNotFound.
	Get(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
	// GetByID returns the workspace with id, or workspace.ErrNotFound.
	GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error)
	// List returns the namespace's workspaces, ordered by name.
	List(ctx context.Context, ns namespace.Name) ([]workspace.Workspace, error)
}

// WorkspaceLocker is what the lock routes need.
type WorkspaceLocker interface {
	// Lock locks the workspace for holder, the subject of the calling
	// principal.
	Lock(ctx context.Context, id workspace.ID, holder, reason string) (workspace.Workspace, error)
	// Unlock releases holder's lock.
	Unlock(ctx context.Context, id workspace.ID, holder string) (workspace.Workspace, error)
	// ForceUnlock releases the lock whoever holds it. The bool result reports
	// an unfenced release under the forced-write override (ADR 44, decision 4).
	ForceUnlock(
		ctx context.Context, id workspace.ID, by string, allow namespace.Overrides, record func() error,
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
	// BeginState declares a new state version for the workspace. The caller
	// must hold the lock; holder is the subject of the calling principal.
	BeginState(
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
	// id now, without reading any body and without writing anything.
	CheckUpload(ctx context.Context, id workspace.StateVersionID, tok token.Token) error
	// CheckJSONRenderingUpload does the same for the JSON rendering upload.
	CheckJSONRenderingUpload(ctx context.Context, id workspace.StateVersionID, tok token.Token) error
	// DiscardJSONRendering accepts the JSON rendering uploaded with a state
	// version and discards it: the server keeps the state itself and the
	// outputs declared at creation.
	DiscardJSONRendering(
		ctx context.Context,
		id workspace.StateVersionID,
		tok token.Token,
		content io.Reader,
	) error
	// Restore makes the finalized state version from current again as a new,
	// forced version. The workspace must be unlocked or locked by holder.
	Restore(
		ctx context.Context,
		id workspace.ID,
		from workspace.StateVersionID,
		holder string,
	) (workspace.StateVersion, error)
}

// Config holds the dependencies of a Handler.
type Config struct {
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
	Namespaces    *namespace.Service
	// Workspaces serves the workspace and state routes. It is required:
	// routes by ID resolve the ID through it.
	Workspaces WorkspaceService
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
	// Gate bounds concurrent large bodies by bytes held at once.
	Gate *bodygate.Gate
	// MaxStateBytes bounds one OpenTofu state version. Zero means
	// workspace.DefaultMaxStateBytes.
	MaxStateBytes int64
}

// Handler serves the TFE-compatible API under /api/v2/, with service
// discovery at /.well-known/terraform.json.
type Handler struct {
	authn          identity.Authenticator
	authz          identity.Authorizer
	logger         *slog.Logger
	namespaces     *namespace.Service
	workspaces     WorkspaceService
	baseURL        baseurl.Resolver
	audit          audit.Store
	onAuditFailure func()
	gate           *bodygate.Gate
	maxState       int64
	mux            *patternMux
	routes         []Route
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
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Workspaces == nil:
		return nil, fmt.Errorf("%w: workspace service", ErrMissingDependency)
	}

	h := &Handler{
		authn:          cfg.Authenticator,
		authz:          cfg.Authorizer,
		logger:         cfg.Logger,
		namespaces:     cfg.Namespaces,
		workspaces:     cfg.Workspaces,
		baseURL:        cfg.BaseURL,
		audit:          cfg.Audit,
		onAuditFailure: cfg.OnAuditFailure,
		gate:           cfg.Gate,
		maxState:       cfg.MaxStateBytes,
		mux:            newPatternMux(),
		routes:         nil,
	}

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
	catch := &notFoundWriter{ResponseWriter: w, notFound: false}

	h.mux.ServeHTTP(catch, r)

	if !catch.notFound {
		return
	}

	header := w.Header()
	header.Del("Content-Length")
	header.Set("Content-Type", jsonAPIMediaType)
	w.WriteHeader(http.StatusNotFound)

	doc := jsonAPIDocument{Errors: []jsonAPIError{{Status: "404", Title: notFoundTitle, Detail: notFoundTitle}}}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		h.logger.WarnContext(r.Context(), "write response failed", slog.String("error", err.Error()))
	}
}

// ExternalBaseURL returns the external base URL for r: the --public-url
// value when one is set, else the scheme and host of the request.
func (h *Handler) ExternalBaseURL(r *http.Request) string {
	return h.baseURL.BaseURL(r)
}

// notFoundWriter swallows the default 404 of the mux, so ServeHTTP can
// replace it with a JSON:API error. A 404 that already carries the JSON:API
// media type comes from a route handler and passes through untouched.
type notFoundWriter struct {
	http.ResponseWriter

	notFound bool
}

// WriteHeader swallows a 404 without the JSON:API media type and records
// it. Every other status passes through.
func (c *notFoundWriter) WriteHeader(status int) {
	if status == http.StatusNotFound && c.Header().Get("Content-Type") != jsonAPIMediaType {
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
// and tfe.v2.1 at /api/v2/ and nothing else: the cloud block reads tfe.v2,
// the remote backend reads tfe.v2.1, and there is no login.v1 service.
// OpenTofu requires the application/json media type.
func (h *Handler) discovery(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	doc := map[string]string{"tfe.v2": "/api/v2/", "tfe.v2.1": "/api/v2/"}

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
	return identity.UnitObject(unitTool, ns.Namespace, ns.Name.String())
}
