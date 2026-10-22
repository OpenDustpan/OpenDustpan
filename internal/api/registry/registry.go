// Package registry serves the tool-neutral REST API of the private registry
// under /api/dustpan/v1/namespaces/{namespace}/registry. It stages the files of
// an upload, publishes a provider release, lists releases and manages the
// trusted signing keys of a namespace. It is an HTTP adapter with its own route
// table. It never imports another API adapter: the protocol that OpenTofu reads
// lives in the TFE-compatible adapter.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

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
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/releasecheck"
	"opendustpan.org/opendustpan/pkg/registry/service"
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

// Config holds the dependencies of a Handler. Every field except Sessions,
// Idempotency, Audit, Namespaces, OnAuditFailure and DenialLimiter is required.
type Config struct {
	Service       *service.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// MaxUploadBytes bounds the body of one staged file. It must equal the limit
	// of the service, so the adapter refuses early what the service refuses.
	MaxUploadBytes int64
	// Audit keeps the entries of the requests that the Authorizer refuses. Nil
	// writes none.
	Audit audit.Store
	// Namespaces reads the namespace of an entry that the shared denial limiter
	// flushes for a key of another adapter.
	Namespaces refusal.Namespaces
	// OnAuditFailure runs once for each refusal whose entry could not be written.
	OnAuditFailure func()
	// DenialLimiter bounds the audit entries of refused requests. Nil means a
	// limiter of this handler only.
	DenialLimiter *denylimit.Limiter
	// Sessions checks the console session cookie of a request with no
	// Authorization header. Nil accepts access tokens only.
	Sessions *sessionauth.Checker
	// Idempotency serves the Idempotency-Key header. Nil makes every request
	// with the header answer 400.
	Idempotency *idempotent.Middleware
	Logger      *slog.Logger
}

// Handler serves the registry API.
type Handler struct {
	service  *service.Service
	authn    identity.Authenticator
	authz    identity.Authorizer
	guard    *refusal.Recorder
	sessions *sessionauth.Checker
	idem     *idempotent.Middleware
	logger   *slog.Logger
	maxFile  int64
	mux      *patternMux
	routes   []Route
}

// NewHandler returns a Handler.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Service == nil:
		return nil, fmt.Errorf("%w: registry service", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.MaxUploadBytes <= 0:
		return nil, fmt.Errorf("%w: upload limit", ErrMissingDependency)
	}

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, fmt.Errorf("denial recorder: %w", err)
	}

	h := &Handler{
		service:  cfg.Service,
		authn:    cfg.Authenticator,
		authz:    cfg.Authorizer,
		guard:    recorder.WithLimiter(cfg.DenialLimiter).WithSurface("registry"),
		sessions: cfg.Sessions,
		idem:     cfg.Idempotency,
		logger:   cfg.Logger,
		maxFile:  cfg.MaxUploadBytes,
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

// authenticate resolves the Authorization header to a principal. A request with
// no header uses the console session cookie, and the bool reports that the
// cookie signed it in.
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

	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, pr.status, errorResponse{Code: pr.status, Message: pr.message, ErrorCode: pr.code})
}

// problem is the answer to an error: a status, a stable code and a message that
// is safe to show callers.
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

	return classifyRegistry(err)
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

// classifyRegistry maps the errors of the registry. The text of a refusal names
// the field or the file that fails, and holds no byte of a key.
func classifyRegistry(err error) problem {
	status, code := registryStatus(err)
	if status == http.StatusInternalServerError {
		return problem{status: status, code: code, message: "internal error"}
	}

	return problem{status: status, code: code, message: err.Error()}
}

// registryProblem maps one error of the registry to its answer.
type registryProblem struct {
	err    error
	code   string
	status int
}

// registryProblems lists the errors of the registry with their answers. The
// first match wins, so a specific error comes before a general one.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant slices.
var registryProblems = []registryProblem{
	{registry.ErrNotFound, "registry.not-found", http.StatusNotFound},
	{registry.ErrKeyNotFound, "registry.not-found", http.StatusNotFound},
	{service.ErrNotStaged, "registry.not-staged", http.StatusNotFound},
	{service.ErrTooLarge, "registry.too-large", http.StatusRequestEntityTooLarge},
	{registry.ErrVersionExists, "registry.version-exists", http.StatusConflict},
	{registry.ErrKeyExists, "registry.key-exists", http.StatusConflict},
	{registry.ErrBadSignature, "registry.signature-invalid", http.StatusUnprocessableEntity},
	{registry.ErrUntrustedKey, "registry.signature-untrusted-key", http.StatusUnprocessableEntity},
	{releasecheck.ErrRefused, "registry.archive-set-refused", http.StatusUnprocessableEntity},
	{service.ErrNoKeys, "registry.no-keys", http.StatusUnprocessableEntity},
	{registry.ErrKeyLimit, "registry.key-limit", http.StatusUnprocessableEntity},
	{registry.ErrBadKey, "registry.key-invalid", http.StatusBadRequest},
	{registry.ErrInvalid, "registry.invalid", http.StatusBadRequest},
	{service.ErrBadRequest, "registry.invalid", http.StatusBadRequest},
}

// registryStatus returns the status and the stable code of a registry error,
// and 500 for an error that it does not know.
func registryStatus(err error) (int, string) {
	for _, p := range registryProblems {
		if errors.Is(err, p.err) {
			return p.status, p.code
		}
	}

	return http.StatusInternalServerError, ""
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
	case errors.Is(err, identity.ErrDenied):
		return problem{status: http.StatusForbidden, code: errcode.Forbidden(err), message: "forbidden"}, true
	default:
		return problem{status: 0, code: "", message: ""}, false
	}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
