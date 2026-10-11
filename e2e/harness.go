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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	dustpan "opendustpan.org/opendustpan"
	"opendustpan.org/opendustpan/blob/fs"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/inventory"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/secret"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/sqlite"
	"opendustpan.org/opendustpan/workspace"
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

	region         = "e2e"
	tokenBytes     = 24
	commandTimeout = 2 * time.Minute
	waitDelay      = 5 * time.Second
	// killWait bounds the wait for the recorded request that triggers the
	// kill of a running CLI. It stays well under commandTimeout, so the
	// kill and the reap always finish before the step times out.
	killWait = time.Minute
	// killPoll is how often the kill helper checks the recorder for the
	// request it waits for.
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
	server   serverStore
	client   *http.Client
	// workspaces runs the workspace use cases the seed helpers need.
	workspaces *workspace.Service
	token      string
	tokenB     string
	home       string
	project    string
	// remoteProject holds the remote-backend part in its own directory.
	remoteProject string
	steps         []StepResult
	// tls reports whether the server speaks HTTPS. OpenTofu always needs
	// it; the Pulumi CLI needs it only on a TLS server, where it trusts
	// the certificate through SSL_CERT_FILE.
	tls bool
	// binPath is the server binary newBinaryEnv built. It is empty in the
	// in-process mode, where RunBinary, StopServer and StartServer fail.
	binPath string
	// backendCfg holds the database URL, blob directory, certificate and
	// key files and tokens of the binary backend.
	backendCfg backendConfig
	// backendPort is the loopback port the binary backend serves. A restart
	// reuses it, so the proxy target and the saved CLI logins stay valid.
	backendPort string
	// backendCmd is the running binary server process, or nil while stopped.
	backendCmd *exec.Cmd
	// backendLogs holds the combined output of backendCmd.
	backendLogs *bytes.Buffer
	// certBundle holds a PEM bundle with the certificates of both servers
	// of a pair. Empty for a single server, which trusts server.pem.
	certBundle string
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
}

// NewEnv starts a server on a random local port, backed by a fresh database
// with the Namespace namespace, and prepares an empty home directory and a
// scratch project for cli. SQLite uses a temporary file; PostgreSQL uses an
// isolated schema that is dropped when the test ends. OpenTofu gets an HTTPS
// server, because its service discovery refuses plain HTTP, and the server's
// certificate is the only one it trusts. Everything is removed when the test ends.
//
// With E2E_SERVER=binary the server is the real cmd/opendustpan binary run
// with --tls-cert and --tls-key, fronted by a recording proxy. That variant
// is the proof for the milestone 2 exit test.
func NewEnv(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	dir := tb.TempDir()
	token := newToken(tb)
	tokenB := newToken(tb)
	rec := &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}}
	in := envInput{tb: tb, cli: cli, store: store, dir: dir, token: token, tokenB: tokenB, rec: rec}

	if UseBinaryServer() {
		return newBinaryEnv(ctx, in)
	}

	return newInprocessEnv(ctx, in)
}

// newInprocessEnv starts an in-process server for cli and returns the
// environment.
func newInprocessEnv(ctx context.Context, in envInput) *Env {
	tb := in.tb
	tb.Helper()

	handler, st, workspaces := newServer(
		ctx,
		tb,
		in.store,
		filepath.Join(in.dir, "dustpan.db"),
		serverTokens{tokenA: in.token, tokenB: in.tokenB},
	)

	ts := httptest.NewUnstartedServer(in.rec.Middleware(handler))
	if in.cli.Tool == ToolTofu {
		ts.StartTLS()
	} else {
		ts.Start()
	}

	tb.Cleanup(ts.Close)

	client := ts.Client()
	client.Timeout = commandTimeout

	env := &Env{
		URL:           ts.URL,
		Recorder:      in.rec,
		cli:           in.cli,
		store:         in.store,
		server:        st,
		client:        client,
		workspaces:    workspaces,
		token:         in.token,
		tokenB:        in.tokenB,
		home:          filepath.Join(in.dir, "home"),
		project:       filepath.Join(in.dir, "project"),
		remoteProject: "",
		steps:         nil,
		tls:           in.cli.Tool == ToolTofu,
		binPath:       "",
		backendCfg:    backendConfig{}, //nolint:exhaustruct_v5 // The in-process mode has no binary backend.
		backendPort:   "",
		backendCmd:    nil,
		backendLogs:   nil,
		certBundle:    "",
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
}

// newServer opens store (a fresh SQLite file at dbPath, or an isolated
// PostgreSQL schema), creates the Namespace namespace and returns the server's
// handler, store and workspace service. The store and its schema are closed
// and removed when the test ends.
// the environment needs the interface.
//
//nolint:ireturn // SQLite and PostgreSQL stores share no concrete type;
func newServer(
	ctx context.Context,
	tb testing.TB,
	store Store,
	dbPath string,
	tokens serverTokens,
) (http.Handler, serverStore, *workspace.Service) {
	tb.Helper()

	var st serverStore

	switch store {
	case StoreSQLite:
		st = openSQLiteStore(ctx, tb, dbPath)
	case StorePostgres:
		st = openPostgresStore(ctx, tb, PostgresURL())
	default:
		tb.Fatalf("unknown store: %s", store)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: region})
	if err != nil {
		tb.Fatalf("namespace service: %v", err)
	}

	if _, err = namespaces.Create(ctx, Namespace); err != nil {
		tb.Fatalf("create namespace: %v", err)
	}

	authn, err := identity.NewStaticTokens(tokens.tokenA, tokens.tokenB)
	if err != nil {
		tb.Fatalf("static tokens: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(testKeyEncryptionKey))
	if err != nil {
		tb.Fatalf("key wrapper: %v", err)
	}

	// The start-up check refuses a key-encryption key that matches none of
	// the wrapped keys in the database (ADR 23).
	if checkErr := secret.CheckWrapper(ctx, st, wrapper); checkErr != nil {
		tb.Fatalf("check key wrapper: %v", checkErr)
	}

	blobs := openTestBlobStore(tb, dbPath)
	workspaces := newWorkspaceService(tb, st, blobs, namespaces)

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(region),
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
	)
	if err != nil {
		tb.Fatalf("new server: %v", err)
	}

	return srv.Handler(), st, workspaces
}

// newWorkspaceService returns the workspace use cases over st and blobs,
// for the seed helpers.
func newWorkspaceService(
	tb testing.TB, st serverStore, blobs *fs.Store, namespaces *namespace.Service,
) *workspace.Service {
	tb.Helper()

	workspaces, err := workspace.NewService(workspace.Config{
		Workspaces:    st,
		States:        st,
		Blobs:         blobs,
		Namespaces:    namespaces,
		Now:           nil,
		MaxStateBytes: 0,
	})
	if err != nil {
		tb.Fatalf("workspace service: %v", err)
	}

	return workspaces
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

// openTestBlobStore opens a blob store in the test directory holding dbPath.
func openTestBlobStore(tb testing.TB, dbPath string) *fs.Store {
	tb.Helper()

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
// latest checkpoint of the stack named stackName in Project. It exists so
// delete tests can set up a stack with resources without driving a full
// update through routes that later issues own.
func (e *Env) SeedResources(ctx context.Context, tb testing.TB, stackName string) {
	tb.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: e.server, Now: nil, Region: region})
	if err != nil {
		tb.Fatalf("namespace service: %v", err)
	}

	ref, err := stack.ParseRef(Namespace, Project, stackName)
	if err != nil {
		tb.Fatalf("parse ref: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            e.server,
		Checkpoints:        e.server,
		Events:             e.server,
		Namespaces:         namespaces,
		Now:                nil,
		MaxCheckpointBytes: 0,
	})
	if err != nil {
		tb.Fatalf("updates service: %v", err)
	}

	u, err := updates.Create(ctx, ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0,
	})
	if err != nil {
		tb.Fatalf("create update: %v", err)
	}

	started, err := updates.Start(ctx, ref, u.ID)
	if err != nil {
		tb.Fatalf("start update: %v", err)
	}

	err = updates.SaveCheckpoint(ctx, ref, u.ID, started.Token, stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(seedDeployment),
		SchemaVersion: seedSchemaVersion,
		Sequence:      0,
		Invalid:       false,
	})
	if err != nil {
		tb.Fatalf("save checkpoint: %v", err)
	}

	if _, err := updates.Complete(ctx, ref, u.ID, started.Token, stack.StatusSucceeded); err != nil {
		tb.Fatalf("complete update: %v", err)
	}
}

const (
	// seedHolder locks the workspace while SeedTofuState writes.
	seedHolder = "seed"
	// seedStateLineage is the lineage SeedTofuState writes when the workspace
	// has no state yet.
	seedStateLineage = "seed-lineage"
	// seedStateOutputs is the json-state-outputs document SeedTofuState
	// writes: the greeting output with its cty value and type, plus a
	// sensitive output so the run also proves the single-output route the
	// CLI uses for it.
	seedStateOutputs = `{"greeting":{"sensitive":false,"value":"hello","type":"string"},` +
		`"secret":{"sensitive":true,"value":"s3cr3t","type":"string"}}`
)

// seedStateDocument returns a minimal tfstate document with lineage, serial
// and the greeting output.
func seedStateDocument(lineage string, serial int64) []byte {
	doc := `{"version":4,"terraform_version":"1.11.0","serial":` + strconv.FormatInt(serial, 10) +
		`,"lineage":` + strconv.Quote(lineage) +
		`,"outputs":{"greeting":{"value":"hello","type":"string"}},"resources":[]}`

	return []byte(doc)
}

// SeedTofuState writes a finalized state version with a greeting output
// straight to the store for the scenario workspace, as SeedResources does
// for Pulumi. It chains the lineage and serial after the current state when
// one exists. It locks as seed and releases the lock, so later steps see an
// unlocked workspace with state.
func (e *Env) SeedTofuState(ctx context.Context, tb testing.TB) workspace.StateVersion {
	tb.Helper()

	ns, err := namespace.ParseName(Namespace)
	if err != nil {
		tb.Fatalf("parse namespace %q: %v", Namespace, err)
	}

	name, err := workspace.ParseName(Workspace)
	if err != nil {
		tb.Fatalf("parse workspace %q: %v", Workspace, err)
	}

	ws, err := e.server.GetWorkspace(ctx, workspace.Ref{Namespace: ns, Name: name})
	if err != nil {
		tb.Fatalf("get workspace %s/%s: %v", Namespace, Workspace, err)
	}

	if _, lockErr := e.workspaces.Lock(ctx, ws.ID, seedHolder, "seed"); lockErr != nil {
		tb.Fatalf("lock workspace for seed: %v", lockErr)
	}

	lineage, serial := seedStateLineage, int64(1)

	current, currentErr := e.workspaces.Current(ctx, ws.ID)

	switch {
	case currentErr == nil:
		lineage, serial = current.Lineage, current.Serial+1
	case errors.Is(currentErr, workspace.ErrNoState):
	default:
		tb.Fatalf("current state: %v", currentErr)
	}

	content := seedStateDocument(lineage, serial)
	sum := md5.Sum(content) //nolint:gosec // The TFE protocol names state by MD5 for integrity.

	pending, beginErr := e.workspaces.BeginState(ctx, ws.ID, seedHolder, workspace.NewState{
		Lineage: lineage,
		MD5:     hex.EncodeToString(sum[:]),
		Outputs: []byte(seedStateOutputs),
		Serial:  serial,
		Force:   false,
	})
	if beginErr != nil {
		tb.Fatalf("begin seed state: %v", beginErr)
	}

	sv, uploadErr := e.workspaces.Upload(ctx, pending.Version.ID, pending.Upload, bytes.NewReader(content))
	if uploadErr != nil {
		tb.Fatalf("upload seed state: %v", uploadErr)
	}

	if _, unlockErr := e.workspaces.Unlock(ctx, ws.ID, seedHolder); unlockErr != nil {
		tb.Fatalf("unlock workspace after seed: %v", unlockErr)
	}

	return sv
}

// CreateNamespace creates a namespace homed in the test region, for steps
// that need more than the Namespace namespace NewEnv creates.
func (e *Env) CreateNamespace(ctx context.Context, tb testing.TB, name string) {
	tb.Helper()

	nsName, err := namespace.ParseName(name)
	if err != nil {
		tb.Fatalf("parse namespace %q: %v", name, err)
	}

	err = e.server.CreateNamespace(ctx, namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            nsName,
		HomeRegion:      region,
		FailoverVersion: 1,
	})
	if err != nil {
		tb.Fatalf("create namespace %q: %v", name, err)
	}
}

// ListInventory returns the inventory entries of ns, so scenarios can prove
// a CLI step published its unit through a direct store call.
func (e *Env) ListInventory(ctx context.Context, ns string) ([]inventory.Entry, error) {
	name, err := namespace.ParseName(ns)
	if err != nil {
		return nil, fmt.Errorf("parse namespace %q: %w", ns, err)
	}

	entries, err := e.server.ListUnits(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("list inventory of %q: %w", ns, err)
	}

	return entries, nil
}

// Steps returns the scenario steps run so far.
func (e *Env) Steps() []StepResult {
	return append([]StepResult(nil), e.steps...)
}

// Install returns the CLI under test.
func (e *Env) Install() Install {
	return e.cli
}

// Store returns the database backend this environment runs against.
func (e *Env) Store() Store {
	return e.store
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
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, reader)
	if err != nil {
		return 0, "", fmt.Errorf("build %s %s: %w", method, path, err)
	}

	req.Header.Set("Authorization", "token "+e.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
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

// TFEStatus sends one request to the TFE-compatible API as the given token
// and returns its status and body. The token travels as a Bearer credential,
// as OpenTofu sends it. It uses the test server's client, which trusts the
// self-signed certificate OpenTofu gets through SSL_CERT_FILE.
func (e *Env) TFEStatus(ctx context.Context, token, method, path, body string) (int, string, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, reader)
	if err != nil {
		return 0, "", fmt.Errorf("build %s %s: %w", method, path, err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/vnd.api+json")
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

// TFEStatusA sends one TFE API request as principal A, the default principal
// that the CLI configuration uses.
func (e *Env) TFEStatusA(ctx context.Context, method, path, body string) (int, string, error) {
	return e.TFEStatus(ctx, e.token, method, path, body)
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

	target := "/api/v2/organizations/" + Namespace + "/workspaces/" + Workspace

	status, body, err := e.TFEStatusA(ctx, http.MethodGet, target, "")
	if err != nil {
		tb.Fatalf("get workspace: %v", err)
	}

	if status != http.StatusOK {
		tb.Fatalf("get workspace answers %d: %s", status, body)
	}

	var doc workspaceIDDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Data.ID == "" {
		tb.Fatalf("decode workspace id: %v (%s)", err, body)
	}

	return doc.Data.ID
}

// APIStepSpec describes one TFE API step.
type APIStepSpec struct {
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
}

// APIStep runs one TFE API request as a scenario step and records it for the
// report. It fails the subtest when the status differs from want or when
// check rejects the body. A failing step does not stop later steps.
func (e *Env) APIStep(ctx context.Context, t *testing.T, token string, spec APIStepSpec) {
	t.Helper()

	t.Run(spec.Name, func(t *testing.T) {
		e.Recorder.SetStep(spec.Name)

		var failure string

		var exitCode int

		status, respBody, err := e.TFEStatus(ctx, token, spec.Method, spec.Path, spec.Body)

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

	doc.Deployment["crashMarker"] = "crash-marker"

	raw, err := json.Marshal(doc.Deployment)
	if err != nil {
		tb.Fatalf("encode crash checkpoint: %v", err)
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
	return e.RunIn(ctx, e.project, extra, args...)
}

// RunIn executes the CLI in dir and returns what it printed. extra holds
// NAME=value pairs added to the environment. A non-zero exit is reported in
// the Result, not as an error.
func (e *Env) RunIn(ctx context.Context, dir string, extra []string, args ...string) (Result, error) {
	return e.runWithEnv(ctx, dir, e.cliEnv(extra), args...)
}

// RunSavedLogin runs the CLI in dir with the saved logins and returns what
// it printed. It omits PULUMI_BACKEND_URL and PULUMI_ACCESS_TOKEN, so the
// CLI uses the saved logins in PULUMI_HOME. Extra holds NAME=value pairs
// for one run. A non-zero exit is a Result, not an error.
func (e *Env) RunSavedLogin(ctx context.Context, dir string, extra []string, args ...string) (Result, error) {
	return e.runWithEnv(ctx, dir, e.savedLoginEnv(extra), args...)
}

// KillSpec describes one CLI invocation killed at a recorded request: the
// harness starts the CLI, waits until the recorder shows the request, kills
// the process with SIGKILL and reaps it. The wait uses the recorder, never a
// fixed sleep.
type KillSpec struct {
	// Name labels the subtest, the report row and the requests the run makes.
	Name string
	// Args are the CLI arguments.
	Args []string
	// Env holds extra NAME=value pairs for this run only.
	Env []string
	// Method is the HTTP method to wait for, for example POST.
	Method string
	// PathSuffix is the suffix of the request path to wait for, for example
	// /actions/lock.
	PathSuffix string
}

// WaitForRequest reports whether the recorder shows a request with method and
// path suffix during step before ctx ends. It polls the recorder, never
// sleeps a fixed time.
func (e *Env) WaitForRequest(ctx context.Context, step, method, suffix string) bool {
	for {
		entries := e.Recorder.Entries()
		for i := range entries {
			if entries[i].Step == step && entries[i].Method == method &&
				strings.HasSuffix(entries[i].Path, suffix) {
				return true
			}
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(killPoll):
		}
	}
}

// KillAtRequest runs the CLI with spec.Args and kills it with SIGKILL once
// the recorder shows spec.Method with spec.PathSuffix during spec.Name. It
// reaps the process, so no process stays behind, and records the run as the
// spec.Name step. The step passes when the request arrived and the process
// was reaped; the exit code of the killed process is reported, not checked.
func (e *Env) KillAtRequest(ctx context.Context, t *testing.T, spec KillSpec) Result {
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
	// Fail expects a non-zero exit. Check then inspects the failed run,
	// for steps that prove an error message.
	Fail bool
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
				outs[i].res, outs[i].err = e.Run(ctx, spec.Env, args...)
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

	return e.stepInWith(ctx, t, dir, spec, e.RunIn)
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

	return e.stepInWith(ctx, t, dir, spec, e.RunSavedLogin)
}

// stepInWith runs spec in dir through run and records it for the report.
// StepIn passes RunIn; the saved-login steps pass RunSavedLogin.
func (e *Env) stepInWith(
	ctx context.Context,
	t *testing.T,
	dir string,
	spec StepSpec,
	run func(context.Context, string, []string, ...string) (Result, error),
) Result {
	t.Helper()

	var res Result

	t.Run(spec.Name, func(t *testing.T) {
		e.Recorder.SetStep(spec.Name)

		var failure string

		var err error

		res, err = run(ctx, dir, spec.Env, spec.Args...)

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
			t.Errorf("%s: %s", res.Command, failure)
		}
	})

	return res
}

// runWithEnv executes the CLI in dir with env and returns what it printed.
// A non-zero exit is reported in the Result, not as an error.
func (e *Env) runWithEnv(ctx context.Context, dir string, env []string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer

	//nolint:gosec // The binary is the CLI this suite downloaded and checksum-verified.
	cmd := exec.CommandContext(ctx, e.cli.Binary(), args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay

	start := time.Now()
	err := cmd.Run()
	timedOut := ctx.Err()
	res := Result{
		Command:  e.cli.Tool + " " + strings.Join(args, " "),
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

// runAndKill starts the CLI with spec.Args, waits for the recorded request,
// kills the process with SIGKILL and reaps it, so no process stays behind.
// It returns the run and the step failure, or "" when the request arrived
// and the process was reaped. The exit code of the killed process is
// reported, not checked.
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
	seen := e.WaitForRequest(waitCtx, spec.Name, spec.Method, spec.PathSuffix)
	waitCancel()

	failure := ""
	if !seen {
		failure = fmt.Sprintf("no %s %q recorded within %s", spec.Method, spec.PathSuffix, killWait)
	}

	if killErr := cmd.Process.Signal(syscall.SIGKILL); killErr != nil {
		failure = joinFailure(failure, fmt.Sprintf("kill: %v", killErr))
	}

	// Reap the process, so the kill leaves none behind. The exit error of a
	// killed process is the expected outcome, not a failure.
	_ = cmd.Wait() //nolint:errcheck // The exit of a killed process is recorded below.
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

// writeTofuFiles writes main.tf, a CLI configuration holding the token of
// principal A, another holding the token of B, and the server certificate.
func (e *Env) writeTofuFiles(tb testing.TB, ts *httptest.Server) {
	tb.Helper()

	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: ts.Certificate().Raw})
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

// clip shortens s for logs and reports.
func clip(s string) string {
	if len(s) <= maxShown {
		return s
	}

	return s[:maxShown] + "…"
}
