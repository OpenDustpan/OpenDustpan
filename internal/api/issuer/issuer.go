// Package issuer serves the OIDC issuer of the namespaces. It has two kinds of
// route. The protocol routes under /oidc/{nsid}/ answer the discovery document
// and the JWKS to anyone, outside the versioned API: their path is part of the
// issuer URL that clouds hold. The routes under
// /api/dustpan/v1/namespaces/{namespace}/issuer read and change the settings
// and the keys of a namespace, with the style of the tool-neutral API.
//
// It is an HTTP adapter with its own route table. It never imports another API
// adapter.
package issuer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	// v1Prefix is the prefix of the tool-neutral REST API.
	v1Prefix = "/api/dustpan/v1"
	// oidcPrefix is the prefix of the protocol routes. It is outside the
	// versioned API.
	oidcPrefix = "/oidc"
	// channelHeader says where a request came from, for the audit entry.
	channelHeader = "Dustpan-Channel"
	// publicMaxAge is the Cache-Control max-age of the protocol routes, in
	// seconds.
	publicMaxAge = 300
)

// Config holds the dependencies of a Handler. Every field except the ones
// marked optional is required.
type Config struct {
	Issuer        *issuersvc.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses.
	Audit audit.Store
	// Namespaces reads the namespace of an entry that the shared denial limiter
	// flushes for a key of another adapter. Optional.
	Namespaces refusal.Namespaces
	// OnAuditFailure runs once for each refusal whose entry could not be
	// written. Optional.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. Optional.
	DenialLimiter *denylimit.Limiter
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Optional: nil accepts access tokens only.
	Sessions *sessionauth.Checker
	// SessionPolicy gives the token freshness window. Optional.
	SessionPolicy SessionPolicy
	Logger        *slog.Logger
}

// Handler serves the issuer routes.
type Handler struct {
	issuer   *issuersvc.Service
	authn    identity.Authenticator
	authz    identity.Authorizer
	guard    *refusal.Recorder
	sessions *sessionauth.Checker
	policy   SessionPolicy
	logger   *slog.Logger
	mux      *route.Mux
	routes   []Route
}

// NewHandler returns a Handler.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Issuer == nil:
		return nil, fmt.Errorf("%w: issuer service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	h := &Handler{
		issuer: cfg.Issuer, authn: cfg.Authenticator, authz: cfg.Authorizer,
		guard:    recorder.WithLimiter(cfg.DenialLimiter).WithSurface("issuer"),
		sessions: cfg.Sessions, policy: cfg.SessionPolicy, logger: cfg.Logger, mux: route.NewMux(), routes: nil,
	}

	h.routes = h.buildRoutes()
	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Method+" "+rt.Path, h.enforce(rt))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: issuerErrors}.Write(ctx, w, err)
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}

// issuerErrors maps the errors of the issuer service and of the fresh sign-in
// that a change of a setting needs.
func issuerErrors(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errFreshStepRequired), errors.Is(err, profile.ErrFreshRequired):
		return problem.New(http.StatusForbidden, errcode.MFAFreshStepRequired, sessionauth.FreshStepRequiredMessage), true
	case errors.Is(err, errReauthenticate):
		p := problem.New(http.StatusUnauthorized, errcode.MFAFreshStepRequired, err.Error())

		if old, ok := errors.AsType[reauthenticateError](err); ok {
			p.Reauthenticate = old.path
		}

		return p, true
	case errors.Is(err, profile.ErrIssuerNotConfigured):
		return problem.New(http.StatusNotFound, errcode.IssuerNotConfigured, err.Error()), true
	case errors.Is(err, profile.ErrIssuerKeyMissing):
		return problem.New(http.StatusConflict, errcode.IssuerKeyMissing, err.Error()), true
	case errors.Is(err, profile.ErrIssuerKeyExpired):
		return problem.New(http.StatusConflict, errcode.IssuerKeyExpired, err.Error()), true
	case errors.Is(err, profile.ErrInvalid):
		return problem.New(http.StatusBadRequest, errcode.IssuerInvalid, textOf(err, profile.ErrInvalid)), true
	case errors.Is(err, profile.ErrConflict):
		return problem.New(http.StatusConflict, errcode.IssuerConflict, textOf(err, profile.ErrConflict)), true
	default:
		return problem.Pass()
	}
}

// resolveNamespace returns the namespace named in the path.
func resolveNamespace(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, fmt.Errorf("namespace: %w", err)
	}

	return identity.NamespaceObject(name), nil
}

// formatTime formats a time as RFC 3339 in UTC.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// textOf returns the text of err without the name of the sentinel of the
// profile package that starts it, so an answer of the issuer does not speak of
// a profile.
func textOf(err, sentinel error) string {
	return strings.Replace(err.Error(), sentinel.Error()+": ", "", 1)
}
