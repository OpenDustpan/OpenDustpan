//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// jobsAgent is the User-Agent of the job API requests. The route coverage
// check counts requests that do not carry the harness agent.
const jobsAgent = "opendustpan-e2e-jobs"

var errJobDoc = errors.New("the job document is not the expected one")

// jobDoc is the part of the job document that the steps read.
type jobDoc struct {
	ID        string      `json:"id"`
	Namespace string      `json:"namespace"`
	Unit      jobDocUnit  `json:"unit"`
	Operation string      `json:"operation"`
	Executor  string      `json:"executor"`
	State     string      `json:"state"`
	Phases    []jobDocRow `json:"phases"`
	Steps     []jobDocRow `json:"steps"`
}

// jobDocUnit is the unit of a job document.
type jobDocUnit struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

// jobDocRow is a phase or a step of a job document. A phase has a kind and a
// step has a name.
type jobDocRow struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Names of the unit and the pool that the scenario uses.
const (
	jobsUnit = "jobs-network"
	jobsPool = "ci"
)

// TestJobsAPI starts a plan job through the API on a server of each store,
// and reads it back. The workspace comes from the TFE-compatible API and the
// runner pool from the runner management API: no step writes to the store.
// It proves the answer for the person who may start the job, a start with the
// same Idempotency-Key, the refusals of a start that is not valid, a person
// who may not start it, and a job that does not exist or belongs to another
// namespace.
func TestJobsAPI(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			// The runner management API needs the public URL, which the sign-in setup gives.
			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			jobsScenario(t, env)
		})
	}
}

// jobPath returns the job route of the job id in the namespace ns.
func jobPath(ns, id string) string {
	return "/api/dustpan/v1/namespaces/" + ns + "/jobs/" + id
}

// jobsRoute is the route that starts the jobs of the scenario namespace.
const jobsRoute = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/jobs"

// startBody returns the body of a start with the fields that a case changes.
func startBody(pool, operation string) string {
	return fmt.Sprintf(`{"unit":{"tool":"opentofu","name":%q},"operation":%q,"pool":%q,`+
		`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"e2e"}`,
		jobsUnit, operation, pool)
}

func jobsScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	env.CreateNamespace(ctx, t, "elsewhere")

	// The unit and the pool come from the public API of the server.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + jobsUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/runners/" + e2e.Namespace + "/pools", Body: `{"name":"` + jobsPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	started := startsAJob(ctx, t, env)
	readsTheJob(ctx, t, env, started)
	aManualLockQueuesAJob(ctx, t, env)
	refusesBadStarts(ctx, t, env)
	missingJobsAnswerNotFound(ctx, t, env, started)
	memberStartsNothing(ctx, t, env, started)
}

// startsAJob starts the plan job, starts it again with the same key, and
// returns the job.
func startsAJob(ctx context.Context, t *testing.T, env *e2e.Env) jobDoc {
	t.Helper()

	var first jobDoc

	headers := map[string]string{"Idempotency-Key": "jobs-e2e-key-1"}

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "start a plan job", Method: http.MethodPost, Path: jobsRoute,
		Body: startBody(jobsPool, "plan"), Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			doc, err := checkStarted(body)
			first = doc

			return err
		},
	}, headers)

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "start it again with the same key", Method: http.MethodPost,
		Path: jobsRoute, Body: startBody(jobsPool, "plan"), Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			var doc jobDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				return fmt.Errorf("decode job: %w", err)
			}

			if doc.ID != first.ID {
				return fmt.Errorf("%w: the second start made job %s, not %s", errJobDoc, doc.ID, first.ID)
			}

			return nil
		},
	}, headers)

	return first
}

// checkStarted checks the document of the job that the start made.
func checkStarted(body string) (jobDoc, error) {
	var doc jobDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return jobDoc{}, fmt.Errorf("decode job: %w", err)
	}

	if !strings.HasPrefix(doc.ID, "job-") || doc.Namespace != e2e.Namespace {
		return doc, fmt.Errorf("%w: job %s in %s", errJobDoc, doc.ID, doc.Namespace)
	}

	if doc.Unit.ID == "" || doc.Unit.Tool != "OpenTofu" || doc.Unit.Name != jobsUnit {
		return doc, fmt.Errorf("%w: unit %+v", errJobDoc, doc.Unit)
	}

	// With no runner online the phase waits dispatched: the claim comes later.
	if doc.Operation != "plan" || doc.Executor != "runner" || doc.State != "dispatched" {
		return doc, fmt.Errorf("%w: operation %s, executor %s, state %s", errJobDoc, doc.Operation, doc.Executor,
			doc.State)
	}

	err := checkJobRows(body, &doc)

	return doc, err
}

// readsTheJob reads the job that the start made.
func readsTheJob(ctx context.Context, t *testing.T, env *e2e.Env, started jobDoc) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "read the job", Method: http.MethodGet, Path: jobPath(e2e.Namespace, started.ID),
		Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			doc, err := checkStarted(body)
			if err == nil && doc.ID != started.ID {
				err = fmt.Errorf("%w: job %s, want %s", errJobDoc, doc.ID, started.ID)
			}

			return err
		},
	})
}

// aManualLockQueuesAJob proves a manual lock on the workspace does not refuse
// the start: the job waits queued with the wait reason locked, and no runner
// can claim it until a person removes the lock.
func aManualLockQueuesAJob(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	lock := "/api/inventory/unit-lock?unit=" + url.QueryEscape("opentofu:"+e2e.Namespace+"/"+jobsUnit)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "lock the workspace", Method: http.MethodPut, Path: lock,
		Body: `{"reason":"change window closed"}`, Want: http.StatusOK, Scheme: "", Check: nil,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "start a plan job on a locked workspace", Method: http.MethodPost,
		Path: jobsRoute, Body: startBody(jobsPool, "plan"), Want: http.StatusCreated, Scheme: "token",
		Check: func(body string) error {
			if !strings.Contains(body, `"state":"queued"`) || !strings.Contains(body, `"wait":"locked"`) {
				return fmt.Errorf("%w: body %s, want a queued job that waits for the lock", errJobDoc, body)
			}

			return nil
		},
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "unlock the workspace", Method: http.MethodDelete, Path: lock, Body: "",
		Want: http.StatusNoContent, Scheme: "", Check: nil,
	})
}

// refusesBadStarts proves a start that is not valid makes no job, with the
// stable code of the refusal.
func refusesBadStarts(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	withCode := func(code string) func(string) error {
		return func(body string) error {
			if !strings.Contains(body, `"errorCode":"`+code+`"`) {
				return fmt.Errorf("%w: body %s, want the code %s", errJobDoc, body, code)
			}

			return nil
		}
	}

	for _, step := range []struct {
		name, body string
		want       int
		code       string
	}{
		{"a start with no pool", startBody("", "plan"), http.StatusBadRequest, "job.invalid"},
		{"a start in a pool that does not exist", startBody("nowhere", "plan"), http.StatusBadRequest, "job.invalid"},
		{"a start of an apply", startBody(jobsPool, "apply"), http.StatusUnprocessableEntity, "job.unsupported"},
		{
			"a start with an operation that does not exist", startBody(jobsPool, "reboot"), http.StatusBadRequest,
			"job.invalid",
		},
		{"a start that picks its source kind", strings.Replace(startBody(jobsPool, "plan"), `"reason"`,
			`"source":"schedule","reason"`, 1), http.StatusBadRequest, "request.body-invalid"},
		{"a start that sends its own steps", strings.Replace(startBody(jobsPool, "plan"), `"reason"`,
			`"steps":[{"args":["sh"]}],"reason"`, 1), http.StatusBadRequest, "request.body-invalid"},
		{"a start with a directory above the checkout", strings.Replace(startBody(jobsPool, "plan"), `"net"`,
			`"../net"`, 1), http.StatusBadRequest, "job.invalid"},
	} {
		env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: jobsAgent, Name: step.name, Method: http.MethodPost, Path: jobsRoute,
			Body: step.body, Want: step.want, Scheme: "token", Check: withCode(step.code),
		})
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "a start on a unit that does not exist", Method: http.MethodPost,
		Path: jobsRoute, Body: strings.Replace(startBody(jobsPool, "plan"), jobsUnit, "no-such", 1),
		Want: http.StatusForbidden, Scheme: "token", Check: nil,
	})

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "a start with a bad key", Method: http.MethodPost, Path: jobsRoute,
		Body: startBody(jobsPool, "plan"), Want: http.StatusBadRequest, Scheme: "token",
		Check: withCode("request.idempotency-key-invalid"),
	}, map[string]string{"Idempotency-Key": strings.Repeat("k", 256)})
}

// missingJobsAnswerNotFound proves the 404 of a job that does not exist, of a
// job under the path of another namespace and of a malformed ID, and that the
// job route takes no write.
func missingJobsAnswerNotFound(ctx context.Context, t *testing.T, env *e2e.Env, started jobDoc) {
	t.Helper()

	notFound := func(body string) error {
		if !strings.Contains(body, `"message":"not found"`) {
			return fmt.Errorf("%w: body %s", errJobDoc, body)
		}

		return nil
	}

	for _, step := range []struct{ name, path string }{
		{"a job that does not exist", jobPath(e2e.Namespace, "job-doesnotexistdoesnot")},
		{"a job under the path of another namespace", jobPath("elsewhere", started.ID)},
		{"a malformed job ID", jobPath(e2e.Namespace, "not-a-job")},
	} {
		env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: jobsAgent, Name: step.name, Method: http.MethodGet, Path: step.path, Body: "",
			Want: http.StatusNotFound, Scheme: "token", Check: notFound,
		})
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "a write on the job route", Method: http.MethodDelete,
		Path: jobPath(e2e.Namespace, started.ID), Body: "", Want: http.StatusMethodNotAllowed, Scheme: "token",
		Check: nil,
	})
}

// checkJobRows checks the phases and the steps of the job document, and that
// the document holds no argument of a step.
func checkJobRows(body string, doc *jobDoc) error {
	switch {
	case len(doc.Phases) != 1 || doc.Phases[0].Kind != "plan":
		return fmt.Errorf("%w: phases %+v", errJobDoc, doc.Phases)
	case len(doc.Steps) != 2 || doc.Steps[0].Name != "init" || doc.Steps[1].Name != "plan":
		return fmt.Errorf("%w: steps %+v", errJobDoc, doc.Steps)
	case strings.Contains(body, `"args"`):
		return fmt.Errorf("%w: the answer holds the arguments of a step", errJobDoc)
	default:
		return nil
	}
}

// memberStartsNothing proves a person with no grant can neither start a job
// nor read one: the namespace and the job answer 404. A binary server has no
// member token, so it skips.
func memberStartsNothing(ctx context.Context, t *testing.T, env *e2e.Env, started jobDoc) {
	t.Helper()

	token, ok := env.MemberToken()
	if !ok {
		return
	}

	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet, jobPath(e2e.Namespace, started.ID), "")
	if err != nil {
		t.Fatalf("read the job as a member: %v", err)
	}

	if status != http.StatusNotFound || !strings.Contains(body, `"message":"not found"`) {
		t.Fatalf("member reads the job = %d %s, want the 404 of an unknown job", status, body)
	}

	status, body, err = env.ServerAPIStatusAs(ctx, token, http.MethodPost, jobsRoute,
		startBody(jobsPool, "plan"))
	if err != nil {
		t.Fatalf("start a job as a member: %v", err)
	}

	if status != http.StatusNotFound {
		t.Fatalf("member starts a job = %d %s, want 404", status, body)
	}
}
