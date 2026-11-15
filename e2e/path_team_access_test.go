//go:build e2e

// This file covers the team access path. The persona is Ana, a developer of
// the payments team. The LDAP directory signs her in, and she makes a
// personal token with her console session and runs "pulumi up" with it
// through the grant of her group. The placement rules put her stack and the
// workspace of the OpenTofu http backend into the payments collection, and a
// workspace that no rule matches is refused. Bob, who is not on the team,
// gets a 403 that names the action and the kind of the object. The access
// review lists Ana through her group. The steps that wait for a feature
// that main does not have are skipped with the reason that names what each
// waits for.
//
// User-visible end checks: the LDAP sign-in docs page, the token page of the
// console, the placement rules page of the docs, the access review export,
// and the members view of the console.

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/internal/ldaptest"
)

// teamAccessTest is the name of this test, used in the names of skipped steps.
const teamAccessTest = "TestPathTeamAccess"

// teamAccessNamespace is the namespace that the team access steps use, and
// teamAccessBase is its path in the namespace API.
const (
	teamAccessNamespace = "teamaccess"
	teamAccessBase      = "/api/dustpan/v1/namespaces/" + teamAccessNamespace
	teamAccessRules     = teamAccessBase + "/placement-rules"
)

// The LDAP person of the steps: ana signs in through the TLS provider, and
// her entryUUID is the subject of her session.
const (
	teamAccessAnaUserName = "ana"
	teamAccessAnaUUID     = "6ba7b811-9dad-11d1-80b4-00c04fd430c9"
	teamAccessAnaSubject  = "tls-ldap:" + teamAccessAnaUUID
)

// teamAccessUnitList is the part of the unit list that the steps read.
type teamAccessUnitList struct {
	Units []teamAccessUnit `json:"units"`
}

// teamAccessUnit is one unit of the unit list.
type teamAccessUnit struct {
	Reference  string `json:"reference"`
	Collection string `json:"collection"`
}

// TestPathTeamAccess runs the team access steps on each store. Each step that
// a feature of main supports runs for real, and each step that waits for a
// feature that main does not have is skipped with the reason that names what
// it waits for.
func TestPathTeamAccess(t *testing.T) {
	t.Parallel()

	// The LDAP sign-in needs the in-process server: the server binary refuses
	// a directory on the loopback.
	if e2e.UseBinaryServer() {
		t.Skip("the team access path signs a person in through LDAP: the steps run against the in-process server")
	}

	fetcher := sharedFetcher(t)

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

	// The OpenTofu http backend step needs the real OpenTofu CLI, and Go
	// honors SSL_CERT_FILE only on Linux.
	tofuCLI := e2e.NoCLI()
	if runtime.GOOS == "linux" {
		tofuVersions := e2e.OpenTofu().Versions()
		if len(tofuVersions) == 0 {
			t.Fatal("the OpenTofu tool needs at least one version in its matrix")
		}

		tofuCLI, err = fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersions[0])
		if err != nil {
			t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersions[0], err)
		}
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runTeamAccessOnStore(t, cli, tofuCLI, db)
		})
	}
}

// runTeamAccessOnStore runs the steps in order on one store. The steps share
// one environment, so a later step sees the namespace, the stack and the
// grants that an earlier one made.
func runTeamAccessOnStore(t *testing.T, cli, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()

	dir := ldaptest.Start(t)
	dir.AddUser(ldaptest.User{
		Attributes: map[string][]string{"entryUUID": {teamAccessAnaUUID}},
		UserName:   teamAccessAnaUserName,
		Password:   teamAccessAnaPassword,
	})

	env := e2e.NewEnvWithLDAP(ctx, t, cli, store, dir)

	checkReady(ctx, t, env)

	var (
		anaID   string
		groupID string
	)

	t.Run("ldap_sign_in", func(t *testing.T) {
		anaID = teamAccessSignIn(ctx, t, env)
	})

	t.Run("group_rule_maps_payments", func(t *testing.T) {
		teamAccessBlocked(t, env, "group_rule_maps_payments",
			"blocked: no sign-in provider maps a group of a person to a collection on main")
	})

	t.Run("placement_pulumi", func(t *testing.T) {
		teamAccessPlacementPulumi(ctx, t, env)
	})

	t.Run("placement_tofu_http", func(t *testing.T) {
		if tofuCLI.Dir == "" {
			teamAccessBlocked(t, env, "placement_tofu_http",
				"blocked: the OpenTofu http backend needs Linux, where Go honors SSL_CERT_FILE")
			return
		}

		teamAccessPlacementTofuHTTP(ctx, t, env, tofuCLI)
	})

	t.Run("cli_up_and_apply_with_token", func(t *testing.T) {
		groupID = teamAccessAnaUp(ctx, t, env, cli, anaID)
	})

	t.Run("outsider_403_names_collection", func(t *testing.T) {
		teamAccessOutsider(ctx, t, env)
	})

	t.Run("access_review_lists_group_member", func(t *testing.T) {
		teamAccessReviewExport(ctx, t, env)
		teamAccessReviewMember(ctx, t, env, groupID, anaID)
	})

	t.Run("console_members_view", func(t *testing.T) {
		teamAccessBlocked(t, env, "console_members_view", "blocked: the console members view is not built")
	})
}

// teamAccessBlocked records that the step name does not run in this mode, and
// skips it. The reason names the missing capability.
func teamAccessBlocked(t *testing.T, env *e2e.Env, name, reason string) {
	t.Helper()

	env.SkipScenario(t, teamAccessTest+"/"+name, reason)
	t.Skip(reason)
}

// teamAccessPlacementPulumi makes the namespace, a collection payments and a
// rule that sends every stack of the e2e project into it. It then runs
// "pulumi stack init" and checks that the new unit is in payments.
func teamAccessPlacementPulumi(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + teamAccessNamespace + `"}`, http.StatusCreated,
	}, nil)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, teamAccessBase + "/collections", collectionBody(t, "payments"), http.StatusCreated,
	}, nil)

	// A rule names its collection by ID, read after the create above.
	payments := placementCollectionID(ctx, t, env, teamAccessNamespace, "payments")

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, teamAccessRules,
		fmt.Sprintf(`{"tool":"pulumi","project":%q,"stack":"*","collectionID":%q}`, e2e.Project, payments),
		http.StatusCreated,
	}, nil)

	env.Step(ctx, t, step("login", contains(env.URL), "login"))
	env.Step(ctx, t, step("stack init dev", nil,
		"stack", "init", teamAccessNamespace+"/"+e2e.Project+"/dev", "--no-select"))

	got, found := teamAccessCollectionOf(ctx, t, env, "pulumi", e2e.Project+"/dev")
	if !found || got != "payments" {
		t.Fatalf("stack dev is in collection %q (found %t), want %q", got, found, "payments")
	}
}

// teamAccessTofuHTTPApply runs "tofu init" and "tofu apply" with the real
// CLI in the project directory of the path, through the http backend. It
// fails unless the init names the http backend and both commands exit zero.
func teamAccessTofuHTTPApply(ctx context.Context, t *testing.T, env *e2e.Env, tofuCLI e2e.Install, project string) {
	t.Helper()

	backend := []string{"TF_HTTP_USERNAME=dustpan", "TF_HTTP_PASSWORD=" + env.TokenA()}

	res, err := env.RunAs(ctx, tofuCLI, project, backend, "init", "-input=false", "-no-color")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("tofu init exits %d, %v:\n%s", res.ExitCode, err, res.Stderr)
	}

	if !strings.Contains(res.Stdout, `Successfully configured the backend "http"`) {
		t.Fatalf("tofu init did not configure the http backend:\n%s", res.Stdout)
	}

	res, err = env.RunAs(ctx, tofuCLI, project, backend, "apply", "-input=false", "-auto-approve", "-no-color")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("tofu apply exits %d, %v:\n%s", res.ExitCode, err, res.Stderr)
	}
}

// teamAccessPlacementTofuHTTP proves that the OpenTofu http backend places a
// new workspace: a rule sends the workspace db into payments, the real CLI
// writes state through the backend, and a first lock for a workspace that no
// rule matches is refused and makes no unit.
func teamAccessPlacementTofuHTTP(ctx context.Context, t *testing.T, env *e2e.Env, tofuCLI e2e.Install) {
	t.Helper()

	// A rule names its collection by ID. The Pulumi step made the collection.
	payments := placementCollectionID(ctx, t, env, teamAccessNamespace, "payments")

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, teamAccessRules,
		fmt.Sprintf(`{"tool":"opentofu","project":"*","stack":"db","collectionID":%q}`, payments),
		http.StatusCreated,
	}, nil)

	project := filepath.Join(env.Home(), "teamaccess-tofu")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatalf("make %s: %v", project, err)
	}

	writeTofuHTTPConfig(t, project, env.URL+"/tofu/state/"+teamAccessNamespace+"/db")

	teamAccessTofuHTTPApply(ctx, t, env, tofuCLI, project)

	got, found := teamAccessCollectionOf(ctx, t, env, "opentofu", "db")
	if !found || got != "payments" {
		t.Fatalf("the workspace db is in collection %q (found %t), want %q", got, found, "payments")
	}

	// A first lock for a workspace that no rule matches is refused with 422,
	// and the server makes no unit.
	status, body, err := env.TFEStatusA(ctx, "LOCK", "/tofu/state/"+teamAccessNamespace+"/orphan",
		tofuHTTPLockInfo())
	if err != nil || status != http.StatusUnprocessableEntity || !strings.Contains(body, "opendustpan placement add") {
		t.Fatalf("lock of the workspace that no rule matches = %d %s %v, want 422 that names the rules",
			status, body, err)
	}

	if got, found := teamAccessCollectionOf(ctx, t, env, "opentofu", "orphan"); found {
		t.Fatalf("the refused workspace exists in collection %q, want no unit", got)
	}
}

// teamAccessSignIn signs ana in through the TLS LDAP provider with her
// password, checks the session of her browser, and answers the ID of the
// principal that the sign-in made.
func teamAccessSignIn(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	c := teamAccessBrowser{env: env, browser: env.BrowserClient(t)}

	c.signIn(ctx, t)

	var session e2eSession

	status, body := c.send(ctx, t, browserRequest{
		method: http.MethodGet, path: "/api/server/session", body: "", csrf: "",
	})
	if status != http.StatusOK {
		t.Fatalf("read the session of ana = %d %s, want 200", status, body)
	}

	if err := json.Unmarshal([]byte(body), &session); err != nil {
		t.Fatalf("decode the session of ana %s: %v", body, err)
	}

	if session.Subject != teamAccessAnaSubject || session.Provider != e2e.LDAPTLSProvider {
		t.Fatalf("the session of ana = %+v, want the subject %s at %s",
			session, teamAccessAnaSubject, e2e.LDAPTLSProvider)
	}

	var doc struct {
		Principals []e2ePrincipal `json:"principals"`
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodGet,
		"/api/server/principals?prefix=" + url.QueryEscape(teamAccessAnaSubject) + "&kind=person",
		"", http.StatusOK,
	}, &doc)

	for _, p := range doc.Principals {
		if p.Subject == teamAccessAnaSubject && p.ID != "" {
			return p.ID
		}
	}

	t.Fatalf("the principal list names no person with the subject %s", teamAccessAnaSubject)

	return ""
}

// teamAccessAnaUp makes the group payments-dev with the writer role on the
// payments collection, proves that ana's "pulumi up" is refused while she is
// no member, adds her to the group, makes a personal token with her console
// session, and runs "pulumi up" with it. It answers the ID of the group.
func teamAccessAnaUp(ctx context.Context, t *testing.T, env *e2e.Env, cli e2e.Install, anaID string) string {
	t.Helper()

	var group e2eGroup

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups", `{"name":"payments-dev"}`, http.StatusCreated,
	}, &group)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/namespaces/" + teamAccessNamespace + "/collections/payments/grants",
		`{"subject":"group:` + group.ID + `#member","role":"writer"}`,
		http.StatusCreated,
	}, nil)

	// The Pulumi project of ana, in her own directory so that her stack
	// selection changes no file of the administrator.
	project := filepath.Join(env.Home(), "teamaccess-ana")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatalf("make %s: %v", project, err)
	}

	env.WriteFile(t, filepath.Join(project, "Pulumi.yaml"), "name: "+e2e.Project+"\nruntime: yaml\n")

	qualified := teamAccessNamespace + "/" + e2e.Project + "/dev"

	c := teamAccessBrowser{env: env, browser: env.BrowserClient(t)}

	c.signIn(ctx, t)
	secret := c.makeToken(ctx, t, "ana-cli")

	ana := []string{"PULUMI_ACCESS_TOKEN=" + secret}
	up := []string{"up", "--yes", "--skip-preview", "--stack", qualified}

	// Ana is no member of the group yet, so the grant of the group gives her
	// no action on the stack.
	res, err := env.RunAs(ctx, cli, project, ana, up...)
	if err != nil {
		t.Fatalf("ana runs pulumi up before her membership: %v", err)
	}

	scopeRefused(t, res, "pulumi up of ana before her membership")

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups/" + group.ID + "/members",
		`{"principalID":"` + anaID + `"}`, http.StatusCreated,
	}, nil)

	res, err = env.RunAs(ctx, cli, project, ana, up...)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("ana runs pulumi up through her group = %d, %v:\n%s", res.ExitCode, err, res.Stderr)
	}

	return group.ID
}

// teamAccessOutsider proves that a person who holds no grant gets a 403 that
// names the action and the kind of the object, and no part of the object
// itself.
func teamAccessOutsider(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var bob e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("team-outsider") + `","kind":"person"}`,
		http.StatusCreated,
	}, &bob)

	token := env.PersonalToken(ctx, t, bob.ID, "team-outsider-check")

	status, body, err := env.ServerAPIStatusAs(ctx, token.Secret, http.MethodGet,
		teamAccessBase+"/collections/payments", "")
	if err != nil || status != http.StatusForbidden {
		t.Fatalf("bob reads the payments collection = %d %v (%s), want 403", status, err, body)
	}

	// The typed denial names the action and the kind of the object, and no
	// part of the object itself.
	for _, want := range []string{"collection:read", "on this collection"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the denial of bob = %s, want the text %q", body, want)
		}
	}

	for _, part := range []string{teamAccessNamespace, "payments"} {
		if strings.Contains(body, part) {
			t.Fatalf("the denial of bob = %s, names the object part %q", body, part)
		}
	}
}

// teamAccessCollectionOf returns the collection of the unit that the
// arguments tool and name identify in the team access namespace, and false
// when the namespace holds no such unit.
func teamAccessCollectionOf(ctx context.Context, t *testing.T, env *e2e.Env, tool, name string) (string, bool) {
	t.Helper()

	var doc teamAccessUnitList

	status, body := freshServerCall(ctx, t, env, principalStep{
		http.MethodGet, teamAccessBase + "/units", "", http.StatusOK,
	})
	if status != http.StatusOK {
		t.Fatalf("GET units answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the unit list %s: %v", body, err)
	}

	want := tool + ":" + teamAccessNamespace + "/" + name

	for _, u := range doc.Units {
		if u.Reference == want {
			return u.Collection, true
		}
	}

	return "", false
}

// teamAccessReviewExport calls the access review export of the namespace.
func teamAccessReviewExport(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/namespaces/" + teamAccessNamespace + "/access-review", "", http.StatusOK,
	}, nil)
}

// teamAccessReviewMember proves that the access review export of the
// namespace, read with the members of each group, lists ana as a member of
// her group with the writer role.
func teamAccessReviewMember(ctx context.Context, t *testing.T, env *e2e.Env, groupID, anaID string) {
	t.Helper()

	var doc e2eReview

	principalCall(ctx, t, env, principalStep{
		http.MethodGet,
		"/api/server/namespaces/" + teamAccessNamespace + "/access-review?expand=members&limit=100",
		"", http.StatusOK,
	}, &doc)

	groupRef := "group:" + groupID + "#member"

	for i := range doc.Grants {
		r := &doc.Grants[i]

		if r.Subject != groupRef || r.Via == nil || r.Via.Member != "principal:"+anaID {
			continue
		}

		if r.Role != "writer" || r.Via.MemberSubject != teamAccessAnaSubject {
			t.Fatalf("the row of ana = %+v, want the writer role with her subject", r)
		}

		return
	}

	t.Fatalf("the export lists no row of ana through the group %s: %+v", groupRef, doc.Grants)
}

// teamAccessAnaPassword is the password of the LDAP person ana. It travels in
// the body of the sign-in only.
const teamAccessAnaPassword = "right-password-of-ana"

// teamAccessBrowser is the console browser of ana, with its own cookie jar.
type teamAccessBrowser struct {
	env     *e2e.Env
	browser *http.Client
}

// browserRequest is one request of the console browser: the method, the
// path, the body and the CSRF value of the session of ana.
type browserRequest struct {
	method string
	path   string
	body   string
	csrf   string
}

// send sends one request with the browser, with the CSRF value of the
// session when the request holds one, and answers its status and body.
func (c teamAccessBrowser) send(ctx context.Context, t *testing.T, req browserRequest) (int, string) {
	t.Helper()

	request, err := http.NewRequestWithContext(ctx, req.method, c.env.URL+req.path, strings.NewReader(req.body))
	if err != nil {
		t.Fatalf("build %s %s: %v", req.method, req.path, err)
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")

	if req.csrf != "" {
		request.Header.Set("X-Dustpan-Csrf", req.csrf)
	}

	resp, err := c.browser.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", req.method, req.path, err)
	}

	raw, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("%s %s: read the answer: %v, %v", req.method, req.path, readErr, closeErr)
	}

	return resp.StatusCode, string(raw)
}

// signIn sends the password sign-in of ana to the TLS LDAP provider, and
// fails unless it answers 204 with no password in the answer.
func (c teamAccessBrowser) signIn(ctx context.Context, t *testing.T) {
	t.Helper()

	body := fmt.Sprintf(`{"userName":%q,"password":%q}`, teamAccessAnaUserName, teamAccessAnaPassword)

	status, got := c.send(ctx, t, browserRequest{
		method: http.MethodPost,
		path:   "/api/dustpan/v1/sign-in/providers/" + e2e.LDAPTLSProvider + "/password",
		body:   body, csrf: "",
	})
	if status != http.StatusNoContent {
		t.Fatalf("the sign-in of ana = %d %s, want 204", status, got)
	}

	if strings.Contains(got, teamAccessAnaPassword) {
		t.Fatal("the answer of the sign-in of ana holds her password")
	}
}

// makeToken makes a personal token with the console session of ana, as the
// console does, and answers its secret.
func (c teamAccessBrowser) makeToken(ctx context.Context, t *testing.T, name string) string {
	t.Helper()

	status, body := c.send(ctx, t, browserRequest{
		method: http.MethodGet, path: "/api/server/session", body: "", csrf: "",
	})
	if status != http.StatusOK {
		t.Fatalf("read the session of ana = %d %s, want 200", status, body)
	}

	var session e2eSession

	if err := json.Unmarshal([]byte(body), &session); err != nil || session.CSRFToken == "" {
		t.Fatalf("decode the session of ana %s: %v", body, err)
	}

	status, body = c.send(ctx, t, browserRequest{
		method: http.MethodPost, path: "/api/server/caller/tokens",
		body: `{"name":"` + name + `"}`, csrf: session.CSRFToken,
	})
	if status != http.StatusCreated {
		t.Fatalf("ana makes a personal token with her session = %d %s, want 201", status, body)
	}

	var token e2e.PersonalToken

	if err := json.Unmarshal([]byte(body), &token); err != nil || token.Secret == "" {
		t.Fatalf("decode the personal token of ana %s: %v", body, err)
	}

	return token.Secret
}
