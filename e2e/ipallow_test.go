//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
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

	tofuVersion := e2e.OpenTofu().Single()
	if tofuVersion == "" {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, version, err)
	}

	tofu, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersion, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runIPAllowlistOnStore(t, cli, tofu, db)
		})
	}
}

// runIPAllowlistOnStore runs the IP allowlist steps on one store.
func runIPAllowlistOnStore(t *testing.T, cli, tofu e2e.Install, store e2e.Store) {
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
	checkIPAllowlistListsLeaveOut(ctx, t, env)
	checkIPAllowlistDelete(ctx, t, env)
	checkIPAllowlistInstallationList(ctx, t, env, tofu)
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
			method: http.MethodGet, path: "/api/dustpan/v1/namespaces/locked/override-switches", body: "",
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
		method: http.MethodGet, path: "/api/dustpan/v1/namespaces/locked/override-switches", body: "",
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
		method: http.MethodGet, path: "/api/dustpan/v1/namespaces/open/override-switches", body: "",
		want: http.StatusOK,
	})
}

// checkIPAllowlistListsLeaveOut lists the namespaces and the caller access
// of the administrator token, whose scope covers every object, from the
// default source and from the source inside the list. From the default
// source, the namespace list leaves out the locked namespace and the caller
// access names it in excluded.namespaces. From inside, both lists hold it.
func checkIPAllowlistListsLeaveOut(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	cases := []struct {
		source       string
		wantLocked   bool
		wantExcluded []string
	}{
		{source: "", wantLocked: false, wantExcluded: []string{"locked"}},
		{source: ipAllowlistSource, wantLocked: true, wantExcluded: []string{}},
	}

	for _, tc := range cases {
		names := listNamespaceNamesFrom(ctx, t, env, tc.source)
		if slices.Contains(names, "locked") != tc.wantLocked || !slices.Contains(names, "open") {
			t.Fatalf("namespaces from %q = %v, want locked listed %t and open listed", tc.source, names, tc.wantLocked)
		}

		var page e2eAccessObjects

		body := serverGetFrom(ctx, t, env, tc.source, "/api/dustpan/v1/me/access/objects?type=namespace")
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("decode caller access %s: %v", body, err)
		}

		if !page.All || !slices.Equal(page.Excluded.Namespaces, tc.wantExcluded) {
			t.Fatalf("caller access from %q = %s, want all with excluded %v", tc.source, body, tc.wantExcluded)
		}
	}
}

// listNamespaceNamesFrom answers the names of the first page of the
// namespace list from source, or from the default source when source is
// empty.
func listNamespaceNamesFrom(ctx context.Context, t *testing.T, env *e2e.Env, source string) []string {
	t.Helper()

	var page e2eNamespaceList

	body := serverGetFrom(ctx, t, env, source, "/api/server/namespaces?limit=1000")
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode namespace list %s: %v", body, err)
	}

	names := make([]string, 0, len(page.Namespaces))
	for i := range page.Namespaces {
		names = append(names, page.Namespaces[i].Name)
	}

	return names
}

// serverGetFrom sends one GET with the administrator token from source, or
// from the default source when source is empty, and fails unless it answers
// 200. It answers the body.
func serverGetFrom(ctx context.Context, t *testing.T, env *e2e.Env, source, path string) string {
	t.Helper()

	if source == "" {
		return ipAllowlistCall(ctx, t, env, ipAllowlistStep{
			step: "read " + path, method: http.MethodGet, path: path, body: "", want: http.StatusOK,
		})
	}

	status, body, err := env.ServerAPIStatusFrom(ctx, source, env.TokenA(), http.MethodGet, path, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s from %s answers %d (%s): %v", path, source, status, body, err)
	}

	return body
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
		method: http.MethodGet, path: "/api/dustpan/v1/namespaces/locked/override-switches", body: "",
		want: http.StatusOK,
	})
}

// checkIPAllowlistInstallationList proves the installation list with the real
// Pulumi CLI and OpenTofu. It first allows every address, so both CLIs run
// with an access token, then narrows the list to the source outside the
// default address. The CLIs then fail, the units route answers 200 from the
// allowed source and 403 with the code from the default source.
func checkIPAllowlistInstallationList(ctx context.Context, t *testing.T, env *e2e.Env, tofu e2e.Install) {
	t.Helper()

	patchInstallationList(ctx, t, env, `{"ipAllowlist.enabled":true,"ipAllowlist.installation":"0.0.0.0/0"}`)

	if runs := runCLIsWithToken(ctx, t, env, tofu); !runs.pulumi || !runs.tofu {
		t.Fatalf("CLIs inside the installation list: %+v, want both to pass", runs)
	}

	patchInstallationList(ctx, t, env, `{"ipAllowlist.installation":"`+ipAllowlistSource+`/32"}`)

	if runs := runCLIsWithToken(ctx, t, env, tofu); runs.pulumi || runs.tofu {
		t.Fatalf("CLIs outside the installation list: %+v, want both to fail", runs)
	}

	checkUnitsBehindInstallationList(ctx, t, env)
}

// patchInstallationList sends body as a merge patch of the installation
// settings with a fresh console session and wants 200.
func patchInstallationList(ctx context.Context, t *testing.T, env *e2e.Env, body string) {
	t.Helper()

	env.PrepareFreshAdmin(ctx, t)

	resp, _ := settingsRead(ctx, t, env, settingsRoute)

	// A change that widens the installation list needs a fresh sign-in, which
	// an access token never gives.
	status, answer := env.FreshAdminMergePatch(ctx, t, settingsRoute, resp.Header.Get("ETag"), body)
	if status != http.StatusOK {
		t.Fatalf("PATCH %s answers %d %s, want 200", body, status, answer)
	}
}

// cliRuns is the result of the CLI runs: whether each CLI exited with 0.
type cliRuns struct {
	pulumi bool
	tofu   bool
}

// runCLIsWithToken runs the Pulumi CLI and OpenTofu with an access token from
// the default address and answers whether each one passed.
func runCLIsWithToken(ctx context.Context, t *testing.T, env *e2e.Env, tofu e2e.Install) cliRuns {
	t.Helper()

	pulumiRun, err := env.Run(ctx, []string{"PULUMI_ACCESS_TOKEN=" + env.TokenA()},
		"stack", "ls", "--json", "--all")
	if err != nil {
		t.Fatalf("pulumi stack ls: %v", err)
	}

	tofuRun, err := env.RunTofuWithToken(ctx, t, tofu, env.TokenA(), "init", "-input=false", "-no-color")
	if err != nil {
		t.Fatalf("tofu init: %v", err)
	}

	return cliRuns{pulumi: pulumiRun.ExitCode == 0, tofu: tofuRun.ExitCode == 0}
}

// checkUnitsBehindInstallationList proves that the units route answers 200
// from the allowed source and 403 access.ip-not-allowed, with the scope
// installation, from the default address.
func checkUnitsBehindInstallationList(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	unitsPath := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/units"

	status, body, err := env.ServerAPIStatusFrom(ctx, ipAllowlistSource, env.TokenA(),
		http.MethodGet, unitsPath, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("the units route from %s answers %d, %v: %s", ipAllowlistSource, status, err, body)
	}

	status, body, err = env.ServerAPIStatus(ctx, http.MethodGet, unitsPath, "")
	if err != nil {
		t.Fatalf("the units route from the default source: %v", err)
	}

	if status != http.StatusForbidden || !strings.Contains(body, `"errorCode":"access.ip-not-allowed"`) ||
		!strings.Contains(body, "of the installation") {
		t.Fatalf("the units route from the default source answers %d (%s), want 403 access.ip-not-allowed "+
			"for the installation", status, body)
	}
}
