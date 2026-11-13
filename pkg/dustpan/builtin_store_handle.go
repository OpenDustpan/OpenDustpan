package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// StorePorts holds every persistence port of a built-in store.
type StorePorts interface {
	UnitStorePorts
	AccessStorePorts
	LocalUserStorePorts
	InstallationStorePorts
}

// UnitStorePorts holds the ports of namespaces, stacks, secrets, workspaces,
// collections and placement rules.
type UnitStorePorts interface {
	namespace.Store
	stackStorePorts
	secret.KeyStore
	workspaceStorePorts
	collection.Store
	collection.PlacementStore
}

// stackStorePorts holds the ports of the stacks.
type stackStorePorts interface {
	stack.Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
}

// workspaceStorePorts holds the ports of the workspaces.
type workspaceStorePorts interface {
	workspace.Store
	workspace.StateStore
}

// AccessStorePorts holds the ports of the inventory and of the principals.
type AccessStorePorts interface {
	inventory.Reader
	ipallow.Store
	audit.ServerStore
	principal.Store
	accesstoken.Store
	accesstoken.HostStore
	tempadmin.Store
}

// LocalUserStorePorts holds the ports of the local users, their passwords and
// their grants.
type LocalUserStorePorts interface {
	localuser.Store
	localuser.PasswordStore
	access.GrantStore
	access.GrantLookups
}

// InstallationStorePorts holds the ports of the installation.
type InstallationStorePorts interface {
	installation.KindStore
	installation.InstanceStore
	// gate.Store is in the set so that a server that uses a built-in store
	// always runs with the state.write gates on: a store wrapper that does
	// not keep gates does not compile.
	gate.Store
}

// errStoreDatabaseForm reports a database value that names no built-in store.
var errStoreDatabaseForm = errors.New(
	"database must start with sqlite:<path>, postgres:// or postgresql://",
)

// errNilBuiltinStore reports WithBuiltinStore without a store.
var errNilBuiltinStore = errors.New("WithBuiltinStore: the store is nil")

// BuiltinStore is an opened built-in store. OpenStore makes it. It implements
// every port of StorePorts.
type BuiltinStore struct {
	storePorts

	options               func(*settings) []Option
	settingStore          setting.Store
	settingDeclarations   []setting.Declaration
	minimumBinaryVersions func(context.Context) ([]string, error)
	closeStore            builtinStoreCloser
}

// storePorts carries the persistence ports of a built-in store.
type storePorts struct {
	StorePorts
}

// builtinStoreParts holds what newBuiltinStore keeps from an opened adapter.
type builtinStoreParts struct {
	ports                 StorePorts
	options               func(*settings) []Option
	settingStore          setting.Store
	settingDeclarations   []setting.Declaration
	minimumBinaryVersions func(context.Context) ([]string, error)
	close                 func() error
}

// newBuiltinStore returns the handle of an opened built-in store.
func newBuiltinStore(parts builtinStoreParts) *BuiltinStore {
	return &BuiltinStore{
		storePorts:            storePorts{parts.ports},
		options:               parts.options,
		settingStore:          parts.settingStore,
		settingDeclarations:   parts.settingDeclarations,
		minimumBinaryVersions: parts.minimumBinaryVersions,
		closeStore:            builtinStoreCloser{mu: sync.Mutex{}, close: parts.close},
	}
}

// OpenStore opens the built-in store that database names. The accepted forms
// are sqlite:<path>, postgres://... and postgresql://.... A value of another
// form fails with an error that names the accepted forms and repeats no part
// of the value. The caller closes the store.
func OpenStore(ctx context.Context, database string, opts ...StoreOption) (*BuiltinStore, error) {
	return openStoreConfig(ctx, database, newStoreConfig(opts))
}

// openStoreConfig opens the built-in store that database names with config.
func openStoreConfig(ctx context.Context, database string, config storeConfig) (*BuiltinStore, error) {
	switch {
	case strings.HasPrefix(database, "sqlite:"):
		return openSQLiteStore(ctx, strings.TrimPrefix(database, "sqlite:"), config)
	case postgresDSNForm(database):
		return openPostgresStore(ctx, database, config)
	default:
		return nil, errStoreDatabaseForm
	}
}

// postgresDSNForm reports whether dsn names a PostgreSQL store: it starts with
// postgres:// or postgresql://. A value of another form must never reach the
// driver, which then takes the host, the user and the database from its
// environment defaults.
func postgresDSNForm(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// openSQLiteStore opens the SQLite store at path.
func openSQLiteStore(ctx context.Context, path string, config storeConfig) (*BuiltinStore, error) {
	st, err := sqlite.Open(ctx, path, config.sqliteOptions()...)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}

	return newBuiltinStore(builtinStoreParts{
		ports:                 st,
		options:               func(cfg *settings) []Option { return sqliteStoreOptions(cfg, st) },
		settingStore:          st.SettingStore(),
		settingDeclarations:   nil,
		minimumBinaryVersions: minimumBinaryVersionsOf(st),
		close:                 st.Close,
	}), nil
}

// openPostgresStore opens the PostgreSQL store at dsn. It refuses a dsn of
// another form with errStoreDatabaseForm and opens nothing.
func openPostgresStore(ctx context.Context, dsn string, config storeConfig) (*BuiltinStore, error) {
	// Defense in depth: openStoreConfig and openBuiltinPostgres check the form
	// too; this check stays so that a new caller cannot skip it.
	if !postgresDSNForm(dsn) {
		return nil, errStoreDatabaseForm
	}

	st, err := postgres.Open(ctx, dsn, config.postgresOptions()...)
	if err != nil {
		return nil, fmt.Errorf("open postgres store: %w", err)
	}

	logFailoverSettings(ctx, config.loggerValue(), st.FailoverSettings())

	return newBuiltinStore(builtinStoreParts{
		ports:                 st,
		options:               func(cfg *settings) []Option { return postgresStoreOptions(cfg, st) },
		settingStore:          st.SettingStore(),
		settingDeclarations:   postgres.SettingDeclarations(),
		minimumBinaryVersions: minimumBinaryVersionsOf(st),
		close: func() error {
			st.CloseContext(context.WithoutCancel(ctx))

			return nil
		},
	}), nil
}

// minimumBinaryVersionsOf returns the minimum binary version of each applied
// migration of st.
func minimumBinaryVersionsOf(st appliedMigrationsStore) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		applied, err := st.AppliedMigrations(ctx)
		if err != nil {
			return nil, err
		}

		minimums := make([]string, 0, len(applied))
		for _, migration := range applied {
			minimums = append(minimums, migration.MinBinaryVersion)
		}

		return minimums, nil
	}
}

// appliedMigrationsStore reads the applied migrations of an open store.
type appliedMigrationsStore interface {
	AppliedMigrations(ctx context.Context) ([]schema.AppliedMigration, error)
}

// BuiltinSettingStore is the store of the settings resource of a built-in
// store.
type BuiltinSettingStore struct {
	setting.Store
}

// SettingStore answers the store of the settings resource.
func (b *BuiltinStore) SettingStore() BuiltinSettingStore {
	return BuiltinSettingStore{b.settingStore}
}

// SettingDeclarations answers the declarations of the settings of the store
// itself: the bounds of a PostgreSQL store, and none for a SQLite store.
func (b *BuiltinStore) SettingDeclarations() []setting.Declaration {
	return b.settingDeclarations
}

// AppliedMinimumBinaryVersions answers the minimum binary version of each
// applied migration.
func (b *BuiltinStore) AppliedMinimumBinaryVersions(ctx context.Context) ([]string, error) {
	return b.minimumBinaryVersions(ctx)
}

// Close closes the store. A second call returns nil.
func (b *BuiltinStore) Close() error {
	return b.closeStore.Close()
}

// logFailoverSettings writes the effective failover settings of the pool
// once at the start: each value and where it came from. The record holds no
// host, user or password.
func logFailoverSettings(ctx context.Context, logger *slog.Logger, settings postgres.FailoverSettings) {
	// One group for each setting, then the check, the deadline and the three
	// server timeouts.
	const extraAttrs = 5

	attrs := make([]any, 0, len(settings.Settings)+extraAttrs)

	for _, item := range settings.Settings {
		attrs = append(attrs, slog.Group(item.Name,
			slog.String("value", item.Value), slog.String("from", item.Source)))
	}

	attrs = append(attrs, slog.String("connect_check", settings.ConnectCheck),
		slog.Duration("statement_deadline", settings.StatementDeadline),
		slog.Duration("statement_timeout", settings.ServerStatementTimeout),
		slog.Duration("idle_in_transaction_session_timeout", settings.ServerIdleInTransactionTimeout),
		slog.Duration("client_connection_check_interval", settings.ClientConnectionCheckInterval))

	logger.InfoContext(ctx, "database failover settings", attrs...)
}

// builtinStoreOpenTimeout bounds the open of a built-in store that NewServer
// opens. NewServer has no context, so the open gets its own bound.
const builtinStoreOpenTimeout = 30 * time.Second
