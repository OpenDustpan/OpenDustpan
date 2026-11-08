//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// e2eReviewRow and e2eReview decode the access-review wire forms.
type e2eReviewRow struct {
	Via               *e2eReviewVia `json:"via"`
	RevokedAtMs       *int64        `json:"revokedAtMs"`
	LastAccessTracked *bool         `json:"lastAccessTracked"`
	LastAccessAtMs    *int64        `json:"lastAccessAtMs"`
	MemberCount       *int64        `json:"memberCount"`
	GrantedAtMs       *int64        `json:"grantedAtMs"`
	EffectiveFromMs   *int64        `json:"effectiveFromMs"`
	GrantID           string        `json:"grantID"`
	Subject           string        `json:"subject"`
	SubjectName       string        `json:"subjectName"`
	Holder            string        `json:"holder"`
	Role              string        `json:"role"`
	Object            string        `json:"object"`
	ObjectState       string        `json:"objectState"`
	InheritedFrom     string        `json:"inheritedFrom"`
	GrantedBy         string        `json:"grantedBy"`
	CanGrantAccess    bool          `json:"canGrantAccess"`
}

type e2eReviewVia struct {
	MemberUntilMs *int64 `json:"memberUntilMs"`
	Group         string `json:"group"`
	Member        string `json:"member"`
	MemberSubject string `json:"memberSubject"`
	MemberFromMs  int64  `json:"memberFromMs"`
}

type e2eReview struct {
	Total      *int64         `json:"total"`
	AsOfMs     *int64         `json:"asOfMs"`
	Grants     []e2eReviewRow `json:"grants"`
	NextCursor string         `json:"nextCursor"`
}

// e2eAccess and e2eAccessObjects decode the two forms of the access of the
// caller. MissingActions names each other grantable action that the caller
// does not hold on the object, with the roles that give it.
type e2eAccess struct {
	Object         string             `json:"object"`
	Actions        []string           `json:"actions"`
	Grants         []e2eGrant         `json:"grants"`
	MissingActions []e2eMissingAction `json:"missingActions"`
}

// e2eMissingAction is one missing action with the roles that give it.
type e2eMissingAction struct {
	Action    string       `json:"action"`
	GrantedBy e2eGrantedBy `json:"grantedBy"`
}

// e2eGrantedBy names the built-in and custom roles that give one action.
type e2eGrantedBy struct {
	BuiltInRoles []string `json:"builtInRoles"`
	CustomRoles  []string `json:"customRoles"`
}

type e2eAccessObjects struct {
	Objects    []e2eAccessObject `json:"objects"`
	NextCursor string            `json:"nextCursor"`
	Total      int64             `json:"total"`
	All        bool              `json:"all"`
}

type e2eAccessObject struct {
	Object string `json:"object"`
	Kind   string `json:"kind"`
}

// reviewFixture holds what the access-review steps made.
type reviewFixture struct {
	carol      e2ePrincipal
	reviewers  e2eGroup
	unitGrant  e2eGrant
	carolRef   string
	groupRef   string
	beforeAtMs int64
}

// TestAccessReview drives the access-review export and the access of the
// caller (ADR 52, decisions 10, 11 and 14) through the real binary, on each
// store. No CLI calls these routes, so the harness calls them with the
// bootstrap admin token.
func TestAccessReview(t *testing.T) {
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
			runReviewOnStore(t, cli, db)
		})
	}
}

// runReviewOnStore runs the access-review steps on one store.
func runReviewOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	f := seedReview(ctx, t, env)

	checkReviewScopes(ctx, t, env, f)
	checkReviewPrincipal(ctx, t, env, f)
	checkReviewExpand(ctx, t, env, f)

	secret := carolToken(ctx, t, env, f)
	checkCallerGrants(ctx, t, env, secret, []string{
		"account-administrator @server via reviewers", "auditor review", "installation-operator @server",
		"maintainer pulumi:review/web/prod", "writer review via reviewers",
	})

	checkReviewHistory(ctx, t, env, f)

	// The revoke and the end of the membership take their grants off the
	// list of carol.
	checkCallerGrants(ctx, t, env, secret, []string{"auditor review", "installation-operator @server"})

	checkReviewRefusals(ctx, t, env)
	checkCallerAccess(ctx, t, env)
	checkCallerAccessRoles(ctx, t, env)

	t.Run("collection", func(t *testing.T) {
		checkCollectionReview(ctx, t, env)
	})
}

// seedReview makes a principal in a group, a namespace and five grants:
//
//   - installation-operator to carol on the installation,
//   - account-administrator to the group on the installation,
//   - writer to the group on the namespace,
//   - auditor to carol on the namespace,
//   - maintainer to carol on a unit.
func seedReview(ctx context.Context, t *testing.T, env *e2e.Env) reviewFixture {
	t.Helper()

	var f reviewFixture

	f.beforeAtMs = time.Now().UnixMilli() - 1

	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("carol") + `","kind":"person"}`,
		http.StatusCreated,
	}, &f.carol)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups", `{"name":"reviewers"}`, http.StatusCreated,
	}, &f.reviewers)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups/" + f.reviewers.ID + "/members",
		`{"principalID":"` + f.carol.ID + `"}`, http.StatusCreated,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"review"}`, http.StatusCreated,
	}, nil)
	createStack(ctx, t, env, "review/web/prod")

	f.carolRef = "principal:" + f.carol.ID
	f.groupRef = "group:" + f.reviewers.ID + "#member"

	for _, g := range []struct{ path, subject, role string }{
		{"/api/server/grants", f.carolRef, "installation-operator"},
		{"/api/server/grants", f.groupRef, "account-administrator"},
		{"/api/server/namespaces/review/grants", f.groupRef, "writer"},
		{"/api/server/namespaces/review/grants", f.carolRef, "auditor"},
	} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, g.path, `{"subject":"` + g.subject + `","role":"` + g.role + `"}`, http.StatusCreated,
		}, nil)
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/review/unit-grants?tool=pulumi&unit=web%2Fprod",
		`{"subject":"` + f.carolRef + `","role":"maintainer"}`, http.StatusCreated,
	}, &f.unitGrant)

	return f
}

// reviewOf reads every page of one export, two rows at a time, and returns
// the rows of grants. It checks the total of the first page, that the next
// pages answer none, that every page reads the snapshot of the first, and
// that the first page of an export with no subject holds one row for each
// of the two bootstrap admin tokens of the harness.
func reviewOf(ctx context.Context, t *testing.T, env *e2e.Env, path string, query url.Values) []e2eReviewRow {
	t.Helper()

	var (
		rows, tokens []e2eReviewRow
		total, asOf  *int64
	)

	query = maps.Clone(query)
	query.Set("limit", "2")

	for {
		var page e2eReview
		principalCall(ctx, t, env, principalStep{
			http.MethodGet, path + "?" + query.Encode(), "", http.StatusOK,
		}, &page)

		if query.Get("cursor") == "" {
			total, asOf = page.Total, page.AsOfMs
		} else if page.Total != nil || !sameMillis(page.AsOfMs, asOf) {
			t.Fatalf("%s next page = total %v as of %v, want no total and the time of the first page",
				path, page.Total, page.AsOfMs)
		}

		tokens = append(tokens, tokenRows(page.Grants)...)
		rows = append(rows, grantRows(page.Grants)...)

		if page.NextCursor == "" {
			break
		}

		query.Set("cursor", page.NextCursor)
	}

	read := reviewRead{path: path, total: total, rows: len(rows), tokens: len(tokens)}
	read.check(t, bootstrapRows(query))

	return rows
}

// reviewRead is what the pages of one export held: the total of the first
// page, and the count of the rows of grants and of bootstrap admin tokens.
type reviewRead struct {
	total  *int64
	path   string
	rows   int
	tokens int
}

// check checks the total against the rows, and the bootstrap rows against
// wantTokens.
func (r reviewRead) check(t *testing.T, wantTokens int) {
	t.Helper()

	if r.total == nil || *r.total != int64(r.rows+r.tokens) {
		t.Fatalf("%s total = %v, read %d rows", r.path, r.total, r.rows+r.tokens)
	}

	if r.tokens != wantTokens {
		t.Fatalf("%s bootstrap rows = %d, want %d", r.path, r.tokens, wantTokens)
	}
}

// tokenRows returns the rows of rows with a holder: the bootstrap admin
// tokens.
func tokenRows(rows []e2eReviewRow) []e2eReviewRow {
	return slices.DeleteFunc(slices.Clone(rows), func(r e2eReviewRow) bool { return r.Holder == "" })
}

// grantRows returns the rows of rows with no holder: the rows of grants.
func grantRows(rows []e2eReviewRow) []e2eReviewRow {
	return slices.DeleteFunc(slices.Clone(rows), func(r e2eReviewRow) bool { return r.Holder != "" })
}

// bootstrapRows returns the count of the rows of bootstrap admin tokens in
// an export with query: one for each of the two tokens of the harness, or
// none with a subject filter.
func bootstrapRows(query url.Values) int {
	if query.Get("subject") != "" {
		return 0
	}

	return 2
}

// sameMillis reports whether a and b are both nil or the same time.
func sameMillis(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// reviewRoles returns "role@object<inheritedFrom" for each row, sorted:
// grants made in the same millisecond come in the order of their IDs.
func reviewRoles(rows []e2eReviewRow) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		r := &rows[i]

		out = append(out, r.Role+"@"+r.Object+"<"+r.InheritedFrom)
	}

	slices.Sort(out)

	return out
}

// sorted returns a sorted copy of s.
func sorted(s ...string) []string {
	out := slices.Clone(s)
	slices.Sort(out)

	return out
}

// checkReviewScopes reads the snapshot of each scope. A scope lists the
// grants on its object and inside it, and the grants with data access on
// the objects that contain it.
func checkReviewScopes(ctx context.Context, t *testing.T, env *e2e.Env, f reviewFixture) {
	t.Helper()

	for _, c := range []struct {
		path  string
		query url.Values
		want  []string
	}{
		{"/api/server/access-review", url.Values{}, sorted(
			"installation-operator@@server<", "site-administrator@@server<", "account-administrator@@server<",
			"writer@review<", "auditor@review<", "maintainer@pulumi:review/web/prod<",
		)},
		{"/api/server/namespaces/review/access-review", url.Values{}, sorted(
			"account-administrator@@server<@server", "site-administrator@@server<@server",
			"writer@review<", "auditor@review<", "maintainer@pulumi:review/web/prod<",
		)},
		{"/api/server/namespaces/review/unit-access-review", url.Values{"tool": {"pulumi"}, "unit": {"web/prod"}}, sorted(
			"account-administrator@@server<@server", "site-administrator@@server<@server",
			"writer@review<review", "auditor@review<review",
			"maintainer@pulumi:review/web/prod<",
		)},
	} {
		got := reviewRoles(reviewOf(ctx, t, env, c.path, c.query))
		if !slices.Equal(got, c.want) {
			t.Fatalf("%s rows = %q, want %q", c.path, got, c.want)
		}
	}

	rows := reviewOf(ctx, t, env, "/api/server/access-review", url.Values{})
	for i := range rows {
		// The site administrator of the fresh sessions is not part of the seed.
		if rows[i].GrantID == "grt-e2e-fresh-admin" {
			continue
		}

		checkReviewRow(t, f, &rows[i])
	}
}

// checkReviewRow checks the fields of one current row of the seed.
func checkReviewRow(t *testing.T, f reviewFixture, r *e2eReviewRow) {
	t.Helper()

	if !isCarolRow(f, r) && !isGroupRow(f, r) {
		t.Fatalf("row = %+v, want carol with a tracked last access, or reviewers with 1 member", r)
	}

	if r.GrantedAtMs == nil || *r.GrantedAtMs < f.beforeAtMs || r.GrantedBy == "" || r.RevokedAtMs != nil ||
		r.ObjectState != "live" || r.CanGrantAccess != (r.Role == "account-administrator") {
		t.Fatalf("row = %+v, want a current grant made after %d on a live object", r, f.beforeAtMs)
	}
}

// isCarolRow reports whether r is a row of carol. No token of carol was
// used yet, so a principal row is tracked with no last access time.
func isCarolRow(f reviewFixture, r *e2eReviewRow) bool {
	return r.Subject == f.carolRef && r.SubjectName == e2e.SignInSubject("carol") && r.LastAccessTracked != nil &&
		*r.LastAccessTracked && r.LastAccessAtMs == nil && r.MemberCount == nil
}

// isGroupRow reports whether r is a row of the group with its one member.
// A group row has no last access field.
func isGroupRow(f reviewFixture, r *e2eReviewRow) bool {
	return r.Subject == f.groupRef && r.SubjectName == "reviewers" && r.MemberCount != nil &&
		*r.MemberCount == 1 && r.LastAccessTracked == nil && r.LastAccessAtMs == nil
}

// checkReviewPrincipal filters the export to one principal: its own grants
// and, with via set, the grants of its groups.
func checkReviewPrincipal(ctx context.Context, t *testing.T, env *e2e.Env, f reviewFixture) {
	t.Helper()

	rows := reviewOf(ctx, t, env, "/api/server/access-review", url.Values{"subject": {f.carolRef}})
	if len(rows) != 5 {
		t.Fatalf("rows of carol = %q, want 5", reviewRoles(rows))
	}

	via := 0

	for i := range rows {
		r := &rows[i]

		if r.Subject == f.groupRef {
			if r.Via == nil || r.Via.Group != f.groupRef || r.Via.MemberUntilMs != nil {
				t.Fatalf("group row of carol = %+v, want via %s", r, f.groupRef)
			}

			via++
		} else if r.Via != nil {
			t.Fatalf("own row of carol = %+v, want no via", r)
		}
	}

	if via != 2 {
		t.Fatalf("rows of carol through a group = %d, want 2", via)
	}

	group := reviewOf(ctx, t, env, "/api/server/namespaces/review/access-review", url.Values{"subject": {f.groupRef}})
	want := sorted("account-administrator@@server<@server", "writer@review<")
	if got := reviewRoles(group); !slices.Equal(got, want) {
		t.Fatalf("rows of the group = %q", got)
	}
}

// checkReviewExpand reads the export of the namespace with each member of
// a group in its own row.
func checkReviewExpand(ctx context.Context, t *testing.T, env *e2e.Env, f reviewFixture) {
	t.Helper()

	rows := reviewOf(ctx, t, env, "/api/server/namespaces/review/access-review", url.Values{"expand": {"members"}})

	members := 0

	for i := range rows {
		r := &rows[i]
		if r.Subject != f.groupRef {
			continue
		}

		if r.Via == nil || r.Via.Member != f.carolRef || r.Via.MemberSubject != e2e.SignInSubject("carol") ||
			r.EffectiveFromMs == nil ||
			*r.EffectiveFromMs < r.Via.MemberFromMs {
			t.Fatalf("expanded row = %+v, want carol through the group", r)
		}

		members++
	}

	if members != 2 {
		t.Fatalf("expanded rows of the group = %d, want 2", members)
	}
}

// checkReviewHistory revokes a grant and removes the member, then reads the
// snapshot and the history.
func checkReviewHistory(ctx context.Context, t *testing.T, env *e2e.Env, f reviewFixture) {
	t.Helper()

	revoke := "/api/server/namespaces/review/unit-grants/" + f.unitGrant.ID + "?tool=pulumi&unit=web%2Fprod"
	principalCall(ctx, t, env, principalStep{http.MethodDelete, revoke, "", http.StatusNoContent}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/groups/" + f.reviewers.ID + "/members/" + f.carol.ID, "", http.StatusNoContent,
	}, nil)

	if rows := reviewOf(ctx, t, env, "/api/server/access-review", url.Values{}); len(rows) != 5 {
		t.Fatalf("snapshot after the revoke = %q, want 5 rows, with the site administrator of the fresh sessions",
			reviewRoles(rows))
	}

	if rows := reviewOf(ctx, t, env, "/api/server/access-review", url.Values{"subject": {f.carolRef}}); len(rows) != 2 {
		t.Fatalf("snapshot of carol after she left = %q, want her 2 own rows", reviewRoles(rows))
	}

	window := url.Values{
		"from": {strconv.FormatInt(f.beforeAtMs, 10)},
		"to":   {strconv.FormatInt(time.Now().UnixMilli()+1, 10)},
	}

	rows := reviewOf(ctx, t, env, "/api/server/access-review", window)
	if len(rows) != 6 {
		t.Fatalf("history = %q, want 6 rows, with the site administrator of the fresh sessions", reviewRoles(rows))
	}

	revoked := 0

	for i := range rows {
		r := &rows[i]

		if r.RevokedAtMs != nil {
			revoked++

			if r.GrantID != f.unitGrant.ID {
				t.Fatalf("revoked row = %+v, want %s", r, f.unitGrant.ID)
			}
		}
	}

	if revoked != 1 {
		t.Fatalf("revoked rows in the history = %d, want 1", revoked)
	}

	checkDeletedUnitInHistory(ctx, t, env, f, window)

	window.Set("subject", f.carolRef)

	checkCarolHistory(ctx, t, env, window)
}

// checkCarolHistory reads the history of carol: every membership ended,
// and every row tracked with a last access time, since she called with her
// token.
func checkCarolHistory(ctx context.Context, t *testing.T, env *e2e.Env, window url.Values) {
	t.Helper()

	rows := reviewOf(ctx, t, env, "/api/server/access-review", window)
	for i := range rows {
		r := &rows[i]

		if r.Via != nil && r.Via.MemberUntilMs == nil {
			t.Fatalf("history row of carol = %+v, want the end of her membership", r)
		}

		if r.LastAccessTracked == nil || !*r.LastAccessTracked || r.LastAccessAtMs == nil {
			t.Fatalf("history row of carol = %+v, want a tracked last access with a time", r)
		}
	}
}

// checkReviewRefusals sends the queries that the export refuses.
func checkReviewRefusals(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, query := range []string{
		"from=1", "to=2", "from=5&to=5", "from=x&to=2", "subject=carol", "limit=0", "cursor=%21",
	} {
		principalCall(ctx, t, env, principalStep{
			http.MethodGet, "/api/server/access-review?" + query, "", http.StatusBadRequest,
		}, nil)
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/namespaces/missing/access-review", "", http.StatusNotFound,
	}, nil)
}

// checkCallerAccess reads the access of the caller on one object and by
// object kind. The bootstrap admin holds every action through no grant.
func checkCallerAccess(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var one e2eAccess
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/caller/access?object=review", "", http.StatusOK,
	}, &one)

	if one.Object != "review" || len(one.Actions) == 0 || len(one.Grants) != 0 {
		t.Fatalf("access on review = %+v, want every action and no grant", one)
	}

	var kinds e2eAccessObjects
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/caller/access?type=unit&limit=10", "", http.StatusOK,
	}, &kinds)

	if !kinds.All || len(kinds.Objects) != 0 || kinds.Total != 0 {
		t.Fatalf("access by type = %+v, want all", kinds)
	}

	for _, query := range []string{"", "object=review&type=unit", "type=group", "object=a%20b", "type=unit&action=nope"} {
		principalCall(ctx, t, env, principalStep{
			http.MethodGet, "/api/server/caller/access?" + query, "", http.StatusBadRequest,
		}, nil)
	}
}

// checkCallerAccessRoles proves what a refused caller learns about the
// roles that give a missing action: erin holds no grant, so the missing
// unit:read-secrets lists the built-in roles that give it and the custom
// role that gives it.
func checkCallerAccessRoles(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"roles"}`, http.StatusCreated,
	}, nil)

	var revealer e2eRole
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles", `{"name":"secret-revealer","actions":["unit:read-secrets"]}`,
		http.StatusCreated,
	}, &revealer)

	var erin e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("erin") + `","kind":"person"}`,
		http.StatusCreated,
	}, &erin)

	erinSecret := callerToken(ctx, t, env, erin.ID)

	// The built-in roles that hold unit:read-secrets, in catalog order.
	builtIn := []string{"secret-viewer", "writer", "maintainer", "admin", "account-administrator", "site-administrator"}

	got := callerAccessAs(ctx, t, env, erinSecret, "/api/server/caller/access?object=roles")
	if got.Object != "roles" || len(got.Actions) != 0 || len(got.Grants) != 0 {
		t.Fatalf("access of erin on roles = %+v, want no action and no grant", got)
	}

	checkRoleGrantedBy(t, got, roleGrantedByWant{
		action: "unit:read-secrets", builtIn: builtIn, custom: []string{revealer.ID},
	})
}

// callerToken returns the secret of a personal token that the person
// principalID makes after a sign-in.
func callerToken(ctx context.Context, t *testing.T, env *e2e.Env, principalID string) string {
	t.Helper()

	return env.PersonalToken(ctx, t, principalID, "roles").Secret
}

// callerAccessAs reads the access of the caller of secret on path.
func callerAccessAs(ctx context.Context, t *testing.T, env *e2e.Env, secret, path string) e2eAccess {
	t.Helper()

	status, body, err := env.ServerAPIStatusAs(ctx, secret, http.MethodGet, path, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s = %d, %v (%s), want 200", path, status, err, body)
	}

	var out e2eAccess
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}

	return out
}

// roleGrantedByWant is one expected missing action: its built-in roles and
// its custom roles.
type roleGrantedByWant struct {
	action  string
	builtIn []string
	custom  []string
}

// checkRoleGrantedBy fails unless access names the missing action of want
// with its built-in roles and its custom roles.
func checkRoleGrantedBy(t *testing.T, access e2eAccess, want roleGrantedByWant) {
	t.Helper()

	for i := range access.MissingActions {
		m := &access.MissingActions[i]
		if m.Action != want.action {
			continue
		}

		if !slices.Equal(m.GrantedBy.BuiltInRoles, want.builtIn) ||
			!slices.Equal(m.GrantedBy.CustomRoles, want.custom) {
			t.Fatalf(
				"missing %s = built-in %q custom %q, want built-in %q custom %q",
				m.Action, m.GrantedBy.BuiltInRoles, m.GrantedBy.CustomRoles,
				want.builtIn, want.custom,
			)
		}

		return
	}

	t.Fatalf("missing actions = %+v, want %s", access.MissingActions, want.action)
}

// carolToken returns the secret of a personal token that carol makes after
// a sign-in.
func carolToken(ctx context.Context, t *testing.T, env *e2e.Env, f reviewFixture) string {
	t.Helper()

	return env.PersonalToken(ctx, t, f.carol.ID, "review").Secret
}

// checkCallerGrants reads the grants of the caller of secret two rows a
// page, and fails unless they are want, sorted, each as its role, its
// object and the group it comes through. The bootstrap admin holds none,
// and a bad cursor answers 400.
func checkCallerGrants(ctx context.Context, t *testing.T, env *e2e.Env, secret string, want []string) {
	t.Helper()

	var got []string

	path := "/api/server/caller/grants?limit=2"

	for page := 0; path != ""; page++ {
		doc := callerGrantsCall(ctx, t, env, secret, path)

		if (page == 0) != (doc.Total != nil) || (doc.Total != nil && *doc.Total != int64(len(want))) {
			t.Fatalf("page %d total = %v, want %d on the first page only", page, doc.Total, len(want))
		}

		got = append(got, callerGrantRows(doc)...)

		path = ""
		if doc.NextCursor != "" {
			path = "/api/server/caller/grants?limit=2&cursor=" + doc.NextCursor
		}
	}

	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Fatalf("grants of the caller = %q, want %q", got, want)
	}

	var admin e2eReview
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/caller/grants", "", http.StatusOK,
	}, &admin)

	if admin.Total == nil || *admin.Total != 0 || len(admin.Grants) != 0 {
		t.Fatalf("grants of the bootstrap admin = %+v, want none", admin)
	}

	status, body, err := env.ServerAPIStatusAs(ctx, secret, http.MethodGet, "/api/server/caller/grants?cursor=%21", "")
	if err != nil || status != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d, %v (%s), want 400", status, err, body)
	}
}

// callerGrantRows returns each row of doc as its role, its object and the
// name of the group it comes through.
func callerGrantRows(doc e2eReview) []string {
	out := make([]string, 0, len(doc.Grants))

	for i := range doc.Grants {
		r := &doc.Grants[i]
		row := r.Role + " " + r.Object

		if r.Via != nil {
			row += " via " + r.SubjectName
		}

		out = append(out, row)
	}

	return out
}

// callerGrantsCall sends one GET of path with the token secret, fails
// unless it answers 200, and returns the decoded answer.
func callerGrantsCall(ctx context.Context, t *testing.T, env *e2e.Env, secret, path string) e2eReview {
	t.Helper()

	status, body, err := env.ServerAPIStatusAs(ctx, secret, http.MethodGet, path, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s = %d, %v (%s), want 200", path, status, err, body)
	}

	var out e2eReview
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}

	return out
}

// checkDeletedUnitInHistory deletes the stack of the revoked unit grant:
// the history still shows the grant, on a deleted object with its name.
func checkDeletedUnitInHistory(ctx context.Context, t *testing.T, env *e2e.Env, f reviewFixture, window url.Values) {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodDelete, "/api/stacks/review/web/prod", "")
	if err != nil || status != http.StatusNoContent {
		t.Fatalf("delete stack = %d, %v (%s), want 204", status, err, body)
	}

	window = maps.Clone(window)
	window.Set("to", strconv.FormatInt(time.Now().UnixMilli()+1, 10))

	rows := reviewOf(ctx, t, env, "/api/server/access-review", window)
	for i := range rows {
		r := &rows[i]
		if r.GrantID == f.unitGrant.ID && (r.ObjectState != "deleted" || r.Object != "pulumi:review/web/prod") {
			t.Fatalf("grant on the deleted stack = %+v, want pulumi:review/web/prod, deleted", r)
		}
	}
}
