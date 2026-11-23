//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/principal"
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
	checkHousekeepingPrincipals(ctx, t, env)

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

	// The total holds the housekeeping principals that the install writes.
	wantTotal := int64(3 + len(principal.HousekeepingPurposes()))
	if len(first.Principals) != 1 || first.Principals[0].ID != fx.alice.ID || first.NextCursor == "" ||
		first.Total != wantTotal || first.Principals[0].Source != "local" || first.Principals[0].GroupCount != 1 {
		t.Fatalf("first page = %+v, want local alice in 1 group, a cursor and a total of %d", first, wantTotal)
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

	// The housekeeping principals are services too. Their subjects sort after
	// ci-deploy.
	purposes := principal.HousekeepingPurposes()
	services := make([]string, 0, 1+len(purposes))

	services = append(services, fx.deploy.ID)
	for _, p := range purposes {
		services = append(services, p.ID())
	}

	for _, c := range []struct {
		query string
		want  []string
	}{
		{"kind=service", services},
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

// housekeepingNamespace is the namespace that checkHousekeepingPrincipals
// creates.
const housekeepingNamespace = "housekeeping"

// checkHousekeepingPrincipals lists the housekeeping principals with the
// source filter, reads the grants that each one holds on the installation
// through the access review, creates a namespace and reads the grants of the
// housekeeping principals on it through the access review of the namespace.
func checkHousekeepingPrincipals(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	checkHousekeepingList(ctx, t, env)

	server := housekeepingReview{
		acts:   principal.HousekeepingPurpose.ActsOnInstallation,
		path:   "/api/server/access-review",
		object: "@server",
	}
	if n := checkHousekeepingGrants(ctx, t, env, server); n != 7 {
		t.Fatalf("housekeeping grants on @server = %d, want 7", n)
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + housekeepingNamespace + `"}`, http.StatusCreated,
	}, nil)

	ns := housekeepingReview{
		acts:   principal.HousekeepingPurpose.ActsInsideNamespace,
		path:   "/api/server/namespaces/" + housekeepingNamespace + "/access-review",
		object: housekeepingNamespace,
	}
	if n := checkHousekeepingGrants(ctx, t, env, ns); n != 5 {
		t.Fatalf("housekeeping grants on %s = %d, want 5", housekeepingNamespace, n)
	}
}

// checkHousekeepingList lists the principals of the source builtin and
// fails unless the list holds one service for each housekeeping purpose.
func checkHousekeepingList(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var list struct {
		NextCursor string         `json:"nextCursor"`
		Principals []e2ePrincipal `json:"principals"`
		Total      int64          `json:"total"`
	}
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/principals?kind=service&source=builtin", "", http.StatusOK,
	}, &list)

	purposes := principal.HousekeepingPurposes()

	ids := make([]string, 0, len(list.Principals))
	for i := range list.Principals {
		ids = append(ids, list.Principals[i].ID)

		if list.Principals[i].Source != "builtin" || list.Principals[i].Kind != "service" {
			t.Fatalf("builtin principal = %+v, want a service of the source builtin", list.Principals[i])
		}
	}

	want := make([]string, 0, len(purposes))
	for _, p := range purposes {
		want = append(want, p.ID())
	}

	if !slices.Equal(ids, want) || list.Total != int64(len(purposes)) || list.NextCursor != "" {
		t.Fatalf("builtin principals = %+v, want %v", list, want)
	}
}

// housekeepingReview is one access review that checkHousekeepingGrants reads:
// the path of the review, the object of the grants, and whether a purpose
// holds a grant on that object.
type housekeepingReview struct {
	acts   func(principal.HousekeepingPurpose) bool
	path   string
	object string
}

// checkHousekeepingGrants reads the access review of r with the subject
// filter of each housekeeping principal. A principal for which r.acts is true
// must hold one grant of its role on r.object, and any other principal none.
// It returns the count of the principals that hold a grant on r.object.
func checkHousekeepingGrants(ctx context.Context, t *testing.T, env *e2e.Env, r housekeepingReview) int {
	t.Helper()

	n := 0

	for _, p := range principal.HousekeepingPurposes() {
		rows := reviewOf(ctx, t, env, r.path, url.Values{"subject": {"principal:" + p.ID()}})
		rows = housekeepingRows(rows, r.object)

		if !r.acts(p) {
			if len(rows) != 0 {
				t.Fatalf("grants of %s on %s = %+v, want none", p.Subject(), r.object, rows)
			}

			continue
		}

		checkHousekeepingRow(t, p, r.object, rows)

		n++
	}

	return n
}

// housekeepingRows returns the rows of rows on object, and leaves out the rows
// that a grant on a containing object gives.
func housekeepingRows(rows []e2eReviewRow, object string) []e2eReviewRow {
	return slices.DeleteFunc(slices.Clone(rows), func(r e2eReviewRow) bool {
		return r.Object != object || r.InheritedFrom != ""
	})
}

// checkHousekeepingRow fails unless rows is one current grant of the role of
// p on object, with the server as the creator and a time.
func checkHousekeepingRow(t *testing.T, p principal.HousekeepingPurpose, object string, rows []e2eReviewRow) {
	t.Helper()

	if len(rows) != 1 {
		t.Fatalf("grants of %s on %s = %+v, want 1", p.Subject(), object, rows)
	}

	r := rows[0]
	if r.Role != p.Role() || r.Subject != "principal:"+p.ID() || r.GrantedBy != "@server" ||
		r.GrantedAtMs == nil || r.RevokedAtMs != nil || r.Source != "grant" {
		t.Fatalf("grant of %s on %s = %+v, want the role %s from @server with a time", p.Subject(), object, r, p.Role())
	}
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
	// membership changes need. The housekeeping principals that the install
	// writes come after it.
	want := 3 + len(principal.HousekeepingPurposes())
	if len(list.Principals) != want || list.Principals[0].ID != fx.alice.ID ||
		list.Principals[1].ID != fx.deploy.ID {
		t.Fatalf("principals = %+v, want alice, ci-deploy, the session administrator and the housekeeping principals",
			list.Principals)
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
		regexp.MustCompile(`^/api/server/(?:service-tokens|principals/[^/]+/tokens)(?:/[^/]+/rotate)?$`),
		[]string{http.MethodPost},
	},
	{
		regexp.MustCompile(`^/api/dustpan/v1/me/tokens(?:/[^/]+/rotate)?$`),
		[]string{http.MethodPost},
	},
	{regexp.MustCompile(`^/api/server/(?:session|token)-policy$`), []string{http.MethodPatch}},
	{
		regexp.MustCompile(`^/api/server/sign-in-providers(?:/[^/]+)?$`),
		[]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
	},
	{regexp.MustCompile(`^/api/server/roles(?:/[^/]+)?$`), []string{http.MethodPost, http.MethodPatch}},
	{regexp.MustCompile(`^/api/dustpan/v1/audit/chain$`), []string{http.MethodPost}},
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
		regexp.MustCompile(`^/api/dustpan/v1/git-host-apps/[^/]+/confirm-rotation$`),
		[]string{http.MethodPost},
	},
	{
		regexp.MustCompile(`^/api/server/namespaces/[^/]+/ip-allowlist$`),
		[]string{http.MethodPut, http.MethodDelete},
	},
	{
		// An approval default and an approval rule of a namespace, of a
		// collection and of a unit.
		regexp.MustCompile(`^/api/dustpan/v1/namespaces/[^/]+/(?:collections/[^/]+/|units/by-name/.+/)?` +
			`approval-(?:default|rules/[^/]+)$`),
		[]string{http.MethodPut, http.MethodDelete},
	},
	{
		// A gate and an access limit of a namespace, of a collection and of a
		// unit. The unit name keeps its percent-encoding, so it may hold a slash.
		regexp.MustCompile(`^/api/dustpan/v1/namespaces/[^/]+/(?:collections/[^/]+/|units/by-name/.+/)?` +
			`(?:gates|access-limits)/[^/]+$`),
		[]string{http.MethodPut, http.MethodDelete},
	},
	{
		// A gate, an access limit and an approval rule of the installation.
		regexp.MustCompile(`^/api/dustpan/v1/(?:gates|access-limits|approval-rules)/[^/]+$`),
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
