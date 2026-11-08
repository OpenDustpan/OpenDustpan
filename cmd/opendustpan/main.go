// Command opendustpan runs the OpenDustpan server with the built-in adapters.
//
//	opendustpan --version
//	opendustpan serve [flags]
//	opendustpan dev [flags]
//	opendustpan dev reset [--data-dir <dir>]
//	opendustpan key generate --out <path>
//	opendustpan doctor [flags]
//	opendustpan health [flags]
//	opendustpan namespace create [flags] <name>
//	opendustpan upgrade check [flags]
//	opendustpan backup check [flags]
//	opendustpan backup [flags]
//	opendustpan restore [flags]
//	opendustpan store check [flags]
//	opendustpan audit-chain status [flags]
//	opendustpan audit-chain approve [flags]
//
// Every flag can also be set through the environment variable shown in its
// help text. The bootstrap admin token is read from DUSTPAN_ADMIN_TOKEN or
// from the file in DUSTPAN_ADMIN_TOKEN_FILE only, never from a flag, so it does
// not appear in process listings; serve clears DUSTPAN_ADMIN_TOKEN from its
// environment after the read. The
// key-encryption key comes from DUSTPAN_KEY_ENCRYPTION_KEY or from the file in
// DUSTPAN_KEY_ENCRYPTION_KEY_FILE only, never from a flag. Serve needs a key
// and a database that the operator sets: it has no default for either, it
// makes no key, and it stops before it opens the database when one is
// missing.
package main

import (
	"context"
	"crypto/sha256"
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
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/instance"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	blobs3 "opendustpan.org/opendustpan/pkg/blob/s3"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
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
		"size limits must not be negative: set --max-checkpoint-bytes, --max-secret-batch-bytes, --max-state-bytes, --max-body-bytes-in-flight or --blob-cache-bytes to 0 or more",
	)
	// errRetention reports a soft-delete retention that is not a duration of
	// at least one hour.
	errRetention = errors.New(
		"soft-delete retention must be a duration of at least 1h, for example 720h: " +
			"set --soft-delete-retention (DUSTPAN_SOFT_DELETE_RETENTION)",
	)
	// errDeltaCutoff reports a negative checkpoint delta cutoff. Zero turns
	// the delta capability off, so only negative values fail.
	errDeltaCutoff = errors.New(
		"checkpoint delta cutoff must not be negative: set --checkpoint-delta-cutoff (DUSTPAN_CHECKPOINT_DELTA_CUTOFF) to 0 or more",
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
	// errNoKey reports serve with no key-encryption key. Serve returns it
	// before it opens the database.
	errNoKey = errors.New(
		"no key-encryption key is set: set --key-encryption-key-file (DUSTPAN_KEY_ENCRYPTION_KEY_FILE) " +
			"or DUSTPAN_KEY_ENCRYPTION_KEY. To make a key file, run: opendustpan key generate --out <path>",
	)
	// errNoKeyTrial is errNoKey for a person who most likely wants a trial.
	errNoKeyTrial = errors.New("no key-encryption key is set. For a trial, run: opendustpan dev")
	// errNoDatabase reports serve with no database.
	errNoDatabase = errors.New(
		"no database is set: set --database (DUSTPAN_DATABASE), for example sqlite:/var/lib/opendustpan/dustpan.db",
	)
	// errDevEnvironment reports serve in a shell that holds DUSTPAN_DEV.
	errDevEnvironment error = &refusalError{
		text:  "DUSTPAN_DEV is set. The dev.env file of opendustpan dev sets it. Start serve in a new shell.",
		cause: nil,
	}
	// errDevKeyRefused reports a key file that opendustpan dev made.
	errDevKeyRefused = errors.New(
		"the key file was made by opendustpan dev: serve refuses it. Set --key-encryption-key-file to a key " +
			"from: opendustpan key generate --out <path>",
	)
)

// version is the server version. The build stamps it with
// -ldflags "-X main.version=...". A build with no stamp prints dev.
var version = "dev"

const (
	// keyEnvVar holds the base64 key-encryption key. It wins when both key
	// settings are present.
	keyEnvVar = "DUSTPAN_KEY_ENCRYPTION_KEY"
	// devEnvVar is set by the dev.env file of opendustpan dev. Serve refuses a
	// shell that holds it.
	devEnvVar = "DUSTPAN_DEV"
	// keyFileEnvVar names a file holding the base64 key-encryption key.
	keyFileEnvVar = "DUSTPAN_KEY_ENCRYPTION_KEY_FILE"
	// keyFileMode is the mode of a key file: owner only.
	keyFileMode = cli.KeyFileMode
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

// common holds the flags shared by every subcommand.
type common struct {
	database     string
	installation string
}

func (c *common) register(fs *flag.FlagSet) {
	cli.ByFlag("database").String(fs, &c.database)
	cli.ByFlag("installation").String(fs, &c.installation)
}

func serve(ctx context.Context, args []string, stderr io.Writer) error {
	return serveWithIO(ctx, args, stderr, isTerminalWriter(stderr))
}

// serveWithIO runs serve with an explicit terminal flag for stderr, so tests
// drive the trial hint of the refusals with a fake terminal.
func serveWithIO(ctx context.Context, args []string, stderr io.Writer, terminal bool) error {
	var f serveFlags

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)

	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if err := refuseDevEnvironment(os.LookupEnv); err != nil {
		return err
	}

	stopHangup := f.catchHangup()
	defer stopHangup()

	// The start resolves against the real variables. A variable changes
	// only at a restart, and the server clears the secret variables after
	// it reads them: each reload resolves against a snapshot that keeps no
	// sensitive value, only its fingerprint (ADR 63 decision 5).
	f.args = args
	f.lookup = cli.SnapshotEnv(os.LookupEnv, cli.DeclaredEnvs())

	if err := f.resolveLayers(fs, os.LookupEnv); err != nil {
		return err
	}

	if err := f.check(); err != nil {
		return err
	}

	if err := f.requireDecisions(terminal, os.LookupEnv); err != nil {
		return err
	}

	return schemaRefusal(kindRefusal(f.run(ctx, stderr), commandServe), commandServe, "")
}

// serveFlags holds the settings of the serve subcommand.
type serveFlags struct {
	common

	// file holds the decoded configuration file, nil without --config.
	// resolved holds the effective value and the source of each setting
	// for the start log (ADR 63 decision 7).
	file     cli.ConfigFile
	resolved []cli.Resolved

	// args and lookup are the command line and the environment snapshot
	// of the start. A reload of the configuration file resolves against
	// them again. fileSum is the SHA-256 of the file at start.
	args    []string
	lookup  cli.LookupFunc
	fileSum [sha256.Size]byte

	// installationKind is the kind of the command: empty means production.
	// Only the dev command sets it.
	installationKind installation.Kind

	// hup receives SIGHUP from the parse of the flags on, when --config
	// names a file. The reloader reads it.
	hup chan os.Signal

	// instanceID names this process in the audit entries that it writes
	// on its own (ADR 63 decision 10).
	instanceID string

	config                       string
	listen                       string
	consoleDir                   string
	origins                      string
	consoleContentTypeOptions    string
	consoleFrameOptions          string
	consoleCSP                   string
	consoleHSTS                  string
	tlsCert                      string
	tlsKey                       string
	publicURL                    string
	issuerBaseURL                string
	oidcIssuer                   string
	oidcProvider                 string
	oidcDisplayName              string
	oidcClientID                 string
	oidcScopes                   string
	oidcNoAutoCreate             bool
	trustedProxies               string
	trustedProxyHeader           string
	ipAllowlists                 bool
	ipAllowlistsIncludeBootstrap bool
	gitHostAllowedRanges         string
	allowPlainConnections        bool
	blobDir                      string
	blobStore                    string
	s3Endpoint                   string
	s3Region                     string
	backupTarget                 string
	allowDevBackups              bool
	logLevel                     string
	logFormat                    string
	brandingPreset               string
	// layer is the static layer of the settings resource. settingsSync records
	// it in the store.
	layer                   *cli.StaticLayer
	settingsSync            func(ctx context.Context) error
	components              string
	keyFile                 string
	maxCheckpointBytes      int64
	maxSecretBatchBytes     int64
	maxStateBytes           int64
	blobCacheBytes          int64
	maxBodyBytesInFlight    int64
	checkpointDeltaCutoff   int64
	disablePulumiJournaling bool
	auditChain              string
	softDeleteRetention     string
	// retention is softDeleteRetention after the check.
	retention time.Duration
	// planRetention and jobLogRetention are the retention flags. jobRetention is
	// their value after the check.
	planRetention, jobLogRetention string
	jobRetention                   history.RetentionSettings
	auditSinks                     []string
	// destinationPorts holds the ports, besides 443, that a notification
	// destination may use.
	destinationPorts string
	logSinks         []string
	// app is the application logger and its sinks, set by run.
	app                 appLog
	auditSigningKeyFile string
	auditVerifyKeysFile string
	auditCheckpoints    bool
}

// kind answers the installation kind of the command: production for serve,
// and the kind that dev sets for the server code that dev shares with serve.
func (f *serveFlags) kind() installation.Kind {
	if f.installationKind == "" {
		return installation.KindProduction
	}

	return f.installationKind
}

// check refuses settings that are wrong together or out of range: the TLS
// pair, the log level and format, the size limits, the delta cutoff, the
// soft-delete retention and the trusted proxies. It keeps the parsed
// retention in f.retention.
// Serve runs it at start and on each reload of the configuration file.
func (f *serveFlags) check() error {
	if err := checkTLSFlags(f.tlsCert, f.tlsKey); err != nil {
		return err
	}

	if _, err := dustpan.ParseComponents(f.components); err != nil {
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
		f.blobCacheBytes,
	}

	if err := checkSizeFlags(sizeLimits); err != nil {
		return err
	}

	if f.checkpointDeltaCutoff < 0 {
		return errDeltaCutoff
	}

	if _, err := audit.ParseChainSetting(f.auditChain); err != nil {
		return fmt.Errorf("--audit-chain: %w", err)
	}

	retention, err := parseRetention(f.softDeleteRetention)
	if err != nil {
		return err
	}

	f.retention = retention

	if err := f.checkJobRetention(); err != nil {
		return err
	}

	if err := f.checkCheckpointFlags(); err != nil {
		return err
	}

	if _, err := egress.ParseAllowedRanges(splitList(f.gitHostAllowedRanges)); err != nil {
		return fmt.Errorf("invalid --git-host-allowed-ranges: %w", err)
	}

	return checkProxyFlags(f.trustedProxies, f.trustedProxyHeader)
}

// checkProxyFlags refuses a trusted proxy list or header that the server
// refuses, with the rules of internal/clientip, so a reload of the
// configuration file refuses the same values as the start.
func checkProxyFlags(list, header string) error {
	prefixes, err := clientip.ParseList(list)
	if err != nil {
		return fmt.Errorf("invalid --trusted-proxies: %w", err)
	}

	parsed, err := clientip.ParseHeader(header)
	if err != nil {
		return fmt.Errorf("invalid --trusted-proxy-header: %w", err)
	}

	if _, err := clientip.NewResolver(prefixes, parsed); err != nil {
		return fmt.Errorf("invalid --trusted-proxies: %w", err)
	}

	return nil
}

func (f *serveFlags) register(fs *flag.FlagSet) {
	cli.ByFlag("config").String(fs, &f.config)
	f.common.register(fs)
	f.registerNet(fs)
	f.registerLimits(fs)
	f.registerStorage(fs)
	f.registerKey(fs)
}

// resolveLayers reads the configuration file that --config names and
// applies the precedence flag > env > file > default to each serve setting
// (ADR 63 decision 1). A refused file stops the start.
func (f *serveFlags) resolveLayers(fs *flag.FlagSet, lookup cli.LookupFunc) error {
	if strings.TrimSpace(f.config) != "" {
		data, err := cli.ReadConfigBytes(strings.TrimSpace(f.config))
		if err != nil {
			return err
		}

		file, err := cli.ParseConfig(data)
		if err != nil {
			return err
		}

		f.file = file
		f.fileSum = sha256.Sum256(data)
	}

	resolved, err := cli.Resolve(fs, cli.ServeSettings(), lookup, f.file)
	if err != nil {
		return fmt.Errorf("resolve settings: %w", err)
	}

	f.resolved = resolved

	return cli.RefuseFlagPassword(fs, resolved)
}

// registerKey registers the key-encryption key file flags. The key itself
// never appears here: it comes from DUSTPAN_KEY_ENCRYPTION_KEY or from the
// file, so it does not show in process listings.
func (f *serveFlags) registerKey(fs *flag.FlagSet) {
	cli.ByFlag("key-encryption-key-file").String(fs, &f.keyFile)
}

// registerStorage registers the blob store, S3 and backup target flags.
// Credentials never appear here: they come from AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY and AWS_SESSION_TOKEN only.
func (f *serveFlags) registerStorage(fs *flag.FlagSet) {
	cli.ByFlag("blob-store").String(fs, &f.blobStore)
	cli.ByFlag("blob-cache-bytes").Int64(fs, &f.blobCacheBytes)
	cli.ByFlag("s3-endpoint").String(fs, &f.s3Endpoint)
	cli.ByFlag("s3-region").String(fs, &f.s3Region)
	cli.ByFlag("backup-target").String(fs, &f.backupTarget)
	cli.ByFlag(cli.UnsafeAllowDevBackupsFlag).Bool(fs, &f.allowDevBackups)
	cli.ByFlag("audit-chain").String(fs, &f.auditChain)
	cli.ByFlag("audit-sink").List(fs, &f.auditSinks)
	cli.ByFlag("destination-ports").String(fs, &f.destinationPorts)
	cli.ByFlag("audit-checkpoints").Bool(fs, &f.auditCheckpoints)
	cli.ByFlag("audit-signing-key-file").String(fs, &f.auditSigningKeyFile)
	cli.ByFlag("audit-verify-keys-file").String(fs, &f.auditVerifyKeysFile)
}

// consoleHeaders answers the console header settings of the flags.
func (f *serveFlags) consoleHeaders() dustpan.ConsoleHeaders {
	return dustpan.ConsoleHeaders{
		ContentTypeOptions:      f.consoleContentTypeOptions,
		FrameOptions:            f.consoleFrameOptions,
		ContentSecurityPolicy:   f.consoleCSP,
		StrictTransportSecurity: f.consoleHSTS,
	}
}

// registerNet registers the network and console flags.
func (f *serveFlags) registerNet(fs *flag.FlagSet) {
	cli.ByFlag("components").String(fs, &f.components)
	cli.ByFlag("listen").String(fs, &f.listen)
	cli.ByFlag("console-dir").String(fs, &f.consoleDir)
	cli.ByFlag("allowed-origins").String(fs, &f.origins)
	cli.ByFlag("console-content-type-options").String(fs, &f.consoleContentTypeOptions)
	cli.ByFlag("console-frame-options").String(fs, &f.consoleFrameOptions)
	cli.ByFlag("console-csp").String(fs, &f.consoleCSP)
	cli.ByFlag("console-hsts").String(fs, &f.consoleHSTS)
	cli.ByFlag("tls-cert").String(fs, &f.tlsCert)
	cli.ByFlag("tls-key").String(fs, &f.tlsKey)
	cli.ByFlag("public-url").String(fs, &f.publicURL)
	cli.ByFlag("issuer-base-url").String(fs, &f.issuerBaseURL)
	cli.ByFlag("trusted-proxies").String(fs, &f.trustedProxies)
	cli.ByFlag("trusted-proxy-header").String(fs, &f.trustedProxyHeader)
	cli.ByFlag("ip-allowlists").Bool(fs, &f.ipAllowlists)
	cli.ByFlag("ip-allowlists-include-bootstrap").Bool(fs, &f.ipAllowlistsIncludeBootstrap)
	cli.ByFlag("git-host-allowed-ranges").String(fs, &f.gitHostAllowedRanges)
	cli.ByFlag("allow-plain-connections").Bool(fs, &f.allowPlainConnections)
	cli.ByFlag("oidc-issuer").String(fs, &f.oidcIssuer)
	cli.ByFlag("oidc-provider").String(fs, &f.oidcProvider)
	cli.ByFlag("oidc-display-name").String(fs, &f.oidcDisplayName)
	cli.ByFlag("oidc-client-id").String(fs, &f.oidcClientID)
	cli.ByFlag("oidc-scopes").String(fs, &f.oidcScopes)
	cli.ByFlag("oidc-no-auto-create").Bool(fs, &f.oidcNoAutoCreate)
	cli.ByFlag("blob-dir").String(fs, &f.blobDir)
	cli.ByFlag("log-level").String(fs, &f.logLevel)
	cli.ByFlag("log-format").String(fs, &f.logFormat)
	cli.ByFlag("branding-preset").String(fs, &f.brandingPreset)
	cli.ByFlag("log-sink").List(fs, &f.logSinks)
}

// registerLimits registers the large-body size flags.
func (f *serveFlags) registerLimits(fs *flag.FlagSet) {
	cli.ByFlag("max-checkpoint-bytes").Int64(fs, &f.maxCheckpointBytes)
	cli.ByFlag("max-secret-batch-bytes").Int64(fs, &f.maxSecretBatchBytes)
	cli.ByFlag("max-state-bytes").Int64(fs, &f.maxStateBytes)
	cli.ByFlag("max-body-bytes-in-flight").Int64(fs, &f.maxBodyBytesInFlight)
	cli.ByFlag("checkpoint-delta-cutoff").Int64(fs, &f.checkpointDeltaCutoff)
	cli.ByFlag("disable-pulumi-journaling").Bool(fs, &f.disablePulumiJournaling)
	cli.ByFlag("soft-delete-retention").String(fs, &f.softDeleteRetention)
	f.registerJobRetention(fs)
}

// run opens the store, runs the start-up checks and serves until ctx is done.
// It resolves the key-encryption key before it opens the database, so a
// missing key stops with the key error and never makes a key silently. Only
// when serve is about to make a key file does it open the store first: the
// store must hold no wrapped keys, because a new key cannot open them (issue
// 355). Serve then uses the same store.
func (f *serveFlags) run(ctx context.Context, stderr io.Writer) error {
	app, err := f.appLogFor(stderr) //nolint:contextcheck // The log writers outlive this call; close bounds them.
	if err != nil {
		return err
	}

	f.app = app
	logger := app.logger

	defer closeAppLog(ctx, &app, stderr)

	cli.LogResolved(ctx, logger, f.resolved)

	f.instanceID = instance.NewID()
	if err := f.logComponents(ctx, logger); err != nil {
		return err
	}

	if dbErr := checkDatabaseFlag(f.database); dbErr != nil {
		return dbErr
	}

	authn, authErr := adminAuthenticator(cli.LayeredLookup(os.LookupEnv, f.file))
	if authErr != nil {
		return authErr
	}

	var st store

	defer func() {
		if st != nil {
			st.close()
		}
	}()

	wrapper, keyPresent, keyErr := directKeyWrapper()
	if keyErr != nil {
		return keyErr
	}

	if !keyPresent {
		path := strings.TrimSpace(f.keyFile)

		wrapper, keyErr = useKeyFile(path, logger)
		if keyErr != nil {
			return keyErr
		}

		f.warnKeyNextToData(logger, path)
	}

	opened, storeErr := openStore(ctx, f.database, logger, openMigrate, installation.KindProduction)
	if storeErr != nil {
		return storeErr
	}

	st = opened

	return f.serveWithStores(ctx, opened, authn, wrapper, logger)
}

// writeStartEntry writes the start entry of ADR 63 decision 10: one audit
// entry in the server scope with the channel startup, the instance ID as the
// subject and each setting with its layer and its effective value. The audit
// log is always on, so a failed write stops the start (decision 11).
func (f *serveFlags) writeStartEntry(ctx context.Context, entries audit.ServerStore) error {
	changes := append(cli.AuditSettings(f.resolved), kindChange(f.kind()))
	entry := cli.SettingsEntry(audit.ChannelStartup, f.instanceID, time.Now(), changes)

	if _, err := entries.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("write the start audit entry: %w", err)
	}

	return nil
}

// beforeServe returns the hook that runs after the start-up checks and before
// serving: it writes the start entry, records the static layer of the
// settings resource, and starts the reloader.
func (f *serveFlags) beforeServe(
	ctx context.Context, st store, logger *slog.Logger, reloads *reloadMetrics, started *startedReloader,
) func(context.Context) error {
	return func(hookCtx context.Context) error {
		if startErr := f.writeStartEntry(hookCtx, st); startErr != nil {
			return startErr
		}

		f.syncSettings(hookCtx, logger)

		started.stop = f.startReloader(ctx, st, reloads.record, logger)

		return nil
	}
}

// checkDatabaseFlag rejects a --database value with an unknown form before
// serve resolves the key file, so the backend error keeps its precedence.
func checkDatabaseFlag(database string) error {
	switch {
	case strings.TrimSpace(database) == "":
		return errNoDatabase
	case strings.HasPrefix(database, "sqlite:"):
		return nil
	case strings.HasPrefix(database, "postgres://"), strings.HasPrefix(database, "postgresql://"):
		return nil
	default:
		return errUnknownBackend
	}
}

// isTerminal reports whether stdin and stderr are both character devices, so
// serve prompts only on a real terminal.
func isTerminal(stdin io.Reader, stderr io.Writer) bool {
	stdinFile, ok := stdin.(*os.File)
	if !ok {
		return false
	}

	stderrFile, ok := stderr.(*os.File)
	if !ok {
		return false
	}

	return isCharDevice(stdinFile) && isCharDevice(stderrFile)
}

// isCharDevice reports whether f is a character device.
func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// refuseDevEnvironment refuses a shell that holds DUSTPAN_DEV: the dev.env
// file of opendustpan dev sets it, and serve is for production.
func refuseDevEnvironment(lookup cli.LookupFunc) error {
	if _, held := lookup(devEnvVar); held {
		return errDevEnvironment
	}

	return nil
}

// ignoredForTrial reports whether env is a variable that a shell often exports
// for other commands or other tools. It does not show that the person set up
// serve, so it does not turn off the trial hint.
func ignoredForTrial(env string) bool {
	return env == adminTokenEnvVar || env == adminTokenFileEnvVar
}

// noSettings reports whether no setting of serve has a value from a flag, a
// variable or a configuration file. A key-encryption key in the variable
// counts as a setting.
func (f *serveFlags) noSettings(lookup cli.LookupFunc) bool {
	if hasValue(lookup, keyEnvVar) {
		return false
	}

	for _, r := range f.resolved {
		if r.Source == cli.SourceDefault || ignoredForTrial(r.Setting.Env) {
			continue
		}

		return false
	}

	return true
}

// hasValue reports whether the variable name has a value that is not blank.
func hasValue(lookup cli.LookupFunc, name string) bool {
	value, set := lookup(name)

	return set && strings.TrimSpace(value) != ""
}

// requireDecisions refuses a start with no key-encryption key and a start with
// no database, in this order, and reports only the first. Serve stops before
// it opens the database. Each error names one command that helps. A person at
// a terminal who set no setting of serve most likely wants a trial, so the
// error for a missing key names the dev command. The error for a missing
// database never does: the code reaches the database check only with a key,
// and a key is a setting of serve.
func (f *serveFlags) requireDecisions(terminal bool, lookup cli.LookupFunc) error {
	trial := terminal && f.noSettings(lookup)

	if !hasValue(lookup, keyEnvVar) && strings.TrimSpace(f.keyFile) == "" {
		if trial {
			return errNoKeyTrial
		}

		return errNoKey
	}

	if strings.TrimSpace(f.database) == "" {
		return errNoDatabase
	}

	return nil
}

// warnKeyNextToData logs a warning when the key file is next to the data: a
// backup of the data then holds the key too.
func (f *serveFlags) warnKeyNextToData(logger *slog.Logger, path string) {
	if cli.KeyNextToData(path, f.database, f.blobDir) {
		logger.Warn(
			"the key file is next to the data: a backup of the data holds the key too",
			slog.String("path", path),
		)
	}
}

// isTerminalWriter reports whether w is a character device, so serve gives
// the trial hint only to a person at a terminal.
func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)

	return ok && isCharDevice(file)
}

// directKeyWrapper returns the wrapper for the direct key-encryption key
// value when set. Present tells whether the value was set: without it serve
// falls back to the key file. It clears the variable after the read, so the
// key does not stay in memory that child processes inherit.
//
//nolint:ireturn // The caller keeps the wrapper as a secret.Wrapper port; absent without an error means no direct value.
func directKeyWrapper() (secret.Wrapper, bool, error) {
	raw, ok := os.LookupEnv(keyEnvVar)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, false, nil
	}

	if err := os.Unsetenv(keyEnvVar); err != nil {
		return nil, true, fmt.Errorf("%s: clear variable: %w", keyEnvVar, err)
	}

	key, err := decodeKey(keyEnvVar, raw)
	if err != nil {
		return nil, true, err
	}

	wrapper, err := builtinWrapper(key)
	if err != nil {
		return nil, true, err
	}

	return wrapper, true, nil
}

// isDevKeyFile reports whether the file at path starts with the header line
// of opendustpan dev. It reads only as many bytes as the header has. A file
// that it cannot read is not a key of dev: the read of the key reports it.
func isDevKeyFile(path string) bool {
	head, err := readFileHead(path, len(devKeyHeader))

	return err == nil && string(head) == devKeyHeader
}

// readFileHead reads at most n bytes from the start of the file at path.
func readFileHead(path string, n int) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open directory: %w", err)
	}

	head, readErr := readHeadOf(root, filepath.Base(path), n)
	closeErr := root.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read the head of the file: %w", err)
	}

	return head, nil
}

// readHeadOf reads at most n bytes from the start of the file name in root.
func readHeadOf(root *os.Root, name string, n int) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	head, readErr := io.ReadAll(io.LimitReader(file, int64(n)))
	closeErr := file.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return head, nil
}

// useKeyFile reads the key file at path with the owner-only rules and returns
// its wrapper. It logs one line with the path, never the key.
//
//nolint:ireturn // The caller keeps the wrapper as a secret.Wrapper port.
func useKeyFile(path string, logger *slog.Logger) (secret.Wrapper, error) {
	key, err := readKeyFile(path)
	if err != nil {
		// A key file of dev does not decode: the header line is not base64.
		// Name the real cause.
		if isDevKeyFile(path) {
			return nil, errDevKeyRefused
		}

		return nil, err
	}

	wrapper, wrapErr := builtinWrapper(key)
	if wrapErr != nil {
		return nil, wrapErr
	}

	logger.Info("using key-encryption key file", slog.String("path", path))

	return wrapper, nil
}

// builtinWrapper returns the built-in wrapper for key. Loaders already
// checked the size, so this fails only when the key is bad.
//
//nolint:ireturn // No key is impossible here, but the caller keeps the interface.
func builtinWrapper(key []byte) (secret.Wrapper, error) {
	wrapper, err := secret.NewBuiltinWrapper(key)
	if err != nil {
		// Unreachable: decodeKey already checked the size.
		return nil, fmt.Errorf("key-encryption key: %w", err)
	}

	return wrapper, nil
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

	sinks, err := openAuditSinks(f.auditSinks, blobs, os.LookupEnv)
	if err != nil {
		return err
	}

	defer func() {
		if closeErr := sinks.close(); closeErr != nil {
			logger.WarnContext(ctx, "close audit sinks", slog.String("error", closeErr.Error()))
		}
	}()

	stopHangup := sinks.reopenOnHangup(ctx, logger, f.app.files)
	defer stopHangup()

	opts, optErr := f.serverOptions(st, blobs, authn, wrapper, logger)
	if optErr != nil {
		return optErr
	}
	opts = append(opts, f.restoreOptions(ctx, logger, backupTarget)...)
	opts = append(opts, dustpan.WithAuditSinks(sinks.ship...), dustpan.WithDrainPeriod(dustpan.DefaultDrainPeriod))

	// After the start-up checks and before serving, write the start entry
	// and only then start the reloader: a start that fails its checks
	// writes no entry, and no reload entry comes before the start entry
	// (ADR 63 decision 10).
	reloads := &reloadMetrics{observe: nil}
	started := &startedReloader{stop: nil}

	opts = append(opts, dustpan.WithBeforeServe(f.beforeServe(ctx, st, logger, reloads, started)))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		return fmt.Errorf("configure server: %w", err)
	}

	if f.app.drops != nil {
		f.app.drops.attach(srv.Metrics().AddLogSinkDropped)
	}

	reloads.observe = srv.Metrics().ObserveConfigReload

	defer started.Stop()

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

// logComponents logs the components of the process with the instance, and one
// line for each setting that no selected component reads. The settings stay
// set and the start goes on.
func (f *serveFlags) logComponents(ctx context.Context, logger *slog.Logger) error {
	set, err := dustpan.ParseComponents(f.components)
	if err != nil {
		return fmt.Errorf("--components: %w", err)
	}

	logger.LogAttrs(ctx, slog.LevelInfo, "instance",
		slog.String("instance_id", f.instanceID), slog.String("components", set.String()))

	for _, name := range cli.UnreadFlags(f.resolved, set.Strings()) {
		logger.LogAttrs(ctx, slog.LevelInfo, "setting has no effect: no selected component reads it",
			slog.String("key", name), slog.String("components", set.String()))
	}

	return nil
}

// serverOptions returns the NewServer options for the opened store, blob
// store, authenticator, key wrapper and logger, with the audit chain
// setting and the signed audit checkpoints.
func (f *serveFlags) serverOptions(
	st store,
	blobs blob.Store,
	authn *identity.StaticTokens,
	wrapper secret.Wrapper,
	logger *slog.Logger,
) ([]dustpan.Option, error) {
	proxies, parseErr := clientip.ParseList(f.trustedProxies)
	if parseErr != nil {
		return nil, fmt.Errorf("invalid --trusted-proxies %q: %w", f.trustedProxies, parseErr)
	}

	signIn, signInErr := f.signInOptions()
	if signInErr != nil {
		return nil, signInErr
	}

	// check refused a bad value, so the parse fails only on a bug.
	chain, chainErr := audit.ParseChainSetting(f.auditChain)
	if chainErr != nil {
		return nil, fmt.Errorf("--audit-chain: %w", chainErr)
	}

	checkpoints, cpErr := f.checkpointOptions()
	if cpErr != nil {
		return nil, cpErr
	}

	opts := append([]dustpan.Option{
		dustpan.WithInstallation(namespace.InstallationName(f.installation)),
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
		dustpan.WithConsoleHeaders(f.consoleHeaders()),
		dustpan.WithTrustedProxies(proxies...),
		dustpan.WithTrustedProxyHeader(f.trustedProxyHeader),
		dustpan.WithTLS(f.tlsCert, f.tlsKey),
		dustpan.WithPublicURL(f.publicURL),
		dustpan.WithIssuerBaseURL(f.issuerBaseURL),
		dustpan.WithVersion(version),
		dustpan.WithMaxCheckpointBytes(int(f.maxCheckpointBytes)),
		dustpan.WithMaxSecretBatchBytes(int(f.maxSecretBatchBytes)),
		dustpan.WithMaxStateBytes(f.maxStateBytes),
		dustpan.WithMaxBodyBytesInFlight(f.maxBodyBytesInFlight),
		dustpan.WithBlobCache(f.blobCacheBytes),
		dustpan.WithCheckpointDeltaCutoff(int(f.checkpointDeltaCutoff)),
		dustpan.WithDisablePulumiJournaling(f.disablePulumiJournaling),
		dustpan.WithAuditChain(chain),
		dustpan.WithSoftDeleteRetention(f.retention),
		dustpan.WithRetention(f.jobRetention),
		dustpan.WithIPAllowlists(f.ipAllowlists),
		dustpan.WithIPAllowlistsIncludeBootstrap(f.ipAllowlistsIncludeBootstrap),
		dustpan.WithIPAllowlistStore(st),
		dustpan.WithInstanceID(f.instanceID),
	}, append(signIn, checkpoints...)...)

	tail, tailErr := f.storeOptions(st, wrapper, logger)

	return append(opts, tail...), tailErr
}

// gitHostOptions returns the option of the address ranges that a connection to
// a Git host may reach. check refused a bad value, so the parse fails only on a
// bug.
func (f *serveFlags) gitHostOptions() ([]dustpan.Option, error) {
	ranges, err := egress.ParseAllowedRanges(splitList(f.gitHostAllowedRanges))
	if err != nil {
		return nil, fmt.Errorf("invalid --git-host-allowed-ranges %q: %w", f.gitHostAllowedRanges, err)
	}

	return []dustpan.Option{dustpan.WithGitHostAllowedRanges(ranges...)}, nil
}

// storeOptions returns the options of the components and of the stores that
// the store may or may not keep: the durable operations, the runners and the
// profiles.
func (f *serveFlags) storeOptions(st store, wrapper secret.Wrapper, logger *slog.Logger) ([]dustpan.Option, error) {
	set, err := dustpan.ParseComponents(f.components)
	if err != nil {
		return nil, fmt.Errorf("--components: %w", err)
	}

	gitHostOpts, err := f.gitHostOptions()
	if err != nil {
		return nil, err
	}

	opts := append([]dustpan.Option{dustpan.WithComponents(set)}, gitHostOpts...)
	opts = append(opts, durableStoreOption(st)...)
	opts = append(opts, runnerStoreOption(st)...)
	opts = append(opts, sourceFilterStoreOption(st)...)
	opts = append(opts, trustRuleStoreOption(st)...)
	opts = append(opts, jobStoreOption(st, logger)...)
	opts = append(opts, triggerStoreOption(st)...)
	opts = append(opts, unitSettingsStoreOption(st)...)
	opts = append(opts, savedViewStoreOption(st)...)

	wiring, wireErr := f.settingsWiring(st)
	if wireErr != nil {
		return nil, wireErr
	}

	opts = append(opts, wiring.options...)
	f.settingsSync = wiring.sync
	opts = append(opts, driftStoreOption(st)...)
	opts = append(opts, gitHostStoreOption(st)...)
	opts = append(opts, registryStoreOption(st)...)
	opts = append(opts, sinkStoreOption(st)...)

	ports, err := f.sinkPorts()
	if err != nil {
		return nil, err
	}
	opts = append(opts, dustpan.WithSinkPorts(ports), dustpan.WithInstallationKind(f.kind()),
		dustpan.WithInstallationKindStore(st), dustpan.WithInstanceStore(st))

	return append(opts, profileStoreOption(st, wrapper)...), nil
}

// errDestinationPorts reports a --destination-ports item that is no port.
var errDestinationPorts = errors.New("--destination-ports: each item is a port number from 1 to 65535")

// sinkPorts parses --destination-ports: whole port numbers, separated by
// commas. It returns an error that names the flag for an item that is not a
// port, and never repeats the value.
func (f *serveFlags) sinkPorts() (egress.Ports, error) {
	return parseSinkPorts(f.destinationPorts)
}

// parseSinkPorts parses the value of --destination-ports.
func parseSinkPorts(value string) (egress.Ports, error) {
	var extra []int

	for _, item := range splitList(value) {
		port, err := strconv.Atoi(item)
		if err != nil {
			return egress.Ports{}, errDestinationPorts
		}

		extra = append(extra, port)
	}

	ports, err := egress.NewPorts(extra...)
	if err != nil {
		return egress.Ports{}, errDestinationPorts
	}

	return ports, nil
}

// durableStoreOption returns the durable schedule store option for st, or nil
// when st keeps no durable schedules.
func durableStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithDurableStore(s.DurableOperations())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithDurableStore(s.DurableOperations())}
	default:
		return nil
	}
}

// runnerStoreOption returns the option of the runner stores for st, or nil
// when st keeps no runners.
func runnerStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithRunners(s.Runners())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithRunners(s.Runners())}
	default:
		return nil
	}
}

// sourceFilterStoreOption returns the option of the source filter store for st,
// or nil when st keeps no source filter.
func sourceFilterStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithSourceFilters(s.SourceFilters())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithSourceFilters(s.SourceFilters())}
	default:
		return nil
	}
}

// trustRuleStoreOption returns the option of the trust rule store for st, or
// nil when st keeps no trust rules.
func trustRuleStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithTrustRules(s.TrustRules())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithTrustRules(s.TrustRules())}
	default:
		return nil
	}
}

// jobStoreOption returns the options of the job store and of the job claimer
// for st, or nil when st keeps no jobs. The claimer takes the phases of the
// jobs as durable operations, so a poll of a runner of a pool for jobs finds
// work.
func jobStoreOption(st store, logger *slog.Logger) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{
			dustpan.WithJobStore(s.Jobs()),
			dustpan.WithJobClaimer(func(g job.GateGuard) job.Claimer {
				return job.NewClaimer(s.Jobs(), s.DurableOperationsPort(), s.Store, g, job.WithClaimerLogger(logger))
			}),
		}
	case postgresStore:
		return []dustpan.Option{
			dustpan.WithJobStore(s.Jobs()),
			dustpan.WithJobClaimer(func(g job.GateGuard) job.Claimer {
				return job.NewClaimer(s.Jobs(), s.DurableOperationsPort(), s.Store, g, job.WithClaimerLogger(logger))
			}),
		}
	default:
		return nil
	}
}

// triggerStoreOption returns the option of the run trigger store for st, or nil
// when st keeps no triggers.
func triggerStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithTriggerStore(s.Triggers())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithTriggerStore(s.Triggers())}
	default:
		return nil
	}
}

// unitSettingsStoreOption returns the option of the unit settings store for
// st, or nil when st keeps no unit settings.
func unitSettingsStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithUnitSettingsStore(s.UnitSettings())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithUnitSettingsStore(s.UnitSettings())}
	default:
		return nil
	}
}

// savedViewStoreOption returns the option of the saved view store for st, or
// nil when st keeps no saved views.
func savedViewStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithSavedViewStore(s.SavedViews())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithSavedViewStore(s.SavedViews())}
	default:
		return nil
	}
}

// driftStoreOption returns the option of the drift result store for st, or nil
// when st keeps no drift results.
func driftStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithDriftStore(s.DriftResults())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithDriftStore(s.DriftResults())}
	default:
		return nil
	}
}

// gitHostStoreOption returns the option of the Git host store for st, or nil
// when st keeps no Git host connections.
func gitHostStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithGitHostStore(s.GitHost())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithGitHostStore(s.GitHost())}
	default:
		return nil
	}
}

// registryStoreOption returns the option of the registry stores for st, or nil
// when st keeps no registry.
func registryStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithRegistry(s.Registry(), s.RegistryKeys())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithRegistry(s.Registry(), s.RegistryKeys())}
	default:
		return nil
	}
}

// sinkStoreOption returns the option of the notification sink store for st,
// or nil when st keeps no sinks.
func sinkStoreOption(st store) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{dustpan.WithSinkStore(s.NotificationSinks())}
	case postgresStore:
		return []dustpan.Option{dustpan.WithSinkStore(s.NotificationSinks())}
	default:
		return nil
	}
}

// profileStoreOption returns the profile store option for st, or nil when st
// keeps no profiles. wrapper seals the server key of the replay records.
func profileStoreOption(st store, wrapper secret.Wrapper) []dustpan.Option {
	switch s := st.(type) {
	case sqliteStore:
		return []dustpan.Option{
			dustpan.WithProfileStore(s.Profiles()),
			dustpan.WithIssuerStore(s.Issuer()),
			dustpan.WithIdempotencyStore(s.Idempotency(wrapper)),
		}
	case postgresStore:
		return []dustpan.Option{
			dustpan.WithProfileStore(s.Profiles()),
			dustpan.WithIssuerStore(s.Issuer()),
			dustpan.WithIdempotencyStore(s.Idempotency(wrapper)),
		}
	default:
		return nil
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
	blobDir := cli.DefaultBlobDir(database, flagDir)

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

	store, err := blobs3.Open(ctx, signer, prefix, nil, blobs3.WithLogger(logger))
	if err != nil {
		return nil, err
	}

	return store, nil
}

// openBackupTarget opens the backup target in raw as a blob store. Empty
// means no backup target is configured: it returns nil without an error. A
// dir:/path target opens the local directory, creating it owner-only, as a
// backup holds sealed OpenTofu state. An s3://<bucket>[/<prefix>]
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

// backupOptions returns the NewServer backup option for the opened target, or
// none when no backup target is configured. WithBackupTarget is the only way
// the target gets in: no request field, header or query value sets it.
func (f *serveFlags) backupOptions(target blob.Store) []dustpan.Option {
	if target == nil {
		return nil
	}

	return []dustpan.Option{dustpan.WithBackupTarget(target, strings.TrimSpace(f.backupTarget))}
}

// restoreOptions returns the backup option and the dev backup options, and
// writes the warning of a start with the dev backup flag.
func (f *serveFlags) restoreOptions(ctx context.Context, logger *slog.Logger, target blob.Store) []dustpan.Option {
	f.warnDevBackups(ctx, logger)

	return append(f.backupOptions(target), f.devBackupOptions()...)
}

// warnDevBackups writes the one warning of each start with the flag that lets
// a production installation restore a backup of a dev installation.
func (f *serveFlags) warnDevBackups(ctx context.Context, logger *slog.Logger) {
	if !f.allowDevBackups {
		return
	}

	logger.WarnContext(ctx, "This server lets a production installation restore a backup of a dev installation. "+
		"A restore deletes the dev users and their data. We do not recommend this.",
		slog.String("flag", "--"+cli.UnsafeAllowDevBackupsFlag))
}

// devBackupOptions returns the NewServer options for a dev backup in a
// production installation. The flag is the only source of the setting: it has
// no variable and no configuration file key. The hint names the flag in code
// quotes for the refusal text.
func (f *serveFlags) devBackupOptions() []dustpan.Option {
	return []dustpan.Option{
		dustpan.WithAllowDevBackups(f.allowDevBackups),
		dustpan.WithAllowDevHint("`--" + cli.UnsafeAllowDevBackupsFlag + "`"),
	}
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

// closeAppLog flushes and closes the application log sinks. The sinks are
// closing, so a failure goes to stderr.
func closeAppLog(ctx context.Context, app *appLog, stderr io.Writer) {
	if err := app.close(ctx); err != nil {
		//nolint:errcheck // Nothing is left to report a failed write of stderr to.
		fmt.Fprintln(stderr, "opendustpan: close log sinks:", err)
	}
}

// logLevelOf answers the slog level of a log level setting that
// checkLogFlags accepted. An unknown value answers info.
func logLevelOf(setting string) slog.Level {
	switch normalizeLogLevel(setting) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// appLogFor builds the application logger and its sinks from the log
// flags. Without --log-sink it writes to stderr, as serve always did.
func (f *serveFlags) appLogFor(stderr io.Writer) (appLog, error) {
	if err := checkLogFlags(f.logLevel, f.logFormat); err != nil {
		return appLog{}, err
	}

	return newAppLog(f.logSinks, logLevelOf(f.logLevel), f.logFormat, stderr, os.LookupEnv)
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
	ipallow.Store
	audit.ServerStore
	principal.Store
	accesstoken.Store
	accesstoken.HostStore
	tempadmin.Store
	installation.KindStore
	installation.InstanceStore
	// gate.Store is here so that opendustpan serve always runs with the
	// state.write gates on: a store wrapper that does not keep gates does not
	// compile.
	gate.Store
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

// openMode says whether openStore applies the pending migrations.
type openMode int

const (
	// openMigrate applies the pending migrations, as serve does at start.
	openMigrate openMode = iota
	// openNoMigrate applies none and refuses a database with a pending
	// migration.
	openNoMigrate
)

// openStore opens the persistence adapter that --database names. Only serve,
// dev, dev reset and host create-temp-admin call it: serve and dev run the
// server, dev reset makes the schema of a dev database again, and
// host create-temp-admin recovers a lost administrator through the database,
// after its off switch and the recovery mode allow it. Every other command
// is a client of the REST API. The gate test swaps it for a function that
// fails the test, so a future client command that opens a store fails the
// test. The mode says whether Open applies pending migrations: serve
// applies them, and the host command applies none and refuses a database
// with a pending migration. The kind is the installation kind of the
// command: the opener refuses a database that another kind claimed before it
// applies a migration. An empty kind makes no check.
//
//nolint:gochecknoglobals // The gate test swaps the opener, so it is a variable.
var openStore = func(
	ctx context.Context, database string, logger *slog.Logger, mode openMode, kind installation.Kind,
) (store, error) {
	switch {
	case strings.HasPrefix(database, "sqlite:"):
		opts := []sqlite.Option{
			sqlite.WithLogger(logger), sqlite.WithBinaryVersion(version), sqlite.WithInstallationKind(kind),
		}
		if mode == openNoMigrate {
			opts = append(opts, sqlite.WithoutMigrate())
		}

		s, err := sqlite.Open(ctx, strings.TrimPrefix(database, "sqlite:"), opts...)
		if err != nil {
			return nil, fmt.Errorf("open sqlite store: %w", err)
		}

		warnDevBuild(ctx, logger, s)

		return sqliteStore{s}, nil
	case strings.HasPrefix(database, "postgres://"), strings.HasPrefix(database, "postgresql://"):
		opts := []postgres.Option{
			postgres.WithLogger(logger), postgres.WithBinaryVersion(version), postgres.WithInstallationKind(kind),
		}
		if mode == openNoMigrate {
			opts = append(opts, postgres.WithoutMigrate())
		}

		s, err := postgres.Open(ctx, database, opts...)
		if errors.Is(err, postgres.ErrFailoverSetting) {
			return nil, fmt.Errorf("open postgres store: %s: %w", errcode.StoreFailoverSettingInvalid, err)
		}

		if err != nil {
			return nil, fmt.Errorf("open postgres store: %w", err)
		}

		warnDevBuild(ctx, logger, s)
		logFailoverSettings(ctx, logger, s.FailoverSettings())

		return postgresStore{s}, nil
	default:
		return nil, errUnknownBackend
	}
}

// logFailoverSettings writes the effective failover settings of the pool
// once at the start: each value and where it came from. The record holds no
// host, user or password.
func logFailoverSettings(ctx context.Context, logger *slog.Logger, settings postgres.FailoverSettings) {
	// One group for each setting, then the check, the deadline and the three
	// server timeouts.
	const extraAttrs = 5

	attrs := make([]any, 0, len(settings.Settings)+extraAttrs)

	for _, setting := range settings.Settings {
		attrs = append(attrs, slog.Group(setting.Name,
			slog.String("value", setting.Value), slog.String("from", setting.Source)))
	}

	attrs = append(attrs, slog.String("connect_check", settings.ConnectCheck),
		slog.Duration("statement_deadline", settings.StatementDeadline),
		slog.Duration("statement_timeout", settings.ServerStatementTimeout),
		slog.Duration("idle_in_transaction_session_timeout", settings.ServerIdleInTransactionTimeout),
		slog.Duration("client_connection_check_interval", settings.ClientConnectionCheckInterval))

	logger.InfoContext(ctx, "database failover settings", attrs...)
}

// appliedMigrationsReader reads the applied migrations of an open store.
type appliedMigrationsReader interface {
	AppliedMigrations(ctx context.Context) ([]schema.AppliedMigration, error)
}

// warnDevBuild logs a warning when a dev build opens a schema that records a
// minimum binary version. A dev build counts as newer than every release, so
// the minimum never refuses it.
func warnDevBuild(ctx context.Context, logger *slog.Logger, s appliedMigrationsReader) {
	if !schema.IsDevBuild(version) {
		return
	}

	applied, err := s.AppliedMigrations(ctx)
	if err != nil {
		logger.WarnContext(ctx, "read the minimum binary version of the database schema",
			slog.String("error", err.Error()))

		return
	}

	minimums := make([]string, 0, len(applied))
	for _, m := range applied {
		minimums = append(minimums, m.MinBinaryVersion)
	}

	minimum, err := schema.HighestMinimum(minimums)
	if err != nil || minimum == "" {
		return
	}

	logger.WarnContext(ctx, "dev build ignores the minimum binary version of the database schema",
		slog.String("binary_version", version), slog.String("schema_minimum", minimum))
}

// minRetention is the shortest soft-delete retention the server accepts.
const minRetention = dustpan.MinSoftDeleteRetention

// parseRetention parses the soft-delete retention: a Go duration of at least
// one hour (ADR 47, decision 2). There is no maximum.
func parseRetention(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil || d < minRetention {
		return 0, errRetention
	}

	return d, nil
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
