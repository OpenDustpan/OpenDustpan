//go:build e2e

package e2e_test

import (
	"net/http"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// noRuleNamespace is a namespace with no collection and no placement rule, so
// no new unit in it has a collection.
const noRuleNamespace = "norules"

// createNamespaceWithoutRules creates the namespace noRuleNamespace through the
// installation API and seeds no collection and no placement rule, which the
// harness helper would seed.
func createNamespaceWithoutRules(t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(
		t.Context(), http.MethodPost, "/api/server/namespaces", `{"name":`+strconv.Quote(noRuleNamespace)+`}`,
	)
	if err != nil {
		t.Fatalf("POST /api/server/namespaces %q: %v", noRuleNamespace, err)
	}

	if status != http.StatusCreated {
		t.Fatalf(
			"POST /api/server/namespaces %q answers %d, want %d (%s)",
			noRuleNamespace,
			status,
			http.StatusCreated,
			body,
		)
	}
}

// pulumiUnitWithoutCollectionRefused proves that the Pulumi CLI cannot make a
// stack that no placement rule places: the CLI fails with the error that
// names the rules.
func pulumiUnitWithoutCollectionRefused(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	createNamespaceWithoutRules(t, env)

	env.Step(ctx, t, failStep(
		"stack init with no placement rule", outputContains("no placement rule matches"),
		"stack", "init", noRuleNamespace+"/"+e2e.Project+"/orphan", "--no-select",
	))
}

// tofuUnitWithoutCollectionRefused proves that OpenTofu cannot make a
// workspace that no placement rule places: init fails with the error that
// names the rules. It replaces the configuration of the project.
func tofuUnitWithoutCollectionRefused(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	createNamespaceWithoutRules(t, env)

	env.WriteTofuConfig(t, `terraform {
  cloud {
    hostname     = `+strconv.Quote(env.Host())+`
    organization = `+strconv.Quote(noRuleNamespace)+`
    workspaces {
      name = "orphan"
    }
  }
}

output "greeting" {
  value = "hello"
}
`)

	env.Step(ctx, t, failStep(
		"init with no placement rule", outputContains("no placement rule matches"),
		"init", "-input=false", "-no-color",
	))
}
