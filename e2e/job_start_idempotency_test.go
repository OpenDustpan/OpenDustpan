//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestJobStartIdempotencyRetention proves that a service principal that starts
// a job with an Idempotency-Key gets the stored job when it sends the same key
// again after a restart of the real server binary. The retention after 24
// hours has its test in the job store, which drives the clock of the store.
func TestJobStartIdempotencyRetention(t *testing.T) {
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

// jobStartRetryAfterRestart starts one plan job as a service principal, stops
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

	env.StopServer(ctx, t)
	env.StartServer(ctx, t)

	replay := startAsService(ctx, t, env, serviceStart{
		token: token, key: key, name: "retry the start after the restart",
	})

	if replay.ID != first.ID {
		t.Fatalf("the retry after the restart made job %s, not %s", replay.ID, first.ID)
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
