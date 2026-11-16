// Package runnerpools serves the runner management API: the tool-neutral
// API that people use to manage runner pools, join tokens and runners under
// /api/dustpan/v1/namespaces/{namespace}. It holds no protocol route: the
// runner protocol under /runner/v1 stays in internal/api/runnerapi with its
// own wire format.
//
// It is an HTTP adapter with its own route table. It never imports another
// API adapter: tools share concepts through the core alone.
package runnerpools

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
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
)

// Config holds the dependencies of a Handler. Sessions checks the console
// session cookie of a request with no Authorization header; nil accepts
// access tokens only.
type Config struct {
	Namespaces *namespace.Service
	Pools      runner.PoolStore
	Members    runner.MemberStore

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
	Sessions      SessionChecker
	Logger        *slog.Logger

	// ServerURL is the public URL of the server, as a join and a rejoin
	// command writes it.
	ServerURL string
	// Now is the clock of this instance. Nil takes time.Now. The handler uses
	// each time in UTC at millisecond precision.
	Now func() time.Time
}

// SessionChecker checks the console session cookie of a request with no
// Authorization header. It is the narrowed surface of sessionauth.Checker
// that this package needs.
type SessionChecker interface {
	Authenticate(r *http.Request) (identity.Principal, sessionMark, error)
}

// sessionMark reports whether the request is signed in with a session.
type sessionMark interface {
	Session() bool
}

// Handler serves the runner management API.
type Handler struct {
	namespaces *namespace.Service
	pools      runner.PoolStore
	members    runner.MemberStore
	authn      identity.Authenticator
	authz      identity.Authorizer
	guard      *refusal.Recorder
	audit      audit.Store
	onAuditErr func()
	sessions   SessionChecker
	logger     *slog.Logger
	serverURL  string
	now        func() time.Time
	polls      sync.Map
	mux        *patternMux
	routes     []Route
}

// validate returns ErrMissingDependency when cfg lacks a required dependency.
func (cfg Config) validate() error {
	switch {
	case cfg.Audit == nil:
		return errors.New("missing dependency: audit store")
	case cfg.Namespaces == nil:
		return errors.New("missing dependency: namespace service")
	case cfg.Pools == nil:
		return errors.New("missing dependency: runner pool store")
	case cfg.Members == nil:
		return errors.New("missing dependency: runner member store")
	case cfg.Authenticator == nil:
		return errors.New("missing dependency: authenticator")
	case cfg.Authorizer == nil:
		return errors.New("missing dependency: authorizer")
	case cfg.Logger == nil:
		return errors.New("missing dependency: logger")
	case cfg.ServerURL == "":
		return errors.New("missing dependency: server URL")
	}

	return nil
}

// NewHandler returns a Handler. Every Config field except Sessions, Now and
// OnAuditFailure is required.
func NewHandler(cfg Config) (*Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	clock := cfg.Now
	if clock == nil {
		clock = time.Now
	}

	// The store keeps a time at millisecond precision. The clock of the
	// handler gives the same precision, so the answer of a create or a change
	// holds the time that a later read gives.
	now := func() time.Time { return clock().UTC().Truncate(time.Millisecond) }

	recorder, err := refusal.New(cfg.Authorizer, cfg.Audit, cfg.Namespaces, cfg.Logger, cfg.OnAuditFailure)
	if err != nil {
		return nil, err
	}

	h := &Handler{
		namespaces: cfg.Namespaces,
		pools:      cfg.Pools,
		members:    cfg.Members,
		authn:      cfg.Authenticator,
		authz:      cfg.Authorizer,
		sessions:   cfg.Sessions,
		logger:     cfg.Logger,
		guard:      recorder.WithLimiter(cfg.DenialLimiter).WithSurface("runnerpools"),
		audit:      cfg.Audit,
		onAuditErr: cfg.OnAuditFailure,
		serverURL:  cfg.ServerURL,
		now:        now,
		polls:      sync.Map{},
		mux:        newPatternMux(),
		routes:     nil,
	}

	h.routes = h.buildRoutes()

	for _, rt := range h.routes {
		h.mux.HandleFunc(rt.Method+" "+rt.Path, h.enforce(rt))
	}

	return h, nil
}

// requireCall is the checked request it serves.
var _ = route.Checks{}

// ServeHTTP implements http.Handler. It reports the release of the protocol
// package, as the protocol handler does, so a caller sees one server.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// Routes returns a copy of the route table.
func (h *Handler) Routes() []Route {
	return append([]Route(nil), h.routes...)
}

// RegisteredPatterns returns the ServeMux patterns of the routes.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}
