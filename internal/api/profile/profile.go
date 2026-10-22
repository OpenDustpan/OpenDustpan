// Package profile serves the tool-neutral REST API of profiles under
// /api/dustpan/v1/namespaces/{namespace}/profiles. It is an HTTP adapter with
// its own route table. It never imports another API adapter: tools share
// concepts through the core alone.
package profile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/profile"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	// v1Prefix is the prefix of the tool-neutral REST API.
	v1Prefix = "/api/dustpan/v1"
	// channelHeader says where a request came from, for the audit entry.
	channelHeader = "Dustpan-Channel"
)

// Config holds the dependencies of a Handler. Every field except Now and
// Sessions is required.
type Config struct {
	Profiles      *profile.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses. Nil
	// writes none.
	Audit audit.Store
	// Namespaces reads the namespace of an entry that the shared denial limiter
	// flushes for a key of another adapter. Nil makes such a flush entry fail
	// with a count in OnAuditFailure.
	Namespaces refusal.Namespaces
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
	// SessionPolicy gives the token freshness window of the session policy.
	// Nil means session.DefaultTokenFreshness.
	SessionPolicy SessionPolicy
	// Idempotency serves the Idempotency-Key header. Nil makes every request
	// with the header answer 400.
	Idempotency *idempotent.Middleware
	Logger      *slog.Logger
	Now         func() time.Time
}

// Handler serves the profile API.
type Handler struct {
	profiles *profile.Service
	authn    identity.Authenticator
	authz    identity.Authorizer
	guard    *refusal.Recorder
	sessions *sessionauth.Checker
	policy   SessionPolicy
	idem     *idempotent.Middleware
	logger   *slog.Logger
	mux      *route.Mux
	routes   []Route
}

// NewHandler returns a Handler.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Profiles == nil:
		return nil, fmt.Errorf("%w: profile service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		profiles: cfg.Profiles,
		authn:    cfg.Authenticator,
		authz:    cfg.Authorizer,
		guard: refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure).
			WithLimiter(cfg.DenialLimiter).
			WithSurface("profile"),
		sessions: cfg.Sessions,
		policy:   cfg.SessionPolicy,
		idem:     cfg.Idempotency,
		logger:   cfg.Logger,
		mux:      route.NewMux(),
		routes:   nil,
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

// writeError answers err through the shared error module of the
// tool-neutral APIs, with the classes of the profile service.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, Classify: classifyProfile}.Write(ctx, w, err)
}

// classifyProfile maps the errors of the profile service and of the fresh
// sign-in that a profile change needs. It runs before the shared classes
// other than the credential refusals, so a routing refusal, which wraps
// identity.ErrDenied, keeps its own code.
func classifyProfile(err error) (problem.Problem, bool) {
	if p, ok := classifyFresh(err); ok {
		return p, true
	}

	switch {
	case errors.Is(err, profile.ErrNotFound):
		return profileProblem(http.StatusNotFound, "profile.not-found", err.Error()), true
	case errors.Is(err, profile.ErrSchemaVersion):
		return profileProblem(http.StatusUnprocessableEntity, "profile.schema-version", err.Error()), true
	case errors.Is(err, profile.ErrLimit):
		return profileProblem(http.StatusUnprocessableEntity, "profile.limit", err.Error()), true
	case errors.Is(err, profile.ErrInvalid):
		return profileProblem(http.StatusBadRequest, "profile.invalid", err.Error()), true
	case errors.Is(err, profile.ErrConflict):
		return profileProblem(http.StatusConflict, "profile.conflict", err.Error()), true
	default:
		return problem.Pass()
	}
}

// classifyFresh maps the refusals of a change that needs a fresh sign-in,
// and of a routing change.
func classifyFresh(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errFreshStepRequired), errors.Is(err, profile.ErrFreshRequired):
		// A request with no console session, or a reveal with no fresh
		// authentication check: fail closed.
		return profileProblem(
			http.StatusForbidden, sessionauth.FreshStepRequiredCode, sessionauth.FreshStepRequiredMessage,
		), true
	case errors.Is(err, errReauthenticate):
		p := profileProblem(http.StatusUnauthorized, sessionauth.FreshStepRequiredCode, err.Error())
		if old, ok := errors.AsType[reauthenticateError](err); ok {
			p.Reauthenticate = old.path
		}

		return p, true
	}

	if routing, ok := errors.AsType[*profile.RoutingError](err); ok {
		return profileProblem(http.StatusForbidden, "profile.routing-change-needs-open", routing.Error()), true
	}

	return problem.Pass()
}

// profileProblem returns the answer of a profile error with no arguments.
func profileProblem(status int, code, message string) problem.Problem {
	return problem.Problem{Status: status, Code: code, Message: message, Args: nil, RetryAfter: 0, Reauthenticate: ""}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}
