// Package tfe serves the HTTP API that OpenTofu speaks to its remote
// backend (the TFE-compatible API). The web console uses the same API.
//
// Only the route table and the JSON:API error shape exist
// so far. Every endpoint lands with its e2e step first, so no real route is
// served until its issue adds it with the CLI coverage.
package tfe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/identity"
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

// WorkspaceLookup resolves a workspace ID to its workspace. It is the part
// of the workspace port that the ID resolver needs.
type WorkspaceLookup interface {
	// GetWorkspaceByID returns the workspace with id, or workspace.ErrNotFound.
	GetWorkspaceByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error)
}

// Config holds the dependencies of a Handler.
type Config struct {
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Logger        *slog.Logger
	// Workspaces resolves workspace IDs. It is nil until the first route by
	// ID lands; ID routes answer 500 while it is nil.
	Workspaces WorkspaceLookup
}

// Handler serves the TFE-compatible API under /api/v2/.
type Handler struct {
	authn      identity.Authenticator
	authz      identity.Authorizer
	logger     *slog.Logger
	workspaces WorkspaceLookup
	mux        *patternMux
	routes     []Route
}

// NewHandler returns a Handler. Authenticator, Authorizer and Logger are
// required. Workspaces is optional until the first route by ID lands.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		authn:      cfg.Authenticator,
		authz:      cfg.Authorizer,
		logger:     cfg.Logger,
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

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
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
