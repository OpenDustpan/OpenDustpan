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
//		dustpan.WithBackupTarget(myBackupTarget, "dir:/var/backups"),
//		dustpan.WithInventoryReader(myStore),
//		dustpan.WithAuthenticator(myAuthenticator),
//	)
//
// Custom stores should pass the store/storetest conformance suite. The SDK is
// pre-1.0: ports may still change between minor versions.
package dustpan

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/pulumi"
	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ErrMissingOption reports that a required option was not provided.
var ErrMissingOption = errors.New("missing required option")

var (
	// errBackupNeedsExporter reports a backup target with a namespace store
	// that cannot capture exports.
	errBackupNeedsExporter = errors.New("backup target needs a namespace store that captures exports")
	// errBackupNeedsRegistry reports a backup target with a namespace store
	// that cannot track backups.
	errBackupNeedsRegistry = errors.New("backup target needs a namespace store that tracks backups")
	// errMaintenanceNeedsStore reports a namespace store that cannot keep
	// the installation state. Every SQL adapter serves it from the same
	// database connection; a custom store must serve it too, so every
	// instance gates the tool APIs on the same value.
	errMaintenanceNeedsStore = errors.New("namespace store must keep the installation state")
)

const (
	defaultListenAddr = ":8080"
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 30 * time.Second
)

// backupStoppedText marks a backup that was running when the server stopped.
// FailRunningBackups writes it at the next start (ADR 30 decision 9).
const backupStoppedText = "server stopped during backup"

// settings collects every option before NewServer validates them.
type settings struct {
	namespaceStore       namespace.Store
	stackStore           stack.Store
	updateStore          stack.UpdateStore
	checkpointStore      stack.CheckpointStore
	eventStore           stack.EventStore
	secretKeys           secret.KeyStore
	secretWrapper        secret.Wrapper
	workspaceStore       workspace.Store
	stateStore           workspace.StateStore
	blobStore            blob.Store
	inventoryReader      inventory.Reader
	authenticator        identity.Authenticator
	authorizer           identity.Authorizer
	logger               *slog.Logger
	now                  func() time.Time
	region               namespace.Region
	listenAddr           string
	consoleDir           string
	allowedOrigins       []string
	tlsCertFile          string
	tlsKeyFile           string
	publicURL            string
	version              string
	maxCheckpointBytes   int
	maxSecretBatchBytes  int
	maxStateBytes        int64
	maxBodyBytesInFlight int64
	// checkpointDeltaCutoff is the checkpoint size in bytes from which the
	// CLI sends delta uploads. Zero hides the delta capability.
	checkpointDeltaCutoff int
	backupTarget          blob.Store
	backupTargetName      string
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
// no key-encryption key is configured. Run runs secret.CheckWrapper at
// start: the server starts without a key only while the database holds no
// wrapped key (ADR 23).
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

// WithVersion sets the server version for dustpan_build_info. Empty means
// dev. The binary passes its stamped version; tests use the default.
func WithVersion(v string) Option {
	return func(o *settings) { o.version = v }
}

// WithMaxCheckpointBytes bounds one checkpoint. Zero means the domain default.
func WithMaxCheckpointBytes(n int) Option {
	return func(o *settings) { o.maxCheckpointBytes = n }
}

// WithMaxSecretBatchBytes bounds one secrets batch. Zero means the domain default.
func WithMaxSecretBatchBytes(n int) Option {
	return func(o *settings) { o.maxSecretBatchBytes = n }
}

// WithMaxStateBytes bounds one OpenTofu state version. Zero means the domain default.
func WithMaxStateBytes(n int64) Option {
	return func(o *settings) { o.maxStateBytes = n }
}

// WithMaxBodyBytesInFlight bounds concurrent large request bodies by bytes
// held at once. Zero means the adapter default.
func WithMaxBodyBytesInFlight(n int64) Option {
	return func(o *settings) { o.maxBodyBytesInFlight = n }
}

// WithCheckpointDeltaCutoff sets the checkpoint size in bytes from which
// the CLI sends delta uploads. Zero hides the delta capability, so the CLI
// sends whole checkpoints. A negative value fails NewServer. Without this
// option the server advertises the default cutoff.
func WithCheckpointDeltaCutoff(n int) Option {
	return func(o *settings) { o.checkpointDeltaCutoff = n }
}

// WithBackupTarget sets the backup target that holds blobs/<key>,
// backups/<id>/db and backups/<id>/manifest.json (ADR 30 decision 5).
// target is the opened target store, for example a blob/fs store for
// "dir:/path". name is the target setting for operators, for example
// "dir:/var/backups": the backups table keeps it. Empty name with a target
// keeps the empty name. Nil target means no backup target is configured and
// NewServer builds no backup service. It is the only way the target gets in:
// no request field, header or query value sets it.
func WithBackupTarget(target blob.Store, name string) Option {
	return func(o *settings) { o.backupTarget, o.backupTargetName = target, name }
}

// Server is a configured OpenDustpan server.
type Server struct {
	handler        http.Handler
	logger         *slog.Logger
	listenAddr     string
	secrets        *secret.Service
	secretKeys     secret.KeyStore
	secretWrapper  secret.Wrapper
	workspaces     *workspace.Service
	backups        *backup.Service
	backupRegistry backup.Registry
	now            func() time.Time
	registry       *metrics.Registry
	tlsCertFile    string
	tlsKeyFile     string
	observed       *metrics.ServerMetrics
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
		namespaceStore:        nil,
		stackStore:            nil,
		updateStore:           nil,
		checkpointStore:       nil,
		eventStore:            nil,
		secretKeys:            nil,
		secretWrapper:         nil,
		workspaceStore:        nil,
		stateStore:            nil,
		blobStore:             nil,
		inventoryReader:       nil,
		authenticator:         nil,
		authorizer:            identity.AdminOnly{},
		logger:                slog.New(slog.DiscardHandler),
		now:                   time.Now,
		region:                "",
		listenAddr:            defaultListenAddr,
		consoleDir:            "",
		allowedOrigins:        nil,
		tlsCertFile:           "",
		tlsKeyFile:            "",
		publicURL:             "",
		version:               "",
		maxCheckpointBytes:    0,
		maxSecretBatchBytes:   0,
		maxStateBytes:         0,
		maxBodyBytesInFlight:  0,
		checkpointDeltaCutoff: pulumi.DefaultCheckpointDeltaCutoff,
		backupTarget:          nil,
		backupTargetName:      "",
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

	backups, err := newBackupService(cfg, svc)
	if err != nil {
		return nil, err
	}

	return assemble(cfg, base, svc, backups)
}

// assemble mounts the adapters and routes around the services. backups is
// the installation backup service, or nil when no backup target is
// configured.
func assemble(cfg settings, base baseurl.Resolver, svc services, backups *backup.Service) (*Server, error) {
	// One registry holds every family: the request, runtime and build
	// families, and the readiness failure counter of the probe.
	observed := metrics.NewServerMetrics(cfg.version, cfg.now())
	registry := observed.Registry()

	adapters, err := newAdapters(svc, cfg, base, backups, observed)
	if err != nil {
		return nil, err
	}

	handler, err := routes(adapters, cfg, observed)
	if err != nil {
		return nil, err
	}

	var backupRegistry backup.Registry
	if reg, ok := cfg.namespaceStore.(backup.Registry); ok {
		backupRegistry = reg
	}

	return &Server{
		handler:        handler,
		logger:         cfg.logger,
		listenAddr:     cfg.listenAddr,
		secrets:        svc.secrets,
		secretKeys:     cfg.secretKeys,
		secretWrapper:  cfg.secretWrapper,
		workspaces:     svc.workspaces,
		backups:        backups,
		backupRegistry: backupRegistry,
		now:            cfg.now,
		registry:       registry,
		tlsCertFile:    cfg.tlsCertFile,
		tlsKeyFile:     cfg.tlsKeyFile,
		observed:       observed,
	}, nil
}

// adapters holds the HTTP adapters that routes mounts.
type adapters struct {
	pulumi      http.Handler
	inventory   http.Handler
	tfe         http.Handler
	server      http.Handler
	ready       http.Handler
	maintenance *maintenance.Watcher
}

// newAdapters builds every HTTP adapter from the services and settings.
// observed is the server registry: the metrics route writes it, and the
// readiness probe counts its failures on it.
func newAdapters(
	svc services,
	cfg settings,
	base baseurl.Resolver,
	backups *backup.Service,
	observed *metrics.ServerMetrics,
) (adapters, error) {
	maintenanceStore, ok := cfg.namespaceStore.(maintenance.Store)
	if !ok {
		return adapters{}, errMaintenanceNeedsStore
	}

	maint, err := maintenance.NewService(maintenance.Config{
		Store:      maintenanceStore,
		Namespaces: svc.namespaces,
		Stacks:     svc.stacks,
		Updates:    svc.updates,
		Workspaces: svc.workspaces,
	})
	if err != nil {
		return adapters{}, fmt.Errorf("maintenance service: %w", err)
	}

	// The watcher measures its cache limit in wall-clock time, not the
	// injected domain clock: tests freeze the domain clock, and the limit
	// must still expire.
	watcher := maintenance.NewWatcher(maintenanceStore, nil, 0)

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

	serverAPI, err := newServerHandler(cfg, svc, backups, maint, observed)
	if err != nil {
		return adapters{}, err
	}

	ready := newReadinessHandler(
		cfg.namespaceStore,
		cfg.blobStore,
		readinessTimeout,
		observed.ReadinessFailures(),
		cfg.logger,
	)

	return adapters{
		pulumi:      api,
		inventory:   units,
		tfe:         tfeAPI,
		server:      serverAPI,
		ready:       ready,
		maintenance: watcher,
	}, nil
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

	if cfg.namespaceStore == nil {
		return baseurl.Resolver{}, fmt.Errorf("%w: WithNamespaceStore", ErrMissingOption)
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
// no database. Run runs the start-up checks (such as secret.CheckWrapper)
// with its own context.
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
		Stacks:             stacks,
		Blobs:              cfg.blobStore,
		Now:                cfg.now,
		Logger:             cfg.logger,
		MaxCheckpointBytes: cfg.maxCheckpointBytes,
	})
	if err != nil {
		return services{}, fmt.Errorf("update service: %w", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys:          cfg.secretKeys,
		Wrapper:       cfg.secretWrapper,
		Namespaces:    namespaces,
		Now:           cfg.now,
		MaxBatchBytes: cfg.maxSecretBatchBytes,
	})
	if err != nil {
		return services{}, fmt.Errorf("secret service: %w", err)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		Workspaces:         cfg.workspaceStore,
		States:             cfg.stateStore,
		Blobs:              cfg.blobStore,
		Namespaces:         namespaces,
		Sealer:             secrets,
		Logger:             cfg.logger,
		Now:                cfg.now,
		MaxStateBytes:      cfg.maxStateBytes,
		MaxPendingVersions: 0,
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

// Backups returns the backup service the server was wired with, or nil when
// no backup target is configured.
func (s *Server) Backups() *backup.Service {
	return s.backups
}

// Metrics returns the request, build, process, runtime and readiness
// metrics the middleware records. The metrics route of issue #165 reads
// through it.
func (s *Server) Metrics() *metrics.ServerMetrics {
	return s.observed
}

// Run serves HTTP until ctx is done, then shuts down gracefully. With
// WithTLS it serves HTTPS; without it, it warns when the listen address is
// not loopback and serves plain HTTP anyway. Run first runs the start-up
// check that refuses a key-encryption key that opens less than every wrapped
// key in the database (ADR 23), so a program that embeds the server cannot
// start with the wrong key. It then marks every running backup failed: the
// server stopped during it (ADR 30 decision 9). When ctx is done before
// these start-up steps end, Run returns nil: the caller asked it to stop,
// and an error caused by that stop is not a failure.
func (s *Server) Run(ctx context.Context) error {
	if err := s.startUp(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}

		return err
	}

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

// startUp runs the start-up steps of Run in order: the key check of ADR 23,
// then marking every running backup failed (ADR 30 decision 9). The store
// adapters already name the backup step in their error text, so startUp
// adds no second prefix.
func (s *Server) startUp(ctx context.Context) error {
	if err := secret.CheckWrapper(ctx, s.secretKeys, s.secretWrapper); err != nil {
		return err
	}

	if s.backupRegistry == nil {
		return nil
	}

	return s.backupRegistry.FailRunningBackups(
		ctx,
		s.now().UTC(),
		backupStoppedText,
	)
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
		Stacks:                svc.stacks,
		Updates:               svc.updates,
		Secrets:               svc.secrets,
		Namespaces:            svc.namespaces,
		Authenticator:         cfg.authenticator,
		Authorizer:            cfg.authorizer,
		Logger:                cfg.logger,
		BaseURL:               base,
		MaxBodyBytesInFlight:  cfg.maxBodyBytesInFlight,
		CheckpointDeltaCutoff: cfg.checkpointDeltaCutoff,
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

// newServerHandler builds the tool-neutral installation API. It carries the
// namespace service that creates namespaces, the backup service when a
// backup target is configured, else nil, the maintenance service, the
// registry that the metrics route writes, the server version and the schema
// store that the upgrade check reads.
func newServerHandler(
	cfg settings,
	svc services,
	backups *backup.Service,
	maint *maintenance.Service,
	observed *metrics.ServerMetrics,
) (http.Handler, error) {
	handler, err := serverapi.NewHandler(serverapi.Config{
		Authenticator:    cfg.authenticator,
		Authorizer:       cfg.authorizer,
		Logger:           cfg.logger,
		Namespaces:       svc.namespaces,
		Backups:          backups,
		Maintenance:      maint,
		Metrics:          observed,
		Version:          cfg.version,
		Schema:           schemaReporterOf(cfg.namespaceStore),
		KeyEncryptionKey: cfg.secretWrapper != nil,
	})
	if err != nil {
		return nil, fmt.Errorf("server api: %w", err)
	}

	return handler, nil
}

// schemaReporterOf returns the schema reporter of the namespace store, or
// nil when the store does not report applied migrations. Both SQL adapters
// report them.
//
// caller keeps the interface the installation API consumes.
//
//nolint:ireturn // The two SQL adapters share no concrete store type, so the
func schemaReporterOf(store namespace.Store) serverapi.SchemaReporter {
	reporter, ok := store.(serverapi.SchemaReporter)
	if !ok {
		return nil
	}

	return reporter
}

// newBackupService builds the installation backup service when a backup
// target is configured, else returns nil. The exporter and the registry come
// from the namespace store: every SQL adapter serves all ports from one
// database connection. A nil target means no backup target is configured.
func newBackupService(cfg settings, svc services) (*backup.Service, error) {
	if cfg.backupTarget == nil {
		//nolint:nilnil // Nil without an error means no backup target is configured.
		return nil, nil
	}

	exporter, ok := cfg.namespaceStore.(backup.Exporter)
	if !ok {
		return nil, errBackupNeedsExporter
	}

	registry, ok := cfg.namespaceStore.(backup.Registry)
	if !ok {
		return nil, errBackupNeedsRegistry
	}

	maxSQL := int64(0)
	if strings.HasPrefix(strings.TrimSpace(cfg.backupTargetName), "s3://") {
		maxSQL = backup.S3ObjectLimit
	}

	service, err := backup.NewService(backup.Config{
		Exporter:    exporter,
		Registry:    registry,
		Blobs:       cfg.blobStore,
		StateOpener: svc.workspaces,
		Target:      cfg.backupTarget,
		TargetName:  cfg.backupTargetName,
		Now:         cfg.now,
		Version:     cfg.version,
		MaxSQLBytes: maxSQL,
	})
	if err != nil {
		return nil, fmt.Errorf("backup service: %w", err)
	}

	return service, nil
}

// requestIDHeader carries the request ID in each response. The server mints
// a new ID for each request and ignores any ID the client sends.
const requestIDHeader = "X-Request-Id"

// unmatchedRoute is the route label for a request that matches no pattern.
// It holds no path, so it cannot leak a capability token.
const unmatchedRoute = "unmatched"

// requestIDBytes is the entropy of one request ID in bytes, as hex.
const requestIDBytes = 16

// requestIDKey holds the request ID in the request context.
type requestIDKey struct{}

// RequestID returns the request ID in ctx, or "" when ctx holds none. The
// middleware sets it for each request, so handlers and later issues can log
// it without reading headers.
func RequestID(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}

	return id
}

// newRequestID mints one request ID: 128 bits from crypto/rand as hex. It
// falls back to the time when the reader fails, so it never returns "".
func newRequestID() string {
	buf := make([]byte, requestIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}

	return hex.EncodeToString(buf)
}

// statusWriter records the status and the body size of one response.
type statusWriter struct {
	http.ResponseWriter

	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)

	return n, err //nolint:wrapcheck // Transparent wrapper: callers must see the original error.
}

// adapterForRequest maps a request to its adapter label from the route
// pattern, or from the path when no pattern matched. It returns pulumi,
// opentofu, inventory or server. It never returns the path itself.
func adapterForRequest(pattern, path string) string {
	route := pattern
	if route == "" {
		route = path
	}

	name := route
	if _, rest, ok := strings.Cut(route, " "); ok {
		name = rest
	}

	switch {
	case strings.HasPrefix(name, "/api/inventory/"):
		return "inventory"
	case strings.HasPrefix(name, "/api/server/"):
		return "server"
	case strings.HasPrefix(name, "/api/v2/"), strings.HasPrefix(name, "/.well-known/"):
		return "opentofu"
	case strings.HasPrefix(name, "/api/"):
		return "pulumi"
	default:
		return "server"
	}
}

// debugRoute reports whether pattern logs at debug. Probes and the metrics
// route log at debug. Every other request logs at info.
func debugRoute(pattern string) bool {
	name := pattern
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		name = rest
	}

	switch name {
	case "/health/live", "/health/ready", "/api/server/metrics":
		return true
	default:
		return false
	}
}

// subjectForRequest returns the subject of the signed-in principal, or false
// when the request carries no valid access token. Lease and upload requests
// carry no access token, so they log with no subject.
func subjectForRequest(ctx context.Context, authn identity.Authenticator, r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")

	var raw string

	switch {
	case strings.HasPrefix(header, "token "):
		raw = strings.TrimPrefix(header, "token ")
	case strings.HasPrefix(header, "Bearer "):
		raw = strings.TrimPrefix(header, "Bearer ")
	default:
		return "", false
	}

	if raw == "" {
		return "", false
	}

	principal, err := authn.AuthenticateToken(ctx, raw)
	if err != nil || principal.Subject == "" {
		return "", false
	}

	return principal.Subject, true
}

// withRequestLog mints a request ID, stores it in the context, records the
// request in the metrics registry, and writes one log line for each request.
// The line holds the method, the route, the status, the duration, the size,
// the ID, the adapter and, for a signed-in request, the subject. It holds
// nothing else.
//
// Go's ServeMux stores the matched pattern in Request.Pattern. The root mux
// matches a prefix such as /api/ and forwards the request to an adapter
// mux. The adapter mux overwrites Pattern with its own method and path
// pattern. The middleware reads Pattern after the next handler returns, so
// it sees the inner pattern. When nothing matches, Pattern stays empty and
// the line uses unmatched. The line names the pattern only, never the path,
// so a capability token in the path cannot leak into the log. The metrics
// use the same pattern and adapter, so no label holds a path value.
func withRequestLog(
	next http.Handler,
	logger *slog.Logger,
	authn identity.Authenticator,
	observed *metrics.ServerMetrics,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		// A direct assignment keeps the header name of ADR 32. The key is already
		// in canonical form, so http.Header reads it with Get.
		w.Header()[requestIDHeader] = []string{id}

		ctx := context.WithValue(r.Context(), requestIDKey{}, id)

		r = r.WithContext(ctx)

		subject, signedIn := subjectForRequest(ctx, authn, r)

		// The route pattern is not known until the handler returns, so the
		// in-flight gauge tracks the adapter from the path only. The gauge
		// holds no route label for the same reason.
		startAdapter := adapterForRequest("", r.URL.Path)
		if observed != nil {
			observed.IncInFlight(startAdapter)
			defer observed.DecInFlight(startAdapter)
		}

		start := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK, bytes: 0}

		next.ServeHTTP(wrapped, r)

		elapsed := time.Since(start)

		route := r.Pattern
		if route == "" {
			route = unmatchedRoute
		}

		// A root prefix without a method means the adapter found no route.
		// Log it as unmatched, so the line never names a bare prefix.
		if !strings.Contains(route, " ") && route != unmatchedRoute {
			route = unmatchedRoute
		}

		adapter := adapterForRequest(r.Pattern, r.URL.Path)

		if observed != nil {
			observed.ObserveRequest(adapter, route, r.Method, wrapped.status, elapsed)
		}

		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int64("status", int64(wrapped.status)),
			slog.Int64("duration_ms", elapsed.Milliseconds()),
			slog.Int64("bytes", wrapped.bytes),
			slog.String("request_id", id),
			slog.String("adapter", adapter),
		}

		if signedIn {
			attrs = append(attrs, slog.String("subject", subject))
		}

		level := slog.LevelInfo
		if debugRoute(r.Pattern) {
			level = slog.LevelDebug
		}

		logger.LogAttrs(ctx, level, "request", attrs...)
	})
}

func routes(a adapters, cfg settings, observed *metrics.ServerMetrics) (http.Handler, error) {
	mux := http.NewServeMux()
	// /api/inventory/, /api/v2/ and /api/server/ are more specific than
	// /api/, so the inventory units route, the OpenTofu routes and the
	// installation routes reach their own adapters while every other /api/
	// route reaches Pulumi. In maintenance mode the tool adapters answer 503
	// with Retry-After; the installation API stays available, so the
	// maintenance routes that end the restore keep serving.
	mux.Handle(
		"/api/inventory/",
		withCORS(withMaintenance(a.inventory, a.maintenance, shapeInventory, cfg.logger), cfg.allowedOrigins),
	)
	mux.Handle(
		"/api/v2/",
		withCORS(withMaintenance(a.tfe, a.maintenance, shapeTFE, cfg.logger), cfg.allowedOrigins),
	)
	mux.Handle("/api/server/", withCORS(a.server, cfg.allowedOrigins))
	mux.Handle("/api/", withCORS(withMaintenance(a.pulumi, a.maintenance, shapePulumi, cfg.logger), cfg.allowedOrigins))
	// Service discovery lives outside /api/v2/, so the root mux serves it
	// through the TFE adapter. It stays available in maintenance mode: the
	// CLI reads only the service addresses from it, and OpenTofu shows only
	// the discovery status, never the refusal body. The lock, state and
	// version endpoints under /api/v2/ pass the maintenance gate, so the
	// CLI still meets the 503 refusal that names maintenance.
	mux.Handle("GET /.well-known/terraform.json", a.tfe)
	// The probes have their own prefix, so that ingress controllers that serve
	// /healthz themselves cannot answer for this server (ADR 34). Liveness
	// checks nothing; readiness pings the store and the blob store. Both stay
	// public.
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("GET /health/ready", a.ready)

	if cfg.consoleDir != "" {
		console, err := consoleHandler(cfg.consoleDir)
		if err != nil {
			return nil, err
		}

		mux.Handle("/", console)
	}

	return withRequestLog(mux, cfg.logger, cfg.authenticator, observed), nil
}
