//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// proxyRulesAgent is the User-Agent of the workspace request of the proxy rule
// steps.
const proxyRulesAgent = "opendustpan-e2e-proxy-rules"

// The proxy rule sets of the steps: one inspect proxy rule for the namespace
// and one tunnel proxy rule for the unit.
const (
	proxyRulesNamespaceDoc = `{"rules":[{"id":"status","operations":["plan"],"host":"status.example.com",` +
		`"mode":"inspect","methods":["GET"],"paths":["/v1/status"]}]}`
	proxyRulesUnitDoc = `{"rules":[{"id":"registry","operations":["plan","apply"],"host":"registry.example.com",` +
		`"mode":"tunnel"}]}`
)

// TestProxyRules proves the proxy rule routes through the real server on each
// store: no CLI sets a proxy rule set, so the harness calls the routes. The
// steps save the namespace set and the unit set, read both, read the joined set
// and the rendered view of one operation, and check that a save with an access
// token and a save with a bad document change nothing.
func TestProxyRules(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.OpenTofu().Single()
	if version == "" {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.OpenTofu(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, version, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runProxyRulesOnStore(t, cli, db)
		})
	}
}

func runProxyRulesOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	checkApprovalCreateNamespace(ctx, t, approvalEnv{env: env, person: ""})

	ae := approvalAdmin(ctx, t, env)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: proxyRulesAgent, Name: "create the workspace web", Method: http.MethodPost,
		Path: "/api/v2/organizations/approvals/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"web"}}}`,
		Want: http.StatusCreated, Scheme: "", Check: nil,
	})

	unit := approvalNamespaceBase + "/units/" + proxyRulesUnitID(ctx, t, env) + "/proxy-rules"
	namespace := approvalNamespaceBase + "/proxy-rules"

	checkProxyRulesSaveAndRead(ctx, t, ae, namespace, unit)
	checkProxyRulesRefusals(ctx, t, ae, namespace, unit)
}

// proxyRulesUnitID returns the unit ID of the workspace web.
func proxyRulesUnitID(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, approvalNamespaceBase+"/units", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("list the units = %d, %v: %s", status, err, body)
	}

	var list unitListDoc
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode the unit list: %v: %s", err, body)
	}

	for _, u := range list.Units {
		if u.Name == "web" {
			return u.UnitID
		}
	}

	t.Fatalf("the unit list holds no unit web: %s", body)

	return ""
}

// checkProxyRulesSaveAndRead saves the two sets and reads them back.
func checkProxyRulesSaveAndRead(ctx context.Context, t *testing.T, env approvalEnv, namespace, unit string) {
	t.Helper()

	body := approvalCall(ctx, t, env, approvalStep{
		step: "read a namespace with no set", method: http.MethodGet, path: namespace, want: http.StatusOK,
	})
	wantBodyHas(t, "read a namespace with no set", body, `"version":0`, `"rules":[]`)

	body = approvalCall(ctx, t, env, approvalStep{
		step: "save the namespace set", method: http.MethodPut, path: namespace, body: proxyRulesNamespaceDoc,
		want: http.StatusOK,
	})
	wantBodyHas(t, "save the namespace set", body, `"version":1`, `"id":"status"`, `"port":443`, `"digest":"`)

	body = approvalCall(ctx, t, env, approvalStep{
		step: "read the namespace set", method: http.MethodGet, path: namespace, want: http.StatusOK,
	})
	wantBodyHas(t, "read the namespace set", body, `"version":1`, `"id":"status"`)

	approvalCall(ctx, t, env, approvalStep{
		step: "save the unit set", method: http.MethodPut, path: unit, body: proxyRulesUnitDoc, want: http.StatusOK,
	})

	body = approvalCall(ctx, t, env, approvalStep{
		step: "read the unit set and the joined set", method: http.MethodGet, path: unit, want: http.StatusOK,
	})
	wantBodyHas(t, "read the unit set and the joined set", body, `"joined":[`, `"scope":"namespace"`, `"scope":"unit"`)

	if strings.Index(body, `"id":"status"`) > strings.LastIndex(body, `"id":"registry"`) {
		t.Fatalf("joined set %s lists the unit rule before the namespace rule", body)
	}

	body = approvalCall(ctx, t, env, approvalStep{
		step: "render the plan view", method: http.MethodGet, path: unit + "/rendered?operation=plan",
		want: http.StatusOK,
	})
	wantBodyHas(t, "render the plan view", body, `"operation":"plan"`, `"id":"status"`, `"id":"registry"`)

	body = approvalCall(ctx, t, env, approvalStep{
		step: "render the apply view", method: http.MethodGet, path: unit + "/rendered?operation=apply",
		want: http.StatusOK,
	})
	wantBodyHas(t, "render the apply view", body, `"id":"registry"`)

	if strings.Contains(body, `"id":"status"`) {
		t.Fatalf("apply view %s holds the plan-only rule status", body)
	}
}

// checkProxyRulesRefusals proves that each refusal changes nothing.
func checkProxyRulesRefusals(ctx context.Context, t *testing.T, env approvalEnv, namespace, unit string) {
	t.Helper()

	approvalCall(ctx, t, env, approvalStep{
		step: "save a bad host", method: http.MethodPut, path: namespace,
		body: strings.Replace(proxyRulesNamespaceDoc, "status.example.com", "Status.Example.com", 1),
		want: http.StatusUnprocessableEntity,
	})
	approvalCall(ctx, t, env, approvalStep{
		step: "render a bad operation", method: http.MethodGet, path: unit + "/rendered?operation=deploy",
		want: http.StatusBadRequest,
	})

	for _, path := range []string{namespace, unit} {
		status, body, err := env.env.ServerAPIStatus(ctx, http.MethodPut, path, proxyRulesNamespaceDoc)
		if err != nil {
			t.Fatalf("save %s with a token: %v", path, err)
		}

		if status != http.StatusForbidden || !strings.Contains(body, "recent sign-in") {
			t.Fatalf("save %s with a token answers %d (%s), want 403 that asks for a recent sign-in", path, status, body)
		}
	}

	body := approvalCall(ctx, t, env, approvalStep{
		step: "read the namespace set after the refusals", method: http.MethodGet, path: namespace,
		want: http.StatusOK,
	})
	wantBodyHas(t, "read the namespace set after the refusals", body, `"version":1`, `"id":"status"`)
}
