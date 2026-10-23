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

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
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
	header := apidesc.Header(r, "Authorization")
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
	Message   string         `json:"message"`
	Code      int            `json:"code"`
	ErrorCode string         `json:"errorCode,omitempty"`
	ErrorArgs map[string]any `json:"errorArgs,omitempty"`
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
		h.writeAnswer(ctx, w, ie.Status, ie.Message, ie.Answer)

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

	h.writeAnswer(ctx, w, pr.status, pr.message, answer)
}

// writeAnswer writes the error body of status and message with the code and
// the arguments of answer.
func (h *Handler) writeAnswer(
	ctx context.Context, w http.ResponseWriter, status int, message string, answer errcode.Answer,
) {
	h.writeJSON(ctx, w, status, errorResponse{
		Code: status, Message: message, ErrorCode: answer.Code(), ErrorArgs: answer.Args(),
	})
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
// errors of the namespace, then the errors of the registry.
func classify(err error) problem {
	return classifyAuth(err)
}

// classifyNamespace maps the errors of the namespace and of maintenance mode.
// Any other error goes on to classifyRegistry.
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
		return classifyRegistry(err)
	}
}

// classifyRegistry maps the errors of the registry. The text of a refusal names
// the field or the file that fails, and holds no byte of a key.
func classifyRegistry(err error) problem {
	p := registryProblemOf(err)
	if p == nil {
		return problem{
			status: http.StatusInternalServerError, code: errcode.ServerInternal, values: nil,
			message: "internal error",
		}
	}

	pr := p.problem
	pr.message = err.Error()

	return pr
}

// registryProblem maps one error of the registry to its answer. The message
// of the answer is the text of the error.
type registryProblem struct {
	err     error
	problem problem
}

// registryProblems lists the errors of the registry with their answers. The
// first match wins, so a specific error comes before a general one.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant slices.
var registryProblems = []registryProblem{
	{err: registry.ErrNotFound, problem: problem{
		status: http.StatusNotFound, code: errcode.RegistryNotFound, values: nil, message: "",
	}},
	{err: registry.ErrKeyNotFound, problem: problem{
		status: http.StatusNotFound, code: errcode.RegistryNotFound, values: nil, message: "",
	}},
	{err: service.ErrNotStaged, problem: problem{
		status: http.StatusNotFound, code: errcode.RegistryNotStaged, values: nil, message: "",
	}},
	{err: service.ErrTooLarge, problem: problem{
		status: http.StatusRequestEntityTooLarge, code: errcode.RegistryTooLarge, values: nil, message: "",
	}},
	{err: registry.ErrVersionExists, problem: problem{
		status: http.StatusConflict, code: errcode.RegistryVersionExists, values: nil, message: "",
	}},
	{err: registry.ErrKeyExists, problem: problem{
		status: http.StatusConflict, code: errcode.RegistryKeyExists, values: nil, message: "",
	}},
	{err: registry.ErrBadSignature, problem: problem{
		status: http.StatusUnprocessableEntity, code: errcode.RegistrySignatureInvalid, values: nil, message: "",
	}},
	{err: registry.ErrUntrustedKey, problem: problem{
		status: http.StatusUnprocessableEntity, code: errcode.RegistrySignatureUntrustedKey, values: nil, message: "",
	}},
	{err: releasecheck.ErrRefused, problem: problem{
		status: http.StatusUnprocessableEntity, code: errcode.RegistryArchiveSetRefused, values: nil, message: "",
	}},
	{err: service.ErrNoKeys, problem: problem{
		status: http.StatusUnprocessableEntity, code: errcode.RegistryNoKeys, values: nil, message: "",
	}},
	{err: registry.ErrKeyLimit, problem: problem{
		status: http.StatusUnprocessableEntity, code: errcode.RegistryKeyLimit, values: nil, message: "",
	}},
	{err: registry.ErrBadKey, problem: problem{
		status: http.StatusBadRequest, code: errcode.RegistryKeyInvalid, values: nil, message: "",
	}},
	{err: registry.ErrInvalid, problem: problem{
		status: http.StatusBadRequest, code: errcode.RegistryInvalid, values: nil, message: "",
	}},
	{err: service.ErrBadRequest, problem: problem{
		status: http.StatusBadRequest, code: errcode.RegistryInvalid, values: nil, message: "",
	}},
}

// registryProblemOf returns the element of registryProblems for err, or nil for
// an error that the table does not hold.
func registryProblemOf(err error) *registryProblem {
	for i := range registryProblems {
		if errors.Is(err, registryProblems[i].err) {
			return &registryProblems[i]
		}
	}

	return nil
}

// classifyAuth maps the errors of the caller: a missing or a refused credential
// and a refused action. Any other error goes on to classifyNamespace.
func classifyAuth(err error) problem {
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
