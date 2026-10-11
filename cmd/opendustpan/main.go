// Command opendustpan runs the OpenDustpan server with the built-in adapters.
//
//	opendustpan --version
//	opendustpan serve [flags]
//	opendustpan namespace create [flags] <name>
//
// Every flag can also be set through the environment variable shown in its
// help text. The bootstrap admin token is read from DUSTPAN_ADMIN_TOKEN only,
// never from a flag, so it does not appear in process listings. The
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
	"strings"
	"syscall"

	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
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
		"usage: opendustpan --version | opendustpan serve | opendustpan namespace create <name>",
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

	switch {
	case args[0] == "--version":
		_, err := fmt.Fprintln(stdout, version)
		if err != nil {
			return fmt.Errorf("write version: %w", err)
		}

		return nil
	case args[0] == "serve":
		return serve(ctx, args[1:], stderr)
	case len(args) >= 2 && args[0] == "namespace" && args[1] == "create":
		return createNamespace(ctx, args[2:], stdout, stderr)
	default:
		return errUsage
	}
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

	return f.run(ctx, stderr)
}

// serveFlags holds the settings of the serve subcommand.
type serveFlags struct {
	common

	listen     string
	consoleDir string
	origins    string
	tlsCert    string
	tlsKey     string
	publicURL  string
	blobDir    string
	logLevel   string
	logFormat  string
}

func (f *serveFlags) register(fs *flag.FlagSet) {
	f.common.register(fs)
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

// run opens the store, runs the start-up checks and serves until ctx is done.
func (f *serveFlags) run(ctx context.Context, stderr io.Writer) error {
	logger, err := f.loggerFor(stderr)
	if err != nil {
		return err
	}

	authn, err := identity.NewStaticTokens(strings.Split(os.Getenv("DUSTPAN_ADMIN_TOKEN"), ",")...)
	if err != nil {
		return fmt.Errorf("DUSTPAN_ADMIN_TOKEN: %w", err)
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

	blobs, err := openBlobs(f.database, f.blobDir)
	if err != nil {
		return err
	}

	defer func() {
		if closeErr := blobs.Close(); closeErr != nil {
			logger.WarnContext(ctx, "close blob store", slog.String("error", closeErr.Error()))
		}
	}()

	// The start-up check refuses a key-encryption key that matches none of
	// the wrapped keys in the database (ADR 23).
	if checkErr := secret.CheckWrapper(ctx, st, wrapper); checkErr != nil {
		return checkErr
	}

	srv, err := dustpan.NewServer(
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
	)
	if err != nil {
		return fmt.Errorf("configure server: %w", err)
	}

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}

// openBlobs creates the state content directory and opens it.
func openBlobs(database, flagDir string) (*blobfs.Store, error) {
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

func createNamespace(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var c common

	fs := flag.NewFlagSet("namespace create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	c.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 1 {
		return errUsage
	}

	name, err := namespace.ParseName(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("namespace name: %w", err)
	}

	st, err := openStore(ctx, c.database)
	if err != nil {
		return err
	}
	defer st.close()

	svc, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: namespace.Region(c.region)})
	if err != nil {
		return fmt.Errorf("namespace service: %w", err)
	}

	ns, err := svc.Create(ctx, name)
	if err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}

	if _, err := fmt.Fprintf(stdout, "created namespace %s in region %s\n", ns.Name, ns.HomeRegion); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
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
// the database holds no wrapped key.
func loadKeyEncryptionKey() ([]byte, error) {
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
