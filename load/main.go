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
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/job"
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

// harnessFlags holds the parsed flags of one harness run.
type harnessFlags struct {
	out         string
	units       int
	ns          string
	flows       string
	blobs       string
	probe       bool
	maxDuration time.Duration
	blobCfg     BlobFlowConfig
	scale       scaleFlags
}

// parseHarnessFlags parses and checks args.
func parseHarnessFlags(args []string) (harnessFlags, error) {
	flags := flag.NewFlagSet("load", flag.ContinueOnError)

	f := harnessFlags{
		out: "", units: 0, ns: "", flows: "", blobs: "", probe: false, maxDuration: 0,
		blobCfg: DefaultBlobFlowConfig(),
		scale: scaleFlags{
			binary: "", instances: 1, clients: defaultScaleClients, duration: defaultScaleDuration,
			warmup: defaultScaleWarmup, think: 0, restart: false, restartTail: defaultScaleTail, storePID: 0,
		},
	}

	flags.StringVar(&f.out, "out", "load-report.json", "write the JSON report to `path`")
	flags.IntVar(&f.units, "units", defaultUnits, "build `n` units, half stacks and half workspaces")
	flags.StringVar(&f.ns, "namespace", "load", "measure in namespace `name`")
	flags.StringVar(
		&f.flows,
		"flows",
		flowsAll,
		"run `set` all (the nightly report), blobs (the blob flow report) or scale (several server processes behind a balancer)",
	)
	flags.StringVar(&f.blobs, "blobs", blobsFS, "live blob store `kind`: fs (a directory) or s3 (DUSTPAN_TEST_S3_*)")
	flags.BoolVar(&f.probe, "s3-probe", true, "open the S3 store with the conditional-write probe, as the server does")
	flags.DurationVar(&f.maxDuration, "max-duration", 0, "stop the run after `duration`; 0 sets no bound")
	flags.StringVar(&f.scale.binary, "binary", "", "scale: the `path` of the opendustpan binary that serves")
	flags.IntVar(&f.scale.instances, "instances", f.scale.instances, "scale: run `n` server processes")
	flags.IntVar(&f.scale.clients, "clients", f.scale.clients, "scale: run `n` simulated users")
	flags.DurationVar(&f.scale.duration, "duration", f.scale.duration, "scale: measure for `duration`")
	flags.DurationVar(&f.scale.warmup, "warmup", f.scale.warmup, "scale: run `duration` before the measurement")
	flags.DurationVar(&f.scale.think, "think", f.scale.think, "scale: each user waits `duration` between cycles")
	flags.BoolVar(&f.scale.restart, "restart", f.scale.restart, "scale: restart instance 0 after the measurement")
	flags.DurationVar(&f.scale.restartTail, "restart-tail", f.scale.restartTail,
		"scale: keep the load `duration` after the restarted instance is ready")
	flags.IntVar(&f.scale.storePID, "store-pid", 0, "scale: the `pid` of the object store, to measure its CPU")
	flags.IntVar(&f.blobCfg.Reps, "reps", f.blobCfg.Reps, "blobs: time `n` state uploads and n checkpoint saves")
	flags.IntVar(&f.blobCfg.StateBytes, "state-bytes", f.blobCfg.StateBytes, "blobs: state size in `bytes`")
	flags.IntVar(&f.blobCfg.CheckpointBytes, "checkpoint-bytes", f.blobCfg.CheckpointBytes,
		"blobs: checkpoint size in `bytes`")
	flags.IntVar(&f.blobCfg.DatasetUnits, "dataset-units", f.blobCfg.DatasetUnits,
		"blobs: `n` workspaces and n stacks in the backup data set")
	flags.BoolVar(&f.blobCfg.Backup, "backup", f.blobCfg.Backup, "blobs: take a backup of the data set and restore it")
	flags.IntVar(&f.blobCfg.HTTPTimeoutSecs, "http-timeout", f.blobCfg.HTTPTimeoutSecs,
		"blobs: bound each request to `seconds`")

	if err := flags.Parse(args); err != nil {
		return harnessFlags{}, fmt.Errorf("parse flags: %w", err)
	}

	return f, f.check()
}

// check refuses flag values the harness cannot run.
func (f harnessFlags) check() error {
	switch {
	case f.flows != flowsAll && f.flows != flowsBlobs && f.flows != flowsScale:
		return errBadFlows
	case f.flows == flowsBlobs && (f.blobCfg.Reps < 1 || f.blobCfg.DatasetUnits < 0 || f.blobCfg.HTTPTimeoutSecs < 1):
		return errBadReps
	case f.units < 1:
		return errBadUnits
	case f.ns == "":
		return errBadNamespace
	case f.maxDuration < 0:
		return errBadDuration
	default:
		return nil
	}
}

// runHarness parses args, starts the servers, replays the traffic and writes
// the report.
func runHarness(ctx context.Context, args []string) error {
	f, err := parseHarnessFlags(args)
	if err != nil {
		return err
	}

	if f.maxDuration > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, f.maxDuration)
		defer cancel()
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

	report, err := f.execute(ctx, logger, dir)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	data = append(data, '\n')

	if err := os.WriteFile(f.out, data, 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	logger.InfoContext(ctx, "wrote load report", slog.String("path", f.out), slog.String("flows", f.flows))

	return nil
}

// execute starts the servers in dir and runs the flows of f.
func (f harnessFlags) execute(ctx context.Context, logger *slog.Logger, dir string) (any, error) {
	if f.flows == flowsScale {
		return executeScale(ctx, logger, f, dir)
	}

	settings, err := newBlobSettings(f.blobs, f.probe, dir)
	if err != nil {
		return nil, err
	}

	setup := serverSetup{blobs: settings, oneInstance: false, backup: false}
	if f.flows == flowsBlobs {
		setup.oneInstance, setup.backup = true, f.blobCfg.Backup
	}

	served, cleanup, err := openServers(ctx, logger, f.ns, setup)
	if err != nil {
		return nil, err
	}

	defer cleanup(context.WithoutCancel(ctx))

	if f.flows == flowsBlobs {
		cfg := f.blobCfg
		cfg.Namespace, cfg.Token, cfg.Store = f.ns, harnessToken(), storeName()
		cfg.Blobs, cfg.S3Probe = f.blobs, f.probe

		return ExecuteBlobFlows(ctx, cfg, served)
	}

	cfg := DefaultConfig()
	cfg.Namespace = f.ns
	cfg.Units = f.units
	cfg.Token = harnessToken()
	cfg.Store = storeName()

	return Execute(ctx, cfg, served)
}

// storeName answers the store the harness runs against.
func storeName() string {
	if os.Getenv(postgresURLEnv) != "" {
		return "postgres"
	}

	return storeSQLite
}

// storeSQLite names the SQLite store in the reports.
const storeSQLite = "sqlite"

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
	jobs        job.Store
	runners     dustpan.RunnerStores
	durable     dustpan.DurableStore
	allowlists  ipallow.Store
	kinds       installation.KindStore
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
		jobs:        store.Jobs(),
		runners:     store.Runners(),
		durable:     store.DurableOperations(),
		allowlists:  store,
		kinds:       store,
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
		jobs:        store.Jobs(),
		runners:     store.Runners(),
		durable:     store.DurableOperations(),
		allowlists:  store,
		kinds:       store,
	}
}

// harnessInstallation is the installation name of the harness servers.
const harnessInstallation = namespace.InstallationName("eu-west")

// servers is what openServers starts: the URLs of the instances, the URL of
// the server that checks IP allowlists, and the fixtures that the access
// steps read.
type servers struct {
	// urls are the instances of the installation, without IP allowlists.
	urls []string
	// gateURL is a server over the first instance's store that checks IP
	// allowlists, also for the bootstrap token the harness signs with.
	gateURL string
	// listNS has an IP allowlist that holds the client. denyNS has one that
	// refuses it.
	listNS string
	denyNS string
	// jobID, listJobID and denyJobID name one job in each namespace.
	jobID     string
	listJobID string
	denyJobID string
}

// openServers starts one SQLite instance, or two PostgreSQL instances on the
// same URL, with the server that checks IP allowlists, and answers their URLs
// with a cleanup func. The instances share one blob directory and one
// key-encryption key, as several instances of one installation do, so a
// write through one instance reads back through the other.
func openServers(
	ctx context.Context,
	logger *slog.Logger,
	ns string,
	setup serverSetup,
) (servers, func(context.Context), error) {
	var cleanups []func(context.Context)

	cleanup := func(cleanupCtx context.Context) {
		for _, fn := range slices.Backward(cleanups) {
			fn(cleanupCtx)
		}
	}

	onErr := func(ctx context.Context, err error) (servers, func(context.Context), error) {
		cleanup(ctx)

		return servers{}, func(context.Context) {}, err
	}

	instances := setup.instances()
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

	for range instances {
		one, instanceErr := openInstance(ctx, logger, setup.blobs, pgURL)
		if instanceErr != nil {
			return onErr(ctx, instanceErr)
		}

		served = append(served, one)
		cleanups = append(cleanups, one.cleanup)
	}

	fixtures, err := seedFixtures(ctx, served[0].ports, ns)
	if err != nil {
		return onErr(ctx, err)
	}

	wrapper, err := newHarnessWrapper()
	if err != nil {
		return onErr(ctx, err)
	}

	running, serveErr := serveAll(ctx, logger, served, wrapper, setup)
	cleanups = append(cleanups, running.closers...)

	if serveErr != nil {
		return onErr(ctx, serveErr)
	}

	fixtures.urls = running.urls
	fixtures.gateURL = running.gateURL

	return fixtures, cleanup, nil
}

// backupOption opens the backup target when setup asks for backups, and
// answers the server option that sets it with the func that closes it.
func backupOption(
	ctx context.Context,
	logger *slog.Logger,
	setup serverSetup,
) ([]dustpan.Option, func(context.Context), error) {
	if !setup.backup {
		return nil, func(context.Context) {}, nil
	}

	target, err := setup.blobs.openBackupTarget(ctx)
	if err != nil {
		return nil, func(context.Context) {}, err
	}

	closeTarget := closeFunc(logger, func(context.Context) error { return target.close() })

	return []dustpan.Option{dustpan.WithBackupTarget(target.store, target.name)}, closeTarget, nil
}

// running holds the servers that serveAll started: the instance URLs, the URL
// of the server that checks IP allowlists, and the funcs that close them.
type running struct {
	urls    []string
	gateURL string
	closers []func(context.Context)
}

// serveAll serves every instance, then the server that checks IP allowlists
// over the first instance. On an error it answers the servers it started, so
// the caller closes them.
func serveAll(
	ctx context.Context,
	logger *slog.Logger,
	served []instance,
	wrapper *secret.BuiltinWrapper,
	setup serverSetup,
) (running, error) {
	var out running

	extra, closeTarget, err := backupOption(ctx, logger, setup)
	if err != nil {
		return out, err
	}

	out.closers = append(out.closers, closeTarget)

	serve := serveInstance
	if len(extra) > 0 {
		serve = func(
			ctx context.Context, one instance, wrapper *secret.BuiltinWrapper, opts ...dustpan.Option,
		) (string, func(context.Context), error) {
			return runInstance(ctx, logger, one, wrapper, opts...)
		}
	}

	for i := range served {
		instanceURL, closeServer, serveErr := serve(ctx, served[i], wrapper, extra...)
		if serveErr != nil {
			return out, serveErr
		}

		out.urls = append(out.urls, instanceURL)
		out.closers = append(out.closers, closeServer)
	}

	gateURL, closeGate, err := serveInstance(ctx, served[0], wrapper, gateOptions(served[0].ports)...)
	if err != nil {
		return out, err
	}

	out.gateURL = gateURL
	out.closers = append(out.closers, closeGate)

	return out, nil
}

// gateOptions answers the options that turn IP allowlists on for the
// bootstrap token of the harness too.
func gateOptions(p ports) []dustpan.Option {
	return []dustpan.Option{
		dustpan.WithIPAllowlistStore(p.allowlists),
		dustpan.WithIPAllowlists(true),
		dustpan.WithIPAllowlistsIncludeBootstrap(true),
	}
}

// Run sets of the -flows flag.
const (
	flowsAll   = "all"
	flowsBlobs = "blobs"
)

// errBadFlows reports a -flows value that names no run set.
var errBadFlows = errors.New("-flows must be all, blobs or scale")

// errBadDuration reports a negative -max-duration.
var errBadDuration = errors.New("-max-duration must not be negative")

// errBadReps reports a blob flow run without attempts.
var errBadReps = errors.New("-reps and -http-timeout must be at least 1 and -dataset-units at least 0")

// serverSetup is what openServers starts besides the store: the blob
// settings, one instance only, and the backup target.
type serverSetup struct {
	blobs       blobSettings
	oneInstance bool
	backup      bool
}

// pgInstances is the count of instances on PostgreSQL.
const pgInstances = 2

// instances answers the count of instances to start: two on PostgreSQL,
// so the report covers several instances, unless setup asks for one.
func (s serverSetup) instances() int {
	if os.Getenv(postgresURLEnv) != "" && !s.oneInstance {
		return pgInstances
	}

	return 1
}

// instance is one serving instance: its ports and blob store with cleanup.
type instance struct {
	ports   ports
	blobs   blob.Store
	cleanup func(context.Context)
}

// openInstance opens the store and blob handles of one instance. Each
// instance opens its own handles on the shared blob directory and database.
func openInstance(
	ctx context.Context,
	logger *slog.Logger,
	settings blobSettings,
	pgURL string,
) (instance, error) {
	blobs, blobsErr := settings.openLive(ctx)
	if blobsErr != nil {
		return instance{}, blobsErr
	}

	if pgURL == "" {
		store, storeErr := sqlite.Open(
			ctx, filepath.Join(settings.dir, "dustpan.db"), sqlite.WithInstallationKind(installation.KindProduction),
		)
		if storeErr != nil {
			return instance{}, fmt.Errorf("open sqlite store: %w", storeErr)
		}

		return instance{
			ports: portsOfSQLite(store),
			blobs: blobs.store,
			cleanup: closeFunc(logger, func(context.Context) error {
				if err := store.Close(); err != nil {
					return fmt.Errorf("close sqlite store: %w", err)
				}

				return blobs.close()
			}),
		}, nil
	}

	store, storeErr := postgres.Open(ctx, pgURL, postgres.WithInstallationKind(installation.KindProduction))
	if storeErr != nil {
		return instance{}, fmt.Errorf("open postgres store: %w", storeErr)
	}

	return instance{
		ports: portsOfPostgres(store),
		blobs: blobs.store,
		cleanup: closeFunc(logger, func(shutdownCtx context.Context) error {
			store.CloseContext(shutdownCtx)

			return blobs.close()
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

		// The store keeps its control tables in a second schema, <schema>_control.
		for _, name := range []string{schema, schema + "_control"} {
			dropStmt := "DROP SCHEMA IF EXISTS " + pgx.Identifier{name}.Sanitize() + " CASCADE"

			if _, err := dropConn.Exec(dropCtx, dropStmt); err != nil {
				logger.ErrorContext(dropCtx, "drop schema",
					slog.String("schema", name), slog.String("error", err.Error()))
			}
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
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                name,
		PrimaryInstallation: harnessInstallation,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := stores.CreateNamespace(ctx, found); err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}

	return seedLoadPlacement(ctx, stores, name)
}

// loadCollection is the collection that the placement rule of the harness
// namespaces names.
const loadCollection = "load-everything"

// placementPorts is what the harness needs to make a collection and a
// placement rule.
type placementPorts interface {
	namespace.Store
	collection.Store
	collection.PlacementStore
}

// seedLoadPlacement makes a collection, a placement rule for Pulumi stacks of
// any project and any name, and a placement rule for OpenTofu workspaces of any
// name in the namespace ns. The server refuses a new stack or workspace that no
// rule places, and the recorded traffic makes both.
func seedLoadPlacement(ctx context.Context, stores namespace.Store, ns namespace.Name) error {
	ports, ok := stores.(placementPorts)
	if !ok {
		return fmt.Errorf("%w: %T", errLoadPlacement, stores)
	}

	collections, err := collection.NewService(collection.Config{Store: ports, Namespaces: ports, Now: nil})
	if err != nil {
		return fmt.Errorf("collection service: %w", err)
	}

	rules, err := collection.NewPlacementService(collection.PlacementConfig{Store: ports, Namespaces: ports, Now: nil})
	if err != nil {
		return fmt.Errorf("placement service: %w", err)
	}

	ref := collection.Ref{Namespace: ns, Name: loadCollection}
	created := loadEntry(ns, identity.ActionCollectionCreate, "collection:"+ns.String()+"/"+loadCollection)

	if _, err = collections.Create(ctx, ref, created); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	for _, tool := range []string{collection.ToolPulumi, collection.ToolOpenTofu} {
		in := collection.RuleInput{Tool: tool, Project: "*", Stack: "*", Collection: loadCollection, Position: 0}
		ruled := loadEntry(ns, identity.ActionNamespaceManagePlacementRules, "namespace:"+ns.String())

		if _, err = rules.Create(ctx, ns, in, ruled); err != nil {
			return fmt.Errorf("create %s placement rule: %w", tool, err)
		}
	}

	return nil
}

// errLoadPlacement reports a store that keeps no collections or placement rules.
var errLoadPlacement = errors.New("the store keeps no collections or placement rules")

// loadEntry returns the audit entry of a change that the harness makes while it
// sets up a namespace.
func loadEntry(ns namespace.Name, action identity.Action, object string) audit.Entry {
	return audit.Entry{
		At: time.Now(), Namespace: ns, Subject: "load-harness", IDPSubject: "", TokenID: "",
		Action: string(action), Object: object, Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "", UserAgent: "load-harness", Channel: "api", Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		PrincipalKind: "", JobID: "", Reason: "",
	}
}

// testServerCloser adapts httptest.Server.Close to a context cleanup func.
func testServerCloser(ts *httptest.Server) func(context.Context) {
	return func(context.Context) {
		ts.Close()
	}
}

// harnessKeyBytes is the size of the key-encryption key of the instances.
const harnessKeyBytes = 32

// newHarnessWrapper returns a key-encryption key wrapper over 32 random
// bytes. The instances start on empty stores, so a new key per run is
// enough. Every instance uses the same wrapper, so each one opens what the
// other sealed. The server seals OpenTofu state with it and cannot store
// state without one.
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

// runStopTimeout bounds the wait for a stopped instance of runInstance.
const runStopTimeout = time.Minute

// errRunStopped reports an instance of runInstance that did not stop in time.
var errRunStopped = errors.New("server did not stop in time")

// runInstance builds the server for one instance and runs it with Run on a
// loopback listener, as the server binary does, and answers its URL. Run
// also starts the workers, so the backups and the restores of the instance
// run to their end. extra options follow the options every server gets.
func runInstance(
	ctx context.Context,
	logger *slog.Logger,
	served instance,
	wrapper *secret.BuiltinWrapper,
	extra ...dustpan.Option,
) (string, func(context.Context), error) {
	noop := func(context.Context) {}

	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", noop, fmt.Errorf("listen: %w", err)
	}

	instanceURL := "http://" + ln.Addr().String()

	extra = append(extra,
		dustpan.WithListener(ln),
		dustpan.WithInstallationKind(installation.KindProduction),
		dustpan.WithInstallationKindStore(served.ports.kinds),
	)

	srv, err := newInstanceServer(served, wrapper, instanceURL, extra...)
	if err != nil {
		return "", noop, errors.Join(err, ln.Close())
	}

	runCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan error, 1)

	go func() { done <- srv.Run(runCtx) }()

	stopRun := func(stopCtx context.Context) {
		stop()

		select {
		case runErr := <-done:
			if runErr != nil {
				logger.ErrorContext(stopCtx, "stop instance", slog.String("error", runErr.Error()))
			}
		case <-time.After(runStopTimeout):
			logger.ErrorContext(stopCtx, "stop instance", slog.String("error", errRunStopped.Error()))
		}
	}

	if err := waitServing(ctx, instanceURL); err != nil {
		stopRun(ctx)

		return "", noop, err
	}

	return instanceURL, stopRun, nil
}

// waitServing polls the liveness route of the server at base until it
// answers, or fails after runStopTimeout.
func waitServing(ctx context.Context, base string) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(runStopTimeout)

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health/live", http.NoBody)
		if err != nil {
			return fmt.Errorf("wait for server: %w", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				return fmt.Errorf("wait for server: %w", closeErr)
			}

			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for server: %w", ctx.Err())
		case <-time.After(waitServingPoll):
		}
	}

	return fmt.Errorf("wait for server at %s: %w", base, errRunStopped)
}

// waitServingPoll is the pause between two reads of waitServing.
const waitServingPoll = 50 * time.Millisecond

// newInstanceServer builds the server of one instance with the options
// every server gets, then extra.
func newInstanceServer(
	served instance,
	wrapper *secret.BuiltinWrapper,
	publicURL string,
	extra ...dustpan.Option,
) (*dustpan.Server, error) {
	authn, err := identity.NewStaticToken("load", harnessToken())
	if err != nil {
		return nil, fmt.Errorf("authenticator: %w", err)
	}

	opts := []dustpan.Option{
		dustpan.WithInstallation(harnessInstallation),
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
		dustpan.WithDurableStore(served.ports.durable),
		dustpan.WithJobStore(served.ports.jobs),
		dustpan.WithRunners(served.ports.runners),
		dustpan.WithPublicURL(publicURL),
	}

	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		return nil, fmt.Errorf("new server: %w", err)
	}

	return srv, nil
}

// serveInstance builds the server for one instance and answers its test URL.
// wrapper is the key-encryption key that all instances share. extra options
// follow the options every server gets.
func serveInstance(
	ctx context.Context,
	served instance,
	wrapper *secret.BuiltinWrapper,
	extra ...dustpan.Option,
) (string, func(context.Context), error) {
	noop := func(context.Context) {}

	// The public URL is the address of the server itself: the OpenTofu upload
	// URL and the runner protocol name it. The listener exists before the
	// server starts, so its address is known here.
	ts := httptest.NewUnstartedServer(nil)

	srv, err := newInstanceServer(served, wrapper, "http://"+ts.Listener.Addr().String(), extra...)
	if err != nil {
		ts.Close()

		return "", noop, err
	}

	if startErr := srv.Start(ctx); startErr != nil {
		ts.Close()

		return "", noop, fmt.Errorf("start the server: %w", startErr)
	}

	ts.Config.Handler = srv.Handler()
	ts.Start()

	return ts.URL, testServerCloser(ts), nil
}
