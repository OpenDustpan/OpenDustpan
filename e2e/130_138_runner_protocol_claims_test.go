//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/client"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// Names and limits of the runner loop step. The pool holds the job of this
// step only, so the poll of the runner finds no other job.
const (
	loopAgent = "opendustpan-e2e-runner-loop"
	loopUnit  = "runner-loop"
	loopPool  = "runner-loop"
	// loopTick is the period of each ticker of the loop in the step: the
	// renewals, the heartbeats and the waits. The real periods are a third
	// of the lease and runner.HeartbeatEvery.
	loopTick = 500 * time.Millisecond
	// loopLimit is the longest run of the loop in the step.
	loopLimit = 2 * time.Minute
	// loopPollWait is the longest wait of one poll of the loop.
	loopPollWait = 10 * time.Second
)

var errRunnerLoop = errors.New("the runner loop step did not end as expected")

// TestRunnerLoopPollsRenewsAndHeartbeats runs the runner loop of
// pkg/runner/client against the server of each store. The runner joins a
// pool, polls the phase of a plan job, renews its claim and sends heartbeats
// with its key. The step ends the work when the server has answered 200 to a
// renewal and to a heartbeat, and Run then returns nil.
func TestRunnerLoopPollsRenewsAndHeartbeats(t *testing.T) {
	t.Parallel()

	stores, err := Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			runnerLoopScenario(t, NewEnvWithRealTimeSignIn(t.Context(), t, NoCLI(), store))
		})
	}
}

// runnerLoopScenario makes the unit and the pool, starts a plan job, joins a
// runner and runs the loop until the work ends.
func runnerLoopScenario(t *testing.T, env *Env) {
	t.Helper()

	poolID := makeLoopPool(t, env)
	jobID := startLoopJob(t, env)
	fake := env.JoinFakeRunner(t.Context(), t, Namespace, poolID, "loop-runner")
	rec := newLoopRecorder(t, env, fake.id)

	if err := runClaimLoop(t, env, fake, rec); err != nil {
		t.Fatalf("Run = %v, want nil after the context ends", err)
	}

	if err := rec.check(jobID); err != nil {
		t.Fatal(err)
	}
}

// runClaimLoop runs the runner loop of fake with the transport rec. The work
// ends when the server has answered 200 to a renewal and to a heartbeat.
func runClaimLoop(t *testing.T, env *Env, fake *FakeRunner, rec *loopRecorder) error {
	ctx, stop := context.WithTimeout(t.Context(), loopLimit)
	defer stop()

	loop, err := client.New(client.Config{
		Server: env.URL, Transport: rec, RunnerID: fake.id, Key: fake.priv,
		Release: launcherRelease, Lease: runner.MinLease, Wait: loopPollWait,
		Ticker: loopTicker, Now: nil,
		Logger: slog.New(slog.NewTextHandler(loopLogWriter{t: t}, nil)),
	})
	if err != nil {
		return fmt.Errorf("new loop: %w", err)
	}

	return loop.Run(ctx, func(wctx context.Context, w protocol.Work) error {
		t.Logf("handler: runs operation %s of job %s", w.Operation, w.Job)

		select {
		case <-rec.seen:
			t.Log("handler: the work ends")
			stop()
		case <-wctx.Done():
			t.Logf("handler: the work stopped: %v", context.Cause(wctx))
		}

		return nil
	})
}

// loopTicker is the Ticker of the loop in the step: each ticker has the
// period loopTick, so that the renewals and the heartbeats come fast.
func loopTicker(time.Duration) (<-chan time.Time, func()) {
	tick := time.NewTicker(loopTick)

	return tick.C, tick.Stop
}

// loopLogWriter writes each record of the logger of the loop to the test log.
type loopLogWriter struct {
	t *testing.T
}

// Write implements io.Writer.
func (w loopLogWriter) Write(p []byte) (int, error) {
	w.t.Log("loop: " + strings.TrimSpace(string(p)))

	return len(p), nil
}

// loopRecorder is the transport of the loop in the step. It sends each
// request to the server of the environment, logs each poll answer with work,
// each renewal and each heartbeat, and closes seen when the server answered
// 200 to a renewal and to a heartbeat.
type loopRecorder struct {
	t        *testing.T
	inner    envTransport
	runnerID string
	seen     chan struct{}

	mu         sync.Mutex
	works      []protocol.Work
	renewals   int
	heartbeats int
	unsigned   []string
	refused    []string
	closed     bool
}

func newLoopRecorder(t *testing.T, env *Env, runnerID string) *loopRecorder {
	t.Helper()

	return &loopRecorder{
		t: t, inner: envTransport{env: env}, runnerID: runnerID, seen: make(chan struct{}),
		mu: sync.Mutex{}, works: nil, renewals: 0, heartbeats: 0, unsigned: nil, refused: nil, closed: false,
	}
}

// Do implements session.Transport.
func (r *loopRecorder) Do(ctx context.Context, req *session.Request) (*session.Response, error) {
	resp, err := r.inner.Do(ctx, req)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// The proof of each call names the runner as its subject.
	if !strings.HasPrefix(req.Header[protocol.HeaderProof], "v1 sub="+r.runnerID+" ") {
		r.unsigned = append(r.unsigned, req.Path)
	}

	if resp.Status >= http.StatusBadRequest {
		r.refused = append(r.refused, fmt.Sprintf("%s answered %d %s", req.Path, resp.Status, resp.Body))
		r.t.Logf("server: %s %s answered %d", req.Method, req.Path, resp.Status)

		return resp, nil
	}

	r.record(req, resp)

	return resp, nil
}

// record logs and counts one answer with a status below 400. The caller holds
// mu.
func (r *loopRecorder) record(req *session.Request, resp *session.Response) {
	switch {
	case req.Path == protocol.PathPoll && resp.Status == http.StatusOK:
		var w protocol.Work
		if json.Unmarshal(resp.Body, &w) == nil {
			r.works = append(r.works, w)
		}

		r.t.Logf("server: %s %s answered 200 with operation %s of job %s", req.Method, req.Path, w.Operation, w.Job)
	case req.Path == protocol.PathHeartbeat:
		r.heartbeats++
		r.t.Logf("server: %s %s answered %d (heartbeat %d)", req.Method, req.Path, resp.Status, r.heartbeats)
	case strings.HasPrefix(req.Path, protocol.Prefix+"/claims/") && strings.HasSuffix(req.Path, "/renew"):
		r.renewals++
		r.t.Logf("server: %s %s answered %d (renewal %d)", req.Method, req.Path, resp.Status, r.renewals)
	default:
	}

	if r.renewals > 0 && r.heartbeats > 0 && !r.closed {
		r.closed = true
		close(r.seen)
	}
}

// check returns an error unless the loop polled the phase of the job jobID,
// renewed its claim and sent a heartbeat, each call with the key of the
// runner, and the server refused no call.
func (r *loopRecorder) check(jobID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch {
	case len(r.works) != 1 || r.works[0].Job != jobID:
		return fmt.Errorf("%w: polled work %+v, want the phase of job %s", errRunnerLoop, r.works, jobID)
	case r.renewals < 1 || r.heartbeats < 1:
		return fmt.Errorf("%w: %d renewals and %d heartbeats, want at least 1 each", errRunnerLoop,
			r.renewals, r.heartbeats)
	case len(r.unsigned) > 0:
		return fmt.Errorf("%w: calls with no proof of the runner: %q", errRunnerLoop, r.unsigned)
	case len(r.refused) > 0:
		return fmt.Errorf("%w: refused calls: %q", errRunnerLoop, r.refused)
	default:
		return nil
	}
}

// makeLoopPool makes the workspace and the pool of the step and returns the
// ID of the pool.
func makeLoopPool(t *testing.T, env *Env) string {
	t.Helper()

	var poolID string

	env.APIStepA(t.Context(), t, APIStepSpec{
		UserAgent: loopAgent, Name: "runner loop: create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + loopUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(t.Context(), t, APIStepSpec{
		UserAgent: loopAgent, Name: "runner loop: create the runner pool", Method: http.MethodPost,
		Path: "/api/dustpan/v1/namespaces/" + Namespace + "/runner-pools",
		Body: `{"name":"` + loopPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error { return decodeLoopID(body, &poolID) },
	})

	if poolID == "" {
		t.Fatal("the pool of the runner loop step has no ID")
	}

	return poolID
}

// startLoopJob starts a plan job in the pool of the step and returns its ID.
func startLoopJob(t *testing.T, env *Env) string {
	t.Helper()

	var jobID string

	env.APIStepA(t.Context(), t, APIStepSpec{
		UserAgent: loopAgent, Name: "runner loop: start a plan job", Method: http.MethodPost,
		Path: "/api/dustpan/v1/namespaces/" + Namespace + "/jobs",
		Body: fmt.Sprintf(`{"unit":{"tool":"opentofu","name":%q},"operation":"plan","pool":%q,`+
			`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"e2e"}`,
			loopUnit, loopPool),
		Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error { return decodeLoopID(body, &jobID) },
	})

	if jobID == "" {
		t.Fatal("the job of the runner loop step has no ID")
	}

	return jobID
}

// decodeLoopID puts the id field of the JSON body in id.
func decodeLoopID(body string, id *string) error {
	var doc struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.ID == "" {
		return fmt.Errorf("%w: answer %s has no id: %w", errRunnerLoop, body, err)
	}

	*id = doc.ID

	return nil
}
