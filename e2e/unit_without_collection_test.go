//go:build e2e

package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

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
	collectionID := placementCollectionID(ctx, t, env, e2e.Namespace, e2e.CatchAllCollection)

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
		again := `{"tool":` + strconv.Quote(tool) + `,"project":"*","stack":"*","collectionID":` +
			strconv.Quote(collectionID) + `}`

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

// dropUnitCollection removes the collection membership of the Pulumi stack
// name with direct SQL, which makes a stack in no collection, as a stack that
// was made before collections existed. No API route does this on purpose.
func dropUnitCollection(t *testing.T, env *e2e.Env, stackName string) {
	t.Helper()

	ctx, dsn := t.Context(), env.DatabaseURL(t)
	suffix := "%/" + stackName

	if path, ok := strings.CutPrefix(dsn, "sqlite:"); ok {
		query := url.Values{}
		query.Add("_pragma", "busy_timeout(10000)")

		conn, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String())
		if err != nil {
			t.Fatalf("open the database: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				t.Errorf("close the database: %v", closeErr)
			}
		}()

		_, err = conn.ExecContext(ctx, `DELETE FROM collection_members WHERE unit_id IN (
			SELECT unit_id FROM inventory WHERE namespace = ? AND tool = 'pulumi' AND name LIKE ?)`,
			e2e.Namespace, suffix)
		if err != nil {
			t.Fatalf("drop the collection of %s: %v", stackName, err)
		}

		return
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to the database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(context.WithoutCancel(ctx)); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	}()

	_, err = conn.Exec(ctx, `DELETE FROM collection_members WHERE unit_id IN (
		SELECT unit_id FROM inventory WHERE namespace = $1 AND tool = 'pulumi' AND name LIKE $2)`,
		e2e.Namespace, suffix)
	if err != nil {
		t.Fatalf("drop the collection of %s: %v", stackName, err)
	}
}

// pulumiStackInNoCollectionRefused proves that the Pulumi CLI cannot write to
// an existing stack that no collection holds: the update fails with the code
// and the command that helps, and works again after the move.
func pulumiStackInNoCollectionRefused(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	const name = "unplaced"

	qualified := org + "/" + e2e.Project + "/" + name
	unit := "pulumi:" + e2e.Namespace + "/" + e2e.Project + "/" + name

	env.Step(t.Context(), t, step("stack init for the no-collection check", nil, "stack", "init", qualified))
	dropUnitCollection(t, env, name)

	env.Step(t.Context(), t, failStep(
		"preview on a stack in no collection", outputContains("PUT /api/inventory/unit-collection"),
		"preview", "--non-interactive", "--stack", qualified,
	))

	status, body, err := env.APIStatus(t.Context(), http.MethodPut,
		"/api/inventory/unit-collection?unit="+url.QueryEscape(unit), `{"collection":"`+e2e.CatchAllCollection+`"}`)
	if err != nil || status != http.StatusOK {
		t.Fatalf("move the stack = %d %s: %v, want 200", status, body, err)
	}

	env.Step(t.Context(), t, step("preview after the move", nil, "preview", "--non-interactive", "--stack", qualified))
	env.Step(
		t.Context(),
		t,
		step("stack rm after the check", nil, "stack", "rm", "--yes", "--force", "--stack", qualified),
	)
}
