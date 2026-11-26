//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestJobStartIdempotencyAfterRestart proves that a service principal that
// starts a job with an Idempotency-Key gets the stored job when it sends the
// same key again after the real server binary restarted. The job conformance
// suite has the cases past the retention age, "a key of a job older than the
// retention starts a new job that holds the key" and "a retry after the new
// job replays the new job".
func TestJobStartIdempotencyAfterRestart(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary: the test restarts the server")
	}

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			ctx := t.Context()
			env := e2e.NewEnvWithSignIn(ctx, t, e2e.NoCLI(), store)

			jobStartRetryAfterRestart(ctx, t, env)
		})
	}
}

// jobStartRetryAfterRestart starts one plan job as a service principal, kills
// and starts the server, and sends the same request and key again.
func jobStartRetryAfterRestart(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + jobsUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/runner-pools",
		Body: `{"name":"` + jobsPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	token := jobStartService(ctx, t, env)
	key := "job-start-restart-1"

	first := startAsService(ctx, t, env, serviceStart{
		token: token, key: key, name: "start a plan job as a service principal",
	})

	env.KillServer(t)
	env.StartServer(ctx, t)

	replay, replayed := retryAsService(ctx, t, env, serviceStart{
		token: token, key: key, name: "retry the start after the restart",
	})

	if replay.ID != first.ID {
		t.Fatalf("the retry after the restart made job %s, not %s", replay.ID, first.ID)
	}

	if replayed != "true" {
		t.Fatalf("the retry after the restart answered Idempotent-Replayed %q, want true", replayed)
	}
}

// jobStartService makes a service principal with the administrator role on the
// namespace and one token, and returns the token.
func jobStartService(ctx context.Context, t *testing.T, env *e2e.Env) e2e.PersonalToken {
	t.Helper()

	var principal e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		method: http.MethodPost, path: "/api/server/principals",
		body: `{"subject":"job-start-agent","kind":"service"}`,
		want: http.StatusCreated,
	}, &principal)

	principalCall(ctx, t, env, principalStep{
		method: http.MethodPost, path: "/api/server/namespaces/" + e2e.Namespace + "/grants",
		body: `{"subject":"principal:` + principal.ID + `","role":"admin"}`,
		want: http.StatusCreated,
	}, nil)

	var tok e2eToken

	principalCall(ctx, t, env, principalStep{
		method: http.MethodPost, path: "/api/server/principals/" + principal.ID + "/tokens",
		body: `{"name":"job-start"}`, want: http.StatusCreated,
	}, &tok)

	return e2e.PersonalToken{ID: tok.ID, Secret: tok.Secret}
}

// serviceStart is one start request of the service principal: its token, its
// idempotency key and the name of the step.
type serviceStart struct {
	token e2e.PersonalToken
	key   string
	name  string
}

// startAsService starts the plan job with the token of the service principal
// and the idempotency key of req, and returns the job.
func startAsService(ctx context.Context, t *testing.T, env *e2e.Env, req serviceStart) jobDoc {
	t.Helper()

	var started jobDoc

	env.APIStepWithHeadersAs(ctx, t, req.token.Secret, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: req.name, Method: http.MethodPost, Path: jobsRoute,
		Body: startBody(jobsPool, "plan"), Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			doc, err := checkStarted(body)
			started = doc

			return err
		},
	}, map[string]string{"Idempotency-Key": req.key})

	return started
}

// retryAsService sends the start of req again with the same key, records the
// step, and returns the job and the value of the Idempotent-Replayed header.
func retryAsService(ctx context.Context, t *testing.T, env *e2e.Env, req serviceStart) (jobDoc, string) {
	t.Helper()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, env.URL+jobsRoute,
		strings.NewReader(startBody(jobsPool, "plan")))
	if err != nil {
		t.Fatalf("build the retry: %v", err)
	}

	httpReq.Header.Set("Authorization", "token "+req.token.Secret)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotency-Key", req.key)
	httpReq.Header.Set("User-Agent", jobsAgent)

	resp, err := env.BrowserClient(t).Do(httpReq)
	if err != nil {
		t.Fatalf("retry the start: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close the retry answer: %v", closeErr)
		}
	}()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the retry answer: %v", err)
	}

	doc, docErr := checkStarted(string(raw))

	failure := ""
	if resp.StatusCode != http.StatusCreated {
		failure = fmt.Sprintf("answers %d, want %d: %s", resp.StatusCode, http.StatusCreated, raw)
	} else if docErr != nil {
		failure = docErr.Error()
	}

	env.RecordExternalStep(t, req.name, http.MethodPost+" "+jobsRoute,
		e2e.ExternalResult{Stdout: string(raw), Stderr: "", ExitCode: 0}, failure)

	return doc, resp.Header.Get("Idempotent-Replayed")
}
