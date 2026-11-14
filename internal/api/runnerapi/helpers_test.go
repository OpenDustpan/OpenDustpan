package runnerapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// phaseKey is the key of the phase that the tests start.
const phaseKey = "job-1/0"

const (
	serverURL        = "https://dustpan.test"
	adminToken       = "admin-token"
	installationName = "eu-west"
)

// serverRelease is the release of the server in the tests; a runner of
// 0.4.0 is inside the skew.
func serverRelease() runner.Release {
	return runner.Release{Major: 0, Minor: 4, Patch: 0, Pre: ""}
}

// env is a real SQLite store with the handler over it, on a test server.
type env struct {
	t       *testing.T
	store   stores
	server  *httptest.Server
	handler *runnerapi.Handler
	claimer *opsClaimer
	fence   namespace.Fence
	clock   *skewClock
}

// opsClaimer is a job.Claimer on the durable store: the phases of a job are
// durable operations. The lease is the lease of the request.
type opsClaimer struct {
	ops      durable.Store
	released atomic.Int64
}

var _ job.Claimer = (*opsClaimer)(nil)

func (c *opsClaimer) Claim(ctx context.Context, req job.ClaimRequest) (job.Assignment, error) {
	op, err := c.ops.Claim(
		ctx,
		durable.ClaimRequest{
			Owner:        req.Owner,
			Kinds:        req.Kinds,
			MaxVersions:  req.MaxVersions,
			Lease:        req.Lease,
			TakeCanceled: false,
		},
	)
	if err != nil {
		return job.Assignment{}, fmt.Errorf("claim: %w", err)
	}

	var (
		fence namespace.Fence
		j     job.Job
	)

	j.ID = job.ID(op.Key)

	return job.Assignment{
		Claim:   job.PhaseClaim{Job: j.ID, Phase: 0, Claim: op.Claim, Fence: fence},
		Job:     j,
		Plan:    nil,
		Profile: "",
	}, nil
}

var errUnused = errors.New("not used in this test")

func (*opsClaimer) Renew(context.Context, job.PhaseClaim, time.Duration) (job.PhaseClaim, error) {
	return job.PhaseClaim{}, errUnused
}

// Release counts the call and gives the operation back, as the job service
// does for a phase with no started step.
func (c *opsClaimer) Release(ctx context.Context, p job.PhaseClaim) error {
	c.released.Add(1)

	if err := c.ops.Release(ctx, p.Claim); err != nil {
		return fmt.Errorf("release: %w", err)
	}

	return nil
}

func (*opsClaimer) Finish(context.Context, job.PhaseClaim, job.Outcome) error { return errUnused }

// adminAuthenticator signs in the administrator for adminToken.
type adminAuthenticator struct{}

func (adminAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != adminToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: "ada", DisplayName: "ada", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}, nil
}

// runnerStores are the ports of the runner protocol on one database.
type runnerStores interface {
	runner.PoolStore
	runner.MemberStore
	runner.SessionStore
}

// stores are the ports of one SQL store that the tests use.
type stores struct {
	namespaces namespace.Store
	operations durable.Store
	jobs       job.Store
	units      unitStore
	runners    runnerStores
	audit      interface {
		List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
	}
}

// unitStore makes the units that a job names. Both SQL stores implement it.
type unitStore interface {
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
	ListUnits(ctx context.Context, ns namespace.Name) ([]inventory.Entry, error)
}

// backend opens an empty store of one SQL dialect.
type backend struct {
	name string
	open func(t *testing.T) stores
}

// backends returns SQLite, and PostgreSQL when DUSTPAN_TEST_POSTGRES_URL is set.
func backends() []backend {
	list := []backend{{name: "sqlite", open: openSQLite}}

	if os.Getenv(postgresEnv) != "" {
		list = append(list, backend{name: "postgres", open: openPostgres})
	}

	return list
}

// wiringBackends returns SQLite alone. A test of the wiring of the handler
// replaces the store behind the check under test with a double, or refuses the
// request before it reaches the store. The dialect of the store does not
// change such a test, and an environment of PostgreSQL costs about a second.
func wiringBackends() []backend {
	return backends()[:1]
}

func openSQLite(t *testing.T) stores {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return stores{
		namespaces: s, operations: s.DurableOperations(), jobs: s.Jobs(), units: s, runners: s.Runners(), audit: s,
	}
}

// newEnv opens a SQLite store, creates the namespace acme, and serves the
// handler. The lease of a claim is the lease of the pool.
func newEnv(t *testing.T) *env {
	t.Helper()

	return newEnvOn(t, openSQLite)
}

// newEnvOn opens a store with open, creates the namespace acme, and serves
// the handler.
func newEnvOn(t *testing.T, open func(t *testing.T) stores) *env {
	t.Helper()

	return newEnvWith(t, open, nil)
}

// newEnvWith is newEnvOn with the claimer that phases returns for the
// stores. A nil phases keeps the claimer of the other tests, which has no
// jobs.
func newEnvWith(
	t *testing.T,
	open func(t *testing.T) stores,
	phases func(stores) job.Claimer,
) *env {
	t.Helper()

	return newEnvServing(t, open, func(s stores, opts *serveOptions) {
		if phases != nil {
			opts.claimer = phases(s)
		}
	})
}

// newEnvServing opens a store with open, creates the namespace acme, and
// serves the handler with the options that set changes.
func newEnvServing(t *testing.T, open func(t *testing.T) stores, set func(s stores, opts *serveOptions)) *env {
	t.Helper()

	s := open(t)

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.namespaces.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	clock := &skewClock{offset: atomic.Int64{}}
	opts := serveOptions{
		now:      clock.now,
		authz:    identity.AdminOnly{},
		claimer:  nil,
		recorder: nil,
		jobs:     nil,
		logger:   nil,
		mutate:   nil,
	}

	if set != nil {
		set(s, &opts)
	}

	h, srv, ops := serve(t, s, opts)

	return &env{t: t, store: s, server: srv, handler: h, claimer: ops, fence: ns.Fence(), clock: clock}
}

// lapseLease is the lease that lapse sets, and lapseMargin is the time to
// wait after its expiry.
const (
	lapseLease  = 20 * time.Millisecond
	lapseMargin = 30 * time.Millisecond
)

// lapse ends the claim of the operation. A case polls with the lease of the
// pool, so that a slow store cannot end the claim before the poll has
// recorded it. After the poll, lapse shortens the lease to lapseLease with
// Extend and waits until the expiry that the store answered has passed. It
// fails at once when the store does not move the expiry earlier. A slow store
// makes the wait longer, not shorter.
func (e *env) lapse(operation string) {
	e.t.Helper()

	op, err := e.claimer.ops.Get(e.t.Context(), durable.ID(operation))
	if err != nil {
		e.t.Fatalf("get operation %s: %v", operation, err)
	}

	shorter, err := e.claimer.ops.Extend(e.t.Context(), op.Claim, lapseLease)
	if err != nil {
		e.t.Fatalf("extend the claim of %s to a short lease: %v", operation, err)
	}

	if !shorter.ExpiresAt.Before(op.Claim.ExpiresAt) {
		e.t.Fatalf("extend to %v did not move the expiry earlier: %v after %v", lapseLease, shorter.ExpiresAt,
			op.Claim.ExpiresAt)
	}

	pause(e.t, time.Until(shorter.ExpiresAt)+lapseMargin)
}

// skewClock is the clock of the server in a test: the real time plus an
// offset that the test moves forward.
type skewClock struct {
	offset atomic.Int64
}

func (c *skewClock) now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }

// advance moves the clock of the server forward by d. Proofs of runners
// carry the real time, so a test advances the clock only for requests that
// carry none.
func (c *skewClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

// serveOptions are the settings of serve.
type serveOptions struct {
	now   func() time.Time
	authz identity.Authorizer
	// claimer replaces the claimer of the test, which has no jobs.
	claimer job.Claimer
	// recorder and jobs are the recorder and the job reader of the session
	// paths. Nil leaves them out.
	recorder job.CheckedRecorder
	jobs     runnerapi.JobReader
	// logger replaces the logger that discards.
	logger *slog.Logger
	// mutate changes the Config before the handler is built, for a test that
	// leaves out a dependency or wraps one.
	mutate func(cfg *runnerapi.Config)
}

// serve returns a handler over s on a test server. Two calls with the stores
// of two instances of one installation give two replicas of the server.
func serve(t *testing.T, s stores, opts serveOptions) (*runnerapi.Handler, *httptest.Server, *opsClaimer) {
	t.Helper()

	cfg, claimer := configFor(t, s, opts)

	h, err := runnerapi.NewHandler(cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return h, srv, claimer
}

// configFor returns the configuration of the handler that serve builds, with
// the change of opts.mutate.
func configFor(t *testing.T, s stores, opts serveOptions) (runnerapi.Config, *opsClaimer) {
	t.Helper()

	service, err := namespace.NewService(
		namespace.Config{Store: s.namespaces, Now: nil, Installation: installationName},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	claimer := &opsClaimer{ops: s.operations, released: atomic.Int64{}}

	var phases job.Claimer = claimer
	if opts.claimer != nil {
		phases = opts.claimer
	}

	cfg := runnerapi.Config{
		Audit:                      newTestAudit(),
		OnAuditFailure:             nil,
		Namespaces:                 service,
		Pools:                      s.runners,
		Members:                    s.runners,
		Handoffs:                   s.runners,
		Claimer:                    phases,
		Sweeps:                     nil,
		Operations:                 s.operations,
		Recorder:                   opts.recorder,
		Jobs:                       opts.jobs,
		Authenticator:              adminAuthenticator{},
		Authorizer:                 opts.authz,
		Sessions:                   nil,
		Logger:                     loggerOf(opts),
		Release:                    serverRelease(),
		ServerURL:                  serverURL,
		Now:                        opts.now,
		PollInterval:               20 * time.Millisecond,
		DenialLimiter:              nil,
		Plans:                      nil,
		Artifacts:                  nil,
		OnHandoffRefusalSuppressed: nil,
	}

	if opts.mutate != nil {
		opts.mutate(&cfg)
	}

	return cfg, claimer
}

// admin sends a request of the management API as the administrator.
func (e *env) admin(method, path string, body any) (int, []byte) {
	e.t.Helper()

	var reader io.Reader = http.NoBody

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("encode body: %v", err)
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(e.t.Context(), method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)

	return e.send(req)
}

func (e *env) send(req *http.Request) (int, []byte) {
	e.t.Helper()

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("send %s %s: %v", req.Method, req.URL.Path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			e.t.Errorf("close body: %v", closeErr)
		}
	}()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, out
}

// createPool makes the pool prod for jobs and returns its ID.
func (e *env) createPool() string {
	e.t.Helper()

	status, body := e.admin(http.MethodPost, "/api/runners/acme/pools",
		map[string]any{"name": "prod", "purpose": "jobs"})
	if status != http.StatusCreated {
		e.t.Fatalf("create pool = %d %s, want 201", status, body)
	}

	var pool struct {
		ID string `json:"id"`
	}

	decode(e.t, body, &pool)

	return pool.ID
}

// joinToken makes a join token of the pool and returns its secret.
func (e *env) joinToken(pool string) string {
	e.t.Helper()

	status, body := e.admin(http.MethodPost, "/api/runners/acme/pools/"+pool+"/join-tokens", nil)
	if status != http.StatusCreated {
		e.t.Fatalf("create join token = %d %s, want 201", status, body)
	}

	var tok struct {
		Token string `json:"token"`
	}

	decode(e.t, body, &tok)

	return tok.Token
}

// joinToken2 makes a join token of a pool of the namespace globex and
// returns its secret.
func (e *env) joinToken2(pool string) string {
	e.t.Helper()

	status, body := e.admin(http.MethodPost, "/api/runners/globex/pools/"+pool+"/join-tokens", nil)
	if status != http.StatusCreated {
		e.t.Fatalf("create join token = %d %s, want 201", status, body)
	}

	var tok struct {
		Token string `json:"token"`
	}

	decode(e.t, body, &tok)

	return tok.Token
}

// startOperation starts a durable operation of the phases of the pool.
func (e *env) startOperation(pool string) {
	e.t.Helper()

	e.startOperationAt(pool, phaseKey, 0)
}

// startOperationAt starts the durable operation key of the phases of the
// pool, in the assignment version version.
func (e *env) startOperationAt(pool, key string, version int) {
	e.t.Helper()

	_, _, err := e.store.operations.Start(e.t.Context(), durable.StartRequest{
		Key: key, Kind: job.RunnerKind(pool), Version: version, Input: nil,
	})
	if err != nil {
		e.t.Fatalf("start operation: %v", err)
	}
}

func decode(t *testing.T, body []byte, v any) {
	t.Helper()

	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// fakeRunner is a runner with its own key pair.
type fakeRunner struct {
	e       *env
	id      string
	name    string
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
	release string
}

func (e *env) newRunner(name string) *fakeRunner {
	e.t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		e.t.Fatalf("generate key: %v", err)
	}

	return &fakeRunner{e: e, id: "", name: name, pub: pub, priv: priv, release: "0.4.0"}
}

// join joins the runner to the pool with the join token and returns the
// status and the body.
func (f *fakeRunner) join(pool, token string) (int, []byte) {
	f.e.t.Helper()

	req := protocol.JoinRequest{
		Token: token, Pool: pool, Name: f.name, Kind: string(runner.KindMachine),
		Isolation: string(runner.IsolationGVisor), Labels: map[string]string{"zone": "a"}, PublicKey: f.pub,
		Proof: ed25519.Sign(f.priv, runner.JoinChallenge(serverURL, pool, f.name, token)),
	}

	status, body := f.raw(protocol.PathJoin, req, "", f.release)
	if status == http.StatusOK {
		var resp protocol.JoinResponse

		decode(f.e.t, body, &resp)

		f.id = resp.Runner
	}

	return status, body
}

// call sends a signed request of the runner and returns the status and body.
func (f *fakeRunner) call(path string, body any) (int, []byte) {
	f.e.t.Helper()

	return f.callWith(f.priv, path, body)
}

func (f *fakeRunner) callWith(key ed25519.PrivateKey, path string, body any) (int, []byte) {
	f.e.t.Helper()

	st := fresh(f.e.t)
	if f.e.clock != nil {
		// The proof carries the time of the clock of the server, so a test
		// can move that clock forward.
		st.at = f.e.clock.now()
	}

	return f.rawSigned(key, path, body, f.release, st)
}

func newNonce(t *testing.T) string {
	t.Helper()

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("nonce: %v", err)
	}

	return hex.EncodeToString(b)
}

// stamp is the nonce and the time of a proof.
type stamp struct {
	nonce string
	at    time.Time
}

// fresh returns a stamp with a new nonce and the time now.
func fresh(t *testing.T) stamp {
	t.Helper()

	return stamp{nonce: newNonce(t), at: time.Now()}
}

// rawSigned signs the request with key and the stamp, and sends it with the
// release.
func (f *fakeRunner) rawSigned(
	key ed25519.PrivateKey, path string, body any, release string, st stamp,
) (int, []byte) {
	f.e.t.Helper()

	raw := encodeBody(f.e.t, body)
	sum := sha256.Sum256(raw)
	proof := runner.Proof{Subject: f.id, At: st.at, Nonce: st.nonce, Signature: nil}
	proof.Signature = ed25519.Sign(key, runner.ProofInput(proof, runner.Signed{
		Method: http.MethodPost, PathQuery: path, BodySHA256: sum[:],
	}))

	header := fmt.Sprintf("v1 sub=%s t=%d n=%s sig=%s", f.id, st.at.UnixMilli(), st.nonce,
		base64.RawURLEncoding.EncodeToString(proof.Signature))

	return f.send(path, raw, header, release)
}

// raw sends a request with a proof header that the caller made.
func (f *fakeRunner) raw(path string, body any, proof, release string) (int, []byte) {
	f.e.t.Helper()

	return f.send(path, encodeBody(f.e.t, body), proof, release)
}

func (f *fakeRunner) send(path string, raw []byte, proof, release string) (int, []byte) {
	f.e.t.Helper()

	req, err := http.NewRequestWithContext(f.e.t.Context(), http.MethodPost, f.e.server.URL+path, bytes.NewReader(raw))
	if err != nil {
		f.e.t.Fatalf("request: %v", err)
	}

	if release != "" {
		req.Header.Set(protocol.HeaderRelease, release)
	}

	if proof != "" {
		req.Header.Set(protocol.HeaderProof, proof)
	}

	return f.e.send(req)
}

func encodeBody(t *testing.T, body any) []byte {
	t.Helper()

	if body == nil {
		return nil
	}

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}

	return raw
}

// opPath returns the path of a claim request for operation.
func opPath(operation, suffix string) string {
	return "/runner/v1/claims/" + operation + "/" + suffix
}

// poll long-polls for work and returns the work, or nil for a 204.
func (f *fakeRunner) poll(wait time.Duration) *protocol.Work {
	f.e.t.Helper()

	return f.pollAt(wait, runner.AssignmentVersion)
}

// pollAt is poll with the assignment version that the launcher reads.
func (f *fakeRunner) pollAt(wait time.Duration, version int) *protocol.Work {
	f.e.t.Helper()

	status, body := f.call(protocol.PathPoll, protocol.PollRequest{
		Wait: protocol.FromStd(wait), AssignmentVersion: version,
	})

	switch status {
	case http.StatusNoContent:
		return nil
	case http.StatusOK:
		var w protocol.Work

		decode(f.e.t, body, &w)

		return &w
	default:
		f.e.t.Fatalf("poll = %d %s, want 200 or 204", status, body)

		return nil
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

// auditActions returns the events that the audit entries of acme record in
// their first change.
func (e *env) auditActions() map[string]bool {
	e.t.Helper()

	out := map[string]bool{}

	listing, err := e.store.audit.List(e.t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		e.t.Fatalf("list audit: %v", err)
	}

	for i := range listing.Entries {
		for _, c := range listing.Entries[i].Changes {
			if c.Key == "event" {
				out[c.New] = true
			}
		}
	}

	return out
}

// loggerOf returns the logger of the options, or a logger that discards.
func loggerOf(opts serveOptions) *slog.Logger {
	if opts.logger != nil {
		return opts.logger
	}

	return slog.New(slog.DiscardHandler)
}
