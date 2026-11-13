package dustpan

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// StoreOption configures a built-in store that WithSQLite, WithPostgreSQL or
// OpenStore opens.
type StoreOption func(*storeConfig)

// storeConfig collects the options of a built-in store before it opens.
type storeConfig struct {
	logger              *slog.Logger
	loggerSet           bool
	binaryVersion       string
	binaryVersionSet    bool
	installationKind    installation.Kind
	installationKindSet bool
	withoutMigrate      bool
}

// defaultStoreConfig returns the store options before any StoreOption.
func defaultStoreConfig() storeConfig {
	return storeConfig{
		logger: nil, loggerSet: false,
		binaryVersion: "", binaryVersionSet: false,
		installationKind: "", installationKindSet: false,
		withoutMigrate: false,
	}
}

// newStoreConfig returns the store options that opts set.
func newStoreConfig(opts []StoreOption) storeConfig {
	config := defaultStoreConfig()

	for _, opt := range opts {
		opt(&config)
	}

	return config
}

// WithStoreLogger sets the logger of the store. Without it, WithSQLite and
// WithPostgreSQL use the logger of the server.
func WithStoreLogger(logger *slog.Logger) StoreOption {
	return func(c *storeConfig) {
		c.logger = logger
		c.loggerSet = true
	}
}

// WithStoreBinaryVersion sets the version of the binary that opens the store.
// Without it, WithSQLite and WithPostgreSQL use the version of the server.
func WithStoreBinaryVersion(version string) StoreOption {
	return func(c *storeConfig) {
		c.binaryVersion = version
		c.binaryVersionSet = true
	}
}

// WithStoreInstallationKind sets the installation kind of the command that
// opens the store. Without it, WithSQLite and WithPostgreSQL use the kind of
// the server.
func WithStoreInstallationKind(kind installation.Kind) StoreOption {
	return func(c *storeConfig) {
		c.installationKind = kind
		c.installationKindSet = true
	}
}

// WithoutStoreMigrate makes the built-in store apply no pending migration and
// refuse a database with a pending migration.
func WithoutStoreMigrate() StoreOption {
	return func(c *storeConfig) { c.withoutMigrate = true }
}

// withServerDefaults answers config with the server values for each store
// option that the caller did not set.
func (c storeConfig) withServerDefaults(cfg *settings) storeConfig {
	if !c.loggerSet {
		c.logger = cfg.logger
		c.loggerSet = true
	}

	if !c.binaryVersionSet {
		c.binaryVersion = cfg.version
		c.binaryVersionSet = true
	}

	if !c.installationKindSet {
		c.installationKind = cfg.installationKind
		c.installationKindSet = true
	}

	return c
}

// loggerValue answers the logger of the store, or a discard logger.
func (c storeConfig) loggerValue() *slog.Logger {
	if c.logger != nil {
		return c.logger
	}

	return slog.New(slog.DiscardHandler)
}

// builtinStoreSource says which option set the built-in store.
type builtinStoreSource int

const (
	builtinStoreNone builtinStoreSource = iota
	builtinStoreSQLitePath
	builtinStorePostgresDSN
	builtinStoreHandle
)

// builtinStoreSettings is the built-in store of a server: the path or DSN that
// NewServer opens, or the handle that the caller opened.
type builtinStoreSettings struct {
	source builtinStoreSource
	path   string
	dsn    string
	config storeConfig
	handle *BuiltinStore
	// open opens the store at path or dsn and sets its store options. It is a
	// function value because NewServer has no context: the open gets its own
	// bound, builtinStoreOpenTimeout.
	open func(cfg *settings) error
	// close closes the store that open opened.
	close func() error
}

// newBuiltinStoreSettings returns the settings of a built-in store with the
// StoreOption values applied.
func newBuiltinStoreSettings(kind builtinStoreSource, opts []StoreOption) builtinStoreSettings {
	return builtinStoreSettings{
		source: kind, path: "", dsn: "", config: newStoreConfig(opts), handle: nil,
		open: nil, close: nil,
	}
}

// WithSQLite opens the built-in SQLite store at path and sets every store
// option of the server from it. The store options apply after all other
// options, so they replace a With...Store option for the same port. Close
// closes the store.
func WithSQLite(path string, opts ...StoreOption) Option {
	return func(o *settings) {
		builtin := newBuiltinStoreSettings(builtinStoreSQLitePath, opts)
		builtin.path = path
		builtin.open = func(cfg *settings) error { return openBuiltinSQLite(cfg, &cfg.builtinStore) }
		o.builtinStore = builtin
	}
}

// WithPostgreSQL opens the built-in PostgreSQL store at dsn and sets every
// store option of the server from it. The store options apply after all other
// options, so they replace a With...Store option for the same port. Close
// closes the store.
func WithPostgreSQL(dsn string, opts ...StoreOption) Option {
	return func(o *settings) {
		builtin := newBuiltinStoreSettings(builtinStorePostgresDSN, opts)
		builtin.dsn = dsn
		builtin.open = func(cfg *settings) error { return openBuiltinPostgres(cfg, &cfg.builtinStore) }
		o.builtinStore = builtin
	}
}

// WithBuiltinStore sets every store option of the server from b, which the
// caller opened with OpenStore. The caller closes b. The store options apply
// after all other options, so they replace a With...Store option for the same
// port. A nil b fails NewServer.
func WithBuiltinStore(b *BuiltinStore) Option {
	return func(o *settings) {
		builtin := newBuiltinStoreSettings(builtinStoreHandle, nil)
		builtin.handle = b
		o.builtinStore = builtin
	}
}

// Close closes the store that WithSQLite or WithPostgreSQL opened. It does
// nothing for a store that the caller opened, and nothing when no built-in
// store was opened. A second call returns nil.
func (s *Server) Close() error {
	if s.closeBuiltin == nil {
		return nil
	}

	return s.closeBuiltin.Close()
}

// builtinStoreCloser closes the store that NewServer opened, once.
type builtinStoreCloser struct {
	mu    sync.Mutex
	close func() error
}

// Close closes the store on the first call and answers nil on each later call.
func (c *builtinStoreCloser) Close() error {
	c.mu.Lock()
	closeFn := c.close
	c.close = nil
	c.mu.Unlock()

	if closeFn == nil {
		return nil
	}

	return closeFn()
}

// applyBuiltinStore opens or adopts the built-in store and sets its store
// options after all other options.
func applyBuiltinStore(cfg *settings) error {
	switch cfg.builtinStore.source {
	case builtinStoreNone:
		return nil
	case builtinStoreSQLitePath, builtinStorePostgresDSN:
		return cfg.builtinStore.open(cfg)
	case builtinStoreHandle:
		return useBuiltinStore(cfg, &cfg.builtinStore)
	}

	return nil
}

// openBuiltinSQLite opens the SQLite store of builtin and sets its options.
func openBuiltinSQLite(cfg *settings, builtin *builtinStoreSettings) error {
	ctx, cancel := context.WithTimeout(context.Background(), builtinStoreOpenTimeout)
	defer cancel()

	st, err := openSQLiteStore(ctx, builtin.path, builtin.config.withServerDefaults(cfg))
	if err != nil {
		return fmt.Errorf("WithSQLite: open the store: %w", err)
	}

	builtin.handle = st
	builtin.close = st.Close
	cfg.setStoreOptions(st.options(cfg))

	return nil
}

// openBuiltinPostgres opens the PostgreSQL store of builtin and sets its
// options. It refuses a dsn that does not name a PostgreSQL store, so that a
// value that names no such store never reaches the driver. The error repeats
// no part of the value.
func openBuiltinPostgres(cfg *settings, builtin *builtinStoreSettings) error {
	// Defense in depth: openStoreConfig and openPostgresStore check the form
	// too; this check stays so that WithPostgreSQL cannot hand a value of
	// another form to the driver.
	if !postgresDSNForm(builtin.dsn) {
		return fmt.Errorf("WithPostgreSQL: %w", errStoreDatabaseForm)
	}

	ctx, cancel := context.WithTimeout(context.Background(), builtinStoreOpenTimeout)
	defer cancel()

	st, err := openPostgresStore(ctx, builtin.dsn, builtin.config.withServerDefaults(cfg))
	if err != nil {
		return fmt.Errorf("WithPostgreSQL: open the store: %w", err)
	}

	builtin.handle = st
	builtin.close = st.Close
	cfg.setStoreOptions(st.options(cfg))

	return nil
}

// useBuiltinStore sets the store options from the handle of builtin.
func useBuiltinStore(cfg *settings, builtin *builtinStoreSettings) error {
	if builtin.handle == nil {
		return errNilBuiltinStore
	}

	cfg.setStoreOptions(builtin.handle.options(cfg))

	return nil
}

// sqliteOptions returns the options of the open of a SQLite store.
func (c storeConfig) sqliteOptions() []sqlite.Option {
	opts := []sqlite.Option{
		sqlite.WithLogger(c.loggerValue()),
		sqlite.WithBinaryVersion(c.binaryVersion),
		sqlite.WithInstallationKind(c.installationKind),
	}
	if c.withoutMigrate {
		opts = append(opts, sqlite.WithoutMigrate())
	}

	return opts
}

// postgresOptions returns the options of the open of a PostgreSQL store.
func (c storeConfig) postgresOptions() []postgres.Option {
	opts := []postgres.Option{
		postgres.WithLogger(c.loggerValue()),
		postgres.WithBinaryVersion(c.binaryVersion),
		postgres.WithInstallationKind(c.installationKind),
	}
	if c.withoutMigrate {
		opts = append(opts, postgres.WithoutMigrate())
	}

	return opts
}

// setStoreOptions applies opts to cfg.
func (o *settings) setStoreOptions(opts []Option) {
	for _, opt := range opts {
		opt(o)
	}
}

// closeFunc returns the function that closes the store that the server opened,
// or nil when the caller opened the store.
func (b *builtinStoreSettings) closeFunc() func() error {
	return b.close
}

// builtinStore is the part of the built-in stores that both dialects share.
type builtinStore interface {
	StorePorts
	builtinPortSource
}

// builtinPortSource holds the methods that return a port with the same type in
// both built-in stores.
type builtinPortSource interface {
	DurableOperationsPort() durable.Store
	DriftResults() drift.Store
	GitHost() forge.Store
	Registry() registry.Store
	RegistryKeys() registry.KeyStore
	SavedViews() savedview.Store
	NotificationSinks() notify.SinkStore
	Triggers() trigger.Store
}

// sharedStoreOptions returns the store options that both built-in stores set
// the same way: the ports of st, and the job store jobs with its claimer.
func sharedStoreOptions(cfg *settings, st builtinStore, jobs job.Store) []Option {
	return []Option{
		WithNamespaceStore(st),
		WithStackStore(st),
		WithUpdateStore(st),
		WithCheckpointStore(st),
		WithEventStore(st),
		WithSecretKeyStore(st),
		WithWorkspaceStore(st),
		WithStateStore(st),
		WithInventoryReader(st),
		WithIPAllowlistStore(st),
		WithJobStore(jobs),
		WithJobClaimer(func(g job.GateGuard) job.Claimer {
			return job.NewClaimer(jobs, st.DurableOperationsPort(), st, g, job.WithClaimerLogger(cfg.logger))
		}),
		WithTriggerStore(st.Triggers()),
		WithSavedViewStore(st.SavedViews()),
		WithDriftStore(st.DriftResults()),
		WithGitHostStore(st.GitHost()),
		WithRegistry(st.Registry(), st.RegistryKeys()),
		WithSinkStore(st.NotificationSinks()),
		WithInstallationKindStore(st),
		WithInstanceStore(st),
	}
}

// sqliteStoreOptions returns the store options of the built-in SQLite store.
func sqliteStoreOptions(cfg *settings, st *sqlite.Store) []Option {
	return append(sharedStoreOptions(cfg, st, st.Jobs()),
		WithDurableStore(st.DurableOperations()),
		WithRunners(st.Runners()),
		WithSourceFilters(st.SourceFilters()),
		WithTrustRules(st.TrustRules()),
		WithUnitSettingsStore(st.UnitSettings()),
		WithProfileStore(st.Profiles()),
		WithIssuerStore(st.Issuer()),
		WithIdempotencyStore(st.Idempotency(cfg.secretWrapper)),
	)
}

// postgresStoreOptions returns the store options of the built-in PostgreSQL
// store.
func postgresStoreOptions(cfg *settings, st *postgres.Store) []Option {
	return append(sharedStoreOptions(cfg, st, st.Jobs()),
		WithDurableStore(st.DurableOperations()),
		WithRunners(st.Runners()),
		WithSourceFilters(st.SourceFilters()),
		WithTrustRules(st.TrustRules()),
		WithUnitSettingsStore(st.UnitSettings()),
		WithProfileStore(st.Profiles()),
		WithIssuerStore(st.Issuer()),
		WithIdempotencyStore(st.Idempotency(cfg.secretWrapper)),
	)
}
