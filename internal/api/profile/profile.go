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
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/profile"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	tokenScheme  = "token "
	bearerScheme = "Bearer "
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
	mux      *patternMux
	routes   []Route
}

// NewHandler returns a Handler.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Profiles == nil:
		return nil, fmt.Errorf("%w: profile service", ErrMissingDependency)
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
		profiles: cfg.Profiles,
		authn:    cfg.Authenticator,
		authz:    cfg.Authorizer,
		guard:    recorder.WithLimiter(cfg.DenialLimiter).WithSurface("profile"),
		sessions: cfg.Sessions,
		policy:   cfg.SessionPolicy,
		idem:     cfg.Idempotency,
		logger:   cfg.Logger,
		mux:      newPatternMux(),
		routes:   nil,
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

// authenticate resolves the Authorization header to a principal. A request
// with no header uses the console session cookie, and the Caller reports that
// the cookie signed it in. It is nil for an access token.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, *sessionauth.Caller, error) {
	header := apidesc.Header(r, "Authorization")
	if header == "" {
		p, caller, err := h.sessions.Authenticate(r)
		if err != nil {
			return p, nil, err
		}

		return p, &caller, nil
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok {
		raw, ok = strings.CutPrefix(header, bearerScheme)
	}

	if !ok || raw == "" {
		return identity.Principal{}, nil, identity.ErrUnauthenticated
	}

	p, err := h.authn.AuthenticateToken(r.Context(), raw)

	return p, nil, err
}

// writeError answers err through the shared error module of the
// tool-neutral APIs, with the classes of the profile service.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, Classify: profileErrors}.Write(ctx, w, err)
}

// profileErrors maps the errors of the profile service and of the fresh
// sign-in that a profile change needs. It runs before the shared classes
// other than the credential refusals, so a routing refusal, which wraps
// identity.ErrDenied, keeps its own code.
func profileErrors(err error) (problem.Problem, bool) {
	if p, ok := freshErrors(err); ok {
		return p, true
	}

	if routing, ok := errors.AsType[*profile.RoutingError](err); ok {
		return profileProblem(http.StatusForbidden, errcode.ProfileRoutingChangeNeedsOpen, routing.Error()), true
	}

	switch {
	case errors.Is(err, profile.ErrNotFound):
		return profileProblem(http.StatusNotFound, errcode.ProfileNotFound, err.Error()), true
	case errors.Is(err, profile.ErrSchemaVersion):
		return profileProblem(http.StatusUnprocessableEntity, errcode.ProfileSchemaVersion, err.Error()), true
	case errors.Is(err, profile.ErrLimit):
		return profileProblem(http.StatusUnprocessableEntity, errcode.ProfileLimit, err.Error()), true
	case errors.Is(err, profile.ErrInvalid):
		return profileProblem(http.StatusBadRequest, errcode.ProfileInvalid, err.Error()), true
	case errors.Is(err, profile.ErrConflict):
		return profileProblem(http.StatusConflict, errcode.ProfileConflict, err.Error()), true
	default:
		return problem.Pass()
	}
}

// freshErrors maps the errors of the fresh sign-in check: a request with no
// console session, or a reveal with no fresh authentication check, fails
// closed.
func freshErrors(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errFreshStepRequired), errors.Is(err, profile.ErrFreshRequired):
		return profileProblem(
			http.StatusForbidden,
			errcode.MFAFreshStepRequired,
			sessionauth.FreshStepRequiredMessage,
		), true
	case errors.Is(err, errReauthenticate):
		p := profileProblem(http.StatusUnauthorized, errcode.MFAFreshStepRequired, err.Error())

		if old, ok := errors.AsType[reauthenticateError](err); ok {
			p.Reauthenticate = old.path
		}

		return p, true
	default:
		return problem.Pass()
	}
}

// profileAnswer returns the answer of a profile error with no arguments.
func profileProblem(status int, code, message string) problem.Problem {
	return problem.Problem{
		Status: status, Code: code, Values: nil, Message: message, RetryAfter: 0, Reauthenticate: "",
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}
