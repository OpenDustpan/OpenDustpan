//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// jobActionsAgent is the User-Agent of the job action requests.
const jobActionsAgent = "opendustpan-e2e-job-actions"

// TestJobActions starts a plan job through the start route and cancels it with
// the job cancel route, on a server of each store. It also sends the requests
// that the cancel, confirm and redact routes refuse before they write anything.
// The approve route has no step here: a job that waits for approval cannot be
// reached through the start route, which starts plan jobs only.
func TestJobActions(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			jobActionsScenario(t.Context(), t, env)
		})
	}
}

func jobActionsScenario(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobActionsAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + jobsUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobActionsAgent,
		Name:      "create the runner pool",
		Method:    http.MethodPost,
		Path:      "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/runner-pools",
		Body:      `{"name":"` + jobsPool + `","purpose":"jobs"}`,
		Want:      http.StatusCreated,
		Scheme:    "token",
		Check:     nil,
	})

	var started jobDoc

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobActionsAgent, Name: "start a plan job", Method: http.MethodPost, Path: jobsRoute,
		Body: startBody(jobsPool, "plan"), Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			doc, err := checkStarted(body)
			started = doc

			return err
		},
	})

	// Requests that the routes refuse before any write.
	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: jobActionsAgent, Name: "a cancel with an Idempotency-Key", Method: http.MethodPost,
		Path: jobPath(e2e.Namespace, started.ID) + "/cancel", Body: `{}`, Want: http.StatusBadRequest,
		Scheme: "token", Check: codeCheck("request.idempotency-key-invalid"),
	}, map[string]string{"Idempotency-Key": "cancel-e2e-key"})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobActionsAgent, Name: "a cancel with a field that the route does not know", Method: http.MethodPost,
		Path: jobPath(e2e.Namespace, started.ID) + "/cancel", Body: `{"agent":"dusty"}`,
		Want: http.StatusBadRequest, Scheme: "token", Check: codeCheck("request.body-invalid"),
	})

	for _, route := range []string{"/cancel", "/confirmations", "/log-redactions"} {
		env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: jobActionsAgent, Name: "a request on a job that does not exist: " + route,
			Method: http.MethodPost, Path: jobPath(e2e.Namespace, "job-doesnotexistdoesnot") + route, Body: `{}`,
			Want: http.StatusNotFound, Scheme: "token", Check: nil,
		})
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobActionsAgent, Name: "cancel the job", Method: http.MethodPost,
		Path: jobPath(e2e.Namespace, started.ID) + "/cancel", Body: `{"reason":"wrong branch"}`,
		Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			if !strings.Contains(body, `"id":"`+started.ID+`"`) {
				return fmt.Errorf("%w: body %s, want the job %s", errJobDoc, body, started.ID)
			}

			if strings.Contains(body, `"state":"dispatched"`) {
				return fmt.Errorf("%w: the job is still dispatched after the cancel: %s", errJobDoc, body)
			}

			return nil
		},
	})
}
