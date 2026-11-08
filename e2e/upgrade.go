//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// oldServeTimeout bounds one start attempt of the previous tag binary
	// on the upgraded database: a refusal exits at once, a start without
	// the check answers /health/live.
	oldServeTimeout = 15 * time.Second
	// gitTimeout bounds a read-only git lookup of the repository.
	gitTimeout = 30 * time.Second
)

// BuildCurrentBinary builds cmd/opendustpan from the working tree into dir
// and returns its path, with the same flags the environments use.
func BuildCurrentBinary(ctx context.Context, tb testing.TB, dir string) string {
	tb.Helper()

	return serverBinary(ctx, tb, dir)
}

// BuildTagBinary builds cmd/opendustpan at tag into dir and returns its
// path, with the same flags as the current binary. It exports the tag with
// `git archive` into a temporary directory, so the working tree and the
// index never change.
func BuildTagBinary(ctx context.Context, tb testing.TB, tag, dir string) string {
	tb.Helper()

	srcDir := filepath.Join(dir, "src")

	if err := os.MkdirAll(srcDir, dirMode); err != nil {
		tb.Fatalf("create tag source dir: %v", err)
	}

	exportTag(ctx, tb, tag, srcDir)

	out := filepath.Join(dir, "opendustpan-prev")
	compileTagSource(ctx, tb, srcDir, out)

	return out
}

// GitTopLevel returns the top level of the repository the test runs in.
// It only reads, so the working tree and the index never change.
func GitTopLevel(ctx context.Context, tb testing.TB) string {
	tb.Helper()

	buildCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(buildCtx, "git", "rev-parse", "--show-toplevel")

	out, err := cmd.Output()
	if err != nil {
		tb.Fatalf("git rev-parse --show-toplevel: %v", err)
	}

	return strings.TrimSpace(string(out))
}

// exportTag writes `git archive <tag>` into srcDir. Archive never touches
// the working tree or the index. It runs at the top level of the
// repository: the test binary runs in the package directory, and from there
// `git archive` would export only that directory.
func exportTag(ctx context.Context, tb testing.TB, tag, srcDir string) {
	tb.Helper()

	buildCtx, cancel := context.WithTimeout(ctx, binaryBuildTimeout)
	defer cancel()

	//nolint:gosec // The tag comes from git describe of this repository.
	archive := exec.CommandContext(buildCtx, "git", "archive", "--format=tar", tag)
	archive.Dir = GitTopLevel(ctx, tb)

	var exported bytes.Buffer

	archive.Stdout = &exported
	archive.Stderr = &exported

	if err := archive.Run(); err != nil {
		tb.Fatalf("git archive %s: %v\n%s", tag, err, exported.String())
	}

	//nolint:gosec // Tar extracts the archive exported above into a temporary directory.
	untar := exec.CommandContext(buildCtx, "tar", "-x", "-C", srcDir)
	untar.Stdin = &exported

	var untarOut bytes.Buffer

	untar.Stdout = &untarOut
	untar.Stderr = &untarOut

	if err := untar.Run(); err != nil {
		tb.Fatalf("extract tag %s: %v\n%s", tag, err, untarOut.String())
	}
}

// compileTagSource compiles the exported source at srcDir into out.
func compileTagSource(ctx context.Context, tb testing.TB, srcDir, out string) {
	tb.Helper()

	buildCtx, cancel := context.WithTimeout(ctx, binaryBuildTimeout)
	defer cancel()

	//nolint:gosec // The suite builds its own server from the exported tag.
	cmd := exec.CommandContext(
		buildCtx,
		"go",
		"build",
		"-o",
		out,
		"opendustpan.org/opendustpan/cmd/opendustpan",
	)
	cmd.Dir = srcDir

	var buildOut bytes.Buffer

	cmd.Stdout = &buildOut
	cmd.Stderr = &buildOut

	if err := cmd.Run(); err != nil {
		tb.Fatalf("build server binary at tag: %v\n%s", err, buildOut.String())
	}
}

// NewEnvWithBinary starts the server binary at binPath on a fresh database
// with the Namespace namespace, and prepares an empty home directory and a
// scratch project. The namespace is created through the server API, as NewEnv
// does, so the create flags are those that binPath accepts. Unlike NewEnv it
// never opens the database with the current store code, so an older binary
// migrates the database to its own schema first. The server field stays nil:
// seed helpers that need a direct store handle do not work on this
// environment, while CLI steps, API calls, RunBinary, StopServer and
// StartServer work as usual.
func NewEnvWithBinary(ctx context.Context, tb testing.TB, cli Install, store Store, binPath string) *Env {
	tb.Helper()

	dir := tb.TempDir()
	token := newToken(tb)
	tokenB := newToken(tb)
	rec := &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}}
	in := envInput{
		tb:                     tb,
		cli:                    cli,
		store:                  store,
		dir:                    dir,
		token:                  token,
		tokenB:                 tokenB,
		rec:                    rec,
		deltaCutoff:            0,
		sinkSenders:            nil,
		issuer:                 nil,
		disableJournaling:      false,
		ipAllowlists:           false,
		signIn:                 false,
		signInSecretInVariable: false,
		signInRealTime:         false,
		passkeys:               false,
		journalRefuser:         nil,
		journalCap:             nil,
	}

	return newBinaryEnvWithBin(ctx, in, binPath)
}

// plainBackendConfig returns the backend settings of a binary environment
// with no blob store, no backup target and every feature switch off.
func plainBackendConfig(binPath, dbURL, blobDir, tokens string, cert backendCert) backendConfig {
	return backendConfig{
		signIn:            nil,
		disableJournaling: false,
		ipAllowlists:      false,
		bin:               binPath,
		dbURL:             dbURL,
		blobDir:           blobDir,
		blobStore:         "",
		s3Endpoint:        "",
		backupTarget:      "",
		certFile:          cert.cert,
		keyFile:           cert.key,
		tokens:            tokens,
		deltaCutoff:       0,
		auditChain:        "",
		components:        "",
		publicURL:         "",
		auditSinks:        nil,
	}
}

// reserveDatabase returns a fresh database URL without opening it: a
// temporary SQLite path, or a fresh PostgreSQL schema that is dropped when
// the test ends.
func reserveDatabase(ctx context.Context, tb testing.TB, store Store, dir string) string {
	tb.Helper()

	switch store {
	case StoreSQLite:
		return "sqlite:" + filepath.Join(dir, "dustpan.db")
	case StorePostgres:
		base := PostgresURL()
		if base == "" {
			tb.Fatalf("set %s to run the PostgreSQL suite", postgresURLEnv)
		}

		schema := randomSchema(tb)
		createSchema(ctx, tb, base, schema)
		tb.Cleanup(func() { dropSchema(context.WithoutCancel(ctx), tb, base, schema) })

		return schemaURL(tb, base, schema)
	default:
		tb.Fatalf("unknown store: %s", store)

		return ""
	}
}

// newBinaryEnvWithBin starts binPath on a reserved database and returns the
// environment. The binary itself creates the namespace and migrates the
// database.
func newBinaryEnvWithBin(ctx context.Context, in envInput, binPath string) *Env {
	tb := in.tb
	tb.Helper()

	cert := writeBackendCert(tb, in.dir)
	blobDir := filepath.Join(in.dir, "blobs")
	dbURL := reserveDatabase(ctx, tb, in.store, in.dir)

	cfg := plainBackendConfig(binPath, dbURL, blobDir, in.token+","+in.tokenB, cert)
	handle, backendURL := startBinaryBackend(ctx, tb, cfg)
	createFirstNamespaceViaBinary(ctx, tb, cfg, backendURL)

	proxy := startRecordingProxy(tb, in.rec, backendURL, nil)

	client := proxy.Client()
	client.Timeout = commandTimeout
	env := &Env{
		dbURL:      "",
		URL:        proxy.URL,
		Recorder:   in.rec,
		cli:        in.cli,
		store:      in.store,
		client:     client,
		inprocess:  nil,
		blobs:      openBlobStoreAt(tb, blobDir),
		blobName:   blobFS,
		s3Endpoint: "",
		token:      in.token,
		tokenB:     in.tokenB, memberToken: "",
		home:          filepath.Join(in.dir, "home"),
		project:       filepath.Join(in.dir, "project"),
		remoteProject: "",
		steps:         nil,
		skipped:       nil,
		tls:           true,
		binPath:       binPath,
		backupDir:     "",
		backendCfg:    cfg,
		backendPort:   handle.port,
		backendCmd:    handle.cmd,
		backendLogs:   handle.logs,
		ha:            nil,
		certBundle:    "",
		dbProxy:       nil,
		signIn:        nil,
		claimers:      nil,
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

// SetServerBinary switches the binary the next StartServer and RunBinary
// use, so one environment can restart an upgraded database with another
// binary on the same port, database and blob directory.
func (e *Env) SetServerBinary(path string) {
	e.binPath = path
	e.backendCfg.bin = path
}

// SetAuditChain sets the --audit-chain value that the next StartServer
// uses: on or off.
func (e *Env) SetAuditChain(setting string) {
	e.backendCfg.auditChain = setting
}

// TryServe runs binPath `serve` once on the database, blob directory,
// certificate and port of the environment and returns what it printed. The
// environment server must be stopped first. A refusal exits at once with a
// non-zero code; a binary without the schema check starts and answers
// /health/live, in which case TryServe stops it and returns exit code 0.
func (e *Env) TryServe(ctx context.Context, tb testing.TB, binPath string) BinaryResult {
	tb.Helper()

	if e.backendCmd != nil {
		tb.Fatalf("TryServe: the server still runs")
	}

	cfg := e.backendCfg
	cfg.bin = binPath

	//nolint:gosec // The binary is built from this repository by the suite itself.
	cmd := exec.CommandContext(ctx, binPath, serveArgs(cfg, e.backendPort)...)
	cmd.Env = backendServeEnv(cfg.tokens, cfg.dbURL)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		tb.Fatalf("start server binary: %v", err)
	}

	done := make(chan error, 1)

	go func() { done <- cmd.Wait() }()

	tb.Cleanup(func() {
		_ = cmd.Process.Kill() //nolint:errcheck // The probe process ends with the kill signal.
		select {
		case killErr := <-done:
			_ = killErr // The kill signal ends the process, so Wait reports it.
		default:
		}
	})

	deadline := time.Now().Add(oldServeTimeout)

	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return serveResult(stdout, stderr, err)
		default:
		}

		if probeHealth(ctx, e.backendPort) {
			_ = cmd.Process.Kill() //nolint:errcheck // The started probe server ends here.
			killErr := <-done
			_ = killErr // The kill signal ends the process, so Wait reports it.

			return BinaryResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
		}

		timer := time.NewTimer(binaryStartPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = cmd.Process.Kill() //nolint:errcheck // The context ends the probe.
			killErr := <-done
			_ = killErr // The kill signal ends the process, so Wait reports it.

			tb.Fatalf("context done while probing the old binary")

			return BinaryResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 1}
		case <-timer.C:
		}
	}

	_ = cmd.Process.Kill() //nolint:errcheck // A start without the check never exits itself.
	killErr := <-done
	_ = killErr // The kill signal ends the process, so Wait reports it.

	return BinaryResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
}

// serveResult converts the wait outcome of a refused start into a result:
// a non-zero exit is a result, not a test failure.
func serveResult(stdout, stderr bytes.Buffer, err error) BinaryResult {
	if err == nil {
		return BinaryResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return BinaryResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitErr.ExitCode()}
	}

	return BinaryResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 1}
}

// probeHealth reports whether /health/live on port answers 204.
func probeHealth(ctx context.Context, port string) bool {
	//nolint:gosec // The probe dials the test backend on loopback.
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13},
		},
		Timeout: commandTimeout,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1:"+port+"/health/live", http.NoBody)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The health body is empty; only the status matters.
	}()

	return resp.StatusCode == http.StatusNoContent
}

// HasSchemaRefusal reports whether combined server output holds a schema
// refusal: the minimum binary version refusal, which names the binary version
// and the schema minimum, or the newer-schema refusal of binaries from before
// the minimum, which names the database and binary schema versions.
func HasSchemaRefusal(output string) bool {
	minimum := strings.Contains(output, "is below") &&
		strings.Contains(output, "the minimum binary version of the database schema")
	newer := strings.Contains(output, "database schema version") &&
		strings.Contains(output, "is newer than binary schema version")

	return minimum || newer
}

// peerPulumiProject is the scratch program of a peer. It holds no stack
// reference: the service secrets manager of a stack records the URL of the
// server that made the stack, so a peer on another address cannot read the
// stack "other" that the first server made. Behind one public URL the
// servers share the address, and this limit does not apply.
const peerPulumiProject = "name: " + Project + "\nruntime: yaml\n"

// StartPeer starts the server binary at binPath next to the server of e, on
// the same database, blob directory, certificate and tokens, and returns an
// environment that reaches the peer. Both servers serve at the same time.
// The peer has its own port, recording proxy, home directory and scratch
// project, so CLI steps on the peer never touch the files of e. The peer
// migrates the database at its own start, as the binary does. It needs
// E2E_SERVER=binary and a running server in e. A peer that the binary
// refuses fails the test: use TryPeer to check a refusal.
func (e *Env) StartPeer(ctx context.Context, tb testing.TB, binPath string) *Env {
	tb.Helper()

	if e.binPath == "" || e.backendCmd == nil {
		tb.Fatalf("StartPeer needs E2E_SERVER=binary and a running server")
	}

	cfg := e.backendCfg
	cfg.bin = binPath

	handle, backendURL := startBinaryBackend(ctx, tb, cfg)
	rec := &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}}
	proxy := startRecordingProxy(tb, rec, backendURL, nil)

	client := proxy.Client()
	client.Timeout = commandTimeout

	dir := tb.TempDir()
	peer := *e
	peer.URL = proxy.URL
	peer.Recorder = rec
	peer.client = client
	peer.home = filepath.Join(dir, "home")
	peer.project = filepath.Join(dir, "project")
	peer.steps = nil
	peer.binPath = binPath
	peer.backendCfg = cfg
	peer.backendPort = handle.port
	peer.backendCmd = handle.cmd
	peer.backendLogs = handle.logs

	for _, d := range []string{peer.home, peer.project} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			tb.Fatalf("create %s: %v", d, err)
		}
	}

	peer.write(tb, filepath.Join(peer.project, "Pulumi.yaml"), peerPulumiProject)
	peer.writeTofuFiles(tb, proxy)

	return &peer
}

// TryPeer starts the server binary at binPath next to the server of e, on
// the same database, and reports how it ended. A refusal exits at once with
// a non-zero code. A binary that starts answers /health/live; TryPeer then
// stops it and returns exit code 0. The server of e keeps running. It needs
// E2E_SERVER=binary.
func (e *Env) TryPeer(ctx context.Context, tb testing.TB, binPath string) BinaryResult {
	tb.Helper()

	if e.binPath == "" || e.backendCmd == nil {
		tb.Fatalf("TryPeer needs E2E_SERVER=binary and a running server")
	}

	cfg := e.backendCfg
	cfg.bin = binPath

	//nolint:gosec // The binary is built from this repository by the suite itself.
	cmd := exec.CommandContext(ctx, binPath, serveArgs(cfg, "0")...)
	cmd.Env = append(backendServeEnv(cfg.tokens, cfg.dbURL), journalingServeEnv(cfg)...)

	var out lockedBuffer

	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		tb.Fatalf("start peer binary: %v", err)
	}

	done := make(chan error, 1)

	go func() { done <- cmd.Wait() }()

	stop := func() {
		_ = cmd.Process.Kill() //nolint:errcheck // The probe process ends with the kill signal.
		killErr := <-done
		_ = killErr // The kill signal ends the process, so Wait reports it.
	}

	deadline := time.Now().Add(oldServeTimeout)

	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return peerResult(out.String(), err)
		default:
		}

		if port, ok := listeningPortFromLogs(out.String()); ok && probeHealth(ctx, port) {
			stop()

			return BinaryResult{Stdout: out.String(), Stderr: "", ExitCode: 0}
		}

		timer := time.NewTimer(binaryStartPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			stop()
			tb.Fatalf("context done while probing the peer binary")

			return BinaryResult{Stdout: out.String(), Stderr: "", ExitCode: 1}
		case <-timer.C:
		}
	}

	stop()
	tb.Fatalf("the peer binary neither exited nor answered /health/live in %s:\n%s", oldServeTimeout, out.String())

	return BinaryResult{Stdout: out.String(), Stderr: "", ExitCode: 1}
}

// peerResult converts the wait outcome of a peer start into a result: a
// non-zero exit is a result, not a test failure.
func peerResult(output string, err error) BinaryResult {
	res := serveResult(bytes.Buffer{}, bytes.Buffer{}, err)
	res.Stdout = output

	return res
}
