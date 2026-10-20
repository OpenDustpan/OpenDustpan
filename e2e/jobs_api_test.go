//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

// TestJobsAPI reads a job through the API on a server of each store. No CLI
// reads a job and no route starts one yet, so the test writes the job to the
// store, as the start of a job will, and reads it through the route. It
// proves the answer for the person who may read the job, for a person who may
// not, for a job that does not exist and for a job of another namespace.
func TestJobsAPI(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			env := e2e.NewEnv(t.Context(), t, e2e.NoCLI(), store)

			jobsScenario(t, env)
		})
	}
}

// jobPath returns the job route of the job id in the namespace ns.
func jobPath(ns, id string) string {
	return "/api/dustpan/v1/namespaces/" + ns + "/jobs/" + id
}

func jobsScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	seed := env.CreateJob(ctx, t, "jobs-network")
	env.CreateNamespace(ctx, t, "elsewhere")

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent,
		Name:      "read the job",
		Method:    http.MethodGet,
		Path:      jobPath(e2e.Namespace, seed.JobID),
		Body:      "",
		Want:      http.StatusOK,
		Scheme:    "token",
		Check:     func(body string) error { return checkJobDoc(body, seed) },
	})

	notFound := func(body string) error {
		if !strings.Contains(body, `"message":"not found"`) {
			return fmt.Errorf("%w: body %s", errJobDoc, body)
		}

		return nil
	}

	for _, step := range []struct{ name, path string }{
		{"a job that does not exist", jobPath(e2e.Namespace, "job-doesnotexistdoesnot")},
		{"a job under the path of another namespace", jobPath("elsewhere", seed.JobID)},
		{"a malformed job ID", jobPath(e2e.Namespace, "not-a-job")},
	} {
		env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: jobsAgent, Name: step.name, Method: http.MethodGet, Path: step.path, Body: "",
			Want: http.StatusNotFound, Scheme: "token", Check: notFound,
		})
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "a write on the job route", Method: http.MethodDelete,
		Path: jobPath(e2e.Namespace, seed.JobID), Body: "", Want: http.StatusMethodNotAllowed, Scheme: "token",
		Check: nil,
	})

	memberReadsNothing(ctx, t, env, seed.JobID)
}

// checkJobDoc checks the document of the job that CreateJob stored.
func checkJobDoc(body string, seed e2e.JobSeed) error {
	var doc jobDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("decode job: %w", err)
	}

	if doc.ID != seed.JobID || doc.Namespace != e2e.Namespace {
		return fmt.Errorf("%w: job %s in %s", errJobDoc, doc.ID, doc.Namespace)
	}

	if doc.Unit.ID != seed.UnitID || doc.Unit.Tool != "OpenTofu" || doc.Unit.Name != "jobs-network" {
		return fmt.Errorf("%w: unit %+v", errJobDoc, doc.Unit)
	}

	if doc.Operation != "plan" || doc.Executor != "runner" || doc.State != "queued" {
		return fmt.Errorf("%w: operation %s, executor %s, state %s", errJobDoc, doc.Operation, doc.Executor,
			doc.State)
	}

	return checkJobRows(body, &doc)
}

// checkJobRows checks the phases and the steps of the job document, and that
// the document holds no argument of a step.
func checkJobRows(body string, doc *jobDoc) error {
	switch {
	case len(doc.Phases) != 1 || doc.Phases[0].Kind != "plan":
		return fmt.Errorf("%w: phases %+v", errJobDoc, doc.Phases)
	case len(doc.Steps) != 1 || doc.Steps[0].Name != "plan":
		return fmt.Errorf("%w: steps %+v", errJobDoc, doc.Steps)
	case strings.Contains(body, `"args"`):
		return fmt.Errorf("%w: the answer holds the arguments of a step", errJobDoc)
	default:
		return nil
	}
}

// memberReadsNothing proves a person with no grant gets the 404 of a job that
// does not exist. A binary server has no member token, so it skips.
func memberReadsNothing(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	token, ok := env.MemberToken()
	if !ok {
		return
	}

	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet, jobPath(e2e.Namespace, id), "")
	if err != nil {
		t.Fatalf("read the job as a member: %v", err)
	}

	if status != http.StatusNotFound || !strings.Contains(body, `"message":"not found"`) {
		t.Fatalf("member reads the job = %d %s, want the 404 of an unknown job", status, body)
	}
}
