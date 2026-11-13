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
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	Wait      string      `json:"wait"`
	Pool      string      `json:"pool"`
	PoolID    string      `json:"poolID"`
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

	// The list and the log read need a launcher session, which has its own
	// server of each store.
	t.Run("list_and_log", func(t *testing.T) {
		t.Parallel()

		for _, store := range stores {
			t.Run(string(store), func(t *testing.T) {
				t.Parallel()

				listAndLog(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store))
			})
		}
	})

	// The plan upload and commit need a launcher session, which has its own
	// server of each store.
	t.Run("plan_parts", func(t *testing.T) {
		t.Parallel()

		for _, store := range stores {
			t.Run(string(store), func(t *testing.T) {
				t.Parallel()

				planPartsScenario(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store))
			})
		}
	})

	// The session round trip has its own server of each store: the read of
	// the stored log stops the server.
	t.Run("session_round_trip", func(t *testing.T) {
		t.Parallel()

		for _, store := range stores {
			t.Run(string(store), func(t *testing.T) {
				t.Parallel()

				sessionRoundTrip(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store))
			})
		}
	})
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
	// The gate steps come before any other refusal: the limiter of denials
	// writes the first entry of a principal, an action and a namespace in a
	// window, and the check reads that entry.
	gatesAndLimitsRefuseAStart(ctx, t, env)
	aManualLockQueuesAJob(ctx, t, env)
	refusesBadStarts(ctx, t, env)
	defaultsAndPools(ctx, t, env)
	missingJobsAnswerNotFound(ctx, t, env, started)
	memberStartsNothing(ctx, t, env, started)
	aGateStopsAClaim(ctx, t, env)
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

	// The name comes from the pool reader of the server, the ID from the job.
	if doc.Pool != jobsPool || doc.PoolID == "" {
		return doc, fmt.Errorf("%w: pool %q, poolID %q, want the pool %s", errJobDoc, doc.Pool, doc.PoolID, jobsPool)
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
		{"a start with an invalid pool name", startBody("a b", "plan"), http.StatusBadRequest, "job.invalid"},
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

	withUnitConflict := map[string]string{"Idempotency-Key": "jobs-e2e-key-1"}

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "a start with the key of another request", Method: http.MethodPost,
		Path: jobsRoute, Body: strings.Replace(startBody(jobsPool, "plan"), `"e2e"`, `"another reason"`, 1),
		Want: http.StatusUnprocessableEntity, Scheme: "token", Check: withCode("request.idempotency-key-reused"),
	}, withUnitConflict)

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

// noLocationBody is a start that names no code location.
func noLocationBody(pool string) string {
	if pool == "" {
		return fmt.Sprintf(`{"unit":{"tool":"opentofu","name":%q},"operation":"plan","reason":"e2e"}`, jobsUnit)
	}

	return fmt.Sprintf(`{"unit":{"tool":"opentofu","name":%q},"operation":"plan","pool":%q,"reason":"e2e"}`,
		jobsUnit, pool)
}

// noPoolBody is a start that names a code location and no pool.
func noPoolBody() string {
	return fmt.Sprintf(`{"unit":{"tool":"opentofu","name":%q},"operation":"plan",`+
		`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"e2e"}`, jobsUnit)
}

// codeCheck returns a check of the stable code of a refusal.
func codeCheck(code string) func(string) error {
	return func(body string) error {
		if !strings.Contains(body, `"errorCode":"`+code+`"`) {
			return fmt.Errorf("%w: body %s, want the code %s", errJobDoc, body, code)
		}

		return nil
	}
}

// defaultsAndPools proves the default pool, the refusal of a start with no
// code location, and the refusal of a default when the namespace has two job
// pools. The administrator holds the actions of a pool and a location.
func defaultsAndPools(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "start on the only job pool", Method: http.MethodPost, Path: jobsRoute,
		Body: noPoolBody(), Want: http.StatusCreated, Scheme: "token", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "a start with no code location", Method: http.MethodPost, Path: jobsRoute,
		Body: noLocationBody(""), Want: http.StatusUnprocessableEntity, Scheme: "token",
		Check: codeCheck("job.unsupported"),
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create a second job pool", Method: http.MethodPost,
		Path: "/api/runners/" + e2e.Namespace + "/pools", Body: `{"name":"deploy","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "a start with two job pools and no pool", Method: http.MethodPost,
		Path: jobsRoute, Body: noPoolBody(), Want: http.StatusUnprocessableEntity, Scheme: "token",
		Check: codeCheck("job.unsupported"),
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "start on the second pool by name", Method: http.MethodPost, Path: jobsRoute,
		Body: startBody("deploy", "plan"), Want: http.StatusCreated, Scheme: "token", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "a start with a connection that the namespace does not hold",
		Method: http.MethodPost, Path: jobsRoute,
		Body: strings.Replace(startBody("deploy", "plan"), `"repository"`, `"connection":"other-conn","repository"`, 1),
		Want: http.StatusForbidden, Scheme: "token", Check: nil,
	})
}

// gatesAndLimitsRefuseAStart proves a start fails closed when an enforced
// gate at state.write or change.propose covers the unit, goes on when the
// gate is in evaluate mode, and fails when an access limit forbids the start
// action. Each refusal writes an audit entry. The steps write gates through
// the store: the binary server has no such step, and the scenario then skips.
func gatesAndLimitsRefuseAStart(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ns := namespace.Name(e2e.Namespace)

	unitScope, err := gate.UnitScope(ns, "opentofu", jobsUnit)
	if err != nil {
		t.Fatalf("unit scope: %v", err)
	}

	newGate := func(scope gate.Scope, id string, point gate.DecisionPoint, mode gate.Mode) gate.Gate {
		return gate.Gate{
			Scope:         scope,
			ID:            gate.ID(id),
			Version:       0,
			DecisionPoint: point,
			Condition:     "",
			Unless:        "",
			Outcome:       gate.OutcomeDeny,
			Message:       "Plans are frozen.",
			Mode:          mode,
			OnError:       gate.OnErrorFail,
			Final:         false,
			CheckedWith:   gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
			State:         "",
			Reads:         nil,
		}
	}

	refused := func(name string, want int, code string) {
		check := codeCheck(code)
		if code == "" {
			check = nil
		}

		env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: jobsAgent,
			Name:      name,
			Method:    http.MethodPost,
			Path:      jobsRoute,
			Body:      startBody(jobsPool, "plan"),
			Want:      want,
			Scheme:    "token",
			Check:     check,
		})
	}

	audited := func(reasonPrefix string) {
		entries, auditErr := fetchAuditEntries(ctx, env, string(ns))
		if auditErr != nil {
			t.Fatalf("audit entries: %v", auditErr)
		}

		for i := range entries {
			e := &entries[i]
			if e.Action == string(identity.ActionUnitRunPlan) && e.Outcome == string(audit.OutcomeDenied) &&
				strings.HasPrefix(e.Reason, reasonPrefix) {
				return
			}
		}

		t.Fatalf("no denied unit:run-plan entry with the reason %q among %d entries", reasonPrefix, len(entries))
	}

	// An enforced gate at state.write on the unit.
	stateWrite := newGate(unitScope, "no-plans", gate.PointStateWrite, gate.ModeEnforce)
	must(t, env.PutGate(ctx, t, stateWrite))

	refused("a start with an enforced gate at state.write", http.StatusForbidden, "job.gate-enforced")
	audited("an enforced gate covers the unit: no-plans")
	must(t, env.DeleteGate(ctx, t, unitScope, stateWrite.ID))

	// An enforced gate at change.propose in the namespace.
	propose := newGate(gate.NamespaceScope(ns), "review-first", gate.PointChangePropose, gate.ModeEnforce)
	must(t, env.PutGate(ctx, t, propose))
	refused("a start with an enforced gate at change.propose", http.StatusForbidden, "job.gate-enforced")
	must(t, env.DeleteGate(ctx, t, propose.Scope, propose.ID))

	// A gate in evaluate mode does not stop the start.
	watch := newGate(unitScope, "watch-plans", gate.PointStateWrite, gate.ModeEvaluate)
	must(t, env.PutGate(ctx, t, watch))
	refused("a start with a gate in evaluate mode", http.StatusCreated, "")
	must(t, env.DeleteGate(ctx, t, unitScope, watch.ID))

	// An access limit that forbids the start action of the starter.
	limit := gate.AccessLimit{
		Scope: unitScope, ID: "no-plan-starts", Version: 0,
		Policy: `forbid (principal, action == OpenDustpan::Action::"unit:run-plan", resource);`,
	}
	must(t, env.PutAccessLimit(ctx, t, limit))
	refused("a start that an access limit forbids", http.StatusForbidden, "")
	must(t, env.DeleteAccessLimit(ctx, t, limit.Scope, limit.ID))

	refused("a start after the gates and the limit are gone", http.StatusCreated, "")
}
