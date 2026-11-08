package runnerapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// sessionJobs is a job.Recorder and a job reader in memory. It keeps what the
// launcher sent, builds the job record from it, and returns the error that a
// case sets for a method.
type sessionJobs struct {
	mu      sync.Mutex
	calls   []string
	logs    map[string]map[int][]byte
	ended   map[string]int
	failure map[string]error
}

var _ job.CheckedRecorder = (*sessionJobs)(nil)

func newSessionJobs() *sessionJobs {
	return &sessionJobs{
		mu: sync.Mutex{}, calls: nil, logs: map[string]map[int][]byte{}, ended: map[string]int{},
		failure: map[string]error{},
	}
}

func (f *sessionJobs) BeginStep(_ context.Context, _ job.PhaseClaim, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "begin:"+name)

	return f.failure["begin"]
}

func (f *sessionJobs) EndStep(_ context.Context, c job.PhaseClaim, r job.StepRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "end:"+r.Name)

	if err := f.failure["end"]; err != nil {
		return err
	}

	if r.Token != c.Claim.Token {
		return fmt.Errorf("%w: token", job.ErrInvalid)
	}

	f.ended[r.Name] = r.ExitCode

	return nil
}

// EndStepChecked compares the log that it holds with want before it records
// the end, as the durable recorder does: a log that differs is ErrConflict and
// changes nothing.
func (f *sessionJobs) EndStepChecked(
	ctx context.Context, c job.PhaseClaim, r job.StepRecord, want job.LogExpectation,
) error {
	f.mu.Lock()

	chunks, sum := len(f.logs[r.Name]), ""
	if chunks > 0 {
		all := sha256.New()

		for i := range chunks {
			all.Write(f.logs[r.Name][i])
		}

		sum = hex.EncodeToString(all.Sum(nil))
	}

	refused := chunks != want.Chunks || (chunks > 0 && sum != want.SHA256)
	if refused {
		f.calls = append(f.calls, "refused:"+r.Name)
	}

	f.mu.Unlock()

	if refused {
		return job.ErrLogDiffers
	}

	return f.EndStep(ctx, c, r)
}

func (f *sessionJobs) AppendLog(_ context.Context, _ job.PhaseClaim, step string, seq int, chunk io.Reader) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, fmt.Sprintf("log:%s:%d", step, seq))

	if err := f.failure["log"]; err != nil {
		return err
	}

	data, err := io.ReadAll(chunk)
	if err != nil {
		return fmt.Errorf("read chunk: %w", err)
	}

	if f.logs[step] == nil {
		f.logs[step] = map[int][]byte{}
	}

	if old, ok := f.logs[step][seq]; ok && !bytes.Equal(old, data) {
		return job.ErrConflict
	}

	f.logs[step][seq] = data

	return nil
}

func (*sessionJobs) PutPlan(context.Context, job.PhaseClaim, []job.PlanUpload) (job.PlanRef, error) {
	return job.PlanRef{}, errUnused
}

func (*sessionJobs) Delivered(context.Context, job.PhaseClaim, job.DeliveryResult) error {
	return errUnused
}

// Get returns the job of the tests: one plan phase with two steps and one
// apply step, with the log of each ended step as the recorder closes it.
func (f *sessionJobs) Get(_ context.Context, id job.ID) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		return job.Job{}, fmt.Errorf("unit: %w", err)
	}

	steps := []job.Step{
		testStep("init", job.PhasePlan, false), testStep("plan", job.PhasePlan, false),
		testStep("apply", job.PhaseApply, true),
	}

	var records []job.StepRecord

	for name, code := range f.ended {
		var ref *job.LogRef

		if chunks := f.logs[name]; len(chunks) > 0 {
			all := sha256.New()

			for i := range len(chunks) {
				all.Write(chunks[i])
			}

			ref = &job.LogRef{
				ID: "", ItemKey: "", Version: 0, Chunks: len(chunks), SHA256: hex.EncodeToString(all.Sum(nil)),
				Size: 0, CreatedAt: time.Time{}, ClosedAt: time.Time{}, ExpiresAt: time.Time{},
				RedactedAt: time.Time{}, ReplacedBy: "", Matches: 0,
			}
		}

		records = append(records, job.StepRecord{
			Name: name, Token: 1, ExitCode: code, StartedAt: time.Time{}, FinishedAt: time.Time{}, Log: ref,
		})
	}

	spec := job.Spec{
		Unit: unit, Operation: job.OperationPlan, Purpose: "", Executor: job.ExecutorRunner,
		Location: job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}, Steps: steps,
		Pool: "", Profile: "prf-default", Edits: nil, Delivery: "", Defaulted: nil,
	}
	phase := job.Phase{
		Kind: job.PhasePlan, Operation: string(id), Claims: 1, Executor: "", Steps: records, StateVersion: "",
		Plan: nil, Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	}

	return job.Job{
		ID: id, NamespaceID: "", Spec: spec, State: "", Wait: "", Reason: "", Phases: []job.Phase{phase}, Current: 0,
		StartedBy: "", Source: "", Agent: "", AgentMode: "", BreakGlass: nil, Trigger: nil, Delivery: nil,
		Closed: nil, Verification: "", IdempotencyKey: "", RequestHash: "", Version: 0, CreatedAt: time.Time{},
		UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
	}, nil
}

// failWith makes the method return err.
func (f *sessionJobs) failWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failure[method] = err
}

// called returns the calls so far, as "method:detail".
func (f *sessionJobs) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.calls...)
}

func testStep(name string, phase job.PhaseKind, changes bool) job.Step {
	return job.Step{
		Name: name, Role: job.RoleTool, Phase: phase, Args: []string{"tofu", name}, Env: map[string]string{"A": "b"},
		Dir: "", Timeout: time.Minute, AllowFailure: false, ChangesResources: changes, Outputs: nil,
	}
}

// sessionClaimer is the claimer of the session tests: a finish completes the
// durable claim. A renewal goes to the session store, not to the claimer.
type sessionClaimer struct {
	*opsClaimer

	finished atomic.Int64
	outcome  atomic.Pointer[job.Outcome]
}

func (c *sessionClaimer) Finish(ctx context.Context, p job.PhaseClaim, o job.Outcome) error {
	if err := c.ops.Complete(ctx, p.Claim); err != nil {
		return fmt.Errorf("complete: %w", err)
	}

	c.finished.Add(1)
	c.outcome.Store(&o)

	return nil
}

// sessionEnv is an environment with a runner that holds the claim of one
// phase.
type sessionEnv struct {
	*env

	jobs    *sessionJobs
	claimer *sessionClaimer
	logs    *syncBuffer
	pool    string
	runner  *fakeRunner
	work    *protocol.Work
}

// syncBuffer is a log buffer that the server and the test share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// newSessionEnv opens a store, joins a runner and claims the phase for it.
func newSessionEnv(t *testing.T, b backend) *sessionEnv {
	t.Helper()

	return newSessionEnvWith(t, b, nil)
}

// newSessionEnvWith is newSessionEnv with a change of the Config of the handler.
func newSessionEnvWith(t *testing.T, b backend, mutate func(cfg *runnerapi.Config)) *sessionEnv {
	t.Helper()

	jobs := newSessionJobs()
	logs := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}
	claimer := &sessionClaimer{opsClaimer: nil, finished: atomic.Int64{}, outcome: atomic.Pointer[job.Outcome]{}}

	e := newEnvServing(t, b.open, func(s stores, o *serveOptions) {
		claimer.opsClaimer = &opsClaimer{ops: s.operations, released: atomic.Int64{}}
		o.claimer, o.recorder, o.jobs = claimer, jobs, jobs
		o.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		o.mutate = mutate
	})

	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	e.startOperation(pool)

	work := r.poll(2 * time.Second)
	if work == nil {
		t.Fatal("poll = 204, want the phase")
	}

	return &sessionEnv{env: e, jobs: jobs, claimer: claimer, logs: logs, pool: pool, runner: r, work: work}
}

// launcher is the launcher of a claim: it holds the key of its job session.
type launcher struct {
	e    *sessionEnv
	id   string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// exchange is one request of the session exchange, with the parts that a case
// changes.
type exchange struct {
	token   string
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
	signer  ed25519.PrivateKey
	release string
	at      time.Time
	nonce   string
	// subject, when set, replaces the subject of the proof.
	subject string
	// challenge, when set, replaces the token of the signed challenge.
	challenge string
}

// newExchange returns a valid exchange of the handoff token.
func (s *sessionEnv) newExchange(token string) exchange {
	s.t.Helper()

	pub, priv := newKeyPair(s.t)

	return exchange{
		token: token, pub: pub, priv: priv, signer: priv, release: "0.4.0", at: s.clock.now(),
		nonce: newNonce(s.t), subject: "", challenge: "",
	}
}

// sendExchange sends the exchange and returns the status and the body.
func (s *sessionEnv) sendExchange(x exchange) (int, []byte) {
	s.t.Helper()

	return s.exchangeAt("", x)
}

// exchangeAt is sendExchange to the server at url, or to the server of the
// environment for an empty url.
func (s *sessionEnv) exchangeAt(url string, x exchange) (int, []byte) {
	s.t.Helper()

	challengeToken := x.token
	if x.challenge != "" {
		challengeToken = x.challenge
	}

	req := protocol.SessionRequest{
		HandoffToken: x.token,
		PublicKey:    x.pub,
		Release:      x.release,
		Proof: ed25519.Sign(
			x.priv,
			runner.SessionChallenge(serverURL, challengeToken, runner.Key{Public: x.pub, CreatedAt: time.Time{}}),
		),
	}
	raw := encodeBody(s.t, req)

	subject := sha256Hex(x.token)
	if x.subject != "" {
		subject = x.subject
	}

	return s.sendSigned(http.MethodPost, protocol.PathSession, raw, sum(raw), signing{
		subject: subject, key: x.signer, at: x.at, nonce: x.nonce, release: x.release, contentHash: "", server: url,
	})
}

// signing holds how a request is signed.
type signing struct {
	subject string
	key     ed25519.PrivateKey
	at      time.Time
	nonce   string
	release string
	// contentHash, when set, is sent in the header Dustpan-Content-SHA256.
	contentHash string
	// server, when set, is the URL of the server that gets the request.
	server string
}

func sum(raw []byte) []byte {
	d := sha256.Sum256(raw)

	return d[:]
}

// sendSigned signs the hash signed of a request and sends it with raw as its
// body.
func (s *sessionEnv) sendSigned(method, path string, raw, signed []byte, sg signing) (int, []byte) {
	s.t.Helper()

	proof := runner.Proof{Subject: sg.subject, At: sg.at, Nonce: sg.nonce, Signature: nil}
	proof.Signature = ed25519.Sign(sg.key, runner.ProofInput(proof, runner.Signed{
		Method: method, PathQuery: path, BodySHA256: signed,
	}))

	base := s.server.URL
	if sg.server != "" {
		base = sg.server
	}

	req, err := http.NewRequestWithContext(s.t.Context(), method, base+path, bytes.NewReader(raw))
	if err != nil {
		s.t.Fatalf("request: %v", err)
	}

	if sg.release != "" {
		req.Header.Set(protocol.HeaderRelease, sg.release)
	}

	req.Header.Set(protocol.HeaderProof, fmt.Sprintf("v1 sub=%s t=%d n=%s sig=%s", sg.subject, sg.at.UnixMilli(),
		sg.nonce, base64.RawURLEncoding.EncodeToString(proof.Signature)))

	if sg.contentHash != "" {
		req.Header.Set(protocol.HeaderContentSHA256, sg.contentHash)
	}

	return s.send(req)
}

// launch gets the handoff token and opens the job session.
func (s *sessionEnv) launch() *launcher {
	s.t.Helper()

	x := s.newExchange(s.runner.handoff(s.work.Operation))

	status, body := s.sendExchange(x)
	if status != http.StatusOK {
		s.t.Fatalf("session exchange = %d %s, want 200", status, body)
	}

	var resp protocol.SessionResponse

	decode(s.t, body, &resp)

	return &launcher{e: s, id: resp.Session, pub: x.pub, priv: x.priv}
}

// lcall is one request of a launcher, with the parts that a case changes.
type lcall struct {
	method string
	path   string
	body   []byte
	// content is the body of an upload: its hash goes in the header and in
	// the proof.
	content []byte
	// key and subject replace the key and the subject of the proof.
	key     ed25519.PrivateKey
	subject string
	// at replaces the time of the proof.
	at time.Time
	// release replaces the release; "-" sends none.
	release string
	// signedBody, when set, replaces the body that the proof signs.
	signedBody []byte
	// server, when set, is the URL of the server that gets the call.
	server string
}

// post returns a POST call of the path with a JSON body.
func (l *launcher) post(path string, body any) lcall {
	return lcall{
		method: http.MethodPost, path: path, body: encodeBody(l.e.t, body), content: nil, key: l.priv, subject: l.id,
		at: l.e.clock.now(), release: "0.4.0", signedBody: nil, server: "",
	}
}

// upload returns a call of PUT path with the body chunk.
func (l *launcher) upload(path string, chunk []byte) lcall {
	return lcall{
		method: http.MethodPut, path: path, body: chunk, content: chunk, key: l.priv, subject: l.id,
		at: l.e.clock.now(), release: "0.4.0", signedBody: nil, server: "",
	}
}

// do sends the call.
func (l *launcher) do(c lcall) (int, []byte) {
	l.e.t.Helper()

	release := c.release
	if release == "-" {
		release = ""
	}

	signed := sum(c.body)
	contentHash := ""

	if c.content != nil {
		contentHash = hex.EncodeToString(sum(c.content))
		signed = sum(c.content)
	}

	if c.signedBody != nil {
		signed = sum(c.signedBody)
	}

	return l.e.sendSigned(c.method, c.path, c.body, signed, signing{
		subject: c.subject, key: c.key, at: c.at, nonce: newNonce(l.e.t), release: release, contentHash: contentHash,
		server: c.server,
	})
}

// stepPath returns the path of a step route with the step name filled in.
func stepPath(pattern, step string) string {
	return strings.NewReplacer("{step}", step).Replace(pattern)
}

// logPath returns the path of the log chunk seq of the step init.
func logPath(seq int) string {
	return strings.NewReplacer("{step}", "init", "{seq}", strconv.Itoa(seq)).Replace(protocol.PathSessionLog)
}

// takeOver lets the lease of the claim pass and gives the phase to another
// runner, with the next claim token.
func (s *sessionEnv) takeOver() {
	s.t.Helper()

	s.lapse(s.work.Operation)

	other := s.newRunner("builder-2")

	if status, body := other.join(s.pool, s.joinToken(s.pool)); status != http.StatusOK {
		s.t.Fatalf("join = %d %s, want 200", status, body)
	}

	work := other.poll(2 * time.Second)
	if work == nil || work.Operation != s.work.Operation || work.Token != s.work.Token+1 {
		s.t.Fatalf("takeover poll = %+v, want the same operation with the next token", work)
	}
}

// opState returns the durable operation of the phase.
func (s *sessionEnv) opState() durable.Operation {
	s.t.Helper()

	op, err := s.store.operations.Get(s.t.Context(), durable.ID(s.work.Operation))
	if err != nil {
		s.t.Fatalf("get operation: %v", err)
	}

	return op
}

// newKeyPair returns a new Ed25519 key pair.
func newKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return pub, priv
}
