//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// e2eRole and e2eGrant decode the role and grant wire forms.
type e2eRole struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
	BuiltIn bool     `json:"builtIn"`
}

type e2eRoleList struct {
	Roles []e2eRole `json:"roles"`
}

type e2eGrant struct {
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	Role       string `json:"role"`
	Object     string `json:"object"`
	HolderName string `json:"holderName"`
	HolderKind string `json:"holderKind"`
}

type e2eGrantList struct {
	Grants     []e2eGrant `json:"grants"`
	NextCursor string     `json:"nextCursor"`
	Total      int64      `json:"total"`
}

// TestRolesAndGrants drives every role and grant route of the installation
// API (ADR 52) through the real binary, on each store. No CLI calls these
// routes, so the harness calls them with the bootstrap admin token.
func TestRolesAndGrants(t *testing.T) {
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
			runAccessOnStore(t, cli, db)
		})
	}
}

// runAccessOnStore runs the role and grant steps on one store.
func runAccessOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	role := checkRoles(ctx, t, env)

	var bob e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"bob","kind":"person"}`, http.StatusCreated,
	}, &bob)

	var ops e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated,
	}, &ops)

	if groupProtected(t, ops) {
		t.Fatal("the new group is protected, want false: it holds no grant")
	}

	checkGroupBecomesProtected(ctx, t, env, ops)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"grants"}`, http.StatusCreated,
	}, nil)

	// A grant names a unit that exists: it follows its stable ID.
	createStack(ctx, t, env, "grants/web/prod")
	checkUnitGrantNeedsTheUnit(ctx, t, env, "principal:"+bob.ID)

	bobSubject := "principal:" + bob.ID
	opsSubject := "group:" + ops.ID + "#member"

	for _, k := range []struct {
		path, object, subject, role string
	}{
		{"/api/server/grants", "@server", bobSubject, "viewer"},
		{"/api/server/namespaces/grants/grants", "grants", opsSubject, role.ID},
		{"/api/server/namespaces/grants/unit-grants", "pulumi:grants/web/prod", bobSubject, "maintainer"},
	} {
		checkGrantRoutes(ctx, t, env, grantKind{path: k.path, object: k.object, subject: k.subject, role: k.role})
	}

	checkCollectionGrantsWait(ctx, t, env, opsSubject)

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/roles/" + role.ID, "", http.StatusNoContent,
	}, nil)

	checkSubjectDeleteRevokes(ctx, t, env, bob)
	checkLastSiteAdministratorKept(ctx, t, env)
	checkTokenReachRefusal(ctx, t, env)
	checkMemberSignsIn(ctx, t, env)
}

// checkTokenReachRefusal gives a person the account-administrator role and
// makes two service principals: one holds the installation-operator role and
// one holds the viewer role. With a console session, the account
// administrator makes a token for the viewer principal and is refused for the
// operator principal: the reach of that principal does not fit inside its own
// reach.
func checkTokenReachRefusal(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var admin e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("reach@example.com") + `","kind":"person"}`, http.StatusCreated,
	}, &admin)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:` + admin.ID + `","role":"account-administrator"}`, http.StatusCreated,
	}, nil)

	for _, p := range []struct {
		subject, role string
		want          int
	}{
		{subject: "ci-reach-ops", role: "installation-operator", want: http.StatusForbidden},
		{subject: "ci-reach-viewer", role: "viewer", want: http.StatusCreated},
	} {
		var svc e2ePrincipal
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/principals", `{"subject":"` + p.subject + `","kind":"service"}`,
			http.StatusCreated,
		}, &svc)

		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/grants",
			`{"subject":"principal:` + svc.ID + `","role":"` + p.role + `"}`, http.StatusCreated,
		}, nil)

		status, body := env.SessionAPIStatus(ctx, t, admin.ID, e2e.SessionRequest{
			Method: http.MethodPost, Path: "/api/server/principals/" + svc.ID + "/tokens", Body: `{"name":"ci"}`,
		})

		if status != p.want {
			t.Fatalf("account administrator makes a token for the %s service principal = %d (%s), want %d",
				p.role, status, body, p.want)
		}

		if p.want == http.StatusForbidden &&
			(!strings.Contains(body, "token.outside-reach") ||
				!strings.Contains(body, "server:backup on the installation")) {
			t.Fatalf("refusal = %s, want the code of the reach rule and the actions outside the reach", body)
		}
	}
}

// groupProtected returns the protected flag of a group answer, and fails t
// when the answer holds no flag.
func groupProtected(t *testing.T, g e2eGroup) bool {
	t.Helper()

	if g.Protected == nil {
		t.Fatal("the group answer has no protected field")
	}

	return *g.Protected
}

// checkGroupBecomesProtected gives the group a custom role with a sensitive
// action on the installation, reads the group again, and checks that the
// group is protected. The grant goes back at the end, so that the later
// grant steps read the installation grants alone, and the group is not
// protected again.
func checkGroupBecomesProtected(ctx context.Context, t *testing.T, env *e2e.Env, g e2eGroup) {
	t.Helper()

	var role e2eRole
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles",
		`{"name":"access-manager","actions":["server:manage-access"]}`, http.StatusCreated,
	}, &role)

	var grant e2eGrant
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/grants",
		`{"subject":"group:` + g.ID + `#member","role":"` + role.ID + `"}`, http.StatusCreated,
	}, &grant)

	var protected e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/groups/" + g.ID, "", http.StatusOK,
	}, &protected)

	if !groupProtected(t, protected) {
		t.Fatal("the group with a sensitive grant on the installation is not protected, want true")
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/grants/" + grant.ID, "", http.StatusNoContent,
	}, nil)

	var revoked e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/groups/" + g.ID, "", http.StatusOK,
	}, &revoked)

	if groupProtected(t, revoked) {
		t.Fatal("the group after the revoke of its sensitive grant is protected, want false")
	}
}

// TestRevokeGrantByID revokes a namespace grant and a unit grant through
// DELETE /api/server/grants/{id} with the bootstrap admin token, and checks
// that each grant is gone from the grants of its object. It also checks
// that a person with only a viewer grant on another namespace gets 404 for
// a grant ID on this namespace.
func TestRevokeGrantByID(t *testing.T) {
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
			runRevokeByIDOnStore(t, cli, db)
		})
	}
}

// runRevokeByIDOnStore runs the revoke-by-ID steps on one store.
func runRevokeByIDOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	for _, ns := range []string{"byid", "other"} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/namespaces", `{"name":"` + ns + `"}`, http.StatusCreated,
		}, nil)
	}

	var bob, carol e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"byid-bob","kind":"person"}`, http.StatusCreated,
	}, &bob)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("byid-carol") + `","kind":"person"}`,
		http.StatusCreated,
	}, &carol)

	// A grant names a unit that exists.
	createStack(ctx, t, env, "byid/web/prod")

	var nsGrant, unitGrant e2eGrant
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/byid/grants",
		`{"subject":"principal:` + bob.ID + `","role":"viewer"}`, http.StatusCreated,
	}, &nsGrant)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/byid/unit-grants?tool=pulumi&unit=web%2Fprod",
		`{"subject":"principal:` + bob.ID + `","role":"viewer"}`, http.StatusCreated,
	}, &unitGrant)

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/grants/" + nsGrant.ID, "", http.StatusNoContent,
	}, nil)

	var nsList e2eGrantList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/namespaces/byid/grants", "", http.StatusOK,
	}, &nsList)

	for i := range nsList.Grants {
		if nsList.Grants[i].ID == nsGrant.ID {
			t.Fatalf("grants on byid still hold %s after the revoke by ID", nsGrant.ID)
		}
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/grants/" + unitGrant.ID, "", http.StatusNoContent,
	}, nil)

	var unitList e2eGrantList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/namespaces/byid/unit-grants?tool=pulumi&unit=web%2Fprod", "",
		http.StatusOK,
	}, &unitList)

	for i := range unitList.Grants {
		if unitList.Grants[i].ID == unitGrant.ID {
			t.Fatalf("grants on the unit still hold %s after the revoke by ID", unitGrant.ID)
		}
	}

	// Carol sees only the other namespace, so a grant on byid is unknown
	// to her.
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/other/grants",
		`{"subject":"principal:` + carol.ID + `","role":"viewer"}`, http.StatusCreated,
	}, nil)

	carolToken := env.PersonalToken(ctx, t, carol.ID, "revoke-check")

	var target e2eGrant
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/byid/grants",
		`{"subject":"principal:` + bob.ID + `","role":"viewer"}`, http.StatusCreated,
	}, &target)

	status, body, err := env.ServerAPIStatusAs(
		ctx, carolToken.Secret, http.MethodDelete, "/api/server/grants/"+target.ID, "",
	)
	if err != nil || status != http.StatusNotFound {
		t.Fatalf("viewer DELETE grant by ID = %d, %v (%s), want %d", status, err, body, http.StatusNotFound)
	}
}

// checkMemberSignsIn gives a principal that is not an administrator a writer
// grant on one namespace. It logs in with the Pulumi CLI and sees that
// namespace only: the routes whose object is the caller need only
// caller:read, and their handlers filter each namespace.
func checkMemberSignsIn(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	token, ok := env.MemberToken()
	if !ok {
		t.Log("skip the member sign-in: the binary server knows only the bootstrap admin tokens (ADR 53)")

		return
	}

	var member e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"` + e2e.MemberSubject + `","kind":"person"}`,
		http.StatusCreated,
	}, &member)

	for _, ns := range []string{"team", "hidden"} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/namespaces", `{"name":"` + ns + `"}`, http.StatusCreated,
		}, nil)
		createStack(ctx, t, env, ns+"/web/dev")
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/team/grants",
		`{"subject":"principal:` + member.ID + `","role":"writer"}`, http.StatusCreated,
	}, nil)

	as := []string{"PULUMI_ACCESS_TOKEN=" + token}

	for _, spec := range []e2e.StepSpec{
		step("member login", contains(env.URL), "login"),
		step("member whoami", contains(e2e.MemberSubject), "whoami"),
		step("member whoami lists its namespace only", onlyOrganization("team"), "whoami", "--verbose"),
		step("member org get-default", contains("team"), "org", "get-default"),
		step("member stack ls lists its namespace only", stacksOnlyIn("team"), "stack", "ls", "--json", "--all"),
		failStep("member stack ls in another namespace fails", nil,
			"stack", "ls", "--json", "--organization", "hidden"),
	} {
		spec.Env, spec.Redact = as, true
		env.Step(ctx, t, spec)
	}

	checkNamespaceList(ctx, t, env)
}

// e2eNamespaceItem decodes one namespace of the list.
type e2eNamespaceItem struct {
	Name string `json:"name"`
}

// e2eNamespaceList decodes one page of GET /api/server/namespaces.
type e2eNamespaceList struct {
	Namespaces []e2eNamespaceItem `json:"namespaces"`
	NextCursor string             `json:"nextCursor"`
	Total      *int64             `json:"total"`
}

// fetchNamespaceList reads one page of the namespace list with the admin
// token and decodes it.
func fetchNamespaceList(ctx context.Context, t *testing.T, env *e2e.Env, route string) e2eNamespaceList {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, route, "")
	if err != nil {
		t.Fatalf("GET %s: %v", route, err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET %s answers %d, want %d (%s)", route, status, http.StatusOK, body)
	}

	var list e2eNamespaceList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode GET %s %q: %v", route, body, err)
	}

	return list
}

// namespaceNames returns the names of a decoded page.
func namespaceNames(list e2eNamespaceList) []string {
	names := make([]string, 0, len(list.Namespaces))
	for _, ns := range list.Namespaces {
		names = append(names, ns.Name)
	}

	return names
}

// checkAdminSeesBoth proves the admin list holds team and hidden.
func checkAdminSeesBoth(ctx context.Context, t *testing.T, env *e2e.Env) []string {
	t.Helper()

	names := namespaceNames(fetchNamespaceList(ctx, t, env, "/api/server/namespaces"))

	for _, want := range []string{"team", "hidden"} {
		if !slices.Contains(names, want) {
			t.Fatalf("admin namespaces = %v, want %q among them", names, want)
		}
	}

	return names
}

// walkNamespacePages follows limit 1 from the first page to the end and
// returns every name once. It fails unless the first page has a cursor and
// a total, and the walk reaches the total.
func walkNamespacePages(ctx context.Context, t *testing.T, env *e2e.Env, names []string) map[string]bool {
	t.Helper()

	first := fetchNamespaceList(ctx, t, env, "/api/server/namespaces?limit=1")
	if first.NextCursor == "" {
		t.Fatalf("first page has no next cursor, want one for limit 1 over %v", names)
	}

	if first.Total == nil {
		t.Fatal("first page has no total, want one")
	}

	seen := map[string]bool{}
	route := "/api/server/namespaces?limit=1"

	for route != "" {
		page := fetchNamespaceList(ctx, t, env, route)
		for _, ns := range page.Namespaces {
			if seen[ns.Name] {
				t.Fatalf("paged namespaces list %q twice", ns.Name)
			}
			seen[ns.Name] = true
		}
		route = ""
		if page.NextCursor != "" {
			route = "/api/server/namespaces?limit=1&cursor=" + page.NextCursor
		}
		if len(seen) > 100 {
			t.Fatalf("paged more than 100 namespaces, want the end: %v", seen)
		}
	}

	if int64(len(seen)) != *first.Total {
		t.Fatalf("paged %d namespaces, want total %d", len(seen), *first.Total)
	}

	return seen
}

// checkMemberSeesTeamOnly proves the member list holds team and not hidden.
// Without an in-process member token it skips, as the binary server knows
// only the bootstrap admin tokens.
func checkMemberSeesTeamOnly(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	token, ok := env.MemberToken()
	if !ok {
		t.Log("skip the member list: the binary server knows only the bootstrap admin tokens")

		return
	}

	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet, "/api/server/namespaces", "")
	if err != nil {
		t.Fatalf("member GET namespaces: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("member GET namespaces answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var member e2eNamespaceList
	if err := json.Unmarshal([]byte(body), &member); err != nil {
		t.Fatalf("decode member list %q: %v", body, err)
	}

	holds := map[string]bool{}
	for _, ns := range member.Namespaces {
		holds[ns.Name] = true
	}

	if !holds["team"] || holds["hidden"] {
		t.Fatalf("member namespaces hold team=%t hidden=%t, want team only", holds["team"], holds["hidden"])
	}
}

// checkNamespaceList proves the namespace list: the admin sees every
// namespace, paging by name reaches each name once, and the member sees
// only the namespace it may read.
func checkNamespaceList(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	names := checkAdminSeesBoth(ctx, t, env)
	seen := walkNamespacePages(ctx, t, env, names)

	for _, want := range []string{"team", "hidden"} {
		if !seen[want] {
			t.Fatalf("paged namespaces miss %q", want)
		}
	}

	checkMemberSeesTeamOnly(ctx, t, env)
}

// onlyOrganization checks that whoami --verbose names ns as the only
// organization.
func onlyOrganization(ns string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		for line := range strings.Lines(r.Stdout) {
			if orgs, ok := strings.CutPrefix(strings.TrimSpace(line), "Organizations:"); ok {
				if strings.TrimSpace(orgs) != ns {
					return fmt.Errorf("%w: organizations %q, want %s only", errUnexpected, orgs, ns)
				}

				return nil
			}
		}

		return fmt.Errorf("%w: whoami printed no organizations: %q", errUnexpected, r.Stdout)
	}
}

// stacksOnlyIn checks that stack ls --json lists at least one stack, and
// only stacks of ns.
func stacksOnlyIn(ns string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var stacks []struct {
			Name string `json:"name"`
		}

		if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
			return fmt.Errorf("decode stack ls: %w", err)
		}

		if len(stacks) == 0 {
			return fmt.Errorf("%w: stack ls listed no stack, want the stacks of %s", errUnexpected, ns)
		}

		for _, st := range stacks {
			if !strings.HasPrefix(st.Name, ns+"/") {
				return fmt.Errorf("%w: stack ls lists %q, want the stacks of %s only", errUnexpected, st.Name, ns)
			}
		}

		return nil
	}
}

// checkCollectionGrantsWait drives the grant routes of a collection that does
// not exist. Each route answers 404, as it does for a missing unit, so no
// grant is made or listed on a collection with no units.
func checkCollectionGrantsWait(ctx context.Context, t *testing.T, env *e2e.Env, subject string) {
	t.Helper()

	const grants = "/api/server/namespaces/grants/collections/payments/grants"

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, grants, `{"subject":"` + subject + `","role":"writer"}`, http.StatusNotFound,
	}, nil)

	principalCall(ctx, t, env, principalStep{http.MethodGet, grants, "", http.StatusNotFound}, nil)

	principalCall(ctx, t, env, principalStep{http.MethodDelete, grants + "/grt-missing", "", http.StatusNotFound}, nil)
}

// checkRoles reads the catalog, makes, changes and reads a custom role, and
// answers it.
func checkRoles(ctx context.Context, t *testing.T, env *e2e.Env) e2eRole {
	t.Helper()

	var list e2eRoleList
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/roles", "", http.StatusOK}, &list)

	names := make([]string, 0, len(list.Roles))
	for _, r := range list.Roles {
		names = append(names, r.Name)
	}

	for _, want := range []string{"viewer", "admin", "installation-operator", "account-administrator", "site-administrator"} {
		if !strings.Contains(" "+strings.Join(names, " ")+" ", " "+want+" ") {
			t.Fatalf("roles = %v, want %s among them", names, want)
		}
	}

	var role e2eRole
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles", `{"name":"deployer","actions":["unit:write","unit:read"]}`,
		http.StatusCreated,
	}, &role)

	if !strings.HasPrefix(role.ID, "rol-") || role.BuiltIn {
		t.Fatalf("role = %+v, want a custom role with a rol- ID", role)
	}

	for _, step := range []principalStep{
		{http.MethodPost, "/api/server/roles", `{"name":"DEPLOYER","actions":["unit:read"]}`, http.StatusConflict},
		{http.MethodPatch, "/api/server/roles/" + role.ID, `{"description":"Runs updates"}`, http.StatusOK},
		{http.MethodPatch, "/api/server/roles/viewer", `{"description":"x"}`, http.StatusConflict},
		{http.MethodGet, "/api/server/roles/viewer", "", http.StatusOK},
	} {
		principalCall(ctx, t, env, step, nil)
	}

	var got e2eRole
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/roles/" + role.ID, "", http.StatusOK}, &got)

	if strings.Join(got.Actions, " ") != "unit:read unit:write" {
		t.Fatalf("role actions = %v, want unit:read unit:write", got.Actions)
	}

	return role
}

// grantKind names the grant routes of one object kind and one grant to
// make there.
type grantKind struct {
	path    string
	object  string
	subject string
	role    string
}

// checkGrantRoutes makes, lists and revokes one grant on one object kind.
func checkGrantRoutes(ctx context.Context, t *testing.T, env *e2e.Env, k grantKind) {
	t.Helper()

	query := ""
	if strings.HasSuffix(k.path, "/unit-grants") {
		query = "?tool=pulumi&unit=web%2Fprod"
	}

	body := `{"subject":"` + k.subject + `","role":"` + k.role + `"}`

	var g e2eGrant
	principalCall(ctx, t, env, principalStep{http.MethodPost, k.path + query, body, http.StatusCreated}, &g)

	if g.Object != k.object || g.Subject != k.subject || g.Role != k.role || !strings.HasPrefix(g.ID, "grt-") {
		t.Fatalf("grant on %s = %+v, want %s %s", k.object, g, k.subject, k.role)
	}

	principalCall(ctx, t, env, principalStep{http.MethodPost, k.path + query, body, http.StatusConflict}, nil)

	var list e2eGrantList
	principalCall(ctx, t, env, principalStep{http.MethodGet, k.path + query, "", http.StatusOK}, &list)

	list = withoutFreshAdmin(list)

	if len(list.Grants) != 1 || list.Grants[0].ID != g.ID || list.Total != 1 || list.NextCursor != "" {
		t.Fatalf("grants on %s = %+v, want %s alone", k.object, list, g.ID)
	}

	checkGrantFilters(ctx, t, env, k, query)

	if strings.HasPrefix(k.role, "rol-") {
		// A custom role that a grant holds cannot be deleted.
		principalCall(ctx, t, env, principalStep{
			http.MethodDelete, "/api/server/roles/" + k.role, "", http.StatusConflict,
		}, nil)
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, k.path + "/" + g.ID + query, "", http.StatusNoContent,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, k.path + "/" + g.ID + query, "", http.StatusNotFound,
	}, nil)
}

// checkGrantFilters lists the grants on one object kind with the holder and
// role filters: the grant of k has its holder name and kind, a filter
// on another role keeps nothing, and a bad holder or role answers 400.
func checkGrantFilters(ctx context.Context, t *testing.T, env *e2e.Env, k grantKind, query string) {
	t.Helper()

	sep := "?"
	if query != "" {
		sep = "&"
	}

	wantKind := map[bool]string{true: "group", false: "person"}[strings.HasPrefix(k.subject, "group:")]

	var held e2eGrantList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, k.path + query + sep + url.Values{"holder": {k.subject}, "role": {k.role}}.Encode(), "",
		http.StatusOK,
	}, &held)

	if len(held.Grants) != 1 || held.Total != 1 || held.Grants[0].HolderName == "" ||
		held.Grants[0].HolderKind != wantKind {
		t.Fatalf("grants of %s on %s = %+v, want one with its holder name and the kind %s",
			k.subject, k.object, held, wantKind)
	}

	var none e2eGrantList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, k.path + query + sep + url.Values{"role": {"secret-viewer"}}.Encode(), "", http.StatusOK,
	}, &none)

	if len(none.Grants) != 0 || none.Total != 0 {
		t.Fatalf("secret-viewer grants on %s = %+v, want none", k.object, none)
	}

	for _, bad := range []string{"holder=bob", "role=bad%20role"} {
		principalCall(ctx, t, env, principalStep{
			http.MethodGet, k.path + query + sep + bad, "", http.StatusBadRequest,
		}, nil)
	}
}

// checkSubjectDeleteRevokes deletes a principal that holds a grant, and
// reads that the grant went with it.
func checkSubjectDeleteRevokes(ctx context.Context, t *testing.T, env *e2e.Env, bob e2ePrincipal) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/grants/grants",
		`{"subject":"principal:` + bob.ID + `","role":"viewer"}`, http.StatusCreated,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/principals/" + bob.ID, "", http.StatusNoContent,
	}, nil)

	var list e2eGrantList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/namespaces/grants/grants", "", http.StatusOK,
	}, &list)

	if len(list.Grants) != 0 {
		t.Fatalf("grants after the principal delete = %+v, want none", list.Grants)
	}
}

// createStack makes the Pulumi stack "<namespace>/<project>/<name>" through
// the API that the Pulumi CLI calls on "pulumi stack init".
func createStack(ctx context.Context, t *testing.T, env *e2e.Env, ref string) {
	t.Helper()

	project, name := path.Split(ref)

	// The server refuses a stack that no rule places. The call is safe to
	// repeat when the namespace has the rule already.
	env.SeedCatchAllPlacement(ctx, t, strings.SplitN(ref, "/", 2)[0])

	resp, err := env.APIStatusHeaders(ctx, http.MethodPost, "/api/stacks/"+strings.TrimSuffix(project, "/"),
		`{"stackName":"`+name+`"}`)
	if err != nil {
		t.Fatalf("create stack %s: %v", ref, err)
	}

	if resp.Status != http.StatusOK {
		t.Fatalf("create stack %s answers %d (%s)", ref, resp.Status, resp.Body)
	}
}

// checkUnitGrantNeedsTheUnit grants on a unit that does not exist: the
// grant answers 404, because a grant follows the stable ID of its unit.
func checkUnitGrantNeedsTheUnit(ctx context.Context, t *testing.T, env *e2e.Env, subject string) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/grants/unit-grants?tool=pulumi&unit=api%2Fprod",
		`{"subject":"` + subject + `","role":"viewer"}`, http.StatusNotFound,
	}, nil)
}

// withoutFreshAdmin returns list without the grant of the site administrator
// that the fresh sessions of the steps use.
func withoutFreshAdmin(list e2eGrantList) e2eGrantList {
	kept := list.Grants[:0:0]

	for _, g := range list.Grants {
		if g.ID == "grt-e2e-fresh-admin" {
			list.Total--

			continue
		}

		kept = append(kept, g)
	}

	list.Grants = kept

	return list
}

// checkLastSiteAdministratorKept makes sure that exactly one person holds the
// site-administrator role on the installation, and sees that the server
// refuses each change that leaves the installation with no site
// administrator: the delete and the disable of the person, and the revoke of
// the grant. The person is the one holder that the harness made, or a new
// person when the installation has none. It skips when the installation has
// more than one site administrator, because then the changes work.
func checkLastSiteAdministratorKept(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var holders e2eGrantList
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/grants?role=site-administrator", "", http.StatusOK,
	}, &holders)

	var grant e2eGrant

	switch {
	case holders.Total == 0:
		var admin e2ePrincipal
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/principals", `{"subject":"last-admin","kind":"person"}`, http.StatusCreated,
		}, &admin)
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/grants",
			`{"subject":"principal:` + admin.ID + `","role":"site-administrator"}`, http.StatusCreated,
		}, &grant)
	case holders.Total == 1 && strings.HasPrefix(holders.Grants[0].Subject, "principal:"):
		grant = holders.Grants[0]
	default:
		t.Logf("skip the last site administrator check: %d holders of the role", holders.Total)

		return
	}

	admin := strings.TrimPrefix(grant.Subject, "principal:")

	for _, step := range []principalStep{
		{http.MethodDelete, "/api/server/principals/" + admin, "", http.StatusConflict},
		{http.MethodPost, "/api/server/principals/" + admin + "/disable", "", http.StatusConflict},
		{http.MethodDelete, "/api/server/grants/" + grant.ID, "", http.StatusConflict},
	} {
		principalCall(ctx, t, env, step, nil)
	}
}
