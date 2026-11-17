//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestJobTokenCredentials drives the credentials path of a launcher session
// on the server of each store: a fake runner claims a phase, a fake launcher
// opens the session with the handoff token and asks for the job token of a
// step with the session key proof.
func TestJobTokenCredentials(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			jobTokenCredentials(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store))
		})
	}
}

// jobTokenOfAJob makes the starter of a job, claims a phase, opens the session
// and asks for the job token of the first step.
func jobTokenOfAJob(t *testing.T, env *e2e.Env) (*launched, protocol.CredentialsResponse) {
	t.Helper()

	ctx := t.Context()

	// The path gives a token only for a job whose starter has a person record
	// and a grant, so the person of the access token gets both first.
	status, answer, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/namespaces",
		`{"name":"`+e2e.Namespace+`"}`)
	if err != nil || (status != http.StatusCreated && status != http.StatusConflict) {
		t.Fatalf("make the namespace = %d, %v: %s", status, err, answer)
	}

	env.SeedStarter(ctx, t, access.RoleNamespaceAdmin)

	run := openLaunchedSession(t, env)
	env.LogJobStepsOnFailure(t, e2e.Namespace, run.job, run.handoff)

	return run, run.launcher.JobToken(ctx, t, run.launcher.Assignment.Steps[0].Name)
}

// jobTokenCredentials runs the credentials path against env.
func jobTokenCredentials(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	run, issued := jobTokenOfAJob(t, env)

	if !strings.HasPrefix(issued.Token, jobtoken.SecretPrefix) {
		t.Fatalf("the job token has no %q prefix", jobtoken.SecretPrefix)
	}

	if issued.ExpiresAt.IsZero() {
		t.Fatal("the job token has no expiry")
	}

	jobTokenStateRoutes(ctx, t, env, issued.Token)

	// A step that the phase does not have, and a name that is not a step
	// name, get no token.
	for _, bad := range []string{"no-such-step", "Bad Step", ""} {
		code, refusal := run.launcher.Credentials(ctx, t, bad)
		if code != http.StatusBadRequest {
			t.Errorf("credentials of step %q = %d %s, want 400", bad, code, refusal)
		}

		if strings.Contains(string(refusal), jobtoken.SecretPrefix) {
			t.Errorf("the refusal of step %q holds token text", bad)
		}
	}

	status, body := run.launcher.Post(ctx, t, protocol.PathSessionCredentials, nil)
	if status != http.StatusBadRequest {
		t.Errorf("credentials with no body = %d %s, want 400", status, body)
	}

	if env.InProcess() {
		return
	}

	if found := env.ServerLogHolds(t, issued.Token, run.handoff); len(found) > 0 {
		t.Fatal("the server log holds the job token or the handoff token")
	}
}

// otherUnit is a second workspace of the namespace. The job token of the unit
// of the job must not reach it.
const otherUnit = "other-unit"

// jobTokenStateRoutes calls the state route of the unit of the job and of
// another unit with the job token. The token reaches its own unit and no
// other, and the refusal names no token text.
func jobTokenStateRoutes(ctx context.Context, t *testing.T, env *e2e.Env, token string) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the other workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + otherUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})

	own := "/tofu/state/" + e2e.Namespace + "/" + jobsUnit

	status, body, err := env.TFEStatus(ctx, token, http.MethodGet, own, "")
	if err != nil {
		t.Fatalf("read the state of the own unit: %v", err)
	}

	// A new workspace holds no state yet: the route answers 404, and not 401 or 403.
	if status != http.StatusNotFound {
		t.Errorf("state of the own unit with its job token = %d %s, want 404 (allowed, no state)", status, body)
	}

	status, body, err = env.TFEStatus(ctx, token, http.MethodGet, "/tofu/state/"+e2e.Namespace+"/"+otherUnit, "")
	if err != nil {
		t.Fatalf("read the state of another unit: %v", err)
	}

	if status != http.StatusForbidden {
		t.Errorf("state of another unit with the job token = %d %s, want 403", status, body)
	}

	if strings.Contains(body, token) {
		t.Error("the refusal holds the job token")
	}
}
