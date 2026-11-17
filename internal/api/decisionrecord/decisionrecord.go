package decisionrecord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Config holds the dependencies of a Handler.
type Config struct {
	Namespaces    *namespace.Service
	Records       Records
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses.
	// Required.
	Audit audit.Store
	// OnAuditFailure, when set, runs once for each refusal whose entry could
	// not be written, so the server can count it.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for
	// all of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
	Logger   *slog.Logger
}

// Handler serves the decision record read API under /api/dustpan/v1/.
type Handler struct {
	namespaces *namespace.Service
	svc        *Service
	authn      identity.Authenticator
	sessions   *sessionauth.Checker
	guard      *refusal.Recorder
	logger     *slog.Logger
	mux        *route.Mux
	routes     []Route
}

// NewHandler returns a Handler. Every Config field except Sessions,
// OnAuditFailure and DenialLimiter is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Records == nil:
		return nil, fmt.Errorf("%w: records", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	svc, err := NewService(cfg.Records)
	if err != nil {
		return nil, err
	}

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	h := &Handler{
		namespaces: cfg.Namespaces,
		svc:        svc,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("decision-records"),
		logger:     cfg.Logger,
		mux:        route.NewMux(),
		routes:     nil,
	}

	h.routes = h.buildRoutes()
	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Method+" "+rt.Path, h.enforce(rt))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A path with an empty ID matches no route pattern, so the mux would
	// answer its own plain 404. The answer here is the one 404 of every
	// unreadable ID, with the same body. It names no record and reads no
	// store.
	if emptyIDPath(r.URL.Path) {
		h.writeError(r.Context(), w, errNotFound)
		return
	}

	h.mux.ServeHTTP(w, r)
}

// errNotFound is the one answer for every read that the caller may not make:
// an unknown ID, a malformed ID, a foreign record, a missing namespace and a
// caller without the read action. The answer never tells that a record exists.
var errNotFound = errors.New("no decision record that you can read has this ID")

// writeError answers err. A store that does not answer gives 503 with the
// code gate.unavailable and no record content. Every other error gives the
// one 404 with the code gate.not-found and the same body, so a caller learns
// nothing about a record that the caller may not read: a denied read, an
// unauthenticated caller, a malformed ID and a foreign or missing record all
// answer alike.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, gate.ErrUnavailable) || errors.Is(err, namespace.ErrUnavailable) {
		problem.WriteJSON(ctx, h.logger, w, http.StatusServiceUnavailable, problem.Body{
			Message: "the decision record store is not available", Code: http.StatusServiceUnavailable,
			ErrorCode: errcode.GateUnavailable, ErrorArgs: nil, Reauthenticate: "", Units: nil,
		})
		return
	}

	problem.WriteJSON(ctx, h.logger, w, http.StatusNotFound, problem.Body{
		Message: errNotFound.Error(), Code: http.StatusNotFound, ErrorCode: errcode.GateNotFound,
		ErrorArgs: nil, Reauthenticate: "", Units: nil,
	})
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}

// readNamespaceRecord serves the namespace route: it reads one stored record
// of the namespace. The wrapper already allowed namespace:read and
// namespace:read-gates on the namespace. An unknown namespace answers the one
// 404.
func (h *Handler) readNamespaceRecord(w http.ResponseWriter, r *http.Request, _ identity.Principal, t target) {
	ctx := r.Context()

	// Defense in depth: the wrapper authorizes on the namespace object, and
	// the store selects by namespace, so an unknown namespace reads no row.
	// This check stays so that a record of a namespace that no longer exists
	// is never served. A store that does not answer is 503, any other error
	// is the one 404.
	if _, err := h.namespaces.Get(ctx, t.ns); err != nil {
		if errors.Is(err, namespace.ErrUnavailable) {
			h.writeError(ctx, w, err)
			return
		}

		h.writeError(ctx, w, errNotFound)

		return
	}

	rec, err := h.svc.Read(ctx, t.scope, t.id)
	if err != nil {
		h.writeError(ctx, w, err)
		return
	}

	h.writeJSON(ctx, w, http.StatusOK, rec)
}

// readInstallationRecord serves the installation route: it reads one stored
// record of an access limit on an installation object. The wrapper already
// allowed installation:read-gates on the installation.
func (h *Handler) readInstallationRecord(w http.ResponseWriter, r *http.Request, _ identity.Principal, t target) {
	ctx := r.Context()

	rec, err := h.svc.Read(ctx, t.scope, t.id)
	if err != nil {
		h.writeError(ctx, w, err)
		return
	}

	h.writeJSON(ctx, w, http.StatusOK, rec)
}

// emptyIDPath reports whether path is a route path of this API with an empty
// record ID: the path ends in "decision-records/".
func emptyIDPath(path string) bool {
	const (
		installation = "/api/dustpan/v1/decision-records/"
		nsPrefix     = "/api/dustpan/v1/namespaces/"
		nsSuffix     = "/decision-records/"
	)

	if path == installation {
		return true
	}

	rest, ok := strings.CutPrefix(path, nsPrefix)
	if !ok {
		return false
	}

	name, ok := strings.CutSuffix(rest, nsSuffix)

	return ok && name != "" && !strings.Contains(name, "/")
}
