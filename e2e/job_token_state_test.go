//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestJobTokenState proves with the real OpenTofu CLI that the job token of a
// job reaches the state of the unit of the job and no other unit. The token
// comes from the credentials path of a launcher session, and the CLI sends it
// as the password of the http backend.
func TestJobTokenState(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the OpenTofu suite needs Linux, not %s", runtime.GOOS)
	}

	tool := e2e.OpenTofu()
	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, err := fetcher.Install(installCtx, tool, label)
			if err != nil {
				t.Fatalf("install %s %s: %v", tool.Name, label, err)
			}

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()

					jobTokenState(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, cli, db))
				})
			}
		})
	}
}

// jobTokenState runs the CLI against the unit of the job and against another
// unit, both with the job token.
func jobTokenState(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	_, issued := jobTokenOfAJob(t, env)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the other workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + otherUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})

	backendEnv := []string{"TF_HTTP_USERNAME=job", "TF_HTTP_PASSWORD=" + issued.Token}

	// The unit of the job: init configures the backend and reads its state.
	writeTofuHTTPConfig(t, env.ProjectDir(), env.URL+"/tofu/state/"+e2e.Namespace+"/"+jobsUnit)

	env.Step(ctx, t, e2e.StepSpec{
		Check: checkTofuHTTPInit(), Name: "init on the unit of the job", Args: []string{"init", "-input=false", "-no-color"},
		Env: backendEnv, Stdin: "", Fail: false, Redact: false,
	})

	env.Step(ctx, t, e2e.StepSpec{
		Check: nil, Name: "state pull on the unit of the job", Args: []string{"state", "pull"},
		Env: backendEnv, Stdin: "", Fail: false, Redact: false,
	})

	// Another unit: the same token gets 403 and the CLI fails.
	writeTofuHTTPConfig(t, env.ProjectDir(), env.URL+"/tofu/state/"+e2e.Namespace+"/"+otherUnit)

	env.Step(ctx, t, e2e.StepSpec{
		Check: func(r e2e.Result) error {
			if strings.Contains(r.Stdout+r.Stderr, issued.Token) {
				return fmt.Errorf("%w: the output holds the job token", errUnexpected)
			}

			return nil
		},
		Name: "init on another unit", Args: []string{"init", "-input=false", "-reconfigure", "-no-color"},
		Env: backendEnv, Stdin: "", Fail: true, Redact: false,
	})
}
