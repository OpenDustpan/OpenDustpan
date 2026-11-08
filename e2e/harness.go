//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	// Namespace is the namespace every Env creates; CLI stack names use it as the organization.
	Namespace = "acme"
	// Namespace2 is a second namespace a scenario creates to prove that
	// listings span namespaces. NewEnv does not create it, so steps that
	// need one readable namespace run first.
	Namespace2 = "beta"
	// Project is the project name written to the scratch Pulumi.yaml.
	Project = "e2e-project"
	// Workspace is the workspace name written to the scratch main.tf.
	Workspace = "e2e-workspace"
	// Subject is the principal the access token authenticates as.
	Subject = "admin"
	// RemotePrefix is the workspace prefix the remote-backend part uses.
	RemotePrefix = "e2e-"
	// RemoteDev is the short name of the first remote workspace.
	RemoteDev = "dev"
	// RemoteProd is the short name of the second remote workspace.
	RemoteProd = "prod"
	// SubjectB is the second principal for lock tests. Both principals are
	// administrators, so the AdminOnly authorizer allows both.
	SubjectB = "admin-2"

	installationName = "e2e"
	actor            = "e2e"
	tokenBytes       = 24
	commandTimeout   = 2 * time.Minute
	waitDelay        = 5 * time.Second
	// killWait bounds the wait for the marker file that triggers the kill
	// of a running CLI. It stays well under commandTimeout, so the kill and
	// the reap always finish before the step times out.
	killWait = time.Minute
	// killPoll is how often the kill helper checks for the marker file it
	// waits for.
	killPoll       = 20 * time.Millisecond
	maxShown       = 2000
	baseEnvEntries = 8
	// testKeyEncryptionKey is the fixed key-encryption key every e2e server
	// wraps data keys with. It is a test value only; operators configure a
	// random key (ADR 23).
	testKeyEncryptionKey = "0123456789abcdef0123456789abcdef"
	// pulumiProject is the scratch YAML program. Its one resource is a stack
	// reference to the other stack, which the engine serves from its built-in
	// provider, so no provider plugin is downloaded.
	pulumiProject = "name: " + Project + "\nruntime: yaml\n" +
		"resources:\n" +
		"  ref:\n" +
		"    type: pulumi:pulumi:StackReference\n" +
		"    properties:\n" +
		"      name: " + Namespace + "/" + Project + "/other\n"
	// tofuConfig uses the cloud block, OpenTofu's client for the TFE-compatible
	// remote API, and declares no providers so nothing is downloaded.
	tofuConfig = `terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

output "greeting" {
  value = "hello"
}
`
	// tofuRemoteConfig uses the remote backend with a workspace prefix, so
	// workspace new, list and select run without tags. It declares no
	// providers so nothing is downloaded.
	tofuRemoteConfig = `terraform {
  backend "remote" {
    hostname     = %q
    organization = %q
    workspaces {
      prefix = %q
    }
  }
}

output "greeting" {
  value = "hello"
}
`
	tofuCLIConfig = "credentials %q {\n  token = %q\n}\n"
)

// Result is the outcome of one CLI invocation.
type Result struct {
	Command  string
	Stdout   string
	Stderr   string
	Duration time.Duration
	ExitCode int
}

// StepResult is one scenario step as shown in the report.
type StepResult struct {
	Name     string `json:"name"`
	Command  string `json:"command"`
	Failure  string `json:"failure,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exitCode"`
	Passed   bool   `json:"passed"`
	Skipped  bool   `json:"skipped,omitempty"`
}

// Env is a running server plus an isolated CLI environment.
type Env struct {
	// URL is the server's base URL: the Pulumi backend URL, or https://<host> for OpenTofu.
	URL string
	// Recorder holds every request the CLI made to the server.
	Recorder *Recorder
	cli      Install
	store    Store
	client   *http.Client
	// inprocess is the store of the in-process server, or nil for the binary
	// server, whose database the test process never holds open while it runs.
	inprocess serverStore
	// blobs holds checkpoint and state content, so the seed helpers save
	// through the same store as the server.
	blobs blob.Store
	// blobName is the live blob store of the run: "fs" or "s3". The report
	// names it, so a summary shows what was driven.
	blobName string
	// s3Endpoint is the S3 endpoint URL of an S3 run, or "" on the directory
	// store. Client commands pass it as --s3-endpoint.
	s3Endpoint string
	token      string
	tokenB     string
	// memberToken signs in as MemberSubject, or is empty when the server
	// knows only the bootstrap admin tokens.
	memberToken string
	home        string
	project     string
	// remoteProject holds the remote-backend part in its own directory.
	remoteProject string
	steps         []StepResult
	// skipped holds the scenarios that did not run in this mode.
	skipped []SkippedScenario
	// tls reports whether the server speaks HTTPS. OpenTofu always needs
	// it; the Pulumi CLI needs it only on a TLS server, where it trusts
	// the certificate through SSL_CERT_FILE.
	tls bool
	// binPath is the server binary newBinaryEnv built. It is empty in the
	// in-process mode, where RunBinary, StopServer and StartServer fail.
	binPath string
	// backupDir is the backup target directory of the in-process server.
	backupDir string
	// signIn is the console sign-in of an env from NewEnvWithSignIn, or nil.
	signIn *signInSetup
	// claimers keeps the job claimer of the in-process server, or is nil for
	// the binary server.
	claimers *claimerSlot
	// backendCfg holds the database URL, blob directory, certificate and
	// key files and tokens of the binary backend.
	backendCfg backendConfig
	// backendPort is the loopback port the binary backend serves. A restart
	// reuses it, so the proxy target and the saved CLI logins stay valid.
	backendPort string
	// backendCmd is the running binary server process, or nil while stopped.
	backendCmd *exec.Cmd
	// backendLogs holds the combined output of backendCmd.
	backendLogs *lockedBuffer
	// ha is the cluster of server binaries behind the proxy when
	// E2E_INSTANCES is above 1, else nil. backendCmd, backendPort and
	// backendLogs are then empty: the cluster owns the processes.
	ha *HACluster
	// certBundle holds a PEM bundle with the certificates of both servers
	// of a pair. Empty for a single server, which trusts server.pem.
	certBundle string
	// dbProxy fronts PostgreSQL for the server, so closing it stops the
	// database for this environment only. It is nil on SQLite.
	dbProxy *DBProxy
	// dbURL is the --database value that reaches the database of the
	// environment: the SQLite file, or PostgreSQL through the proxy. A
	// step runs the host command against it.
	dbURL string
}

// serverStore is every persistence port the server needs.
//
//nolint:interfacebloat // The harness needs every persistence port in one adapter.
type serverStore interface {
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
}

// serverBuild groups the handler, blob store and database proxy newServer
// opens, so the constructor stays within the result limit.
type serverBuild struct {
	handler http.Handler
	store   serverStore
	blobs   blob.Store
	dbProxy *DBProxy
	// dbURL is the --database value that reaches the database of the build:
	// the SQLite file, or PostgreSQL through the proxy.
	dbURL string
	// backupDir is the directory of the dir: backup target the server writes.
	backupDir string
	// claimers keeps the job claimer of the server, or is nil.
	claimers *claimerSlot
}

// NewEnv starts a server on a random local port, backed by a fresh database
// with the Namespace namespace, and prepares an empty home directory and a
// scratch project for cli. SQLite uses a temporary file; PostgreSQL uses an
// isolated schema that is dropped when the test ends. OpenTofu gets an HTTPS
// server, because its service discovery refuses plain HTTP, and the server's
// certificate is the only one it trusts. Everything is removed when the test ends.
// The server hides the delta checkpoint capability, so the CLI sends whole
// checkpoints; use NewEnvWithDeltaCutoff for delta uploads.
//
// With E2E_SERVER=binary the server is the real cmd/opendustpan binary run
// with --tls-cert and --tls-key, fronted by a recording proxy. That variant
// is the proof for the milestone 2 exit test.
func NewEnv(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	return NewEnvWithDeltaCutoff(ctx, tb, cli, store, 0)
}

// defaultEnvInput returns the input of an env for cli and store with new
// tokens, a new directory and every feature switch off.
func defaultEnvInput(tb testing.TB, cli Install, store Store) envInput {
	tb.Helper()

	return envInput{
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
		tb:                     tb,
		cli:                    cli,
		store:                  store,
		dir:                    tb.TempDir(),
		token:                  newToken(tb),
		tokenB:                 newToken(tb),
		rec:                    &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}},
	}
}

// startEnv starts the env of in: the server binary with E2E_SERVER=binary,
// else an in-process server.
func startEnv(ctx context.Context, in envInput) *Env {
	if UseBinaryServer() {
		return newBinaryEnv(ctx, in)
	}

	return newInprocessEnv(ctx, in)
}

// NewEnvWithJournalingOff starts a server as NewEnv does, with Pulumi
// journaling turned off through DUSTPAN_DISABLE_PULUMI_JOURNALING=true, so
// each update answers journalVersion 0.
func NewEnvWithJournalingOff(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	in := defaultEnvInput(tb, cli, store)
	in.disableJournaling = true

	return startEnv(ctx, in)
}

// NewEnvWithSinkSenders starts a server as NewEnv does, with senders in place
// of the built-in notification sink senders of their kinds. It is for a step
// that needs a receiver on the machine of the test, which the built-in
// senders refuse by design. Only the in-process server takes the senders: the
// server binary keeps its own, and SinkSendersApply reports which one runs.
func NewEnvWithSinkSenders(
	ctx context.Context,
	tb testing.TB,
	cli Install,
	store Store,
	senders ...notify.Sender,
) *Env {
	tb.Helper()

	in := defaultEnvInput(tb, cli, store)
	in.sinkSenders = senders

	return startEnv(ctx, in)
}

// SinkSendersApply reports whether NewEnvWithSinkSenders changes the server:
// it does for the in-process server only.
func SinkSendersApply() bool { return !UseBinaryServer() }

// NewEnvWithIPAllowlists starts a server as NewEnv does, with IP allowlists
// turned on and applied to the bootstrap admin token too, so a step can lock
// one namespace to one source address and prove the 403 answers.
func NewEnvWithIPAllowlists(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	in := defaultEnvInput(tb, cli, store)
	in.ipAllowlists = true

	return startEnv(ctx, in)
}

// NewEnvWithPasskeys starts a server as NewEnv does, with a public URL whose
// host name is localhost, so the server can serve the passkey routes. A
// passkey relying party ID is a host name and never an IP address. The server
// still listens on its address, and the public URL names the same port.
func NewEnvWithPasskeys(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	in := defaultEnvInput(tb, cli, store)
	in.passkeys = true

	return startEnv(ctx, in)
}

// journalTestInput returns the input of an env for cli and store with new
// tokens, a new directory and journaling on, so the journal fault tests only
// set their own fault.
func journalTestInput(tb testing.TB, cli Install, store Store) envInput {
	tb.Helper()

	return envInput{
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
		tb:                     tb,
		cli:                    cli,
		store:                  store,
		dir:                    tb.TempDir(),
		token:                  newToken(tb),
		tokenB:                 newToken(tb),
		rec:                    &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}},
	}
}

// NewEnvWithJournalRefuser starts a server as NewEnv does, with the refuser
// corrupting the first outputs-only journal batch on the way to the server,
// so the server refuses it and records the refusal. The test reads how many
// batches the refuser corrupted.
func NewEnvWithJournalRefuser(
	ctx context.Context, tb testing.TB, cli Install, store Store, refuser *JournalRefuser,
) *Env {
	tb.Helper()

	in := journalTestInput(tb, cli, store)
	in.journalRefuser = refuser

	return startEnv(ctx, in)
}

// NewEnvWithJournalVersionCap starts a server as NewEnv does, with the cap
// lowering the journal version that start-update answers, so the CLI journals
// at an older version than the server supports. The test reads how many
// answers the cap lowered.
func NewEnvWithJournalVersionCap(
	ctx context.Context, tb testing.TB, cli Install, store Store, versionCap *JournalVersionCap,
) *Env {
	tb.Helper()

	in := journalTestInput(tb, cli, store)
	in.journalCap = versionCap

	return startEnv(ctx, in)
}

// NewEnvWithDeltaCutoff starts a server as NewEnv does, with the delta
// checkpoint cutoff in bytes. A cutoff of 1 makes every save after the
// first a delta; 0 hides the delta capability.
func NewEnvWithDeltaCutoff(ctx context.Context, tb testing.TB, cli Install, store Store, cutoff int) *Env {
	tb.Helper()

	dir := tb.TempDir()
	token := newToken(tb)
	tokenB := newToken(tb)
	rec := &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}}
	in := envInput{
		deltaCutoff:            cutoff,
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
		tb:                     tb,
		cli:                    cli,
		store:                  store,
		dir:                    dir,
		token:                  token,
		tokenB:                 tokenB,
		rec:                    rec,
	}

	if UseBinaryServer() {
		return newBinaryEnv(ctx, in)
	}

	return newInprocessEnv(ctx, in)
}

// startInprocessServer builds the in-process server of in and starts it.
// The handler is set before the server starts. The listener exists
// already, so the sign-in knows its public URL before the build.
func startInprocessServer(
	ctx context.Context,
	in envInput,
	member string,
) (*httptest.Server, serverBuild, *signInSetup) {
	tb := in.tb
	tb.Helper()

	var handler http.Handler

	ts := httptest.NewUnstartedServer(in.rec.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	})))

	var signIn *signInSetup
	if in.signIn {
		var shared *TestClock
		if !in.signInRealTime {
			shared = newTestClock()
		}

		signIn = newSignInSetup(tb, "https://"+ts.Listener.Addr().String(), shared)
	}

	// Binary mode proves the DUSTPAN_DISABLE_PULUMI_JOURNALING variable;
	// in-process mode uses the equivalent server option.
	claimers := newClaimerSlot()
	build := newServer(ctx, tb, serverArgs{
		store:             in.store,
		dbPath:            filepath.Join(in.dir, "dustpan.db"),
		tokens:            serverTokens{tokenA: in.token, tokenB: in.tokenB, member: member},
		deltaCutoff:       in.deltaCutoff,
		disableJournaling: in.disableJournaling,
		ipAllowlists:      in.ipAllowlists,
		signIn:            signIn,
		publicURL:         inprocessPublicURL(in, ts),
		sinkSenders:       in.sinkSenders,
		issuer:            in.issuer,
		claimers:          claimers,
	})
	build.claimers = claimers

	handler = cappedHandler(faultedHandler(build.handler, in.journalRefuser), in.journalCap)

	if in.cli.Tool == ToolTofu || in.signIn {
		ts.StartTLS()
	} else {
		ts.Start()
	}

	return ts, build, signIn
}

// newInprocessEnv starts an in-process server for cli and returns the
// environment.
func newInprocessEnv(ctx context.Context, in envInput) *Env {
	tb := in.tb
	tb.Helper()

	member := newToken(tb)

	ts, build, signIn := startInprocessServer(ctx, in, member)

	tb.Cleanup(ts.Close)

	client := ts.Client()
	client.Timeout = commandTimeout

	env := &Env{
		URL:           ts.URL,
		Recorder:      in.rec,
		cli:           in.cli,
		store:         in.store,
		client:        client,
		inprocess:     build.store,
		claimers:      build.claimers,
		blobs:         build.blobs,
		blobName:      blobNameOf(tb),
		s3Endpoint:    s3EndpointOf(tb),
		token:         in.token,
		tokenB:        in.tokenB,
		memberToken:   member,
		home:          filepath.Join(in.dir, "home"),
		project:       filepath.Join(in.dir, "project"),
		remoteProject: "",
		steps:         nil,
		skipped:       nil,
		tls:           in.cli.Tool == ToolTofu || in.signIn,
		binPath:       "",
		backendCfg:    backendConfig{}, //nolint:exhaustruct_v5 // The in-process mode has no binary backend.
		backendPort:   "",
		backendCmd:    nil,
		backendLogs:   nil,
		ha:            nil,
		certBundle:    "",
		dbProxy:       build.dbProxy,
		dbURL:         build.dbURL,
		signIn:        signIn,
		backupDir:     build.backupDir,
	}

	for _, d := range []string{env.home, env.project} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			tb.Fatalf("create %s: %v", d, err)
		}
	}

	env.write(tb, filepath.Join(env.project, "Pulumi.yaml"), pulumiProject)

	if env.tls {
		env.writeTofuFiles(tb, ts)
	}

	// A new installation holds no namespace: the first one is created through
	// the installation API with the bootstrap admin token, so the route is
	// proven before any CLI logs in.
	env.createNamespaceThroughAPI(ctx, tb, Namespace)

	return env
}

// SetCLI switches the environment to another installed CLI on the same
// server, so one test can drive both tools against one server process and
// database. The project files of both tools are already written.
func (e *Env) SetCLI(cli Install) {
	e.cli = cli
}

// Host is the server's host:port, as OpenTofu names it.
func (e *Env) Host() string {
	return strings.TrimPrefix(strings.TrimPrefix(e.URL, "https://"), "http://")
}

func newToken(tb testing.TB) string {
	tb.Helper()

	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		tb.Fatalf("generate token: %v", err)
	}

	return hex.EncodeToString(raw)
}

// serverTokens holds the two access tokens one Env accepts.
type serverTokens struct {
	tokenA string
	tokenB string
	// member signs in as MemberSubject: a principal that is not an
	// administrator.
	member string
}

// newServer opens store (a fresh SQLite file at dbPath, or an isolated
// PostgreSQL schema through a TCP proxy), creates the Namespace namespace and
// returns the build with the server's handler, store, workspace service and
// database proxy. The proxy is nil on SQLite. The store and its schema are
// closed and removed when the test ends.
// the environment needs the interface.
// openServerStore opens the store under test with the --database value
// that reaches it: the SQLite file, or PostgreSQL through a DBProxy, so a
// step can stop the database for this environment only. The proxy is nil
// on SQLite.
//
//nolint:ireturn // SQLite and PostgreSQL stores share no concrete type.
func openServerStore(
	ctx context.Context, tb testing.TB, store Store, dbPath string,
) (serverStore, *DBProxy, string) {
	tb.Helper()

	switch store {
	case StoreSQLite:
		return openSQLiteStore(ctx, tb, dbPath), nil, "sqlite:" + dbPath
	case StorePostgres:
		st, proxy := openProxiedPostgresStore(ctx, tb, PostgresURL())
		target := proxy.URL()

		return st, proxy, target
	default:
		tb.Fatalf("unknown store: %s", store)

		return nil, nil, ""
	}
}

// serverArgs groups what newServer needs, so the constructor takes one
// argument next to the context and the test helper.
type serverArgs struct {
	// sinkSenders replace the built-in sender of their kind on the in-process
	// server.
	sinkSenders []notify.Sender
	store       Store
	dbPath      string
	tokens      serverTokens
	deltaCutoff int
	// disableJournaling turns Pulumi journaling off on the in-process server.
	disableJournaling bool
	// ipAllowlists turns IP allowlists on for the in-process server and
	// applies them to the bootstrap admin token too.
	ipAllowlists bool
	// signIn turns on the console sign-in of the in-process server, or is
	// nil.
	signIn *signInSetup
	// publicURL is the external URL of the in-process server, or empty. The
	// provider registry protocol compares each request with its host.
	publicURL string
	// issuer is a fake OpenID provider whose keys the server may read on the
	// loopback, or nil.
	issuer *OIDCIssuerFake
	// claimers keeps the job claimer that the in-process server builds, or
	// is nil.
	claimers *claimerSlot
}

// profileStoreOptions returns the option that gives the in-process server the
// profile store of st.
func profileStoreOptions(st serverStore, wrapper secret.Wrapper) []dustpan.Option {
	switch s := st.(type) {
	case *sqlite.Store:
		return []dustpan.Option{
			dustpan.WithProfileStore(s.Profiles()),
			dustpan.WithIssuerStore(s.Issuer()),
			dustpan.WithIdempotencyStore(s.Idempotency(wrapper)),
		}
	case *postgres.Store:
		return []dustpan.Option{
			dustpan.WithProfileStore(s.Profiles()),
			dustpan.WithIssuerStore(s.Issuer()),
			dustpan.WithIdempotencyStore(s.Idempotency(wrapper)),
		}
	default:
		return nil
	}
}

// ipAllowlistOptions adds the IP allowlist options to opts when args turns
// the feature on, so the in-process server checks the lists of every
// namespace, including for the bootstrap admin token.
func ipAllowlistOptions(st serverStore, args serverArgs, opts []dustpan.Option) []dustpan.Option {
	if !args.ipAllowlists {
		return opts
	}

	return append(opts,
		dustpan.WithIPAllowlists(true),
		dustpan.WithIPAllowlistsIncludeBootstrap(true),
		dustpan.WithIPAllowlistStore(st),
	)
}

// storeOptions returns the options that give the in-process server the optional
// stores of st and the public URL.
func storeOptions(st serverStore, wrapper secret.Wrapper, args serverArgs) []dustpan.Option {
	return slices.Concat(
		profileStoreOptions(
			st,
			wrapper,
		),
		jobStoreOptions(st),
		claimerOptions(st, args.claimers),
		savedViewStoreOptions(st),
		settingStoreOptions(st), trustRuleStoreOptions(st, args.issuer), registryStoreOptions(st),
		sinkStoreOptions(st),
		publicURLOptions(args.publicURL),
		[]dustpan.Option{dustpan.WithSinkSenders(args.sinkSenders...)},
	)
}

// featureStoreOptions returns the options that give the in-process server the
// stores of the features that the store of the test keeps.
func featureStoreOptions(tb testing.TB, st serverStore, wrapper secret.Wrapper, args serverArgs) []dustpan.Option {
	tb.Helper()

	opts := append(storeOptions(st, wrapper, args), gitHostOptions(tb, st)...)

	return append(opts, dustpan.WithComponents(apiOnly(tb)))
}

func newServer(ctx context.Context, tb testing.TB, args serverArgs) serverBuild {
	tb.Helper()

	st, dbProxy, dbURL := openServerStore(ctx, tb, args.store, args.dbPath)

	// The first namespace is created through POST /api/server/namespaces
	// after the server starts (see newInprocessEnv and newBinaryEnv), so a
	// new installation is proven through the API with the bootstrap admin
	// token, not through a direct store call.

	authn, err := identity.NewStaticTokens(args.tokens.tokenA, args.tokens.tokenB)
	if err != nil {
		tb.Fatalf("static tokens: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(testKeyEncryptionKey))
	if err != nil {
		tb.Fatalf("key wrapper: %v", err)
	}

	// The start-up check refuses a key-encryption key that opens less than
	// every wrapped key in the database (ADR 23). The harness serves the
	// handler directly, so it runs the check here. Servers that run
	// Server.Run get the same check at start.
	if checkErr := secret.CheckWrapper(ctx, st, wrapper); checkErr != nil {
		tb.Fatalf("check key wrapper: %v", checkErr)
	}

	// The harness serves the handler directly, so it claims the installation
	// kind that Server.Run claims at start. The backup records it.
	claimer, ok := st.(installation.KindStore)
	if !ok {
		tb.Fatal("the store cannot claim an installation kind")
	}

	if _, claimErr := claimer.ClaimKind(ctx, installation.KindProduction); claimErr != nil {
		tb.Fatalf("claim the installation kind: %v", claimErr)
	}

	blobs := openTestBlobStore(ctx, tb, args.dbPath)
	target, targetName := openBackupTarget(tb, args.dbPath)

	srv, err := dustpan.NewServer(append(ipAllowlistOptions(
		st,
		args,
		append(
			featureStoreOptions(tb, st, wrapper, args),
			[]dustpan.Option{
				dustpan.WithInstallation(installationName),
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
				dustpan.WithBackupTarget(target, targetName),
				dustpan.WithInventoryReader(st),
				dustpan.WithAuthenticator(memberTokens{admins: authn, member: args.tokens.member}),
				dustpan.WithCheckpointDeltaCutoff(args.deltaCutoff),
				dustpan.WithDisablePulumiJournaling(args.disableJournaling),
			}...,
		),
	), args.signIn.options()...)...)
	if err != nil {
		tb.Fatalf("new server: %v", err)
	}

	startServing(ctx, tb, srv)

	return serverBuild{
		handler: srv.Handler(), store: st,
		blobs:     blobs,
		dbProxy:   dbProxy,
		dbURL:     dbURL,
		backupDir: strings.TrimPrefix(targetName, "dir:"),
		claimers:  nil,
	}
}

// startServing runs what Server.Run does before it serves, because the harness
// serves the handler directly: the start-up steps, and the backup worker. The
// worker stops before the stores close.
func startServing(ctx context.Context, tb testing.TB, srv *dustpan.Server) {
	tb.Helper()

	if err := srv.Start(ctx); err != nil {
		tb.Fatalf("start the server: %v", err)
	}

	startBackupWorker(ctx, tb, srv)
}

// apiOnly returns the api component alone. The in-process server runs Start
// and serves its handler, but it never runs the claim loop of the worker, so a
// worker component would fail the claim-loop readiness check once the server
// has a durable store and 2 claim intervals have passed. The real binary
// covers the worker component.
func apiOnly(tb testing.TB) dustpan.Components {
	tb.Helper()

	c, err := dustpan.ParseComponents(string(dustpan.ComponentAPI))
	if err != nil {
		tb.Fatalf("parse components: %v", err)
	}

	return c
}

// startBackupWorker runs the backup worker of srv until the test ends.
func startBackupWorker(ctx context.Context, tb testing.TB, srv *dustpan.Server) {
	tb.Helper()

	backups := srv.Backups()
	if backups == nil {
		return
	}

	workCtx, stop := context.WithCancel(context.WithoutCancel(ctx))

	var work sync.WaitGroup

	work.Go(func() { backups.Work(workCtx) })

	tb.Cleanup(func() {
		stop()
		work.Wait()
	})
}

// openBackupTarget opens a backup target directory next to the database at
// dbPath and returns it with its target name (dir:<path>), so the backup
// routes write real backups in every environment. The directory is removed
// when the test ends.
func openBackupTarget(tb testing.TB, dbPath string) (*fs.Store, string) {
	tb.Helper()

	path := filepath.Join(filepath.Dir(dbPath), "backup-target")

	target, err := fs.Open(path)
	if err != nil {
		tb.Fatalf("open backup target: %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			tb.Errorf("close backup target: %v", closeErr)
		}
	})

	return target, "dir:" + path
}

// openSQLiteStore opens the SQLite file at dbPath for one test.
//
//nolint:ireturn // SQLite and PostgreSQL stores share no concrete type;
func openSQLiteStore(ctx context.Context, tb testing.TB, dbPath string) serverStore {
	tb.Helper()

	st, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		tb.Fatalf("open sqlite store: %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			tb.Errorf("close sqlite store: %v", closeErr)
		}
	})

	return st
}

// openTestBlobStore opens the live blob store of one environment: an
// S3-compatible store under a unique prefix with E2E_BLOBS=s3, else a blob
// store in the test directory holding dbPath.
//
//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func openTestBlobStore(ctx context.Context, tb testing.TB, dbPath string) blob.Store {
	tb.Helper()

	if UseS3Blobs(tb) {
		cfg := RequireS3Config(tb)

		return openS3Store(ctx, tb, cfg, newS3RunPrefix(tb)+"/blobs")
	}

	return openBlobStoreAt(tb, filepath.Join(filepath.Dir(dbPath), "blobs"))
}

// openBlobStoreAt opens a blob store in blobDir.
func openBlobStoreAt(tb testing.TB, blobDir string) *fs.Store {
	tb.Helper()

	blobs, err := fs.Open(blobDir)
	if err != nil {
		tb.Fatalf("open blob store: %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			tb.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// seedSchemaVersion is the deployment format version SeedResources saves.
// The server never interprets the deployment, so any version 1 or higher
// passes validation.
const seedSchemaVersion = 3

// seedDeployment is the checkpoint SeedResources saves: the automatic stack
// resource and one managed resource, so the stack counts as having resources.
const seedDeployment = `{"manifest":{},"resources":[` +
	`{"urn":"urn:pulumi:stack::e2e-project::pulumi:pulumi:Stack::e2e-project-resourced",` +
	`"type":"pulumi:pulumi:Stack"},` +
	`{"urn":"urn:pulumi:stack::e2e-project::custom:resource:Type::res1",` +
	`"type":"custom:resource:Type","custom":true,"id":"res1-id"}]}`

// SeedResources records a finished update with one managed resource as the
// latest checkpoint of the stack named stackName in Project. It drives the
// update routes as principal A: it creates and starts an update, saves the
// checkpoint with the lease token and completes the update. Delete tests use
// it to set up a stack with resources without a provider that makes real
// resources.
func (e *Env) SeedResources(ctx context.Context, tb testing.TB, stackName string) {
	tb.Helper()

	held := e.StartHeldUpdate(ctx, tb, Namespace, Project, stackName)

	e.apiCall(ctx, tb, apiRequest{
		Method: http.MethodPatch,
		Path:   "/api/stacks/" + Namespace + "/" + Project + "/" + stackName + "/update/" + held.ID + "/checkpoint",
		Auth:   "update-token " + held.Lease,
		Body:   `{"version":` + strconv.Itoa(seedSchemaVersion) + `,"deployment":` + seedDeployment + `}`,
		Out:    nil,
	})

	e.EndHeldUpdate(ctx, tb, held, "succeeded")
}

const (
	// seedLockReason is the lock reason SeedTofuState sends.
	seedLockReason = "seed"
	// seedStateLineage is the lineage SeedTofuState writes when the workspace
	// has no state yet.
	seedStateLineage = "seed-lineage"
	// seedStateOutputs is the outputs document of the seeded state. The stored
	// outputs come from the uploaded body, so SeedTofuState writes it there:
	// the greeting output with its cty value and type, plus a sensitive
	// output so the run also proves the single-output route the CLI uses for
	// it.
	seedStateOutputs = `{"greeting":{"sensitive":false,"value":"hello","type":"string"},` +
		`"secret":{"sensitive":true,"value":"s3cr3t","type":"string"}}`
)

// seedStateDocument returns a minimal tfstate document with lineage, serial
// and the outputs of the seed. The outputs come from this body, not from the
// declared rendering (ADR 49, decision 8), so the body carries both outputs.
func seedStateDocument(lineage string, serial int64) []byte {
	doc := `{"version":4,"terraform_version":"1.11.0","serial":` + strconv.FormatInt(serial, 10) +
		`,"lineage":` + strconv.Quote(lineage) +
		`,"outputs":` + seedStateOutputs + `,"resources":[]}`

	return []byte(doc)
}

// seedStateAttributes holds the attributes of a state version document that
// SeedTofuState reads: the lineage and serial of the current state, and the
// upload URL of a new state version.
type seedStateAttributes struct {
	Lineage   string `json:"lineage"`
	Serial    int64  `json:"serial"`
	UploadURL string `json:"hosted-state-upload-url"` //nolint:tagliatelle // The TFE wire name.
}

// seedStateData is the data member of a state version document.
type seedStateData struct {
	Attributes seedStateAttributes `json:"attributes"`
}

// seedStateDoc is a state version document of the TFE-compatible API.
type seedStateDoc struct {
	Data seedStateData `json:"data"`
}

// SeedTofuState writes a finalized state version with a greeting output for
// the scenario workspace, as SeedResources does for Pulumi. It drives the
// TFE-compatible routes as principal A: it locks the workspace, declares the
// state version, uploads its content and unlocks. It chains the lineage and
// serial after the current state when one exists, so later steps see an
// unlocked workspace with state.
func (e *Env) SeedTofuState(ctx context.Context, tb testing.TB) {
	tb.Helper()

	byID := "/api/v2/workspaces/" + e.WorkspaceID(ctx, tb)

	e.seedCall(ctx, tb, seedRequest{
		method: http.MethodPost, path: byID + "/actions/lock",
		body: `{"data":{"attributes":{"reason":"` + seedLockReason + `"}}}`, want: http.StatusOK, out: nil,
	})

	lineage, serial := seedStateLineage, int64(1)

	current, err := e.TFEStatusHeaders(ctx, e.token, http.MethodGet, byID+"/current-state-version", "")
	if err != nil {
		tb.Fatalf("read current state version: %v", err)
	}

	switch current.Status {
	case http.StatusOK:
		var doc seedStateDoc
		if decodeErr := json.Unmarshal([]byte(current.Body), &doc); decodeErr != nil {
			tb.Fatalf("decode current state version: %v (%s)", decodeErr, current.Body)
		}

		lineage, serial = doc.Data.Attributes.Lineage, doc.Data.Attributes.Serial+1
	case http.StatusNotFound:
	default:
		tb.Fatalf("read current state version answers %d: %s", current.Status, current.Body)
	}

	content := seedStateDocument(lineage, serial)
	sum := md5.Sum(content) //nolint:gosec // The TFE protocol names state by MD5 for integrity.

	var created seedStateDoc

	e.seedCall(ctx, tb, seedRequest{
		method: http.MethodPost, path: byID + "/state-versions",
		body: `{"data":{"type":"state-versions","attributes":{"serial":` + strconv.FormatInt(serial, 10) +
			`,"lineage":` + strconv.Quote(lineage) + `,"md5":"` + hex.EncodeToString(sum[:]) + `"}}}`,
		want: http.StatusCreated, out: &created,
	})

	uploadPath, ok := strings.CutPrefix(created.Data.Attributes.UploadURL, e.URL)
	if !ok {
		tb.Fatalf("upload URL %q is not on the server %s", created.Data.Attributes.UploadURL, e.URL)
	}

	e.seedCall(ctx, tb, seedRequest{
		method: http.MethodPut, path: uploadPath, body: string(content), want: http.StatusOK, out: nil,
	})

	e.seedCall(ctx, tb, seedRequest{
		method: http.MethodPost, path: byID + "/actions/unlock", body: "", want: http.StatusOK, out: nil,
	})
}

// CreateNamespace creates a namespace whose primary installation is the test installation through the
// installation API, for steps that need more than the Namespace namespace
// NewEnv creates. It proves the namespace create route with the bootstrap
// admin token.
func (e *Env) CreateNamespace(ctx context.Context, tb testing.TB, name string) {
	tb.Helper()
	e.createNamespaceThroughAPI(ctx, tb, name)
}

// CatchAllCollection is the collection that the catch-all placement rule of a
// namespace of the harness names.
const CatchAllCollection = "e2e-default"

// SeedCatchAllPlacement makes the collection CatchAllCollection in the
// namespace ns, a placement rule for Pulumi stacks of any project and any name,
// and a placement rule for OpenTofu workspaces of any name. Both rules name the
// collection. The server refuses a new stack or workspace that no rule places,
// so each scenario that makes one through a CLI needs the rules. The
// administrator token holds collection:create-unit on the collection.
func (e *Env) SeedCatchAllPlacement(ctx context.Context, tb testing.TB, ns string) {
	tb.Helper()

	base := "/api/dustpan/v1/namespaces/" + ns

	for _, step := range []struct{ path, body string }{
		{base + "/collections", `{"name":"` + CatchAllCollection + `"}`},
		{
			base + "/placement-rules",
			`{"tool":"pulumi","project":"*","stack":"*","collection":"` + CatchAllCollection + `"}`,
		},
		{
			base + "/placement-rules",
			`{"tool":"opentofu","project":"*","stack":"*","collection":"` + CatchAllCollection + `"}`,
		},
	} {
		status, body, err := e.serverStatus(ctx, e.token, http.MethodPost, step.path, step.body)
		if err != nil {
			tb.Fatalf("POST %s: %v", step.path, err)
		}

		// 409: an earlier call made the collection or the rule, so the call is safe to repeat.
		if status != http.StatusCreated && status != http.StatusConflict {
			tb.Fatalf("POST %s answers %d, want %d (%s)", step.path, status, http.StatusCreated, body)
		}
	}
}

// CreateForeignNamespace stores a namespace whose primary installation is installationName, so a step can
// prove what the server answers for a namespace that another installation owns.
// The installation API creates namespaces in the server's own installation only,
// so this is a direct store write. The binary server owns its database, so
// the write runs while the binary is stopped, on a handle that is closed
// before the binary starts again. The in-process server writes through its
// own store.
func (e *Env) CreateForeignNamespace(ctx context.Context, tb testing.TB, name, installationName string) {
	tb.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                namespace.Name(name),
		PrimaryInstallation: namespace.InstallationName(installationName),
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if e.binPath == "" {
		if err := e.inprocess.CreateNamespace(ctx, ns); err != nil {
			tb.Fatalf("create namespace %q with primary installation %q: %v", name, installationName, err)
		}

		return
	}

	e.StopServer(ctx, tb)
	e.writeStopped(ctx, tb, func(st namespace.Store) error { return st.CreateNamespace(ctx, ns) })
	e.StartServer(ctx, tb)
}

// InventoryUnit is one unit of the inventory API list: the tool label
// ("Pulumi" or "OpenTofu"), the unit reference with the lowercase tool code,
// the namespace and the tool's own name of the unit.
type InventoryUnit struct {
	Tool      string `json:"tool"`
	Reference string `json:"reference"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// inventoryUnitList is the document of GET /api/dustpan/v1/namespaces/{namespace}/units.
type inventoryUnitList struct {
	Units []InventoryUnit `json:"units"`
}

// ListInventory returns the inventory units of ns through the inventory API
// as principal A, so scenarios can prove a CLI step published its unit.
func (e *Env) ListInventory(ctx context.Context, ns string) ([]InventoryUnit, error) {
	path := "/api/dustpan/v1/namespaces/" + url.PathEscape(ns) + "/units"

	status, body, err := e.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		return nil, fmt.Errorf("list inventory of %q: %w", ns, err)
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: GET %s answers %d: %s", errInventoryList, path, status, body)
	}

	var doc inventoryUnitList
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, fmt.Errorf("decode inventory of %q: %w", ns, err)
	}

	return doc.Units, nil
}

// errInventoryList reports an inventory list that did not answer 200.
var errInventoryList = errors.New("inventory list failed")

// Steps returns the scenario steps run so far.
func (e *Env) Steps() []StepResult {
	return append([]StepResult(nil), e.steps...)
}

// SkipScenario records that a scenario does not run in this mode and logs the
// reason. gaps names the contract lines that only this scenario produces. The
// contract check then reports them as not checked and does not fail the run.
// A mode that runs the scenario records nothing, so it still requires them.
func (e *Env) SkipScenario(tb testing.TB, name, reason string, gaps ...Gap) {
	tb.Helper()

	e.skipped = append(e.skipped, SkippedScenario{Name: name, Reason: reason, Gaps: gaps})
	tb.Logf("skip %s: %s", name, reason)
}

// Skipped returns the scenarios that did not run in this mode.
func (e *Env) Skipped() []SkippedScenario {
	return append([]SkippedScenario(nil), e.skipped...)
}

// Install returns the CLI under test.
func (e *Env) Install() Install {
	return e.cli
}

// Store returns the database backend this environment runs against.
func (e *Env) Store() Store {
	return e.store
}

// ProjectDir returns the scratch project directory the CLI runs in, so tests
// that read the stack file can find it.
func (e *Env) ProjectDir() string {
	return e.project
}

// Requested reports whether the CLI sent method path during step.
func (e *Env) Requested(step, method, path string) bool {
	entries := e.Recorder.Entries()

	for i := range entries {
		if r := &entries[i]; r.Step == step && r.Method == method && r.Path == path {
			return true
		}
	}

	return false
}

// errNoStackID reports a stack response without an id field.
var errNoStackID = errors.New("stack has no id")

// StackID fetches the stack at ns/project/name over the Pulumi CLI API
// and returns its id field and the response status. It authenticates with the
// environment's token. A missing stack returns status 404 with no error.
func (e *Env) StackID(
	ctx context.Context, ns, project, name string,
) (string, int, error) {
	target := e.URL + "/api/stacks/" + ns + "/" + project + "/" + name

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return "", 0, fmt.Errorf("build stack request: %w", err)
	}

	req.Header.Set("Authorization", "token "+e.token)

	resp, err := e.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("get stack %s/%s/%s: %w", ns, project, name, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, nil
	}

	var got struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return "", 0, fmt.Errorf("decode stack %s/%s/%s: %w", ns, project, name, err)
	}

	if got.ID == "" {
		return "", 0, fmt.Errorf("%w: %s/%s/%s", errNoStackID, ns, project, name)
	}

	return got.ID, resp.StatusCode, nil
}

// APIStatus sends one authenticated request and returns its status and body.
// Checks that expect an error status use it: a decrypt with a foreign
// ciphertext returns 400 with no test failure.
func (e *Env) APIStatus(ctx context.Context, method, path, body string) (int, string, error) {
	resp, err := e.APIStatusHeaders(ctx, method, path, body)

	return resp.Status, resp.Body, err
}

// APIStatusAs sends one request to the Pulumi Cloud-compatible API with token
// in the scheme "token" that the Pulumi CLI sends, and returns its status and
// body. Steps that use an access token that the server issued use it.
func (e *Env) APIStatusAs(ctx context.Context, token, method, path, body string) (int, string, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, reader)
	if err != nil {
		return 0, "", fmt.Errorf("build %s %s: %w", method, path, err)
	}

	req.Header.Set("Authorization", "token "+token)

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("%s %s: %w", method, path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below.
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", fmt.Errorf("read %s %s: %w", method, path, err)
	}

	return resp.StatusCode, string(respBody), nil
}

// OpenStream sends an authenticated GET and returns the response before its
// body ends, so a test reads a stream line by line while the server still
// writes it. The caller closes the body.
func (e *Env) OpenStream(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL+path, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build GET %s: %w", path, err)
	}

	req.Header.Set("Authorization", "token "+e.token)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}

	return resp, nil
}

// APIResponse is the status, headers and body of one harness request.
// Reveal checks read the headers: the value must travel with
// Cache-Control: no-store.
type APIResponse struct {
	Status int
	Header http.Header
	Body   string
}

// APIStatusHeaders sends one authenticated request and returns its status,
// headers and body.
func (e *Env) APIStatusHeaders(ctx context.Context, method, path, body string) (APIResponse, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, reader)
	if err != nil {
		return APIResponse{}, fmt.Errorf("build %s %s: %w", method, path, err)
	}

	req.Header.Set("Authorization", "token "+e.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return APIResponse{}, fmt.Errorf("%s %s: %w", method, path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return APIResponse{}, fmt.Errorf("read %s %s: %w", method, path, err)
	}

	return APIResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(respBody)}, nil
}

// ServerAPIStatus sends one request to the installation API with the
// environment's token as a Bearer credential and returns its status and
// body. The installation API reads Bearer credentials, as OpenTofu sends
// them.
func (e *Env) ServerAPIStatus(ctx context.Context, method, path, body string) (int, string, error) {
	return e.serverStatus(ctx, e.token, method, path, body)
}

// ServerAPIStatusAs sends one installation API request with token as a
// Bearer credential and returns its status and body, for steps that use an
// access token that the server issued (ADR 53).
func (e *Env) ServerAPIStatusAs(ctx context.Context, token, method, path, body string) (int, string, error) {
	return e.serverStatus(ctx, token, method, path, body)
}

// ServerAPIHeadersAs sends one GET to the installation API with token as a
// Bearer credential and returns its status, headers and body, for steps
// that read a response header such as the token expiry warning (ADR 53).
func (e *Env) ServerAPIHeadersAs(ctx context.Context, token, path string) (APIResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL+path, http.NoBody)
	if err != nil {
		return APIResponse{}, fmt.Errorf("build GET %s: %w", path, err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := e.client.Do(req)
	if err != nil {
		return APIResponse{}, fmt.Errorf("GET %s: %w", path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return APIResponse{}, fmt.Errorf("read GET %s: %w", path, err)
	}

	return APIResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(body)}, nil
}

// ServerAPIStatusBare sends one installation API request without credentials
// and returns its status and body, for steps that prove a route needs a
// token.
func (e *Env) ServerAPIStatusBare(ctx context.Context, method, path string) (int, string, error) {
	return e.serverStatus(ctx, "", method, path, "")
}

// ServerAPIStatusFrom sends one installation API request with token as a
// Bearer credential, leaving from the local address source (for example
// "127.0.0.2"), and returns its status and body. A step that holds an IP
// allowlist for one namespace uses it to prove that a source inside the
// list passes and a source outside it is refused.
//
//nolint:revive // The step names the source beside the token, the method, the path and the body.
func (e *Env) ServerAPIStatusFrom(
	ctx context.Context, source, token, method, path, body string,
) (int, string, error) {
	base, ok := e.client.Transport.(*http.Transport)
	if !ok {
		return 0, "", fmt.Errorf("server request %s %s from %s: %w",
			method, path, source, errNotHTTPTransport)
	}

	transport := base.Clone()
	//nolint:exhaustruct_v5 // The dialer sets only the source address; the rest stays zero.
	transport.DialContext = (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}).DialContext

	client := &http.Client{Transport: transport, Timeout: e.client.Timeout}

	defer client.CloseIdleConnections()

	return e.serverStatusWith(ctx, serverRequest{
		client: client, token: token, method: method, path: path, body: body,
	})
}

// TFEStatus sends one request to the TFE-compatible API as the given token
// and returns its status and body. The token travels as a Bearer credential,
// as OpenTofu sends it. It uses the test server's client, which trusts the
// self-signed certificate OpenTofu gets through SSL_CERT_FILE.
func (e *Env) TFEStatus(ctx context.Context, token, method, path, body string) (int, string, error) {
	return e.tfeStatusAs(
		ctx,
		token,
		tfeCall{method: method, path: path, body: body, userAgent: "", scheme: "", headers: nil},
	)
}

// tfeCall is one TFE API request. An empty userAgent keeps the client default,
// which the route coverage check counts as harness traffic.
type tfeCall struct {
	method    string
	path      string
	body      string
	userAgent string
	// scheme names the Authorization scheme: "token" for the console scheme, or
	// empty for "Bearer" (the TFE-compatible and installation scheme).
	scheme string
	// headers are extra request headers, for example If-Match.
	headers map[string]string
}

// TFEStatusA sends one TFE API request as principal A, the default principal
// that the CLI configuration uses.
func (e *Env) TFEStatusA(ctx context.Context, method, path, body string) (int, string, error) {
	resp, err := e.TFEStatusHeaders(ctx, e.token, method, path, body)

	return resp.Status, resp.Body, err
}

// TFEStatusHeaders sends one TFE API request as token and returns its
// status, headers and body.
func (e *Env) TFEStatusHeaders(ctx context.Context, token, method, path, body string) (APIResponse, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, reader)
	if err != nil {
		return APIResponse{}, fmt.Errorf("build %s %s: %w", method, path, err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/vnd.api+json")
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return APIResponse{}, fmt.Errorf("%s %s: %w", method, path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return APIResponse{}, fmt.Errorf("read %s %s: %w", method, path, err)
	}

	return APIResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(respBody)}, nil
}

// TFEStatusB sends one TFE API request as principal B, the second principal
// for lock tests.
func (e *Env) TFEStatusB(ctx context.Context, method, path, body string) (int, string, error) {
	return e.TFEStatus(ctx, e.tokenB, method, path, body)
}

// TofuEnvB returns the extra environment to run the OpenTofu CLI as principal
// B: its own CLI configuration holds B's token. Later entries win in cliEnv,
// so this overrides the default configuration.
func (e *Env) TofuEnvB() []string {
	return []string{"TF_CLI_CONFIG_FILE=" + filepath.Join(e.home, "tofurc-b")}
}

// MemberToken returns the token that signs in as MemberSubject, a principal
// that is not an administrator, and reports whether the server knows it.
// The binary server knows only the bootstrap admin tokens until sign-in and
// access tokens exist (ADR 53).
func (e *Env) MemberToken() (string, bool) {
	return e.memberToken, e.memberToken != ""
}

// TokenA returns the access token of principal A, the principal the CLI
// configuration uses. Tests that drive the import script pass it to the
// script through the environment, never as an argument.
func (e *Env) TokenA() string {
	return e.token
}

// CABundle returns the PEM bundle that trusts this server: both certificates
// of a pair after MergeTofuPair, else this server's own certificate. Tests
// pass it to the import script, which hands it to curl.
func (e *Env) CABundle() string {
	if e.certBundle != "" {
		return e.certBundle
	}

	return filepath.Join(e.home, "server.pem")
}

// ExternalResult is the outcome of a command a test runs outside the CLI,
// like the import script.
type ExternalResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// RecordExternalStep records an external command as a scenario step for the
// report. failure holds the check failure, or "" when the step passed. A
// failing step does not stop later steps: the goal is to see every gap.
func (e *Env) RecordExternalStep(t *testing.T, name, command string, res ExternalResult, failure string) {
	t.Helper()

	e.steps = append(e.steps, StepResult{
		Name:     name,
		Command:  command,
		Failure:  failure,
		Stderr:   clip(res.Stderr),
		ExitCode: res.ExitCode,
		Passed:   failure == "",
		Skipped:  false,
	})

	t.Logf("%s (exit %d)\nstdout:\n%s\nstderr:\n%s", command, res.ExitCode, clip(res.Stdout), clip(res.Stderr))

	if failure != "" {
		t.Errorf("%s: %s", name, failure)
	}
}

// workspaceIDData holds the data object of a workspace read document.
type workspaceIDData struct {
	ID string `json:"id"`
}

// workspaceIDDoc decodes a workspace read document in tests.
type workspaceIDDoc struct {
	Data workspaceIDData `json:"data"`
}

// WorkspaceID returns the ID of the e2e workspace through the TFE API as
// principal A, failing the test when the workspace is missing.
func (e *Env) WorkspaceID(ctx context.Context, tb testing.TB) string {
	tb.Helper()

	return e.WorkspaceIDByName(ctx, tb, Workspace)
}

// APIStepSpec describes one TFE API step.
type APIStepSpec struct {
	// UserAgent sets the User-Agent of the request. Empty keeps the client
	// default. A step that counts for route coverage names its own agent.
	UserAgent string
	// Check inspects the response body; nil means the status is enough.
	Check func(body string) error
	// Name labels the subtest, the report row and the requests the step makes.
	Name string
	// Method is the HTTP method, for example POST.
	Method string
	// Path is the request path, for example /api/v2/workspaces/ws-1/actions/lock.
	Path string
	// Body is the request body, or "" for none.
	Body string
	// Want is the expected HTTP status.
	Want int
	// Scheme is "token" to send the console scheme ("Authorization: token <t>").
	// Empty sends "Bearer <t>", the scheme of the TFE-compatible API.
	Scheme string
}

// authSchemeToken names the console Authorization scheme in APIStepSpec.Scheme.
const authSchemeToken = "token"

// APIStep runs one TFE API request as a scenario step and records it for the
// report. It fails the subtest when the status differs from want or when
// check rejects the body. A failing step does not stop later steps.
func (e *Env) APIStep(ctx context.Context, t *testing.T, token string, spec APIStepSpec) {
	t.Helper()
	e.apiStepWith(ctx, t, token, spec, nil)
}

// APIStepWithHeaders is APIStepA with extra request headers, for example
// If-Match.
func (e *Env) APIStepWithHeaders(ctx context.Context, t *testing.T, spec APIStepSpec, headers map[string]string) {
	t.Helper()
	e.apiStepWith(ctx, t, e.token, spec, headers)
}

// APIStepWithHeadersAs is APIStepWithHeaders for the person who owns token,
// for example to send If-Match with the token of a person.
func (e *Env) APIStepWithHeadersAs(
	ctx context.Context, t *testing.T, token string, spec APIStepSpec, headers map[string]string,
) {
	t.Helper()
	e.apiStepWith(ctx, t, token, spec, headers)
}

// APIStepA runs one TFE API request as principal A and records it as a step.
func (e *Env) APIStepA(ctx context.Context, t *testing.T, spec APIStepSpec) {
	t.Helper()
	e.APIStep(ctx, t, e.token, spec)
}

// APIStepB runs one TFE API request as principal B and records it as a step.
func (e *Env) APIStepB(ctx context.Context, t *testing.T, spec APIStepSpec) {
	t.Helper()
	e.APIStep(ctx, t, e.tokenB, spec)
}

// HeldUpdate is one update a scenario holds open through the API to prove
// that a second update cannot start while it runs.
type HeldUpdate struct {
	Namespace string
	Project   string
	Name      string
	ID        string
	Lease     string
}

// StartHeldUpdate creates and starts a mutating update on the stack through
// the API and returns it. The stack stays locked until EndHeldUpdate ends the
// update. Scenario steps use it to prove that a second update cannot start
// while one runs.
func (e *Env) StartHeldUpdate(ctx context.Context, tb testing.TB, ns, project, name string) HeldUpdate {
	tb.Helper()

	prefix := "/api/stacks/" + ns + "/" + project + "/" + name

	var created struct {
		UpdateID string `json:"updateID"`
	}

	e.apiCall(ctx, tb, apiRequest{
		Method: http.MethodPost,
		Path:   prefix + "/update",
		Auth:   "token " + e.token,
		Body:   `{}`,
		Out:    &created,
	})

	if created.UpdateID == "" {
		tb.Fatalf("create held update on %s/%s/%s: no update id", ns, project, name)
	}

	var started struct {
		Token string `json:"token"`
	}

	e.apiCall(ctx, tb, apiRequest{
		Method: http.MethodPost,
		Path:   prefix + "/update/" + created.UpdateID,
		Auth:   "token " + e.token,
		Body:   `{}`,
		Out:    &started,
	})

	if started.Token == "" {
		tb.Fatalf("start held update on %s/%s/%s: no lease token", ns, project, name)
	}

	return HeldUpdate{Namespace: ns, Project: project, Name: name, ID: created.UpdateID, Lease: started.Token}
}

// SaveHeldCheckpoint saves the exported deployment into the held update
// through the checkpoint route with its lease token, as the engine would have
// before the CLI vanished. It adds the crash marker: an unknown field the
// engine ignores, so the next up still previews against the matching base
// snapshot while the export after cancel proves cancel kept the checkpoint.
func (e *Env) SaveHeldCheckpoint(ctx context.Context, tb testing.TB, held HeldUpdate, exportStdout string) {
	tb.Helper()
	e.saveHeldDeployment(ctx, tb, held, exportStdout, map[string]string{"crashMarker": "crash-marker"})
}

// SaveHeldPaddedCheckpoint saves the exported deployment into the held
// update with the given extra deployment fields merged in, as the engine
// would have before the CLI vanished. Tests use a large padding field to grow
// the database, so a backup holds still running while the test starts another
// one or stops the server.
func (e *Env) SaveHeldPaddedCheckpoint(
	ctx context.Context, tb testing.TB, held HeldUpdate, exportStdout string, extra map[string]string,
) {
	tb.Helper()
	e.saveHeldDeployment(ctx, tb, held, exportStdout, extra)
}

// EndHeldUpdate completes the held update with status, releasing the stack
// lock. Only the lease token authorizes it, as for the CLI.
func (e *Env) EndHeldUpdate(ctx context.Context, tb testing.TB, held HeldUpdate, status string) {
	tb.Helper()

	prefix := "/api/stacks/" + held.Namespace + "/" + held.Project + "/" + held.Name

	e.apiCall(ctx, tb, apiRequest{
		Method: http.MethodPost,
		Path:   prefix + "/update/" + held.ID + "/complete",
		Auth:   "update-token " + held.Lease,
		Body:   `{"status":` + fmt.Sprintf("%q", status) + `}`,
		Out:    nil,
	})
}

// apiRequest is one authenticated API call.
type apiRequest struct {
	Method string
	Path   string
	Auth   string
	Body   string
	Out    any
}

// Run executes the CLI with args and returns what it printed. extra holds
// NAME=value pairs added to the environment. A non-zero exit is reported in the
// Result, not as an error.
func (e *Env) Run(ctx context.Context, extra []string, args ...string) (Result, error) {
	return e.RunIn(ctx, e.project, extra, "", args...)
}

// RunAs executes the CLI of tool cli in dir and returns what it printed,
// without touching the environment CLI. Background writers use it to drive
// one tool while the environment CLI serves another, so concurrent runs never
// race on SetCLI. extra holds NAME=value pairs added to the environment. It
// sends no standard input. A non-zero exit is reported in the Result, not as
// an error.
func (e *Env) RunAs(
	ctx context.Context, cli Install, dir string, extra []string, args ...string,
) (Result, error) {
	return e.runCLI(ctx, cliCall{cli: cli, dir: dir, env: e.cliEnvFor(cli, extra), stdin: "", args: args})
}

// RunIn executes the CLI in dir and returns what it printed. extra holds
// NAME=value pairs added to the environment. stdin is piped to the CLI as
// standard input, or no input when empty. A non-zero exit is reported in
// the Result, not as an error.
func (e *Env) RunIn(
	ctx context.Context, dir string, extra []string, stdin string, args ...string,
) (Result, error) {
	return e.runWithEnv(ctx, dir, e.cliEnv(extra), stdin, args...)
}

// RunSavedLogin runs the CLI in dir with the saved logins and returns what
// it printed. It omits PULUMI_BACKEND_URL and PULUMI_ACCESS_TOKEN, so the
// CLI uses the saved logins in PULUMI_HOME. Extra holds NAME=value pairs
// for one run. A non-zero exit is a Result, not an error.
func (e *Env) RunSavedLogin(ctx context.Context, dir string, extra []string, args ...string) (Result, error) {
	return e.runWithEnv(ctx, dir, e.savedLoginEnv(extra), "", args...)
}

// KillSpec describes one CLI invocation killed at a marker file: the
// harness starts the CLI in its own process group, waits until the file
// exists, kills the whole group with SIGKILL and reaps the CLI. A child of
// the CLI, for example a provisioner, writes the file, so the kill lands
// while that child runs. The wait polls the file, never a fixed sleep.
type KillSpec struct {
	// Name labels the subtest, the report row and the requests the run makes.
	Name string
	// Args are the CLI arguments.
	Args []string
	// Env holds extra NAME=value pairs for this run only.
	Env []string
	// Marker is the path of the file to wait for.
	Marker string
}

// waitForFile reports whether path exists before ctx ends. It polls the
// file system, never sleeps a fixed time.
func waitForFile(ctx context.Context, path string) bool {
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(killPoll):
		}
	}
}

// KillAtMarker runs the CLI with spec.Args and kills its process group with
// SIGKILL once spec.Marker exists. It reaps the CLI, checks that no process
// of the group stays behind, and records the run as the spec.Name step. The
// step passes when the marker appeared and the group is gone; the exit code
// of the killed process is reported, not checked.
func (e *Env) KillAtMarker(ctx context.Context, t *testing.T, spec KillSpec) Result {
	t.Helper()

	var res Result

	t.Run(spec.Name, func(t *testing.T) {
		e.Recorder.SetStep(spec.Name)

		var failure string

		res, failure = e.runAndKill(ctx, spec)

		e.steps = append(e.steps, StepResult{
			Name:     spec.Name,
			Command:  res.Command,
			Failure:  failure,
			Stderr:   clip(res.Stderr),
			ExitCode: res.ExitCode,
			Passed:   failure == "",
			Skipped:  false,
		})

		t.Logf("%s (%s)\nstdout:\n%s\nstderr:\n%s", res.Command, res.Duration.Round(time.Millisecond),
			clip(res.Stdout), clip(res.Stderr))

		if failure != "" {
			t.Errorf("%s: %s", spec.Name, failure)
		}
	})

	return res
}

// joinFailure joins two failure texts without a leading separator.
func joinFailure(first, second string) string {
	if first == "" {
		return second
	}

	return first + "; " + second
}

// WriteTofuConfig replaces the scenario's main.tf with content, so a step
// can change the configuration without a new environment. Callers that add a
// resource needing no provider download keep the run free of downloads.
func (e *Env) WriteTofuConfig(tb testing.TB, content string) {
	tb.Helper()
	e.write(tb, filepath.Join(e.project, "main.tf"), content)
}

// WritePulumiProject replaces the scenario's Pulumi.yaml with content, so an
// import test can use a program with a secret output while the default
// scenario keeps its stack reference program.
func (e *Env) WritePulumiProject(tb testing.TB, content string) {
	tb.Helper()
	e.write(tb, filepath.Join(e.project, "Pulumi.yaml"), content)
}

// StepSpec describes one scenario step.
type StepSpec struct {
	// Check inspects the run; nil means the exit code is enough.
	Check func(Result) error
	// Name labels the subtest, the report row and the requests the step makes.
	Name string
	// Args are the CLI arguments.
	Args []string
	// Env holds extra NAME=value pairs for this step only.
	Env []string
	// Stdin is piped to the CLI as standard input, for steps that answer an
	// interactive prompt. Empty means no input.
	Stdin string
	// Fail expects a non-zero exit. Check then inspects the failed run,
	// for steps that prove an error message.
	Fail bool
	// Redact hides the step output from the log and the report, for steps
	// that carry secrets. The check still sees the full Result.
	Redact bool
}

// redacted marks a redacted step in logs, reports and recorded bodies, so a
// secret value never reaches them.
const redacted = "[redacted]"

// loggedStdout returns what the test log shows for res under spec.
func loggedStdout(res Result, spec StepSpec) string {
	if spec.Redact {
		return redacted
	}

	return clip(res.Stdout)
}

// loggedStderr returns what the test log shows for res under spec.
func loggedStderr(res Result, spec StepSpec) string {
	if spec.Redact {
		return redacted
	}

	return clip(res.Stderr)
}

// storedStderr returns what the report stores for res under spec.
func storedStderr(res Result, spec StepSpec) string {
	if spec.Redact {
		return redacted
	}

	return clip(res.Stderr)
}

// ParallelSpec describes one scenario step that runs several CLI invocations
// at the same time, for operations that must not block each other.
type ParallelSpec struct {
	// Check inspects every successful run; nil means zero exits are enough.
	Check func([]Result) error
	// Name labels the subtest, the report row and the requests the runs make.
	Name string
	// Args holds one CLI argument list per invocation.
	Args [][]string
	// Env holds extra NAME=value pairs for every invocation of this step.
	Env []string
	// Envs holds extra NAME=value pairs per invocation. When it holds one
	// entry per argument list, each run uses its own entry instead of Env,
	// so two principals can contend for one lock.
	Envs [][]string
}

// StepParallel runs every argument list in spec at the same time and records
// one step. It fails the subtest on a non-zero exit or when spec.Check
// returns an error. A failing step does not stop later steps.
func (e *Env) StepParallel(ctx context.Context, t *testing.T, spec ParallelSpec) []Result {
	t.Helper()

	res := make([]Result, len(spec.Args))

	t.Run(spec.Name, func(t *testing.T) {
		e.Recorder.SetStep(spec.Name)

		type outcome struct {
			res Result
			err error
		}

		outs := make([]outcome, len(spec.Args))

		var wg sync.WaitGroup

		for i, args := range spec.Args {
			wg.Go(func() {
				outs[i].res, outs[i].err = e.Run(ctx, parallelEnv(spec, i), args...)
			})
		}

		wg.Wait()

		var failure string

		for i := range outs {
			res[i] = outs[i].res

			t.Logf("%s (%s)\nstdout:\n%s\nstderr:\n%s", res[i].Command,
				res[i].Duration.Round(time.Millisecond), clip(res[i].Stdout), clip(res[i].Stderr))

			switch {
			case failure != "":
			case outs[i].err != nil:
				failure = outs[i].err.Error()
			case res[i].ExitCode != 0:
				failure = fmt.Sprintf("exit code %d", res[i].ExitCode)
			default:
			}
		}

		if failure == "" && spec.Check != nil {
			if checkErr := spec.Check(res); checkErr != nil {
				failure = checkErr.Error()
			}
		}

		e.steps = append(e.steps, StepResult{
			Name:     spec.Name,
			Command:  parallelCommand(spec.Args),
			Failure:  failure,
			Stderr:   clip(parallelStderr(res)),
			ExitCode: parallelExit(res, failure),
			Passed:   failure == "",
			Skipped:  false,
		})

		if failure != "" {
			t.Errorf("%s: %s", spec.Name, failure)
		}
	})

	return res
}

// parallelEnv returns the extra environment for run i: its own entry when
// Envs holds one per argument list, else the shared Env.
func parallelEnv(spec ParallelSpec, i int) []string {
	if len(spec.Envs) == len(spec.Args) {
		return spec.Envs[i]
	}

	return spec.Env
}

// parallelCommand joins several argument lists into one report command.
func parallelCommand(argSets [][]string) string {
	parts := make([]string, 0, len(argSets))
	for _, args := range argSets {
		parts = append(parts, strings.Join(args, " "))
	}

	return strings.Join(parts, " & ")
}

// parallelStderr joins the stderr of parallel runs for the report.
func parallelStderr(res []Result) string {
	parts := make([]string, 0, len(res))
	for _, r := range res {
		parts = append(parts, r.Stderr)
	}

	return strings.Join(parts, "\n")
}

// parallelExit reports the first non-zero exit, or zero when failure names a
// transport error rather than an exit.
func parallelExit(res []Result, failure string) int {
	for _, r := range res {
		if r.ExitCode != 0 {
			return r.ExitCode
		}
	}

	if failure != "" {
		return 1
	}

	return 0
}

// Step runs spec as a subtest. It fails the subtest on an unexpected exit
// (non-zero, or zero when spec.Fail expects a failure) or when spec.Check
// returns an error, and records the outcome for the report. A failing step
// does not stop later steps: the goal is to see every gap.
func (e *Env) Step(ctx context.Context, t *testing.T, spec StepSpec) Result {
	t.Helper()

	return e.StepIn(ctx, t, e.project, spec)
}

// RemoteStep runs spec in the remote-backend directory as a subtest. It
// fails the same way as Step.
func (e *Env) RemoteStep(ctx context.Context, t *testing.T, spec StepSpec) Result {
	t.Helper()

	if e.remoteProject == "" {
		t.Fatal("remote project is not written")
	}

	return e.StepIn(ctx, t, e.remoteProject, spec)
}

// WriteRemoteProject writes the remote-backend part in its own directory, so
// workspace new, list and select run apart from the cloud-block part.
func (e *Env) WriteRemoteProject(tb testing.TB) string {
	tb.Helper()

	dir := filepath.Join(filepath.Dir(e.project), "project-remote")
	if err := os.MkdirAll(dir, dirMode); err != nil {
		tb.Fatalf("create %s: %v", dir, err)
	}

	e.write(tb, filepath.Join(dir, "main.tf"), fmt.Sprintf(tofuRemoteConfig, e.Host(), Namespace, RemotePrefix))
	e.remoteProject = dir

	return dir
}

// StepIn runs spec in dir as a subtest. It fails the subtest on an
// unexpected exit or when spec.Check returns an error, and records the
// outcome for the report. A failing step does not stop later steps.
func (e *Env) StepIn(ctx context.Context, t *testing.T, dir string, spec StepSpec) Result {
	t.Helper()

	return e.stepInWith(ctx, t, dir, spec, func(
		ctx context.Context, dir string, extra []string, stdin string, args ...string,
	) (Result, error) {
		return e.runWithEnv(ctx, dir, e.cliEnv(extra), stdin, args...)
	})
}

// StepSavedLogin runs spec in the project directory with the saved logins
// and records it for the report. It fails the subtest on an unexpected exit
// or a rejected check. A failing step does not stop later steps.
func (e *Env) StepSavedLogin(ctx context.Context, t *testing.T, spec StepSpec) Result {
	t.Helper()

	return e.StepSavedLoginIn(ctx, t, e.project, spec)
}

// StepSavedLoginIn runs spec in dir with the saved logins and records it
// for the report. It fails the same way as StepSavedLogin.
func (e *Env) StepSavedLoginIn(
	ctx context.Context, t *testing.T, dir string, spec StepSpec,
) Result {
	t.Helper()

	return e.stepInWith(ctx, t, dir, spec, func(
		ctx context.Context, dir string, extra []string, stdin string, args ...string,
	) (Result, error) {
		return e.runWithEnv(ctx, dir, e.savedLoginEnv(extra), stdin, args...)
	})
}

// StatusResponse is the outcome of StatusWithHeader: the status, the
// response headers and the body.
type StatusResponse struct {
	Status int
	Header http.Header
	Body   string
}

// StatusWithHeader sends one request with auth as the whole Authorization
// header value (empty for none) and returns its status, headers and body.
// Tests that check refusal headers use it. It uses the test server's
// client, which trusts the self-signed certificate.
func (e *Env) StatusWithHeader(
	ctx context.Context, auth, method, path, body string,
) (StatusResponse, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, reader)
	if err != nil {
		return StatusResponse{}, fmt.Errorf("build %s %s: %w", method, path, err)
	}

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return StatusResponse{}, fmt.Errorf("%s %s: %w", method, path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return StatusResponse{}, fmt.Errorf("read %s %s: %w", method, path, err)
	}

	return StatusResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(respBody)}, nil
}

// Home returns the home directory of the harness: the CLI configuration files
// and the saved logins live there.
func (e *Env) Home() string {
	return e.home
}

// WriteFile writes content to name, creating its directory, and fails the
// test on error.
func (e *Env) WriteFile(tb testing.TB, name, content string) {
	tb.Helper()
	e.write(tb, name, content)
}

// stepInWith runs spec in dir through run and records it for the report.
// StepIn passes RunIn; the saved-login steps pass RunSavedLogin.
func (e *Env) stepInWith(
	ctx context.Context,
	t *testing.T,
	dir string,
	spec StepSpec,
	run func(context.Context, string, []string, string, ...string) (Result, error),
) Result {
	t.Helper()

	var res Result

	t.Run(spec.Name, func(t *testing.T) {
		e.Recorder.SetStep(spec.Name)

		var failure string

		var err error

		res, err = run(ctx, dir, spec.Env, spec.Stdin, spec.Args...)

		switch {
		case err != nil:
			failure = err.Error()
		case spec.Fail && res.ExitCode == 0:
			failure = "expected a non-zero exit"
		case !spec.Fail && res.ExitCode != 0:
			failure = fmt.Sprintf("exit code %d", res.ExitCode)
		case spec.Check != nil:
			if checkErr := spec.Check(res); checkErr != nil {
				failure = checkErr.Error()
			}
		default:
		}

		if spec.Redact {
			e.Recorder.RedactSteps(spec.Name)
		}

		e.steps = append(e.steps, StepResult{
			Name:     spec.Name,
			Command:  res.Command,
			Failure:  failure,
			Stderr:   storedStderr(res, spec),
			ExitCode: res.ExitCode,
			Passed:   failure == "",
			Skipped:  false,
		})

		t.Logf("%s (%s)\nstdout:\n%s\nstderr:\n%s", res.Command, res.Duration.Round(time.Millisecond),
			loggedStdout(res, spec), loggedStderr(res, spec))

		if failure != "" {
			t.Errorf("%s: %s", res.Command, failure)
		}
	})

	return res
}

// runWithEnv executes the CLI in dir with env and returns what it printed.
// stdin is piped to the CLI as standard input, or no input when empty. A
// non-zero exit is reported in the Result, not as an error.
func (e *Env) runWithEnv(
	ctx context.Context, dir string, env []string, stdin string, args ...string,
) (Result, error) {
	return e.runCLI(ctx, cliCall{cli: e.cli, dir: dir, env: env, stdin: stdin, args: args})
}

// cliCall groups one CLI process invocation: the tool with the directory,
// environment, standard input and arguments.
type cliCall struct {
	cli   Install
	dir   string
	env   []string
	stdin string
	args  []string
}

// runCLI executes one CLI call in its directory and returns what it printed.
// stdin is piped to the CLI as standard input, or no input when empty. A
// non-zero exit is reported in the Result, not as an error.
func (e *Env) runCLI(ctx context.Context, call cliCall) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer

	//nolint:gosec // The binary is the CLI this suite downloaded and checksum-verified.
	cmd := exec.CommandContext(ctx, call.cli.Binary(), call.args...)
	cmd.Dir = call.dir
	cmd.Env = call.env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay

	if call.stdin != "" {
		cmd.Stdin = strings.NewReader(call.stdin)
	}

	start := time.Now()
	err := cmd.Run()
	timedOut := ctx.Err()
	res := Result{
		Command:  call.cli.Tool + " " + strings.Join(call.args, " "),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
		ExitCode: 0,
	}

	if timedOut != nil {
		// The kill shows up as an exit error; name the real cause instead.
		return res, fmt.Errorf("%s did not finish within %s: %w", res.Command, commandTimeout, timedOut)
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		res.ExitCode = exitErr.ExitCode()

		return res, nil
	}

	if err != nil {
		return res, fmt.Errorf("run %s: %w", res.Command, err)
	}

	return res, nil
}

// runAndKill starts the CLI with spec.Args in a new process group, waits
// for spec.Marker, kills the group with SIGKILL and reaps the CLI, so no
// process stays behind. It returns the run and the step failure, or "" when
// the marker appeared and the kill reached the whole group. The exit code of
// the killed process is reported, not checked.
func (e *Env) runAndKill(ctx context.Context, spec KillSpec) (Result, string) {
	killCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer

	//nolint:gosec // The binary is the CLI this suite downloaded and checksum-verified.
	cmd := exec.CommandContext(killCtx, e.cli.Binary(), spec.Args...)
	cmd.Dir = e.project
	cmd.Env = e.cliEnv(spec.Env)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	// The CLI and its children share a new process group, so the kill
	// reaches a child that the CLI started, for example a provisioner.
	cmd.SysProcAttr = new(syscall.SysProcAttr)
	cmd.SysProcAttr.Setpgid = true

	res := Result{
		Command:  e.cli.Tool + " " + strings.Join(spec.Args, " "),
		Stdout:   "",
		Stderr:   "",
		Duration: 0,
		ExitCode: 1,
	}

	start := time.Now()

	if err := cmd.Start(); err != nil {
		return res, fmt.Sprintf("start %s: %v", spec.Args, err)
	}

	waitCtx, waitCancel := context.WithTimeout(killCtx, killWait)
	seen := waitForFile(waitCtx, spec.Marker)
	waitCancel()

	failure := ""
	if !seen {
		failure = fmt.Sprintf("no marker file %s within %s", spec.Marker, killWait)
	}

	// The process group ID is the PID of the CLI (Setpgid above).
	pgid := cmd.Process.Pid
	if killErr := syscall.Kill(-pgid, syscall.SIGKILL); killErr != nil {
		failure = joinFailure(failure, fmt.Sprintf("kill process group: %v", killErr))
	}

	// Reap the process, so the kill leaves none behind. The exit error of a
	// killed process is the expected outcome, not a failure. ErrWaitDelay
	// means that a child still held the output of the CLI open after the
	// kill, so the kill missed it.
	if waitErr := cmd.Wait(); errors.Is(waitErr, exec.ErrWaitDelay) {
		failure = joinFailure(failure, "a child of the CLI kept its output open after the kill")
	}

	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	res.Duration = time.Since(start)

	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}

	return res, failure
}

// apiCall sends one authenticated request and decodes a 200 JSON response
// into req.Out (when not nil). Any other status fails the test with the
// response body, so error paths stay visible in the log.
func (e *Env) apiCall(ctx context.Context, tb testing.TB, req apiRequest) {
	tb.Helper()

	var reader io.Reader
	if req.Body != "" {
		reader = strings.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, e.URL+req.Path, reader)
	if err != nil {
		tb.Fatalf("build %s %s: %v", req.Method, req.Path, err)
	}

	httpReq.Header.Set("Authorization", req.Auth)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		tb.Fatalf("%s %s: %v", req.Method, req.Path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		tb.Fatalf("read %s %s: %v", req.Method, req.Path, err)
	}

	if resp.StatusCode != http.StatusOK {
		tb.Fatalf("%s %s answers %d: %s", req.Method, req.Path, resp.StatusCode, string(respBody))
	}

	if req.Out != nil {
		if err := json.Unmarshal(respBody, req.Out); err != nil {
			tb.Fatalf("decode %s %s: %v (%s)", req.Method, req.Path, err, string(respBody))
		}
	}
}

// seedRequest is one TFE API request of SeedTofuState as principal A: the
// method, the path, the body ("" sends none), the status it must answer and
// the document to decode the answer into (nil decodes nothing).
type seedRequest struct {
	method string
	path   string
	body   string
	want   int
	out    any
}

// seedCall sends one seed request and fails the test when the status differs
// from req.want.
func (e *Env) seedCall(ctx context.Context, tb testing.TB, req seedRequest) {
	tb.Helper()

	resp, err := e.TFEStatusHeaders(ctx, e.token, req.method, req.path, req.body)
	if err != nil {
		tb.Fatalf("seed %s %s: %v", req.method, req.path, err)
	}

	if resp.Status != req.want {
		tb.Fatalf("seed %s %s answers %d, want %d: %s", req.method, req.path, resp.Status, req.want, resp.Body)
	}

	if req.out == nil {
		return
	}

	if err := json.Unmarshal([]byte(resp.Body), req.out); err != nil {
		tb.Fatalf("decode seed %s %s: %v (%s)", req.method, req.path, err, resp.Body)
	}
}

// writeTofuFiles writes main.tf, a CLI configuration holding the token of
// principal A, another holding the token of B, and the server certificate.
func (e *Env) writeTofuFiles(tb testing.TB, ts *httptest.Server) {
	tb.Helper()

	cert := pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Headers: nil, Bytes: ts.Certificate().Raw})
	e.write(tb, filepath.Join(e.home, "server.pem"), string(cert))
	e.write(tb, filepath.Join(e.home, "tofurc"), fmt.Sprintf(tofuCLIConfig, e.Host(), e.token))
	e.write(tb, filepath.Join(e.home, "tofurc-b"), fmt.Sprintf(tofuCLIConfig, e.Host(), e.tokenB))
	e.write(tb, filepath.Join(e.project, "main.tf"), fmt.Sprintf(tofuConfig, e.Host(), Namespace, Workspace))
}

func (e *Env) write(tb testing.TB, name, content string) {
	tb.Helper()

	if err := os.WriteFile(name, []byte(content), fileMode); err != nil {
		tb.Fatalf("write %s: %v", name, err)
	}
}

// cliEnv builds the CLI environment from scratch, so nothing from the
// developer's own Pulumi or OpenTofu setup leaks into a run.
func (e *Env) cliEnv(extra []string) []string {
	return e.cliEnvFor(e.cli, extra)
}

// cliEnvFor builds the CLI environment of tool cli from scratch, so nothing
// from the developer's own Pulumi or OpenTofu setup leaks into a run.
func (e *Env) cliEnvFor(cli Install, extra []string) []string {
	env := make([]string, 0, baseEnvEntries+len(extra))
	env = append(env,
		"PATH="+cli.Dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+e.home,
		"NO_COLOR=1",
		"NO_PROXY=127.0.0.1,localhost",
	)

	if cli.Tool == ToolTofu {
		env = append(env,
			"TF_CLI_CONFIG_FILE="+filepath.Join(e.home, "tofurc"),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
		)
	} else {
		env = append(env,
			"PULUMI_HOME="+filepath.Join(e.home, ".pulumi"),
			"PULUMI_BACKEND_URL="+e.URL,
			"PULUMI_ACCESS_TOKEN="+e.token,
			// The update check is asynchronous and would make the request log vary between runs.
			"PULUMI_SKIP_UPDATE_CHECK=true",
		)
	}

	if e.tls {
		// Go reads SSL_CERT_FILE on Linux only; see the OpenTofu test.
		// Both CLIs are Go programs, so the Pulumi CLI trusts a TLS
		// server the same way when they share one. A pair trusts both
		// servers through a bundle file holding both certificates.
		bundle := filepath.Join(e.home, "server.pem")
		if e.certBundle != "" {
			bundle = e.certBundle
		}

		env = append(env, "SSL_CERT_FILE="+bundle)
	}

	// Later entries win, so extra can override anything above.
	return append(env, extra...)
}

// savedLoginEnv builds the CLI environment without PULUMI_BACKEND_URL and
// without PULUMI_ACCESS_TOKEN, so the CLI uses the saved logins in
// PULUMI_HOME. Extra holds NAME=value pairs for one run. OpenTofu has no
// backend variables, so its environment is unchanged.
func (e *Env) savedLoginEnv(extra []string) []string {
	env := make([]string, 0, baseEnvEntries+len(extra))
	env = append(env,
		"PATH="+e.cli.Dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+e.home,
		"NO_COLOR=1",
		"NO_PROXY=127.0.0.1,localhost",
	)

	if e.cli.Tool == ToolTofu {
		env = append(env,
			"TF_CLI_CONFIG_FILE="+filepath.Join(e.home, "tofurc"),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
		)
	} else {
		env = append(env,
			"PULUMI_HOME="+filepath.Join(e.home, ".pulumi"),
			"PULUMI_SKIP_UPDATE_CHECK=true",
		)
	}

	if e.tls {
		bundle := filepath.Join(e.home, "server.pem")
		if e.certBundle != "" {
			bundle = e.certBundle
		}

		env = append(env, "SSL_CERT_FILE="+bundle)
	}

	return append(env, extra...)
}

// saveHeldDeployment saves the exported deployment with extra fields merged
// into it through the checkpoint route with the held update's lease token.
func (e *Env) saveHeldDeployment(
	ctx context.Context, tb testing.TB, held HeldUpdate, exportStdout string, extra map[string]string,
) {
	tb.Helper()

	var doc struct {
		Deployment map[string]any `json:"deployment"`
		Version    int            `json:"version"`
	}

	if err := json.Unmarshal([]byte(exportStdout), &doc); err != nil {
		tb.Fatalf("decode stack export: %v", err)
	}

	if doc.Deployment == nil {
		tb.Fatalf("decode stack export: no deployment to save")
	}

	for key, value := range extra {
		doc.Deployment[key] = value
	}

	raw, err := json.Marshal(doc.Deployment)
	if err != nil {
		tb.Fatalf("encode checkpoint: %v", err)
	}

	prefix := "/api/stacks/" + held.Namespace + "/" + held.Project + "/" + held.Name

	e.apiCall(ctx, tb, apiRequest{
		Method: http.MethodPatch,
		Path:   prefix + "/update/" + held.ID + "/checkpoint",
		Auth:   "update-token " + held.Lease,
		Body:   fmt.Sprintf(`{"version":%d,"deployment":%s}`, doc.Version, raw),
		Out:    nil,
	})
}

// errNotHTTPTransport reports that the harness client has no
// *http.Transport, so no request can leave from another source address.
var errNotHTTPTransport = errors.New("client transport is not an *http.Transport")

// serverRequest is one installation API request: the client that sends it,
// the Bearer token ("" sends none), the method, the path and the body (""
// sends none).
type serverRequest struct {
	client *http.Client
	token  string
	method string
	path   string
	body   string
}

// serverStatus sends one installation API request and returns its status and
// body. An empty token sends no Authorization header.
func (e *Env) serverStatus(
	ctx context.Context, token, method, path, body string,
) (int, string, error) {
	return e.serverStatusWith(ctx, serverRequest{
		client: e.client, token: token, method: method, path: path, body: body,
	})
}

// serverStatusWith sends req to the installation API and returns its status
// and body.
func (e *Env) serverStatusWith(ctx context.Context, req serverRequest) (int, string, error) {
	var reader io.Reader
	if req.body != "" {
		reader = strings.NewReader(req.body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.method, e.URL+req.path, reader)
	if err != nil {
		return 0, "", fmt.Errorf("build %s %s: %w", req.method, req.path, err)
	}

	if req.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.token)
	}

	if req.body != "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := req.client.Do(httpReq)
	if err != nil {
		return 0, "", fmt.Errorf("%s %s: %w", req.method, req.path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", fmt.Errorf("read %s %s: %w", req.method, req.path, err)
	}

	return resp.StatusCode, string(respBody), nil
}

// createNamespaceThroughAPI creates name through POST /api/server/namespaces
// with the environment token and fails the test on any other status. A new
// installation holds no namespace, so the first call proves the bootstrap
// admin token works on an empty installation.
func (e *Env) createNamespaceThroughAPI(ctx context.Context, tb testing.TB, name string) {
	tb.Helper()

	status, body, err := e.serverStatus(
		ctx,
		e.token,
		http.MethodPost,
		"/api/server/namespaces",
		`{"name":`+strconv.Quote(name)+`}`,
	)
	if err != nil {
		tb.Fatalf("POST /api/server/namespaces %q: %v", name, err)
	}

	if status != http.StatusCreated {
		tb.Fatalf("POST /api/server/namespaces %q answers %d, want %d (%s)", name, status, http.StatusCreated, body)
	}

	var doc struct {
		Name                string `json:"name"`
		PrimaryInstallation string `json:"primaryInstallation"`
		FailoverVersion     int64  `json:"failoverVersion"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		tb.Fatalf("decode namespace %q %q: %v", name, body, err)
	}

	if doc.Name != name || doc.PrimaryInstallation != installationName || doc.FailoverVersion != 1 {
		tb.Fatalf("namespace answers %+v, want %q in %q at version 1", doc, name, installationName)
	}

	e.SeedCatchAllPlacement(ctx, tb, name)
}

// tfeStatusAs is TFEStatus for one tfeCall.
func (e *Env) tfeStatusAs(ctx context.Context, token string, call tfeCall) (int, string, error) {
	method, path, body, userAgent := call.method, call.path, call.body, call.userAgent

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, reader)
	if err != nil {
		return 0, "", fmt.Errorf("build %s %s: %w", method, path, err)
	}

	// An empty token sends no credential, for a route that needs none.
	switch {
	case token == "":
	case call.scheme == authSchemeToken:
		req.Header.Set("Authorization", "token "+token)
	default:
		req.Header.Set("Authorization", "Bearer "+token)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/vnd.api+json")
	}

	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	for k, v := range call.headers {
		req.Header.Set(k, v)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("%s %s: %w", method, path, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", fmt.Errorf("read %s %s: %w", method, path, err)
	}

	return resp.StatusCode, string(respBody), nil
}

// clip shortens s for logs and reports.
func clip(s string) string {
	if len(s) <= maxShown {
		return s
	}

	return s[:maxShown] + "…"
}

func (e *Env) apiStepWith(
	ctx context.Context, t *testing.T, token string, spec APIStepSpec, headers map[string]string,
) {
	t.Helper()

	t.Run(spec.Name, func(t *testing.T) {
		e.Recorder.SetStep(spec.Name)

		var failure string

		var exitCode int

		status, respBody, err := e.tfeStatusAs(ctx, token, tfeCall{
			method:    spec.Method,
			path:      spec.Path,
			body:      spec.Body,
			userAgent: spec.UserAgent,
			scheme:    spec.Scheme,
			headers:   headers,
		})

		switch {
		case err != nil:
			failure = err.Error()
			exitCode = 1
		case status != spec.Want:
			failure = fmt.Sprintf("answers %d, want %d: %s", status, spec.Want, respBody)
			exitCode = 1
		case spec.Check != nil:
			if checkErr := spec.Check(respBody); checkErr != nil {
				failure = checkErr.Error()
				exitCode = 1
			}
		default:
		}

		e.steps = append(e.steps, StepResult{
			Name:     spec.Name,
			Command:  spec.Method + " " + spec.Path,
			Failure:  failure,
			Stderr:   clip(respBody),
			ExitCode: exitCode,
			Passed:   failure == "",
			Skipped:  false,
		})

		if failure != "" {
			t.Errorf("%s %s: %s", spec.Method, spec.Path, failure)
		}
	})
}
