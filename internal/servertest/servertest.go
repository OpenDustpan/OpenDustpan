// Package servertest runs several real servers on one PostgreSQL database,
// one blob directory and one key-encryption key, as several instances of one
// installation run. A test starts a cluster, sends requests to any server,
// kills a server and restarts it:
//
//	c := servertest.Start(t, 2)
//	c.Do(t, 0, http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`)
//	c.Kill(t, 0)
//	c.Restart(t, 0)
//
// Each server calls dustpan.Server.Run and has its own store handle, its own
// blob handle and its own listener. The servers share nothing in memory, so
// a test sees only what the database and the blob directory share.
//
// Start skips the test when DUSTPAN_TEST_POSTGRES_URL is not set. Each
// cluster gets its own schema in that database and drops it at the end.
package servertest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// EnvURL names the variable that holds the URL of the PostgreSQL database
// for the cluster. The URL must name a TCP host.
const EnvURL = "DUSTPAN_TEST_POSTGRES_URL"

// Token is the bootstrap token of the admin on every server of a cluster.
// Do sends it.
const Token = "servertest-token-0123456789abcdef0123"

// Installation is the installation name of every server of a cluster.
const Installation namespace.InstallationName = "eu-west"

// key is the key-encryption key that every server of a cluster shares.
const key = "0123456789abcdef0123456789abcdef"

// waitLimit bounds the start of a server and the stop of a killed server.
const waitLimit = 30 * time.Second

// pollEvery is the period of the readiness poll during a start.
const pollEvery = 20 * time.Millisecond

// schemaSuffixBytes is the number of random bytes in a schema name.
const schemaSuffixBytes = 8

// Cluster is a set of servers on one database. Start creates it, and the
// cleanup of the test stops every server that still runs. The methods are
// safe for concurrent use.
type Cluster struct {
	client    *http.Client
	transport *http.Transport
	opts      []dustpan.Option
	storeOpts []postgres.Option
	sets      []dustpan.Components
	schemaURL string
	blobDir   string

	mu        sync.Mutex
	instances []*instance
}

// instance is one server of a cluster and the handles it holds.
type instance struct {
	db     *connProxy
	conns  *trackedListener
	store  *postgres.Store
	blobs  *fs.Store
	cancel context.CancelFunc
	done   chan error
	url    string
}

// Start runs n servers on one fresh schema of the database that EnvURL
// names, and waits until each one answers ready. The servers start one at a
// time, in order. opts are added to the options of every server, after the
// options of the cluster, so an option in opts wins. Start skips the test
// when EnvURL is not set.
func Start(t *testing.T, n int, opts ...dustpan.Option) *Cluster {
	t.Helper()

	return startCluster(t, make([]dustpan.Components, n), nil, opts)
}

// StartWithStore is Start with options for the store of every server. A test
// of database round trips uses it to give the store a query tracer.
func StartWithStore(t *testing.T, n int, storeOpts []postgres.Option, opts ...dustpan.Option) *Cluster {
	t.Helper()

	return startCluster(t, make([]dustpan.Components, n), storeOpts, opts)
}

// StartSplit runs one server for each entry of sets, on one fresh schema,
// and waits until each one answers ready. Server i runs the components of
// sets[i], so a test can start a split topology, for example one api server
// and one worker server. An option in opts wins over the components.
func StartSplit(t *testing.T, sets []dustpan.Components, opts ...dustpan.Option) *Cluster {
	t.Helper()

	return startCluster(t, sets, nil, opts)
}

// startCluster starts one server for each entry of sets. A zero entry runs
// every component.
func startCluster(
	t *testing.T, sets []dustpan.Components, storeOpts []postgres.Option, opts []dustpan.Option,
) *Cluster {
	t.Helper()

	n := len(sets)

	base := os.Getenv(EnvURL)
	if base == "" {
		t.Skipf("set %s to run the multi-instance tests", EnvURL)
	}

	transport := &http.Transport{}

	c := &Cluster{
		client:    &http.Client{Transport: transport, Timeout: waitLimit},
		transport: transport,
		opts:      opts,
		storeOpts: storeOpts,
		sets:      sets,
		schemaURL: "",
		blobDir:   filepath.Join(t.TempDir(), "blobs"),
		mu:        sync.Mutex{},
		instances: make([]*instance, n),
	}

	c.schemaURL = newSchema(t, base)

	t.Cleanup(func() { c.stopAll(t) })

	for i := range n {
		c.instances[i] = c.start(t, i)
	}

	return c
}

// URL returns the base URL of server i, without a trailing slash. A restart
// gives server i a new port, so call URL again after Restart. URL returns
// an empty string for a killed server.
func (c *Cluster) URL(i int) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if in := c.instances[i]; in != nil {
		return in.url
	}

	return ""
}

// InstanceID returns the instance ID that server i uses as the owner of its
// claims on durable operations. A restart keeps it.
func InstanceID(i int) string {
	return fmt.Sprintf("server-%d", i)
}

// Operations returns the durable operations as server i reads them, for a
// test that checks who holds a claim. It fails the test when server i does
// not run.
//
//nolint:ireturn // The test reads the port, as the servers do.
func (c *Cluster) Operations(t *testing.T, i int) durable.Store {
	t.Helper()

	c.mu.Lock()
	in := c.instances[i]
	c.mu.Unlock()

	if in == nil {
		t.Fatalf("operations of server %d: the server does not run", i)
	}

	return in.store.DurableOperationsPort()
}

// Store returns the store handle of server i, for a test that writes the gate
// configuration or reads the decision records and the audit log through the
// ports of the store. It fails the test when server i does not run.
func (c *Cluster) Store(t *testing.T, i int) *postgres.Store {
	t.Helper()

	return c.instance(t, i, "store of").store
}

// SchemaURL returns the URL of the schema of the cluster, for a test that
// reads the rows of the database. The URL names a TCP host.
func (c *Cluster) SchemaURL() string {
	return c.schemaURL
}

// BlobDir returns the directory of the blob store that every server of the
// cluster shares, for a test that reads or changes a blob.
func (c *Cluster) BlobDir() string {
	return c.blobDir
}

// Do sends a request with Token to server i and returns the status and the
// body. The token goes in the scheme that the API of path reads: "Bearer"
// for the installation API and the TFE-compatible API, "token" for the
// other APIs. A body that is not empty is sent as JSON. Do fails the test when
// the request gets no response.
func (c *Cluster) Do(t *testing.T, i int, method, path, body string) (int, string) {
	t.Helper()

	status, _, data := c.DoHeader(t, i, method, path, body)

	return status, data
}

// DoHeader is Do with the response header in the result.
func (c *Cluster) DoHeader(t *testing.T, i int, method, path, body string) (int, http.Header, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, c.URL(i)+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", authScheme(path)+Token)

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s on server %d: %v", method, path, i, err)
	}

	data, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}

	return resp.StatusCode, resp.Header, string(data)
}

// CutDatabase closes every database connection of server i and refuses its
// new ones until RestoreDatabase, as a database that fails over does. The
// server keeps running. CutDatabase fails the test when server i does not run.
func (c *Cluster) CutDatabase(t *testing.T, i int) {
	t.Helper()

	c.instance(t, i, "cut the database of").db.cut()
}

// RestoreDatabase lets server i reach the database again after CutDatabase.
// It does not restart the server.
func (c *Cluster) RestoreDatabase(t *testing.T, i int) {
	t.Helper()

	c.instance(t, i, "restore the database of").db.resume()
}

// authScheme returns the Authorization scheme of the API that serves path.
// The tool-neutral API accepts "Bearer " on every route; some of its routes
// accept only that scheme. The Pulumi Cloud-compatible API accepts only
// "token ".
func authScheme(path string) string {
	for _, prefix := range []string{"/api/server/", "/api/v2/", "/api/dustpan/v1/"} {
		if strings.HasPrefix(path, prefix) {
			return "Bearer "
		}
	}

	return "token "
}

// Kill stops server i at once, as a crash of its process. It first cuts
// every database connection of the server, so an open transaction rolls
// back and no later statement of the server reaches the database. It then
// closes every HTTP connection of the server, so a request in flight gets
// no response. It then stops Run and closes the handles of the server. A
// blob write in flight may still finish, as it may in a crash. Kill fails
// the test when server i does not run, or when Run does not return in time.
func (c *Cluster) Kill(t *testing.T, i int) {
	t.Helper()

	c.mu.Lock()
	in := c.instances[i]
	c.instances[i] = nil
	c.mu.Unlock()

	if in == nil {
		t.Fatalf("kill server %d: the server does not run", i)
	}

	in.db.close()
	in.conns.closeAll()
	in.cancel()

	// A killed server may return an error from Run, as a crash may.
	waitErr := in.wait()

	in.closeHandles()
	c.transport.CloseIdleConnections()

	if errors.Is(waitErr, errStopTimeout) {
		t.Fatalf("kill server %d: %v", i, waitErr)
	}
}

// Signal sends the stop signal to server i, as a SIGTERM to its process: it
// cancels the context of Run and returns at once; the server stays in the
// cluster until the returned function ran. That function waits until Run returns, closes the handles of the server and
// fails the
// test when Run returns an error or does not return in time. Signal fails
// the test when server i does not run.
func (c *Cluster) Signal(t *testing.T, i int) func() {
	t.Helper()

	c.mu.Lock()
	in := c.instances[i]
	c.mu.Unlock()

	if in == nil {
		t.Fatalf("stop server %d: the server does not run", i)
	}

	in.cancel()

	return func() {
		t.Helper()

		// The server stays in the cluster until Run returned, so the test
		// can still send requests to it in the drain.
		c.mu.Lock()
		c.instances[i] = nil
		c.mu.Unlock()

		if err := in.wait(); err != nil {
			t.Errorf("stop server %d: %v", i, err)
		}

		in.closeHandles()
		c.transport.CloseIdleConnections()
	}
}

// Stop stops server i with the stop signal and waits until it returned. It
// keeps the database connections open, so the server can release its work.
func (c *Cluster) Stop(t *testing.T, i int) {
	t.Helper()

	c.Signal(t, i)()
}

// DoStream sends a request with Token to server i, with body as the request
// body, and returns the status and the answer body. Unlike Do, it returns the
// error of the request, so a goroutine can call it.
func (c *Cluster) DoStream(ctx context.Context, i int, method, path string, body io.Reader) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL(i)+path, body)
	if err != nil {
		return 0, "", fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Authorization", authScheme(path)+Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("%s %s on server %d: %w", method, path, i, err)
	}

	data, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, "", fmt.Errorf("read answer: %w", err)
	}

	return resp.StatusCode, string(data), nil
}

// Restart starts server i again after Kill, with new handles on the same
// database, blob directory and key, and waits until it answers ready. It
// fails the test when server i still runs.
func (c *Cluster) Restart(t *testing.T, i int) {
	t.Helper()

	c.mu.Lock()
	running := c.instances[i] != nil
	c.mu.Unlock()

	if running {
		t.Fatalf("restart server %d: the server still runs", i)
	}

	in := c.start(t, i)

	c.mu.Lock()
	c.instances[i] = in
	c.mu.Unlock()
}

// instance returns the running server i, and fails the test with action when
// it does not run.
func (c *Cluster) instance(t *testing.T, i int, action string) *instance {
	t.Helper()

	c.mu.Lock()
	in := c.instances[i]
	c.mu.Unlock()

	if in == nil {
		t.Fatalf("%s server %d: the server does not run", action, i)
	}

	return in
}

// start opens the handles of server i, runs it and waits until it answers
// ready.
func (c *Cluster) start(t *testing.T, i int) *instance {
	t.Helper()

	db := startProxy(t, c.schemaURL)

	store, err := postgres.Open(t.Context(), db.url, c.storeOpts...)
	if err != nil {
		db.close()
		t.Fatalf("open store of server %d: %v", i, err)
	}

	blobs, err := fs.Open(c.blobDir)
	if err != nil {
		store.Close()
		db.close()
		t.Fatalf("open blob store of server %d: %v", i, err)
	}

	ln, err := listen(t)
	if err != nil {
		closeAll(t, store, blobs, db)
		t.Fatalf("listen for server %d: %v", i, err)
	}

	own := serverOptions(t, i, store, blobs, ln)
	if len(c.sets[i].Names()) > 0 {
		own = append(own, dustpan.WithComponents(c.sets[i]))
	}

	srv, err := dustpan.NewServer(append(own, c.opts...)...)
	if err != nil {
		closeAll(t, store, blobs, db)
		t.Fatalf("new server %d: %v", i, err)
	}

	// The server outlives the test context until the cleanup stops it, so
	// the cleanup can stop it in order.
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))

	in := &instance{
		db:     db,
		conns:  ln,
		store:  store,
		blobs:  blobs,
		cancel: cancel,
		done:   make(chan error, 1),
		url:    "http://" + ln.Addr().String(),
	}

	go func() { in.done <- srv.Run(ctx) }()

	if readyErr := c.waitReady(t, in); readyErr != nil {
		in.cancel()
		waitErr := in.wait()
		in.closeHandles()
		t.Fatalf("start server %d: %v", i, errors.Join(readyErr, waitErr))
	}

	return in
}

// serverOptions returns the options of server i over its own handles.
func serverOptions(
	t *testing.T,
	i int,
	store *postgres.Store,
	blobs *fs.Store,
	ln net.Listener,
) []dustpan.Option {
	t.Helper()

	authn, err := identity.NewStaticToken("admin", Token)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(key))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(t.Output(), nil)).With(slog.Int("server", i))

	return []dustpan.Option{
		dustpan.WithInstallation(Installation),
		dustpan.WithNamespaceStore(store),
		dustpan.WithStackStore(store),
		dustpan.WithUpdateStore(store),
		dustpan.WithCheckpointStore(store),
		dustpan.WithEventStore(store),
		dustpan.WithSecretKeyStore(store),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(store),
		dustpan.WithStateStore(store),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(store),
		dustpan.WithIPAllowlistStore(store),
		dustpan.WithAuthenticator(authn),
		dustpan.WithLogger(logger),
		dustpan.WithListener(ln),
		dustpan.WithDurableStore(store.DurableOperations()),
		// The job, trigger and drift routes need their stores, as the binary gives them.
		dustpan.WithRunners(store.Runners()),
		dustpan.WithSourceFilters(store.SourceFilters()),
		dustpan.WithTrustRules(store.TrustRules()),
		dustpan.WithJobStore(store.Jobs()),
		dustpan.WithJobClaimer(func(g job.GateGuard) job.Claimer {
			return job.NewClaimer(store.Jobs(), store.DurableOperationsPort(), store, g, job.WithClaimerLogger(logger))
		}),
		dustpan.WithTriggerStore(store.Triggers()),
		dustpan.WithUnitSettingsStore(store.UnitSettings()),
		dustpan.WithDriftStore(store.DriftResults()),
		dustpan.WithInstanceID(InstanceID(i)),
		dustpan.WithInstallationKindStore(store),
		dustpan.WithInstanceStore(store),
	}
}

// errNotReady reports a server that did not answer ready in time.
var errNotReady = errors.New("the server did not answer ready in time")

// waitReady polls the readiness probe of in until it answers 200, Run
// returns, or waitLimit passes.
func (c *Cluster) waitReady(t *testing.T, in *instance) error {
	t.Helper()

	deadline := time.Now().Add(waitLimit)

	for time.Now().Before(deadline) {
		select {
		case err := <-in.done:
			in.done <- err

			return fmt.Errorf("run returned before ready: %w", err)
		default:
		}

		if c.ready(t, in.url) {
			return nil
		}

		timer := time.NewTimer(pollEvery)
		select {
		case <-t.Context().Done():
			timer.Stop()

			return fmt.Errorf("wait for ready: %w", t.Context().Err())
		case <-timer.C:
		}
	}

	return errNotReady
}

// ready reports whether the readiness probe at base answers 200.
func (c *Cluster) ready(t *testing.T, base string) bool {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/health/ready", http.NoBody)
	if err != nil {
		return false
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}

	_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck // The probe reads only the status.
	_ = resp.Body.Close()                 //nolint:errcheck // The probe reads only the status.

	return resp.StatusCode == http.StatusOK
}

// stopAll stops every server that still runs, as a graceful stop, and
// drops the schema of the cluster. A server that stops with an error fails
// the test.
func (c *Cluster) stopAll(t *testing.T) {
	t.Helper()

	c.mu.Lock()
	running := make([]*instance, 0, len(c.instances))

	for i, in := range c.instances {
		if in != nil {
			running = append(running, in)
			c.instances[i] = nil
		}
	}
	c.mu.Unlock()

	for _, in := range running {
		in.cancel()
	}

	for _, in := range running {
		if err := in.wait(); err != nil {
			t.Errorf("stop server on %s: %v", in.url, err)
		}

		in.closeHandles()
	}

	c.transport.CloseIdleConnections()

	if c.schemaURL != "" {
		dropSchema(t, c.schemaURL)
	}
}

// wait waits until Run of in returns, at most waitLimit, and returns its
// error, or errStopTimeout.
func (in *instance) wait() error {
	timer := time.NewTimer(waitLimit)
	defer timer.Stop()

	select {
	case err := <-in.done:
		in.done <- err

		return err
	case <-timer.C:
		return errStopTimeout
	}
}

// errStopTimeout reports a server whose Run did not return in time.
var errStopTimeout = errors.New("run did not return in time")

// closeHandles closes the store, the blob store and the database proxy of
// in, after Run returned.
func (in *instance) closeHandles() {
	in.store.Close()
	_ = in.blobs.Close() //nolint:errcheck // The directory handle has nothing left to flush.
	in.db.close()
	in.conns.closeAll()
}

// closeAll closes the handles of a server that did not start.
func closeAll(t *testing.T, store *postgres.Store, blobs *fs.Store, db *connProxy) {
	t.Helper()

	store.Close()

	if err := blobs.Close(); err != nil {
		t.Errorf("close blob store: %v", err)
	}

	db.close()
}

// newSchema creates a schema with a random name in the database at base and
// returns a URL whose search path names it.
func newSchema(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, schemaSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvURL, err)
	}

	if execErr := execSQL(t.Context(), base, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		t.Fatalf("create schema: %v", execErr)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}

// dropSchema drops the schema that the search path of schemaURL names.
func dropSchema(t *testing.T, schemaURL string) {
	t.Helper()

	u, err := url.Parse(schemaURL)
	if err != nil {
		t.Errorf("parse schema URL: %v", err)

		return
	}

	name := u.Query().Get("search_path")
	q := u.Query()
	q.Del("search_path")
	u.RawQuery = q.Encode()

	// The store keeps its control tables in a second schema, <name>_control.
	for _, schema := range []string{name, name + "_control"} {
		drop := "DROP SCHEMA IF EXISTS " + pgx.Identifier{schema}.Sanitize() + " CASCADE"
		if execErr := execSQL(context.WithoutCancel(t.Context()), u.String(), drop); execErr != nil {
			t.Errorf("drop schema %s: %v", schema, execErr)
		}
	}
}

// execSQL runs one statement on a new connection to dbURL.
func execSQL(ctx context.Context, dbURL, statement string) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	_, execErr := conn.Exec(ctx, statement)
	if execErr != nil {
		execErr = fmt.Errorf("%s: %w", statement, execErr)
	}

	if closeErr := conn.Close(ctx); closeErr != nil {
		return errors.Join(execErr, fmt.Errorf("close: %w", closeErr))
	}

	return execErr
}
