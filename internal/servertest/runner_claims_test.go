package servertest_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// poolRunner is a runner that joined a pool: its runner key and its ID.
type poolRunner struct {
	key ed25519.PrivateKey
	id  string
}

// createJobPool makes the runner pool name with the purpose jobs and no lease
// (a lease of zero), and returns the ID of the pool. The pool takes the default
// lease.
func createJobPool(h *rtHarness, name string) string {
	h.t.Helper()

	var pool struct {
		ID string `json:"id"`
	}

	body := encodeJSON(h, map[string]any{"name": name, "purpose": "jobs", "lease": 0})

	h.decode(h.call(http.MethodPost, "/api/dustpan/v1/namespaces/acme/runner-pools", string(body),
		http.StatusCreated), &pool)

	if pool.ID == "" {
		h.t.Fatalf("the pool %s has no id", name)
	}

	return pool.ID
}

// joinPoolRunner makes a join token of the pool poolID and joins the runner
// name with a new ed25519 key.
func joinPoolRunner(h *rtHarness, client *session.Client, poolID, name string) poolRunner {
	h.t.Helper()

	var created struct {
		Token string `json:"token"`
	}

	h.decode(h.call(http.MethodPost, "/api/dustpan/v1/namespaces/acme/runner-pools/"+poolID+"/join-tokens", "",
		http.StatusCreated), &created)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		h.t.Fatalf("generate the key of the runner: %v", err)
	}

	body := encodeJSON(h, protocol.JoinRequest{
		Token: created.Token, Pool: poolID, Name: name, Kind: string(runner.KindMachine),
		Isolation: string(runner.IsolationGVisor), Labels: nil, PublicKey: pub,
		Proof: ed25519.Sign(priv, runner.JoinChallenge(publicURL, poolID, name, created.Token)),
	})

	resp, err := client.Send(h.t.Context(), &session.Request{
		Method: http.MethodPost, Path: protocol.PathJoin, Body: body, Header: nil,
	})
	if err != nil || resp.Status != http.StatusOK {
		h.t.Fatalf("join of %s = %+v, %v; want 200", name, resp, err)
	}

	var joined protocol.JoinResponse

	h.decode(string(resp.Body), &joined)

	return poolRunner{key: priv, id: joined.Runner}
}

// queuePlanJob starts a plan job on the workspace web in the pool name and
// returns the ID of the job.
func queuePlanJob(h *rtHarness, pool string) string {
	h.t.Helper()

	answer := h.call(http.MethodPost, "/api/dustpan/v1/namespaces/acme/jobs",
		`{"unit":{"tool":"opentofu","name":"web"},"operation":"plan","pool":"`+pool+`",`+
			`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"runner claims"}`,
		http.StatusCreated)

	var doc struct {
		ID string `json:"id"`
	}

	h.decode(answer, &doc)

	if doc.ID == "" {
		h.t.Fatalf("job answer %s has no id", answer)
	}

	return doc.ID
}

// runnerPost sends a POST with the proof of the runner key of rn, and returns
// the answer of any status.
func runnerPost(h *rtHarness, client *session.Client, rn poolRunner, path string, body any) *session.Response {
	h.t.Helper()

	var raw []byte

	if body != nil {
		raw = encodeJSON(h, body)
	}

	resp, err := client.Signed(h.t.Context(), rn.key, rn.id, &session.Request{
		Method: http.MethodPost, Path: path, Body: raw, Header: nil,
	}, nil)
	if err != nil {
		h.t.Fatalf("POST %s: %v", path, err)
	}

	return resp
}

// pollOnce polls for work as rn with a short wait.
func pollOnce(h *rtHarness, client *session.Client, rn poolRunner) *session.Response {
	h.t.Helper()

	return runnerPost(h, client, rn, protocol.PathPoll, protocol.PollRequest{
		Wait: protocol.FromStd(time.Second), AssignmentVersion: runner.AssignmentVersion,
	})
}

// wantProtocolError fails the test when resp does not have the status and the
// protocol error code, or when it holds work.
func wantProtocolError(h *rtHarness, what string, resp *session.Response, status int, code string) {
	h.t.Helper()

	if resp.Status != status {
		h.t.Fatalf("%s = %d %s, want %d %s", what, resp.Status, resp.Body, status, code)
	}

	var got protocol.Error

	h.decode(string(resp.Body), &got)

	if got.Code != code {
		h.t.Fatalf("%s code = %q, want %q", what, got.Code, code)
	}

	if strings.Contains(string(resp.Body), `"operation"`) {
		h.t.Fatalf("%s holds work: %s", what, resp.Body)
	}
}

// expireClaim shortens the claim of work by rn to 1 ms and returns when the
// clock of the database is past its expiry.
func expireClaim(h *rtHarness, rn poolRunner, work protocol.Work) {
	h.t.Helper()

	claim := durable.Claim{
		Operation: durable.ID(work.Operation), Owner: rn.id, Token: work.Token, ExpiresAt: work.ExpiresAt,
	}

	short, err := h.cluster.Operations(h.t, 0).Extend(h.t.Context(), claim, time.Millisecond)
	if err != nil {
		h.t.Fatalf("shorten the claim of %s: %v", work.Operation, err)
	}

	st := h.cluster.Store(h.t, 0)
	tick := time.NewTicker(time.Millisecond)

	defer tick.Stop()

	for {
		now, err := st.DatabaseNow(h.t.Context())
		if err != nil {
			h.t.Fatalf("read the clock of the database: %v", err)
		}

		if now.After(short.ExpiresAt) {
			return
		}

		select {
		case <-h.t.Context().Done():
			h.t.Fatal("the test ended before the claim expired")
		case <-tick.C:
		}
	}
}

// TestRunnerOfAnotherPoolGetsNoWork checks that a runner of pool B gets no
// work while a job waits in pool A, and that the job stays queued for a
// runner of pool A.
func TestRunnerOfAnotherPoolGetsNoWork(t *testing.T) {
	t.Parallel()

	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL(publicURL))
	h.workspace = h.createWorkspace("web")

	client := &session.Client{Server: publicURL, Transport: httpTransport{h: h}, Release: "0.0.1", Now: nil}

	poolA := createJobPool(h, "pool-a")
	poolB := createJobPool(h, "pool-b")
	runnerA := joinPoolRunner(h, client, poolA, "builder-a")
	runnerB := joinPoolRunner(h, client, poolB, "builder-b")

	jobID := queuePlanJob(h, "pool-a")

	// Hostile case: the runner key of pool B polls while the work waits in
	// pool A.
	resp := pollOnce(h, client, runnerB)
	if resp.Status != http.StatusNoContent {
		t.Fatalf("poll of the runner of pool B = %d %s, want 204", resp.Status, resp.Body)
	}

	if len(resp.Body) != 0 {
		t.Fatalf("poll of the runner of pool B has a body: %s", resp.Body)
	}

	// The work stays queued: the runner of pool A gets it.
	resp = pollOnce(h, client, runnerA)
	if resp.Status != http.StatusOK {
		t.Fatalf("poll of the runner of pool A = %d %s, want 200", resp.Status, resp.Body)
	}

	var work protocol.Work

	h.decode(string(resp.Body), &work)

	if work.Job != jobID || work.Operation == "" {
		t.Fatalf("work of the runner of pool A = %+v, want the job %s", work, jobID)
	}
}

// TestSingleRequestsRefuseRetiredAndLostClaim checks, with single requests and
// no runner loop, that a retired runner key gets the code retired on a poll,
// and that the renewal of a claim after its lease expired gets the code
// claim-lost. Neither answer holds work.
func TestSingleRequestsRefuseRetiredAndLostClaim(t *testing.T) {
	t.Parallel()

	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL(publicURL))
	h.workspace = h.createWorkspace("web")

	client := &session.Client{Server: publicURL, Transport: httpTransport{h: h}, Release: "0.0.1", Now: nil}

	// The pool takes the default lease; expireClaim shortens the claim.
	poolID := createJobPool(h, "short-lease")
	retired := joinPoolRunner(h, client, poolID, "builder-retired")
	live := joinPoolRunner(h, client, poolID, "builder-live")

	h.call(http.MethodPost, "/api/dustpan/v1/namespaces/acme/runners/"+retired.id+"/retire", "",
		http.StatusNoContent)

	jobID := queuePlanJob(h, "short-lease")

	// Hostile case: the retired runner key polls while work waits in its
	// pool.
	wantProtocolError(h, "poll of the retired runner", pollOnce(h, client, retired),
		http.StatusForbidden, protocol.ErrorRetired)

	// The work stayed queued: the live runner claims it.
	resp := pollOnce(h, client, live)
	if resp.Status != http.StatusOK {
		t.Fatalf("poll of the live runner = %d %s, want 200", resp.Status, resp.Body)
	}

	var work protocol.Work

	h.decode(string(resp.Body), &work)

	if work.Job != jobID || work.Operation == "" {
		t.Fatalf("work of the live runner = %+v, want the job %s", work, jobID)
	}

	// The claim expires at the clock of the database. The test shortens its
	// lease to 1 ms and then reads the clock of the database until it is past
	// the expiry: no wall clock and no wait for the lease of the pool.
	expireClaim(h, live, work)

	// Hostile case: the runner renews a claim whose lease expired.
	path := strings.NewReplacer("{operation}", work.Operation).Replace(protocol.PathClaimRenew)

	wantProtocolError(h, "renew after the expiry of the lease", runnerPost(h, client, live, path, nil),
		http.StatusConflict, protocol.ErrorClaimLost)
}

// TestRenewWithAStaleFenceIsDenied checks that a claim write with a fence that
// moved is denied with namespace.ErrFenced, and that the stored claim keeps its
// expiry. A renewal with the new fence still extends the same claim.
func TestRenewWithAStaleFenceIsDenied(t *testing.T) {
	t.Parallel()

	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL(publicURL))
	h.workspace = h.createWorkspace("web")

	client := &session.Client{Server: publicURL, Transport: httpTransport{h: h}, Release: "0.0.1", Now: nil}

	poolID := createJobPool(h, "fence-pool")
	live := joinPoolRunner(h, client, poolID, "builder-fence")
	queuePlanJob(h, "fence-pool")

	resp := pollOnce(h, client, live)
	if resp.Status != http.StatusOK {
		t.Fatalf("poll of the live runner = %d %s, want 200", resp.Status, resp.Body)
	}

	var work protocol.Work

	h.decode(string(resp.Body), &work)

	st := h.cluster.Store(h.t, 0)
	ctx := h.t.Context()

	claim := durable.Claim{
		Operation: durable.ID(work.Operation), Owner: live.id, Token: work.Token, ExpiresAt: work.ExpiresAt,
	}

	storedExpiry := func() time.Time {
		h.t.Helper()

		op, err := st.DurableOperations().Get(ctx, claim.Operation)
		if err != nil {
			h.t.Fatalf("read the claim of %s: %v", work.Operation, err)
		}

		return op.Claim.ExpiresAt
	}

	before := storedExpiry()

	stale, err := st.GetNamespace(ctx, "acme")
	if err != nil {
		h.t.Fatalf("namespace acme: %v", err)
	}

	// Hostile case: the fence moves after the claim is taken, so the fence the
	// runner holds is stale.
	if _, err = st.BumpFailoverVersions(ctx); err != nil {
		h.t.Fatalf("bump the fence: %v", err)
	}

	now, err := st.DatabaseNow(ctx)
	if err != nil {
		h.t.Fatalf("read the clock of the database: %v", err)
	}

	_, err = st.Runners().RunnerRenew(ctx, claim, stale.Fence(), runner.DefaultLease, now)
	if !errors.Is(err, namespace.ErrFenced) {
		h.t.Fatalf("renew with a stale fence = %v, want namespace.ErrFenced", err)
	}

	if after := storedExpiry(); !after.Equal(before) {
		h.t.Fatalf("the denied renewal moved the claim expiry from %v to %v", before, after)
	}

	current, err := st.GetNamespace(ctx, "acme")
	if err != nil {
		h.t.Fatalf("namespace acme: %v", err)
	}

	if _, err = st.Runners().RunnerRenew(ctx, claim, current.Fence(), runner.DefaultLease, now); err != nil {
		h.t.Fatalf("renew with the new fence: %v", err)
	}

	clockAfter, err := st.DatabaseNow(ctx)
	if err != nil {
		h.t.Fatalf("read the clock of the database: %v", err)
	}

	checkRenewedExpiry(h, now, clockAfter, storedExpiry())
}

// checkRenewedExpiry fails the test when the stored expiry is not the clock of
// the database plus the lease of the pool. The store reads that clock again
// inside the renewal, so the expiry lies between the two clock reads around
// the call, plus the lease.
func checkRenewedExpiry(h *rtHarness, clockBefore, clockAfter, stored time.Time) {
	h.t.Helper()

	lowest := clockBefore.UnixMilli() + runner.DefaultLease.Milliseconds()
	highest := clockAfter.UnixMilli() + runner.DefaultLease.Milliseconds()

	if got := stored.UnixMilli(); got < lowest || got > highest {
		h.t.Fatalf("the renewal set the claim expiry to %d ms, want from %d to %d ms: "+
			"the clock of the database plus the pool lease", got, lowest, highest)
	}
}

// replayTransport sends each request with httpTransport and keeps the last
// signed request, so that a test can send the same proof again. A non-nil
// change edits each request after the proof signed it.
type replayTransport struct {
	inner  httpTransport
	last   *session.Request
	change func(*session.Request) *session.Request
}

// Do implements session.Transport.
func (r *replayTransport) Do(ctx context.Context, req *session.Request) (*session.Response, error) {
	if r.change != nil {
		req = r.change(req)
	}

	r.last = req

	return r.inner.Do(ctx, req)
}

// TestReplayedOrBadPollProofGetsNoWork checks that, while work waits in the
// pool, a poll that sends a proof again, a proof of another key or a body that
// the proof does not sign gets 401 bad-proof and no work. The work stays
// queued for a poll with a new proof.
func TestReplayedOrBadPollProofGetsNoWork(t *testing.T) {
	t.Parallel()

	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL(publicURL))
	h.workspace = h.createWorkspace("web")

	rec := &replayTransport{inner: httpTransport{h: h}, last: nil, change: nil}
	client := &session.Client{Server: publicURL, Transport: rec, Release: "0.0.1", Now: nil}

	poolID := createJobPool(h, "proofs")
	rn := joinPoolRunner(h, client, poolID, "builder-proofs")

	// The first signed poll finds no work. The test keeps its proof.
	resp := pollOnce(h, client, rn)
	if resp.Status != http.StatusNoContent {
		t.Fatalf("first poll = %d %s, want 204", resp.Status, resp.Body)
	}

	replayed := rec.last
	if replayed == nil {
		t.Fatal("the transport kept no request")
	}

	jobID := queuePlanJob(h, "proofs")

	// Hostile case: the same signed poll again while the work waits.
	again, err := rec.inner.Do(t.Context(), replayed)
	if err != nil {
		t.Fatalf("replayed poll: %v", err)
	}

	wantProtocolError(h, "replayed poll", again, http.StatusUnauthorized, protocol.ErrorBadProof)

	// Hostile case: a proof of a key that is not the runner key.
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate another key: %v", err)
	}

	wantProtocolError(h, "poll with the proof of another key", pollOnce(h, client, poolRunner{key: other, id: rn.id}),
		http.StatusUnauthorized, protocol.ErrorBadProof)

	// Hostile case: a body that the proof does not sign.
	rec.change = func(req *session.Request) *session.Request {
		return &session.Request{
			Method: req.Method, Path: req.Path, Body: append(slices.Clone(req.Body), ' '), Header: req.Header,
		}
	}

	wantProtocolError(h, "poll with a changed body", pollOnce(h, client, rn),
		http.StatusUnauthorized, protocol.ErrorBadProof)

	rec.change = nil

	// The work stayed queued: a poll with a new proof gets it.
	resp = pollOnce(h, client, rn)
	if resp.Status != http.StatusOK {
		t.Fatalf("poll with a new proof = %d %s, want 200", resp.Status, resp.Body)
	}

	var work protocol.Work

	h.decode(string(resp.Body), &work)

	if work.Job != jobID || work.Operation == "" {
		t.Fatalf("work of the runner = %+v, want the job %s", work, jobID)
	}
}

// TestPoolShowsTheEffectiveLease checks that a pool that sets no lease shows
// the default lease of 90 s in the API, so that the value a runner works with
// is visible to the user.
func TestPoolShowsTheEffectiveLease(t *testing.T) {
	t.Parallel()

	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL(publicURL))

	body := h.call(http.MethodPost, "/api/dustpan/v1/namespaces/acme/runner-pools",
		`{"name":"no-lease","purpose":"jobs"}`, http.StatusCreated)

	if !strings.Contains(body, `"lease":90000`) {
		t.Fatalf("pool without a lease = %s, want the effective lease 90000 ms", body)
	}
}
