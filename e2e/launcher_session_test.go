//go:build e2e

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

var errLauncherFlow = errors.New("the launcher flow did not end as expected")

// pollWait is the longest wait of the poll of the fake runner for a phase.
const pollWait = 10 * time.Second

// sessionRoundTrip drives the job session of one claim with a fake runner and
// a fake launcher over HTTP: the exchange, a step, one log chunk, the end of
// the step and the finish. It reads the stored log back through the job log
// reader, and checks that a second finish gets claim-lost and that the server
// log holds none of the secrets of the run.
func sessionRoundTrip(t *testing.T, env *e2e.Env) {
	t.Helper()

	if env.InProcess() {
		t.Skip("the read of the stored log needs the binary server")
	}

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

	step := launcher.Assignment.Steps[0].Name
	secretText := "log-secret-line-" + started.ID
	chunk := []byte(secretText + "\n")
	sum := sha256.Sum256(chunk)
	now := time.Now().UTC()
	stepPath := func(p string) string { return strings.NewReplacer("{step}", step, "{seq}", "0").Replace(p) }

	wantNoContent(t, "begin the step", func() (int, []byte) {
		return launcher.Post(ctx, t, stepPath(protocol.PathSessionStepBegin), nil)
	})
	wantNoContent(t, "store one log chunk", func() (int, []byte) {
		return launcher.PutLog(ctx, t, stepPath(protocol.PathSessionLog), chunk)
	})
	wantNoContent(t, "end the step", func() (int, []byte) {
		return launcher.Post(ctx, t, stepPath(protocol.PathSessionStepEnd), protocol.StepEnd{
			ExitCode: 0, StartedAt: now, FinishedAt: now.Add(time.Second), LogChunks: 1,
			LogSHA256: hex.EncodeToString(sum[:]),
		})
	})

	finishReason := "finish-secret-reason-" + started.ID
	finish := protocol.Finish{Succeeded: true, Message: finishReason}

	wantNoContent(t, "finish the phase", func() (int, []byte) {
		return launcher.Post(ctx, t, protocol.PathSessionFinish, finish)
	})

	status, body := launcher.Post(ctx, t, protocol.PathSessionFinish, finish)
	if status != http.StatusConflict || !strings.Contains(string(body), protocol.ErrorClaimLost) {
		t.Fatalf("second finish = %d %s, want 409 %s", status, body, protocol.ErrorClaimLost)
	}

	// The server log is read before the server stops, and holds none of the
	// secrets of the run.
	if found := env.ServerLogHolds(t, handoff, secretText, finishReason); len(found) > 0 {
		t.Fatalf("the server log holds %d secrets of the run: %q", len(found), found)
	}

	if got := env.JobStepLog(ctx, t, e2e.Namespace, started.ID, step); got != string(chunk) {
		t.Fatalf("stored log = %q, want %q", got, chunk)
	}
}

// wantNoContent calls send and fails the test when its status is not 204.
func wantNoContent(t *testing.T, name string, send func() (int, []byte)) {
	t.Helper()

	if status, body := send(); status != http.StatusNoContent {
		t.Fatalf("%s = %d %s, want %d", name, status, body, http.StatusNoContent)
	}
}
