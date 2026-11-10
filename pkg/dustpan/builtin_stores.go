package dustpan

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// builtinStoreOpenTimeout bounds the open of a built-in store. NewServer has
// no context, so the open gets its own bound.
const builtinStoreOpenTimeout = 30 * time.Second

// errNilSQLiteStore reports WithOpenedSQLite without a store.
var errNilSQLiteStore = errors.New("WithOpenedSQLite: the store is nil")

// errNilPostgresStore reports WithOpenedPostgreSQL without a store.
var errNilPostgresStore = errors.New("WithOpenedPostgreSQL: the store is nil")

// StoreOption configures a built-in store that WithSQLite or WithPostgreSQL
// opens.
type StoreOption func(*builtinStoreConfig)

// builtinStoreConfig collects the options of a built-in store before NewServer
// opens the store.
type builtinStoreConfig struct {
	withoutMigrate bool
}

// WithoutStoreMigrate makes the built-in store apply no pending migration and
// refuse a database with a pending migration.
func WithoutStoreMigrate() StoreOption {
	return func(c *builtinStoreConfig) { c.withoutMigrate = true }
}

// builtinStoreSource says which option set the built-in store.
type builtinStoreSource int

const (
	builtinStoreNone builtinStoreSource = iota
	builtinStoreSQLitePath
	builtinStorePostgresDSN
	builtinStoreSQLiteOpened
	builtinStorePostgresOpened
)

// builtinStoreSettings is the built-in store of a server: the path or DSN that
// NewServer opens, or the store that the caller opened.
type builtinStoreSettings struct {
	source   builtinStoreSource
	path     string
	dsn      string
	config   builtinStoreConfig
	sqlite   *sqlite.Store
	postgres *postgres.Store
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
	settings := builtinStoreSettings{
		source: kind, path: "", dsn: "", config: builtinStoreConfig{withoutMigrate: false},
		sqlite: nil, postgres: nil, open: nil, close: nil,
	}

	for _, opt := range opts {
		opt(&settings.config)
	}

	return settings
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

// WithOpenedSQLite sets every store option of the server from st, which the
// caller opened. The caller closes st. The store options apply after all other
// options, so they replace a With...Store option for the same port.
func WithOpenedSQLite(st *sqlite.Store) Option {
	return func(o *settings) {
		builtin := newBuiltinStoreSettings(builtinStoreSQLiteOpened, nil)
		builtin.sqlite = st
		o.builtinStore = builtin
	}
}

// WithOpenedPostgreSQL sets every store option of the server from st, which
// the caller opened. The caller closes st. The store options apply after all
// other options, so they replace a With...Store option for the same port.
func WithOpenedPostgreSQL(st *postgres.Store) Option {
	return func(o *settings) {
		builtin := newBuiltinStoreSettings(builtinStorePostgresOpened, nil)
		builtin.postgres = st
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
	case builtinStoreSQLiteOpened:
		return useOpenedSQLite(cfg, &cfg.builtinStore)
	case builtinStorePostgresOpened:
		return useOpenedPostgres(cfg, &cfg.builtinStore)
	}

	return nil
}

// openBuiltinSQLite opens the SQLite store of builtin and sets its options.
func openBuiltinSQLite(cfg *settings, builtin *builtinStoreSettings) error {
	st, err := openSQLite(builtin.path, sqliteOpenOptions(cfg, builtin)...)
	if err != nil {
		return fmt.Errorf("WithSQLite: open the store: %w", err)
	}

	builtin.sqlite = st
	builtin.close = st.Close
	cfg.setStoreOptions(sqliteStoreOptions(cfg, st))

	return nil
}

// openBuiltinPostgres opens the PostgreSQL store of builtin and sets its
// options.
func openBuiltinPostgres(cfg *settings, builtin *builtinStoreSettings) error {
	st, err := openPostgres(builtin.dsn, postgresOpenOptions(cfg, builtin)...)
	if err != nil {
		return fmt.Errorf("WithPostgreSQL: open the store: %w", err)
	}

	builtin.postgres = st
	builtin.close = func() error {
		st.Close()
		return nil
	}
	cfg.setStoreOptions(postgresStoreOptions(cfg, st))

	return nil
}

// useOpenedSQLite sets the store options from the SQLite store of builtin.
func useOpenedSQLite(cfg *settings, builtin *builtinStoreSettings) error {
	if builtin.sqlite == nil {
		return errNilSQLiteStore
	}

	cfg.setStoreOptions(sqliteStoreOptions(cfg, builtin.sqlite))

	return nil
}

// useOpenedPostgres sets the store options from the PostgreSQL store of
// builtin.
func useOpenedPostgres(cfg *settings, builtin *builtinStoreSettings) error {
	if builtin.postgres == nil {
		return errNilPostgresStore
	}

	cfg.setStoreOptions(postgresStoreOptions(cfg, builtin.postgres))

	return nil
}

// sqliteOpenOptions returns the options of the open of a built-in SQLite
// store.
func sqliteOpenOptions(cfg *settings, builtin *builtinStoreSettings) []sqlite.Option {
	opts := []sqlite.Option{
		sqlite.WithLogger(cfg.logger),
		sqlite.WithBinaryVersion(cfg.version),
		sqlite.WithInstallationKind(cfg.installationKind),
	}
	if builtin.config.withoutMigrate {
		opts = append(opts, sqlite.WithoutMigrate())
	}

	return opts
}

// postgresOpenOptions returns the options of the open of a built-in PostgreSQL
// store.
func postgresOpenOptions(cfg *settings, builtin *builtinStoreSettings) []postgres.Option {
	opts := []postgres.Option{
		postgres.WithLogger(cfg.logger),
		postgres.WithBinaryVersion(cfg.version),
		postgres.WithInstallationKind(cfg.installationKind),
	}
	if builtin.config.withoutMigrate {
		opts = append(opts, postgres.WithoutMigrate())
	}

	return opts
}

// openSQLite opens the SQLite store at path.
func openSQLite(path string, opts ...sqlite.Option) (*sqlite.Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), builtinStoreOpenTimeout)
	defer cancel()

	return sqlite.Open(ctx, path, opts...)
}

// openPostgres opens the PostgreSQL store at dsn.
func openPostgres(dsn string, opts ...postgres.Option) (*postgres.Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), builtinStoreOpenTimeout)
	defer cancel()

	return postgres.Open(ctx, dsn, opts...)
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
	builtinUnitStore
	builtinInstallationStore
	builtinPortSource
}

// builtinUnitStore holds the ports of namespaces, stacks, secrets and
// workspaces that a built-in store implements itself.
type builtinUnitStore interface {
	namespace.Store
	stack.Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
	secret.KeyStore
	workspace.Store
	workspace.StateStore
}

// builtinInstallationStore holds the other ports that a built-in store
// implements itself.
type builtinInstallationStore interface {
	inventory.Reader
	ipallow.Store
	installation.KindStore
	installation.InstanceStore
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
