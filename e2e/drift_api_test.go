//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// driftAgent is the User-Agent of the drift API requests. The route coverage
// check counts requests that do not carry the harness agent.
const driftAgent = "opendustpan-e2e-drift"

var errDriftDoc = errors.New("the drift document is not the expected one")

// Names of the unit and the pool that the scenario uses.
const (
	driftUnit       = "drift-network"
	driftLockedUnit = "drift-locked"
	driftPool       = "ci"
)

// driftBody is a start with a pool and a code location, as the jobs scenario
// starts a plan.
const driftBody = `{"pool":"` + driftPool + `","location":{"repository":"acme/infra","ref":"main","directory":"net"},` +
	`"reason":"e2e"}`

// driftUnitDoc is the unit of a drift document.
type driftUnitDoc struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

// driftCheckDoc is the answer to a start.
type driftCheckDoc struct {
	JobID string       `json:"jobId"` //nolint:tagliatelle // The description of the API demands camelCase: jobId.
	Unit  driftUnitDoc `json:"unit"`
}

// driftResourceDoc is one resource of a drift read.
type driftResourceDoc struct {
	Address string `json:"address"`
}

// driftReadDoc is the document of a drift read.
type driftReadDoc struct {
	Status    string             `json:"status"`
	CheckedAt *time.Time         `json:"checkedAt"`
	JobID     string             `json:"jobId"` //nolint:tagliatelle // The description of the API demands camelCase: jobId.
	Resources []driftResourceDoc `json:"resources"`
}

// workspaceDoc is the part of a workspace document of the TFE-compatible API
// that the scenario reads: the ID of the workspace, which is its unit ID.
type workspaceDoc struct {
	Data workspaceData `json:"data"`
}

type workspaceData struct {
	ID string `json:"id"`
}

// workspaceID returns a check that reads the ID of a workspace document into
// id.
func workspaceID(id *string) func(string) error {
	return func(body string) error {
		var doc workspaceDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Data.ID == "" {
			return fmt.Errorf("%w: workspace document %s: %w", errDriftDoc, body, err)
		}

		*id = doc.Data.ID

		return nil
	}
}

// TestDriftAPI starts a drift check through the API on a server of each store,
// and reads the drift result of the unit. The workspace comes from the
// TFE-compatible API and the runner pool from the runner management API: no
// step writes to the store. It proves that a unit with no result reads unknown
// and never in-sync, that a start makes one refresh job of the purpose drift
// check with the plan phase alone, that a second start in the same minute gives
// the same job, that a manual lock queues the job, the refusals of a start that
// is not valid, and a person who may not read the unit. No runner records a
// result yet, so the unit stays unknown.
func TestDriftAPI(t *testing.T) {
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

			driftScenario(t, env)
		})
	}
}

// driftRoutes holds the two routes of the unit of the scenario.
type driftRoutes struct {
	read   string
	checks string
}

func driftScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	var unitID string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + driftUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: workspaceID(&unitID),
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/runners/" + e2e.Namespace + "/pools", Body: `{"name":"` + driftPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	base := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units/" + unitID
	routes := driftRoutes{read: base + "/drift", checks: base + "/drift-checks"}

	readsUnknownBeforeAnyCheck(ctx, t, env, &routes)
	contextReadsUnknown(ctx, t, env, base)

	started := startsACheck(ctx, t, env, &routes)

	readsTheJobOfTheCheck(ctx, t, env, started)
	readsUnknownAfterTheStart(ctx, t, env, &routes)
	aManualLockQueuesACheck(ctx, t, env)
	refusesBadChecks(ctx, t, env, &routes)
	unknownUnitsAnswerNotFound(ctx, t, env)
	memberReadsNothing(ctx, t, env, &routes)
	contextReadsDrift(ctx, t, env, &driftContextCase{unitID: unitID, jobID: started, base: base})
}

// driftContextCase names the unit and the job of the context steps.
type driftContextCase struct {
	unitID string
	jobID  string
	base   string
}

// driftContextData is the data of the drift section of a context document.
type driftContextData struct {
	CheckedAt *time.Time `json:"checkedAt"`
	Status    string     `json:"status"`
	JobID     string     `json:"jobID"`
	Addresses []string   `json:"addresses"`
	Truncated bool       `json:"truncated"`
}

// driftContextSection is the drift section of a context document.
type driftContextSection struct {
	Data   *driftContextData `json:"data"`
	Status string            `json:"status"`
}

// driftContextDoc is the part of the context document that the drift steps
// read.
type driftContextDoc struct {
	Sections map[string]driftContextSection `json:"sections"`
}

// checkContextDrift returns a check of the drift section of a context document:
// the status of the section, the drift status, and the addresses.
func checkContextDrift(section, status, jobID string, addresses []string) func(string) error {
	return func(body string) error {
		var doc driftContextDoc
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			return fmt.Errorf("decode context document: %w", err)
		}

		d := doc.Sections["drift"].Data
		if doc.Sections["drift"].Status != section || d == nil || d.Status != status || d.JobID != jobID ||
			(jobID != "") != (d.CheckedAt != nil) || !slices.Equal(d.Addresses, addresses) || d.Truncated ||
			!strings.Contains(body, `"addresses":[`) {
			return fmt.Errorf("%w: %s, want the section %s with the drift status %s", errDriftDoc, body, section, status)
		}

		return nil
	}
}

// contextReadsUnknown proves that the drift section of a unit that no check
// finished answers the status unknown with no time, no job and no address.
func contextReadsUnknown(ctx context.Context, t *testing.T, env *e2e.Env, base string) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "read the drift section of a unit with no check", Method: http.MethodGet,
		Path: base + "/context?include=drift", Body: "", Want: http.StatusOK, Scheme: "token",
		Check: checkContextDrift("ok", "unknown", "", []string{}),
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "the default sections hold no drift section", Method: http.MethodGet,
		Path: base + "/context", Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			if strings.Contains(body, `"drift"`) {
				return fmt.Errorf("%w: %s, want no drift section without include", errDriftDoc, body)
			}

			return nil
		},
	})
}

// contextReadsDrift records a drift result through the store, because no runner
// reports drift yet, and proves that the drift section of the context document
// shows it: the status, the time, the job and the addresses. A member who
// cannot read the unit gets the 404 of an unknown unit.
func contextReadsDrift(ctx context.Context, t *testing.T, env *e2e.Env, c *driftContextCase) {
	t.Helper()

	addresses := []string{"aws_s3_bucket.logs", "aws_instance.web"}
	env.RecordDrift(ctx, t, c.unitID, c.jobID, addresses...)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "read the drift section of a drifted unit", Method: http.MethodGet,
		Path: c.base + "/context?include=drift,health", Body: "", Want: http.StatusOK, Scheme: "token",
		Check: checkContextDrift("ok", "drifted", c.jobID, addresses),
	})

	token, ok := env.MemberToken()
	if !ok {
		return
	}

	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet, c.base+"/context?include=drift", "")
	if err != nil {
		t.Fatalf("read the drift section as a member: %v", err)
	}

	if status != http.StatusNotFound || strings.Contains(body, "aws_s3_bucket") {
		t.Fatalf("drift section as a member = %d %s, want the 404 of an unknown unit", status, body)
	}
}

// checkUnknown checks a drift read of a unit with no result: the status
// unknown, no time, no job and no address.
func checkUnknown(body string) error {
	var doc driftReadDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("decode drift read: %w", err)
	}

	if doc.Status != "unknown" || doc.CheckedAt != nil || doc.JobID != "" || len(doc.Resources) != 0 ||
		!strings.Contains(body, `"resources":[]`) {
		return fmt.Errorf("%w: %s, want the status unknown with no result", errDriftDoc, body)
	}

	return nil
}

// readsUnknownBeforeAnyCheck proves that a unit that no check ran on has the
// status unknown, never in-sync.
func readsUnknownBeforeAnyCheck(ctx context.Context, t *testing.T, env *e2e.Env, r *driftRoutes) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "read the drift of a unit with no check", Method: http.MethodGet, Path: r.read,
		Body: "", Want: http.StatusOK, Scheme: "token", Check: checkUnknown,
	})
}

// startsACheck starts a drift check and starts it again in the same minute. The
// second start must return the same job. A start that straddles a minute
// boundary makes a second job, so the step tries once more.
func startsACheck(ctx context.Context, t *testing.T, env *e2e.Env, r *driftRoutes) string {
	t.Helper()

	var first string

	for attempt := range 3 {
		var ids [2]string

		for i := range ids {
			env.APIStepA(ctx, t, e2e.APIStepSpec{
				UserAgent: driftAgent, Name: fmt.Sprintf("start a drift check (%d.%d)", attempt, i),
				Method: http.MethodPost, Path: r.checks, Body: driftBody, Want: http.StatusAccepted, Scheme: "token",
				Check: func(body string) error {
					var doc driftCheckDoc
					if err := json.Unmarshal([]byte(body), &doc); err != nil {
						return fmt.Errorf("decode drift check: %w", err)
					}

					if !strings.HasPrefix(doc.JobID, "job-") || doc.Unit.Tool != "OpenTofu" ||
						doc.Unit.Name != driftUnit {
						return fmt.Errorf("%w: %s, want a job of the unit %s", errDriftDoc, body, driftUnit)
					}

					ids[i] = doc.JobID

					return nil
				},
			})
		}

		first = ids[0]

		if ids[0] == ids[1] {
			return first
		}
	}

	t.Fatal("two starts in one minute gave two jobs in three tries, want one job")

	return first
}

// jobDriftDoc is the part of the job document that the drift steps read.
type jobDriftDoc struct {
	Operation string      `json:"operation"`
	Purpose   string      `json:"purpose"`
	Executor  string      `json:"executor"`
	State     string      `json:"state"`
	Source    string      `json:"source"`
	Phases    []jobDocRow `json:"phases"`
	Steps     []jobDocRow `json:"steps"`
}

// checkDriftJob checks the document of a job that a drift check started: a
// dispatched refresh with the purpose drift-check, the plan phase alone, and the
// steps init and plan, with no argument in the answer.
func checkDriftJob(body string) error {
	var doc jobDriftDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("decode job: %w", err)
	}

	if doc.Operation != "refresh" || doc.Purpose != "drift-check" || doc.Executor != "runner" ||
		doc.Source != "api" || doc.State != "dispatched" {
		return fmt.Errorf("%w: %s, want a dispatched refresh with the purpose drift-check", errDriftDoc, body)
	}

	if strings.Contains(body, "refresh-only") || strings.Contains(body, "lock=false") {
		return fmt.Errorf("%w: %s shows an argument of a step", errDriftDoc, body)
	}

	return checkDriftRows(body, &doc)
}

// checkDriftRows checks the phases and the steps of a drift check job.
func checkDriftRows(body string, doc *jobDriftDoc) error {
	if len(doc.Phases) != 1 || doc.Phases[0].Kind != "plan" {
		return fmt.Errorf("%w: %s, want the plan phase alone", errDriftDoc, body)
	}

	if len(doc.Steps) != 2 || doc.Steps[0].Name != "init" || doc.Steps[1].Name != "plan" {
		return fmt.Errorf("%w: %s, want the steps init and plan", errDriftDoc, body)
	}

	return nil
}

// readsTheJobOfTheCheck proves that a drift check shows as the job that any
// other plan shows: waiting for a runner. It has the operation refresh, the
// purpose drift-check and the plan phase alone.
func readsTheJobOfTheCheck(ctx context.Context, t *testing.T, env *e2e.Env, jobID string) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "read the job of the check", Method: http.MethodGet,
		Path: jobPath(e2e.Namespace, jobID), Body: "", Want: http.StatusOK, Scheme: "token", Check: checkDriftJob,
	})
}

// readsUnknownAfterTheStart proves that a check that started makes no result:
// nothing pretends that the unit is in sync.
func readsUnknownAfterTheStart(ctx context.Context, t *testing.T, env *e2e.Env, r *driftRoutes) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "read the drift while the check waits", Method: http.MethodGet, Path: r.read,
		Body: "", Want: http.StatusOK, Scheme: "token", Check: checkUnknown,
	})
}

// aManualLockQueuesACheck proves that a manual lock on a workspace does not
// refuse the start: the job waits queued with the wait reason locked, as any
// other job does. It uses a second workspace, because a second start of the
// first one in the same minute gives the first job.
func aManualLockQueuesACheck(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var lockedID string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "create the workspace to lock", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + driftLockedUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: workspaceID(&lockedID),
	})

	checks := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units/" + lockedID + "/drift-checks"
	lock := "/api/inventory/unit-lock?unit=" + url.QueryEscape("opentofu:"+e2e.Namespace+"/"+driftLockedUnit)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "lock the workspace", Method: http.MethodPut, Path: lock,
		Body: `{"reason":"change window closed"}`, Want: http.StatusOK, Scheme: "", Check: nil,
	})

	var queued string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "start a drift check on a locked workspace", Method: http.MethodPost,
		Path: checks, Body: driftBody, Want: http.StatusAccepted, Scheme: "token",
		Check: func(body string) error {
			var doc driftCheckDoc
			if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.JobID == "" {
				return fmt.Errorf("%w: %s: %w", errDriftDoc, body, err)
			}

			queued = doc.JobID

			return nil
		},
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "read the queued check", Method: http.MethodGet,
		Path: jobPath(e2e.Namespace, queued), Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			if !strings.Contains(body, `"state":"queued"`) || !strings.Contains(body, `"wait":"locked"`) {
				return fmt.Errorf("%w: %s, want a queued job that waits for the lock", errDriftDoc, body)
			}

			return nil
		},
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: driftAgent, Name: "unlock the workspace", Method: http.MethodDelete, Path: lock, Body: "",
		Want: http.StatusNoContent, Scheme: "", Check: nil,
	})
}

// refusesBadChecks proves a start that is not valid makes no job, with the
// stable code of the refusal, and that the route never ignores a key.
func refusesBadChecks(ctx context.Context, t *testing.T, env *e2e.Env, r *driftRoutes) {
	t.Helper()

	refuse := func(name, body string, headers map[string]string, want int, code string) {
		t.Helper()

		env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
			UserAgent: driftAgent, Name: name, Method: http.MethodPost, Path: r.checks, Body: body, Want: want,
			Scheme: "token", Check: codeCheck(code),
		}, headers)
	}

	refuse("start with a field that the server fixes", `{"operation":"apply"}`, nil, http.StatusBadRequest,
		"request.body-invalid")
	refuse("start with a body that is not JSON", `pool=ci`, nil, http.StatusBadRequest, "request.body-invalid")
	refuse("start with an idempotency key", driftBody, map[string]string{"Idempotency-Key": "drift-e2e-key"},
		http.StatusBadRequest, "request.body-invalid")
	refuse(
		"start with a pool that does not exist",
		`{"pool":"nope","location":{"repository":"acme/infra","ref":"main"}}`,
		nil,
		http.StatusBadRequest,
		"job.invalid",
	)
	refuse("start with no code location", `{"pool":"`+driftPool+`"}`, nil, http.StatusUnprocessableEntity,
		"job.unsupported")
}

// unknownUnitsAnswerNotFound proves that a unit that does not exist, and a unit
// ID that is no ID, answer the 404 of an unknown object on both routes.
func unknownUnitsAnswerNotFound(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, id := range []string{"ws-doesnotexist", "..%2f..", strings.Repeat("a", 200)} {
		base := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units/" + id

		for _, call := range []struct{ method, path string }{
			{http.MethodGet, base + "/drift"}, {http.MethodPost, base + "/drift-checks"},
		} {
			env.APIStepA(ctx, t, e2e.APIStepSpec{
				UserAgent: driftAgent, Name: call.method + " " + call.path, Method: call.method, Path: call.path,
				Body: "", Want: http.StatusNotFound, Scheme: "token",
				Check: func(body string) error {
					if !strings.Contains(body, `"message":"not found"`) {
						return fmt.Errorf("%w: %s, want the 404 of an unknown object", errDriftDoc, body)
					}

					return nil
				},
			})
		}
	}
}

// memberReadsNothing proves that a member with no role in the namespace reads
// the 404 of an unknown unit on both routes, and starts nothing.
func memberReadsNothing(ctx context.Context, t *testing.T, env *e2e.Env, r *driftRoutes) {
	t.Helper()

	token, ok := env.MemberToken()
	if !ok {
		return
	}

	for _, call := range []struct{ method, path string }{
		{http.MethodGet, r.read}, {http.MethodPost, r.checks},
	} {
		status, body, err := env.ServerAPIStatusAs(ctx, token, call.method, call.path, "")
		if err != nil {
			t.Fatalf("%s %s as a member: %v", call.method, call.path, err)
		}

		if status != http.StatusNotFound || !strings.Contains(body, `"message":"not found"`) {
			t.Fatalf("%s as a member = %d %s, want the 404 of an unknown unit", call.method, status, body)
		}
	}
}
