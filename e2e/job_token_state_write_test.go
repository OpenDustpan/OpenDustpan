//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/access"
)

// TestJobTokenStateWrite proves, on the server of each store, the state.write
// rules of a job token of a plan phase. The server starts plan jobs only, on a
// runner, so each case uses the job token of a plan phase. On a workspace, the
// token takes the lock of its plan and cannot force-unlock it. On a stack, the
// token cannot import a checkpoint or restore a version. Each refusal answers
// 403 with the code gate.denied.
func TestJobTokenStateWrite(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			// Each tool has its own server: both scenarios make the same runner pool.
			t.Run("opentofu", func(t *testing.T) {
				t.Parallel()

				jobTokenWorkspaceWrites(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store))
			})

			t.Run("pulumi", func(t *testing.T) {
				t.Parallel()

				jobTokenStackWrites(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store))
			})
		})
	}
}

// jobTokenWorkspaceWrites starts a plan job on a workspace, gets the job token
// of its first step, takes the lock of the workspace with the token and then
// sends a force-unlock with the same token.
func jobTokenWorkspaceWrites(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	_, issued := jobTokenOfAJob(t, env)
	wsID := env.WorkspaceIDByName(ctx, t, jobsUnit)

	stateWriteAnswers(ctx, t, jobTokenCall{
		name: "the lock of the plan", token: issued.Token, method: http.MethodPost,
		path: "/api/v2/workspaces/" + wsID + "/actions/lock", body: `{"data":{}}`,
		want: http.StatusOK, code: "", tfe: true,
	}, env)

	stateWriteAnswers(ctx, t, jobTokenCall{
		name: "a force-unlock", token: issued.Token, method: http.MethodPost,
		path: forceUnlockPath(wsID), body: "",
		want: http.StatusForbidden, code: "gate.denied", tfe: true,
	}, env)
}

// jobTokenStackWrites starts a plan job on a stack, gets the job token of its
// first step and sends an import and a restore of the stack with the token.
func jobTokenStackWrites(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	status, answer, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/namespaces",
		`{"name":"`+e2e.Namespace+`"}`)
	if err != nil || (status != http.StatusCreated && status != http.StatusConflict) {
		t.Fatalf("make the namespace = %d, %v: %s", status, err, answer)
	}

	env.SeedStarter(ctx, t, access.RoleNamespaceAdmin)
	createStack(ctx, t, env, e2e.Namespace+"/"+exportProject+"/"+exportOwn)

	started := startExportJob(ctx, t, env)
	fake := env.JoinFakeRunner(ctx, t, e2e.Namespace, started.pool, "builder-1")

	work := fake.Poll(ctx, t, pollWait)
	if work == nil || work.Job != started.job {
		t.Fatalf("poll = %+v, want the phase of the job %s", work, started.job)
	}

	handoff := fake.Handoff(ctx, t, work.Operation)
	launcher := env.OpenSession(ctx, t, handoff)
	env.LogJobStepsOnFailure(t, e2e.Namespace, started.job, handoff)

	issued := launcher.JobToken(ctx, t, launcher.Assignment.Steps[0].Name)
	prefix := "/api/stacks/" + e2e.Namespace + "/" + exportProject + "/" + exportOwn

	// The guard runs before the restore reads the version, so the stack needs
	// no version 1 for the refusal.
	for _, c := range []jobTokenCall{
		{
			name: "an import", token: issued.Token, method: http.MethodPost, path: prefix + "/import",
			body: `{"version":3,"deployment":{"resources":[]}}`,
			want: http.StatusForbidden, code: "gate.denied", tfe: false,
		},
		{
			name: "a restore", token: issued.Token, method: http.MethodPost, path: prefix + "/restore/1",
			body: "", want: http.StatusForbidden, code: "gate.denied", tfe: false,
		},
	} {
		stateWriteAnswers(ctx, t, c, env)
	}
}

// jobTokenCall is one state write with a job token and the answer that the
// step wants.
type jobTokenCall struct {
	name, token, method, path, body string
	want                            int
	// code is the error code that the body must hold. Empty checks no code.
	code string
	// tfe sends the call to the TFE-compatible API, and false to the Pulumi
	// Cloud-compatible API.
	tfe bool
}

// stateWriteAnswers sends c with its job token and checks the status, the
// error code and that the body holds no token text.
func stateWriteAnswers(ctx context.Context, t *testing.T, c jobTokenCall, env *e2e.Env) {
	t.Helper()

	var (
		status int
		body   string
		err    error
	)

	if c.tfe {
		status, body, err = env.TFEStatus(ctx, c.token, c.method, c.path, c.body)
	} else {
		status, body, err = env.APIStatusAs(ctx, c.token, c.method, c.path, c.body)
	}

	if err != nil {
		t.Fatalf("%s with the job token: %v", c.name, err)
	}

	if status != c.want {
		t.Fatalf("%s with the job token = %d %s, want %d", c.name, status, body, c.want)
	}

	if c.code != "" && !strings.Contains(body, c.code) {
		t.Fatalf("%s with the job token: %v", c.name,
			fmt.Errorf("%w: body %s, want the code %s", errUnexpected, body, c.code))
	}

	if strings.Contains(body, c.token) {
		t.Fatalf("the answer of %s holds the job token", c.name)
	}
}
