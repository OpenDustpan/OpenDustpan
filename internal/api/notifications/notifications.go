// Package notifications serves the tool-neutral REST API of notification
// sinks, subscriptions and deliveries under /api/dustpan/v1/namespaces/{namespace}. It is an HTTP
// adapter with its own route table. It never imports another API adapter:
// tools share concepts through the core alone.
package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/subscriptionsvc"
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
	Sinks *sinks.Service
	// Subscriptions serves the subscription and delivery routes.
	Subscriptions *subscriptionsvc.Service
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
	sinks         *sinks.Service
	subscriptions *subscriptionsvc.Service
	authn         identity.Authenticator
	authz         identity.Authorizer
	guard         *refusal.Recorder
	sessions      *sessionauth.Checker
	idem          *idempotent.Middleware
	logger        *slog.Logger
	mux           *route.Mux
	routes        []Route
}

// NewHandler returns a Handler.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	case cfg.Sinks == nil:
		return nil, fmt.Errorf("%w: sink service", ErrMissingDependency)
	case cfg.Subscriptions == nil:
		return nil, fmt.Errorf("%w: subscription service", ErrMissingDependency)
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
		sinks:         cfg.Sinks,
		subscriptions: cfg.Subscriptions,
		authn:         cfg.Authenticator,
		authz:         cfg.Authorizer,
		guard:         recorder.WithLimiter(cfg.DenialLimiter).WithSurface("sinks"),
		sessions:      cfg.Sessions,
		idem:          cfg.Idempotency,
		logger:        cfg.Logger,
		mux:           route.NewMux(),
		routes:        nil,
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
// tool-neutral APIs, with the classes of the sink service.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: sinkErrors}.Write(ctx, w, err)
}

// sinkErrors maps the errors of the sink service. The text of a sink error
// is fixed: an error of the service can wrap the host of a URL, and a request
// must never learn more than it sent.
func sinkErrors(err error) (problem.Problem, bool) {
	if limited, ok := errors.AsType[*sinks.RateLimitedError](err); ok {
		wait := problem.RetryAfterSeconds(limited.RetryAfter)

		p := problem.New(http.StatusTooManyRequests, errcode.SinkTestRateLimited,
			"too many tests of this notification destination or namespace: try again later",
			errcode.Quantity("retryAfterSeconds", wait))
		// The header names the same wait as the argument.
		p.RetryAfter = limited.RetryAfter

		return p, true
	}

	switch {
	case errors.Is(err, sinks.ErrLimitUnavailable):
		// The text names no cause: the cause is a database error.
		return problem.New(
			http.StatusServiceUnavailable,
			errcode.SinkTestLimitUnavailable,
			"the server cannot count the tests of notification destinations: try again later",
		), true
	case errors.Is(err, notify.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.SinkNotFound, "notification destination not found"), true
	case errors.Is(err, notify.ErrConflict):
		return problem.New(
			http.StatusConflict,
			errcode.SinkConflict,
			"the notification destination changed since you read it: read it again and send the new version",
		), true
	case errors.Is(err, sinks.ErrNoSender):
		return problem.New(
			http.StatusUnprocessableEntity,
			errcode.SinkNoSender,
			"this server cannot send to this kind of notification destination",
		), true
	case errors.Is(err, notify.ErrNoURL):
		return problem.New(
			http.StatusUnprocessableEntity,
			errcode.SinkNoURL,
			"the notification destination has no URL that the server can use: set the URL again",
		), true
	case errors.Is(err, sinks.ErrAccessToken):
		return problem.New(http.StatusBadRequest, errcode.SinkAccessToken, accessTokenText(err)), true
	case errors.Is(err, sinks.ErrPlatformHost):
		return problem.New(http.StatusBadRequest, errcode.SinkPlatformHost, platformHostText(err)), true
	case errors.Is(err, notify.ErrInvalid):
		return problem.New(http.StatusBadRequest, errcode.SinkInvalid, invalidText(err)), true
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

// accessTokenText returns the rule for a refused access token. The rule is a
// fixed sentence and never holds the token.
func accessTokenText(err error) string {
	if rule := sinks.AccessTokenRule(err); rule != "" {
		return rule
	}

	return "The access token is not valid for this platform."
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

// writeJSON answers 200 with body as JSON.
func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, body any) {
	problem.WriteJSON(ctx, h.logger, w, http.StatusOK, body)
}
