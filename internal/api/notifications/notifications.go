// Package notifications serves the tool-neutral REST API of notification
// sinks under /api/dustpan/v1/namespaces/{namespace}/sinks. It is an HTTP
// adapter with its own route table. It never imports another API adapter:
// tools share concepts through the core alone.
package notifications

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
	"opendustpan.org/opendustpan/internal/api/routekit"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
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
	mux      *routekit.Mux
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
		mux:      routekit.NewMux(),
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

// writeError answers err through the shared error module of the
// tool-neutral APIs, with the classes of the sink service.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, Classify: classifySink}.Write(ctx, w, err)
}

// classify returns the answer to err.
func classify(err error) problem.Problem {
	return problem.Writer{Logger: nil, Classify: classifySink}.Problem(err)
}

// classifySink maps the errors of the sink service. The text of a sink error
// is fixed: an error of the service can wrap the host of a URL, and a request
// must never learn more than it sent.
func classifySink(err error) (problem.Problem, bool) {
	var wait time.Duration
	if limited, ok := errors.AsType[*sinks.RateLimitedError](err); ok {
		wait = limited.RetryAfter
	}

	switch {
	case errors.Is(err, notify.ErrNotFound):
		return sinkProblem(http.StatusNotFound, "sink.not-found", "notification destination not found"), true
	case errors.Is(err, notify.ErrConflict):
		return sinkProblem(
			http.StatusConflict,
			"sink.conflict",
			"the notification destination changed since you read it: read it again and send the new version",
		), true
	case errors.Is(err, sinks.ErrNoSender):
		return sinkProblem(
			http.StatusUnprocessableEntity,
			"sink.no-sender",
			"this server cannot send to this kind of notification destination",
		), true
	case errors.Is(err, notify.ErrNoURL):
		return sinkProblem(
			http.StatusUnprocessableEntity,
			"sink.no-url",
			"the notification destination has no URL that the server can use: set the URL again",
		), true
	case errors.Is(err, sinks.ErrRateLimited):
		limited := sinkProblem(http.StatusTooManyRequests, "sink.test-rate-limited",
			"too many tests of this notification destination or namespace: try again later")
		limited.RetryAfter = wait

		return limited, true
	case errors.Is(err, sinks.ErrPlatformHost):
		return sinkProblem(http.StatusBadRequest, "sink.platform-host", platformHostText(err)), true
	case errors.Is(err, notify.ErrInvalid):
		return sinkProblem(http.StatusBadRequest, "sink.invalid", invalidText(err)), true
	default:
		return problem.Pass()
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

// sinkProblem returns the answer of a sink error with no arguments.
func sinkProblem(status int, code, message string) problem.Problem {
	return problem.Problem{Status: status, Code: code, Message: message, Args: nil, RetryAfter: 0, Reauthenticate: ""}
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	problem.WriteJSON(ctx, h.logger, w, status, body)
}
