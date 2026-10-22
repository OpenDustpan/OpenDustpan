// Package profile serves the tool-neutral REST API of profiles under
// /api/dustpan/v1/namespaces/{namespace}/profiles. It is an HTTP adapter with
// its own route table. It never imports another API adapter: tools share
// concepts through the core alone.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	header := r.Header.Get("Authorization")
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

// errorResponse is the error body of the tool-neutral APIs. ErrorCode is a
// stable machine code.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
	// ErrorCode is a stable machine code.
	ErrorCode string `json:"errorCode,omitempty"`
	// Reauthenticate is the path that starts a new sign-in, for the refusal
	// of an old sign-in.
	Reauthenticate string `json:"reauthenticate,omitempty"`
}

// statusError is an error with a fixed answer.
type statusError struct {
	status  int
	code    string
	message string
}

func (e *statusError) Error() string { return e.message }

func badRequest(code, message string) error {
	return &statusError{status: http.StatusBadRequest, code: code, message: message}
}

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	pr := classify(err)
	if pr.status == http.StatusInternalServerError {
		// The log names the status only: the text may hold driver detail.
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", pr.status))
	}

	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	w.Header().Set("Cache-Control", "no-store")
	var reauthenticate string
	if old, ok := errors.AsType[reauthenticateError](err); ok {
		reauthenticate = old.path
	}

	h.writeJSON(ctx, w, pr.status, errorResponse{
		Code: pr.status, Message: pr.message, ErrorCode: pr.code, Reauthenticate: reauthenticate,
	})
}

// problem is the answer to an error: a status, a stable code and a message
// that is safe to show callers.
type problem struct {
	status  int
	code    string
	message string
}

func classify(err error) problem {
	if pr, ok := classifyAuth(err); ok {
		return pr
	}

	if se, ok := errors.AsType[*statusError](err); ok {
		return problem{status: se.status, code: se.code, message: se.message}
	}

	if ie, ok := errors.AsType[*idempotent.Error](err); ok {
		return problem{status: ie.Status, code: ie.Code, message: ie.Message}
	}

	if errors.Is(err, idempotency.ErrLost) {
		return problem{
			status: http.StatusConflict, code: idempotent.CodeInProgress,
			message: "a newer request took this Idempotency-Key before this request ended: retry later",
		}
	}

	if pr, ok := classifyNamespace(err); ok {
		return pr
	}

	return classifyProfile(err)
}

func classifyNamespace(err error) (problem, bool) {
	switch {
	case errors.Is(err, namespace.ErrInvalidName):
		return problem{status: http.StatusBadRequest, code: "namespace.invalid-name", message: err.Error()}, true
	case errors.Is(err, namespace.ErrNotFound):
		return problem{status: http.StatusNotFound, code: "namespace.not-found", message: "namespace not found"}, true
	case errors.Is(err, namespace.ErrFenced):
		return problem{
			status:  http.StatusConflict,
			code:    "namespace.fenced",
			message: namespace.ErrFenced.Error(),
		}, true
	case errors.Is(err, namespace.ErrNotHome):
		return problem{
			status: http.StatusMisdirectedRequest, code: "namespace.not-home",
			message: "namespace is not homed in this region",
		}, true
	case errors.Is(err, maintenance.ErrInMaintenance):
		return problem{
			status: http.StatusServiceUnavailable, code: "maintenance.in-maintenance",
			message: maintenance.ErrInMaintenance.Error() + ": try again later",
		}, true
	default:
		return problem{status: 0, code: "", message: ""}, false
	}
}

func classifyProfile(err error) problem {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		return problem{status: http.StatusNotFound, code: "profile.not-found", message: err.Error()}
	case errors.Is(err, profile.ErrSchemaVersion):
		return problem{status: http.StatusUnprocessableEntity, code: "profile.schema-version", message: err.Error()}
	case errors.Is(err, profile.ErrLimit):
		return problem{status: http.StatusUnprocessableEntity, code: "profile.limit", message: err.Error()}
	case errors.Is(err, profile.ErrInvalid):
		return problem{status: http.StatusBadRequest, code: "profile.invalid", message: err.Error()}
	case errors.Is(err, profile.ErrConflict):
		return problem{status: http.StatusConflict, code: "profile.conflict", message: err.Error()}
	default:
		return problem{status: http.StatusInternalServerError, code: "", message: "internal error"}
	}
}

func classifyAuth(err error) (problem, bool) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return problem{
			status: http.StatusTooManyRequests, code: "auth.rate-limited",
			message: "too many failed sign-ins from this address: try again later",
		}, true
	case errors.Is(err, identity.ErrUnauthenticated):
		return problem{status: http.StatusUnauthorized, code: "auth.unauthenticated", message: "unauthenticated"}, true
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return problem{status: http.StatusForbidden, code: errcode.Forbidden(err), message: err.Error()}, true
	case errors.Is(err, errFreshStepRequired), errors.Is(err, profile.ErrFreshRequired):
		// A request with no console session, or a reveal with no fresh
		// authentication check: fail closed.
		return problem{
			status: http.StatusForbidden, code: sessionauth.FreshStepRequiredCode,
			message: sessionauth.FreshStepRequiredMessage,
		}, true
	case errors.Is(err, errReauthenticate):
		return problem{
			status: http.StatusUnauthorized, code: sessionauth.FreshStepRequiredCode, message: err.Error(),
		}, true
	}

	if routing, ok := errors.AsType[*profile.RoutingError](err); ok {
		return problem{
			status: http.StatusForbidden, code: "profile.routing-change-needs-open", message: routing.Error(),
		}, true
	}

	if errors.Is(err, identity.ErrDenied) {
		return problem{status: http.StatusForbidden, code: errcode.Forbidden(err), message: "forbidden"}, true
	}

	return problem{status: 0, code: "", message: ""}, false
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
