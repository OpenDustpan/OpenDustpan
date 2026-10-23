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

	"opendustpan.org/opendustpan/internal/api/apidesc"
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

// errorResponse is the error body of the tool-neutral APIs. ErrorCode is a
// stable machine code.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
	// ErrorCode is a stable machine code.
	ErrorCode string `json:"errorCode,omitempty"`
	// ErrorArgs are the arguments of the code.
	ErrorArgs map[string]any `json:"errorArgs,omitempty"`
	// Reauthenticate is the path that starts a new sign-in, for the refusal
	// of an old sign-in.
	Reauthenticate string `json:"reauthenticate,omitempty"`
}

// statusError is an error with a fixed answer.
type statusError struct {
	problem problem
}

func (e *statusError) Error() string { return e.problem.message }

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, idempotency.ErrLost) {
		err = idempotent.Lost()
	}

	w.Header().Set("Cache-Control", "no-store")

	if ie, ok := errors.AsType[*idempotent.Error](err); ok {
		h.writeJSON(ctx, w, ie.Status, answerBody(ie.Status, ie.Message, ie.Answer, ""))

		return
	}

	pr := classify(err)
	if se, ok := errors.AsType[*statusError](err); ok {
		pr = se.problem
	}

	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	answer, codeErr := errcode.New(pr.code, pr.values...)
	if codeErr != nil {
		// A code or a value that the catalog refuses is a bug of this adapter.
		// The log names the code only.
		h.logger.ErrorContext(ctx, "error code refused", slog.String("error_code", pr.code))

		pr = problem{
			status: http.StatusInternalServerError, code: errcode.ServerInternal, values: nil,
			message: "internal error",
		}
		answer = errcode.Internal()
	}

	if pr.status == http.StatusInternalServerError {
		// The log names the status only: the text may hold driver detail.
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", pr.status))
	}

	var reauthenticate string
	if old, ok := errors.AsType[reauthenticateError](err); ok {
		reauthenticate = old.path
	}

	h.writeJSON(ctx, w, pr.status, answerBody(pr.status, pr.message, answer, reauthenticate))
}

// answerBody returns the error body of status and message with the code and
// the arguments of answer. reauthenticate is the path that starts a new
// sign-in, or "".
func answerBody(status int, message string, answer errcode.Answer, reauthenticate string) errorResponse {
	return errorResponse{
		Code: status, Message: message, ErrorCode: answer.Code(), ErrorArgs: answer.Args(),
		Reauthenticate: reauthenticate,
	}
}

// problem is the answer to an error: a status, a code of the catalog with the
// values of its arguments, and a message that is safe to show callers.
type problem struct {
	status  int
	code    string
	values  []errcode.Value
	message string
}

// classify maps err to its answer: first the errors of the caller, then the
// errors of the namespace, then the errors of the profile service.
func classify(err error) problem {
	return classifyAuth(err)
}

// classifyNamespace maps the errors of the namespace and of maintenance mode.
// Any other error goes on to classifyProfile.
func classifyNamespace(err error) problem {
	switch {
	case errors.Is(err, namespace.ErrInvalidName):
		return problem{
			status: http.StatusBadRequest, code: errcode.NamespaceInvalidName, values: nil, message: err.Error(),
		}
	case errors.Is(err, namespace.ErrNotFound):
		return problem{
			status: http.StatusNotFound, code: errcode.NamespaceNotFound, values: nil, message: "namespace not found",
		}
	case errors.Is(err, namespace.ErrFenced):
		return problem{
			status: http.StatusConflict, code: errcode.NamespaceFenced, values: nil,
			message: namespace.ErrFenced.Error(),
		}
	case errors.Is(err, namespace.ErrNotHome):
		return problem{
			status: http.StatusMisdirectedRequest, code: errcode.NamespaceNotHome, values: nil,
			message: "namespace is not homed in this region",
		}
	case errors.Is(err, maintenance.ErrInMaintenance):
		return problem{
			status: http.StatusServiceUnavailable, code: errcode.MaintenanceInMaintenance, values: nil,
			message: maintenance.ErrInMaintenance.Error() + ": try again later",
		}
	default:
		return classifyProfile(err)
	}
}

// classifyProfile maps the errors of the profile service.
func classifyProfile(err error) problem {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		return problem{status: http.StatusNotFound, code: errcode.ProfileNotFound, values: nil, message: err.Error()}
	case errors.Is(err, profile.ErrSchemaVersion):
		return problem{
			status: http.StatusUnprocessableEntity, code: errcode.ProfileSchemaVersion, values: nil,
			message: err.Error(),
		}
	case errors.Is(err, profile.ErrLimit):
		return problem{
			status: http.StatusUnprocessableEntity, code: errcode.ProfileLimit, values: nil, message: err.Error(),
		}
	case errors.Is(err, profile.ErrInvalid):
		return problem{status: http.StatusBadRequest, code: errcode.ProfileInvalid, values: nil, message: err.Error()}
	case errors.Is(err, profile.ErrConflict):
		return problem{status: http.StatusConflict, code: errcode.ProfileConflict, values: nil, message: err.Error()}
	default:
		return problem{
			status: http.StatusInternalServerError, code: errcode.ServerInternal, values: nil,
			message: "internal error",
		}
	}
}

// classifyAuth maps the errors of the caller: a missing or a refused
// credential, a missing fresh sign-in and a refused action. Any other error
// goes on to classifyNamespace.
func classifyAuth(err error) problem {
	if routing, ok := errors.AsType[*profile.RoutingError](err); ok {
		return problem{
			status: http.StatusForbidden, code: errcode.ProfileRoutingChangeNeedsOpen, values: nil,
			message: routing.Error(),
		}
	}

	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return problem{
			status: http.StatusTooManyRequests, code: errcode.AuthRateLimited, values: nil,
			message: "too many failed sign-ins from this address: try again later",
		}
	case errors.Is(err, identity.ErrUnauthenticated):
		return problem{
			status: http.StatusUnauthorized, code: errcode.AuthUnauthenticated, values: nil, message: "unauthenticated",
		}
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return problem{status: http.StatusForbidden, code: errcode.Forbidden(err), values: nil, message: err.Error()}
	case errors.Is(err, errFreshStepRequired), errors.Is(err, profile.ErrFreshRequired):
		// A request with no console session, or a reveal with no fresh
		// authentication check: fail closed.
		return problem{
			status: http.StatusForbidden, code: errcode.MFAFreshStepRequired, values: nil,
			message: sessionauth.FreshStepRequiredMessage,
		}
	case errors.Is(err, errReauthenticate):
		return problem{
			status: http.StatusUnauthorized, code: errcode.MFAFreshStepRequired, values: nil, message: err.Error(),
		}
	case errors.Is(err, identity.ErrDenied):
		return problem{status: http.StatusForbidden, code: errcode.Forbidden(err), values: nil, message: "forbidden"}
	default:
		return classifyNamespace(err)
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
