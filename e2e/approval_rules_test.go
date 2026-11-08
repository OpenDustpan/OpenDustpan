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

	version := e2e.Pulumi().Single()
	if version == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, version, err)
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
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	checkApprovalCreateNamespace(ctx, t, approvalEnv{env: env, person: ""})
	approvalGroups(ctx, t, approvalEnv{env: env, person: ""})

	ae := approvalAdmin(ctx, t, env)

	checkApprovalDefaultLifecycle(ctx, t, ae)
	checkApprovalRuleLifecycle(ctx, t, ae)
	checkApprovalRefusals(ctx, t, ae)
	checkApprovalNeedsSignIn(ctx, t, ae)
	checkApprovalTokenWrites(ctx, t, ae)
}

// approvalEnv is the env with the person who administers the namespace. A
// write signs this person in, because an access token never gives fresh
// authentication. A read uses the access token of the env.
type approvalEnv struct {
	env    *e2e.Env
	person string
}

// approvalAdmin makes a person with the admin role on the namespace.
func approvalAdmin(ctx context.Context, t *testing.T, env *e2e.Env) approvalEnv {
	t.Helper()

	var p e2ePrincipal

	body := approvalCall(ctx, t, approvalEnv{env: env, person: ""}, approvalStep{
		step: "make the person", method: http.MethodPost, path: "/api/server/principals",
		body: `{"subject":"` + e2e.SignInSubject("approval-admin") + `","kind":"person"}`, want: http.StatusCreated,
	})

	if err := json.Unmarshal([]byte(body), &p); err != nil || p.ID == "" {
		t.Fatalf("decode the person: %v: %s", err, body)
	}

	approvalCall(ctx, t, approvalEnv{env: env, person: ""}, approvalStep{
		step: "grant admin on the namespace", method: http.MethodPost, path: "/api/server/namespaces/approvals/grants",
		body: `{"subject":"principal:` + p.ID + `","role":"admin"}`, want: http.StatusCreated,
	})

	return approvalEnv{env: env, person: p.ID}
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
func approvalCall(ctx context.Context, t *testing.T, env approvalEnv, s approvalStep) string {
	t.Helper()

	var (
		status int
		resp   string
	)

	if env.person != "" && (s.method == http.MethodPut || s.method == http.MethodDelete) {
		status, resp = env.env.SessionAPIStatus(
			ctx, t, env.person, e2e.SessionRequest{Method: s.method, Path: s.path, Body: s.body},
		)
	} else {
		status, resp = freshServerCall(ctx, t, env.env, principalStep{s.method, s.path, s.body, s.want})
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

// approvalGroups makes the groups that the approvers of the steps name: platform
// with two members and a with one. The server refuses a group that does not
// exist and a count above the members.
func approvalGroups(ctx context.Context, t *testing.T, env approvalEnv) {
	t.Helper()

	for name, subjects := range map[string][]string{"platform": {"approver-one", "approver-two"}, "a": {"approver-one"}} {
		var group e2eGroup

		decodeApprovalBody(t, approvalCall(ctx, t, env, approvalStep{
			step: "make the group " + name, method: http.MethodPost, path: "/api/server/groups",
			body: `{"name":"` + name + `"}`, want: http.StatusCreated,
		}), &group)

		for _, subject := range subjects {
			var member e2ePrincipal

			resp := approvalCall(ctx, t, env, approvalStep{
				step: "make the approver " + subject, method: http.MethodPost, path: "/api/server/principals",
				body: `{"subject":"` + name + "-" + subject + `","kind":"person"}`, want: http.StatusCreated,
			})
			decodeApprovalBody(t, resp, &member)

			approvalCall(ctx, t, env, approvalStep{
				step: "add " + subject + " to " + name, method: http.MethodPost,
				path: "/api/server/groups/" + group.ID + "/members",
				body: `{"principalID":"` + member.ID + `"}`, want: http.StatusCreated,
			})
		}
	}
}

// decodeApprovalBody decodes the JSON body of an answer into v.
func decodeApprovalBody(t *testing.T, body string, v any) {
	t.Helper()

	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}

func checkApprovalCreateNamespace(ctx context.Context, t *testing.T, env approvalEnv) {
	t.Helper()

	approvalCall(ctx, t, env, approvalStep{
		step: "create the namespace", method: http.MethodPost, path: "/api/server/namespaces",
		body: `{"name":"approvals"}`, want: http.StatusCreated,
	})
}

// checkApprovalDefaultLifecycle sets, reads, replaces and deletes the default.
func checkApprovalDefaultLifecycle(ctx context.Context, t *testing.T, env approvalEnv) {
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
func checkApprovalRuleLifecycle(ctx context.Context, t *testing.T, env approvalEnv) {
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
func checkApprovalRefusals(ctx context.Context, t *testing.T, env approvalEnv) {
	t.Helper()

	def := approvalNamespaceBase + "/approval-default"
	rule := approvalNamespaceBase + "/approval-rules/ops"

	for _, s := range []approvalStep{
		{step: "default with count 0", method: http.MethodPut, path: def, body: `{"count":0,"approvers":{}}`, want: http.StatusUnprocessableEntity},
		{step: "default with count 11", method: http.MethodPut, path: def, body: `{"count":11,"approvers":{}}`, want: http.StatusUnprocessableEntity},
		{step: "default with an unknown field", method: http.MethodPut, path: def, body: `{"count":1,"approvers":{},"unless":"true"}`, want: http.StatusBadRequest},
		{step: "rule with no approver", method: http.MethodPut, path: rule, body: `{"count":1,"approvers":{}}`, want: http.StatusUnprocessableEntity},
		{step: "rule with the reserved ID", method: http.MethodPut, path: approvalNamespaceBase + "/approval-rules/default", body: `{"count":1,"approvers":{"groups":["a"]}}`, want: http.StatusUnprocessableEntity},
		{step: "default with a group that does not exist", method: http.MethodPut, path: def, body: `{"count":1,"approvers":{"groups":["nogroup"]}}`, want: http.StatusUnprocessableEntity},
		{step: "default with a group name that is not valid", method: http.MethodPut, path: def, body: `{"count":1,"approvers":{"groups":["<b>x</b>"]}}`, want: http.StatusUnprocessableEntity},
		{step: "default with a count above the named users", method: http.MethodPut, path: def, body: `{"count":10,"approvers":{"users":["approval-user"]}}`, want: http.StatusUnprocessableEntity},
		{step: "default with a count above the group members", method: http.MethodPut, path: def, body: `{"count":3,"approvers":{"groups":["platform"]}}`, want: http.StatusUnprocessableEntity},
		{step: "rule with a group that does not exist", method: http.MethodPut, path: rule, body: `{"count":1,"approvers":{"groups":["nogroup"]}}`, want: http.StatusUnprocessableEntity},
		{step: "rule with a count above the named users", method: http.MethodPut, path: rule, body: `{"count":2,"approvers":{"users":["approval-user"]}}`, want: http.StatusUnprocessableEntity},
		{step: "delete when unset", method: http.MethodDelete, path: def, body: "", want: http.StatusNoContent},
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
func checkApprovalNeedsSignIn(ctx context.Context, t *testing.T, env approvalEnv) {
	t.Helper()

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		status, body, err := env.env.ServerAPIStatusBare(ctx, method, approvalNamespaceBase+"/approval-default")
		if err != nil {
			t.Fatalf("%s without a credential: %v", method, err)
		}

		if status != http.StatusUnauthorized {
			t.Fatalf("%s without a credential answers %d, want 401 (%s)", method, status, body)
		}
	}
}

// checkApprovalTokenWrites proves that an access token never gives fresh
// authentication: a set or a delete with the token of the env answers 403
// and changes nothing, and a read with the token still works.
func checkApprovalTokenWrites(ctx context.Context, t *testing.T, env approvalEnv) {
	t.Helper()

	def := approvalNamespaceBase + "/approval-default"
	rule := approvalNamespaceBase + "/approval-rules/ops"

	approvalCall(ctx, t, env, approvalStep{
		step: "set the default with the session", method: http.MethodPut, path: def,
		body: `{"count":2,"approvers":{"groups":["platform"]}}`, want: http.StatusOK,
	})

	for _, s := range []approvalStep{
		{step: "set the default with a token", method: http.MethodPut, path: def, body: `{"count":3,"approvers":{}}`, want: 0},
		{step: "delete the default with a token", method: http.MethodDelete, path: def, body: "", want: 0},
		{step: "set a rule with a token", method: http.MethodPut, path: rule, body: `{"count":1,"approvers":{"groups":["a"]}}`, want: 0},
		{step: "delete a rule with a token", method: http.MethodDelete, path: rule, body: "", want: 0},
	} {
		status, body, err := env.env.ServerAPIStatus(ctx, s.method, s.path, s.body)
		if err != nil {
			t.Fatalf("%s: %v", s.step, err)
		}

		if status != http.StatusForbidden || !strings.Contains(body, "recent sign-in") {
			t.Fatalf("%s answers %d (%s), want 403 that asks for a recent sign-in", s.step, status, body)
		}
	}

	body := approvalCall(ctx, t, env, approvalStep{
		step: "read the default with a token", method: http.MethodGet, path: def, body: "", want: http.StatusOK,
	})
	wantBodyHas(t, "read the default with a token", body, `"count":2`, `"version":1`)

	approvalCall(ctx, t, env, approvalStep{
		step: "read a rule with a token", method: http.MethodGet, path: rule, body: "", want: http.StatusNotFound,
	})
	approvalCall(ctx, t, env, approvalStep{
		step: "delete the default with the session", method: http.MethodDelete, path: def, body: "",
		want: http.StatusNoContent,
	})
}
