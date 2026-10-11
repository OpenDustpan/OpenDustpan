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
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// serverEnv names the variant selector: "binary" runs the real
	// cmd/opendustpan binary with --tls-cert and --tls-key, anything else
	// runs the in-process server. The binary variant is the proof for the
	// milestone 2 exit test; the in-process TLS server stays for fast runs.
	serverEnv = "E2E_SERVER"
	// binaryServer names the proof variant.
	binaryServer = "binary"
	// binaryBuildTimeout bounds the `go build` of the server binary.
	binaryBuildTimeout = 5 * time.Minute
	// binaryStartTimeout bounds the wait for the backend's /health/live.
	binaryStartTimeout = 30 * time.Second
	// binaryStartPoll sets how often the wait checks /health/live.
	binaryStartPoll = 50 * time.Millisecond
	// backendCertLifetime sets how long the generated backend certificate lasts.
	backendCertLifetime = 24 * time.Hour
	// certSerialBits sets the size of the generated certificate serial.
	certSerialBits = 128
	// databaseFlag names the database flag every serve and namespace
	// command takes.
	databaseFlag = "--database"
	// regionFlag names the region flag every serve and namespace command
	// takes.
	regionFlag = "--region"
)

// UseBinaryServer reports whether the suite runs the real server binary.
// Set E2E_SERVER=binary for the proof run of the exit test.
func UseBinaryServer() bool {
	return os.Getenv(serverEnv) == binaryServer
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
}

// backendConfig groups the settings of one backend process.
type backendConfig struct {
	bin          string
	dbURL        string
	blobDir      string
	backupTarget string
	certFile     string
	keyFile      string
	tokens       string
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

	out := filepath.Join(dir, "opendustpan")

	buildCtx, cancel := context.WithTimeout(ctx, binaryBuildTimeout)
	defer cancel()

	//nolint:gosec // The suite builds its own server from this repository.
	cmd := exec.CommandContext(
		buildCtx,
		"go",
		"build",
		"-o",
		out,
		"opendustpan.org/opendustpan/cmd/opendustpan",
	)

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
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

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

// freeBackendPort returns an unused loopback port for the backend.
func freeBackendPort(ctx context.Context, tb testing.TB) string {
	tb.Helper()

	//nolint:exhaustruct_v5 // The probe listens with defaults; only the port matters.
	lc := &net.ListenConfig{}

	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}

	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		tb.Fatalf("split address: %v", err)
	}

	if err := l.Close(); err != nil {
		tb.Fatalf("close listener: %v", err)
	}

	return port
}

// backendEnv returns the backend process environment: the test tokens and
// key, over the current environment without the operator settings they
// replace. Other DUSTPAN_ settings are inherited.
func backendEnv(tokens string) []string {
	key := base64.StdEncoding.EncodeToString([]byte(testKeyEncryptionKey))

	var out []string

	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "DUSTPAN_ADMIN_TOKEN=") ||
			strings.HasPrefix(kv, "DUSTPAN_KEY_ENCRYPTION_KEY=") ||
			strings.HasPrefix(kv, "DUSTPAN_KEY_ENCRYPTION_KEY_FILE=") {
			continue
		}

		out = append(out, kv)
	}

	return append(out, "DUSTPAN_ADMIN_TOKEN="+tokens, "DUSTPAN_KEY_ENCRYPTION_KEY="+key)
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
	logs *bytes.Buffer
	url  string
}

// serveArgs returns the `serve` arguments for cfg on port. A backup target
// joins them only when the environment configures one: the binary creates
// the directory, so the test passes dir:<path> without creating it first.
func serveArgs(cfg backendConfig, port string) []string {
	args := []string{
		"serve",
		databaseFlag, cfg.dbURL,
		"--listen", "127.0.0.1:" + port,
		"--tls-cert", cfg.certFile,
		"--tls-key", cfg.keyFile,
		"--blob-dir", cfg.blobDir,
		regionFlag, region,
	}

	if cfg.backupTarget != "" {
		args = append(args, "--backup-target", cfg.backupTarget)
	}

	return args
}

// launchBinaryBackend starts `opendustpan serve` for cfg on port and waits
// until /health/live answers 204. It registers a cleanup that kills a child that
// still runs, and logs the process output.
func launchBinaryBackend(ctx context.Context, tb testing.TB, cfg backendConfig, port string) *binaryHandle {
	tb.Helper()

	//nolint:gosec // The binary is built from this repository by the suite itself.
	cmd := exec.CommandContext(ctx, cfg.bin, serveArgs(cfg, port)...)
	cmd.Env = backendEnv(cfg.tokens)

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		tb.Fatalf("start server binary: %v", err)
	}

	tb.Cleanup(func() {
		_ = cmd.Process.Kill() //nolint:errcheck // The serve process ends with the kill signal.
		_ = cmd.Wait()         //nolint:errcheck // The killed process always reports an error.
		tb.Logf("server binary output:\n%s", out.String())
	})

	backendURL := "https://127.0.0.1:" + port

	waitForBackend(ctx, tb, backendURL, &out)

	return &binaryHandle{cmd: cmd, port: port, logs: &out, url: backendURL}
}

// startBinaryBackend picks a loopback port, starts `opendustpan serve` with
// TLS on it and returns the handle and its base URL. It fails the test when
// /health/live never answers 204.
func startBinaryBackend(ctx context.Context, tb testing.TB, cfg backendConfig) (*binaryHandle, string) {
	tb.Helper()

	port := freeBackendPort(ctx, tb)
	handle := launchBinaryBackend(ctx, tb, cfg, port)

	return handle, handle.url
}

// DatabaseURL returns the --database value of the binary environment, so a
// test does not rebuild it for RunBinary. It needs E2E_SERVER=binary.
func (e *Env) DatabaseURL(tb testing.TB) string {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("DatabaseURL needs E2E_SERVER=binary")
	}

	return e.backendCfg.dbURL
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

// RunBinary runs `opendustpan <args>` as a child process against the database
// and the blob directory of the environment. It reuses the binary that
// serverBinary built and the environment that backendEnv builds. Callers pass
// the --database and --blob-dir flags the subcommand needs; DatabaseURL and
// BlobDir offer the values. It needs E2E_SERVER=binary.
func (e *Env) RunBinary(ctx context.Context, tb testing.TB, args ...string) BinaryResult {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("RunBinary needs E2E_SERVER=binary")
	}

	//nolint:gosec // The binary is built from this repository by the suite itself.
	cmd := exec.CommandContext(ctx, e.binPath, args...)
	cmd.Env = backendEnv(e.backendCfg.tokens)

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

// StopServer ends the binary server with SIGTERM and checks that it exits
// with status 0. The process output goes to the test log. It needs
// E2E_SERVER=binary.
func (e *Env) StopServer(ctx context.Context, tb testing.TB) {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("StopServer needs E2E_SERVER=binary")
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

	if e.backendCmd != nil {
		tb.Fatalf("StartServer: the server still runs")
	}

	handle := launchBinaryBackend(ctx, tb, e.backendCfg, e.backendPort)
	e.backendCmd = handle.cmd
	e.backendLogs = handle.logs
}

// waitForBackend waits until the backend answers /health/live with 204.
func waitForBackend(ctx context.Context, tb testing.TB, backendURL string, logs *bytes.Buffer) {
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

// startRecordingProxy fronts the backend with a TLS server that records
// every request, so binary runs keep the same request log as in-process
// runs. It keeps the client-facing host, so absolute URLs the backend
// reports point back through the proxy.
func startRecordingProxy(tb testing.TB, rec *Recorder, backendURL string) *httptest.Server {
	tb.Helper()

	target, err := url.Parse(backendURL)
	if err != nil {
		tb.Fatalf("parse backend URL: %v", err)
	}

	//nolint:gosec // The proxy dials the test backend on loopback; there is no other party to impersonate.
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}

	//nolint:exhaustruct_v5 // The test proxy needs only Rewrite and Transport; the rest stays zero.
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host
		},
		Transport: transport,
	}

	ts := httptest.NewUnstartedServer(rec.Middleware(proxy))
	ts.StartTLS()
	tb.Cleanup(ts.Close)

	return ts
}

// openBinaryStore opens a direct store handle on the database the backend
// serves, so seed helpers keep working. It returns the --database value for
// the backend and the handle.
//
//nolint:ireturn // SQLite and PostgreSQL stores share no concrete type;
func openBinaryStore(
	ctx context.Context, tb testing.TB, store Store, dir string,
) (string, serverStore) {
	tb.Helper()

	switch store {
	case StoreSQLite:
		dbPath := filepath.Join(dir, "dustpan.db")

		return "sqlite:" + dbPath, openSQLiteStore(ctx, tb, dbPath)
	case StorePostgres:
		base := PostgresURL()
		if base == "" {
			tb.Fatalf("set %s to run the PostgreSQL suite", postgresURLEnv)
		}

		st, schemaURL := openPostgresStoreWithURL(ctx, tb, base)

		return schemaURL, st
	default:
		tb.Fatalf("unknown store: %s", store)

		return "", nil
	}
}

// newBinaryEnv starts the real server binary with TLS behind a recording
// proxy and returns the environment. The CLI trusts the proxy through
// SSL_CERT_FILE, as with the in-process TLS server.
func newBinaryEnv(ctx context.Context, in envInput) *Env {
	tb := in.tb
	tb.Helper()

	bin := serverBinary(ctx, tb, in.dir)
	cert := writeBackendCert(tb, in.dir)
	blobDir := filepath.Join(in.dir, "blobs")
	dbURL, st := openBinaryStore(ctx, tb, in.store, in.dir)
	blobs := openBlobStoreAt(tb, blobDir)

	runBinary(ctx, tb, bin, []string{
		"namespace", "create",
		"--database", dbURL,
		"--region", region,
		Namespace,
	}, os.Environ())

	cfg := backendConfig{
		bin:          bin,
		dbURL:        dbURL,
		blobDir:      blobDir,
		backupTarget: "dir:" + filepath.Join(in.dir, "backup-target"),
		certFile:     cert.cert,
		keyFile:      cert.key,
		tokens:       in.token + "," + in.tokenB,
	}
	handle, backendURL := startBinaryBackend(ctx, tb, cfg)
	proxy := startRecordingProxy(tb, in.rec, backendURL)

	client := proxy.Client()
	client.Timeout = commandTimeout

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: region})
	if err != nil {
		tb.Fatalf("namespace service: %v", err)
	}

	env := &Env{
		URL:           proxy.URL,
		Recorder:      in.rec,
		cli:           in.cli,
		store:         in.store,
		server:        st,
		client:        client,
		workspaces:    newWorkspaceService(tb, st, blobs, namespaces),
		token:         in.token,
		tokenB:        in.tokenB,
		home:          filepath.Join(in.dir, "home"),
		project:       filepath.Join(in.dir, "project"),
		remoteProject: "",
		steps:         nil,
		tls:           true,
		binPath:       bin,
		backendCfg:    cfg,
		backendPort:   handle.port,
		backendCmd:    handle.cmd,
		backendLogs:   handle.logs,
		certBundle:    "",
	}

	for _, d := range []string{env.home, env.project} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			tb.Fatalf("create %s: %v", d, err)
		}
	}

	env.write(tb, filepath.Join(env.project, "Pulumi.yaml"), pulumiProject)
	env.writeTofuFiles(tb, proxy)

	return env
}
