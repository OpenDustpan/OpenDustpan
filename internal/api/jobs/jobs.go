// Package jobs serves the tool-neutral jobs API: a person reads a job, its
// phases and their records. The API is an HTTP adapter with its own route
// table. It never imports another API adapter: tools share concepts through
// the core alone.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	tokenScheme = "token "
	// bearerScheme is the scheme of the TFE-compatible API and the
	// installation API. This API accepts it too, so one client can read
	// every API.
	bearerScheme = "Bearer "
)

// Config holds the dependencies of a Handler. Sessions checks the console
// session cookie of a request with no Authorization header; nil accepts
// access tokens only.
type Config struct {
	Namespaces *namespace.Service
	Jobs       job.Store
	// Starter starts jobs. Nil serves the read route only: a server that lacks
	// a port of the job service starts no job.
	Starter       job.Starter
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses. Nil
	// writes none.
	Audit audit.Store
	// OnAuditFailure, when set, runs once for each refusal whose entry could not
	// be written, so the server can count it.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. The server
	// gives every adapter the same limiter, so its bound on keys holds for all
	// of them. Nil means a limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	Sessions      *sessionauth.Checker
	Logger        *slog.Logger
}

// Handler serves the jobs API under /api/dustpan/v1/.
type Handler struct {
	namespaces *namespace.Service
	jobs       job.Store
	starter    job.Starter
	authn      identity.Authenticator
	sessions   *sessionauth.Checker
	guard      *refusal.Recorder
	logger     *slog.Logger
	mux        *patternMux
	routes     []Route
}

// NewHandler returns a Handler. Every Config field except Sessions is
// required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Jobs == nil:
		return nil, fmt.Errorf("%w: job store", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	h := &Handler{
		namespaces: cfg.Namespaces,
		jobs:       cfg.Jobs,
		starter:    cfg.Starter,
		authn:      cfg.Authenticator,
		sessions:   cfg.Sessions,
		guard: refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure).
			WithLimiter(cfg.DenialLimiter).
			WithSurface("jobs"),
		logger: cfg.Logger,
		mux:    newPatternMux(),
		routes: nil,
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

// authenticate resolves the "Authorization: token <t>" or the
// "Authorization: Bearer <t>" header to a principal. A request with no
// Authorization header uses the console session cookie, and the bool
// reports that the cookie signed it in. A request with an Authorization
// header never uses the cookie.
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

// errorResponse is the error body of the tool-neutral APIs.
type errorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
	// ErrorCode is a stable code for a program. A refusal of a request to
	// start a job sets it.
	ErrorCode string `json:"errorCode,omitempty"`
}

// errNotFound is the one answer for an unknown namespace, an unknown job, a
// job of another namespace, and a job that the caller cannot read. The answer
// never tells that the job exists.
var errNotFound = errors.New("not found")

func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	body := classify(err)
	if body.Code == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text may
		// hold driver detail, a token, or state.
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", body.Code))
	}

	h.writeJSON(ctx, w, body.Code, body)
}

// Codes of the refusals of a request to start a job.
const (
	codeInvalid        = "job.invalid"
	codeUnsupported    = "job.unsupported"
	codePolicyDenied   = "job.policy-denied"
	codeFrozen         = "job.frozen"
	codeLimit          = "job.limit"
	codeAgentDenied    = "job.agent-denied"
	codeKeyConflict    = "request.idempotency-key-reused"
	codeKeyInvalid     = "request.idempotency-key-invalid"
	codeBodyInvalid    = "request.body-invalid"
	codeGateEnforced   = "job.gate-enforced"
	codeFenced         = "namespace.fenced"
	maxMessageBytes    = 300
	unitCannotBeStated = "forbidden"
)

// classify maps errors to an HTTP status, a stable code and a message safe to
// show callers. A refusal of the job service shows its own text, which the
// service and its ports write without detail of a driver or a secret. The
// text is cut and cleaned before it leaves.
func classify(err error) errorResponse {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return errorResponse{
			Code:      http.StatusTooManyRequests,
			ErrorCode: "",
			Message:   "too many failed sign-ins from this address: try again later",
		}
	case errors.Is(err, identity.ErrUnauthenticated):
		return errorResponse{Code: http.StatusUnauthorized, ErrorCode: "", Message: "unauthenticated"}
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return errorResponse{Code: http.StatusForbidden, ErrorCode: errcode.Forbidden(err), Message: err.Error()}
	case errors.Is(err, identity.ErrDenied):
		return errorResponse{Code: http.StatusForbidden, ErrorCode: errcode.Forbidden(err), Message: unitCannotBeStated}
	case errors.Is(err, errNotFound), errors.Is(err, namespace.ErrNotFound), errors.Is(err, job.ErrNotFound):
		return errorResponse{Code: http.StatusNotFound, ErrorCode: "", Message: errNotFound.Error()}
	case errors.Is(err, namespace.ErrNotHome):
		return errorResponse{
			Code:      http.StatusMisdirectedRequest,
			ErrorCode: "",
			Message:   "namespace is not homed in this region",
		}
	case errors.Is(err, maintenance.ErrInMaintenance):
		return errorResponse{
			Code:      http.StatusServiceUnavailable,
			ErrorCode: "",
			Message:   maintenance.ErrInMaintenance.Error() + ": try again later",
		}
	}

	if status, code, ok := classifyStart(err); ok {
		return errorResponse{Code: status, ErrorCode: code, Message: refusal.Clean(err.Error(), maxMessageBytes)}
	}

	return errorResponse{Code: http.StatusInternalServerError, ErrorCode: "", Message: "internal error"}
}

// classifyStart maps the refusals of the job service.
func classifyStart(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errBody):
		return http.StatusBadRequest, codeBodyInvalid, true
	case errors.Is(err, idempotency.ErrInvalidKey):
		return http.StatusBadRequest, codeKeyInvalid, true
	case errors.Is(err, job.ErrKeyConflict):
		return http.StatusUnprocessableEntity, codeKeyConflict, true
	case errors.Is(err, namespace.ErrFenced):
		return http.StatusConflict, codeFenced, true
	default:
		return classifyJobRefusal(err)
	}
}

// classifyJobRefusal maps the refusals that the rules of a job give.
func classifyJobRefusal(err error) (int, string, bool) {
	switch {
	case errors.Is(err, job.ErrInvalid):
		return http.StatusBadRequest, codeInvalid, true
	case errors.Is(err, job.ErrUnsupported):
		return http.StatusUnprocessableEntity, codeUnsupported, true
	case errors.Is(err, job.ErrPolicyDenied):
		return http.StatusForbidden, codePolicyDenied, true
	case errors.Is(err, job.ErrGateEnforced):
		return http.StatusForbidden, codeGateEnforced, true
	case errors.Is(err, job.ErrAgentDenied):
		return http.StatusForbidden, codeAgentDenied, true
	case errors.Is(err, job.ErrFrozen):
		return http.StatusConflict, codeFrozen, true
	case errors.Is(err, job.ErrLimit):
		return http.StatusTooManyRequests, codeLimit, true
	default:
		return 0, "", false
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
