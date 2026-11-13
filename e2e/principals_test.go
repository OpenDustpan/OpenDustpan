//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// e2ePrincipal and e2eGroup decode the principal and group wire forms.
type e2ePrincipal struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	GroupCount  int64  `json:"groupCount"`
}

type e2eGroup struct {
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	Description              string `json:"description"`
	ClosedToIdentityProvider bool   `json:"closedToIdentityProvider"`
	// Protected is a pointer, so that a missing field is not a false flag.
	Protected *bool `json:"protected"`
}

// TestPrincipalsAndGroups drives every principal and group route of the
// installation API (ADR 52) through the real binary, on each store. No CLI
// calls these routes, so the harness calls them with the bootstrap admin
// token, and the steps prove that the token keeps working (ADR 6).
func TestPrincipalsAndGroups(t *testing.T) {
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
			runPrincipalsOnStore(t, cli, db)
		})
	}
}

// runPrincipalsOnStore runs the principal and group steps on one store.
func runPrincipalsOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	var alice, deploy e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","displayName":"Alice","kind":"person"}`, http.StatusCreated,
	}, &alice)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"ci-deploy","kind":"service"}`, http.StatusCreated,
	}, &deploy)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","kind":"person"}`, http.StatusConflict,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"admin","kind":"person"}`, http.StatusBadRequest,
	}, nil)

	if !strings.HasPrefix(alice.ID, "prn-") || alice.Kind != "person" {
		t.Fatalf("created principal = %+v, want a person with a prn- ID", alice)
	}

	var group e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups",
		`{"name":"ops","description":"Runs the platform"}`, http.StatusCreated,
	}, &group)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups", `{"name":"OPS"}`,
		http.StatusConflict,
	}, nil)

	members := "/api/server/groups/" + group.ID + "/members"
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, members, `{"principalID":"` + alice.ID + `"}`,
		http.StatusCreated,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, members, `{"principalID":"` + deploy.ID + `"}`,
		http.StatusCreated,
	}, nil)

	fx := principalFixture{alice: alice, deploy: deploy, group: group}
	checkPrincipalLists(ctx, t, env, fx)
	checkPrincipalMembers(ctx, t, env, fx)
	checkPrincipalChanges(ctx, t, env, fx)
	checkPrincipalPaging(ctx, t, env, fx)

	for _, step := range []principalStep{
		{http.MethodDelete, members + "/" + alice.ID, "", http.StatusNoContent},
		{http.MethodDelete, "/api/server/groups/" + group.ID, "", http.StatusNoContent},
		{http.MethodGet, "/api/server/groups/" + group.ID, "", http.StatusNotFound},
		{http.MethodDelete, "/api/server/principals/" + alice.ID, "", http.StatusNoContent},
		{http.MethodGet, "/api/server/principals/" + alice.ID, "", http.StatusNotFound},
	} {
		principalCall(ctx, t, env, step, nil)
	}

	status, _, err := env.ServerAPIStatusBare(ctx, http.MethodGet, "/api/server/principals")
	if err != nil || status != http.StatusUnauthorized {
		t.Fatalf("list without a token = %d, %v; want 401", status, err)
	}
}

// checkPrincipalPaging reads the principals one at a time with a cursor,
// and with a prefix and a limit that is too large.
func checkPrincipalPaging(ctx context.Context, t *testing.T, env *e2e.Env, fx principalFixture) {
	t.Helper()

	type page struct {
		NextCursor string         `json:"nextCursor"`
		Principals []e2ePrincipal `json:"principals"`
		Total      int64          `json:"total"`
	}

	var first, second page
	principalCall(
		ctx,
		t,
		env,
		principalStep{http.MethodGet, "/api/server/principals?limit=1", "", http.StatusOK},
		&first,
	)

	if len(first.Principals) != 1 || first.Principals[0].ID != fx.alice.ID || first.NextCursor == "" ||
		first.Total != 3 || first.Principals[0].Source != "local" || first.Principals[0].GroupCount != 1 {
		t.Fatalf("first page = %+v, want local alice in 1 group, a cursor and a total of 3", first)
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/principals?limit=1&cursor=" + first.NextCursor, "", http.StatusOK,
	}, &second)

	if len(second.Principals) != 1 || second.Principals[0].ID != fx.deploy.ID || second.NextCursor == "" {
		t.Fatalf("second page = %+v, want ci-deploy and a cursor to the person of the console session", second)
	}

	var prefixed page
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/principals?prefix=ci-", "", http.StatusOK,
	}, &prefixed)

	if len(prefixed.Principals) != 1 || prefixed.Principals[0].ID != fx.deploy.ID {
		t.Fatalf("prefix ci- = %+v, want ci-deploy", prefixed)
	}

	checkPrincipalKind(ctx, t, env, fx)
	checkPageRefusals(ctx, t, env)
}

// checkPrincipalKind filters the principals by kind, with a prefix and a
// page.
func checkPrincipalKind(ctx context.Context, t *testing.T, env *e2e.Env, fx principalFixture) {
	t.Helper()

	type page struct {
		NextCursor string         `json:"nextCursor"`
		Principals []e2ePrincipal `json:"principals"`
		Total      int64          `json:"total"`
	}

	for _, c := range []struct {
		query string
		want  []string
	}{
		{"kind=service", []string{fx.deploy.ID}},
		{"kind=person&prefix=al&limit=1", []string{fx.alice.ID}},
		{"kind=person&prefix=ci-", nil},
		{"kind=workload", nil},
	} {
		var got page
		principalCall(ctx, t, env, principalStep{
			http.MethodGet, "/api/server/principals?" + c.query, "", http.StatusOK,
		}, &got)

		ids := make([]string, 0, len(got.Principals))
		for i := range got.Principals {
			ids = append(ids, got.Principals[i].ID)
		}

		if !slices.Equal(ids, c.want) || got.Total != int64(len(c.want)) || got.NextCursor != "" {
			t.Fatalf("principals with %s = %+v, want %v", c.query, got, c.want)
		}
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/principals?kind=robot", "", http.StatusBadRequest,
	}, nil)
}

// checkPageRefusals reads pages with a bad limit, prefix or cursor: each
// answers 400, on PostgreSQL too, where a text cannot hold a NUL.
func checkPageRefusals(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, bad := range []string{"limit=1001", "limit=0", "prefix=a%00", "cursor=YQA"} {
		principalCall(ctx, t, env, principalStep{
			http.MethodGet, "/api/server/principals?" + bad, "", http.StatusBadRequest,
		}, nil)
	}
}

// principalStep is one installation API request of the principal steps and
// the status it must answer.
type principalStep struct {
	method string
	path   string
	body   string
	want   int
}

// principalFixture holds the records that the steps create.
type principalFixture struct {
	alice  e2ePrincipal
	deploy e2ePrincipal
	group  e2eGroup
}

// e2ePrincipalList, e2eGroupList, e2eMember and e2eMemberList decode the
// lists.
type e2ePrincipalList struct {
	Principals []e2ePrincipal `json:"principals"`
}

type e2eGroupList struct {
	Groups []e2eGroup `json:"groups"`
}

type e2eMember struct {
	Principal e2ePrincipal `json:"principal"`
}

type e2eMemberList struct {
	Members []e2eMember `json:"members"`
}

// checkPrincipalLists reads the principals, one principal, the groups and
// one group.
func checkPrincipalLists(ctx context.Context, t *testing.T, env *e2e.Env, fx principalFixture) {
	t.Helper()

	var list e2ePrincipalList
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/principals", "", http.StatusOK}, &list)

	// The third principal is the administrator of the console session that the
	// membership changes need.
	if len(list.Principals) != 3 || list.Principals[0].ID != fx.alice.ID || list.Principals[1].ID != fx.deploy.ID {
		t.Fatalf("principals = %+v, want alice, ci-deploy and the session administrator", list.Principals)
	}

	var one e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/principals/" + fx.deploy.ID, "",
		http.StatusOK,
	}, &one)

	if one.Subject != "ci-deploy" || one.Kind != "service" || one.DisplayName != "ci-deploy" {
		t.Fatalf("principal = %+v, want the ci-deploy service", one)
	}

	var groups e2eGroupList
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/groups", "", http.StatusOK}, &groups)

	if len(groups.Groups) != 1 || groups.Groups[0].ID != fx.group.ID {
		t.Fatalf("groups = %+v, want ops", groups.Groups)
	}

	var got e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/groups/" + fx.group.ID, "",
		http.StatusOK,
	}, &got)

	if got.Name != "ops" || got.Description != "Runs the platform" {
		t.Fatalf("group = %+v, want ops", got)
	}
}

// checkPrincipalMembers reads the members of the group and the groups of
// one principal.
func checkPrincipalMembers(ctx context.Context, t *testing.T, env *e2e.Env, fx principalFixture) {
	t.Helper()

	var members e2eMemberList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/groups/" + fx.group.ID + "/members", "",
		http.StatusOK,
	}, &members)

	if len(members.Members) != 2 {
		t.Fatalf("members = %+v, want alice and ci-deploy", members.Members)
	}

	var of e2eGroupList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/principals/" + fx.deploy.ID + "/groups", "",
		http.StatusOK,
	}, &of)

	if len(of.Groups) != 1 || of.Groups[0].ID != fx.group.ID {
		t.Fatalf("groups of ci-deploy = %+v, want ops", of.Groups)
	}
}

// checkPrincipalChanges renames a principal and a group, then closes the
// group to the identity provider, reads it back closed, opens it and reads
// it back open.
func checkPrincipalChanges(ctx context.Context, t *testing.T, env *e2e.Env, fx principalFixture) {
	t.Helper()

	var changed e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/principals/" + fx.alice.ID,
		`{"displayName":"Alice A."}`, http.StatusOK,
	}, &changed)

	if changed.DisplayName != "Alice A." || changed.Subject != fx.alice.Subject {
		t.Fatalf("changed principal = %+v, want the new display name", changed)
	}

	var renamed e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/groups/" + fx.group.ID,
		`{"name":"operations"}`, http.StatusOK,
	}, &renamed)

	if renamed.Name != "operations" {
		t.Fatalf("renamed group = %+v, want operations", renamed)
	}

	group := "/api/server/groups/" + fx.group.ID

	var closed e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, group, `{"closedToIdentityProvider":true}`, http.StatusOK,
	}, &closed)

	if !closed.ClosedToIdentityProvider {
		t.Fatalf("closed group = %+v, want the identity-provider switch on", closed)
	}

	var read e2eGroup
	principalCall(ctx, t, env, principalStep{http.MethodGet, group, "", http.StatusOK}, &read)

	if !read.ClosedToIdentityProvider {
		t.Fatalf("read of the closed group = %+v, want the identity-provider switch on", read)
	}

	var open e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, group, `{"closedToIdentityProvider":false}`, http.StatusOK,
	}, &open)

	if open.ClosedToIdentityProvider {
		t.Fatalf("open group = %+v, want the identity-provider switch off", open)
	}

	principalCall(ctx, t, env, principalStep{http.MethodGet, group, "", http.StatusOK}, &open)

	if open.ClosedToIdentityProvider {
		t.Fatalf("read of the open group = %+v, want the identity-provider switch off", open)
	}
}

// freshAuthRule is a write that needs a fresh authentication: the paths
// that match path, with one of the methods.
type freshAuthRule struct {
	path    *regexp.Regexp
	methods []string
}

// freshAuthRules lists the writes that need a fresh authentication: the
// creation and the rotation of an access token, a change of the session
// policy, the token policy, the roles and the grants, a change of a sign-in
// provider, a change of a gate object, the approval of the audit chain and a
// change of an IP allowlist.
//
//nolint:gochecknoglobals // A constant table of routes.
var freshAuthRules = []freshAuthRule{
	{
		regexp.MustCompile(`^/api/server/(?:caller/tokens|service-tokens|principals/[^/]+/tokens)(?:/[^/]+/rotate)?$`),
		[]string{http.MethodPost},
	},
	{regexp.MustCompile(`^/api/server/(?:session|token)-policy$`), []string{http.MethodPatch}},
	{
		regexp.MustCompile(`^/api/server/sign-in-providers(?:/[^/]+)?$`),
		[]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
	},
	{regexp.MustCompile(`^/api/server/roles(?:/[^/]+)?$`), []string{http.MethodPost, http.MethodPatch}},
	{regexp.MustCompile(`^/api/server/audit/chain$`), []string{http.MethodPost}},
	{
		regexp.MustCompile(`^/api/server/(?:namespaces/[^/]+(?:/collections/[^/]+)?/|principals/[^/]+/)?grants$`),
		[]string{http.MethodPost},
	},
	{
		// A grant to a group applies to each member, so a membership change is
		// an act-as edge.
		regexp.MustCompile(`^/api/server/groups/[^/]+(?:/members(?:/[^/]+)?)?$`),
		[]string{http.MethodPost, http.MethodDelete},
	},
	{
		regexp.MustCompile(`^/api/dustpan/v1/git-host-(?:apps|installations)/[^/]+$`),
		[]string{http.MethodPut, http.MethodDelete},
	},
	{
		regexp.MustCompile(`^/api/server/namespaces/[^/]+/ip-allowlist$`),
		[]string{http.MethodPut, http.MethodDelete},
	},
	{
		regexp.MustCompile(`^/api/dustpan/v1/namespaces/[^/]+/approval-(?:default|rules/[^/]+)$`),
		[]string{http.MethodPut, http.MethodDelete},
	},
	{
		// A gate and an access limit of a namespace, of a collection and of a
		// unit. The unit name keeps its percent-encoding, so it may hold a slash.
		regexp.MustCompile(`^/api/dustpan/v1/namespaces/[^/]+/(?:collections/[^/]+/|units/by-name/.+/)?` +
			`(?:gates|access-limits)/[^/]+$`),
		[]string{http.MethodPut, http.MethodDelete},
	},
}

// needsFreshAuth reports whether method on path is a write that needs a
// fresh authentication. An access token never gives one.
func needsFreshAuth(method, path string) bool {
	for _, rule := range freshAuthRules {
		if rule.path.MatchString(path) {
			return slices.Contains(rule.methods, method)
		}
	}

	return false
}

// freshServerCall sends one installation API request and answers its status and
// body. A write that needs a fresh authentication goes with the console
// session of a site administrator, any other request with the bootstrap
// admin token. The env of a fresh write must come from NewEnvWithSignIn.
func freshServerCall(ctx context.Context, t *testing.T, env *e2e.Env, step principalStep) (int, string) {
	t.Helper()

	if needsFreshAuth(step.method, step.path) {
		return env.FreshAdminAPIStatus(
			ctx,
			t,
			e2e.SessionRequest{Method: step.method, Path: step.path, Body: step.body},
		)
	}

	status, resp, err := env.ServerAPIStatus(ctx, step.method, step.path, step.body)
	if err != nil {
		t.Fatalf("%s %s: %v", step.method, step.path, err)
	}

	return status, resp
}

// principalCall sends one installation API request, as freshServerCall does, and
// fails unless it answers step.want. It decodes the answer into out when out
// is not nil.
func principalCall(ctx context.Context, t *testing.T, env *e2e.Env, step principalStep, out any) {
	t.Helper()

	status, resp := freshServerCall(ctx, t, env, step)

	checkPrincipalAnswer(t, step, status, resp, out)
}

// principalCallWithToken sends one installation API request with the
// bootstrap admin token, also a write that needs a fresh authentication, and
// fails unless it answers step.want. A step uses it to prove that an access
// token never gives a fresh authentication.
func principalCallWithToken(ctx context.Context, t *testing.T, env *e2e.Env, step principalStep, out any) {
	t.Helper()

	status, resp, err := env.ServerAPIStatus(ctx, step.method, step.path, step.body)
	if err != nil {
		t.Fatalf("%s %s: %v", step.method, step.path, err)
	}

	checkPrincipalAnswer(t, step, status, resp, out)
}

// checkPrincipalAnswer fails unless status is step.want. It decodes resp into
// out when out is not nil.
func checkPrincipalAnswer(t *testing.T, step principalStep, status int, resp string, out any) {
	t.Helper()

	if status != step.want {
		t.Fatalf("%s %s answers %d, want %d (%s)", step.method, step.path, status, step.want, resp)
	}

	if out == nil {
		return
	}

	if err := json.Unmarshal([]byte(resp), out); err != nil {
		t.Fatalf("decode %s %s answer %q: %v", step.method, step.path, resp, err)
	}
}
