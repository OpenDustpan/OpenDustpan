//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/access"
)

// Names of the Pulumi stacks of the export test: the unit of the job and
// another unit of the same project.
const (
	exportProject = "jobproj"
	exportOwn     = "own"
	exportOther   = "other"
)

// TestJobTokenStackExport proves that the job token of a job on a Pulumi stack
// reads the export of that stack and gets 403 on another stack, with the
// Pulumi Cloud-compatible API.
func TestJobTokenStackExport(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			jobTokenStackExport(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store))
		})
	}
}

// jobTokenStackExport starts a job on a Pulumi stack, gets the job token of
// its first step and calls the export route on two stacks.
func jobTokenStackExport(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	status, answer, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/namespaces",
		`{"name":"`+e2e.Namespace+`"}`)
	if err != nil || (status != http.StatusCreated && status != http.StatusConflict) {
		t.Fatalf("make the namespace = %d, %v: %s", status, err, answer)
	}

	env.SeedStarter(ctx, t, access.RoleNamespaceAdmin)

	for _, name := range []string{exportOwn, exportOther} {
		createStack(ctx, t, env, e2e.Namespace+"/"+exportProject+"/"+name)
	}

	started := startExportJob(ctx, t, env)
	jobID, poolID := started.job, started.pool

	fake := env.JoinFakeRunner(ctx, t, e2e.Namespace, poolID, "builder-1")

	work := fake.Poll(ctx, t, pollWait)
	if work == nil || work.Job != jobID {
		t.Fatalf("poll = %+v, want the phase of the job %s", work, jobID)
	}

	handoff := fake.Handoff(ctx, t, work.Operation)
	launcher := env.OpenSession(ctx, t, handoff)
	env.LogJobStepsOnFailure(t, e2e.Namespace, jobID, handoff)

	issued := launcher.JobToken(ctx, t, launcher.Assignment.Steps[0].Name)
	prefix := "/api/stacks/" + e2e.Namespace + "/" + exportProject + "/"

	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{"own stack", prefix + exportOwn + "/export", http.StatusOK},
		{"another stack", prefix + exportOther + "/export", http.StatusForbidden},
	} {
		got, body, err := env.APIStatusAs(ctx, issued.Token, http.MethodGet, tc.path, "")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if got != tc.want {
			t.Errorf("export of the %s with the job token = %d %s, want %d", tc.name, got, body, tc.want)
		}
	}
}

// exportJob holds the IDs of the job of the export test and of its runner pool.
type exportJob struct {
	job, pool string
}

// startExportJob makes a runner pool and starts a plan job on the first stack.
// It returns their IDs.
func startExportJob(ctx context.Context, t *testing.T, env *e2e.Env) exportJob {
	t.Helper()

	var out exportJob

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/runner-pools",
		Body: `{"name":"` + jobsPool + `","purpose":"jobs"}`, Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			var pool struct {
				ID string `json:"id"`
			}

			if err := json.Unmarshal([]byte(body), &pool); err != nil || pool.ID == "" {
				return fmt.Errorf("%w: pool %s: %w", errLauncherFlow, body, err)
			}

			out.pool = pool.ID

			return nil
		},
	})

	started := fmt.Sprintf(`{"unit":{"tool":"pulumi","name":"%s/%s"},"operation":"plan","pool":%q,`+
		`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"e2e"}`,
		exportProject, exportOwn, jobsPool)

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "start a plan job on the stack", Method: http.MethodPost, Path: jobsRoute,
		Body: started, Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			var doc jobDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.ID == "" {
				return fmt.Errorf("%w: job %s: %w", errLauncherFlow, body, err)
			}

			out.job = doc.ID

			return nil
		},
	}, map[string]string{"Idempotency-Key": "job-token-export-key"})

	return out
}
