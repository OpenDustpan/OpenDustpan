package servertest_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// poolRunner is a runner that joined a pool: its runner key and its ID.
type poolRunner struct {
	key ed25519.PrivateKey
	id  string
}

// createJobPool makes the runner pool name with the purpose jobs and the
// lease in milliseconds, and returns the ID of the pool. A lease of zero takes
// the default lease.
func createJobPool(h *rtHarness, name string, leaseMs int64) string {
	h.t.Helper()

	var pool struct {
		ID string `json:"id"`
	}

	body := encodeJSON(h, map[string]any{"name": name, "purpose": "jobs", "lease": leaseMs})

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

	poolA := createJobPool(h, "pool-a", 0)
	poolB := createJobPool(h, "pool-b", 0)
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

// TestLoopStopsOnRetiredAndLostClaim checks that a retired runner key gets
// the code retired on a poll, and that the renewal of a claim after its lease
// expired gets the code claim-lost. Neither answer holds work.
func TestLoopStopsOnRetiredAndLostClaim(t *testing.T) {
	t.Parallel()

	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL(publicURL))
	h.workspace = h.createWorkspace("web")

	client := &session.Client{Server: publicURL, Transport: httpTransport{h: h}, Release: "0.0.1", Now: nil}

	// The pool takes the default lease; expireClaim shortens the claim.
	poolID := createJobPool(h, "short-lease", 0)
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
