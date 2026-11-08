//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// rulesPath is the placement rule route of the namespace of the harness.
const rulesPath = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/placement-rules"

// ruleDoc holds the fields of a placement rule that the steps read.
type ruleDoc struct {
	ID         string `json:"id"`
	Tool       string `json:"tool"`
	Project    string `json:"project"`
	Stack      string `json:"stack"`
	Collection string `json:"collection"`
}

// ruleListDoc is the answer of the rule list.
type ruleListDoc struct {
	Rules []ruleDoc `json:"rules"`
}

// catchAllRuleID returns the ID of the catch-all rule of tool in the namespace
// of the harness.
func catchAllRuleID(t *testing.T, env *e2e.Env, tool string) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(t.Context(), http.MethodGet, rulesPath, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s = %d %s: %v, want 200", rulesPath, status, body, err)
	}

	var list ruleListDoc

	if err = json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode the rules %q: %v", body, err)
	}

	for _, rule := range list.Rules {
		if rule.Tool == tool && rule.Project == "*" && rule.Stack == "*" && rule.Collection == e2e.CatchAllCollection {
			return rule.ID
		}
	}

	t.Fatalf("the namespace %s has no catch-all rule for %s in %s", e2e.Namespace, tool, body)

	return ""
}

// withoutCatchAllRule removes the catch-all placement rule of tool from the
// namespace of the harness, runs fn, and makes the rule again. While fn runs,
// no rule places a new unit of the tool, so the server must refuse it. The
// steps use the namespace of the scenario, because a second namespace changes
// the default organization of the Pulumi CLI.
func withoutCatchAllRule(t *testing.T, env *e2e.Env, tool string, fn func()) {
	t.Helper()

	ctx := t.Context()
	id := catchAllRuleID(t, env, tool)

	status, body, err := env.ServerAPIStatus(ctx, http.MethodDelete, rulesPath+"/"+id, "")
	if err != nil || status != http.StatusNoContent {
		t.Fatalf("DELETE the %s rule = %d %s: %v, want 204", tool, status, body, err)
	}

	// The rule comes back whether fn passes or fails. A failed step stops the
	// test with t.Fatal, and a rule that stayed deleted would fail every later
	// scenario of the shared server with a misleading placement error. The
	// context of the test is canceled before a cleanup runs, so the restore
	// uses a context that is not canceled.
	t.Cleanup(func() {
		again := `{"tool":` + strconv.Quote(tool) + `,"project":"*","stack":"*","collection":` +
			strconv.Quote(e2e.CatchAllCollection) + `}`

		restoreStatus, restoreBody, restoreErr := env.ServerAPIStatus(
			context.WithoutCancel(ctx), http.MethodPost, rulesPath, again,
		)
		if restoreErr != nil || restoreStatus != http.StatusCreated {
			t.Errorf("POST the %s rule again = %d %s: %v, want 201", tool, restoreStatus, restoreBody, restoreErr)
		}
	})

	fn()
}

// pulumiUnitWithoutCollectionRefused proves that the Pulumi CLI cannot make a
// stack that no placement rule places: the CLI fails with the error that
// names the rules.
func pulumiUnitWithoutCollectionRefused(t *testing.T, env *e2e.Env) {
	t.Helper()

	withoutCatchAllRule(t, env, "pulumi", func() {
		env.Step(t.Context(), t, failStep(
			"stack init with no placement rule", outputContains("no placement rule matches"),
			"stack", "init", e2e.Namespace+"/"+e2e.Project+"/orphan", "--no-select",
		))
	})
}

// tofuUnitWithoutCollectionRefused proves that OpenTofu cannot make a
// workspace that no placement rule places: init fails with the error that
// names the rules. It replaces the configuration of the project.
func tofuUnitWithoutCollectionRefused(t *testing.T, env *e2e.Env) {
	t.Helper()

	withoutCatchAllRule(t, env, "opentofu", func() {
		env.WriteTofuConfig(t, `terraform {
  cloud {
    hostname     = `+strconv.Quote(env.Host())+`
    organization = `+strconv.Quote(e2e.Namespace)+`
    workspaces {
      name = "orphan"
    }
  }
}

output "greeting" {
  value = "hello"
}
`)

		env.Step(t.Context(), t, failStep(
			"init with no placement rule", outputContains("no placement rule matches"),
			"init", "-input=false", "-no-color",
		))
	})
}
