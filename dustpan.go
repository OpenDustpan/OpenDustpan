// Package dustpan is the OpenDustpan server as a library.
//
// The opendustpan binary is a thin main around this package. To swap a
// built-in adapter (persistence, identity, storage) for your own, write your
// own main and pass your implementations as options, in the style of
// Temporal's server SDK:
//
//	srv, err := dustpan.NewServer(
//		dustpan.WithRegion("eu-west"),
//		dustpan.WithNamespaceStore(myStore),
//		dustpan.WithStackStore(myStore),
//		dustpan.WithUpdateStore(myStore),
//		dustpan.WithCheckpointStore(myStore),
//		dustpan.WithEventStore(myStore),
//		dustpan.WithAuthenticator(myAuthenticator),
//	)
//
// Custom stores should pass the store/storetest conformance suite. The SDK is
// pre-1.0: ports may still change between minor versions.
package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/api/pulumi"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
)

// ErrMissingOption reports that a required option was not provided.
var ErrMissingOption = errors.New("missing required option")

const (
	defaultListenAddr = ":8080"
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 30 * time.Second
)

// settings collects every option before NewServer validates them.
type settings struct {
	namespaceStore  namespace.Store
	stackStore      stack.Store
	updateStore     stack.UpdateStore
	checkpointStore stack.CheckpointStore
	eventStore      stack.EventStore
	authenticator   identity.Authenticator
	authorizer      identity.Authorizer
	logger          *slog.Logger
	now             func() time.Time
	region          namespace.Region
	listenAddr      string
	consoleDir      string
	allowedOrigins  []string
}

// Option configures a Server.
type Option func(*settings)

// WithNamespaceStore sets the namespace persistence port. Required.
func WithNamespaceStore(s namespace.Store) Option {
	return func(o *settings) { o.namespaceStore = s }
}

// WithStackStore sets the stack persistence port. Required.
func WithStackStore(s stack.Store) Option {
	return func(o *settings) { o.stackStore = s }
}

// WithUpdateStore sets the update persistence port. Required.
func WithUpdateStore(s stack.UpdateStore) Option {
	return func(o *settings) { o.updateStore = s }
}

// WithCheckpointStore sets the checkpoint persistence port. Required.
func WithCheckpointStore(s stack.CheckpointStore) Option {
	return func(o *settings) { o.checkpointStore = s }
}

// WithEventStore sets the engine event persistence port. Required.
func WithEventStore(s stack.EventStore) Option {
	return func(o *settings) { o.eventStore = s }
}

// WithAuthenticator sets how access tokens are resolved to principals. Required.
func WithAuthenticator(a identity.Authenticator) Option {
	return func(o *settings) { o.authenticator = a }
}

// WithAuthorizer sets the authorization policy. Defaults to identity.AdminOnly.
func WithAuthorizer(a identity.Authorizer) Option {
	return func(o *settings) { o.authorizer = a }
}

// WithRegion sets the region this server is authoritative for. Required.
func WithRegion(r namespace.Region) Option {
	return func(o *settings) { o.region = r }
}

// WithLogger sets the structured logger. Defaults to discarding logs.
func WithLogger(l *slog.Logger) Option {
	return func(o *settings) { o.logger = l }
}

// WithListenAddr sets the address Run listens on. Defaults to ":8080".
func WithListenAddr(addr string) Option {
	return func(o *settings) { o.listenAddr = addr }
}

// WithConsoleDir serves a built web console from dir at "/". The console is a
// separate build; this exists so one process can serve both (omnibus installs).
func WithConsoleDir(dir string) Option {
	return func(o *settings) { o.consoleDir = dir }
}

// WithAllowedOrigins lists the web console origins allowed to call the API
// cross-origin, for example "https://dustpan.acme.internal".
func WithAllowedOrigins(origins ...string) Option {
	return func(o *settings) { o.allowedOrigins = origins }
}

// WithClock overrides the time source, for tests.
func WithClock(now func() time.Time) Option {
	return func(o *settings) { o.now = now }
}

// Server is a configured OpenDustpan server.
type Server struct {
	handler    http.Handler
	logger     *slog.Logger
	listenAddr string
}

// NewServer validates the options and wires the server together.
func NewServer(opts ...Option) (*Server, error) {
	cfg := settings{
		namespaceStore:  nil,
		stackStore:      nil,
		updateStore:     nil,
		checkpointStore: nil,
		eventStore:      nil,
		authenticator:   nil,
		authorizer:      identity.AdminOnly{},
		logger:          slog.New(slog.DiscardHandler),
		now:             time.Now,
		region:          "",
		listenAddr:      defaultListenAddr,
		consoleDir:      "",
		allowedOrigins:  nil,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.authenticator == nil {
		return nil, fmt.Errorf("%w: WithAuthenticator", ErrMissingOption)
	}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: cfg.namespaceStore, Now: cfg.now, Region: cfg.region},
	)
	if err != nil {
		return nil, fmt.Errorf("namespace service: %w", err)
	}

	stacks, err := stack.NewService(stack.Config{Store: cfg.stackStore, Namespaces: namespaces, Now: cfg.now})
	if err != nil {
		return nil, fmt.Errorf("stack service: %w", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            cfg.updateStore,
		Checkpoints:        cfg.checkpointStore,
		Events:             cfg.eventStore,
		Namespaces:         namespaces,
		Now:                cfg.now,
		MaxCheckpointBytes: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}

	api, err := pulumi.NewHandler(pulumi.Config{
		Stacks:        stacks,
		Updates:       updates,
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Logger:        cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}

	handler, err := routes(api, cfg)
	if err != nil {
		return nil, err
	}

	return &Server{handler: handler, logger: cfg.logger, listenAddr: cfg.listenAddr}, nil
}

// Handler returns the server's root HTTP handler, for embedding in your own server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Run serves HTTP until ctx is done, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.listenAddr,
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)

	go func() {
		s.logger.InfoContext(ctx, "listening", slog.String("addr", s.listenAddr))
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}

func routes(api http.Handler, cfg settings) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.Handle("/api/", withCORS(api, cfg.allowedOrigins))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	if cfg.consoleDir != "" {
		console, err := consoleHandler(cfg.consoleDir)
		if err != nil {
			return nil, err
		}

		mux.Handle("/", console)
	}

	return mux, nil
}
