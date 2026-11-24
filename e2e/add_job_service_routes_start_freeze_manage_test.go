//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// freezeRoute is the freeze route of the scenario namespace.
const freezeRoute = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/freezes"

// freezeDoc is the part of a freeze document that the steps read.
type freezeDoc struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Active bool   `json:"active"`
}

// freezeList is the part of the freeze list that the steps read.
type freezeList struct {
	Freezes []freezeDoc `json:"freezes"`
}

// TestFreezeManageThroughJobs drives a change freeze with a personal access
// token over TLS, on each store. The person creates a freeze, and the job routes
// refuse a start and a cancel with the code job.frozen. The person ends the
// freeze, and a start is accepted again. A start through a new freeze with a
// breakFreezeReason is accepted.
//
// The person is a site administrator: the freeze actions are not classified
// in the access families yet, so only an administrator holds them.
func TestFreezeManageThroughJobs(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			freezeScenario(t, env)
		})
	}
}

// freezeScenario runs the steps against env.
func freezeScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	checkReady(ctx, t, env)

	// The unit and the pool come from the public API of the server, as the jobs
	// scenario makes them.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the workspace", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"` + jobsUnit + `"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "create the runner pool", Method: http.MethodPost,
		Path: "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/runner-pools",
		Body: `{"name":"` + jobsPool + `","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	// A job that the administrator starts before the freeze. The freeze refuses
	// the cancel of it.
	started := startsAJob(ctx, t, env)

	token := freezeAdminToken(ctx, t, env)

	created := createFreeze(ctx, t, env, token, "release freeze")

	freezeStep(ctx, t, env, token, http.MethodPost, freezeRoute, `{"reason":"second freeze"}`, http.StatusConflict)

	answer := freezeStep(ctx, t, env, token, http.MethodPost, jobsRoute, startBody(jobsPool, "plan"),
		http.StatusConflict)
	if err := codeCheck("job.frozen")(answer); err != nil {
		t.Fatalf("start during the freeze: %v", err)
	}

	answer = freezeStep(ctx, t, env, token, http.MethodPost, jobPath(e2e.Namespace, started.ID)+"/cancel", "",
		http.StatusConflict)
	if err := codeCheck("job.frozen")(answer); err != nil {
		t.Fatalf("cancel during the freeze: %v", err)
	}

	checkActiveFreeze(ctx, t, env, token, created)

	answer = freezeStep(ctx, t, env, token, http.MethodPost, freezeRoute+"/current/end", "", http.StatusOK)

	var ended freezeDoc
	if err := json.Unmarshal([]byte(answer), &ended); err != nil {
		t.Fatalf("decode the ended freeze %q: %v", answer, err)
	}

	if ended.Active {
		t.Fatalf("the freeze after its end is active: %s", answer)
	}

	answer = freezeStep(ctx, t, env, token, http.MethodPost, jobsRoute, startBody(jobsPool, "plan"),
		http.StatusCreated)
	if _, err := checkStarted(answer); err != nil {
		t.Fatalf("start after the end of the freeze: %v", err)
	}

	// A new freeze is created after the end. A start through it needs a
	// breakFreezeReason, and the start with the reason is accepted.
	createFreeze(ctx, t, env, token, "break-glass freeze")

	answer = freezeStep(ctx, t, env, token, http.MethodPost, jobsRoute, breakGlassBody("incident fix"),
		http.StatusCreated)
	if _, err := checkStarted(answer); err != nil {
		t.Fatalf("break-glass start during the freeze: %v", err)
	}

	freezeStep(ctx, t, env, token, http.MethodPost, freezeRoute+"/current/end", "", http.StatusOK)
}

// createFreeze creates a freeze with reason through token and checks the answer.
func createFreeze(ctx context.Context, t *testing.T, env *e2e.Env, token, reason string) freezeDoc {
	t.Helper()

	body, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		t.Fatalf("encode the freeze reason: %v", err)
	}

	answer := freezeStep(ctx, t, env, token, http.MethodPost, freezeRoute, string(body), http.StatusCreated)

	var created freezeDoc
	if err := json.Unmarshal([]byte(answer), &created); err != nil {
		t.Fatalf("decode the created freeze %q: %v", answer, err)
	}

	if created.ID == "" || !created.Active || created.Reason != reason {
		t.Fatalf("the created freeze = %+v, want an active freeze with reason %q", created, reason)
	}

	return created
}

// checkActiveFreeze reads the freeze list and checks that the active freeze is
// the one that was created.
func checkActiveFreeze(ctx context.Context, t *testing.T, env *e2e.Env, token string, want freezeDoc) {
	t.Helper()

	answer := freezeStep(ctx, t, env, token, http.MethodGet, freezeRoute, "", http.StatusOK)

	var list freezeList
	if err := json.Unmarshal([]byte(answer), &list); err != nil {
		t.Fatalf("decode the freeze list %q: %v", answer, err)
	}

	for _, f := range list.Freezes {
		if f.ID == want.ID && f.Active && f.Reason == want.Reason {
			return
		}
	}

	t.Fatalf("the freeze list %q does not hold the active freeze %s", answer, want.ID)
}

// breakGlassBody returns the body of a start that names a breakFreezeReason.
func breakGlassBody(reason string) string {
	return strings.TrimSuffix(startBody(jobsPool, "plan"), "}") + fmt.Sprintf(`,"breakFreezeReason":%q}`, reason)
}

// freezeStep sends one request with the personal token and fails unless the
// status is want. It returns the answer body.
func freezeStep(
	ctx context.Context, t *testing.T, env *e2e.Env, token, method, path, body string, want int,
) string {
	t.Helper()

	status, answer, err := env.APIStatusAs(ctx, token, method, path, body)
	if err != nil || status != want {
		t.Logf("answer: %s", answer)
		t.Fatalf("status %d, want %d, for %s, err %v", status, want, method, err)
	}

	return answer
}

// freezeAdminToken makes a person with the site-administrator role and returns
// the personal token of the person.
func freezeAdminToken(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	var person e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("freeze-admin") + `","kind":"person"}`,
		http.StatusCreated,
	}, &person)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:` + person.ID + `","role":"site-administrator"}`,
		http.StatusCreated,
	}, nil)

	return env.PersonalToken(ctx, t, person.ID, "freeze-admin-token").Secret
}
