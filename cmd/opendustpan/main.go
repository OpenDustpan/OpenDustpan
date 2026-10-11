// Command opendustpan runs the OpenDustpan server with the built-in adapters.
//
//	opendustpan --version
//	opendustpan serve [flags]
//	opendustpan health [flags]
//	opendustpan namespace create [flags] <name>
//	opendustpan upgrade check [flags]
//	opendustpan backup check [flags]
//	opendustpan backup [flags]
//	opendustpan restore [flags]
//
// Every flag can also be set through the environment variable shown in its
// help text. The bootstrap admin token is read from DUSTPAN_ADMIN_TOKEN or
// from the file in DUSTPAN_ADMIN_TOKEN_FILE only, never from a flag, so it does
// not appear in process listings; serve clears DUSTPAN_ADMIN_TOKEN from its
// environment after the read. The
// key-encryption key comes from DUSTPAN_KEY_ENCRYPTION_KEY or from the file in
// DUSTPAN_KEY_ENCRYPTION_KEY_FILE only, never from a flag, and the server
// never generates it.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	blobs3 "opendustpan.org/opendustpan/pkg/blob/s3"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	errUsage = errors.New(
		"usage: opendustpan --version | opendustpan serve | opendustpan health | opendustpan namespace create <name> | " +
			"opendustpan upgrade check | opendustpan backup check | opendustpan backup | opendustpan restore",
	)
	errUnknownBackend     = errors.New("database must start with sqlite: or postgres://")
	errKeyFilePermissions = errors.New("key file allows group or other access")
	// errTLSCertMissing reports --tls-key without --tls-cert.
	errTLSCertMissing = errors.New(
		"TLS certificate is missing: set --tls-cert (DUSTPAN_TLS_CERT) or clear --tls-key",
	)
	// errTLSKeyMissing reports --tls-cert without --tls-key.
	errTLSKeyMissing = errors.New(
		"TLS key is missing: set --tls-key (DUSTPAN_TLS_KEY) or clear --tls-cert",
	)
	// errLogLevel reports an unknown --log-level value.
	errLogLevel = errors.New(
		"log level must be debug, info, warn or error: set --log-level (DUSTPAN_LOG_LEVEL)",
	)
	// errLogFormat reports an unknown --log-format value.
	errLogFormat = errors.New(
		"log format must be json or text: set --log-format (DUSTPAN_LOG_FORMAT)",
	)
	// errSizeLimit reports a negative size limit.
	errSizeLimit = errors.New(
		"size limits must not be negative: set --max-checkpoint-bytes, --max-secret-batch-bytes, --max-state-bytes or --max-body-bytes-in-flight to 0 or more",
	)
	// errBackupTarget reports a backup target with an unknown form. It shows
	// the allowed forms, so the operator knows what to set. Only the serve
	// flag --backup-target takes it.
	errBackupTarget = errors.New(
		"backup target must be dir:/path or s3://<bucket>[/<prefix>]: set --backup-target (DUSTPAN_BACKUP_TARGET)",
	)
	// errBlobStore reports a blob store with an unknown form. It shows the
	// allowed forms, so the operator knows what to set.
	errBlobStore = errors.New(
		"blob store must be fs or s3://<bucket>[/<prefix>]: set --blob-store (DUSTPAN_BLOB_STORE)",
	)
	// errS3Endpoint reports an S3 endpoint that is not an http(s) URL. It
	// names the flag, so the operator knows what to set.
	errS3Endpoint = errors.New(
		"s3 endpoint must be an http(s) URL with a host: set --s3-endpoint (DUSTPAN_S3_ENDPOINT)",
	)
	// errBackupID reports a backup check without a backup ID. It names the
	// flag, so the operator knows what to set.
	errBackupID = errors.New("backup id is missing: set --backup <id>")
	// errRestoreSource reports a restore source with an unknown form. It
	// shows the allowed forms, so the operator knows what to set. Only the
	// restore flag --from takes it.
	errRestoreSource = errors.New(
		"backup source must be dir:/path: set --from (DUSTPAN_BACKUP_TARGET)",
	)
	// errRestoreSourceMissing reports a restore source that does not exist.
	// It names the flag, so the operator knows what to set.
	errRestoreSourceMissing = errors.New(
		"backup source does not exist: set --from to an existing directory (DUSTPAN_BACKUP_TARGET)",
	)
	// errRestoreSourceNotDir reports a restore source that is not a
	// directory. It names the flag, so the operator knows what to set.
	errRestoreSourceNotDir = errors.New(
		"backup source is not a directory: set --from to an existing directory (DUSTPAN_BACKUP_TARGET)",
	)
	// errRestoreFrom reports a restore without a backup source. It names the
	// flag, so the operator knows what to set.
	errRestoreFrom = errors.New(
		"backup source is missing: set --from to dir:/path (DUSTPAN_BACKUP_TARGET)",
	)
	// errRestoreBackup reports a restore without a backup ID. It names the
	// flag, so the operator knows what to set.
	errRestoreBackup = errors.New("backup id is missing: set --backup <id>")
)

// version is the server version. The build stamps it with
// -ldflags "-X main.version=...". A build with no stamp prints dev.
var version = "dev"

const (
	// keyEnvVar holds the base64 key-encryption key. It wins when both key
	// settings are present.
	keyEnvVar = "DUSTPAN_KEY_ENCRYPTION_KEY"
	// keyFileEnvVar names a file holding the base64 key-encryption key.
	keyFileEnvVar = "DUSTPAN_KEY_ENCRYPTION_KEY_FILE"
	// keyFilePrivateBits are the permission bits the key file must not set:
	// group or other access of any kind.
	keyFilePrivateBits = 0o077
	// blobDirPerm is the mode the server creates the blob directory with:
	// owner only, so state content does not leak through shared directories.
	blobDirPerm = 0o700
	// defaultS3Region signs S3 requests when --s3-region is empty. Both S3
	// settings stay optional for AWS (ADR 31 decision 6).
	defaultS3Region = "us-east-1"
	// schemeHTTP and schemeHTTPS name the URL schemes of S3 endpoints and
	// of the health probe.
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)

	stop()

	if err != nil {
		fmt.Fprintln(os.Stderr, "opendustpan:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	switch args[0] {
	case "--version":
		_, err := fmt.Fprintln(stdout, version)
		if err != nil {
			return fmt.Errorf("write version: %w", err)
		}

		return nil
	case "serve":
		return serve(ctx, args[1:], stderr)
	case "health":
		return runHealth(ctx, args[1:], stdout, stderr)
	case "namespace":
		return runNamespaceCommand(ctx, args[1:], stdout, stderr)
	case "upgrade":
		return runUpgradeCommand(ctx, args[1:], stdout, stderr)
	case "backup":
		return runBackup(ctx, args[1:], stdout, stderr)
	case "restore":
		return runRestore(ctx, args[1:], stdout, stderr)
	default:
		return errUsage
	}
}

// runNamespaceCommand runs the namespace subcommands: create only.
func runNamespaceCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) >= 1 && args[0] == "create" {
		return createNamespace(ctx, args[1:], stdout, stderr)
	}

	return errUsage
}

// runUpgradeCommand runs the upgrade subcommands: check only.
func runUpgradeCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) >= 1 && args[0] == "check" {
		return upgradeCheck(ctx, args[1:], stdout, stderr)
	}

	return errUsage
}

// common holds the flags shared by every subcommand.
type common struct {
	database string
	region   string
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.database, "database", env("DUSTPAN_DATABASE", "sqlite:dustpan.db"),
		"sqlite:<path> or postgres://... (DUSTPAN_DATABASE)")
	fs.StringVar(&c.region, "region", env("DUSTPAN_REGION", "local"),
		"region this server is authoritative for (DUSTPAN_REGION)")
}

func serve(ctx context.Context, args []string, stderr io.Writer) error {
	var f serveFlags

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)

	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if err := checkTLSFlags(f.tlsCert, f.tlsKey); err != nil {
		return err
	}

	if err := checkLogFlags(f.logLevel, f.logFormat); err != nil {
		return err
	}

	sizeLimits := []int64{
		f.maxCheckpointBytes,
		f.maxSecretBatchBytes,
		f.maxStateBytes,
		f.maxBodyBytesInFlight,
	}

	if err := checkSizeFlags(sizeLimits); err != nil {
		return err
	}

	return f.run(ctx, stderr)
}

// serveFlags holds the settings of the serve subcommand.
type serveFlags struct {
	common

	listen               string
	consoleDir           string
	origins              string
	tlsCert              string
	tlsKey               string
	publicURL            string
	blobDir              string
	blobStore            string
	s3Endpoint           string
	s3Region             string
	backupTarget         string
	logLevel             string
	logFormat            string
	maxCheckpointBytes   int64
	maxSecretBatchBytes  int64
	maxStateBytes        int64
	maxBodyBytesInFlight int64
}

func (f *serveFlags) register(fs *flag.FlagSet) {
	f.common.register(fs)
	f.registerNet(fs)
	f.registerLimits(fs)
	f.registerStorage(fs)
}

// registerStorage registers the blob store, S3 and backup target flags.
// Credentials never appear here: they come from AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY and AWS_SESSION_TOKEN only.
func (f *serveFlags) registerStorage(fs *flag.FlagSet) {
	fs.StringVar(
		&f.blobStore,
		"blob-store",
		env("DUSTPAN_BLOB_STORE", "fs"),
		"blob store fs with --blob-dir, or s3://<bucket>[/<prefix>] (DUSTPAN_BLOB_STORE)",
	)
	fs.StringVar(
		&f.s3Endpoint,
		"s3-endpoint",
		env("DUSTPAN_S3_ENDPOINT", ""),
		"S3 endpoint URL, empty means AWS (DUSTPAN_S3_ENDPOINT)",
	)
	fs.StringVar(
		&f.s3Region,
		"s3-region",
		env("DUSTPAN_S3_REGION", ""),
		"S3 region, empty means us-east-1 (DUSTPAN_S3_REGION)",
	)
	fs.StringVar(
		&f.backupTarget,
		"backup-target",
		env("DUSTPAN_BACKUP_TARGET", ""),
		"backup target dir:/path or s3://<bucket>[/<prefix>], empty means no backups (DUSTPAN_BACKUP_TARGET)",
	)
}

// registerNet registers the network and console flags.
func (f *serveFlags) registerNet(fs *flag.FlagSet) {
	fs.StringVar(&f.listen, "listen", env("DUSTPAN_LISTEN", ":8080"), "listen address (DUSTPAN_LISTEN)")
	fs.StringVar(
		&f.consoleDir,
		"console-dir",
		env("DUSTPAN_CONSOLE_DIR", ""),
		"serve a built web console from this directory (DUSTPAN_CONSOLE_DIR)",
	)
	fs.StringVar(
		&f.origins,
		"allowed-origins",
		env("DUSTPAN_ALLOWED_ORIGINS", ""),
		"comma-separated console origins allowed to call the API (DUSTPAN_ALLOWED_ORIGINS)",
	)
	fs.StringVar(
		&f.tlsCert,
		"tls-cert",
		env("DUSTPAN_TLS_CERT", ""),
		"certificate file for HTTPS, with --tls-key (DUSTPAN_TLS_CERT)",
	)
	fs.StringVar(
		&f.tlsKey,
		"tls-key",
		env("DUSTPAN_TLS_KEY", ""),
		"key file for HTTPS, with --tls-cert (DUSTPAN_TLS_KEY)",
	)
	fs.StringVar(
		&f.publicURL,
		"public-url",
		env("DUSTPAN_PUBLIC_URL", ""),
		"external base URL, for example https://dustpan.example (DUSTPAN_PUBLIC_URL)",
	)
	fs.StringVar(
		&f.blobDir,
		"blob-dir",
		env("DUSTPAN_BLOB_DIR", ""),
		"directory for state content (DUSTPAN_BLOB_DIR, default blobs next to the database)",
	)
	fs.StringVar(
		&f.logLevel,
		"log-level",
		env("DUSTPAN_LOG_LEVEL", "info"),
		"log level: debug, info, warn or error (DUSTPAN_LOG_LEVEL)",
	)
	fs.StringVar(
		&f.logFormat,
		"log-format",
		env("DUSTPAN_LOG_FORMAT", "json"),
		"log format: json or text (DUSTPAN_LOG_FORMAT)",
	)
}

// registerLimits registers the large-body size flags.
func (f *serveFlags) registerLimits(fs *flag.FlagSet) {
	fs.Int64Var(
		&f.maxCheckpointBytes,
		"max-checkpoint-bytes",
		envBytes("DUSTPAN_MAX_CHECKPOINT_BYTES"),
		"largest checkpoint in bytes, 0 means the default (DUSTPAN_MAX_CHECKPOINT_BYTES)",
	)
	fs.Int64Var(
		&f.maxSecretBatchBytes,
		"max-secret-batch-bytes",
		envBytes("DUSTPAN_MAX_SECRET_BATCH_BYTES"),
		"largest secrets batch in bytes, 0 means the default (DUSTPAN_MAX_SECRET_BATCH_BYTES)",
	)
	fs.Int64Var(
		&f.maxStateBytes,
		"max-state-bytes",
		envBytes("DUSTPAN_MAX_STATE_BYTES"),
		"largest OpenTofu state in bytes, 0 means the default (DUSTPAN_MAX_STATE_BYTES)",
	)
	fs.Int64Var(
		&f.maxBodyBytesInFlight,
		"max-body-bytes-in-flight",
		envBytes("DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT"),
		"concurrent large bodies in bytes, 0 means the default (DUSTPAN_MAX_BODY_BYTES_IN_FLIGHT)",
	)
}

// run opens the store, runs the start-up checks and serves until ctx is done.
func (f *serveFlags) run(ctx context.Context, stderr io.Writer) error {
	logger, err := f.loggerFor(stderr)
	if err != nil {
		return err
	}

	authn, err := adminAuthenticator()
	if err != nil {
		return err
	}

	wrapper, err := keyWrapper()
	if err != nil {
		return err
	}

	st, err := openStore(ctx, f.database)
	if err != nil {
		return err
	}
	defer st.close()

	return f.serveWithStores(ctx, st, authn, wrapper, logger)
}

// serveWithStores opens the blob stores, wires the server and serves until
// ctx is done.
func (f *serveFlags) serveWithStores(
	ctx context.Context,
	st store,
	authn *identity.StaticTokens,
	wrapper secret.Wrapper,
	logger *slog.Logger,
) error {
	blobs, backupTarget, err := f.openBlobStores(ctx, logger)
	if err != nil {
		return err
	}

	defer closeLiveStore(ctx, logger, blobs)

	if backupTarget != nil {
		defer closeTargetStore(ctx, logger, backupTarget)
	}

	opts := f.serverOptions(st, blobs, authn, wrapper, logger)
	opts = append(opts, f.backupOptions(backupTarget)...)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		return fmt.Errorf("configure server: %w", err)
	}

	// Run runs the start-up checks, including the key-encryption key check
	// (ADR 23), before it serves.
	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}

// openBlobStores opens the live blob store and the backup target. A backup
// target failure closes the live store, so no directory handle leaks. The two
// stores stay unnamed, as nonamedreturns requires; the first is the live
// store, the second is the backup target.
//
//nolint:revive,ireturn // Unnamed two-store result per nonamedreturns; adapters are chosen at runtime.
func (f *serveFlags) openBlobStores(
	ctx context.Context,
	logger *slog.Logger,
) (blob.Store, blob.Store, error) {
	blobs, err := f.openLiveBlobs(ctx, logger)
	if err != nil {
		return nil, nil, err
	}

	target, err := openBackupTarget(ctx, f.backupTarget, f.s3Endpoint, f.s3Region, logger)
	if err != nil {
		closeLiveStore(ctx, logger, blobs)

		return nil, nil, err
	}

	return blobs, target, nil
}

// openLiveBlobs opens the live blob store from the serve storage flags.
//
//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func (f *serveFlags) openLiveBlobs(ctx context.Context, logger *slog.Logger) (blob.Store, error) {
	cfg := blobOpenConfig{
		database: f.database,
		dir:      f.blobDir,
		store:    f.blobStore,
		endpoint: f.s3Endpoint,
		region:   f.s3Region,
	}

	return openBlobs(ctx, cfg, logger)
}

// closeLiveStore closes the live blob store when it holds a directory
// handle, so dir: stores release their handle while S3 stores need no close.
func closeLiveStore(ctx context.Context, logger *slog.Logger, s blob.Store) {
	closer, ok := s.(io.Closer)
	if !ok {
		return
	}

	if err := closer.Close(); err != nil {
		logger.WarnContext(ctx, "close blob store", slog.String("error", err.Error()))
	}
}

// closeTargetStore closes the backup target when it holds a directory
// handle, so dir: targets release their handle while S3 targets need no close.
func closeTargetStore(ctx context.Context, logger *slog.Logger, s blob.Store) {
	closer, ok := s.(io.Closer)
	if !ok {
		return
	}

	if err := closer.Close(); err != nil {
		logger.WarnContext(ctx, "close backup target", slog.String("error", err.Error()))
	}
}

// serverOptions returns the NewServer options for the opened store, blob
// store, authenticator, key wrapper and logger.
func (f *serveFlags) serverOptions(
	st store,
	blobs blob.Store,
	authn *identity.StaticTokens,
	wrapper secret.Wrapper,
	logger *slog.Logger,
) []dustpan.Option {
	return []dustpan.Option{
		dustpan.WithRegion(namespace.Region(f.region)),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithLogger(logger),
		dustpan.WithListenAddr(f.listen),
		dustpan.WithConsoleDir(f.consoleDir),
		dustpan.WithAllowedOrigins(splitList(f.origins)...),
		dustpan.WithTLS(f.tlsCert, f.tlsKey),
		dustpan.WithPublicURL(f.publicURL),
		dustpan.WithVersion(version),
		dustpan.WithMaxCheckpointBytes(int(f.maxCheckpointBytes)),
		dustpan.WithMaxSecretBatchBytes(int(f.maxSecretBatchBytes)),
		dustpan.WithMaxStateBytes(f.maxStateBytes),
		dustpan.WithMaxBodyBytesInFlight(f.maxBodyBytesInFlight),
	}
}

// blobOpenConfig holds the live blob store settings of serve.
type blobOpenConfig struct {
	database string
	dir      string
	store    string
	endpoint string
	region   string
}

// openBlobs opens the live blob store. A store of fs (the default) opens the
// state content directory. An s3://<bucket>[/<prefix>] store opens S3-compatible
// storage with the probe of ADR 31, so serve fails at start when the store
// ignores conditional writes. Anything else names --blob-store.
//
//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func openBlobs(ctx context.Context, cfg blobOpenConfig, logger *slog.Logger) (blob.Store, error) {
	trimmed := strings.TrimSpace(cfg.store)
	if trimmed == "" || trimmed == "fs" {
		return openFSBlobs(cfg.database, cfg.dir)
	}

	if strings.HasPrefix(trimmed, "s3://") {
		return openS3Store(ctx, s3StoreConfig{
			raw:      trimmed,
			endpoint: cfg.endpoint,
			region:   cfg.region,
			formErr:  errBlobStore,
		}, logger)
	}

	return nil, fmt.Errorf("blob store %q: %w", trimmed, errBlobStore)
}

// openFSBlobs creates the state content directory and opens it.
//
//nolint:ireturn // The caller keeps the store as a blob.Store.
func openFSBlobs(database, flagDir string) (blob.Store, error) {
	blobDir := defaultBlobDir(database, flagDir)

	if err := os.MkdirAll(blobDir, blobDirPerm); err != nil {
		return nil, fmt.Errorf("create blob directory: %w", err)
	}

	blobs, err := blobfs.Open(blobDir)
	if err != nil {
		return nil, err
	}

	return blobs, nil
}

// parseS3Target splits an s3://<bucket>[/<prefix>] value into its bucket and
// prefix. The prefix keeps no leading or trailing slash. The two strings stay
// unnamed, as nonamedreturns requires; the first is the bucket, the second is
// the prefix. A bad value wraps formErr, so the live blob store names
// --blob-store and the backup target names --backup-target.
//
//nolint:revive // Unnamed two-string result per nonamedreturns; order is documented.
func parseS3Target(raw string, formErr error) (string, string, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(raw), "s3://")
	if !ok {
		return "", "", fmt.Errorf("parse %q: %w", raw, formErr)
	}

	bucket, prefix, _ := strings.Cut(strings.TrimSpace(rest), "/")

	bucket = strings.TrimSpace(bucket)
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")

	if bucket == "" || strings.Contains(bucket, " ") {
		return "", "", fmt.Errorf("parse %q: %w", raw, formErr)
	}

	if prefix != "" {
		if _, err := blob.ParseKey(prefix); err != nil {
			return "", "", fmt.Errorf("parse %q: %w", raw, formErr)
		}
	}

	return bucket, prefix, nil
}

// resolveS3Region returns region, or the default for AWS when empty.
func resolveS3Region(region string) string {
	if trimmed := strings.TrimSpace(region); trimmed != "" {
		return trimmed
	}

	return defaultS3Region
}

// checkS3Endpoint rejects an endpoint that is not an http(s) URL with a host.
func checkS3Endpoint(endpoint string) error {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return nil
	}

	parsed, err := parseEndpoint(trimmed)
	if err != nil {
		return err
	}

	if parsed != schemeHTTP && parsed != schemeHTTPS {
		return errS3Endpoint
	}

	return nil
}

// parseEndpoint returns the scheme of endpoint when it has a host.
func parseEndpoint(endpoint string) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(endpoint))

	var scheme string

	switch {
	case strings.HasPrefix(lower, "http://"):
		scheme = schemeHTTP
	case strings.HasPrefix(lower, "https://"):
		scheme = schemeHTTPS
	default:
		return "", errS3Endpoint
	}

	host := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(lower, "http://"), "https://"))
	host, _, _ = strings.Cut(host, "/")

	if strings.TrimSpace(host) == "" {
		return "", errS3Endpoint
	}

	return scheme, nil
}

// warnPlainHTTP logs a warning for a plain http:// endpoint, so the operator
// knows state content travels without TLS.
func warnPlainHTTP(ctx context.Context, logger *slog.Logger, endpoint string) {
	if logger == nil {
		return
	}

	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return
	}

	if strings.HasPrefix(strings.ToLower(trimmed), "http://") {
		logger.WarnContext(ctx, "s3 endpoint uses plain HTTP", slog.String("endpoint", trimmed))
	}
}

// s3StoreConfig holds the s3:// value and S3 settings of openS3Store.
// formErr wraps a bad s3:// value, so the live blob store names --blob-store
// and the backup target names --backup-target.
type s3StoreConfig struct {
	raw      string
	endpoint string
	region   string
	formErr  error
}

// openS3Store builds a blob.Store from the s3:// form for the live blob store
// and for the backup target. Credentials come from AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY and AWS_SESSION_TOKEN only: a missing variable names
// the variable. The open probes conditional writes, so serve fails at start
// with the probe error. A bad s3:// value wraps the config form error, so the
// caller names the flag it parsed. No error holds a credential.
//
//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func openS3Store(ctx context.Context, cfg s3StoreConfig, logger *slog.Logger) (blob.Store, error) {
	bucket, prefix, parseErr := parseS3Target(cfg.raw, cfg.formErr)
	if parseErr != nil {
		return nil, parseErr
	}

	if endpointErr := checkS3Endpoint(cfg.endpoint); endpointErr != nil {
		return nil, endpointErr
	}

	creds, credErr := blobs3.LoadCredentials(os.LookupEnv)
	if credErr != nil {
		return nil, credErr
	}

	warnPlainHTTP(ctx, logger, cfg.endpoint)

	signer := &blobs3.Signer{
		Bucket:      bucket,
		Region:      resolveS3Region(cfg.region),
		Endpoint:    strings.TrimSpace(cfg.endpoint),
		Credentials: creds,
		Now:         nil,
	}

	store, err := blobs3.Open(ctx, signer, prefix, nil)
	if err != nil {
		return nil, err
	}

	return store, nil
}

// openBackupTarget opens the backup target in raw as a blob store. Empty
// means no backup target is configured: it returns nil without an error. A
// dir:/path target opens the local directory, creating it owner-only, as a
// backup holds OpenTofu state in plain text. An s3://<bucket>[/<prefix>]
// target opens S3-compatible storage with the same constructor as the live
// blob store. Anything else is an error that shows the allowed forms.
//
//nolint:nilnil,ireturn // No target without an error means backups stay off; the adapter is chosen at runtime.
func openBackupTarget(
	ctx context.Context,
	raw, endpoint, region string,
	logger *slog.Logger,
) (blob.Store, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	if strings.HasPrefix(trimmed, "s3://") {
		return openS3Store(ctx, s3StoreConfig{
			raw:      trimmed,
			endpoint: endpoint,
			region:   region,
			formErr:  errBackupTarget,
		}, logger)
	}

	dir, ok := strings.CutPrefix(trimmed, "dir:")
	if !ok || strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("backup target %q: %w", trimmed, errBackupTarget)
	}

	dir = strings.TrimSpace(dir)

	if err := os.MkdirAll(dir, blobDirPerm); err != nil {
		return nil, fmt.Errorf("create backup target directory: %w", err)
	}

	target, err := blobfs.Open(dir)
	if err != nil {
		return nil, err
	}

	return target, nil
}

// openRestoreSource opens the restore source in raw as a blob store. It
// never creates the directory: a restore source must already hold a backup,
// so a missing source is a refusal naming --from. An s3:// source stays
// reserved for a later change (issue #158). Anything else is an error that
// shows the allowed forms.
func openRestoreSource(raw string) (*blobfs.Store, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errRestoreFrom
	}

	if strings.HasPrefix(trimmed, "s3://") {
		return nil, fmt.Errorf("backup source %q: s3 sources are not supported yet: %w", trimmed, errRestoreSource)
	}

	dir, ok := strings.CutPrefix(trimmed, "dir:")
	if !ok || strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("backup source %q: %w", trimmed, errRestoreSource)
	}

	dir = strings.TrimSpace(dir)

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("backup source %q: %w", trimmed, errRestoreSourceMissing)
		}

		return nil, fmt.Errorf("backup source %q: %w", trimmed, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("backup source %q: %w", trimmed, errRestoreSourceNotDir)
	}

	target, err := blobfs.Open(dir)
	if err != nil {
		return nil, err
	}

	return target, nil
}

// backupOptions returns the NewServer backup option for the opened target, or
// none when no backup target is configured. WithBackupTarget is the only way
// the target gets in: no request field, header or query value sets it.
func (f *serveFlags) backupOptions(target blob.Store) []dustpan.Option {
	if target == nil {
		return nil
	}

	return []dustpan.Option{dustpan.WithBackupTarget(target, strings.TrimSpace(f.backupTarget))}
}

// checkTLSFlags requires both TLS files or none. It names the missing one,
// so the operator knows which flag to set or clear.
func checkTLSFlags(certFile, keyFile string) error {
	switch {
	case certFile == "" && keyFile == "":
		return nil
	case certFile == "":
		return errTLSCertMissing
	case keyFile == "":
		return errTLSKeyMissing
	default:
		return nil
	}
}

// checkLogFlags rejects an unknown log level or format. It names the flag,
// so the operator knows which flag to fix.
func checkLogFlags(level, format string) error {
	switch normalizeLogLevel(level) {
	case "debug", "info", "warn", "error":
	default:
		return errLogLevel
	}

	switch normalizeLogLevel(format) {
	case "json", "text":
	default:
		return errLogFormat
	}

	return nil
}

// normalizeLogLevel trims and lowers a log setting, so flags and the
// environment match the same values.
func normalizeLogLevel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// loggerFor builds the serve logger from the log flags. It keeps the JSON
// default and switches to text only when asked.
func (f *serveFlags) loggerFor(stderr io.Writer) (*slog.Logger, error) {
	if err := checkLogFlags(f.logLevel, f.logFormat); err != nil {
		return nil, err
	}

	var level slog.Level

	switch normalizeLogLevel(f.logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	if normalizeLogLevel(f.logFormat) == "text" {
		return slog.New(slog.NewTextHandler(stderr, opts)), nil
	}

	return slog.New(slog.NewJSONHandler(stderr, opts)), nil
}

// runRestore runs `opendustpan restore --from <target> --backup <id>`: it
// loads a backup from the target into an empty installation. It opens the
// destination store in a way that does not start the server, runs
// backup.Restore, and prints the steps with the row counts. It is the one
// exception to ADR 17 in ADR 30: an offline command with no REST endpoint,
// because restore needs an empty installation with no running server. It
// needs an empty destination and no running server. The key comes from
// DUSTPAN_KEY_ENCRYPTION_KEY or from the file in
// DUSTPAN_KEY_ENCRYPTION_KEY_FILE as serve reads it. The newer-schema guard
// still applies to the destination. It exits 0 on success and 1 on any
// refusal, with the one command that helps.
func runRestore(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	plan, err := parseRestoreFlags(args, stderr)
	if err != nil {
		return err
	}

	wrapper, err := keyWrapper()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))

	return executeRestore(ctx, plan, wrapper, logger, stdout)
}

// restorePlan holds the validated settings of one restore run: the flags
// with the trimmed backup id and source.
type restorePlan struct {
	flags restoreFlags
	id    string
	from  string
}

// parseRestoreFlags parses the restore flags and validates the backup id
// and the source. It names the missing flag, so the operator knows what to
// set.
func parseRestoreFlags(args []string, stderr io.Writer) (restorePlan, error) {
	var f restoreFlags

	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return restorePlan{}, fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return restorePlan{}, errUsage
	}

	id := strings.TrimSpace(f.backup)
	if id == "" {
		return restorePlan{}, errRestoreBackup
	}

	from := strings.TrimSpace(f.from)
	if from == "" {
		return restorePlan{}, errRestoreFrom
	}

	return restorePlan{flags: f, id: id, from: from}, nil
}

// executeRestore opens the destination store first, so the newer-schema
// guard reports before the source check, then the blob directory and the
// read-only source, runs backup.Restore and prints the report. It opens
// nothing when the destination refuses: a refused restore writes nothing.
func executeRestore(
	ctx context.Context,
	plan restorePlan,
	wrapper secret.Wrapper,
	logger *slog.Logger,
	stdout io.Writer,
) error {
	st, err := openStore(ctx, plan.flags.database)
	if err != nil {
		return fmt.Errorf("restore backup %s: %w", plan.id, err)
	}

	defer st.close()

	dest, err := restoreStoreOf(st)
	if err != nil {
		return fmt.Errorf("restore backup %s: %w", plan.id, err)
	}

	// The offline restore writes to a local blob directory only; ADR 39
	// (issue #281) moves restore into the server, which owns every store.
	blobs, err := openFSBlobs(plan.flags.database, plan.flags.blobDir)
	if err != nil {
		return fmt.Errorf("restore backup %s: %w", plan.id, err)
	}

	defer closeLiveStore(ctx, logger, blobs)

	target, err := openRestoreSource(plan.from)
	if err != nil {
		return fmt.Errorf("restore backup %s from %q: %w", plan.id, plan.from, err)
	}

	defer closeRestoreTarget(ctx, logger, target)

	warning, err := backup.Restore(ctx, target, backup.ID(plan.id), backup.RestoreDest{
		Blobs:  blobs,
		Store:  dest,
		Region: strings.TrimSpace(plan.flags.region),
	}, wrapper)
	if err != nil {
		return restoreRefusal(plan.id, err)
	}

	if writeErr := printRestoreHeader(stdout, plan.id, plan.from); writeErr != nil {
		return writeErr
	}

	if reportErr := writeRestoreReport(ctx, stdout, dest, plan.id); reportErr != nil {
		return reportErr
	}

	if reportErr := writeRestoreWarning(stdout, warning); reportErr != nil {
		return reportErr
	}

	if _, writeErr := fmt.Fprintf(stdout, "restored backup %s\n", plan.id); writeErr != nil {
		return fmt.Errorf("write output: %w", writeErr)
	}

	return nil
}

// closeRestoreTarget closes the backup source and logs the error with
// context instead of discarding it, as serve does for its stores.
func closeRestoreTarget(ctx context.Context, logger *slog.Logger, target *blobfs.Store) {
	if err := target.Close(); err != nil {
		logger.WarnContext(ctx, "close backup source", slog.String("error", err.Error()))
	}
}

// printRestoreHeader prints the restore start line. executeRestore calls it
// only after backup.Restore succeeds, so a refused restore never emits a
// line that reads like the start of a restore.
func printRestoreHeader(stdout io.Writer, id, from string) error {
	if _, err := fmt.Fprintf(stdout, "restore backup %s from %s\n", id, from); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// writeRestoreWarning prints the region warning of the restore, or nothing
// when the regions match.
func writeRestoreWarning(stdout io.Writer, warning string) error {
	if warning == "" {
		return nil
	}

	if _, err := fmt.Fprintf(stdout, "warning: %s\n", warning); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// restoreFlags holds the settings of the restore subcommand.
type restoreFlags struct {
	common

	from    string
	backup  string
	blobDir string
}

func (f *restoreFlags) register(fs *flag.FlagSet) {
	f.common.register(fs)
	fs.StringVar(
		&f.from,
		"from",
		env("DUSTPAN_BACKUP_TARGET", ""),
		"backup source dir:/path (DUSTPAN_BACKUP_TARGET)",
	)
	fs.StringVar(&f.backup, "backup", "", "backup ID to restore, for example bkp-aaa")
	fs.StringVar(
		&f.blobDir,
		"blob-dir",
		env("DUSTPAN_BLOB_DIR", ""),
		"directory for state content (DUSTPAN_BLOB_DIR, default blobs next to the database)",
	)
	fs.StringVar(
		&f.blobDir,
		"blob-store",
		env("DUSTPAN_BLOB_DIR", ""),
		"same as --blob-dir (DUSTPAN_BLOB_DIR)",
	)
}

// restoreStoreOf returns the backup restore port of the opened destination
// store. Both store adapters implement it.
//
//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func restoreStoreOf(st store) (backup.RestoreStore, error) {
	switch s := st.(type) {
	case sqliteStore:
		return s.Store, nil
	case postgresStore:
		return s.Store, nil
	default:
		return nil, errUnknownBackend
	}
}

// restoreRefusal wraps a restore failure with the one command that helps. A
// destination that is not empty needs an empty one: a new --database with a
// new --blob-dir. Every other error already names its fix: the key errors
// name DUSTPAN_KEY_ENCRYPTION_KEY, the dialect and schema errors name the
// versions, and the newer-schema guard names the binary.
func restoreRefusal(id string, err error) error {
	if errors.Is(err, backup.ErrNotEmpty) {
		return fmt.Errorf(
			"restore backup %s: %w: restore into an empty destination: use a new --database with a new --blob-dir",
			id,
			err,
		)
	}

	return fmt.Errorf("restore backup %s: %w", id, err)
}

// writeRestoreReport prints the steps the restore ran with the row count of
// each table, in the order of backup.Tables. It reads the counts back from
// the destination, so the report shows what the restore loaded.
func writeRestoreReport(ctx context.Context, stdout io.Writer, dest backup.RestoreStore, id string) error {
	summary, err := dest.Export(ctx, io.Discard)
	if err != nil {
		return fmt.Errorf("restore backup %s: read restored data: %w", id, err)
	}

	lines := []string{
		"read manifest",
		"checked key",
		"copied blobs",
		"loaded SQL data",
	}

	for _, table := range backup.Tables {
		lines = append(lines, fmt.Sprintf("table %s: %d rows", table, summary.RowCounts[table]))
	}

	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}

	return nil
}

// runBackup runs the backup subcommands. It shares one backup entry, so the
// backup command of a later change and backup check never clash: check runs
// when the first argument is check, anything else is usage for now.
func runBackup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) >= 1 && args[0] == "check" {
		return backupCheck(ctx, args[1:], stdout, stderr)
	}

	return backupCommand(ctx, args, stdout, stderr)
}

// store is the persistence adapter chosen by --database.
//
//nolint:interfacebloat // The binary needs every persistence port in one adapter.
type store interface {
	namespace.Store
	stack.Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
	secret.KeyStore
	workspace.Store
	workspace.StateStore
	inventory.Reader
	close()
}

type sqliteStore struct{ *sqlite.Store }

func (s sqliteStore) close() {
	if err := s.Close(); err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Warn("close database", slog.String("error", err.Error()))
	}
}

type postgresStore struct{ *postgres.Store }

func (s postgresStore) close() { s.Close() }

//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func openStore(ctx context.Context, database string) (store, error) {
	switch {
	case strings.HasPrefix(database, "sqlite:"):
		s, err := sqlite.Open(ctx, strings.TrimPrefix(database, "sqlite:"))
		if err != nil {
			return nil, fmt.Errorf("open sqlite store: %w", withBinaryVersion(err))
		}

		return sqliteStore{s}, nil
	case strings.HasPrefix(database, "postgres://"), strings.HasPrefix(database, "postgresql://"):
		s, err := postgres.Open(ctx, database)
		if err != nil {
			return nil, fmt.Errorf("open postgres store: %w", withBinaryVersion(err))
		}

		return postgresStore{s}, nil
	default:
		return nil, errUnknownBackend
	}
}

// withBinaryVersion adds the version of the binary to a newer-schema refusal,
// so the message names the schema version of the database, the schema version
// of the binary and the version of the binary. The adapters do not know the
// binary version, so only this command adds it.
func withBinaryVersion(err error) error {
	newer, ok := errors.AsType[*schema.NewerError](err)
	if !ok {
		return err
	}

	return fmt.Errorf("binary version is %s. %w", version, newer)
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}

	return fallback
}

// envBytes returns the byte size in key, or 0 when unset. An invalid value
// returns -1, so checkSizeFlags rejects it with the flag names.
func envBytes(key string) int64 {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return 0
	}

	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return -1
	}

	return n
}

// checkSizeFlags rejects a negative large-body limit. Zero means the default,
// so only negative values fail.
func checkSizeFlags(limits []int64) error {
	for _, n := range limits {
		if n < 0 {
			return errSizeLimit
		}
	}

	return nil
}

// defaultBlobDir returns the state content directory: the --blob-dir flag
// when set, else a blobs directory next to the SQLite file, or blobs in the
// working directory for PostgreSQL.
func defaultBlobDir(database, flagDir string) string {
	if strings.TrimSpace(flagDir) != "" {
		return strings.TrimSpace(flagDir)
	}

	if rest, ok := strings.CutPrefix(database, "sqlite:"); ok {
		path := strings.TrimSpace(rest)
		if path == "" {
			return "blobs"
		}

		return filepath.Join(filepath.Dir(path), "blobs")
	}

	return "blobs"
}

func splitList(s string) []string {
	var out []string

	for item := range strings.SplitSeq(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}

	return out
}

// loadKeyEncryptionKey reads the key-encryption key from DUSTPAN_KEY_ENCRYPTION_KEY
// (base64) or from the file in DUSTPAN_KEY_ENCRYPTION_KEY_FILE. The direct
// value wins when both settings are present. It returns nil without an error
// when neither setting is present: the server may start without a key while
// the database holds no wrapped key. It clears DUSTPAN_KEY_ENCRYPTION_KEY
// from the process environment after the read, so the key does not stay in
// memory that child processes inherit.
func loadKeyEncryptionKey() ([]byte, error) {
	if raw, ok := os.LookupEnv(keyEnvVar); ok && strings.TrimSpace(raw) != "" {
		if err := os.Unsetenv(keyEnvVar); err != nil {
			return nil, fmt.Errorf("%s: clear variable: %w", keyEnvVar, err)
		}

		return decodeKey(keyEnvVar, raw)
	}

	return loadKeyEncryptionKeyFrom(os.LookupEnv)
}

// loadKeyEncryptionKeyFrom reads the key-encryption key with lookup instead
// of the process environment, so tests stay hermetic.
func loadKeyEncryptionKeyFrom(lookup func(key string) (string, bool)) ([]byte, error) {
	if raw, ok := lookup(keyEnvVar); ok && strings.TrimSpace(raw) != "" {
		return decodeKey(keyEnvVar, raw)
	}

	if path, ok := lookup(keyFileEnvVar); ok && strings.TrimSpace(path) != "" {
		return readKeyFile(strings.TrimSpace(path))
	}

	return nil, nil
}

// keyWrapper loads the key-encryption key and returns its wrapper, or nil
// when no key is configured.
//
//nolint:ireturn // No key is a valid state, so the result must stay an interface to hold nil.
func keyWrapper() (secret.Wrapper, error) {
	key, err := loadKeyEncryptionKey()
	if err != nil {
		return nil, err
	}

	if key == nil {
		//nolint:nilnil // A nil wrapper without an error means no key is configured.
		return nil, nil
	}

	wrapper, err := secret.NewBuiltinWrapper(key)
	if err != nil {
		// Unreachable: loadKeyEncryptionKey already checked the size.
		return nil, fmt.Errorf("key-encryption key: %w", err)
	}

	return wrapper, nil
}

// decodeKey decodes a base64 key-encryption key from setting source and
// checks its size.
func decodeKey(source, raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: not valid base64: %w", source, err)
	}

	if _, err := secret.NewBuiltinWrapper(key); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}

	return key, nil
}

// readKeyFile reads a base64 key-encryption key from the file at path. The
// file must not be readable by group or others, so the key does not leak
// through shared directories or backups.
func readKeyFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: stat file: %w", keyFileEnvVar, err)
	}

	if perm := info.Mode().Perm(); perm&keyFilePrivateBits != 0 {
		return nil, fmt.Errorf(
			"%s: %w %q (mode %04o): run chmod 600 on the file",
			keyFileEnvVar,
			errKeyFilePermissions,
			path,
			perm,
		)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The operator names the key file through the environment.
	if err != nil {
		return nil, fmt.Errorf("%s: read file: %w", keyFileEnvVar, err)
	}

	return decodeKey(keyFileEnvVar, string(raw))
}
