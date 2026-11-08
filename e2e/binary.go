//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/notify"
)

const (
	// serverEnv names the variant selector: "binary" runs the real
	// cmd/opendustpan binary with --tls-cert and --tls-key, anything else
	// runs the in-process server. The binary variant is the proof for the
	// milestone 2 exit test; the in-process TLS server stays for fast runs.
	serverEnv = "E2E_SERVER"
	// binaryServer names the proof variant.
	binaryServer = "binary"
	// binaryBuildTimeout bounds the `go build` of the server binary. A race
	// build on a 4-core CI runner, next to 16 parallel tests, can take more
	// than 5 minutes.
	binaryBuildTimeout = 15 * time.Minute
	// binaryStartTimeout bounds the wait for the backend's /health/live.
	binaryStartTimeout = 30 * time.Second
	// binaryStartPoll sets how often the wait checks /health/live.
	binaryStartPoll = 50 * time.Millisecond
	// backendCertLifetime sets how long the generated backend certificate lasts.
	backendCertLifetime = 24 * time.Hour
	// certSerialBits sets the size of the generated certificate serial.
	certSerialBits = 128
	// installationFlag names the installation flag serve takes.
	installationFlag = "--installation"
	// deltaCutoffFlag names the checkpoint delta cutoff flag serve takes.
	deltaCutoffFlag = "--checkpoint-delta-cutoff"
	// disableJournalingFlag names the flag that turns Pulumi journaling off.
	disableJournalingFlag = "--disable-pulumi-journaling"
	// disableJournalingEnv names the variable that turns Pulumi journaling
	// off on the server. The off backend sets it to true, so the run proves
	// the variable. It never inherits from the test process, so an on
	// backend stays on.
	disableJournalingEnv = "DUSTPAN_DISABLE_PULUMI_JOURNALING"
	// namespaceSubcommand names the namespace subcommand of the server binary.
	namespaceSubcommand = "namespace"
	// createSubcommand names the create verb of the namespace subcommand.
	createSubcommand = "create"
)

// UseBinaryServer reports whether the suite runs the real server binary.
// Set E2E_SERVER=binary for the proof run of the exit test. Several instances
// (E2E_INSTANCES above 1) are server binaries too.
func UseBinaryServer() bool {
	return os.Getenv(serverEnv) == binaryServer || instanceCount() > 1
}

// envInput groups what one environment needs, so the constructors take one
// argument next to the context.
type envInput struct {
	tb     testing.TB
	cli    Install
	store  Store
	dir    string
	token  string
	tokenB string
	rec    *Recorder
	// deltaCutoff is the checkpoint size in bytes from which the CLI sends
	// delta uploads. Zero hides the delta capability, so the CLI sends
	// whole checkpoints.
	deltaCutoff int
	// disableJournaling turns Pulumi journaling off on the server, so each
	// update answers journalVersion 0.
	disableJournaling bool
	// ipAllowlists turns IP allowlists on for the server and applies them to
	// the bootstrap admin token too.
	ipAllowlists bool
	// signIn turns on the console sign-in through a test OpenID provider,
	// in process and with the server binary.
	signIn bool
	// signInSecretInVariable passes the client secret of the sign-in in
	// DUSTPAN_OIDC_CLIENT_SECRET in place of DUSTPAN_OIDC_CLIENT_SECRET_FILE.
	signInSecretInVariable bool
	// signInRealTime runs the in-process server of a sign-in env on the
	// real time instead of a TestClock, for tests that need time to pass.
	// The server binary always keeps the real time.
	signInRealTime bool
	// passkeys gives the server a public URL with the host name localhost
	// in place of its address, because a passkey relying party ID is never an
	// IP address.
	passkeys bool
	// journalRefuser corrupts one outputs-only journal batch on the way to
	// the server, so the server refuses it and records the refusal. Nil
	// passes every batch through untouched.
	journalRefuser *JournalRefuser
	// journalCap lowers the journal version that start-update answers, so a
	// test can drive version 1 with a CLI that offers version 2. Nil answers
	// the negotiated version.
	journalCap *JournalVersionCap
	// sinkSenders replace the built-in notification sink senders of the
	// in-process server. The server binary has no such seam.
	sinkSenders []notify.Sender
	// issuer is a fake OpenID provider of a CI platform whose keys the
	// in-process server may read, on the loopback, or nil.
	issuer *OIDCIssuerFake
}

// backendConfig groups the settings of one backend process.
type backendConfig struct {
	bin          string
	dbURL        string
	blobDir      string
	blobStore    string
	s3Endpoint   string
	backupTarget string
	certFile     string
	keyFile      string
	tokens       string
	// deltaCutoff is the checkpoint delta cutoff serve runs with. serveArgs
	// always passes it explicitly, so an inherited
	// DUSTPAN_CHECKPOINT_DELTA_CUTOFF never changes a test backend.
	deltaCutoff int
	// disableJournaling passes --disable-pulumi-journaling with
	// DUSTPAN_DISABLE_PULUMI_JOURNALING=true when set, so an inherited
	// variable never changes a test backend.
	disableJournaling bool
	// ipAllowlists passes --ip-allowlists with
	// --ip-allowlists-include-bootstrap when set, so the binary server checks
	// the lists of every namespace, including for the bootstrap admin token.
	ipAllowlists bool
	// auditChain passes --audit-chain with this value when set, so a test
	// restarts the server with another setting of the audit chain.
	auditChain string
	// components passes --components with this value when set, so a test
	// runs servers that have only some components. Empty keeps the default,
	// which runs all of them.
	components string
	// signIn turns on the console sign-in through a test OpenID provider
	// when it is not nil.
	signIn *binarySignIn
	// publicURL passes --public-url with this value when it is set and signIn
	// is nil, so the provider registry of an OpenTofu run answers on the
	// host of the recording proxy. The sign-in passes its own public URL.
	publicURL string
	// auditSinks passes --audit-sink items, with the variables that hold
	// their secrets, when it is not nil. Nil starts the server with no
	// audit sink.
	auditSinks *AuditSinkSettings
}

// pemCertificate is the PEM block type of a certificate.
const pemCertificate = "CERTIFICATE"

// binarySignIn is the console sign-in of a binary backend. The server reads
// the client secret from secretFile (DUSTPAN_OIDC_CLIENT_SECRET_FILE), or
// from secret (DUSTPAN_OIDC_CLIENT_SECRET) when secretInVariable is set, and
// trusts the provider through caFile (SSL_CERT_FILE), so a run proves the
// flags and the environment variables of the sign-in.
type binarySignIn struct {
	publicURL        string
	issuer           string
	caFile           string
	secretFile       string
	secret           string
	secretInVariable bool
}

// args returns the serve arguments of the sign-in, or none for nil.
func (s *binarySignIn) args() []string {
	if s == nil {
		return nil
	}

	return []string{
		"--public-url", s.publicURL,
		"--oidc-issuer", s.issuer,
		"--oidc-provider", SignInProvider,
		"--oidc-display-name", "Corp",
		"--oidc-client-id", signInClientID,
	}
}

// env returns the environment of the sign-in, or none for nil.
func (s *binarySignIn) env() []string {
	if s == nil {
		return nil
	}

	if s.secretInVariable {
		return []string{"SSL_CERT_FILE=" + s.caFile, "DUSTPAN_OIDC_CLIENT_SECRET=" + s.secret}
	}

	return []string{"SSL_CERT_FILE=" + s.caFile, "DUSTPAN_OIDC_CLIENT_SECRET_FILE=" + s.secretFile}
}

// backendCert holds the certificate and key paths of one backend.
type backendCert struct {
	cert string
	key  string
}

// serverBinary builds cmd/opendustpan into dir and returns its path. Each
// environment builds its own copy, so parallel runs share nothing.
func serverBinary(ctx context.Context, tb testing.TB, dir string) string {
	tb.Helper()

	return buildBinary(ctx, tb, filepath.Join(dir, "opendustpan"), "")
}

// BuildBinaryWithVersion builds cmd/opendustpan into dir with its version
// stamped as version, for example v0.1.0, and returns its path. A test runs it
// as a client with RunBinaryPath to prove the rules that depend on a release
// version: the suite's own server binary is a dev build.
func BuildBinaryWithVersion(ctx context.Context, tb testing.TB, dir, version string) string {
	tb.Helper()

	return buildBinary(ctx, tb, filepath.Join(dir, "opendustpan-"+version), version)
}

// buildBinary builds cmd/opendustpan to out. A non-empty version is stamped
// into main.version.
func buildBinary(ctx context.Context, tb testing.TB, out, version string) string {
	tb.Helper()

	args := []string{"build", "-tags=" + serverBuildTags}
	if version != "" {
		args = append(args, "-ldflags", "-X main.version="+version)
	}

	args = append(args, "-o", out, "opendustpan.org/opendustpan/cmd/opendustpan")

	buildCtx, cancel := context.WithTimeout(ctx, binaryBuildTimeout)
	defer cancel()

	cmd := exec.CommandContext(buildCtx, "go", args...)

	var buildOut bytes.Buffer

	cmd.Stdout = &buildOut
	cmd.Stderr = &buildOut

	if err := cmd.Run(); err != nil {
		tb.Fatalf("build server binary: %v\n%s", err, buildOut.String())
	}

	return out
}

// writeBackendCert generates a self-signed certificate for loopback and
// writes it to dir. It returns the certificate and key paths.
func writeBackendCert(tb testing.TB, dir string) backendCert {
	tb.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("generate key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), certSerialBits))
	if err != nil {
		tb.Fatalf("generate serial: %v", err)
	}

	//nolint:exhaustruct_v5 // A test certificate sets only identity and validity; the rest stays zero.
	template := x509.Certificate{
		SerialNumber: serial,
		//nolint:exhaustruct_v5 // The test subject needs only a common name.
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(backendCertLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		tb.Fatalf("create certificate: %v", err)
	}

	//nolint:exhaustruct_v5 // A test PEM block carries no headers.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		tb.Fatalf("marshal key: %v", err)
	}

	//nolint:exhaustruct_v5 // A test PEM block carries no headers.
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	certFile := filepath.Join(dir, "backend-cert.pem")
	keyFile := filepath.Join(dir, "backend-key.pem")

	if err := os.WriteFile(certFile, certPEM, fileMode); err != nil {
		tb.Fatalf("write certificate: %v", err)
	}

	if err := os.WriteFile(keyFile, keyPEM, fileMode); err != nil {
		tb.Fatalf("write key: %v", err)
	}

	return backendCert{cert: certFile, key: keyFile}
}

// lockedBuffer is a goroutine-safe sink for the binary backend output:
// the child process writes from exec copy goroutines while the test polls
// for the listening line.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log buffer: %w", err)
	}

	return n, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// listeningPortFromLogs answers the loopback port in the listening log
// line the server writes after it binds on 127.0.0.1:0.
func listeningPortFromLogs(logs string) (string, bool) {
	const prefix = `"addr":"`

	if _, after, found := strings.Cut(logs, prefix); found {
		if addr, _, found := strings.Cut(after, `"`); found {
			if _, port, err := net.SplitHostPort(addr); err == nil {
				return port, true
			}
		}
	}

	const textPrefix = "addr="

	_, after, found := strings.Cut(logs, textPrefix)
	if !found {
		return "", false
	}

	rest := strings.TrimSpace(after)
	addr, _, _ := strings.Cut(rest, " ")

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false
	}

	return port, true
}

// boundPortFromLogs waits until logs hold the listening line and answers
// the bound port. It fails the test when the server never logs it.
func boundPortFromLogs(ctx context.Context, tb testing.TB, logs *lockedBuffer) string {
	tb.Helper()

	deadline := time.Now().Add(binaryStartTimeout)

	for time.Now().Before(deadline) {
		if port, ok := listeningPortFromLogs(logs.String()); ok {
			return port
		}

		timer := time.NewTimer(binaryStartPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			tb.Fatalf("context done while waiting for the server port\n%s", logs.String())
		case <-timer.C:
		}
	}

	tb.Fatalf("server binary did not log its bound port in time\n%s", logs.String())

	return ""
}

// backendEnv returns the backend process environment: the test tokens and
// key, over the current environment without the operator settings they
// replace. Other DUSTPAN_ settings are inherited. The S3 test credentials
// reach the binary as AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY only, never
// as a flag, so the binary reads them as operators set them.
func backendEnv(tokens string) []string {
	key := base64.StdEncoding.EncodeToString([]byte(testKeyEncryptionKey))

	var out []string

	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "DUSTPAN_ADMIN_TOKEN=") ||
			strings.HasPrefix(kv, "DUSTPAN_DATABASE=") ||
			strings.HasPrefix(kv, "DUSTPAN_KEY_ENCRYPTION_KEY=") ||
			strings.HasPrefix(kv, "DUSTPAN_KEY_ENCRYPTION_KEY_FILE=") ||
			strings.HasPrefix(kv, disableJournalingEnv+"=") ||
			strings.HasPrefix(kv, "AWS_ACCESS_KEY_ID=") ||
			strings.HasPrefix(kv, "AWS_SECRET_ACCESS_KEY=") {
			continue
		}

		out = append(out, kv)
	}

	out = append(out, "DUSTPAN_ADMIN_TOKEN="+tokens, "DUSTPAN_KEY_ENCRYPTION_KEY="+key)

	return append(out, s3CredentialEnv()...)
}

// backendServeEnv returns the environment for a `serve` child process: the
// backend environment with DUSTPAN_DATABASE set to dbURL. Serve always reads
// the database from the variable, because it refuses a password in the flag.
func backendServeEnv(tokens, dbURL string) []string {
	return append(backendEnv(tokens), "DUSTPAN_DATABASE="+dbURL)
}

// journalingServeEnv returns the journaling environment of a `serve` child
// process: DUSTPAN_DISABLE_PULUMI_JOURNALING=true when cfg turns journaling
// off, else nothing. The off backend proves the variable, next to the flag
// that serveArgs passes. The variable never inherits from the test process,
// so an on backend stays on.
func journalingServeEnv(cfg backendConfig) []string {
	if !cfg.disableJournaling {
		return nil
	}

	return []string{disableJournalingEnv + "=true"}
}

// s3CredentialEnv maps the S3 test credentials into the child environment.
// It returns nothing when they are unset, so directory runs inherit no S3
// settings. It never logs or reports the values: callers pass them to the
// child process only.
func s3CredentialEnv() []string {
	var out []string

	if access := os.Getenv(s3AccessEnv); access != "" {
		out = append(out, "AWS_ACCESS_KEY_ID="+access)
	}

	if secret := os.Getenv(s3SecretEnv); secret != "" {
		out = append(out, "AWS_SECRET_ACCESS_KEY="+secret)
	}

	return out
}

// runBinary runs the server binary to completion and fails the test when it
// exits non-zero.
func runBinary(ctx context.Context, tb testing.TB, bin string, args, env []string) {
	tb.Helper()

	//nolint:gosec // The binary is built from this repository by the suite itself.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		tb.Fatalf("run %s %s: %v\n%s", bin, strings.Join(args, " "), err, out.String())
	}
}

// BinaryResult is the outcome of one server binary invocation. A non-zero
// exit code is a result and not a test failure.
type BinaryResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// binaryHandle is one running binary backend: its process, loopback port,
// combined output and base URL.
type binaryHandle struct {
	cmd  *exec.Cmd
	port string
	logs *lockedBuffer
	url  string
}

// serveArgs returns the `serve` arguments for cfg on port. A backup target
// joins them only when the environment configures one: the binary creates
// the directory, so the test passes dir:<path> without creating it first.
// With an S3 blob store the arguments name --blob-store with --s3-endpoint
// instead of --blob-dir, so the server keeps its live blobs on the
// S3-compatible server. Credentials never join them: they travel in the
// environment only. The database URL never joins them either: serve reads it
// from DUSTPAN_DATABASE in the backend environment, because serve refuses a
// password in the flag.
// The delta cutoff always joins them explicitly, so the flag default never
// changes a test backend.
func serveArgs(cfg backendConfig, port string) []string {
	args := []string{
		"serve",
		"--listen", "127.0.0.1:" + port,
		"--tls-cert", cfg.certFile,
		"--tls-key", cfg.keyFile,
		installationFlag, installationName,
		deltaCutoffFlag, strconv.Itoa(cfg.deltaCutoff),
	}

	if cfg.disableJournaling {
		args = append(args, disableJournalingFlag)
	}

	if cfg.ipAllowlists {
		// The recording proxy on loopback fronts the binary, so the binary
		// trusts it to name the client address in X-Forwarded-For.
		args = append(args, "--ip-allowlists", "--ip-allowlists-include-bootstrap",
			"--trusted-proxies", "127.0.0.1/32")
	}

	if cfg.blobStore != "" {
		args = append(args, "--blob-store", cfg.blobStore, "--s3-endpoint", cfg.s3Endpoint)
	} else {
		args = append(args, "--blob-dir", cfg.blobDir)
	}

	if cfg.backupTarget != "" {
		args = append(args, "--backup-target", cfg.backupTarget)
	}

	if cfg.auditChain != "" {
		args = append(args, "--audit-chain", cfg.auditChain)
	}

	if cfg.components != "" {
		args = append(args, "--components", cfg.components)
	}

	if cfg.publicURL != "" && cfg.signIn == nil {
		args = append(args, "--public-url", cfg.publicURL)
	}

	args = append(args, cfg.auditSinks.args()...)

	return append(args, cfg.signIn.args()...)
}

// launchBinaryBackend starts `opendustpan serve` for cfg on port and waits
// until /health/live answers 204. A port of 0 means serve binds an
// ephemeral loopback port and the handle reports the bound port from the
// listening log, so no test closes a listener and binds its port again. A
// restart passes the bound port again, so the proxy target stays valid. It
// registers a cleanup that kills a child that still runs, and logs the
// process output.
func launchBinaryBackend(ctx context.Context, tb testing.TB, cfg backendConfig, port string) *binaryHandle {
	tb.Helper()

	cmd, out, err := spawnBinaryBackend(ctx, cfg, port)
	if err != nil {
		tb.Fatalf("%v", err)
	}

	tb.Cleanup(func() {
		_ = cmd.Process.Kill() //nolint:errcheck // The serve process ends with the kill signal.
		_ = cmd.Wait()         //nolint:errcheck // The killed process always reports an error.
		tb.Logf("server binary output:\n%s", out.String())
	})

	if port == "0" {
		port = boundPortFromLogs(ctx, tb, out)
	}

	backendURL := "https://127.0.0.1:" + port

	waitForBackend(ctx, tb, backendURL, out)

	return &binaryHandle{cmd: cmd, port: port, logs: out, url: backendURL}
}

// spawnBinaryBackend starts `opendustpan serve` for cfg on port and returns
// the running process with its combined output. It does not wait for the
// server to listen.
func spawnBinaryBackend(ctx context.Context, cfg backendConfig, port string) (*exec.Cmd, *lockedBuffer, error) {
	//nolint:gosec // The binary is built from this repository by the suite itself.
	cmd := exec.CommandContext(ctx, cfg.bin, serveArgs(cfg, port)...)
	cmd.Env = append(backendServeEnv(cfg.tokens, cfg.dbURL), cfg.signIn.env()...)
	cmd.Env = append(cmd.Env, journalingServeEnv(cfg)...)
	cmd.Env = append(cmd.Env, cfg.auditSinks.env()...)

	var out lockedBuffer

	cmd.Stdout = &out
	cmd.Stderr = &out
	// Set before Start: os/exec reads WaitDelay in its context watcher.
	cmd.WaitDelay = stepLogWaitDelay

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start server binary: %w", err)
	}

	return cmd, &out, nil
}

// startBinaryBackend starts `opendustpan serve` with TLS on an ephemeral
// loopback port and returns the handle and its base URL. It fails the test
// when /health/live never answers 204.
func startBinaryBackend(ctx context.Context, tb testing.TB, cfg backendConfig) (*binaryHandle, string) {
	tb.Helper()

	handle := launchBinaryBackend(ctx, tb, cfg, "0")

	return handle, handle.url
}

// DatabaseURL returns the --database value of the environment, so a test
// runs a command against the same database the server serves without
// rebuilding it.
func (e *Env) DatabaseURL(tb testing.TB) string {
	tb.Helper()

	if e.dbURL == "" {
		tb.Fatalf("DatabaseURL needs a database")
	}

	return e.dbURL
}

// BackupTarget returns the --backup-target value of the binary environment
// (dir:<path>), so a test runs backup check and restore against the same
// target the server backs up into. It needs E2E_SERVER=binary.
func (e *Env) BackupTarget(tb testing.TB) string {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("BackupTarget needs E2E_SERVER=binary")
	}

	return e.backendCfg.backupTarget
}

// BlobDir returns the --blob-dir value of the binary environment, so a test
// does not rebuild it for RunBinary. It needs E2E_SERVER=binary.
func (e *Env) BlobDir(tb testing.TB) string {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("BlobDir needs E2E_SERVER=binary")
	}

	return e.backendCfg.blobDir
}

// BackendURL returns the base URL of the binary backend itself, so a client
// command proves the API without going through the recording proxy. It needs
// E2E_SERVER=binary.
func (e *Env) BackendURL(tb testing.TB) string {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("BackendURL needs E2E_SERVER=binary")
	}

	return "https://127.0.0.1:" + e.backendPort
}

// BackendCertFile returns the certificate file of the binary backend, so a
// client command trusts it through --ca-file. It needs E2E_SERVER=binary.
func (e *Env) BackendCertFile(tb testing.TB) string {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("BackendCertFile needs E2E_SERVER=binary")
	}

	return e.backendCfg.certFile
}

// MigrateDatabase migrates the database at dbURL to the schema of the
// environment binary and creates the Namespace namespace in it. It starts a
// temporary server on dbURL so the server applies the migrations, creates the
// namespace through `opendustpan namespace create --server` with the
// environment tokens, then stops the server. It never opens the database from
// a command: only the server opens the data (ADR 39). It needs
// E2E_SERVER=binary.
func (e *Env) MigrateDatabase(ctx context.Context, tb testing.TB, dbURL string) {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("MigrateDatabase needs E2E_SERVER=binary")
	}

	dir := tb.TempDir()
	cert := writeBackendCert(tb, dir)
	blobDir := filepath.Join(dir, "blobs")

	cfg := backendConfig{
		disableJournaling: false,
		signIn:            nil,
		ipAllowlists:      false,
		bin:               e.binPath,
		dbURL:             dbURL,
		blobDir:           blobDir,
		blobStore:         "",
		s3Endpoint:        "",
		backupTarget:      "",
		certFile:          cert.cert,
		keyFile:           cert.key,
		tokens:            e.backendCfg.tokens,
		deltaCutoff:       0,
		auditChain:        "",
		components:        "",
		publicURL:         "",
		auditSinks:        nil,
	}

	handle := launchBinaryBackend(ctx, tb, cfg, "0")

	runBinary(ctx, tb, cfg.bin, []string{
		namespaceSubcommand, createSubcommand,
		"--server", handle.url,
		"--ca-file", cert.cert,
		Namespace,
	}, backendEnv(cfg.tokens))

	cmd := handle.cmd
	if cmd == nil || cmd.Process == nil {
		tb.Fatal("migration server is not running")
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		tb.Fatalf("stop migration server: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		tb.Fatalf("migration server did not exit with status 0: %v\n%s", err, handle.logs.String())
	}

	if code := cmd.ProcessState.ExitCode(); code != 0 {
		tb.Fatalf("migration server exits with status %d, want 0\n%s", code, handle.logs.String())
	}

	tb.Logf("migration server output:\n%s", handle.logs.String())
}

// BackupTargetDir returns the backup target directory of the server, in-process
// or binary, so a test can read or corrupt the target after a backup. A binary
// environment with no dir: target fails the test.
func (e *Env) BackupTargetDir(tb testing.TB) string {
	tb.Helper()

	if e.binPath == "" {
		if e.backupDir == "" {
			tb.Fatalf("BackupTargetDir: the in-process server has no backup target")
		}

		return e.backupDir
	}

	dir, ok := strings.CutPrefix(e.backendCfg.backupTarget, "dir:")
	if !ok || dir == "" {
		tb.Fatalf("BackupTargetDir: the server has no dir: backup target")
	}

	return dir
}

// RunBinary runs `opendustpan <args>` as a child process with the environment
// tokens. It reuses the binary that serverBinary built and the environment
// that backendEnv builds. Callers pass the flags the subcommand needs. In
// the in-process mode it builds the server binary first.
func (e *Env) RunBinary(ctx context.Context, tb testing.TB, args ...string) BinaryResult {
	tb.Helper()

	return e.RunBinaryWithEnv(ctx, tb, nil, args...)
}

// RunBinaryWithEnv runs `opendustpan <args>` as RunBinary does, with extra
// NAME=value pairs added to the child environment. An entry replaces the
// backend entry of the same name, so a test overrides the key-encryption key
// for a wrong-key restore without changing the server. In the in-process
// mode it builds the server binary first, so a step runs the real command
// against the database of the environment. It needs E2E_SERVER=binary for
// commands that talk to the server process itself.
func (e *Env) RunBinaryWithEnv(
	ctx context.Context, tb testing.TB, extra []string, args ...string,
) BinaryResult {
	tb.Helper()

	if e.binPath == "" {
		e.binPath = serverBinary(ctx, tb, tb.TempDir())
	}

	return e.runBinaryAt(ctx, tb, e.binPath, extra, args...)
}

// RunBinaryPath runs `<bin> <args>` with the environment of RunBinary: bin
// is another build of the binary, for example one from
// BuildBinaryWithVersion. It needs E2E_SERVER=binary.
func (e *Env) RunBinaryPath(ctx context.Context, tb testing.TB, bin string, args ...string) BinaryResult {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("RunBinaryPath needs E2E_SERVER=binary")
	}

	return e.runBinaryAt(ctx, tb, bin, nil, args...)
}

// runBinaryAt runs bin with args and the backend environment plus extra.
func (e *Env) runBinaryAt(ctx context.Context, tb testing.TB, bin string, extra []string, args ...string) BinaryResult {
	tb.Helper()

	//nolint:gosec // The binary is built from this repository by the suite itself.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = backendEnvWith(e.backendCfg.tokens, extra)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			tb.Fatalf("run binary %s: context done: %v", strings.Join(args, " "), err)
		}

		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return BinaryResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitErr.ExitCode()}
		}

		tb.Fatalf("start binary %s: %v", strings.Join(args, " "), err)
	}

	return BinaryResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
}

// backendEnvWith returns the backend process environment with extra applied:
// an entry replaces the backend entry of the same name, so overrides win.
func backendEnvWith(tokens string, extra []string) []string {
	base := backendEnv(tokens)

	replaced := map[string]bool{}

	for _, kv := range extra {
		name, _, _ := strings.Cut(kv, "=")
		replaced[name] = true
	}

	env := make([]string, 0, len(base)+len(extra))

	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")

		if !replaced[name] {
			env = append(env, kv)
		}
	}

	return append(env, extra...)
}

// StopServer ends the binary server with SIGTERM and checks that it exits
// with status 0. The process output goes to the test log. It needs
// E2E_SERVER=binary.
func (e *Env) StopServer(ctx context.Context, tb testing.TB) {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("StopServer needs E2E_SERVER=binary")
	}

	// With several instances the whole installation stops: a test that stops
	// the server does it to use the database alone.
	if e.ha != nil {
		e.ha.StopAll(tb)

		return
	}

	if e.backendCmd == nil || e.backendCmd.Process == nil {
		tb.Fatalf("StopServer: the server is not running")
	}

	cmd := e.backendCmd
	logs := e.backendLogs

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		tb.Fatalf("stop server: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			tb.Fatalf("context done while stopping the server: %v\n%s", err, logs.String())
		}

		tb.Fatalf("server did not exit with status 0: %v\n%s", err, logs.String())
	}

	tb.Logf("server binary output:\n%s", logs.String())

	if code := cmd.ProcessState.ExitCode(); code != 0 {
		tb.Fatalf("server exits with status %d, want 0\n%s", code, logs.String())
	}

	e.backendCmd = nil
}

// StartServer starts the same binary again on the same database, blob
// directory, certificate and port, and waits for GET /health/live to answer 204.
// It needs E2E_SERVER=binary.
func (e *Env) StartServer(ctx context.Context, tb testing.TB) {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("StartServer needs E2E_SERVER=binary")
	}

	if e.ha != nil {
		e.ha.StartAll(ctx, tb)

		return
	}

	if e.backendCmd != nil {
		tb.Fatalf("StartServer: the server still runs")
	}

	handle := launchBinaryBackend(ctx, tb, e.backendCfg, e.backendPort)
	e.backendCmd = handle.cmd
	e.backendLogs = handle.logs
}

// StartServerExpectingRefusal starts the binary server on the database at
// dbURL with the same settings as StartServer, and waits for the process to
// exit. It does not wait for GET /health/live, because the server must refuse
// the database. It returns the exit code and the combined output. It fails
// the test when the server still runs after commandTimeout, and kills it
// then. It needs E2E_SERVER=binary, and no server of the environment may run.
func (e *Env) StartServerExpectingRefusal(ctx context.Context, tb testing.TB, dbURL string) (int, string) {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("StartServerExpectingRefusal needs E2E_SERVER=binary")
	}

	if e.ha != nil {
		tb.Fatalf("StartServerExpectingRefusal does not run with several instances")
	}

	if e.backendCmd != nil {
		tb.Fatalf("StartServerExpectingRefusal: the server still runs")
	}

	cfg := e.backendCfg
	cfg.dbURL = dbURL

	cmd, out, err := spawnBinaryBackend(ctx, cfg, e.backendPort)
	if err != nil {
		tb.Fatalf("%v", err)
	}

	done := make(chan error, 1)

	go func() {
		done <- cmd.Wait()
	}()

	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()

	select {
	case waitErr := <-done:
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			tb.Fatalf("wait for server binary: %v\n%s", waitErr, out.String())
		}

		return cmd.ProcessState.ExitCode(), out.String()
	case <-timer.C:
		// The process may exit between the timer and the kill. Then the kill
		// reports that the process is done, and the wait below returns its status.
		if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			tb.Errorf("kill server binary: %v", killErr)
		}

		tb.Logf("server binary after kill: %v", <-done)

		tb.Fatalf("server binary still runs after %s, want a refusal\n%s", commandTimeout, out.String())

		return 0, ""
	}
}

// waitForBackend waits until the backend answers /health/live with 204.
func waitForBackend(ctx context.Context, tb testing.TB, backendURL string, logs *lockedBuffer) {
	tb.Helper()

	//nolint:gosec // The wait dials the test backend on loopback; there is no other party to impersonate.
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13},
		},
		Timeout: commandTimeout,
	}

	deadline := time.Now().Add(binaryStartTimeout)

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, backendURL+"/health/live", http.NoBody)
		if err != nil {
			tb.Fatalf("build health request: %v", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close() //nolint:errcheck // The health body is empty; only the status matters.

			if resp.StatusCode == http.StatusNoContent {
				return
			}
		}

		timer := time.NewTimer(binaryStartPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			tb.Fatalf("context done while waiting for the server binary\n%s", logs.String())
		case <-timer.C:
		}
	}

	tb.Fatalf("server binary did not start in time\n%s", logs.String())
}

// binarySignInSetup starts the test provider of a binary env with the
// console sign-in, and sets the sign-in of cfg. It returns nil, and leaves
// cfg as it is, for an env with no sign-in.
func binarySignInSetup(tb testing.TB, in envInput, proxy *recordingProxy, cfg *backendConfig) *signInSetup {
	tb.Helper()

	if !in.signIn {
		return nil
	}

	s := newSignInSetup(tb, proxy.publicURL(), nil)
	cfg.signIn = writeBinarySignIn(tb, in.dir, s, in.signInSecretInVariable)

	return s
}

// startRecordingProxy fronts the backend with a TLS server that records
// every request, so binary runs keep the same request log as in-process
// runs. It keeps the client-facing host, so absolute URLs the backend
// reports point back through the proxy.
func startRecordingProxy(
	tb testing.TB, rec *Recorder, backendURL string, refuser *JournalRefuser,
) *httptest.Server {
	tb.Helper()

	p := reserveRecordingProxy(tb, rec, refuser, nil)

	return p.start(tb, backendURL)
}

// recordingProxy is a recording proxy whose address is known before its
// backend: the sign-in needs the public URL of the proxy at the start of
// the backend.
type recordingProxy struct {
	server *httptest.Server
	target *url.URL
}

// publicURL returns the URL that the proxy serves once it starts.
func (p *recordingProxy) publicURL() string {
	return "https://" + p.server.Listener.Addr().String()
}

// start points the proxy at backendURL and starts it.
func (p *recordingProxy) start(tb testing.TB, backendURL string) *httptest.Server {
	tb.Helper()

	target, err := url.Parse(backendURL)
	if err != nil {
		tb.Fatalf("parse backend URL: %v", err)
	}

	*p.target = *target

	p.server.StartTLS()

	return p.server
}

// reserveRecordingProxy binds the listener of a recording proxy and
// returns it, not started.
func reserveRecordingProxy(
	tb testing.TB, rec *Recorder, refuser *JournalRefuser, versionCap *JournalVersionCap,
) *recordingProxy {
	tb.Helper()

	target := &url.URL{}

	//nolint:gosec // The proxy dials the test backend on loopback; there is no other party to impersonate.
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}

	//nolint:exhaustruct_v5 // The test proxy needs only Rewrite and Transport; the rest stays zero.
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host
			// The backend reads the client address of a request from this
			// header, when it trusts the proxy (--trusted-proxies).
			r.SetXForwarded()
		},
		Transport: transport,
	}

	ts := httptest.NewUnstartedServer(rec.Middleware(cappedHandler(faultedHandler(proxy, refuser), versionCap)))
	tb.Cleanup(ts.Close)

	return &recordingProxy{server: ts, target: target}
}

// binaryDatabase returns the --database value of the database the backend
// serves, with the PostgreSQL proxy. The test process opens no store on it:
// the binary owns the database and migrates it, and the seed helpers write
// through the API. On SQLite the proxy is nil and the binary creates the
// file. On PostgreSQL the backend reaches a fresh schema through a TCP proxy.
func binaryDatabase(ctx context.Context, tb testing.TB, store Store, dir string) (string, *DBProxy) {
	tb.Helper()

	switch store {
	case StoreSQLite:
		return "sqlite:" + filepath.Join(dir, "dustpan.db"), nil
	case StorePostgres:
		proxy, proxiedURL := startDBProxy(ctx, tb, newPostgresSchema(ctx, tb, PostgresURL()))

		return proxiedURL, proxy
	default:
		tb.Fatalf("unknown store: %s", store)

		return "", nil
	}
}

// blobSetup holds the live blob store handle of one binary environment with
// the backend settings for it.
type blobSetup struct {
	blobs        blob.Store
	blobName     string
	blobDir      string
	blobStore    string
	s3Endpoint   string
	backupTarget string
}

// newBlobSetup opens the live blob store handle of one binary environment
// with the backend settings for it: an S3-compatible store under a unique
// prefix with E2E_BLOBS=s3, else the state content directory. The S3 backup
// target shares the run prefix, so the backup scenario restores from an S3
// target. The backup handle carries only the cleanup that removes the backup
// keys at the end of the run.
func newBlobSetup(ctx context.Context, tb testing.TB, dir string) blobSetup {
	tb.Helper()

	if UseS3Blobs(tb) {
		cfg := RequireS3Config(tb)
		root := newS3RunPrefix(tb)

		blobs := openS3Store(ctx, tb, cfg, root+"/blobs")
		_ = openS3Store(ctx, tb, cfg, root+"/backup")

		return blobSetup{
			blobs:        blobs,
			blobName:     blobS3,
			blobDir:      filepath.Join(dir, "blobs"),
			blobStore:    "s3://" + cfg.bucket + "/" + root + "/blobs",
			s3Endpoint:   cfg.endpoint,
			backupTarget: "s3://" + cfg.bucket + "/" + root + "/backup",
		}
	}

	blobDir := filepath.Join(dir, "blobs")

	return blobSetup{
		blobs:        openBlobStoreAt(tb, blobDir),
		blobName:     blobFS,
		blobDir:      blobDir,
		blobStore:    "",
		s3Endpoint:   "",
		backupTarget: "dir:" + filepath.Join(dir, "backup-target"),
	}
}

// binaryBackendConfig returns the backend settings of the binary env of
// in, with no sign-in.
func binaryBackendConfig(in envInput, bin, dbURL string, setup blobSetup, cert backendCert) backendConfig {
	return backendConfig{
		bin:               bin,
		dbURL:             dbURL,
		blobDir:           setup.blobDir,
		blobStore:         setup.blobStore,
		s3Endpoint:        setup.s3Endpoint,
		backupTarget:      setup.backupTarget,
		certFile:          cert.cert,
		keyFile:           cert.key,
		tokens:            in.token + "," + in.tokenB,
		deltaCutoff:       in.deltaCutoff,
		disableJournaling: in.disableJournaling,
		ipAllowlists:      in.ipAllowlists,
		auditChain:        "",
		components:        "",
		signIn:            nil,
		publicURL:         "",
		auditSinks:        nil,
	}
}

// newBinaryEnv starts the real server binary with TLS behind a recording
// proxy and returns the environment. The CLI trusts the proxy through
// SSL_CERT_FILE, as with the in-process TLS server. A new installation holds
// no namespace: the first one is created with `opendustpan namespace create
// --server` through the installation API, so the command as a client is
// proven before any CLI logs in.
func newBinaryEnv(ctx context.Context, in envInput) *Env {
	tb := in.tb
	tb.Helper()

	if n := instanceCount(); n > 1 {
		return newHABinaryEnv(ctx, in, n)
	}

	bin := serverBinary(ctx, tb, in.dir)
	dbURL, dbProxy := binaryDatabase(ctx, tb, in.store, in.dir)
	setup := newBlobSetup(ctx, tb, in.dir)
	cfg := binaryBackendConfig(in, bin, dbURL, setup, writeBackendCert(tb, in.dir))

	reserved := reserveRecordingProxy(tb, in.rec, in.journalRefuser, in.journalCap)
	signIn := binarySignInSetup(tb, in, reserved, &cfg)
	cfg.publicURL = binaryPublicURL(in, reserved)

	handle, backendURL := startBinaryBackend(ctx, tb, cfg)

	createFirstNamespaceViaBinary(ctx, tb, cfg, backendURL)

	proxy := reserved.start(tb, backendURL)

	client := proxy.Client()
	client.Timeout = commandTimeout

	env := &Env{
		URL:           proxy.URL,
		Recorder:      in.rec,
		cli:           in.cli,
		store:         in.store,
		client:        client,
		inprocess:     nil,
		blobs:         setup.blobs,
		blobName:      setup.blobName,
		s3Endpoint:    setup.s3Endpoint,
		token:         in.token,
		tokenB:        in.tokenB,
		memberToken:   "",
		home:          filepath.Join(in.dir, "home"),
		project:       filepath.Join(in.dir, "project"),
		remoteProject: "",
		steps:         nil,
		skipped:       nil,
		tls:           true,
		binPath:       bin,
		backupDir:     "",
		backendCfg:    cfg,
		backendPort:   handle.port,
		backendCmd:    handle.cmd,
		backendLogs:   handle.logs,
		ha:            nil,
		certBundle:    "",
		dbProxy:       dbProxy,
		dbURL:         cfg.dbURL,
		signIn:        signIn,
		claimers:      nil,
	}

	prepareBinaryEnvDirs(tb, env, proxy)

	// The command made the first namespace, so the harness adds the catch-all
	// placement rule that the in-process server gets from the API create.
	env.SeedCatchAllPlacement(ctx, tb, Namespace)

	if in.store == StoreSQLite {
		dbPath := strings.TrimPrefix(dbURL, "sqlite:")
		checkNoOwnSQLiteHandle(tb, dbPath)
		// Cleanups run last in, first out, so this one runs while the
		// binary still serves: it catches a handle that a step opened.
		tb.Cleanup(func() { checkNoOwnSQLiteHandle(tb, dbPath) })
	}

	return env
}

// newHABinaryEnv starts n server binaries of one installation on one
// database and one blob store, behind a round-robin TLS proxy that records
// the instance of each request, and returns the environment. The first
// instance migrates the database and creates the first namespace through the
// installation API.
func newHABinaryEnv(ctx context.Context, in envInput, n int) *Env {
	tb := in.tb
	tb.Helper()

	if in.store != StorePostgres {
		tb.Fatalf("E2E_INSTANCES above 1 needs PostgreSQL: SQLite holds one instance only")
	}

	bin := serverBinary(ctx, tb, in.dir)
	dbURL, dbProxy := binaryDatabase(ctx, tb, in.store, in.dir)
	setup := newBlobSetup(ctx, tb, in.dir)
	cfg := binaryBackendConfig(in, bin, dbURL, setup, writeBackendCert(tb, in.dir))

	cluster := registerHACluster(tb, cfg, n)
	proxy := reserveHAProxy(tb, in.rec, in.journalRefuser, in.journalCap, cluster)
	reserved := &recordingProxy{server: proxy, target: &url.URL{}}
	signIn := binarySignInSetup(tb, in, reserved, &cfg)

	// Every instance serves the public URL of the proxy, as the instances
	// behind one load balancer do.
	if signIn == nil {
		cfg.publicURL = reserved.publicURL()
	}

	cluster.cfg = cfg

	if err := cluster.bootAll(ctx); err != nil {
		tb.Fatalf("start the HA cluster: %v", err)
	}

	createFirstNamespaceViaBinary(ctx, tb, cfg, cluster.URL(0))

	proxy.StartTLS()

	client := proxy.Client()
	client.Timeout = commandTimeout
	env := &Env{
		URL:           proxy.URL,
		Recorder:      in.rec,
		cli:           in.cli,
		store:         in.store,
		client:        client,
		inprocess:     nil,
		blobs:         setup.blobs,
		blobName:      setup.blobName,
		s3Endpoint:    setup.s3Endpoint,
		token:         in.token,
		tokenB:        in.tokenB,
		memberToken:   "",
		home:          filepath.Join(in.dir, "home"),
		project:       filepath.Join(in.dir, "project"),
		remoteProject: "",
		steps:         nil,
		skipped:       nil,
		tls:           true,
		binPath:       bin,
		backupDir:     "",
		backendCfg:    cfg,
		backendPort:   "",
		backendCmd:    nil,
		backendLogs:   nil,
		ha:            cluster,
		certBundle:    "",
		dbProxy:       dbProxy,
		dbURL:         cfg.dbURL,
		signIn:        signIn,
		claimers:      nil,
	}

	prepareBinaryEnvDirs(tb, env, proxy)
	env.SeedCatchAllPlacement(ctx, tb, Namespace)

	return env
}

// prepareBinaryEnvDirs creates the home and project directories of a binary
// environment and writes its CLI project files. It keeps newBinaryEnv short.
func prepareBinaryEnvDirs(tb testing.TB, env *Env, proxy *httptest.Server) {
	tb.Helper()

	for _, d := range []string{env.home, env.project} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			tb.Fatalf("create %s: %v", d, err)
		}
	}

	env.write(tb, filepath.Join(env.project, "Pulumi.yaml"), pulumiProject)
	env.writeTofuFiles(tb, proxy)
}

// createFirstNamespaceViaBinary creates the first namespace on an empty
// installation with `opendustpan namespace create --server` through the API.
// The command trusts the backend through its certificate file, so the run
// proves the command as a client with the bootstrap admin token.
func createFirstNamespaceViaBinary(ctx context.Context, tb testing.TB, cfg backendConfig, backendURL string) {
	tb.Helper()

	runBinary(ctx, tb, cfg.bin, []string{
		namespaceSubcommand, createSubcommand,
		"--server", backendURL,
		"--ca-file", cfg.certFile,
		Namespace,
	}, backendEnv(cfg.tokens))
}
