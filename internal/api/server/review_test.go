package server_test

import (
	"context"
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
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

type reviewRowDoc struct {
	LastAccessTracked *bool  `json:"lastAccessTracked"`
	LastAccessAtMs    *int64 `json:"lastAccessAtMs"`
	Subject           string `json:"subject"`
	SubjectName       string `json:"subjectName"`
	Holder            string `json:"holder"`
	Role              string `json:"role"`
	Object            string `json:"object"`
	ObjectState       string `json:"objectState"`
	InheritedFrom     string `json:"inheritedFrom"`
	CanGrantAccess    bool   `json:"canGrantAccess"`
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

	checkUntrackedRows(t, ns.Grants)

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

// checkUntrackedRows checks that each principal row says that the last
// access is not tracked, that no row has a last access time, and that every
// object is live.
func checkUntrackedRows(t *testing.T, rows []reviewRowDoc) {
	t.Helper()

	for i := range rows {
		g := &rows[i]

		tracked := g.LastAccessTracked != nil && !*g.LastAccessTracked
		if g.LastAccessAtMs != nil || tracked != (g.Holder == "") || g.ObjectState != access.ObjectLive {
			t.Fatalf(
				"row %+v: want lastAccessTracked false on a principal row, no lastAccessAtMs, and a live object",
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
		if err := f.store.CreateGrant(t.Context(), g, fence, e); err != nil {
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

// failingAudit refuses every append, as an audit store that is down does.
type failingAudit struct{}

func (failingAudit) AppendServer(context.Context, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errAuditDown
}

func (failingAudit) Append(context.Context, namespace.Fence, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errAuditDown
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
