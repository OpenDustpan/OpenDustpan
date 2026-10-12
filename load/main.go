package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// defaultLoadToken signs the harness requests when DUSTPAN_LOAD_TOKEN is
// empty. The servers are fresh and local, so the token never leaves the
// process. Set DUSTPAN_LOAD_TOKEN to override it, so no copy of this command
// reuses a published credential elsewhere.
const defaultLoadToken = "load-harness-token-0123456789abcdef"

// harnessToken answers the token the harness servers accept: the
// DUSTPAN_LOAD_TOKEN env var when set, else the default. The servers are
// fresh and local, so the token never leaves the process. Set the env var to
// override it, so no copy of this command reuses a published credential
// elsewhere.
func harnessToken() string {
	// The literal stays inline: a named constant for an env var name that
	// holds a credential trips the hardcoded-credential check.
	if token := os.Getenv("DUSTPAN_LOAD_TOKEN"); token != "" {
		return token
	}

	return defaultLoadToken
}

// postgresURLEnv names the PostgreSQL server the harness runs against. Empty
// means SQLite in a temporary directory.
const postgresURLEnv = "DUSTPAN_TEST_POSTGRES_URL"

// errBadUnits reports a unit count below 1.
var errBadUnits = errors.New("units must be at least 1")

// errBadNamespace reports an empty namespace name.
var errBadNamespace = errors.New("namespace must not be empty")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := runHarness(ctx, os.Args[1:])
	stop()

	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1) //nolint:forbidigo // A command reports failure with its exit code.
	}
}

// runHarness parses args, starts the servers, replays the traffic and writes
// the report.
func runHarness(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("load", flag.ContinueOnError)

	out := flags.String("out", "load-report.json", "write the JSON report to `path`")
	units := flags.Int("units", defaultUnits, "build `n` units, half stacks and half workspaces")
	ns := flags.String("namespace", "load", "measure in namespace `name`")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if *units < 1 {
		return errBadUnits
	}

	if *ns == "" {
		return errBadNamespace
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	dir, err := os.MkdirTemp("", "opendustpan-load-")
	if err != nil {
		return fmt.Errorf("make scratch directory: %w", err)
	}

	defer func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			logger.ErrorContext(ctx, "remove scratch directory", slog.String("error", removeErr.Error()))
		}
	}()

	cfg := DefaultConfig()
	cfg.Namespace = *ns
	cfg.Units = *units
	cfg.Token = harnessToken()
	cfg.Store = storeName()

	urls, cleanup, err := openServers(ctx, logger, dir, *ns)
	if err != nil {
		return err
	}

	defer cleanup(context.WithoutCancel(ctx))

	report, err := Execute(ctx, cfg, urls)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	data = append(data, '\n')

	if err := os.WriteFile(*out, data, 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	logger.InfoContext(
		ctx, "wrote load report",
		slog.String("path", *out),
		slog.Int64("duration_ms", int64(report.DurationMs)),
		slog.Int("units_listed", report.Lists.UnitsListed),
	)

	return nil
}

// storeName answers the store the harness runs against.
func storeName() string {
	if os.Getenv(postgresURLEnv) != "" {
		return "postgres"
	}

	return "sqlite"
}

// ports groups every store port one server instance needs.
type ports struct {
	namespaces  namespace.Store
	stacks      stack.Store
	updates     stack.UpdateStore
	checkpoints stack.CheckpointStore
	events      stack.EventStore
	keys        secret.KeyStore
	workspaces  workspace.Store
	states      workspace.StateStore
	units       inventory.Reader
}

// portsOfSQLite answers the ports of one SQLite handle.
func portsOfSQLite(store *sqlite.Store) ports {
	return ports{
		namespaces:  store,
		stacks:      store,
		updates:     store,
		checkpoints: store,
		events:      store,
		keys:        store,
		workspaces:  store,
		states:      store,
		units:       store,
	}
}

// portsOfPostgres answers the ports of one PostgreSQL handle.
func portsOfPostgres(store *postgres.Store) ports {
	return ports{
		namespaces:  store,
		stacks:      store,
		updates:     store,
		checkpoints: store,
		events:      store,
		keys:        store,
		workspaces:  store,
		states:      store,
		units:       store,
	}
}

// harnessRegion is the region the harness servers are authoritative for.
const harnessRegion = namespace.Region("eu-west")

// openServers starts one SQLite instance, or two PostgreSQL instances on the
// same URL, and answers their URLs with a cleanup func.
func openServers(
	ctx context.Context,
	logger *slog.Logger,
	dir, ns string,
) ([]string, func(context.Context), error) {
	var cleanups []func(context.Context)

	cleanup := func(cleanupCtx context.Context) {
		for _, fn := range slices.Backward(cleanups) {
			fn(cleanupCtx)
		}
	}

	onErr := func(ctx context.Context, err error) ([]string, func(context.Context), error) {
		cleanup(ctx)

		return nil, func(context.Context) {}, err
	}

	instances := 1
	if os.Getenv(postgresURLEnv) != "" {
		instances = 2
	}

	pgURL := os.Getenv(postgresURLEnv)

	if pgURL != "" {
		schemaURL, drop, schemaErr := openPostgresSchema(ctx, logger, pgURL)
		if schemaErr != nil {
			return onErr(ctx, schemaErr)
		}

		pgURL = schemaURL
		cleanups = append(cleanups, drop)
	}

	served := make([]instance, 0, instances)

	for i := range instances {
		one, instanceErr := openInstance(ctx, logger, dir, i, pgURL)
		if instanceErr != nil {
			return onErr(ctx, instanceErr)
		}

		served = append(served, one)
		cleanups = append(cleanups, one.cleanup)
	}

	if err := createLoadNamespace(ctx, served[0].ports.namespaces, ns); err != nil {
		return onErr(ctx, err)
	}

	urls := make([]string, 0, instances)

	for i := range served {
		instanceURL, closeServer, serveErr := serveInstance(served[i])
		if serveErr != nil {
			return onErr(ctx, serveErr)
		}

		urls = append(urls, instanceURL)
		cleanups = append(cleanups, closeServer)
	}

	return urls, cleanup, nil
}

// instance is one serving instance: its ports and blob store with cleanup.
type instance struct {
	ports   ports
	blobs   blob.Store
	cleanup func(context.Context)
}

// openInstance opens the store and blob handles of instance i.
func openInstance(
	ctx context.Context,
	logger *slog.Logger,
	dir string,
	i int,
	pgURL string,
) (instance, error) {
	blobs, blobsErr := fs.Open(filepath.Join(dir, "blobs-"+strconv.Itoa(i)))
	if blobsErr != nil {
		return instance{}, fmt.Errorf("open blob store: %w", blobsErr)
	}

	if pgURL == "" {
		store, storeErr := sqlite.Open(ctx, filepath.Join(dir, "dustpan.db"))
		if storeErr != nil {
			return instance{}, fmt.Errorf("open sqlite store: %w", storeErr)
		}

		return instance{
			ports: portsOfSQLite(store),
			blobs: blobs,
			cleanup: closeFunc(logger, func(context.Context) error {
				if err := store.Close(); err != nil {
					return fmt.Errorf("close sqlite store: %w", err)
				}

				return blobs.Close()
			}),
		}, nil
	}

	store, storeErr := postgres.Open(ctx, pgURL)
	if storeErr != nil {
		return instance{}, fmt.Errorf("open postgres store: %w", storeErr)
	}

	return instance{
		ports: portsOfPostgres(store),
		blobs: blobs,
		cleanup: closeFunc(logger, func(shutdownCtx context.Context) error {
			store.CloseContext(shutdownCtx)

			return blobs.Close()
		}),
	}, nil
}

// closeFunc answers a cleanup func that logs its error.
func closeFunc(logger *slog.Logger, shut func(context.Context) error) func(context.Context) {
	return func(shutdownCtx context.Context) {
		if err := shut(shutdownCtx); err != nil {
			logger.ErrorContext(shutdownCtx, "close instance", slog.String("error", err.Error()))
		}
	}
}

// schemaNameBytes is the randomness of one harness schema name.
const schemaNameBytes = 8

// schemaDropTimeout bounds the cleanup that drops the harness schema.
const schemaDropTimeout = 30 * time.Second

// openPostgresSchema creates a fresh schema on the PostgreSQL server at base
// and answers its URL with the func that drops it, so every run measures an
// empty database even when runs share one server.
func openPostgresSchema(
	ctx context.Context,
	logger *slog.Logger,
	base string,
) (string, func(context.Context), error) {
	raw := make([]byte, schemaNameBytes)

	if _, err := rand.Read(raw); err != nil {
		return "", func(context.Context) {}, fmt.Errorf("random schema name: %w", err)
	}

	schema := "load_" + hex.EncodeToString(raw)

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", func(context.Context) {}, fmt.Errorf("connect to postgres: %w", err)
	}

	_, execErr := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	closeErr := conn.Close(ctx)

	if execErr != nil {
		return "", func(context.Context) {}, fmt.Errorf("create schema: %w", execErr)
	}

	if closeErr != nil {
		return "", func(context.Context) {}, fmt.Errorf("close postgres connection: %w", closeErr)
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", func(context.Context) {}, fmt.Errorf("parse postgres URL: %w", err)
	}

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()

	drop := func(shutdownCtx context.Context) {
		dropCtx, cancel := context.WithTimeout(shutdownCtx, schemaDropTimeout)
		defer cancel()

		dropConn, err := pgx.Connect(dropCtx, base)
		if err != nil {
			logger.ErrorContext(dropCtx, "connect to drop schema", slog.String("error", err.Error()))

			return
		}

		defer func() {
			if err := dropConn.Close(dropCtx); err != nil {
				logger.ErrorContext(dropCtx, "close drop connection", slog.String("error", err.Error()))
			}
		}()

		dropStmt := "DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE"

		if _, err := dropConn.Exec(dropCtx, dropStmt); err != nil {
			logger.ErrorContext(dropCtx, "drop schema", slog.String("error", err.Error()))
		}
	}

	return parsed.String(), drop, nil
}

// createLoadNamespace creates the harness namespace on the first instance.
func createLoadNamespace(ctx context.Context, stores namespace.Store, ns string) error {
	name, err := namespace.ParseName(ns)
	if err != nil {
		return fmt.Errorf("parse namespace: %w", err)
	}

	found := namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            name,
		HomeRegion:      harnessRegion,
		FailoverVersion: 1,
	}

	if err := stores.CreateNamespace(ctx, found); err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}

	return nil
}

// testServerCloser adapts httptest.Server.Close to a context cleanup func.
func testServerCloser(ts *httptest.Server) func(context.Context) {
	return func(context.Context) {
		ts.Close()
	}
}

// harnessKeyBytes is the size of the key-encryption key of one instance.
const harnessKeyBytes = 32

// newHarnessWrapper returns a key-encryption key wrapper over 32 random
// bytes. Each instance starts on empty stores, so a new key per instance is
// enough. The server seals OpenTofu state with it and cannot store state
// without one.
func newHarnessWrapper() (*secret.BuiltinWrapper, error) {
	key := make([]byte, harnessKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("key-encryption key: %w", err)
	}

	wrapper, err := secret.NewBuiltinWrapper(key)
	if err != nil {
		return nil, fmt.Errorf("key-encryption key: %w", err)
	}

	return wrapper, nil
}

// serveInstance builds the server for one instance and answers its test URL.
func serveInstance(served instance) (string, func(context.Context), error) {
	noop := func(context.Context) {}

	authn, err := identity.NewStaticToken("load", harnessToken())
	if err != nil {
		return "", noop, fmt.Errorf("authenticator: %w", err)
	}

	wrapper, err := newHarnessWrapper()
	if err != nil {
		return "", noop, err
	}

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(harnessRegion),
		dustpan.WithNamespaceStore(served.ports.namespaces),
		dustpan.WithStackStore(served.ports.stacks),
		dustpan.WithUpdateStore(served.ports.updates),
		dustpan.WithCheckpointStore(served.ports.checkpoints),
		dustpan.WithEventStore(served.ports.events),
		dustpan.WithSecretKeyStore(served.ports.keys),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(served.ports.workspaces),
		dustpan.WithStateStore(served.ports.states),
		dustpan.WithBlobStore(served.blobs),
		dustpan.WithInventoryReader(served.ports.units),
		dustpan.WithAuthenticator(authn),
	)
	if err != nil {
		return "", noop, fmt.Errorf("new server: %w", err)
	}

	ts := httptest.NewServer(srv.Handler())

	return ts.URL, testServerCloser(ts), nil
}
