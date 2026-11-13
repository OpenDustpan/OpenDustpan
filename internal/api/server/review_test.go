package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

type reviewRowDoc struct {
	Via               *reviewViaDoc `json:"via"`
	LastAccessTracked *bool         `json:"lastAccessTracked"`
	LastAccessAtMs    *int64        `json:"lastAccessAtMs"`
	Subject           string        `json:"subject"`
	SubjectName       string        `json:"subjectName"`
	Holder            string        `json:"holder"`
	Role              string        `json:"role"`
	Object            string        `json:"object"`
	ObjectState       string        `json:"objectState"`
	InheritedFrom     string        `json:"inheritedFrom"`
	CanGrantAccess    bool          `json:"canGrantAccess"`
}

type reviewViaDoc struct {
	Group  string `json:"group"`
	Member string `json:"member"`
}

type reviewDoc struct {
	Total      *int64         `json:"total"`
	AsOfMs     *int64         `json:"asOfMs"`
	Grants     []reviewRowDoc `json:"grants"`
	NextCursor string         `json:"nextCursor"`
}

type accessDoc struct {
	Object  string     `json:"object"`
	Actions []string   `json:"actions"`
	Grants  []grantDoc `json:"grants"`
}

type accessObjectDoc struct {
	Object string `json:"object"`
	Kind   string `json:"kind"`
}

type accessObjectsDoc struct {
	Objects    []accessObjectDoc `json:"objects"`
	Action     string            `json:"action"`
	NextCursor string            `json:"nextCursor"`
	Total      int64             `json:"total"`
	All        bool              `json:"all"`
}

// TestReviewScopesAndActions reads the exports as callers with grants: the
// namespace admin reads its namespace with the installation grant that
// gives data access, and not the operator grant. Only a holder of
// server:read-access reads the export of the installation.
func TestReviewScopesAndActions(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var ns reviewDoc
	f.as(t, "alice", apiCall{http.MethodGet, "/api/server/namespaces/acme/access-review", "", http.StatusOK}, &ns)

	got := make([]string, 0, len(ns.Grants))
	for _, g := range ns.Grants {
		got = append(got, fmt.Sprintf("%s%s %s %s <%s grant=%t", g.Subject, g.Holder, g.Role, g.Object,
			g.InheritedFrom, g.CanGrantAccess))
	}

	slices.Sort(got)

	// The bootstrap admin token holds every action with no grant. An account
	// administrator and a namespace admin can both change who has access.
	want := []string{
		"bootstrap admin token admin @server <@server grant=true",
		"principal:prn-alice admin acme < grant=true",
		"principal:prn-ann account-administrator @server <@server grant=true",
	}
	if !slices.Equal(got, want) || ns.Total == nil || *ns.Total != 3 || ns.AsOfMs == nil {
		t.Fatalf("export of acme = %q (total %v, as of %v), want %q", got, ns.Total, ns.AsOfMs, want)
	}

	checkTrackedRows(t, ns.Grants)

	for _, c := range []struct {
		subject string
		path    string
		want    int
	}{
		{"alice", "/api/server/access-review", http.StatusForbidden},
		{"olga", "/api/server/access-review", http.StatusForbidden},
		{"olga", "/api/server/namespaces/acme/access-review", http.StatusForbidden},
		{"bob", "/api/server/namespaces/acme/access-review", http.StatusForbidden},
		{"ann", "/api/server/access-review", http.StatusOK},
		{"ann", "/api/server/namespaces/acme/access-review?subject=principal:prn-olga", http.StatusOK},
	} {
		f.as(t, c.subject, apiCall{http.MethodGet, c.path, "", c.want}, nil)
	}

	var all reviewDoc
	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/access-review", "", http.StatusOK}, &all)

	if all.Total == nil || *all.Total != 4 {
		t.Fatalf("export of the installation = %+v, want the 3 seeded grants and the bootstrap admin", all)
	}

	f.as(t, "ann", apiCall{
		http.MethodGet, "/api/server/namespaces/acme/collections/payments/access-review", "", http.StatusNotImplemented,
	}, nil)
	f.as(t, "ann", apiCall{
		http.MethodGet, "/api/server/access-review?expand=everyone", "", http.StatusBadRequest,
	}, nil)
}

// checkTrackedRows checks that each principal row says that the last
// access is tracked with no last access time, that a group row and a
// bootstrap row carry neither field, and that every object is live.
func checkTrackedRows(t *testing.T, rows []reviewRowDoc) {
	t.Helper()

	for i := range rows {
		g := &rows[i]

		tracked := g.LastAccessTracked != nil && *g.LastAccessTracked
		if g.LastAccessAtMs != nil || tracked != (g.Holder == "") || g.ObjectState != access.ObjectLive {
			t.Fatalf(
				"row %+v: want lastAccessTracked true on a principal row, no lastAccessAtMs, and a live object",
				g,
			)
		}
	}
}

// TestReviewWritesAnAuditEntry checks that each export request writes one
// entry of the kind read-access (ADR 52, decision 13): in @server for the
// installation, in the namespace for a namespace export. A read of the
// access of the caller writes none.
func TestReviewWritesAnAuditEntry(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	latest := func(scope string) audit.Entry {
		listing, err := f.store.List(t.Context(), namespace.Name(scope), audit.Page{Number: 1, Size: 1})
		if err != nil || len(listing.Entries) != 1 {
			t.Fatalf("list %s entries: %v", scope, err)
		}

		return listing.Entries[0]
	}

	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/access-review?from=0&to=60000", "", http.StatusOK}, nil)

	if e := latest("@server"); !isReadAccess(e, "ann", "server:read-access") ||
		e.Object != "access-review @server history 0-60000" {
		t.Fatalf("entry of the installation export = %+v", e)
	}

	f.as(t, "alice", apiCall{
		http.MethodGet, "/api/server/namespaces/acme/access-review?subject=principal:prn-bob&expand=members", "",
		http.StatusOK,
	}, nil)

	if e := latest("acme"); !isReadAccess(e, "alice", "namespace:read-access") ||
		!strings.HasPrefix(e.Object, "access-review acme snapshot asOf=") ||
		!strings.HasSuffix(e.Object, " subject=principal:prn-bob expand=members") {
		t.Fatalf("entry of the namespace export = %+v", e)
	}

	before := latest("@server")

	f.as(t, "alice", apiCall{http.MethodGet, "/api/server/caller/access?object=acme", "", http.StatusOK}, nil)

	if after := latest("@server"); after.Sequence != before.Sequence {
		t.Fatalf("caller access wrote %+v, want no entry", after)
	}
}

// isReadAccess reports whether e is a read-access entry of subject with the
// action action.
func isReadAccess(e audit.Entry, subject, action string) bool {
	return e.Kind == audit.KindReadAccess && e.Subject == subject && e.Action == action
}

// TestReviewPagesOneSnapshot reads an export in pages: the first page
// answers the total and the time of the snapshot, the next pages keep that
// time and answer no total.
func TestReviewPagesOneSnapshot(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var first reviewDoc
	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/access-review?limit=2", "", http.StatusOK}, &first)

	// The bootstrap row counts inside the limit.
	if first.Total == nil || *first.Total != 4 || first.AsOfMs == nil || first.NextCursor == "" ||
		len(first.Grants) != 2 || first.Grants[0].Holder != access.BootstrapHolder {
		t.Fatalf("first page = %+v, want the bootstrap row, 1 grant, a total and a time", first)
	}

	var next reviewDoc
	f.as(t, "ann", apiCall{
		http.MethodGet, "/api/server/access-review?limit=2&cursor=" + first.NextCursor, "", http.StatusOK,
	}, &next)

	if next.Total != nil || next.AsOfMs == nil || *next.AsOfMs != *first.AsOfMs || len(next.Grants) != 2 {
		t.Fatalf("second page = %+v, want 2 grants, no total, and the time of the first page", next)
	}
}

// TestCallerAccess reads the access of callers with and without grants.
func TestCallerAccess(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var one accessDoc
	f.as(t, "alice", apiCall{http.MethodGet, "/api/server/caller/access?object=acme", "", http.StatusOK}, &one)

	if one.Object != "acme" || !slices.Contains(one.Actions, "namespace:read-access") ||
		len(one.Grants) != 1 || one.Grants[0].Role != "admin" {
		t.Fatalf("access of alice on acme = %+v, want the admin grant and its actions", one)
	}

	var none accessDoc
	f.as(t, "bob", apiCall{http.MethodGet, "/api/server/caller/access?object=acme", "", http.StatusOK}, &none)

	if len(none.Actions) != 0 || len(none.Grants) != 0 {
		t.Fatalf("access of bob on acme = %+v, want none", none)
	}

	for _, c := range []struct {
		subject, query string
		want           string
	}{
		{"alice", "type=unit", "unit:read [acme:namespace] all=false total=1"},
		{"alice", "type=namespace&action=namespace:read-access", "namespace:read-access [acme:namespace] all=false total=1"},
		{"olga", "type=namespace", "namespace:read [] all=false total=0"},
		{"ann", "type=collection", "collection:read [] all=true total=0"},
		{"bob", "type=unit", "unit:read [] all=false total=0"},
	} {
		var page accessObjectsDoc
		f.as(t, c.subject, apiCall{
			http.MethodGet, "/api/server/caller/access?" + c.query, "", http.StatusOK,
		}, &page)

		objs := make([]string, 0, len(page.Objects))
		for _, o := range page.Objects {
			objs = append(objs, o.Object+":"+o.Kind)
		}

		got := fmt.Sprintf("%s [%s] all=%t total=%d", page.Action, strings.Join(objs, " "), page.All, page.Total)
		if got != c.want {
			t.Fatalf("access of %s by %s = %q, want %q", c.subject, c.query, got, c.want)
		}
	}

	for _, query := range []string{"", "object=acme&type=unit", "type=group", "type=unit&action=nope", "type=unit&limit=0"} {
		f.as(t, "alice", apiCall{
			http.MethodGet, "/api/server/caller/access?" + query, "", http.StatusBadRequest,
		}, nil)
	}
}

// grantedByDoc, missingActionDoc and callerAccessFullDoc decode the access
// of the caller with the roles that give each missing action.
type grantedByDoc struct {
	BuiltInRoles []string `json:"builtInRoles"`
	CustomRoles  []string `json:"customRoles"`
}

type missingActionDoc struct {
	Action    string       `json:"action"`
	GrantedBy grantedByDoc `json:"grantedBy"`
}

type callerAccessFullDoc struct {
	Object         string             `json:"object"`
	Actions        []string           `json:"actions"`
	Grants         []grantDoc         `json:"grants"`
	MissingActions []missingActionDoc `json:"missingActions"`
}

// TestCallerAccessGrantedBy reads the access of a caller without grants on
// acme: the missing unit:read-secrets lists the built-in roles that give it
// and the custom role that gives it. The bootstrap admin holds every action
// and misses none.
func TestCallerAccessGrantedBy(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var revealer roleDoc
	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/roles", `{"name":"secret-revealer","actions":["unit:read-secrets"]}`,
		http.StatusCreated,
	}, &revealer)

	f.seed(t, "erin", "", identity.Object{})

	// unit:read-secrets is missing for the caller. The built-in roles
	// that hold it are secret-viewer, writer, maintainer, admin,
	// account-administrator and site-administrator, in catalog order.
	wantBuiltIn := []string{
		"secret-viewer", "writer", "maintainer", "admin",
		"account-administrator", "site-administrator",
	}

	got := callerMissing(t, f, "erin", "acme")
	if len(got.Actions) != 0 || len(got.Grants) != 0 || got.Object != "acme" {
		t.Fatalf("access of erin = %+v, want no action and no grant on acme", got)
	}

	checkGrantedBy(t, got, grantedByWant{
		action: "unit:read-secrets", builtIn: wantBuiltIn, custom: []string{revealer.ID},
	})

	var admin callerAccessFullDoc
	f.as(t, "admin", apiCall{http.MethodGet, "/api/server/caller/access?object=acme", "", http.StatusOK}, &admin)

	if len(admin.MissingActions) != 0 {
		t.Fatalf("missing actions of the bootstrap admin = %+v, want none", admin.MissingActions)
	}
}

// callerMissing reads the access of subject on object.
func callerMissing(t *testing.T, f accessFixture, subject, object string) callerAccessFullDoc {
	t.Helper()

	var got callerAccessFullDoc
	f.as(t, subject, apiCall{
		http.MethodGet, "/api/server/caller/access?object=" + object, "", http.StatusOK,
	}, &got)

	return got
}

// grantedByWant is one expected missing action: its built-in roles and its
// custom roles.
type grantedByWant struct {
	action  string
	builtIn []string
	custom  []string
}

// checkGrantedBy checks the missing action of got against want. It also
// checks that every missing action is sorted and lists at least one
// built-in role.
func checkGrantedBy(t *testing.T, got callerAccessFullDoc, want grantedByWant) {
	t.Helper()

	if len(got.MissingActions) == 0 {
		t.Fatalf("missing actions on %s = none, want %s", got.Object, want.action)
	}

	names := make([]string, 0, len(got.MissingActions))

	for i := range got.MissingActions {
		m := &got.MissingActions[i]
		names = append(names, m.Action)

		if len(m.GrantedBy.BuiltInRoles) == 0 {
			t.Errorf("missing %s lists no built-in role", m.Action)
		}

		if m.Action != want.action {
			continue
		}

		if !slices.Equal(m.GrantedBy.BuiltInRoles, want.builtIn) ||
			!slices.Equal(m.GrantedBy.CustomRoles, want.custom) {
			t.Errorf(
				"missing %s = built-in %q custom %q, want built-in %q custom %q",
				m.Action, m.GrantedBy.BuiltInRoles, m.GrantedBy.CustomRoles, want.builtIn, want.custom,
			)
		}
	}

	if !slices.IsSorted(names) {
		t.Errorf("missing actions = %q, want sorted", names)
	}

	for _, self := range []string{"caller:read", "role:read", "token:manage-own"} {
		if slices.Contains(names, self) {
			t.Errorf("missing actions = %q, want no self action %s", names, self)
		}
	}
}

// TestCallerGrants reads the grants of callers: bob holds a grant of his
// own and one through a group, read one row a page.
func TestCallerGrants(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedOpsGrants(t, f)

	var got []string

	path := "/api/server/caller/grants?limit=1"

	for page := 0; path != ""; page++ {
		var doc reviewDoc
		f.as(t, "bob", apiCall{http.MethodGet, path, "", http.StatusOK}, &doc)

		if (page == 0) != (doc.Total != nil) || (doc.Total != nil && *doc.Total != 2) || doc.AsOfMs == nil {
			t.Fatalf("page %d = %+v, want a total of 2 on the first page only, and a time", page, doc)
		}

		got = append(got, callerGrantRows(doc)...)

		path = ""
		if doc.NextCursor != "" {
			path = "/api/server/caller/grants?limit=1&cursor=" + doc.NextCursor
		}
	}

	want := []string{
		"principal:prn-bob bob viewer acme",
		"group:grp-ops#member ops installation-operator @server via group:grp-ops#member principal:prn-bob",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("grants of bob = %q, want %q", got, want)
	}
}

// TestCallerGrantsOfOtherCallers reads the grants of olga, who holds one,
// and of the bootstrap admin, who holds none.
func TestCallerGrantsOfOtherCallers(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	for subject, rows := range map[string]int{"olga": 1, "admin": 0} {
		var doc reviewDoc
		f.as(t, subject, apiCall{http.MethodGet, "/api/server/caller/grants", "", http.StatusOK}, &doc)

		if doc.Total == nil || *doc.Total != int64(rows) || len(doc.Grants) != rows || doc.NextCursor != "" {
			t.Fatalf("grants of %s = %+v, want %d", subject, doc, rows)
		}
	}
}

// TestCallerGrantsRefusals sends the queries that the caller grants
// refuse. A cursor of one caller does not page the grants of another.
func TestCallerGrantsRefusals(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedOpsGrants(t, f)

	var first reviewDoc
	f.as(t, "bob", apiCall{http.MethodGet, "/api/server/caller/grants?limit=1", "", http.StatusOK}, &first)

	for _, c := range []struct{ subject, query string }{
		{"olga", "cursor=" + first.NextCursor},
		{"bob", "limit=0"},
		{"bob", "limit=x"},
		{"bob", "cursor=%21"},
	} {
		f.as(t, c.subject, apiCall{
			http.MethodGet, "/api/server/caller/grants?" + c.query, "", http.StatusBadRequest,
		}, nil)
	}
}

// seedOpsGrants gives bob a viewer grant on acme and makes him a member of
// the group ops, which holds installation-operator on the installation.
func seedOpsGrants(t *testing.T, f accessFixture) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 13, 0, 0, 0, time.UTC)

	group := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: "grp-ops", Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := f.store.CreateGroup(t.Context(), group, storetest.ServerChange(at, "group:grp-ops")); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	member := principal.Membership{
		AddedAt: at, SuspendedAt: time.Time{}, GroupID: "grp-ops", PrincipalID: "prn-bob",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive, Pinned: false,
	}
	joined := storetest.ServerChange(at, "group:grp-ops#member:prn-bob")

	if err := f.store.AddMember(t.Context(), member, joined, nil); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	grants := []access.Grant{
		{
			CreatedAt: at,
			Condition: nil,
			Object:    identity.NamespaceObject("acme"),
			ID:        "grt-bob-own",
			Role:      access.RoleViewer,
			CreatedBy: "admin",
			Subject:   access.Subject{Kind: access.SubjectPrincipal, ID: "prn-bob"},
		},
		{
			CreatedAt: at.Add(time.Second), Condition: nil, Object: identity.ServerObject(), ID: "grt-ops",
			Role: access.RoleInstallationOperator, CreatedBy: "admin",
			Subject: access.Subject{Kind: access.SubjectGroup, ID: "grp-ops"},
		},
	}

	for i := range grants {
		fence, e := storetest.GrantWrite(t, f.store, grants[i].Object, at, "grant:"+grants[i].ID)
		if err := f.store.CreateGrant(t.Context(), grants[i], fence, e, nil); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
	}
}

// TestReviewMembershipFields checks the source and suspension fields in JSON.
func TestReviewMembershipFields(t *testing.T) {
	t.Parallel()

	for _, c := range []reviewMembershipCase{
		{subject: "bob", source: "local", sourceID: "", suspended: false},
		{subject: "olga", source: "scim", sourceID: "scim-token-1", suspended: true},
	} {
		t.Run(c.source, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)
			seedOpsGrants(t, f)
			seedSuspendedReviewMember(t, f)

			path := "/api/server/access-review?subject=principal:prn-" + c.subject
			rows := reviewRowsAsMap(t, f, "ann", path)
			checkReviewMembershipJSON(t, rows, c)
		})
	}
}

// reviewMembershipCase is one membership that the JSON of a review must show.
type reviewMembershipCase struct {
	subject   string
	source    string
	sourceID  string
	suspended bool
}

// checkReviewMembershipJSON checks that the membership fields are present, including empty and false values.
func checkReviewMembershipJSON(t *testing.T, rows []map[string]any, want reviewMembershipCase) {
	t.Helper()

	count := 0

	for _, row := range rows {
		if row["via"] == nil {
			continue
		}

		via, ok := row["via"].(map[string]any)
		if !ok {
			t.Fatalf("via = %v, want an object", row["via"])
		}

		count++
		if via["memberSource"] != want.source || via["memberSourceId"] != want.sourceID ||
			via["memberSuspended"] != want.suspended {
			t.Fatalf("via = %v, want source %q, source ID %q and suspended %t",
				via, want.source, want.sourceID, want.suspended)
		}
	}

	if count != 1 {
		t.Fatalf("review has %d membership rows, want 1", count)
	}
}

// seedSuspendedReviewMember makes olga a suspended member of ops from SCIM.
func seedSuspendedReviewMember(t *testing.T, f accessFixture) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 13, 0, 0, 0, time.UTC)
	m := principal.Membership{
		AddedAt: at, SuspendedAt: at.Add(time.Minute), GroupID: "grp-ops", PrincipalID: "prn-olga",
		Source: principal.MembershipSourceSCIM, SourceID: "scim-token-1",
		State: principal.MembershipStateSuspended, Pinned: false,
	}
	joined := storetest.ServerChange(at, "group:grp-ops#member:prn-olga")
	if err := f.store.AddMember(t.Context(), m, joined, nil); err != nil {
		t.Fatalf("seed suspended member: %v", err)
	}
}

// callerGrantRows returns each row of doc as its subject, subject name,
// role, object and membership.
func callerGrantRows(doc reviewDoc) []string {
	out := make([]string, 0, len(doc.Grants))

	for i := range doc.Grants {
		r := &doc.Grants[i]

		via := ""
		if r.Via != nil {
			via = " via " + r.Via.Group + " " + r.Via.Member
		}

		out = append(out, r.Subject+" "+r.SubjectName+" "+r.Role+" "+r.Object+via)
	}

	return out
}

// TestCallerAccessPaging walks the objects of a caller one page at a time.
// Grants on units, a collection and a namespace all cover units.
func TestCallerAccessPaging(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	at := time.Date(2026, time.October, 13, 13, 0, 0, 0, time.UTC)

	api := seedStack(t, f.store, "acme", "api", "prod")

	web, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	web = web.WithUnitID("stk-acme-web-prod")

	for i, o := range []identity.Object{identity.CollectionObject("acme", "payments").WithCollectionID("col-payments"), web, api} {
		g := access.Grant{
			CreatedAt: at, Condition: nil, Object: o, ID: "grt-page-" + strconv.Itoa(i), Role: access.RoleViewer,
			CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-bob"},
		}
		fence, e := storetest.GrantWrite(t, f.store, o, at, "grant:"+g.ID)
		if err := f.store.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
	}

	var got []string

	path := "/api/server/caller/access?type=unit&limit=2"

	for path != "" {
		var page accessObjectsDoc
		f.as(t, "bob", apiCall{http.MethodGet, path, "", http.StatusOK}, &page)

		if page.Total != 3 {
			t.Fatalf("total = %d, want 3", page.Total)
		}

		for _, o := range page.Objects {
			got = append(got, o.Object)
		}

		path = ""
		if page.NextCursor != "" {
			path = "/api/server/caller/access?type=unit&limit=2&cursor=" + page.NextCursor
		}
	}

	want := []string{"acme/payments", "pulumi:acme/api/prod", "pulumi:acme/web/prod"}
	if !slices.Equal(got, want) {
		t.Fatalf("objects = %q, want %q", got, want)
	}

	for _, cursor := range []string{"%21", "AA", strings.Repeat("A", 513)} {
		f.as(t, "bob", apiCall{
			http.MethodGet, "/api/server/caller/access?type=unit&cursor=" + cursor, "", http.StatusBadRequest,
		}, nil)
	}
}

// TestReviewCursorIsBoundToItsExport sends cursors of one export to
// another: each answers 400, so a page never reads an export that its audit
// entry does not name.
func TestReviewCursorIsBoundToItsExport(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	cursorOf := func(path string) string {
		var page reviewDoc
		f.as(t, "ann", apiCall{http.MethodGet, path, "", http.StatusOK}, &page)

		if page.NextCursor == "" {
			t.Fatalf("%s gave no next cursor", path)
		}

		return page.NextCursor
	}

	acme := cursorOf("/api/server/namespaces/acme/access-review?limit=1")
	snapshot := cursorOf("/api/server/access-review?limit=1")
	history := cursorOf("/api/server/access-review?limit=1&from=0&to=9999999999999")

	for _, c := range []struct{ name, path string }{
		{"a cursor of another object", "/api/server/access-review?limit=1&cursor=" + acme},
		{"a snapshot cursor on a history", "/api/server/access-review?limit=1&from=0&to=9999999999999&cursor=" + snapshot},
		{"a history cursor on a snapshot", "/api/server/access-review?limit=1&cursor=" + history},
		{"a history cursor on another window", "/api/server/access-review?limit=1&from=1&to=9999999999999&cursor=" + history},
		{"a cursor with a subject added", "/api/server/access-review?limit=1&subject=principal:prn-ann&cursor=" + snapshot},
		{"a cursor with expand added", "/api/server/access-review?limit=1&expand=members&cursor=" + snapshot},
		{"a hand-made cursor", "/api/server/access-review?limit=1&cursor=" + handMadeCursor()},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f.as(t, "ann", apiCall{http.MethodGet, c.path, "", http.StatusBadRequest}, nil)
		})
	}

	f.as(
		t,
		"ann",
		apiCall{http.MethodGet, "/api/server/access-review?limit=1&cursor=" + snapshot, "", http.StatusOK},
		nil,
	)
}

// handMadeCursor returns a cursor of a snapshot at the Unix epoch with a
// binding that no export has.
func handMadeCursor() string {
	raw := strings.Join([]string{"0", "0", "", "0", "", "0", strings.Repeat("0", 16)}, "\x00")

	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// seedUsedToken stores a token of principalID used at used.
func seedUsedToken(t *testing.T, f accessFixture, principalID string, used time.Time) {
	t.Helper()

	tok := accesstoken.Token{
		CreatedAt:   used.Add(-time.Hour),
		ExpiresAt:   used.Add(24 * time.Hour),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          "tok-" + principalID,
		PrincipalID: principalID,
		Name:        "token tok-" + principalID,
		Kind:        accesstoken.KindService,
		ReplacedBy:  "",
		CreatedBy:   "admin",
	}
	e := storetest.ServerChange(tok.CreatedAt, "token:"+tok.ID)

	if err := f.store.CreateToken(t.Context(), tok, sha256.Sum256([]byte(principalID)), e); err != nil {
		t.Fatalf("create token: %v", err)
	}

	if err := f.store.TouchToken(t.Context(), tok.ID, used, time.Minute); err != nil {
		t.Fatalf("touch token: %v", err)
	}
}

// trackedOf reports whether the row says its last access is tracked.
func trackedOf(r map[string]any) bool {
	tracked, ok := r["lastAccessTracked"].(bool)

	return ok && tracked
}

// checkNeverJSON checks that every row of rows is tracked with no
// lastAccessAtMs key at all.
func checkNeverJSON(t *testing.T, rows []map[string]any) {
	t.Helper()

	for _, r := range rows {
		if !trackedOf(r) {
			t.Fatalf("row %+v: want lastAccessTracked true", r)
		}

		if _, ok := r["lastAccessAtMs"]; ok {
			t.Fatalf("row %+v: want no lastAccessAtMs key", r)
		}
	}
}

// TestReviewLastAccess checks the last access on the wire: a principal row
// with a used token answers lastAccessTracked true with lastAccessAtMs; a
// principal row never used answers tracked true with no lastAccessAtMs key
// at all; a group row and a bootstrap row carry neither key. The grants of
// the caller answer the same fields.
func TestReviewLastAccess(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	seedOpsGrants(t, f)

	used := time.Date(2026, time.October, 13, 14, 0, 0, 0, time.UTC)
	seedUsedToken(t, f, "prn-alice", used)

	rows := reviewRowsAsMap(t, f, "ann", "/api/server/access-review")

	checkUsedRow(t, rows, "principal:prn-alice", float64(used.UnixMilli()))
	checkNeverRow(t, rows, "principal:prn-bob")
	checkBareRow(t, rows, "group:grp-ops#member")
	checkBareRow(t, rows, "")

	checkFilteredLastAccess(t, f)

	grants := reviewRowsAsMap(t, f, "alice", "/api/server/caller/grants")
	if len(grants) != 1 {
		t.Fatalf("grants of alice = %d rows, want her own row", len(grants))
	}

	checkUsedRow(t, grants, "principal:prn-alice", float64(used.UnixMilli()))

	bob := reviewRowsAsMap(t, f, "bob", "/api/server/caller/grants")
	if len(bob) != 2 {
		t.Fatalf("grants of bob = %d rows, want the own row and the row through the group", len(bob))
	}

	checkNeverJSON(t, bob)
}

// checkFilteredLastAccess checks the export filtered to bob: his own row
// and his row through the group, both tracked with no lastAccessAtMs key.
func checkFilteredLastAccess(t *testing.T, f accessFixture) {
	t.Helper()

	rows := reviewRowsAsMap(t, f, "ann", "/api/server/access-review?subject=principal:prn-bob")

	own, through := false, false

	for _, r := range rows {
		switch {
		case r["subject"] == "principal:prn-bob" && r["via"] == nil:
			own = true
		case r["subject"] == "group:grp-ops#member" && r["via"] != nil:
			through = true
		default:
			continue
		}

		if !trackedOf(r) {
			t.Fatalf("filtered row %+v: want lastAccessTracked true", r)
		}

		if _, ok := r["lastAccessAtMs"]; ok {
			t.Fatalf("filtered row %+v: want no lastAccessAtMs key", r)
		}
	}

	if !own || !through {
		t.Fatal("filtered export has no own row and no row through the group")
	}
}

// reviewRowsAsMap reads path and returns its grant rows as maps, so a test
// can tell a missing key from a null one.
func reviewRowsAsMap(t *testing.T, f accessFixture, subject, path string) []map[string]any {
	t.Helper()

	var raw map[string]any
	f.as(t, subject, apiCall{http.MethodGet, path, "", http.StatusOK}, &raw)

	list, ok := raw["grants"].([]any)
	if !ok {
		t.Fatalf("%s grants = %v, want a list", path, raw["grants"])
	}

	out := make([]map[string]any, 0, len(list))

	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%s row = %v, want an object", path, item)
		}

		out = append(out, row)
	}

	return out
}

// checkUsedRow checks the row of subject: tracked, with the last access at
// usedMs.
func checkUsedRow(t *testing.T, rows []map[string]any, subject string, usedMs float64) {
	t.Helper()

	for _, r := range rows {
		if r["subject"] == subject && r["holder"] == nil {
			if !trackedOf(r) || r["lastAccessAtMs"] != usedMs {
				t.Fatalf("row %+v: want lastAccessTracked true with lastAccessAtMs %v", r, usedMs)
			}

			return
		}
	}

	t.Fatalf("no row of %s", subject)
}

// checkNeverRow checks the row of subject: tracked, with no lastAccessAtMs
// key at all.
func checkNeverRow(t *testing.T, rows []map[string]any, subject string) {
	t.Helper()

	for _, r := range rows {
		if r["subject"] == subject && r["via"] == nil {
			if !trackedOf(r) {
				t.Fatalf("row %+v: want lastAccessTracked true", r)
			}

			if _, ok := r["lastAccessAtMs"]; ok {
				t.Fatalf("row %+v: want no lastAccessAtMs key", r)
			}

			return
		}
	}

	t.Fatalf("no row of %s", subject)
}

// checkBareRow checks that the row of subject, or the bootstrap row for an
// empty subject, carries neither last access key.
func checkBareRow(t *testing.T, rows []map[string]any, subject string) {
	t.Helper()

	for _, r := range rows {
		if matchBareRow(r, subject) {
			if _, ok := r["lastAccessTracked"]; ok {
				t.Fatalf("row %+v: want no lastAccessTracked key", r)
			}

			if _, ok := r["lastAccessAtMs"]; ok {
				t.Fatalf("row %+v: want no lastAccessAtMs key", r)
			}

			return
		}
	}

	t.Fatalf("no bare row of %q", subject)
}

// matchBareRow reports whether r is the bootstrap row for an empty subject,
// or the group row of subject with no membership.
func matchBareRow(r map[string]any, subject string) bool {
	if subject == "" {
		return r["holder"] != nil
	}

	return r["subject"] == subject && r["via"] == nil
}

// failingAudit refuses every append, as an audit store that is down does.
type failingAudit struct{}

func (failingAudit) AppendServer(context.Context, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errAuditDown
}

func (failingAudit) Append(context.Context, namespace.Fence, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errAuditDown
}

func (failingAudit) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	return audit.Listing{}, errAuditDown
}

func (failingAudit) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{}, errAuditDown
}

// errAuditDown is the error of failingAudit.
var errAuditDown = errors.New("audit store down")

// TestReviewFailsClosedWithoutItsEntry proves that an export whose audit
// entry fails answers 500 with no rows.
func TestReviewFailsClosedWithoutItsEntry(t *testing.T) {
	t.Parallel()

	f := newAccessFixtureWith(t, func(*sqlite.Store) auditStore { return failingAudit{} })

	for _, path := range []string{"/api/server/access-review", "/api/server/namespaces/acme/access-review"} {
		var answer map[string]any
		f.as(t, "ann", apiCall{http.MethodGet, path, "", http.StatusInternalServerError}, &answer)

		if _, ok := answer["grants"]; ok {
			t.Fatalf("%s with a failed audit entry = %v, want no rows", path, answer)
		}
	}
}
