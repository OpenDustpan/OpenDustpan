// Package notifications serves the tool-neutral REST API of notification
// sinks under /api/dustpan/v1/namespaces/{namespace}/sinks. It is an HTTP
// adapter with its own route table. It never imports another API adapter:
// tools share concepts through the core alone.
package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
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

// Config holds the dependencies of a Handler. Every field except Audit,
// Namespaces, OnAuditFailure, DenialLimiter, Sessions and Idempotency is
// required.
type Config struct {
	Sinks         *sinks.Service
	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	// Audit keeps the entries of the requests that the Authorizer refuses. Nil
	// writes none.
	Audit audit.Store
	// Namespaces reads the namespace of an entry that the shared denial limiter
	// flushes for a key of another adapter.
	Namespaces refusal.Namespaces
	// OnAuditFailure, when set, runs once for each refusal whose entry could not
	// be written, so the server can count it.
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

// Handler serves the sink API.
type Handler struct {
	sinks    *sinks.Service
	authn    identity.Authenticator
	authz    identity.Authorizer
	guard    *refusal.Recorder
	sessions *sessionauth.Checker
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
	case cfg.Sinks == nil:
		return nil, fmt.Errorf("%w: sink service", ErrMissingDependency)
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
		sinks:    cfg.Sinks,
		authn:    cfg.Authenticator,
		authz:    cfg.Authorizer,
		guard:    recorder.WithLimiter(cfg.DenialLimiter).WithSurface("sinks"),
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

	if limited, ok := errors.AsType[*sinks.RateLimitedError](err); ok {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds(limited), 10))
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
		// The log names the status only: the text may hold driver detail or a
		// secret.
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

// retryAfterSeconds returns the wait of a refused test in whole seconds,
// rounded up, and at least 1.
func retryAfterSeconds(limited *sinks.RateLimitedError) int64 {
	return int64(max(1, (limited.RetryAfter+time.Second-1)/time.Second))
}

// problem is the answer to an error: a status, a code of the catalog with the
// values of its arguments, and a message that is safe to show callers. The
// message of a sink error is fixed text: it never holds the URL, a secret or
// the text of a network error.
type problem struct {
	status  int
	code    string
	values  []errcode.Value
	message string
}

// classify maps err to its answer: first the errors of the caller, then the
// errors of the namespace, then the errors of the sink service.
func classify(err error) problem {
	return classifyAuth(err)
}

// classifyNamespace maps the errors of the namespace and of maintenance mode.
// Any other error goes on to classifySink.
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
		return classifySink(err)
	}
}

// classifySink maps the errors of the sink service. The text of a sink error
// is fixed: an error of the service can wrap the host of a URL, and a request
// must never learn more than it sent.
func classifySink(err error) problem {
	if limited, ok := errors.AsType[*sinks.RateLimitedError](err); ok {
		return problem{
			status: http.StatusTooManyRequests, code: errcode.SinkTestRateLimited,
			values:  []errcode.Value{errcode.Quantity("retryAfterSeconds", retryAfterSeconds(limited))},
			message: "too many tests of this notification destination or namespace: try again later",
		}
	}

	switch {
	case errors.Is(err, notify.ErrNotFound):
		return problem{
			status: http.StatusNotFound, code: errcode.SinkNotFound, values: nil,
			message: "notification destination not found",
		}
	case errors.Is(err, notify.ErrConflict):
		return problem{
			status: http.StatusConflict, code: errcode.SinkConflict, values: nil,
			message: "the notification destination changed since you read it: read it again and send the new version",
		}
	case errors.Is(err, sinks.ErrNoSender):
		return problem{
			status: http.StatusUnprocessableEntity, code: errcode.SinkNoSender, values: nil,
			message: "this server cannot send to this kind of notification destination",
		}
	case errors.Is(err, notify.ErrNoURL):
		return problem{
			status: http.StatusUnprocessableEntity, code: errcode.SinkNoURL, values: nil,
			message: "the notification destination has no URL that the server can use: set the URL again",
		}
	case errors.Is(err, sinks.ErrPlatformHost):
		return problem{
			status: http.StatusBadRequest, code: errcode.SinkPlatformHost, values: nil, message: platformHostText(err),
		}
	case errors.Is(err, notify.ErrInvalid):
		return problem{
			status: http.StatusBadRequest, code: errcode.SinkInvalid, values: nil, message: invalidText(err),
		}
	default:
		return problem{
			status: http.StatusInternalServerError, code: errcode.ServerInternal, values: nil,
			message: "internal error",
		}
	}
}

// platformHostText returns the host rule of the platform. The rule is a fixed
// sentence and never holds the host or the URL of the request.
func platformHostText(err error) string {
	if rule := sinks.PlatformHostRule(err); rule != "" {
		return rule
	}

	return "The URL is not a webhook URL of this platform: copy the incoming webhook URL from the platform."
}

// invalidText returns the text for a sink that the service refused. A URL
// that the outbound address rules refuse gets one fixed text.
func invalidText(err error) string {
	if errors.Is(err, egress.ErrRefused) {
		return "the URL is not allowed for an outbound request: " +
			"use an https URL with a public host name and the port 443 or a port that the operator allows"
	}

	return "the notification destination is not valid: check the kind, the platform, the name and the target"
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
