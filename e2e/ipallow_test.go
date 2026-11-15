//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// ipAllowlistSource is the local address inside the list of the locked
// namespace. The default source 127.0.0.1 stays outside it.
const ipAllowlistSource = "127.0.0.2"

// TestIPAllowlists locks one namespace to one source address and proves the
// gate: a source outside the list gets 403 without the list in the body, a
// source inside it passes, another namespace is not affected, and deleting
// the rule opens the namespace again. It runs on each store with IP
// allowlists applied to the bootstrap admin token too.
func TestIPAllowlists(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

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
			runIPAllowlistOnStore(t, cli, db)
		})
	}
}

// runIPAllowlistOnStore runs the IP allowlist steps on one store.
func runIPAllowlistOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithIPAllowlistsAndSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	checkIPAllowlistCreateLocked(ctx, t, env)
	unitID := checkIPAllowlistCreateUnit(ctx, t, env)
	checkIPAllowlistEmpty(ctx, t, env)
	checkIPAllowlistSet(ctx, t, env)
	checkIPAllowlistRefusesOutside(ctx, t, env, unitID)
	t.Run("access denied code", func(t *testing.T) {
		checkAccessDeniedCode(ctx, t, env)
	})
	checkIPAllowlistAdmitsInside(ctx, t, env)
	checkIPAllowlistLeavesOpenAlone(ctx, t, env)
	checkIPAllowlistDelete(ctx, t, env)
}

// ipAllowlistStep is one installation API request of the IP allowlist steps
// and the status it must answer.
type ipAllowlistStep struct {
	step   string
	method string
	path   string
	body   string
	want   int
}

// ipAllowlistCall sends one installation API request from the default source
// and fails unless it answers s.want. It answers the body.
func ipAllowlistCall(ctx context.Context, t *testing.T, env *e2e.Env, s ipAllowlistStep) string {
	t.Helper()

	status, resp := freshServerCall(ctx, t, env, principalStep{s.method, s.path, s.body, s.want})
	if status != s.want {
		t.Fatalf("%s: %s %s answers %d, want %d (%s)", s.step, s.method, s.path, status, s.want, resp)
	}

	return resp
}

// ipAllowlistCallFrom sends one installation API request from source and
// fails unless it answers s.want.
func ipAllowlistCallFrom(
	ctx context.Context, t *testing.T, env *e2e.Env, source string, s ipAllowlistStep,
) {
	t.Helper()

	var (
		status int
		resp   string
	)

	if needsFreshAuth(s.method, s.path) {
		status, resp = env.FreshAdminAPIStatusFrom(
			ctx, t, source, e2e.SessionRequest{Method: s.method, Path: s.path, Body: s.body},
		)
	} else {
		var err error

		status, resp, err = env.ServerAPIStatusFrom(ctx, source, env.TokenA(), s.method, s.path, s.body)
		if err != nil {
			t.Fatalf("%s: %s %s from %s: %v", s.step, s.method, s.path, source, err)
		}
	}

	if status != s.want {
		t.Fatalf("%s: %s %s from %s answers %d, want %d (%s)",
			s.step, s.method, s.path, source, status, s.want, resp)
	}
}

// checkIPAllowlistCreateLocked creates the namespace that the later steps lock.
func checkIPAllowlistCreateLocked(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "create the locked namespace",
		method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"locked"}`,
		want: http.StatusCreated,
	})
}

// ipAllowlistAgent is the User-Agent of the workspace request that makes the
// unit of the locked namespace.
const ipAllowlistAgent = "opendustpan-e2e-ipallowlist"

// ipAllowlistUnitList is the part of the unit list that the setup reads.
type ipAllowlistUnitList struct {
	Units []ipAllowlistUnit `json:"units"`
}

// ipAllowlistUnit is one unit of the unit list.
type ipAllowlistUnit struct {
	UnitID string `json:"unitID"`
	Name   string `json:"name"`
}

// checkIPAllowlistCreateUnit makes a unit in the locked namespace before the
// list is set, so that the steps after it can read the three unit surfaces.
// It answers the unit ID.
func checkIPAllowlistCreateUnit(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	env.SeedCatchAllPlacement(ctx, t, "locked")
	env.APIStep(ctx, t, env.AdminToken(), e2e.APIStepSpec{
		UserAgent: ipAllowlistAgent, Check: nil, Name: "create the locked unit", Method: http.MethodPost,
		Path: "/api/v2/organizations/locked/workspaces",
		Body: `{"data":{"type":"workspaces","attributes":{"name":"locked"}}}`,
		Want: http.StatusCreated, Scheme: "",
	})

	status, body, err := env.ServerAPIStatusAs(
		ctx, env.AdminToken(), http.MethodGet, "/api/dustpan/v1/namespaces/locked/units", "",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("list the units of the locked namespace = %d, %v: %s", status, err, body)
	}

	var list ipAllowlistUnitList

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode the unit list: %v: %s", err, body)
	}

	for _, unit := range list.Units {
		if unit.Name == "locked" {
			return unit.UnitID
		}
	}

	t.Fatalf("the unit list holds no unit locked: %s", body)

	return ""
}

// checkIPAllowlistEmpty reads the list of the locked namespace before any
// rule: the feature is on and the namespace has not opted in.
func checkIPAllowlistEmpty(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	body := ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "read the empty allowlist",
		method: http.MethodGet, path: "/api/server/namespaces/locked/ip-allowlist", body: "",
		want: http.StatusOK,
	})

	if !strings.Contains(body, `"enabled":true`) {
		t.Fatalf("read the empty allowlist: body %s has no \"enabled\":true", body)
	}

	if !strings.Contains(body, `"optedIn":false`) {
		t.Fatalf("read the empty allowlist: body %s has no \"optedIn\":false", body)
	}
}

// checkIPAllowlistSet writes the list of the locked namespace. No rule
// exists yet, so the bootstrap admin writes from the default source.
func checkIPAllowlistSet(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "set the allowlist",
		method: http.MethodPut, path: "/api/server/namespaces/locked/ip-allowlist",
		body: `{"cidrs":["127.0.0.2/32"]}`,
		want: http.StatusOK,
	})
}

// checkIPAllowlistRefusesOutside reads the locked namespace and the three
// unit surfaces from the default source: each answer is 403 and never names
// the list.
func checkIPAllowlistRefusesOutside(ctx context.Context, t *testing.T, env *e2e.Env, unitID string) {
	t.Helper()

	unitBase := "/api/dustpan/v1/namespaces/locked/units/" + unitID

	for _, step := range []ipAllowlistStep{
		{
			step:   "read the locked namespace from outside",
			method: http.MethodGet, path: "/api/server/namespaces/locked/override-switches", body: "",
			want: http.StatusForbidden,
		},
		{
			step:   "read the triggers of the locked unit from outside",
			method: http.MethodGet, path: unitBase + "/triggers", body: "",
			want: http.StatusForbidden,
		},
		{
			step:   "read the context of the locked unit from outside",
			method: http.MethodGet, path: unitBase + "/context", body: "",
			want: http.StatusForbidden,
		},
		{
			step:   "read the settings of the locked unit from outside",
			method: http.MethodGet, path: unitBase + "/settings", body: "",
			want: http.StatusForbidden,
		},
	} {
		body := ipAllowlistCall(ctx, t, env, step)

		if strings.Contains(body, ipAllowlistSource) {
			t.Fatalf("%s: body %s leaks the list", step.step, body)
		}

		checkForbiddenAnswer(t, step.step, body, forbiddenAnswer{
			code: "access.ip-not-allowed", message: "forbidden", args: nil,
		})
	}
}

// forbiddenAnswer is the 403 answer that checkForbiddenAnswer expects: the
// machine code, the message and the arguments.
type forbiddenAnswer struct {
	code    string
	message string
	args    map[string]any
}

// checkForbiddenAnswer fails unless body is the 403 answer want.
func checkForbiddenAnswer(t *testing.T, step, body string, want forbiddenAnswer) {
	t.Helper()

	var got struct {
		Code      int            `json:"code"`
		Message   string         `json:"message"`
		ErrorCode string         `json:"errorCode"`
		ErrorArgs map[string]any `json:"errorArgs"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%s: body %s is not JSON: %v", step, body, err)
	}

	if got.Code != http.StatusForbidden || got.Message != want.message || got.ErrorCode != want.code {
		t.Fatalf("%s: body %s, want code 403, message %q and errorCode %q", step, body, want.message, want.code)
	}

	if !reflect.DeepEqual(got.ErrorArgs, want.args) {
		t.Fatalf("%s: body %s, want errorArgs %v", step, body, want.args)
	}
}

// checkAccessDeniedCode sends a request of a caller that holds no grant for
// it. The answer is 403 with the code access.denied, the missing action and
// the kind of the object, and no part of the object.
func checkAccessDeniedCode(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	token, ok := env.MemberToken()
	if !ok {
		t.Skip("the binary server knows only the bootstrap admin tokens, so no caller without a grant exists")
	}

	const name = "secret-object-7f3"

	status, body, err := env.ServerAPIStatusAs(
		ctx,
		token,
		http.MethodPost,
		"/api/server/namespaces",
		`{"name":"`+name+`"}`,
	)
	if err != nil {
		t.Fatalf("create a namespace without a grant: %v", err)
	}

	if status != http.StatusForbidden {
		t.Fatalf("create a namespace without a grant answers %d, want %d (%s)", status, http.StatusForbidden, body)
	}

	checkForbiddenAnswer(t, "create a namespace without a grant", body, forbiddenAnswer{
		code:    "access.denied",
		message: "forbidden: you need the action server:create-namespace on this server",
		args:    map[string]any{"action": "server:create-namespace", "objectKind": "server"},
	})

	if strings.Contains(body, name) {
		t.Fatalf("create a namespace without a grant names the object: %s", body)
	}
}

// checkIPAllowlistAdmitsInside reads the locked namespace from a source
// inside the list.
func checkIPAllowlistAdmitsInside(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCallFrom(ctx, t, env, ipAllowlistSource, ipAllowlistStep{
		step:   "read the locked namespace from inside",
		method: http.MethodGet, path: "/api/server/namespaces/locked/override-switches", body: "",
		want: http.StatusOK,
	})
}

// checkIPAllowlistLeavesOpenAlone creates a second namespace and reads it
// from the default source: its requests still pass.
func checkIPAllowlistLeavesOpenAlone(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "create the open namespace",
		method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"open"}`,
		want: http.StatusCreated,
	})
	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "read the open namespace from the default source",
		method: http.MethodGet, path: "/api/server/namespaces/open/override-switches", body: "",
		want: http.StatusOK,
	})
}

// checkIPAllowlistDelete deletes the rule from a source inside the list and
// reads the locked namespace from the default source again: it passes now.
func checkIPAllowlistDelete(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCallFrom(ctx, t, env, ipAllowlistSource, ipAllowlistStep{
		step:   "delete the allowlist",
		method: http.MethodDelete, path: "/api/server/namespaces/locked/ip-allowlist", body: "",
		want: http.StatusNoContent,
	})
	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "read the unlocked namespace from the default source",
		method: http.MethodGet, path: "/api/server/namespaces/locked/override-switches", body: "",
		want: http.StatusOK,
	})
}
