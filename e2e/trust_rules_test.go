//go:build e2e

package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// trustRuleAgent is the User-Agent of the trust rule requests. The route
// coverage check counts requests that do not carry the harness agent.
const trustRuleAgent = "opendustpan-e2e-trust-rules"

// trustRulesPath is the trust rule route of the e2e namespace.
const trustRulesPath = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/trust-rules"

// errNoID reports an answer without the ID of the object that it made.
var errNoID = errors.New("the answer holds no id")

// TestTrustRuleAPI creates, reads, lists and deletes a workload trust rule
// of a namespace through the API on a server of each store. It refuses a
// rule with no condition on the subject and a rule for a person, each with no
// change.
func TestTrustRuleAPI(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()
			skipWithoutStore(t, store)

			// The creates of principals need a console session with a recent
			// sign-in.
			env := e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store)

			trustRuleScenario(t, env)
		})
	}
}

// captureID returns a check that stores the id of a JSON answer in dst.
func captureID(dst *string) func(string) error {
	return func(body string) error {
		var doc struct {
			ID string `json:"id"`
		}

		if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.ID == "" {
			return fmt.Errorf("%w: %s", errNoID, body)
		}

		*dst = doc.ID

		return nil
	}
}

// trustRuleBody returns the body of a create of a rule called name for the
// principal principalID, with the conditions conds as JSON.
func trustRuleBody(name, issuer, principalID, conds string) string {
	return `{"name":"` + name + `","issuer":"` + issuer + `","audience":"https://dustpan.example.com",` +
		`"keySource":"discovery","principal":"` + principalID + `","conditions":` + conds + `}`
}

// trustRuleSubject is the condition on the subject of the rules of the
// scenario.
const trustRuleSubject = `[{"claim":"sub","values":["repo:acme/infra:ref:refs/heads/main"]}]`

func trustRuleScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	step := func(name, method, path, body string, want int, check func(string) error) {
		env.APIStepA(ctx, t, e2e.APIStepSpec{
			UserAgent: trustRuleAgent, Name: name, Method: method, Path: path, Body: body,
			Want: want, Check: check, Scheme: "token",
		})
	}

	var worker, person e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + e2e.Namespace + "/principals", `{"subject":"ci-deployer"}`,
		http.StatusCreated,
	}, &worker)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"trust-rule-person","kind":"person"}`,
		http.StatusCreated,
	}, &person)

	var rule string

	issuer := "https://token.actions.example.com"

	step("a rule with no condition on the subject is refused", http.MethodPost, trustRulesPath,
		trustRuleBody("deploy", issuer, worker.ID, `[{"claim":"repository","values":["acme/infra"]}]`),
		http.StatusBadRequest, bodyHolds("trust-rule.invalid"))
	step("a rule for a person is refused", http.MethodPost, trustRulesPath,
		trustRuleBody("deploy", issuer, person.ID, trustRuleSubject),
		http.StatusBadRequest, bodyHolds("trust-rule.principal-refused"))
	step("the refused creates made no rule", http.MethodGet, trustRulesPath, "", http.StatusOK,
		bodyHolds(`{"rules":[]}`))
	step("create a rule", http.MethodPost, trustRulesPath, trustRuleBody("deploy", issuer, worker.ID, trustRuleSubject),
		http.StatusCreated, captureID(&rule))
	step("read the rule", http.MethodGet, trustRulesPath+"/"+rule, "", http.StatusOK, bodyHolds(`"name":"deploy"`))
	step("list the rules", http.MethodGet, trustRulesPath, "", http.StatusOK, bodyHolds(rule))
	step("delete the rule", http.MethodDelete, trustRulesPath+"/"+rule, "", http.StatusNoContent, nil)
	step("the deleted rule is not found", http.MethodGet, trustRulesPath+"/"+rule, "", http.StatusNotFound,
		bodyHolds("trust-rule.not-found"))
	step("a namespace that does not exist is not found", http.MethodGet,
		"/api/dustpan/v1/namespaces/absent/trust-rules", "", http.StatusNotFound, nil)
}

// bodyHolds returns a check that the body holds fragment.
func bodyHolds(fragment string) func(string) error {
	return func(body string) error {
		if !strings.Contains(body, fragment) {
			return fmt.Errorf("%w: %q not in %s", errAnswerLacksFragment, fragment, body)
		}

		return nil
	}
}
