//go:build e2e

// This file covers the caps of the job session paths. The e2e launcher client
// (e2e.FakeLauncher, from Env.OpenSession) opens a job session with the handoff
// token of a claim, begins the first step, sends
// a chunk at the step chunk cap, and gets invalid. It then stores chunk 0,
// ends the step with one chunk, and the server holds the one chunk: the
// refused chunk kept no bytes and moved no counter.

package e2e_test

import (
	"encoding/hex"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestJobSessionPathsLogCap runs the log cap step of the job session paths on
// each store.
func TestJobSessionPathsLogCap(t *testing.T) {
	t.Parallel()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runJobSessionLogCap(t, db)
		})
	}
}

// runJobSessionLogCap runs the cap step on one store.
func runJobSessionLogCap(t *testing.T, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, e2e.NoCLI(), store)
	c := claimAPhase(t, env)
	launcher := env.OpenSession(ctx, t, c.handoff)

	if len(launcher.Assignment.Steps) == 0 {
		t.Fatalf("assignment = %+v, want at least one step", launcher.Assignment)
	}

	step := launcher.Assignment.Steps[0].Name

	wantNoContent(t, "begin the step", func() (int, []byte) {
		return launcher.Post(ctx, t, pathOf(protocol.PathSessionStepBegin, step, 0), nil)
	})

	// The chunk numbers of a step are 0 to MaxLogChunksPerStep - 1. A chunk
	// at the cap is invalid, and it stores nothing.
	wantInvalid(t, "a chunk at the step chunk cap", func() (int, []byte) {
		return launcher.PutLog(ctx, t, pathOf(protocol.PathSessionLog, step, protocol.MaxLogChunksPerStep),
			[]byte("refused\n"))
	})

	chunk := []byte("cap-step-log\n")
	sum := hex.EncodeToString(sha256Of(chunk))

	wantNoContent(t, "store chunk 0", func() (int, []byte) {
		return launcher.PutLog(ctx, t, pathOf(protocol.PathSessionLog, step, 0), chunk)
	})

	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	wantNoContent(t, "end the step", func() (int, []byte) {
		return launcher.Post(ctx, t, pathOf(protocol.PathSessionStepEnd, step, 0), protocol.StepEnd{
			ExitCode: 0, StartedAt: now, FinishedAt: now.Add(time.Second), LogChunks: 1, LogSHA256: sum,
		})
	})

	// The refused chunk moved no counter: the end with one chunk and the hash
	// of chunk 0 succeeds. On the binary server, the job log reader returns
	// the one chunk too.
	if !env.InProcess() {
		if got := env.JobStepLog(ctx, t, e2e.Namespace, c.job, step); got != string(chunk) {
			t.Fatalf("stored log = %q, want %q", got, chunk)
		}
	}

	finish := protocol.Finish{Succeeded: true, Message: "cap step done"}
	if status, body := launcher.Post(ctx, t, protocol.PathSessionFinish, finish); status != http.StatusNoContent {
		t.Fatalf("finish = %d %s, want 204", status, body)
	}
}
