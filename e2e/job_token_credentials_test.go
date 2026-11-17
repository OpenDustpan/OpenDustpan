//go:build e2e

package e2e_test

import (
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

// jobTokenCredentials runs the credentials path against env.
func jobTokenCredentials(t *testing.T, env *e2e.Env) {
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

	step := run.launcher.Assignment.Steps[0].Name
	issued := run.launcher.JobToken(ctx, t, step)

	if !strings.HasPrefix(issued.Token, jobtoken.SecretPrefix) {
		t.Fatalf("the job token has no %q prefix", jobtoken.SecretPrefix)
	}

	if issued.ExpiresAt.IsZero() {
		t.Fatal("the job token has no expiry")
	}

	// A step that the phase does not have, and a name that is not a step
	// name, get no token.
	for _, bad := range []string{"no-such-step", "Bad Step", ""} {
		status, body := run.launcher.Credentials(ctx, t, bad)
		if status != http.StatusBadRequest {
			t.Errorf("credentials of step %q = %d %s, want 400", bad, status, body)
		}

		if strings.Contains(string(body), jobtoken.SecretPrefix) {
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
