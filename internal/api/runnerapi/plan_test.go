package runnerapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/secret"
)

// planPut is one call of PutPlanPart that the fake recorder got.
type planPut struct {
	claim job.PhaseClaim
	kind  job.OutputKind
	body  []byte
	sum   string
}

// planCommit is one call of CommitPlan that the fake recorder got.
type planCommit struct {
	claim job.PhaseClaim
	parts []job.PlanCommitPart
}

// fakePlans is a job.PlanRecorder in memory. It keeps what the handler passed,
// and returns the error that a case sets.
type fakePlans struct {
	mu        sync.Mutex
	puts      []planPut
	commits   []planCommit
	putErr    error
	commitErr error
}

var _ job.PlanRecorder = (*fakePlans)(nil)

func (*fakePlans) BeginPlan(context.Context, job.PhaseClaim) (job.PlanRef, error) {
	return job.PlanRef{}, errUnused
}

func (f *fakePlans) PutPlanPart(
	_ context.Context, c job.PhaseClaim, kind job.OutputKind, body io.Reader, sum string,
) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.puts = append(f.puts, planPut{claim: c, kind: kind, body: data, sum: sum})

	return f.putErr
}

func (f *fakePlans) CommitPlan(_ context.Context, c job.PhaseClaim, parts []job.PlanCommitPart) (job.PlanRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.commits = append(f.commits, planCommit{claim: c, parts: parts})

	return job.PlanRef{}, f.commitErr
}

// touched returns the number of calls the recorder got.
func (f *fakePlans) touched() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.puts) + len(f.commits)
}

// planRead is one call of OpenPlanPart that the fake reader got.
type planRead struct {
	ns   namespace.Name
	plan string
	kind job.OutputKind
}

// fakeReads is a job.ArtifactReader in memory.
type fakeReads struct {
	mu      sync.Mutex
	reads   []planRead
	content map[job.OutputKind]string
	err     error
}

var _ job.ArtifactReader = (*fakeReads)(nil)

func (f *fakeReads) OpenPlanPart(
	_ context.Context, ns namespace.Name, plan string, k job.OutputKind,
) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.reads = append(f.reads, planRead{ns: ns, plan: plan, kind: k})

	if f.err != nil {
		return nil, f.err
	}

	return io.NopCloser(strings.NewReader(f.content[k])), nil
}

func (*fakeReads) OpenLog(context.Context, namespace.Name, string) (io.ReadCloser, error) {
	return nil, errUnused
}

func (f *fakeReads) called() []planRead {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.reads)
}

// Contents of the plan parts of the cases.
const (
	planDisplayText = `{"changes": 3}`
	planApplyText   = "binary plan file"
	planID          = "pln-fromthejob"
)

// planJobs is the job reader of the plan cases: the plan phase at index 0 and
// the apply phase at index 1. Phase 0 holds the plan p, or none for a nil p.
type planJobs struct {
	*sessionJobs

	plan *job.PlanRef
}

func (f *planJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := f.sessionJobs.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	j.Phases[0].Plan = f.plan
	j.Phases = append(j.Phases, job.Phase{
		Kind: job.PhaseApply, Operation: string(id), Claims: 1, Executor: "", Steps: nil, StateVersion: "",
		Plan: nil, Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	})

	return j, nil
}

// storedPlan returns the plan that the cases keep in the job.
func storedPlan() *job.PlanRef {
	return &job.PlanRef{
		ID: planID, ItemKey: "", StateVersion: "", CreatedAt: time.Time{}, ExpiresAt: time.Time{},
		RedactedAt: time.Time{},
		Parts: []job.PlanPart{
			{
				Kind: job.OutputPlanDisplay, Size: int64(len(planDisplayText)), SHA256: textHash(planDisplayText),
				Secret: false, StoredSize: 0, StoredMD5: "",
			},
			{
				Kind: job.OutputPlanApply, Size: int64(len(planApplyText)), SHA256: textHash(planApplyText),
				Secret: true, StoredSize: 0, StoredMD5: "",
			},
		},
	}
}

// textHash returns the SHA-256 of s in lowercase hex.
func textHash(s string) string {
	d := sha256.Sum256([]byte(s))

	return hex.EncodeToString(d[:])
}

// phaseClaimer is the claimer of the session tests with a claim of the job
// job-1 at a phase index that the case sets.
type phaseClaimer struct {
	*sessionClaimer

	phase int
}

func (c *phaseClaimer) Claim(ctx context.Context, req job.ClaimRequest) (job.Assignment, error) {
	a, err := c.sessionClaimer.Claim(ctx, req)
	if err != nil {
		return job.Assignment{}, err
	}

	a.Claim.Job, a.Claim.Phase = "job-1", c.phase

	return a, nil
}

// planEnv is a session environment whose claim is the phase at index phase of
// job-1, with a fake plan recorder and a fake plan reader.
type planEnv struct {
	*sessionEnv

	plans *fakePlans
	reads *fakeReads
}

// newPlanEnv opens a store, joins a runner and claims the phase at index phase.
func newPlanEnv(t *testing.T, b backend, phase int, stored *job.PlanRef) *planEnv {
	t.Helper()

	jobs := &planJobs{sessionJobs: newSessionJobs(), plan: stored}
	plans := &fakePlans{mu: sync.Mutex{}, puts: nil, commits: nil, putErr: nil, commitErr: nil}
	reads := &fakeReads{
		mu: sync.Mutex{}, reads: nil, err: nil,
		content: map[job.OutputKind]string{job.OutputPlanDisplay: planDisplayText, job.OutputPlanApply: planApplyText},
	}
	logs := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}
	claimer := &sessionClaimer{opsClaimer: nil, finished: atomic.Int64{}, outcome: atomic.Pointer[job.Outcome]{}}

	e := newEnvServing(t, b.open, func(s stores, o *serveOptions) {
		claimer.opsClaimer = &opsClaimer{ops: s.operations, released: atomic.Int64{}}
		o.claimer, o.recorder, o.jobs = &phaseClaimer{sessionClaimer: claimer, phase: phase}, jobs, jobs
		o.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		o.mutate = func(cfg *runnerapi.Config) { cfg.Plans, cfg.Artifacts = plans, reads }
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

	s := &sessionEnv{env: e, jobs: jobs.sessionJobs, claimer: claimer, logs: logs, pool: pool, runner: r, work: work}

	return &planEnv{sessionEnv: s, plans: plans, reads: reads}
}

// planUploadPath returns the path of the upload of the part name.
func planUploadPath(name string) string {
	return strings.NewReplacer("{part}", name).Replace(protocol.PathSessionPlanUpload)
}

// planPartPath returns the path of the read of the part name.
func planPartPath(name string) string {
	return strings.NewReplacer("{part}", name).Replace(protocol.PathSessionPlanPart)
}

// get returns a signed GET call of the path.
func (l *launcher) get(path string) lcall {
	return lcall{
		method: http.MethodGet, path: path, body: nil, content: nil, key: l.priv, subject: l.id,
		at: l.e.clock.now(), release: "0.4.0", signedBody: nil, server: "",
	}
}

// signedProof returns the value of the proof header of the call c, signed with
// its key.
func signedProof(t *testing.T, c *lcall) string {
	t.Helper()

	proof := runner.Proof{Subject: c.subject, At: c.at, Nonce: newNonce(t), Signature: nil}
	proof.Signature = ed25519.Sign(c.key, runner.ProofInput(proof, runner.Signed{
		Method: c.method, PathQuery: c.path, BodySHA256: sum(c.body),
	}))

	return fmt.Sprintf("v1 sub=%s t=%d n=%s sig=%s", proof.Subject, proof.At.UnixMilli(), proof.Nonce,
		base64.RawURLEncoding.EncodeToString(proof.Signature))
}

// commitOf returns a commit of the standard parts.
func commitOf() protocol.PlanCommit {
	return protocol.PlanCommit{Parts: []protocol.PlanPart{
		{Kind: string(job.OutputPlanDisplay), Size: int64(len(planDisplayText)), SHA256: textHash(planDisplayText)},
		{Kind: string(job.OutputPlanApply), Size: int64(len(planApplyText)), SHA256: textHash(planApplyText)},
	}}
}

// TestPlanUploadAndCommit proves the whole store of a plan: two uploads and a
// commit reach the recorder with the claim of the session, the signed hash and
// the named parts, and a second commit is the same call again.
func TestPlanUploadAndCommit(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 0, nil)
			l := s.launch()

			l.expect(t, "upload display", l.upload(planUploadPath("plan-display"), []byte(planDisplayText)), 204, "")
			l.expect(t, "upload apply", l.upload(planUploadPath("plan-apply"), []byte(planApplyText)), 204, "")
			l.expect(t, "commit", l.post(protocol.PathSessionPlanCommit, commitOf()), 204, "")
			l.expect(t, "second commit", l.post(protocol.PathSessionPlanCommit, commitOf()), 204, "")

			checkPlanCalls(t, s)
		})
	}
}

// checkPlanCalls fails t unless the fake recorder of s got the two uploads and
// the two commits of TestPlanUploadAndCommit, each with the claim of the
// session.
func checkPlanCalls(t *testing.T, s *planEnv) {
	t.Helper()

	if len(s.plans.puts) != 2 || len(s.plans.commits) != 2 {
		t.Fatalf("recorder got %d puts and %d commits, want 2 and 2", len(s.plans.puts), len(s.plans.commits))
	}

	checkPut(t, s, 0, job.OutputPlanDisplay, planDisplayText)
	checkPut(t, s, 1, job.OutputPlanApply, planApplyText)

	if got := s.plans.commits[0]; len(got.parts) != 2 || got.parts[0].SHA256 != textHash(planDisplayText) ||
		got.parts[1].Kind != job.OutputPlanApply || got.parts[1].Size != int64(len(planApplyText)) {
		t.Errorf("commit parts = %+v, want the two named parts", got.parts)
	}
}

// checkPut fails t unless upload i of the fake recorder of s is the part kind
// with the content text and its hash, under the claim of the session.
func checkPut(t *testing.T, s *planEnv, i int, kind job.OutputKind, text string) {
	t.Helper()

	got := &s.plans.puts[i]
	if got.kind != kind || string(got.body) != text || got.sum != textHash(text) {
		t.Errorf("put %d = %s %q %s, want %s %q %s", i, got.kind, got.body, got.sum, kind, text, textHash(text))
	}

	// The claim comes from the session, never from the request.
	if got.claim.Job != "job-1" || got.claim.Phase != 0 || got.claim.Fence.Namespace != "acme" ||
		got.claim.Claim.Token != s.work.Token {
		t.Errorf("put %d claim = %+v, want job-1 phase 0 of acme with the token of the session", i, got.claim)
	}
}

// TestPlanUploadHash proves the hash of an upload at the handler: the recorder
// gets the hash that the proof signed and the bytes that came, so a body that
// is not the signed body is the recorder's mismatch, and the handler answers it
// as bad-proof.
func TestPlanUploadHash(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 0, nil)
			l := s.launch()

			// The proof and the header carry the hash of "signed"; the body is
			// another text. The recorder, as the durable recorder does, compares.
			c := l.upload(planUploadPath("plan-display"), []byte("signed"))
			c.body = []byte("tampered")

			s.plans.putErr = fmt.Errorf("%w: plan part", job.ErrHashMismatch)

			status, body := l.do(c)
			if status != http.StatusUnauthorized || errorCode(t, body) != protocol.ErrorBadProof {
				t.Fatalf("tampered body = %d %s, want 401 bad-proof", status, body)
			}

			if len(s.plans.puts) != 1 || s.plans.puts[0].sum != textHash("signed") ||
				string(s.plans.puts[0].body) != "tampered" {
				t.Fatalf("recorder got %+v, want the signed hash and the bytes that came", s.plans.puts)
			}
		})
	}
}

// TestPlanUploadHeaderIsChecked proves that a header that is no SHA-256 fails
// with invalid before the recorder gets a call.
func TestPlanUploadHeaderIsChecked(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 0, nil)
			l := s.launch()

			for _, header := range []string{"not-a-hash", textHash("x")[:10], textHash("x") + "00"} {
				sg := signing{
					subject: l.id, key: l.priv, at: s.clock.now(), nonce: newNonce(t), release: "0.4.0",
					contentHash: header, server: "",
				}

				status, body := s.sendSigned(
					http.MethodPut,
					planUploadPath("plan-display"),
					[]byte("x"),
					sum([]byte("x")),
					sg,
				)
				if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
					t.Errorf("header %q = %d %s, want 400 invalid", header, status, body)
				}
			}

			if n := s.plans.touched(); n != 0 {
				t.Fatalf("a refused upload reached the recorder %d times", n)
			}
		})
	}
}

// TestPlanRecorderErrors proves how the handler answers each error of the
// recorder: a second commit with other parts, a content that differs from the
// named part, a stale plan and a lost claim.
func TestPlanRecorderErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			"a second commit with other parts", fmt.Errorf("%w: plan", job.ErrConflict), http.StatusConflict,
			protocol.ErrorConflict,
		},
		{
			"a part that is not the named one", fmt.Errorf("%w: part", job.ErrHashMismatch), http.StatusConflict,
			protocol.ErrorConflict,
		},
		{"a stale plan", fmt.Errorf("%w: plan", job.ErrPlanStale), http.StatusConflict, protocol.ErrorPlanStale},
		{
			"a part that was not uploaded", fmt.Errorf("%w: part", job.ErrInvalid), http.StatusBadRequest,
			protocol.ErrorInvalid,
		},
		{
			"a claim that is lost", fmt.Errorf("%w: claim", job.ErrClaimLost), http.StatusConflict,
			protocol.ErrorClaimLost,
		},
		{
			"a fence that is stale", fmt.Errorf("%w: fence", namespace.ErrFenced), http.StatusServiceUnavailable,
			protocol.ErrorUnavailable,
		},
	}

	for _, b := range wiringBackends() {
		for _, tc := range tests {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				s := newPlanEnv(t, b, 0, nil)
				l := s.launch()
				s.plans.commitErr = tc.err

				status, body := l.do(l.post(protocol.PathSessionPlanCommit, commitOf()))
				if status != tc.status || (tc.code != "" && errorCode(t, body) != tc.code) {
					t.Fatalf("commit = %d %s, want %d %s", status, body, tc.status, tc.code)
				}
			})
		}
	}
}

// TestPlanPartNamesAreChecked proves the allowlist of the part name on each
// plan route: a name that is not one of the two plan outputs fails with invalid
// before the recorder or the reader gets a call.
func TestPlanPartNamesAreChecked(t *testing.T) {
	t.Parallel()

	names := []string{
		"log", "PLAN-APPLY", "plan-apply%00", "plan-apply%0D", "plan-apply%0A", "plan-display%2F..", "a%2Fb",
		"plan-display%20", "%E2%80%AE", "plan",
	}

	for _, b := range wiringBackends() {
		for _, name := range names {
			t.Run(b.name+"/"+name, func(t *testing.T) {
				t.Parallel()

				s := newPlanEnv(t, b, 0, nil)
				l := s.launch()

				l.expect(t, "upload", l.upload(planUploadPath(name), []byte("x")), http.StatusBadRequest,
					protocol.ErrorInvalid)
				l.expect(t, "read", l.get(planPartPath(name)), http.StatusBadRequest, protocol.ErrorInvalid)

				if n := s.plans.touched(); n != 0 || len(s.reads.called()) != 0 {
					t.Fatalf("a refused name reached the recorder (%d) or the reader (%d)", n, len(s.reads.called()))
				}
			})
		}
	}
}

// TestPlanCommitIsChecked proves the checks of a commit at the handler, each
// before the recorder gets a call.
func TestPlanCommitIsChecked(t *testing.T) {
	t.Parallel()

	good := commitOf().Parts

	tests := []struct {
		name string
		body any
	}{
		{"no part", protocol.PlanCommit{Parts: nil}},
		{"nine parts", protocol.PlanCommit{Parts: slices.Repeat(good, 5)}},
		{"a kind that is a log", protocol.PlanCommit{Parts: []protocol.PlanPart{
			{Kind: "log", Size: 1, SHA256: textHash("x")},
		}}},
		{"a kind with a newline", protocol.PlanCommit{Parts: []protocol.PlanPart{
			{Kind: "plan-apply\n", Size: 1, SHA256: textHash("x")},
		}}},
		{"a short hash", protocol.PlanCommit{Parts: []protocol.PlanPart{
			{Kind: "plan-apply", Size: 1, SHA256: "abcd"},
		}}},
		{"an upper case hash", protocol.PlanCommit{Parts: []protocol.PlanPart{
			{Kind: "plan-apply", Size: 1, SHA256: strings.ToUpper(textHash("x"))},
		}}},
		{"a negative size", protocol.PlanCommit{Parts: []protocol.PlanPart{
			{Kind: "plan-apply", Size: -1, SHA256: textHash("x")},
		}}},
		{"a size above the limit", protocol.PlanCommit{Parts: []protocol.PlanPart{
			{Kind: "plan-apply", Size: job.MaxPlanPart + 1, SHA256: textHash("x")},
		}}},
		{"an unknown field", map[string]any{"parts": []any{}, "plan": "pln-other"}},
		{"a plan ID of another plan", map[string]any{"parts": good, "id": "pln-other"}},
	}

	for _, b := range wiringBackends() {
		for _, tc := range tests {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				s := newPlanEnv(t, b, 0, nil)
				l := s.launch()

				l.expect(t, "commit", l.post(protocol.PathSessionPlanCommit, tc.body), http.StatusBadRequest,
					protocol.ErrorInvalid)

				if n := s.plans.touched(); n != 0 {
					t.Fatalf("a refused commit reached the recorder %d times", n)
				}
			})
		}
	}
}

// TestPlanRoutesNeedThePhaseOfTheirKind proves that an upload and a commit work
// in a plan phase alone, and a read in an apply phase alone.
func TestPlanRoutesNeedThePhaseOfTheirKind(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name+"/apply phase", func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 1, storedPlan())
			l := s.launch()

			l.expect(t, "upload", l.upload(planUploadPath("plan-apply"), []byte("x")), http.StatusBadRequest,
				protocol.ErrorInvalid)
			l.expect(t, "commit", l.post(protocol.PathSessionPlanCommit, commitOf()), http.StatusBadRequest,
				protocol.ErrorInvalid)

			if n := s.plans.touched(); n != 0 {
				t.Fatalf("a plan write in an apply phase reached the recorder %d times", n)
			}
		})

		t.Run(b.name+"/plan phase", func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 0, storedPlan())
			l := s.launch()

			l.expect(t, "read", l.get(planPartPath("plan-apply")), http.StatusBadRequest, protocol.ErrorInvalid)

			if got := s.reads.called(); len(got) != 0 {
				t.Fatalf("a read in a plan phase reached the reader: %v", got)
			}
		})
	}
}

// TestPlanRoutesWithALostClaim proves that after another runner took the phase
// no plan call of the old session reaches the recorder or the reader, also with
// a valid proof of the old session key.
func TestPlanRoutesWithALostClaim(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		for _, phase := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/phase %d", b.name, phase), func(t *testing.T) {
				t.Parallel()

				s := newPlanEnv(t, b, phase, storedPlan())
				l := s.launch()

				s.takeOver()

				calls := []lcall{
					l.upload(planUploadPath("plan-display"), []byte("x")),
					l.post(protocol.PathSessionPlanCommit, commitOf()),
					l.get(planPartPath("plan-apply")),
				}

				for _, c := range calls {
					l.expect(t, c.method+" "+c.path, c, http.StatusConflict, protocol.ErrorClaimLost)
				}

				if n := s.plans.touched(); n != 0 || len(s.reads.called()) != 0 {
					t.Fatalf("a call with a lost claim reached the recorder (%d) or the reader (%d)", n,
						len(s.reads.called()))
				}
			})
		}
	}
}

// TestPlanRoutesNeedTheKeyOfTheSession proves that a proof of another key is
// refused on each plan route before anything else runs.
func TestPlanRoutesNeedTheKeyOfTheSession(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 0, storedPlan())
			l := s.launch()
			_, other := newKeyPair(t)

			for _, c := range []lcall{
				l.upload(planUploadPath("plan-display"), []byte("x")),
				l.post(protocol.PathSessionPlanCommit, commitOf()),
				l.get(planPartPath("plan-display")),
			} {
				c.key = other

				l.expect(t, c.method+" "+c.path, c, http.StatusUnauthorized, protocol.ErrorBadProof)
			}

			// The proof of the runner key does not pass the scope of the session.
			c := l.get(planPartPath("plan-display"))
			c.key, c.subject = s.runner.priv, s.runner.id

			if status, body := l.do(c); status != http.StatusUnauthorized {
				t.Errorf("read with the runner key = %d %s, want 401", status, body)
			}

			if n := s.plans.touched(); n != 0 || len(s.reads.called()) != 0 {
				t.Fatalf("a refused call reached the recorder (%d) or the reader (%d)", n, len(s.reads.called()))
			}
		})
	}
}

// TestPlanPartRead proves the read of a plan part by an apply phase: the plan
// is the one that the job holds, in the namespace of the claim, whatever the
// request says, and the bytes come from the reader.
func TestPlanPartRead(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 1, storedPlan())
			l := s.launch()

			status, body := l.do(l.get(planPartPath("plan-apply")))
			if status != http.StatusOK || string(body) != planApplyText {
				t.Fatalf("read = %d %q, want 200 %q", status, body, planApplyText)
			}

			status, body = l.do(l.get(planPartPath("plan-display")))
			if status != http.StatusOK || string(body) != planDisplayText {
				t.Fatalf("read = %d %q, want 200 %q", status, body, planDisplayText)
			}

			// A query that names another plan or namespace changes nothing.
			l.expect(t, "read with a query", l.get(planPartPath("plan-display")+"?plan=pln-other&namespace=other"),
				http.StatusOK, "")

			want := []planRead{
				{ns: "acme", plan: planID, kind: job.OutputPlanApply},
				{ns: "acme", plan: planID, kind: job.OutputPlanDisplay},
				{ns: "acme", plan: planID, kind: job.OutputPlanDisplay},
			}
			if got := s.reads.called(); !slices.Equal(got, want) {
				t.Fatalf("reader calls = %v, want %v", got, want)
			}
		})
	}
}

// TestPlanPartReadHeaders proves that the answer holds the size and the hash
// that the server stored, so the launcher checks the bytes it got.
func TestPlanPartReadHeaders(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 1, storedPlan())
			l := s.launch()

			c := l.get(planPartPath("plan-apply"))
			proof := signedProof(t, &c)

			req, err := http.NewRequestWithContext(t.Context(), c.method, s.server.URL+c.path, http.NoBody)
			if err != nil {
				t.Fatalf("request: %v", err)
			}

			req.Header.Set(protocol.HeaderRelease, c.release)
			req.Header.Set(protocol.HeaderProof, proof)

			resp, err := s.server.Client().Do(req)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			defer func() { _ = resp.Body.Close() }() //nolint:errcheck // The test body is read.

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			if resp.StatusCode != http.StatusOK || string(data) != planApplyText ||
				resp.Header.Get(protocol.HeaderContentSHA256) != textHash(planApplyText) ||
				resp.ContentLength != int64(len(planApplyText)) ||
				resp.Header.Get("Content-Type") != "application/octet-stream" {
				t.Fatalf(
					"answer = %d %q %v, want the part with its hash, size and type",
					resp.StatusCode,
					data,
					resp.Header,
				)
			}
		})
	}
}

// TestPlanPartReadRefusals proves the answers for a plan that is not there: no
// plan in the job, a part that the plan lacks, and the errors of the reader.
func TestPlanPartReadRefusals(t *testing.T) {
	t.Parallel()

	onlyDisplay := storedPlan()
	onlyDisplay.Parts = onlyDisplay.Parts[:1]

	tests := []struct {
		name    string
		stored  *job.PlanRef
		part    string
		readErr error
		status  int
		code    string
		reads   int
	}{
		{"no plan in the job", nil, "plan-apply", nil, http.StatusNotFound, protocol.ErrorNotFound, 0},
		{"a part the plan lacks", onlyDisplay, "plan-apply", nil, http.StatusNotFound, protocol.ErrorNotFound, 0},
		{
			"a part the reader does not find", storedPlan(), "plan-apply", job.ErrNotFound, http.StatusNotFound,
			protocol.ErrorNotFound, 1,
		},
		{
			"a plan with a shredded key", storedPlan(), "plan-apply", secret.ErrShredded, http.StatusNotFound,
			protocol.ErrorNotFound, 1,
		},
	}

	for _, b := range wiringBackends() {
		for _, tc := range tests {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				s := newPlanEnv(t, b, 1, tc.stored)
				l := s.launch()
				s.reads.err = tc.readErr

				status, body := l.do(l.get(planPartPath(tc.part)))
				if status == http.StatusOK || (tc.status != 0 && status != tc.status) ||
					(tc.code != "" && errorCode(t, body) != tc.code) {
					t.Fatalf("read = %d %s, want %d %s", status, body, tc.status, tc.code)
				}

				if got := s.reads.called(); len(got) != tc.reads {
					t.Fatalf("reader calls = %v, want %d", got, tc.reads)
				}
			})
		}
	}
}

// TestPlanRoutesFailClosedWithoutADependency proves that a server without the
// plan recorder or the plan reader answers not-found, and that a handler with
// either and no job reader does not start.
func TestPlanRoutesFailClosedWithoutADependency(t *testing.T) {
	t.Parallel()

	for _, b := range wiringBackends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newPlanEnv(t, b, 0, storedPlan())
			l := s.launch()

			url := s.replica(func(cfg *runnerapi.Config) { cfg.Plans, cfg.Artifacts = nil, nil })

			for _, c := range []lcall{
				l.upload(planUploadPath("plan-display"), []byte("x")),
				l.post(protocol.PathSessionPlanCommit, commitOf()),
				l.get(planPartPath("plan-display")),
			} {
				l.expect(t, c.method+" "+c.path, via(c, url), http.StatusNotFound, protocol.ErrorNotFound)
			}

			if n := s.plans.touched(); n != 0 {
				t.Fatalf("a server without the dependency reached the recorder %d times", n)
			}

			for name, mutate := range map[string]func(cfg *runnerapi.Config){
				"plans without a job reader":     func(cfg *runnerapi.Config) { cfg.Jobs, cfg.Recorder, cfg.Artifacts = nil, nil, nil },
				"artifacts without a job reader": func(cfg *runnerapi.Config) { cfg.Jobs, cfg.Recorder, cfg.Plans = nil, nil, nil },
			} {
				cfg, _ := configFor(t, b.open(t), serveOptions{
					now: time.Now, authz: identity.AdminOnly{}, claimer: nil, recorder: nil, jobs: nil, logger: nil,
					mutate: func(cfg *runnerapi.Config) { cfg.Plans, cfg.Artifacts = s.plans, s.reads },
				})
				mutate(&cfg)

				if _, err := runnerapi.NewHandler(cfg); err == nil || !strings.Contains(err.Error(), "job reader") {
					t.Errorf("NewHandler with %s = %v, want ErrMissingDependency", name, err)
				}
			}
		})
	}
}
