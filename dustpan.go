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
//		dustpan.WithSecretKeyStore(myStore),
//		dustpan.WithSecretWrapper(myWrapper),
//		dustpan.WithWorkspaceStore(myStore),
//		dustpan.WithStateStore(myStore),
//		dustpan.WithBlobStore(myBlobs),
//		dustpan.WithInventoryReader(myStore),
//		dustpan.WithAuthenticator(myAuthenticator),
//	)
//
// Custom stores should pass the store/storetest conformance suite. The SDK is
// pre-1.0: ports may still change between minor versions.
package dustpan

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	inventoryapi "opendustpan.org/opendustpan/api/inventory"
	"opendustpan.org/opendustpan/api/pulumi"
	serverapi "opendustpan.org/opendustpan/api/server"
	"opendustpan.org/opendustpan/api/tfe"
	"opendustpan.org/opendustpan/blob"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/inventory"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/secret"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/workspace"
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
	secretKeys      secret.KeyStore
	secretWrapper   secret.Wrapper
	workspaceStore  workspace.Store
	stateStore      workspace.StateStore
	blobStore       blob.Store
	inventoryReader inventory.Reader
	authenticator   identity.Authenticator
	authorizer      identity.Authorizer
	logger          *slog.Logger
	now             func() time.Time
	region          namespace.Region
	listenAddr      string
	consoleDir      string
	allowedOrigins  []string
	tlsCertFile     string
	tlsKeyFile      string
	publicURL       string
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

// WithSecretKeyStore sets the wrapped-data-key persistence port. Required.
func WithSecretKeyStore(s secret.KeyStore) Option {
	return func(o *settings) { o.secretKeys = s }
}

// WithSecretWrapper sets the key-encryption key holder. Optional: nil means
// no key-encryption key is configured. Every main calls secret.CheckWrapper
// during start-up: the server starts without a key only while the database
// holds no wrapped key (ADR 23).
func WithSecretWrapper(w secret.Wrapper) Option {
	return func(o *settings) { o.secretWrapper = w }
}

// WithWorkspaceStore sets the workspace persistence port. Required.
func WithWorkspaceStore(s workspace.Store) Option {
	return func(o *settings) { o.workspaceStore = s }
}

// WithStateStore sets the state version persistence port. Required.
func WithStateStore(s workspace.StateStore) Option {
	return func(o *settings) { o.stateStore = s }
}

// WithBlobStore sets the state content persistence port. Required.
func WithBlobStore(s blob.Store) Option {
	return func(o *settings) { o.blobStore = s }
}

// WithInventoryReader sets the inventory projection reader. Required.
func WithInventoryReader(r inventory.Reader) Option {
	return func(o *settings) { o.inventoryReader = r }
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

// WithTLS serves HTTPS with the certificate and key in certFile and keyFile,
// read once at start. The minimum version is TLS 1.2. Both names are
// required: set both or none.
func WithTLS(certFile, keyFile string) Option {
	return func(o *settings) { o.tlsCertFile, o.tlsKeyFile = certFile, keyFile }
}

// WithPublicURL sets the external base URL of the server, for example
// "https://dustpan.acme.internal". Adapters use it for absolute URLs. It
// must be an absolute http or https URL with a host and no query. Empty
// means each request derives it from its own scheme and host.
func WithPublicURL(raw string) Option {
	return func(o *settings) { o.publicURL = raw }
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
	handler     http.Handler
	logger      *slog.Logger
	listenAddr  string
	secrets     *secret.Service
	workspaces  *workspace.Service
	tlsCertFile string
	tlsKeyFile  string
}

// services holds the domain services that NewServer wires together.
type services struct {
	namespaces *namespace.Service
	stacks     *stack.Service
	updates    *stack.Updates
	secrets    *secret.Service
	workspaces *workspace.Service
}

// NewServer validates the options and wires the server together.
func NewServer(opts ...Option) (*Server, error) {
	cfg := settings{
		namespaceStore:  nil,
		stackStore:      nil,
		updateStore:     nil,
		checkpointStore: nil,
		eventStore:      nil,
		secretKeys:      nil,
		secretWrapper:   nil,
		workspaceStore:  nil,
		stateStore:      nil,
		blobStore:       nil,
		inventoryReader: nil,
		authenticator:   nil,
		authorizer:      identity.AdminOnly{},
		logger:          slog.New(slog.DiscardHandler),
		now:             time.Now,
		region:          "",
		listenAddr:      defaultListenAddr,
		consoleDir:      "",
		allowedOrigins:  nil,
		tlsCertFile:     "",
		tlsKeyFile:      "",
		publicURL:       "",
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	base, err := checkSettings(&cfg)
	if err != nil {
		return nil, err
	}

	svc, err := newServices(cfg)
	if err != nil {
		return nil, err
	}

	adapters, err := newAdapters(svc, cfg, base)
	if err != nil {
		return nil, err
	}

	handler, err := routes(adapters.pulumi, adapters.inventory, adapters.tfe, adapters.server, cfg)
	if err != nil {
		return nil, err
	}

	return &Server{
		handler:     handler,
		logger:      cfg.logger,
		listenAddr:  cfg.listenAddr,
		secrets:     svc.secrets,
		workspaces:  svc.workspaces,
		tlsCertFile: cfg.tlsCertFile,
		tlsKeyFile:  cfg.tlsKeyFile,
	}, nil
}

// adapters holds the HTTP adapters that routes mounts.
type adapters struct {
	pulumi    http.Handler
	inventory http.Handler
	tfe       http.Handler
	server    http.Handler
}

// newAdapters builds every HTTP adapter from the services and settings.
func newAdapters(svc services, cfg settings, base baseurl.Resolver) (adapters, error) {
	api, err := newPulumiHandler(svc, cfg, base)
	if err != nil {
		return adapters{}, err
	}

	units, err := newInventoryHandler(svc, cfg)
	if err != nil {
		return adapters{}, err
	}

	tfeAPI, err := newTFEHandler(cfg, svc, base)
	if err != nil {
		return adapters{}, err
	}

	serverAPI, err := newServerHandler(cfg)
	if err != nil {
		return adapters{}, err
	}

	return adapters{pulumi: api, inventory: units, tfe: tfeAPI, server: serverAPI}, nil
}

// newTFEHandler returns the TFE-compatible API handler that OpenTofu speaks
// to. base reports the external base URL for absolute URLs.
func newTFEHandler(cfg settings, svc services, base baseurl.Resolver) (*tfe.Handler, error) {
	handler, err := tfe.NewHandler(tfe.Config{
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Logger:        cfg.logger,
		Namespaces:    svc.namespaces,
		Workspaces:    svc.workspaces,
		BaseURL:       base,
	})
	if err != nil {
		return nil, fmt.Errorf("tfe api: %w", err)
	}

	return handler, nil
}

// checkSettings validates the required options and the TLS and public URL
// settings. It returns the parsed public URL resolver.
func checkSettings(cfg *settings) (baseurl.Resolver, error) {
	if cfg.authenticator == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithAuthenticator", ErrMissingOption)
	}

	if cfg.secretKeys == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithSecretKeyStore", ErrMissingOption)
	}

	if cfg.workspaceStore == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithWorkspaceStore", ErrMissingOption)
	}

	if cfg.stateStore == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithStateStore", ErrMissingOption)
	}

	if cfg.blobStore == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithBlobStore", ErrMissingOption)
	}

	if cfg.inventoryReader == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithInventoryReader", ErrMissingOption)
	}

	if (cfg.tlsCertFile == "") != (cfg.tlsKeyFile == "") {
		missing := "WithTLS certificate file"
		if cfg.tlsCertFile != "" {
			missing = "WithTLS key file"
		}

		return baseurl.Resolver{}, fmt.Errorf("%w: %s", ErrMissingOption, missing)
	}

	base, err := baseurl.Parse(cfg.publicURL)
	if err != nil {
		return baseurl.Resolver{}, fmt.Errorf("public URL: %w", err)
	}

	return base, nil
}

// newServices builds the domain services from validated settings. It reads
// no database: mains run the start-up checks (such as secret.CheckWrapper)
// with their own context before or after it.
func newServices(cfg settings) (services, error) {
	namespaces, err := namespace.NewService(
		namespace.Config{Store: cfg.namespaceStore, Now: cfg.now, Region: cfg.region},
	)
	if err != nil {
		return services{}, fmt.Errorf("namespace service: %w", err)
	}

	stacks, err := stack.NewService(stack.Config{Store: cfg.stackStore, Namespaces: namespaces, Now: cfg.now})
	if err != nil {
		return services{}, fmt.Errorf("stack service: %w", err)
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
		return services{}, fmt.Errorf("update service: %w", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys:          cfg.secretKeys,
		Wrapper:       cfg.secretWrapper,
		Namespaces:    namespaces,
		Now:           cfg.now,
		MaxBatchBytes: 0,
	})
	if err != nil {
		return services{}, fmt.Errorf("secret service: %w", err)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		Workspaces:    cfg.workspaceStore,
		States:        cfg.stateStore,
		Blobs:         cfg.blobStore,
		Namespaces:    namespaces,
		Now:           cfg.now,
		MaxStateBytes: 0,
	})
	if err != nil {
		return services{}, fmt.Errorf("workspace service: %w", err)
	}

	return services{
		namespaces: namespaces,
		stacks:     stacks,
		updates:    updates,
		secrets:    secrets,
		workspaces: workspaces,
	}, nil
}

// Handler returns the server's root HTTP handler, for embedding in your own server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Workspaces returns the workspace service the server was wired with.
func (s *Server) Workspaces() *workspace.Service {
	return s.workspaces
}

// Run serves HTTP until ctx is done, then shuts down gracefully. With
// WithTLS it serves HTTPS; without it, it warns when the listen address is
// not loopback and serves plain HTTP anyway.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.listenAddr,
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	if s.tlsCertFile != "" {
		cert, err := tls.LoadX509KeyPair(s.tlsCertFile, s.tlsKeyFile)
		if err != nil {
			return fmt.Errorf("load TLS certificate: %w", err)
		}

		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	} else if !isLoopbackListenAddr(s.listenAddr) {
		s.logger.WarnContext(
			ctx,
			"serving plain HTTP on a non-loopback address",
			slog.String("addr", s.listenAddr),
		)
	}

	serveErr := make(chan error, 1)

	go func() {
		s.logger.InfoContext(ctx, "listening", slog.String("addr", s.listenAddr))

		if srv.TLSConfig != nil {
			serveErr <- srv.ListenAndServeTLS("", "")
		} else {
			serveErr <- srv.ListenAndServe()
		}
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

// isLoopbackListenAddr reports whether addr binds only the loopback
// interface. An empty host binds every interface, so it is not loopback. A
// name that is not an IP address counts as loopback only for localhost.
func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}

	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	return ip.IsLoopback()
}

// newPulumiHandler builds the Pulumi CLI API from the domain services.
func newPulumiHandler(svc services, cfg settings, base baseurl.Resolver) (http.Handler, error) {
	handler, err := pulumi.NewHandler(pulumi.Config{
		Stacks:        svc.stacks,
		Updates:       svc.updates,
		Secrets:       svc.secrets,
		Namespaces:    svc.namespaces,
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Logger:        cfg.logger,
		BaseURL:       base,
	})
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}

	return handler, nil
}

// newInventoryHandler builds the tool-neutral inventory API from the
// namespace service and the inventory reader.
func newInventoryHandler(svc services, cfg settings) (http.Handler, error) {
	handler, err := inventoryapi.NewHandler(inventoryapi.Config{
		Namespaces:    svc.namespaces,
		Units:         cfg.inventoryReader,
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Logger:        cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("inventory api: %w", err)
	}

	return handler, nil
}

// newServerHandler builds the tool-neutral installation API. The skeleton
// needs only the identity ports; later issues add the backup and metrics
// dependencies.
func newServerHandler(cfg settings) (http.Handler, error) {
	handler, err := serverapi.NewHandler(serverapi.Config{
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Logger:        cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("server api: %w", err)
	}

	return handler, nil
}

func routes(api, units, tfeAPI, serverAPI http.Handler, cfg settings) (http.Handler, error) {
	mux := http.NewServeMux()
	// /api/inventory/, /api/v2/ and /api/server/ are more specific than
	// /api/, so the inventory units route, the OpenTofu routes and the
	// installation routes reach their own adapters while every other /api/
	// route reaches Pulumi.
	mux.Handle("/api/inventory/", withCORS(units, cfg.allowedOrigins))
	mux.Handle("/api/v2/", withCORS(tfeAPI, cfg.allowedOrigins))
	mux.Handle("/api/server/", withCORS(serverAPI, cfg.allowedOrigins))
	mux.Handle("/api/", withCORS(api, cfg.allowedOrigins))
	// Service discovery lives outside /api/v2/, so the root mux serves it
	// through the TFE adapter.
	mux.Handle("GET /.well-known/terraform.json", tfeAPI)
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
