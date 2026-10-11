// Package tfe serves the HTTP API that OpenTofu speaks to its remote
// backend (the TFE-compatible API). The web console uses the same API.
//
// Only service discovery, ping, the entitlement set and the workspace
// read, create and list routes exist so far. Every further endpoint lands
// with its e2e step first, so no other route is served until its issue adds
// it with the CLI coverage.
package tfe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/workspace"
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

// WorkspaceService is the workspace use cases the workspace routes need:
// create, read and list for the documents, lock and unlock for the lock
// routes, and the current state for the current-state-version relationship.
type WorkspaceService interface {
	// Create creates an unlocked workspace without state.
	Create(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
	// Get returns the workspace called ref, or workspace.ErrNotFound.
	Get(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error)
	// GetByID returns the workspace with id, or workspace.ErrNotFound.
	GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error)
	// List returns the namespace's workspaces, ordered by name.
	List(ctx context.Context, ns namespace.Name) ([]workspace.Workspace, error)
	// Lock locks the workspace for holder, the subject of the calling
	// principal.
	Lock(ctx context.Context, id workspace.ID, holder, reason string) (workspace.Workspace, error)
	// Unlock releases holder's lock.
	Unlock(ctx context.Context, id workspace.ID, holder string) (workspace.Workspace, error)
	// ForceUnlock releases the lock whoever holds it.
	ForceUnlock(ctx context.Context, id workspace.ID) (workspace.Workspace, error)
	// Current returns the workspace's current state version, or
	// workspace.ErrNoState.
	Current(ctx context.Context, id workspace.ID) (workspace.StateVersion, error)
}

// Config holds the dependencies of a Handler.
type Config struct {
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
	Namespaces    *namespace.Service
	// Workspaces serves the workspace routes. It is required: routes by ID
	// resolve the ID through it.
	Workspaces WorkspaceService
}

// Handler serves the TFE-compatible API under /api/v2/, with service
// discovery at /.well-known/terraform.json.
type Handler struct {
	authn      identity.Authenticator
	authz      identity.Authorizer
	logger     *slog.Logger
	namespaces *namespace.Service
	workspaces WorkspaceService
	mux        *patternMux
	routes     []Route
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
		authn:      cfg.Authenticator,
		authz:      cfg.Authorizer,
		logger:     cfg.Logger,
		namespaces: cfg.Namespaces,
		workspaces: cfg.Workspaces,
		mux:        newPatternMux(),
		routes:     nil,
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
// at /api/v2/ and nothing else: there is no login.v1 service. OpenTofu
// requires the application/json media type.
func (h *Handler) discovery(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(map[string]string{"tfe.v2": "/api/v2/"}); err != nil {
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
