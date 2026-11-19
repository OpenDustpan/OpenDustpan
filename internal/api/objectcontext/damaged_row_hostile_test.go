package objectcontext_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	contextapi "opendustpan.org/opendustpan/internal/api/objectcontext"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// spyUnits is an inventory reader that holds rows by unit ID, as GetUnit
// finds them, and counts the calls of ListUnits.
type spyUnits struct {
	rows   map[string]coreinventory.Entry
	getErr error
	lists  atomic.Int64
	gets   atomic.Int64
}

func (s *spyUnits) ListUnits(context.Context, namespace.Name) ([]coreinventory.Entry, error) {
	s.lists.Add(1)

	out := make([]coreinventory.Entry, 0, len(s.rows))
	for id := range s.rows {
		out = append(out, s.rows[id])
	}

	return out, nil
}

func (s *spyUnits) QueryUnits(ctx context.Context, q coreinventory.UnitQuery) ([]coreinventory.Entry, error) {
	all, err := s.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return coreinventory.PageUnits(all, q), nil
}

func (s *spyUnits) GetUnit(_ context.Context, unitID string) (coreinventory.Entry, error) {
	s.gets.Add(1)

	if s.getErr != nil {
		return coreinventory.Entry{}, s.getErr
	}

	if e, ok := s.rows[unitID]; ok {
		return e, nil
	}

	return coreinventory.Entry{}, coreinventory.ErrUnitNotFound
}

// spyDeleted holds soft-deleted rows and counts the calls of each read.
type spyDeleted struct {
	rows    []coreinventory.DeletedEntry
	lists   atomic.Int64
	queries atomic.Int64
	lastMu  sync.Mutex
	last    *coreinventory.DeletedQuery
}

func (s *spyDeleted) ListDeletedUnits(_ context.Context, ns namespace.Name) ([]coreinventory.DeletedEntry, error) {
	s.lists.Add(1)

	var out []coreinventory.DeletedEntry

	for _, e := range s.rows {
		if e.Namespace == ns {
			out = append(out, e)
		}
	}

	return out, nil
}

// QueryDeletedUnits answers the rows that match q, as a store does.
func (s *spyDeleted) QueryDeletedUnits(
	_ context.Context, q coreinventory.DeletedQuery,
) (coreinventory.DeletedPage, error) {
	s.queries.Add(1)

	s.lastMu.Lock()
	s.last = &q
	s.lastMu.Unlock()

	var out []coreinventory.DeletedEntry

	for _, e := range s.rows {
		if q.Matches(e) {
			out = append(out, e)
		}
	}

	return coreinventory.PageDeleted(out, q), nil
}

func (*spyDeleted) GetDeletedUnit(context.Context, namespace.Name, string, string) (coreinventory.DeletedEntry, error) {
	return coreinventory.DeletedEntry{}, coreinventory.ErrNotFound
}

var errUnitStoreDown = errors.New("unit store is unavailable")

const (
	storedBadName = "secret//name"
	storedBadTool = "evil tool"
	storedBadID   = "ws~bad"
)

func liveRow(ns namespace.Name, tool, name, id string) coreinventory.Entry {
	return coreinventory.Entry{
		LastRelease: nil, ManualLock: nil, LastChanged: time.Unix(0, 0),
		Namespace: ns, Tool: tool, Name: name, UnitID: id,
		DisplayName: "", Status: "", ResourceCount: nil, Lock: nil, Collection: "", Labels: nil,
	}
}

func goneRow(tool, name, id string) coreinventory.DeletedEntry {
	return coreinventory.DeletedEntry{
		DeletedAt: time.Unix(0, 0), PurgeAfter: time.Unix(1, 0), Namespace: "acme", Tool: tool, UnitID: id, Name: name,
	}
}

// hostileUnits holds the valid unit ws-1 of acme, the unit of globex, and the
// stored rows that the resolver must not serve.
func hostileUnits() (*spyUnits, *spyDeleted) {
	live := map[string]coreinventory.Entry{
		testUnitID:    liveRow("acme", "opentofu", "net", testUnitID),
		"ws-globex":   liveRow("globex", "opentofu", "net", "ws-globex"),
		"ws-badname":  liveRow("acme", "opentofu", storedBadName, "ws-badname"),
		"ws-badtool":  liveRow("acme", storedBadTool, "net", "ws-badtool"),
		"ws-badcolon": liveRow("acme", "opentofu", "a:b", "ws-badcolon"),
		// A unit ID that the path accepts but that Ref.Validate refuses. The
		// tool and the name are valid for identity.UnitObject.
		storedBadID: liveRow("acme", "opentofu", "net", storedBadID),
	}

	gone := &spyDeleted{
		lists:   atomic.Int64{},
		queries: atomic.Int64{},
		lastMu:  sync.Mutex{},
		last:    nil,
		rows: []coreinventory.DeletedEntry{
			goneRow("opentofu", "old", "ws-gone"),
			goneRow("opentofu", storedBadName, "ws-gonebadname"),
			goneRow(storedBadTool, "old", "ws-gonebadtool"),
			goneRow("opentofu", "old", "ws~gonebadid"),
		},
	}

	return &spyUnits{rows: live, getErr: nil, lists: atomic.Int64{}, gets: atomic.Int64{}}, gone
}

func newSpyHandler(
	t *testing.T, authz identity.Authorizer, units *spyUnits, deleted *spyDeleted, logs *strings.Builder,
) testHandler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	store := newFakeStore()

	writer, err := objectcontext.NewWriter(
		objectcontext.WriterConfig{Store: store, Namespaces: fakeNamespaces{}, Now: nil},
	)
	if err != nil {
		t.Fatalf("writer: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	if logs != nil {
		logger = slog.New(slog.NewTextHandler(logs, nil))
	}

	handler, err := contextapi.NewHandler(contextapi.Config{
		Audit: newTestAudit(), OnAuditFailure: nil, Namespaces: namespaces, Units: units, Deleted: deleted,
		Collections: nil, Drift: nil, Entries: store, Writer: writer, Authenticator: tokenAuthenticator{},
		Authorizer: authz, Sessions: nil, Logger: logger, DenialLimiter: nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return testHandler{Handler: handler, served: apidesctest.CheckStatus(t, handler)}
}

// hostileRow is one input of the table: the namespace and the unit segment of
// the path, and the stored text that no answer may repeat.
type hostileRow struct {
	name   string
	ns     string
	id     string
	stored []string
}

func hostileRows() []hostileRow {
	return []hostileRow{
		{"another namespace", "acme", "ws-globex", nil},
		{"namespace that does not exist", "absent", testUnitID, nil},
		{"namespace name in place of the ID", "acme", "acme", nil},
		{"unit name in place of the ID", "acme", "net", nil},
		{"tool name in place of the ID", "acme", "opentofu", nil},
		{"ID with a path escape", "acme", "..%2f..%2fetc%2fpasswd", nil},
		{"ID with a NUL byte", "acme", "ws-1%00", nil},
		{"ID with a CR and an LF", "acme", "ws-1%0d%0aX-Injected:%201", nil},
		{"ID with a quote and a tag", "acme", "%22%3Cscript%3E", nil},
		{"ID of 5000 bytes", "acme", strings.Repeat("a", 5000), nil},
		{"namespace name that is not valid", "bad!ns", testUnitID, nil},
		{"live row with a valid tool and a name that is not valid", "acme", "ws-badname", []string{"secret", "//"}},
		{"live row with a tool that is not valid", "acme", "ws-badtool", []string{"evil"}},
		{"live row with a colon in the name", "acme", "ws-badcolon", []string{"a:b"}},
		{"live row that only the reference check refuses", "acme", storedBadID, []string{"bad"}},
		{"deleted row with a name that is not valid", "acme", "ws-gonebadname", []string{"secret", "//"}},
		{"deleted row with a tool that is not valid", "acme", "ws-gonebadtool", []string{"evil"}},
		{"deleted row that only the reference check refuses", "acme", "ws~gonebadid", []string{"bad"}},
	}
}

func unitContextPath(ns, id string) string {
	return "/api/dustpan/v1/namespaces/" + ns + "/units/" + id + "/context"
}

type seen struct {
	header http.Header
	body   string
	status int
}

func seenOf(rec *httptest.ResponseRecorder) seen {
	return seen{header: rec.Header().Clone(), body: rec.Body.String(), status: rec.Code}
}

// TestUnitContextHostile proves that each input that names no unit that the
// caller may read gets the answer of an unknown ID, for a caller that holds
// every grant and for a caller that holds none, on each method of the unit
// route. A stored row that fails its checks is one of the inputs, live or
// soft-deleted.
func TestUnitContextHostile(t *testing.T) {
	t.Parallel()

	callers := []struct {
		name  string
		holds []string
	}{
		{"caller with every grant", []string{
			"namespace:read acme", "unit:read opentofu:acme/net", "unit:read opentofu:acme/old",
		}},
		{"caller with no grant", nil},
	}

	for _, caller := range callers {
		for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
			t.Run(caller.name+"/"+method, func(t *testing.T) {
				t.Parallel()

				units, deleted := hostileUnits()
				handler := newSpyHandler(t, newAuthorizer(caller.holds...), units, deleted, nil)

				checkHostileRows(t, handler, method)
			})
		}
	}
}

// checkHostileRows sends each row of the table with method and compares the
// answer with the answer for an unknown ID.
func checkHostileRows(t *testing.T, handler http.Handler, method string) {
	t.Helper()

	body := ""
	if method == http.MethodPatch {
		body = `{"onCall":"pager:x"}`
	}

	send := func(ns, id string) seen {
		return seenOf(serve(t, handler, request{
			method: method, path: unitContextPath(ns, id), token: testToken, body: body,
		}))
	}

	want := send("acme", "ws-unknown")
	if want.status != http.StatusNotFound {
		t.Fatalf("unknown ID: status %d (%s), want 404", want.status, want.body)
	}

	for _, row := range hostileRows() {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			compareSeen(t, send(row.ns, row.id), want, row.stored)
		})
	}
}

// TestUnitResolverAnswersNotFound proves, below the classifier, that the
// resolver returns the one not-found answer for each hostile input. The
// classifier answers 404 for other errors of this adapter too, so an answer
// check alone cannot show that the resolver itself refuses the row.
func TestUnitResolverAnswersNotFound(t *testing.T) {
	t.Parallel()

	for _, row := range hostileRows() {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			units, deleted := hostileUnits()
			handler := newSpyHandler(t, newAuthorizer(), units, deleted, nil)

			// The mux decodes the escapes of the path before it sets the value.
			id, err := url.PathUnescape(row.id)
			if err != nil {
				t.Fatalf("unescape %q: %v", row.id, err)
			}

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.SetPathValue("namespace", row.ns)
			r.SetPathValue("unit_id", id)

			err = contextapi.ResolveUnitByID(handler.Handler, r)
			if !contextapi.IsNotFoundAnswer(err) {
				t.Fatalf("resolver error = %v, want the not-found answer", err)
			}

			for _, text := range row.stored {
				if strings.Contains(err.Error(), text) {
					t.Errorf("error repeats the stored text %q: %v", text, err)
				}
			}
		})
	}
}

// TestNamespaceNotFoundMapping proves that findNamespace itself maps a missing
// namespace and a name that is not valid to the not-found answer. The
// classifier maps namespace.ErrNotFound to the same 404, so this test calls
// the resolvers below the classifier.
func TestNamespaceNotFoundMapping(t *testing.T) {
	t.Parallel()

	for _, ns := range []string{"absent", "bad!ns", ""} {
		t.Run(fmt.Sprintf("namespace %q", ns), func(t *testing.T) {
			t.Parallel()

			units, deleted := hostileUnits()
			handler := newSpyHandler(t, newAuthorizer(), units, deleted, nil)

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.SetPathValue("namespace", ns)
			r.SetPathValue("unit_id", testUnitID)

			if err := contextapi.ResolveNamespace(handler.Handler, r); !contextapi.IsNotFoundAnswer(err) {
				t.Errorf("namespace resolver error = %v, want the not-found answer", err)
			}

			if err := contextapi.ResolveUnitByID(handler.Handler, r); !contextapi.IsNotFoundAnswer(err) {
				t.Errorf("unit resolver error = %v, want the not-found answer", err)
			}
		})
	}
}

// TestResolverNeverListsUnits proves that no route lists the units of a
// namespace to find one unit, before or after an authorization check, for a
// caller that is not signed in, a caller that is denied, and a caller that
// holds every grant.
func TestResolverNeverListsUnits(t *testing.T) {
	t.Parallel()

	callers := []struct {
		name  string
		token string
		holds []string
	}{
		{"no token", "", nil},
		{"token that is not valid", "wrong", nil},
		{"denied caller", testToken, nil},
		{"caller with every grant", testToken, []string{"namespace:read acme", "unit:read opentofu:acme/net"}},
	}

	ids := []string{testUnitID, "ws-unknown", "ws-gone", "ws-badname", "ws-globex"}

	for _, caller := range callers {
		t.Run(caller.name, func(t *testing.T) {
			t.Parallel()

			units, deleted := hostileUnits()
			handler := newSpyHandler(t, newAuthorizer(caller.holds...), units, deleted, nil)

			driveUnitRoutes(t, handler, caller.token, ids)

			if n := units.lists.Load(); n != 0 {
				t.Errorf("ListUnits calls = %d, want 0", n)
			}

			if n := deleted.lists.Load(); n != 0 {
				t.Errorf("ListDeletedUnits calls = %d, want 0", n)
			}

			if !signedIn(caller.token) && units.gets.Load() != 0 {
				t.Errorf("GetUnit calls = %d, want 0 for a caller that is not signed in", units.gets.Load())
			}
		})
	}
}

func signedIn(token string) bool { return token == testToken }

// driveUnitRoutes sends each method of the unit route for each ID.
func driveUnitRoutes(t *testing.T, handler http.Handler, token string, ids []string) {
	t.Helper()

	for _, id := range ids {
		for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
			serve(t, handler, request{
				method: method, path: unitContextPath("acme", id), token: token, body: `{"onCall":"pager:x"}`,
			})
		}
	}
}

// TestMalformedUnitIDNeverReachesTheStore proves that the resolver refuses a
// unit ID that is malformed or too long before it reads any store. Each row
// answers the not-found answer, and the spies count no read.
func TestMalformedUnitIDNeverReachesTheStore(t *testing.T) {
	t.Parallel()

	ids := []string{
		"", "a b", "ws~bad", "ws-1/../x", "ws-1\x00", "ws-1\r\nX: 1", "\"<script>", ".hidden", "-lead",
		strings.Repeat("a", 101), strings.Repeat("a", 5000),
	}

	for _, id := range ids {
		t.Run(fmt.Sprintf("ID %.20q", id), func(t *testing.T) {
			t.Parallel()

			units, deleted := hostileUnits()
			handler := newSpyHandler(t, newAuthorizer(), units, deleted, nil)

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			r.SetPathValue("namespace", "acme")
			r.SetPathValue("unit_id", id)

			if err := contextapi.ResolveUnitByID(handler.Handler, r); !contextapi.IsNotFoundAnswer(err) {
				t.Errorf("resolver error = %v, want the not-found answer", err)
			}

			if units.gets.Load() != 0 || deleted.queries.Load() != 0 || deleted.lists.Load() != 0 {
				t.Errorf("store reads: GetUnit %d, QueryDeletedUnits %d, ListDeletedUnits %d, want 0",
					units.gets.Load(), deleted.queries.Load(), deleted.lists.Load())
			}
		})
	}
}

// TestUnavailableUnitStoreAnswersAnError proves that a storage failure of
// GetUnit is not an unknown unit: the route answers 500 and never reads the
// soft-deleted units.
func TestUnavailableUnitStoreAnswersAnError(t *testing.T) {
	t.Parallel()

	units, deleted := hostileUnits()
	units.getErr = errUnitStoreDown
	handler := newSpyHandler(
		t,
		newAuthorizer("namespace:read acme", "unit:read opentofu:acme/net"),
		units,
		deleted,
		nil,
	)

	rec := serve(t, handler, request{
		method: http.MethodGet, path: unitContextPath("acme", testUnitID), token: testToken, body: "",
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d body %s, want 500", rec.Code, rec.Body.String())
	}

	if deleted.queries.Load() != 0 {
		t.Errorf("QueryDeletedUnits calls = %d, want 0 after a storage failure", deleted.queries.Load())
	}
}

// TestDeletedUnitFoundByOneRestrictedQuery proves that a soft-deleted unit is
// found with one query that names the unit ID, not with a list.
func TestDeletedUnitFoundByOneRestrictedQuery(t *testing.T) {
	t.Parallel()

	units, deleted := hostileUnits()
	handler := newSpyHandler(
		t,
		newAuthorizer("namespace:read acme", "unit:read opentofu:acme/old"),
		units,
		deleted,
		nil,
	)

	rec := serve(t, handler, request{
		method: http.MethodGet, path: unitContextPath("acme", "ws-gone"), token: testToken, body: "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", rec.Code, rec.Body.String())
	}

	if deleted.queries.Load() != 1 || deleted.lists.Load() != 0 {
		t.Errorf(
			"QueryDeletedUnits %d, ListDeletedUnits %d, want 1 and 0",
			deleted.queries.Load(),
			deleted.lists.Load(),
		)
	}

	deleted.lastMu.Lock()
	q := deleted.last
	deleted.lastMu.Unlock()

	if q == nil || !q.Restricted || !reflect.DeepEqual(q.UnitIDs, []string{"ws-gone"}) || q.Limit != 2 ||
		q.Namespace != "acme" {
		t.Errorf("query = %+v, want a query restricted to the unit ws-gone of acme with limit 2", q)
	}
}

// TestDamagedRowIsLoggedNotServed proves that the log holds the cause and the
// unit ID of a stored row that fails its checks, and that the answer holds
// neither.
func TestDamagedRowIsLoggedNotServed(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"ws-badname", "ws-gonebadname"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			var logs strings.Builder

			units, deleted := hostileUnits()
			handler := newSpyHandler(t, newAuthorizer("namespace:read acme"), units, deleted, &logs)

			rec := serve(t, handler, request{
				method: http.MethodGet, path: unitContextPath("acme", id), token: testToken, body: "",
			})
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
				t.Errorf("status %d body %s, want a 404 with no stored text", rec.Code, rec.Body.String())
			}

			if !strings.Contains(logs.String(), id) {
				t.Errorf("log %q does not name the unit ID %q", logs.String(), id)
			}

			if !strings.Contains(logs.String(), "secret") {
				t.Errorf("log %q does not name the cause", logs.String())
			}
		})
	}
}

// compareSeen fails when got differs from want in status, body or headers, or
// when the body repeats a text of stored.
func compareSeen(t *testing.T, got, want seen, stored []string) {
	t.Helper()

	if got.status != want.status || got.body != want.body {
		t.Errorf("status %d body %q, want status %d body %q", got.status, got.body, want.status, want.body)
	}

	if !reflect.DeepEqual(got.header, want.header) {
		t.Errorf("headers = %v, want %v", got.header, want.header)
	}

	for _, text := range stored {
		if strings.Contains(got.body, text) {
			t.Errorf("body repeats the stored text %q: %s", text, got.body)
		}
	}
}

// TestDamagedRowLogBoundsStoredText proves that a stored name of 5000 bytes
// does not make a log line of 5000 bytes.
func TestDamagedRowLogBoundsStoredText(t *testing.T) {
	t.Parallel()

	var logs strings.Builder

	units, deleted := hostileUnits()
	units.rows["ws-badname"] = liveRow("acme", "opentofu", strings.Repeat("x/", 2500), "ws-badname")
	handler := newSpyHandler(t, newAuthorizer("namespace:read acme"), units, deleted, &logs)

	serve(t, handler, request{
		method: http.MethodGet, path: unitContextPath("acme", "ws-badname"), token: testToken, body: "",
	})

	if logs.Len() == 0 || logs.Len() > 1000 {
		t.Errorf("log of %d bytes, want 1..1000", logs.Len())
	}
}
