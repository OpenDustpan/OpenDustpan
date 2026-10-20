//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// approvalNamespaceBase is the path of the namespace that the steps use.
const approvalNamespaceBase = "/api/dustpan/v1/namespaces/approvals"

// TestApprovalRules proves the approval default and the approval rule routes
// of a namespace through the real server on each store: no CLI sets an
// approval default, so the harness calls the routes. The steps set a default,
// read it, replace it, list and delete a rule, and delete the default again.
// Each refusal answers its status and changes nothing.
func TestApprovalRules(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runApprovalRulesOnStore(t, cli, db)
		})
	}
}

func runApprovalRulesOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)

	checkApprovalCreateNamespace(ctx, t, env)
	checkApprovalDefaultLifecycle(ctx, t, env)
	checkApprovalRuleLifecycle(ctx, t, env)
	checkApprovalRefusals(ctx, t, env)
	checkApprovalNeedsSignIn(ctx, t, env)
}

// approvalStep is one installation API request and the status it must answer.
type approvalStep struct {
	step   string
	method string
	path   string
	body   string
	want   int
}

// approvalCall sends s and fails unless it answers s.want. It answers the body.
func approvalCall(ctx context.Context, t *testing.T, env *e2e.Env, s approvalStep) string {
	t.Helper()

	status, resp, err := env.ServerAPIStatus(ctx, s.method, s.path, s.body)
	if err != nil {
		t.Fatalf("%s: %s %s: %v", s.step, s.method, s.path, err)
	}

	if status != s.want {
		t.Fatalf("%s: %s %s answers %d, want %d (%s)", s.step, s.method, s.path, status, s.want, resp)
	}

	return resp
}

func wantBodyHas(t *testing.T, step, body string, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if !strings.Contains(body, part) {
			t.Fatalf("%s: body %s has no %s", step, body, part)
		}
	}
}

func checkApprovalCreateNamespace(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	approvalCall(ctx, t, env, approvalStep{
		step: "create the namespace", method: http.MethodPost, path: "/api/server/namespaces",
		body: `{"name":"approvals"}`, want: http.StatusCreated,
	})
}

// checkApprovalDefaultLifecycle sets, reads, replaces and deletes the default.
func checkApprovalDefaultLifecycle(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	path := approvalNamespaceBase + "/approval-default"

	approvalCall(
		ctx,
		t,
		env,
		approvalStep{
			step:   "read before any default",
			method: http.MethodGet,
			path:   path,
			body:   "",
			want:   http.StatusNotFound,
		},
	)

	body := approvalCall(ctx, t, env, approvalStep{
		step: "set the default", method: http.MethodPut, path: path,
		body: `{"count":2,"approvers":{"groups":["platform"]}}`, want: http.StatusOK,
	})
	wantBodyHas(t, "set the default", body, `"count":2`, `"version":1`, `"platform"`)

	body = approvalCall(
		ctx,
		t,
		env,
		approvalStep{step: "read the default", method: http.MethodGet, path: path, body: "", want: http.StatusOK},
	)
	wantBodyHas(t, "read the default", body, `"count":2`, `"platform"`)

	body = approvalCall(ctx, t, env, approvalStep{
		step: "replace the default", method: http.MethodPut, path: path,
		body: `{"count":1,"approvers":{}}`, want: http.StatusOK,
	})
	wantBodyHas(t, "replace the default", body, `"count":1`, `"version":2`)

	approvalCall(
		ctx,
		t,
		env,
		approvalStep{
			step:   "delete the default",
			method: http.MethodDelete,
			path:   path,
			body:   "",
			want:   http.StatusNoContent,
		},
	)
	approvalCall(
		ctx,
		t,
		env,
		approvalStep{
			step:   "read after the delete",
			method: http.MethodGet,
			path:   path,
			body:   "",
			want:   http.StatusNotFound,
		},
	)
}

// checkApprovalRuleLifecycle creates, lists, reads and deletes a rule.
func checkApprovalRuleLifecycle(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	list := approvalNamespaceBase + "/approval-rules"
	item := list + "/platform-review"

	body := approvalCall(
		ctx,
		t,
		env,
		approvalStep{step: "list before any rule", method: http.MethodGet, path: list, body: "", want: http.StatusOK},
	)
	wantBodyHas(t, "list before any rule", body, `"rules":[]`)

	body = approvalCall(ctx, t, env, approvalStep{
		step: "create the rule", method: http.MethodPut, path: item,
		body: `{"count":2,"approvers":{"groups":["platform"]},"timeoutSeconds":86400}`, want: http.StatusOK,
	})
	wantBodyHas(t, "create the rule", body, `"id":"platform-review"`, `"count":2`, `"timeoutSeconds":86400`)

	body = approvalCall(
		ctx,
		t,
		env,
		approvalStep{step: "list the rule", method: http.MethodGet, path: list, body: "", want: http.StatusOK},
	)
	wantBodyHas(t, "list the rule", body, `"platform-review"`)

	approvalCall(
		ctx,
		t,
		env,
		approvalStep{step: "read the rule", method: http.MethodGet, path: item, body: "", want: http.StatusOK},
	)
	approvalCall(
		ctx,
		t,
		env,
		approvalStep{
			step:   "delete the rule",
			method: http.MethodDelete,
			path:   item,
			body:   "",
			want:   http.StatusNoContent,
		},
	)
	approvalCall(
		ctx,
		t,
		env,
		approvalStep{
			step:   "read the deleted rule",
			method: http.MethodGet,
			path:   item,
			body:   "",
			want:   http.StatusNotFound,
		},
	)
}

// checkApprovalRefusals sends invalid requests and proves each one stores nothing.
func checkApprovalRefusals(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	def := approvalNamespaceBase + "/approval-default"
	rule := approvalNamespaceBase + "/approval-rules/ops"

	for _, s := range []approvalStep{
		{step: "default with count 0", method: http.MethodPut, path: def, body: `{"count":0,"approvers":{}}`, want: http.StatusUnprocessableEntity},
		{step: "default with count 11", method: http.MethodPut, path: def, body: `{"count":11,"approvers":{}}`, want: http.StatusUnprocessableEntity},
		{step: "default with an unknown field", method: http.MethodPut, path: def, body: `{"count":1,"approvers":{},"unless":"true"}`, want: http.StatusBadRequest},
		{step: "rule with no approver", method: http.MethodPut, path: rule, body: `{"count":1,"approvers":{}}`, want: http.StatusUnprocessableEntity},
		{step: "rule with the reserved ID", method: http.MethodPut, path: approvalNamespaceBase + "/approval-rules/default", body: `{"count":1,"approvers":{"groups":["a"]}}`, want: http.StatusUnprocessableEntity},
		{step: "delete when unset", method: http.MethodDelete, path: def, body: "", want: http.StatusNotFound},
	} {
		approvalCall(ctx, t, env, s)
	}

	approvalCall(
		ctx,
		t,
		env,
		approvalStep{
			step:   "default stays unset",
			method: http.MethodGet,
			path:   def,
			body:   "",
			want:   http.StatusNotFound,
		},
	)
	approvalCall(
		ctx,
		t,
		env,
		approvalStep{step: "rule stays unset", method: http.MethodGet, path: rule, body: "", want: http.StatusNotFound},
	)
}

// checkApprovalNeedsSignIn proves that a request with no credential is refused.
func checkApprovalNeedsSignIn(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		status, body, err := env.ServerAPIStatusBare(ctx, method, approvalNamespaceBase+"/approval-default")
		if err != nil {
			t.Fatalf("%s without a credential: %v", method, err)
		}

		if status != http.StatusUnauthorized {
			t.Fatalf("%s without a credential answers %d, want 401 (%s)", method, status, body)
		}
	}
}
