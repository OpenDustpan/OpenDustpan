package server_test

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// namespaceListDoc decodes one page of GET /api/server/namespaces.
type namespaceListDoc struct {
	Namespaces []namespaceDoc `json:"namespaces"`
	NextCursor string         `json:"nextCursor"`
	Total      *int64         `json:"total"`
}

// listAs sends one list request as subject and decodes the page. It fails
// unless the list answers 200.
func listAs(t *testing.T, f accessFixture, subject, path string) namespaceListDoc {
	t.Helper()

	var doc namespaceListDoc

	f.as(t, subject, apiCall{method: http.MethodGet, path: path, body: "", want: http.StatusOK}, &doc)

	return doc
}

// createListNamespaces creates every name through the namespace create
// route as the bootstrap admin.
func createListNamespaces(t *testing.T, f accessFixture, names []string) {
	t.Helper()

	for _, name := range names {
		f.as(t, "admin", apiCall{
			method: http.MethodPost, path: "/api/server/namespaces",
			body: `{"name":"` + name + `"}`, want: http.StatusCreated,
		}, nil)
	}
}

// grantViewerOn gives bob the viewer role on the namespace ns, directly in
// the store.
func grantViewerOn(t *testing.T, f accessFixture, ns, grantID string) {
	t.Helper()

	obj := identity.NamespaceObject(namespace.Name(ns))
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: grantID, Role: access.RoleViewer, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-bob"},
	}

	fence, e := storetest.GrantWrite(t, f.store, obj, at, "grant:"+grantID)
	if err := f.store.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("seed grant on %q: %v", ns, err)
	}
}

// listNames returns the names of a decoded page, in order.
func listNames(doc namespaceListDoc) []string {
	out := make([]string, 0, len(doc.Namespaces))
	for _, ns := range doc.Namespaces {
		out = append(out, ns.Name)
	}

	return out
}

// checkListRoute proves the list route declares caller:read on the caller.
func checkListRoute(t *testing.T, route apiserver.Route) {
	t.Helper()

	if route.Scope != apiserver.ScopeAuthorized {
		t.Fatalf("route scope = %d, want authorized", route.Scope)
	}

	if len(route.Actions) != 1 || route.Actions[0] != identity.ActionCallerRead {
		t.Fatalf("route actions = %v, want [caller:read]", route.Actions)
	}

	if route.Resolve == nil {
		t.Fatal("route declares actions but no resolver")
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/namespaces", http.NoBody)

	obj, err := route.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if !obj.IsCaller() {
		t.Fatalf("resolver returns %q, want the caller", obj.String())
	}
}

// TestNamespaceListRouteDeclaresRead proves the list route is in the table
// with caller:read on the caller.
func TestNamespaceListRouteDeclaresRead(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	found := false

	for _, route := range handler.Routes() {
		if route.Method == http.MethodGet && route.Path == "/api/server/namespaces" {
			checkListRoute(t, route)
			found = true
		}
	}

	if !found {
		t.Fatal("no GET /api/server/namespaces route")
	}
}

// collectListPages walks every page with the given limit and returns the
// names in order. It fails unless each page holds at most limit rows, the
// first page totals len(want) and later pages have no total.
func collectListPages(t *testing.T, f accessFixture, subject string, limit int, want []string) []string {
	t.Helper()

	var got []string

	path := "/api/server/namespaces?limit=2"
	first := true

	for path != "" {
		doc := listAs(t, f, subject, path)

		if first {
			checkFirstPageTotal(t, doc, len(want))
		} else {
			checkLaterPageTotal(t, doc)
		}

		got = append(got, listNames(doc)...)

		if len(doc.Namespaces) > limit {
			t.Fatalf("page holds %d namespaces, want at most %d", len(doc.Namespaces), limit)
		}

		path = nextListPath(doc)
		first = false
	}

	return got
}

// checkFirstPageTotal fails unless doc totals want.
func checkFirstPageTotal(t *testing.T, doc namespaceListDoc, want int) {
	t.Helper()

	if doc.Total == nil {
		t.Fatal("first page has no total")
	}

	if *doc.Total != int64(want) {
		t.Fatalf("first page total = %d, want %d", *doc.Total, want)
	}
}

// checkLaterPageTotal fails unless doc has no total.
func checkLaterPageTotal(t *testing.T, doc namespaceListDoc) {
	t.Helper()

	if doc.Total != nil {
		t.Fatalf("later page total = %d, want null", *doc.Total)
	}
}

// nextListPath answers the path of the page after doc, or empty on the last
// page.
func nextListPath(doc namespaceListDoc) string {
	if doc.NextCursor == "" {
		return ""
	}

	return "/api/server/namespaces?limit=2&cursor=" + doc.NextCursor
}

// checkListFirstPage fails unless the first page holds two namespaces with
// primary installation and time, and names a next page.
func checkListFirstPage(t *testing.T, f accessFixture) {
	t.Helper()

	first := listAs(t, f, "ann", "/api/server/namespaces?limit=2")
	if first.NextCursor == "" {
		t.Fatal("first page has no next cursor, want one")
	}

	if len(first.Namespaces) != 2 || first.Namespaces[0].PrimaryInstallation == "" ||
		first.Namespaces[0].CreatedAt == "" {
		t.Fatalf("first page = %+v, want two namespaces with primary installation and time", first)
	}

	if first.Namespaces[0].ID == "" || first.Namespaces[0].ID == first.Namespaces[1].ID {
		t.Fatalf("first page = %+v, want two namespaces with distinct IDs", first)
	}
}

// TestNamespaceListPagesAll proves a caller with a grant on the
// installation sees every namespace, paged by name, with the total only on
// the first page.
func TestNamespaceListPagesAll(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createListNamespaces(t, f, []string{"bravo", "charlie", "delta", "echo"})

	want := []string{"acme", "bravo", "charlie", "delta", "echo"}
	got := collectListPages(t, f, "ann", 2, want)

	if !slices.Equal(got, want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}

	checkListFirstPage(t, f)
}

// TestNamespaceListSeesOnlyGranted proves a caller with a reader grant on
// two namespaces sees exactly those two, in name order.
func TestNamespaceListSeesOnlyGranted(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createListNamespaces(t, f, []string{"bravo", "charlie", "delta", "echo"})
	grantViewerOn(t, f, "bravo", "grt-list-bravo")
	grantViewerOn(t, f, "delta", "grt-list-delta")

	var got []string

	path := "/api/server/namespaces?limit=1"
	pages := 0

	for path != "" {
		doc := listAs(t, f, "bob", path)
		got = append(got, listNames(doc)...)
		pages++

		path = ""

		if doc.NextCursor != "" {
			path = "/api/server/namespaces?limit=1&cursor=" + doc.NextCursor
		}

		if pages > 3 {
			t.Fatalf("paged more than 3 times, want 2 pages: got %v", got)
		}
	}

	if !slices.Equal(got, []string{"bravo", "delta"}) {
		t.Fatalf("pages = %v, want [bravo delta]", got)
	}

	if pages != 2 {
		t.Fatalf("pages = %d, want 2", pages)
	}
}

// TestNamespaceListEmpty proves a caller with no grant sees an empty list
// with total 0.
func TestNamespaceListEmpty(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	doc := listAs(t, f, "bob", "/api/server/namespaces")

	if len(doc.Namespaces) != 0 {
		t.Fatalf("namespaces = %v, want none", listNames(doc))
	}

	if doc.Namespaces == nil {
		t.Fatal("namespaces is null, want []")
	}

	if doc.NextCursor != "" {
		t.Fatalf("next cursor = %q, want empty", doc.NextCursor)
	}

	if doc.Total == nil || *doc.Total != 0 {
		t.Fatalf("total = %v, want 0", doc.Total)
	}
}

// TestNamespaceListSkipsUnitGrant proves a namespace that the caller reaches
// only through a unit grant is not listed.
func TestNamespaceListSkipsUnitGrant(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createListNamespaces(t, f, []string{"solo"})

	unit := seedStack(t, f.store, "solo", "web", "dev")

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: unit, ID: "grt-list-unit", Role: access.RoleViewer,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-bob"},
	}

	fence, e := storetest.GrantWrite(t, f.store, unit, at, "grant:grt-list-unit")
	if err := f.store.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("seed unit grant: %v", err)
	}

	doc := listAs(t, f, "bob", "/api/server/namespaces")

	if len(doc.Namespaces) != 0 {
		t.Fatalf("namespaces = %v, want none: a unit grant must not list its namespace", listNames(doc))
	}

	if doc.Total == nil || *doc.Total != 0 {
		t.Fatalf("total = %v, want 0", doc.Total)
	}
}

// TestNamespaceListSkipsCollectionGrant proves a namespace that the caller
// reaches only through a collection grant is not listed.
func TestNamespaceListSkipsCollectionGrant(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createListNamespaces(t, f, []string{"solo"})

	obj := identity.CollectionObject("solo", "payments").WithCollectionID("col-solo-payments")

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-list-collection", Role: access.RoleViewer,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-bob"},
	}

	fence, e := storetest.GrantWrite(t, f.store, obj, at, "grant:grt-list-collection")
	if grantErr := f.store.CreateGrant(t.Context(), grt, fence, e, nil); grantErr != nil {
		t.Fatalf("seed collection grant: %v", grantErr)
	}

	doc := listAs(t, f, "bob", "/api/server/namespaces")

	if len(doc.Namespaces) != 0 {
		t.Fatalf("namespaces = %v, want none: a collection grant must not list its namespace", listNames(doc))
	}
}

// newListNoAccessHandler builds a handler with no access service over a
// store with the given namespaces.
func newListNoAccessHandler(t *testing.T, authn identity.Authenticator, names []string) testHandler {
	t.Helper()

	st := openServerTemplateStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	for _, name := range names {
		parsed, parseErr := namespace.ParseName(name)
		if parseErr != nil {
			t.Fatalf("parse name %q: %v", name, parseErr)
		}

		if _, createErr := namespaces.Create(t.Context(), parsed); createErr != nil {
			t.Fatalf("create namespace %q: %v", name, createErr)
		}
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    authn,
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// serveListAs sends one list request with the given bearer token.
func serveListAs(t *testing.T, handler testHandler, token, path string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// TestNamespaceListWithoutAccessAdmin proves an administrator lists every
// namespace when no access service is configured.
func TestNamespaceListWithoutAccessAdmin(t *testing.T) {
	t.Parallel()

	handler := newListNoAccessHandler(t, subjectAuthenticator{}, []string{"acme", "bravo"})

	code, body := serveListAs(t, handler, "admin", "/api/server/namespaces")
	if code != http.StatusOK {
		t.Fatalf("admin list answers %d, want %d (%s)", code, http.StatusOK, body)
	}

	doc := decodeListBody(t, body)

	if !slices.Equal(listNames(doc), []string{"acme", "bravo"}) {
		t.Fatalf("admin namespaces = %v, want [acme bravo]", listNames(doc))
	}
}

// TestNamespaceListWithoutAccessReader proves a caller without the
// administrator flag gets an empty page when no access service is
// configured.
func TestNamespaceListWithoutAccessReader(t *testing.T) {
	t.Parallel()

	handler := newListNoAccessHandler(t, subjectAuthenticator{}, []string{"acme", "bravo"})

	code, body := serveListAs(t, handler, "bob", "/api/server/namespaces")
	if code != http.StatusOK {
		t.Fatalf("reader list answers %d, want %d (%s)", code, http.StatusOK, body)
	}

	doc := decodeListBody(t, body)

	if len(doc.Namespaces) != 0 || doc.Total == nil || *doc.Total != 0 {
		t.Fatalf("reader page = %+v, want empty with total 0", doc)
	}
}

// namespaceCursorToken is the wire form of a namespace list cursor in a test.
type namespaceCursorToken struct {
	After       string `json:"a"`
	Fingerprint string `json:"f"`
}

// decodeNamespaceCursorToken decodes a token that a list answered.
func decodeNamespaceCursorToken(t *testing.T, token string) namespaceCursorToken {
	t.Helper()

	var wire namespaceCursorToken
	if err := pagetoken.Decode(token, &wire); err != nil {
		t.Fatalf("decode cursor %q: %v", token, err)
	}

	return wire
}

// encodeNamespaceCursorToken encodes a wire cursor for a request.
func encodeNamespaceCursorToken(t *testing.T, wire namespaceCursorToken) string {
	t.Helper()

	token, err := pagetoken.Encode(wire)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}

	return token
}

// checkCursorRefusal sends path as subject and proves that it answers 400
// with the code of an invalid page token.
func checkCursorRefusal(t *testing.T, f accessFixture, subject, path string) {
	t.Helper()

	var failure struct {
		ErrorCode string `json:"errorCode"`
	}

	f.as(t, subject, apiCall{method: http.MethodGet, path: path, body: "", want: http.StatusBadRequest}, &failure)

	if failure.ErrorCode != errcode.RequestPageTokenInvalid {
		t.Fatalf("%s answers errorCode %q, want %q", path, failure.ErrorCode, errcode.RequestPageTokenInvalid)
	}
}

// TestNamespaceListCursorOfAnotherFilterIsRefused proves a cursor of the
// administrator query answers 400 for a caller with one visible namespace.
func TestNamespaceListCursorOfAnotherFilterIsRefused(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createListNamespaces(t, f, []string{"bravo", "charlie", "delta"})
	grantViewerOn(t, f, "delta", "grt-list-delta")

	first := listAs(t, f, "admin", "/api/server/namespaces?limit=2")
	if first.NextCursor == "" {
		t.Fatal("the administrator page has no next cursor, want one")
	}

	path := "/api/server/namespaces?cursor=" + url.QueryEscape(first.NextCursor)
	checkCursorRefusal(t, f, "bob", path)
}

// TestNamespaceListCursorWithoutSealIsRefused proves the server refuses a
// wire token with a valid fingerprint that it did not seal.
func TestNamespaceListCursorWithoutSealIsRefused(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createListNamespaces(t, f, []string{"bravo", "charlie", "delta"})

	first := listAs(t, f, "admin", "/api/server/namespaces?limit=2")
	if first.NextCursor == "" {
		t.Fatal("the administrator page has no next cursor, want one")
	}

	wire := namespaceCursorToken{
		After:       base64.RawURLEncoding.EncodeToString([]byte("bravo")),
		Fingerprint: decodeNamespaceCursorToken(t, first.NextCursor).Fingerprint,
	}
	path := "/api/server/namespaces?cursor=" + url.QueryEscape(encodeNamespaceCursorToken(t, wire))

	checkCursorRefusal(t, f, "admin", path)
}

// TestNamespaceListBadQuery proves a bad cursor and a bad limit answer 400.
func TestNamespaceListBadQuery(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createListNamespaces(t, f, []string{"bravo", "charlie", "delta"})

	first := listAs(t, f, "admin", "/api/server/namespaces?limit=2")
	if first.NextCursor == "" {
		t.Fatal("the first page has no next cursor, want one")
	}

	valid := decodeNamespaceCursorToken(t, first.NextCursor)

	changed := []byte(first.NextCursor)
	if changed[len(changed)-1] == 'A' {
		changed[len(changed)-1] = 'B'
	} else {
		changed[len(changed)-1] = 'A'
	}

	unknownField := `{"a":"` + valid.After + `","f":"` + valid.Fingerprint + `","x":1}`

	for _, path := range []string{
		"/api/server/namespaces?limit=0",
		"/api/server/namespaces?limit=-1",
		"/api/server/namespaces?limit=abc",
		"/api/server/namespaces?limit=1001",
	} {
		f.as(t, "bob", apiCall{method: http.MethodGet, path: path, body: "", want: http.StatusBadRequest}, nil)
	}

	for _, token := range []string{
		"!!!",
		base64.RawURLEncoding.EncodeToString([]byte("not json")),
		base64.RawURLEncoding.EncodeToString([]byte(unknownField)),
		encodeNamespaceCursorToken(t, namespaceCursorToken{After: "", Fingerprint: valid.Fingerprint}),
		encodeNamespaceCursorToken(t, namespaceCursorToken{
			After:       base64.RawURLEncoding.EncodeToString([]byte("bad/name")),
			Fingerprint: valid.Fingerprint,
		}),
		string(changed),
		strings.Repeat("A", pagetoken.MaxLen+1),
	} {
		checkCursorRefusal(t, f, "admin", "/api/server/namespaces?cursor="+url.QueryEscape(token))
	}
}

// TestNamespaceListCursorWithAnotherLimitIsAccepted proves the page size is
// not part of the binding of a cursor.
func TestNamespaceListCursorWithAnotherLimitIsAccepted(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createListNamespaces(t, f, []string{"bravo", "charlie", "delta"})
	grantViewerOn(t, f, "bravo", "grt-list-bravo")
	grantViewerOn(t, f, "charlie", "grt-list-charlie")
	grantViewerOn(t, f, "delta", "grt-list-delta")

	first := listAs(t, f, "bob", "/api/server/namespaces?limit=1")
	if first.NextCursor == "" {
		t.Fatal("the first page has no next cursor, want one")
	}

	path := "/api/server/namespaces?limit=2&cursor=" + url.QueryEscape(first.NextCursor)
	second := listAs(t, f, "bob", path)

	if !slices.Equal(listNames(second), []string{"charlie", "delta"}) {
		t.Fatalf("second page = %v, want [charlie delta]", listNames(second))
	}
}

// TestNamespaceListWithoutPager proves a store without the paging port
// answers 501.
func TestNamespaceListWithoutPager(t *testing.T) {
	t.Parallel()

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    subjectAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       newNoPagerNamespaces(t),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	code, body := serveListAs(t, handler, "admin", "/api/server/namespaces")
	if code != http.StatusNotImplemented {
		t.Fatalf("list without a pager answers %d, want %d (%s)", code, http.StatusNotImplemented, body)
	}

	if !strings.Contains(body, "page") {
		t.Fatalf("501 body %q names no paging problem", body)
	}
}

// decodeListBody decodes a list body into a page.
func decodeListBody(t *testing.T, body string) namespaceListDoc {
	t.Helper()

	var doc namespaceListDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode list %q: %v", body, err)
	}

	return doc
}

// newNoPagerNamespaces returns a namespace service over a store without the
// paging port.
func newNoPagerNamespaces(t *testing.T) *namespace.Service {
	t.Helper()

	svc, err := namespace.NewService(namespace.Config{Store: stubNamespaceStore{}, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	return svc
}
