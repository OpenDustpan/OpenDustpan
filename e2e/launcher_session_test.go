//go:build e2e

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

var errLauncherFlow = errors.New("the launcher flow did not end as expected")

// pollWait is the longest wait of the poll of the fake runner for a phase.
const pollWait = 10 * time.Second

// launched is the state of a launcher that opened the session of a claim.
type launched struct {
	env      *e2e.Env
	job      string
	handoff  string
	launcher *e2e.FakeLauncher
}

// sessionRoundTrip drives the job session of one claim with a fake runner and
// a fake launcher over HTTP: the exchange, a step, one log chunk, the end of
// the step and the finish. It sends hostile calls to the real recorder and
// store, checks that a second finish gets claim-lost, and, on the binary
// server, that the server log holds none of the secrets of the run, that the
// job log reader returns the stored log, and that the blobs hold no
// plaintext.
func sessionRoundTrip(t *testing.T, env *e2e.Env) {
	t.Helper()

	run := openLaunchedSession(t, env)
	step := run.launcher.Assignment.Steps[0].Name
	secretText := "log-secret-line-" + run.job
	chunk := []byte(secretText + "\n")
	finishReason := "finish-secret-reason-" + run.job

	run.refusedBeforeTheStepBegan(t, step, chunk)
	run.writesTheStep(t, step, chunk)
	run.finishesTwice(t, finishReason)

	if env.InProcess() {
		// The server log, the database and the blob directory of the stopped
		// server are the parts of the binary server.
		return
	}

	// The server log is read before the server stops, and holds none of the
	// secrets of the run.
	if found := env.ServerLogHolds(t, run.handoff, secretText, finishReason); len(found) > 0 {
		t.Fatalf("the server log holds %d secrets of the run: %q", len(found), found)
	}

	if got := env.JobStepLog(t.Context(), t, e2e.Namespace, run.job, step); got != string(chunk) {
		t.Fatalf("stored log = %q, want %q", got, chunk)
	}

	// The stored blobs hold the log sealed: no file of the blob directory holds
	// the text of the log.
	if env.BlobDirHolds(t, secretText) {
		t.Fatal("a blob of the blob directory holds the plaintext of the log")
	}
}

// openLaunchedSession makes the unit and the pool, starts a job, joins a fake
// runner, claims the phase and opens the session of the claim.
func openLaunchedSession(t *testing.T, env *e2e.Env) *launched {
	t.Helper()

	ctx := t.Context()

	var poolID string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + jobsUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/runners/" + e2e.Namespace + "/pools", Body: `{"name":"` + jobsPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			var pool struct {
				ID string `json:"id"`
			}

			if err := json.Unmarshal([]byte(body), &pool); err != nil || pool.ID == "" {
				return fmt.Errorf("%w: pool %s: %w", errLauncherFlow, body, err)
			}

			poolID = pool.ID

			return nil
		},
	})

	started := startsAJob(ctx, t, env)
	fake := env.JoinFakeRunner(ctx, t, e2e.Namespace, poolID, "builder-1")

	work := fake.Poll(ctx, t, pollWait)
	if work == nil || work.Job != started.ID {
		t.Fatalf("poll = %+v, want the phase of the job %s", work, started.ID)
	}

	handoff := fake.Handoff(ctx, t, work.Operation)
	launcher := env.OpenSession(ctx, t, handoff)

	if launcher.Assignment.Job != started.ID || len(launcher.Assignment.Steps) == 0 {
		t.Fatalf("assignment = %+v, want the steps of the job %s", launcher.Assignment, started.ID)
	}

	return &launched{env: env, job: started.ID, handoff: handoff, launcher: launcher}
}

// pathOf returns the path of a step route with the step and the chunk number
// filled in.
func pathOf(pattern, step string, seq int) string {
	return strings.NewReplacer("{step}", step, "{seq}", strconv.Itoa(seq)).Replace(pattern)
}

// refusedBeforeTheStepBegan sends the calls that the real recorder and store
// refuse because the step has no begin record. Each is refused and writes
// nothing.
func (r *launched) refusedBeforeTheStepBegan(t *testing.T, step string, chunk []byte) {
	t.Helper()

	ctx := t.Context()
	now := time.Now().UTC()
	badEnd := protocol.StepEnd{ExitCode: 0, StartedAt: now, FinishedAt: now, LogChunks: 0, LogSHA256: ""}

	wantInvalid(t, "end a step that never began", func() (int, []byte) {
		return r.launcher.Post(ctx, t, pathOf(protocol.PathSessionStepEnd, step, 0), badEnd)
	})
	wantInvalid(t, "store a chunk for a step that never began", func() (int, []byte) {
		return r.launcher.PutLog(ctx, t, pathOf(protocol.PathSessionLog, step, 0), chunk)
	})
}

// writesTheStep begins the step, refuses a chunk out of sequence, stores one
// chunk, refuses an end with another hash, and ends the step with the right
// count and hash.
func (r *launched) writesTheStep(t *testing.T, step string, chunk []byte) {
	t.Helper()

	ctx := t.Context()
	now := time.Now().UTC()
	sum := hex.EncodeToString(sha256Of(chunk))

	wantNoContent(t, "begin the step", func() (int, []byte) {
		return r.launcher.Post(ctx, t, pathOf(protocol.PathSessionStepBegin, step, 0), nil)
	})
	wantInvalid(t, "store a chunk out of sequence", func() (int, []byte) {
		return r.launcher.PutLog(ctx, t, pathOf(protocol.PathSessionLog, step, outOfSequence), chunk)
	})
	wantNoContent(t, "store one log chunk", func() (int, []byte) {
		return r.launcher.PutLog(ctx, t, pathOf(protocol.PathSessionLog, step, 0), chunk)
	})

	// An end with another hash is refused and writes nothing: the right end
	// that follows still succeeds, and the log it closes is the one stored.
	wrongEnd := protocol.StepEnd{
		ExitCode: 0, StartedAt: now, FinishedAt: now, LogChunks: 1, LogSHA256: strings.Repeat("0", len(sum)),
	}
	endPath := pathOf(protocol.PathSessionStepEnd, step, 0)
	if status, body := r.launcher.Post(ctx, t, endPath, wrongEnd); status != http.StatusConflict {
		t.Fatalf("end with another hash = %d %s, want 409", status, body)
	}

	wantNoContent(t, "end the step", func() (int, []byte) {
		return r.launcher.Post(ctx, t, pathOf(protocol.PathSessionStepEnd, step, 0), protocol.StepEnd{
			ExitCode: 0, StartedAt: now, FinishedAt: now.Add(time.Second), LogChunks: 1, LogSHA256: sum,
		})
	})
}

// finishesTwice finishes the phase, and then finishes it again: the second
// finish gets claim-lost.
func (r *launched) finishesTwice(t *testing.T, reason string) {
	t.Helper()

	ctx := t.Context()
	finish := protocol.Finish{Succeeded: true, Message: reason}

	wantNoContent(t, "finish the phase", func() (int, []byte) {
		return r.launcher.Post(ctx, t, protocol.PathSessionFinish, finish)
	})

	status, body := r.launcher.Post(ctx, t, protocol.PathSessionFinish, finish)
	if status != http.StatusConflict || !strings.Contains(string(body), protocol.ErrorClaimLost) {
		t.Fatalf("second finish = %d %s, want 409 %s", status, body, protocol.ErrorClaimLost)
	}
}

// outOfSequence is a chunk number above the next one of a log with no chunk.
const outOfSequence = 5

// sha256Of returns the SHA-256 of data.
func sha256Of(data []byte) []byte {
	sum := sha256.Sum256(data)

	return sum[:]
}

// wantInvalid calls send and fails the test when its status is not 400 with
// the code invalid.
func wantInvalid(t *testing.T, name string, send func() (int, []byte)) {
	t.Helper()

	status, body := send()
	if status != http.StatusBadRequest || !strings.Contains(string(body), protocol.ErrorInvalid) {
		t.Fatalf("%s = %d %s, want 400 %s", name, status, body, protocol.ErrorInvalid)
	}
}

// wantNoContent calls send and fails the test when its status is not 204.
func wantNoContent(t *testing.T, name string, send func() (int, []byte)) {
	t.Helper()

	if status, body := send(); status != http.StatusNoContent {
		t.Fatalf("%s = %d %s, want %d", name, status, body, http.StatusNoContent)
	}
}
