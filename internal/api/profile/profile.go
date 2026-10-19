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
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
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
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
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
	sessions *sessionauth.Checker
	idem     *idempotent.Middleware
	logger   *slog.Logger
	mux      *patternMux
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
		sessions: cfg.Sessions,
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
// with no header uses the console session cookie, and the bool reports that
// the cookie signed it in.
func (h *Handler) authenticate(r *http.Request) (identity.Principal, bool, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		p, _, err := h.sessions.Authenticate(r)

		return p, err == nil, err
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok {
		raw, ok = strings.CutPrefix(header, bearerScheme)
	}

	if !ok || raw == "" {
		return identity.Principal{}, false, identity.ErrUnauthenticated
	}

	p, err := h.authn.AuthenticateToken(r.Context(), raw)

	return p, false, err
}

// errorResponse is the error body of the tool-neutral APIs. ErrorCode is a
// stable machine code.
type errorResponse struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode,omitempty"`
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

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, pr.status, errorResponse{Code: pr.status, Message: pr.message, ErrorCode: pr.code})
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
		return problem{status: http.StatusConflict, code: "namespace.fenced", message: "write fenced: try again"}, true
	case errors.Is(err, namespace.ErrNotHome):
		return problem{
			status: http.StatusMisdirectedRequest, code: "namespace.not-home",
			message: "namespace is not homed in this region",
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
		return problem{status: http.StatusForbidden, code: "auth.forbidden", message: err.Error()}, true
	}

	if routing, ok := errors.AsType[*profile.RoutingError](err); ok {
		return problem{
			status: http.StatusForbidden, code: "profile.routing-change-needs-open", message: routing.Error(),
		}, true
	}

	if errors.Is(err, identity.ErrDenied) {
		return problem{status: http.StatusForbidden, code: "auth.forbidden", message: "forbidden"}, true
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
