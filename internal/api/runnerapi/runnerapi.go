// Package runnerapi serves the server side of the runner protocol under
// /runner/v1, and the tool-neutral API that people use to manage runner
// pools, join tokens and runners under /api/runners.
//
// The protocol is a thin layer over the ports of package runner, package job
// and package durable: a path calls one port. A runner proves possession of
// its key on each request, and a join token authenticates a join. The runner
// never calls the REST API, and a runner key never holds an action that a
// person holds.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone.
package runnerapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

const (
	tokenScheme  = "token "
	bearerScheme = "Bearer "
	// channelHeader names the request header that says where a request came
	// from, for the audit entry.
	channelHeader = "Dustpan-Channel"
	// defaultPollInterval is the time between two tries of a claim during a
	// long poll. Other instances start operations that this instance never
	// sees, so a poll tries at least this often.
	defaultPollInterval = 5 * time.Second
)

// Config holds the dependencies of a Handler. Claimer and Sweeps are
// optional: a pool whose purpose has no claimer gets no work. Operations is
// optional: without it, a heartbeat reports no cancel and no lost claim.
// Sessions checks the console session cookie of a request with no
// Authorization header; nil accepts access tokens only.
type Config struct {
	Namespaces *namespace.Service
	Pools      runner.PoolStore
	Members    runner.MemberStore
	Handoffs   runner.SessionStore
	Claimer    job.Claimer
	Sweeps     runner.SweepClaimer
	Operations durable.Store

	Authenticator identity.Authenticator
	Authorizer    identity.Authorizer
	Sessions      *sessionauth.Checker
	Logger        *slog.Logger

	// Release is the release of this server. A runner of another release
	// than the skew allows gets version-unsupported.
	Release runner.Release
	// ServerURL is the public URL of the server, as a runner writes it in
	// the join challenge.
	ServerURL string
	// Now is the clock of this instance. Nil takes time.Now.
	Now func() time.Time
	// PollInterval is the time between two tries of a claim in a long poll.
	// Zero takes five seconds.
	PollInterval time.Duration
}

// Handler serves the runner protocol and the runner management API.
type Handler struct {
	namespaces *namespace.Service
	pools      runner.PoolStore
	members    runner.MemberStore
	handoffs   runner.SessionStore
	claimer    job.Claimer
	sweeps     runner.SweepClaimer
	operations durable.Store
	authn      identity.Authenticator
	authz      identity.Authorizer
	sessions   *sessionauth.Checker
	logger     *slog.Logger
	release    runner.Release
	serverURL  string
	now        func() time.Time
	interval   time.Duration
	mux        *patternMux
	routes     []Route
}

// NewHandler returns a Handler. Every Config field except Claimer, Sweeps,
// Operations, Sessions, Now and PollInterval is required.
func NewHandler(cfg Config) (*Handler, error) {
	switch {
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Pools == nil:
		return nil, fmt.Errorf("%w: runner pool store", ErrMissingDependency)
	case cfg.Members == nil:
		return nil, fmt.Errorf("%w: runner member store", ErrMissingDependency)
	case cfg.Handoffs == nil:
		return nil, fmt.Errorf("%w: runner session store", ErrMissingDependency)
	case cfg.Authenticator == nil:
		return nil, fmt.Errorf("%w: authenticator", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.ServerURL == "":
		return nil, fmt.Errorf("%w: server URL", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	interval := cfg.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}

	h := &Handler{
		namespaces: cfg.Namespaces, pools: cfg.Pools, members: cfg.Members, handoffs: cfg.Handoffs,
		claimer: cfg.Claimer, sweeps: cfg.Sweeps, operations: cfg.Operations,
		authn: cfg.Authenticator, authz: cfg.Authorizer, sessions: cfg.Sessions, logger: cfg.Logger,
		release: cfg.Release, serverURL: cfg.ServerURL, now: now, interval: interval,
		mux: newPatternMux(), routes: nil,
	}

	h.routes = h.buildRoutes()
	for _, route := range h.routes {
		h.mux.HandleFunc(route.Method+" "+route.Path, h.enforce(route))
	}

	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Dustpan-Release", h.release.String())
	h.mux.ServeHTTP(w, r)
}
