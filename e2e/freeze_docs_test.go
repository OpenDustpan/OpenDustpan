//go:build e2e

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// freezeGuideAgent is the User-Agent of the requests of the freeze guide.
const freezeGuideAgent = "opendustpan-e2e-freeze-guide"

// TestFreezeGuideCommands runs each command of docs/guides/freeze.md against a
// server of each store: create a freeze, list the freezes, start a refresh that
// the freeze refuses, grant the freeze-breaker role, start the refresh through
// the freeze with a breakFreezeReason, and end the freeze.
func TestFreezeGuideCommands(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			env := e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store)

			freezeGuideScenario(t.Context(), t, env)
		})
	}
}

// freezeGuideScenario runs the guide commands in the order of the guide. The
// guide assumes a unit web and a runner pool jobs, so the scenario makes them
// first.
func freezeGuideScenario(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	checkReady(ctx, t, env)

	base := "/api/dustpan/v1/namespaces/" + e2e.Namespace
	freezes := base + "/freezes"
	jobs := base + "/jobs"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "make the unit web", Method: http.MethodPost,
		Path: "/api/v2/organizations/" + e2e.Namespace + "/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"web"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "make the runner pool jobs", Method: http.MethodPost,
		Path: base + "/runner-pools", Body: `{"name":"jobs","purpose":"jobs"}`,
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	// The guide's "$BREAK_TOKEN" is the token of a person who gets the
	// freeze-breaker role. The person also needs the admin role on the namespace
	// for a start that names a runner pool.
	breaker := freezeGuidePrincipal(ctx, t, env)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "create a freeze", Method: http.MethodPost, Path: freezes,
		Body: `{"reason": "release freeze"}`, Want: http.StatusCreated, Scheme: "token",
		Check: freezeGuideBodyHas(`"reason":"release freeze"`, `"active":true`),
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "list the freezes", Method: http.MethodGet, Path: freezes,
		Body: "", Want: http.StatusOK, Scheme: "token",
		Check: freezeGuideBodyHas(`"reason":"release freeze"`, `"active":true`),
	})

	// The route starts plan jobs only: it refuses a refresh as unsupported, and it
	// accepts a plan start during the freeze.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "start a refresh during the freeze", Method: http.MethodPost,
		Path: jobs, Body: freezeGuideStart("refresh", "nightly refresh", ""),
		Want: http.StatusUnprocessableEntity, Scheme: "token", Check: codeCheck("job.unsupported"),
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "start a plan during the freeze", Method: http.MethodPost,
		Path: jobs, Body: freezeGuideStart("plan", "nightly plan", ""),
		Want: http.StatusCreated, Scheme: "token", Check: nil,
	})

	// The guide's grant of the freeze-breaker role. The person in the guide is
	// the breaker that the scenario made.
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:` + breaker.ID + `","role":"freeze-breaker"}`,
		http.StatusCreated,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + e2e.Namespace + "/grants",
		`{"subject":"principal:` + breaker.ID + `","role":"admin"}`,
		http.StatusCreated,
	}, nil)

	breakToken := env.PersonalToken(ctx, t, breaker.ID, "freeze-guide-breaker").Secret

	// The guide's break-glass start, with an empty and with a valid reason.
	env.APIStep(ctx, t, breakToken, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "break-glass start with an empty reason", Method: http.MethodPost,
		Path: jobs, Body: freezeGuideStart("plan", "incident fix", `""`),
		Want: http.StatusBadRequest, Scheme: "token", Check: codeCheck("job.invalid"),
	})
	env.APIStep(ctx, t, breakToken, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "break-glass start of a plan", Method: http.MethodPost,
		Path: jobs, Body: freezeGuideStart("plan", "incident fix", `"sev1 rollback"`),
		Want: http.StatusCreated, Scheme: "token",
		Check: freezeGuideBodyHas(`"id":"job-`, `"operation":"plan"`),
	})

	// The guide's end of the freeze, and the answer when none is active.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "end the freeze", Method: http.MethodPost,
		Path: freezes + "/current/end", Body: "", Want: http.StatusOK, Scheme: "token",
		Check: freezeGuideBodyHas(`"active":false`),
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: freezeGuideAgent, Name: "end with no active freeze", Method: http.MethodPost,
		Path: freezes + "/current/end", Body: "", Want: http.StatusNotFound, Scheme: "token",
		Check: codeCheck("job.not-found"),
	})
}

// freezeGuideStart returns the body of a start that the guide sends. breakReason
// is the JSON value of breakFreezeReason, or "" to leave the field out.
func freezeGuideStart(operation, reason, breakReason string) string {
	body := fmt.Sprintf(
		`{"unit":{"tool":"opentofu","name":"web"},"operation":%q,"pool":"jobs",`+
			`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":%q`,
		operation, reason,
	)

	if breakReason != "" {
		body += `,"breakFreezeReason":` + breakReason
	}

	return body + "}"
}

// freezeGuidePrincipal makes the person of the guide's break-glass examples and
// returns the principal.
func freezeGuidePrincipal(ctx context.Context, t *testing.T, env *e2e.Env) e2ePrincipal {
	t.Helper()

	var breaker e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("freeze-guide") + `","kind":"person"}`,
		http.StatusCreated,
	}, &breaker)

	return breaker
}

// errFreezeGuideBody reports an answer that lacks a text of the guide.
var errFreezeGuideBody = errors.New("freeze guide answer")

// freezeGuideBodyHas returns a check that the body holds each of the texts.
func freezeGuideBodyHas(want ...string) func(string) error {
	return func(body string) error {
		for _, w := range want {
			if !strings.Contains(body, w) {
				return fmt.Errorf("%w: body %s, want %s", errFreezeGuideBody, body, w)
			}
		}

		return nil
	}
}
