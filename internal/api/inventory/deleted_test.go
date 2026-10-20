package inventory_test

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errBoom is a store failure without a domain meaning.
var errBoom = errors.New("boom")

// seededUnitID is the ID of the deleted Pulumi stack acme/web/prod that
// seededDeleted holds.
const seededUnitID = "stk-1"

// deletedNowUnix is the clock of the deleted-units tests, in Unix seconds.
const deletedNowUnix = 1_800_000_000

// memDeleted is an in-memory inventory.DeletedReader.
type memDeleted struct {
	entries []coreinventory.DeletedEntry
}

func (m *memDeleted) ListDeletedUnits(
	_ context.Context, ns namespace.Name,
) ([]coreinventory.DeletedEntry, error) {
	q := coreinventory.DeletedQuery{
		PurgeBefore: time.Time{}, After: nil, Namespace: ns, Tool: "", NamePrefix: "",
		UnitIDs: nil, Restricted: false, Limit: coreinventory.MaxDeletedPageSize,
	}

	return coreinventory.PageDeleted(m.sorted(), q).Entries, nil
}

func (m *memDeleted) QueryDeletedUnits(
	_ context.Context, q coreinventory.DeletedQuery,
) (coreinventory.DeletedPage, error) {
	return coreinventory.PageDeleted(m.sorted(), q), nil
}

func (m *memDeleted) GetDeletedUnit(
	_ context.Context, ns namespace.Name, tool, unitID string,
) (coreinventory.DeletedEntry, error) {
	for _, e := range m.entries {
		if e.Namespace == ns && e.Tool == tool && e.UnitID == unitID {
			return e, nil
		}
	}

	return coreinventory.DeletedEntry{}, coreinventory.ErrNotFound
}

// sorted returns the entries ordered by tool, then name, then unit ID, as
// the port contract requires of implementations.
func (m *memDeleted) sorted() []coreinventory.DeletedEntry {
	out := slices.Clone(m.entries)
	slices.SortFunc(out, func(a, b coreinventory.DeletedEntry) int {
		return cmp.Or(
			strings.Compare(a.Tool, b.Tool), strings.Compare(a.Name, b.Name), strings.Compare(a.UnitID, b.UnitID),
		)
	})

	return out
}

// deletedEntry returns a deleted unit of acme, deleted at the test clock and
// purged after the given number of days.
func deletedEntry(tool, name, unitID string, purgeInDays int) coreinventory.DeletedEntry {
	return coreinventory.DeletedEntry{
		DeletedAt:  time.Unix(deletedNowUnix, 0),
		PurgeAfter: time.Unix(deletedNowUnix, 0).Add(time.Duration(purgeInDays) * 24 * time.Hour),
		Namespace:  "acme",
		Tool:       tool,
		UnitID:     unitID,
		Name:       name,
	}
}

// seededDeleted holds the deleted Pulumi stack acme/web/prod.
func seededDeleted() *memDeleted {
	return &memDeleted{entries: []coreinventory.DeletedEntry{deletedEntry("pulumi", "web/prod", seededUnitID, 30)}}
}

// lifecycleCall is one recorded restore or purge.
type lifecycleCall struct {
	entry  audit.Entry
	ns     namespace.Name
	unitID string
	name   string
	purge  bool
}

// memLifecycle is an inventory.Lifecycle that records its calls and answers
// err when it is set.
type memLifecycle struct {
	err   error
	calls []lifecycleCall
}

func (m *memLifecycle) Restore(
	_ context.Context, ns namespace.Name, unitID, name string, e audit.Entry,
) (string, error) {
	m.calls = append(m.calls, lifecycleCall{entry: e, ns: ns, unitID: unitID, name: name, purge: false})
	if m.err != nil {
		return "", m.err
	}

	if name == "" {
		return "web/prod", nil
	}

	return "web/" + name, nil
}

func (m *memLifecycle) Purge(_ context.Context, ns namespace.Name, unitID string, e audit.Entry) error {
	m.calls = append(m.calls, lifecycleCall{entry: e, ns: ns, unitID: unitID, name: "", purge: true})

	return m.err
}

// testLifecycles returns a lifecycle for each tool that succeeds.
func testLifecycles() map[string]coreinventory.Lifecycle {
	return map[string]coreinventory.Lifecycle{
		"pulumi":   &memLifecycle{err: nil, calls: nil},
		"opentofu": &memLifecycle{err: nil, calls: nil},
	}
}

// deletedFixture is a handler over one seeded acme namespace, its deleted
// units and a recording Pulumi lifecycle.
type deletedFixture struct {
	handler *apiinventory.Handler
	pulumi  *memLifecycle
}

// newDeletedFixture builds a deletedFixture with the deleted units entries.
func newDeletedFixture(
	t *testing.T, entries []coreinventory.DeletedEntry, authz identity.Authorizer, lifecycleErr error,
) deletedFixture {
	t.Helper()

	return buildDeletedFixture(t, entries, authz, lifecycleErr, fixedScope{err: nil, ids: nil, all: true})
}

// buildDeletedFixture is newDeletedFixture with the read scope scope.
func buildDeletedFixture(
	t *testing.T,
	entries []coreinventory.DeletedEntry,
	authz identity.Authorizer,
	lifecycleErr error,
	scope apiinventory.DeletedReadScope,
) deletedFixture {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	namespaces, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	pulumi := &memLifecycle{err: lifecycleErr, calls: nil}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		Audit: nil, OnAuditFailure: nil,
		PublicURL:  "",
		Moves:      nil,
		Namespaces: namespaces,
		Units:      &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Deleted:    &memDeleted{entries: entries},
		History:    nil,
		Lifecycles: map[string]coreinventory.Lifecycle{
			"pulumi":   pulumi,
			"opentofu": &memLifecycle{err: nil, calls: nil},
		},
		ReadScope:     scope,
		Collections:   nil,
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           func() time.Time { return time.Unix(deletedNowUnix, 0) },
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return deletedFixture{handler: handler, pulumi: pulumi}
}

// serve sends one signed-in request and returns the recorder.
func (f deletedFixture) serve(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "token test-token")
	req.Header.Set("User-Agent", "restore-test")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// deletedUnitBody is one decoded deleted unit.
type deletedUnitBody struct {
	Tool       string `json:"tool"`
	Reference  string `json:"reference"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UnitID     string `json:"unitID"`
	DeletedAt  string `json:"deletedAt"`
	PurgeAfter string `json:"purgeAfter"`
}

// deletedUnitsBody is the decoded GET /api/dustpan/v1/namespaces/{namespace}/deleted-units response.
type deletedUnitsBody struct {
	NextPageToken string            `json:"nextPageToken"`
	Units         []deletedUnitBody `json:"units"`
	Total         int               `json:"total"`
}

func decodeDeleted(t *testing.T, rec *httptest.ResponseRecorder) deletedUnitsBody {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got deletedUnitsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode deleted units: %v (%s)", err, rec.Body.String())
	}

	if got.Units == nil {
		t.Fatalf("units is null, want an array (%s)", rec.Body.String())
	}

	return got
}

// unitIDs returns the unit IDs of a page in order.
func unitIDs(units []deletedUnitBody) []string {
	out := make([]string, 0, len(units))
	for _, u := range units {
		out = append(out, u.UnitID)
	}

	return out
}

// listTarget is the deleted-units path of acme with the query values q.
func listTarget(q url.Values) string {
	return "/api/dustpan/v1/namespaces/acme/deleted-units?" + q.Encode()
}

// fiveDeleted is the fixture of the list tests: three Pulumi stacks and two
// OpenTofu workspaces, with retention ends from 2 to 40 days.
func fiveDeleted() []coreinventory.DeletedEntry {
	return []coreinventory.DeletedEntry{
		deletedEntry("pulumi", "web/prod", "s1", 30),
		deletedEntry("pulumi", "web/dev", "s2", 2),
		deletedEntry("pulumi", "api/prod", "s3", 40),
		deletedEntry("opentofu", "net-prod", "w1", 5),
		deletedEntry("opentofu", "net-dev", "w2", 20),
	}
}

// TestListDeletedUnitsFields proves the wire shape of one deleted unit.
func TestListDeletedUnitsFields(t *testing.T) {
	t.Parallel()

	f := newDeletedFixture(t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)
	got := decodeDeleted(t, f.serve(t, http.MethodGet, listTarget(url.Values{}), ""))

	want := deletedUnitsBody{
		NextPageToken: "",
		Units: []deletedUnitBody{{
			Tool:       "Pulumi",
			Reference:  "pulumi:acme/web/prod",
			Namespace:  "acme",
			Name:       "web/prod",
			UnitID:     seededUnitID,
			DeletedAt:  "2027-01-15T08:00:00Z",
			PurgeAfter: "2027-02-14T08:00:00Z",
		}},
		Total: 1,
	}

	if got.NextPageToken != want.NextPageToken || got.Total != want.Total || len(got.Units) != 1 ||
		got.Units[0] != want.Units[0] {
		t.Fatalf("deleted units = %+v, want %+v", got, want)
	}
}

// TestListDeletedUnitsFilters proves the tool, namePrefix and
// purgeWithinDays filters, alone and together.
func TestListDeletedUnitsFilters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query url.Values
		want  []string
	}{
		{name: "no filter", query: url.Values{}, want: []string{"w2", "w1", "s3", "s2", "s1"}},
		{name: "tool code", query: url.Values{"tool": {"pulumi"}}, want: []string{"s3", "s2", "s1"}},
		{name: "tool label", query: url.Values{"tool": {"OpenTofu"}}, want: []string{"w2", "w1"}},
		{name: "unknown tool", query: url.Values{"tool": {"other"}}, want: []string{}},
		{name: "name prefix", query: url.Values{"namePrefix": {"web/"}}, want: []string{"s2", "s1"}},
		{name: "purge within 5 days", query: url.Values{"purgeWithinDays": {"5"}}, want: []string{"w1", "s2"}},
		{name: "purge within 0 days", query: url.Values{"purgeWithinDays": {"0"}}, want: []string{}},
		{
			name:  "all filters",
			query: url.Values{"tool": {"pulumi"}, "namePrefix": {"web/"}, "purgeWithinDays": {"30"}},
			want:  []string{"s2", "s1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(t, fiveDeleted(), selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)
			got := decodeDeleted(t, f.serve(t, http.MethodGet, listTarget(tc.query), ""))

			if ids := unitIDs(got.Units); strings.Join(ids, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("unit IDs = %v, want %v", ids, tc.want)
			}

			if got.Total != len(tc.want) {
				t.Fatalf("total = %d, want %d", got.Total, len(tc.want))
			}
		})
	}
}

// TestListDeletedUnitsPages proves that the cursor walks every match once,
// in order, and that the total counts every match on each page.
func TestListDeletedUnitsPages(t *testing.T) {
	t.Parallel()

	f := newDeletedFixture(t, fiveDeleted(), selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)

	var seen []string

	cursor := ""

	for range 5 {
		q := url.Values{"pageSize": {"2"}}
		if cursor != "" {
			q.Set("pageToken", cursor)
		}

		got := decodeDeleted(t, f.serve(t, http.MethodGet, listTarget(q), ""))
		if got.Total != 5 {
			t.Fatalf("total = %d, want 5", got.Total)
		}

		seen = append(seen, unitIDs(got.Units)...)
		cursor = got.NextPageToken

		if cursor == "" {
			break
		}
	}

	if want := "w2,w1,s3,s2,s1"; strings.Join(seen, ",") != want {
		t.Fatalf("pages = %v, want %s", seen, want)
	}
}

// TestListDeletedUnitsInvalidQuery proves that a bad query value answers 400.
func TestListDeletedUnitsInvalidQuery(t *testing.T) {
	t.Parallel()

	cases := []url.Values{
		{"pageSize": {"-1"}},
		{"pageSize": {"0"}},
		{"pageSize": {"501"}},
		{"pageSize": {"ten"}},
		{"purgeWithinDays": {"3651"}},
		{"pageToken": {"!!"}},
		{"pageToken": {"bm90LWpzb24"}},
	}

	for _, q := range cases {
		t.Run(q.Encode(), func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(t, fiveDeleted(), selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)

			rec := f.serve(t, http.MethodGet, listTarget(q), "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}

// fixedScope is a DeletedReadScope with a fixed answer for every caller.
type fixedScope struct {
	err error
	ids []string
	all bool
}

func (f fixedScope) ReadableUnits(
	_ context.Context, _ identity.Principal, _ namespace.Name,
) (bool, []string, error) {
	return f.all, f.ids, f.err
}

// TestListDeletedUnitsReadScope proves that the list holds only the units
// that the caller may read, and that the cursor and the total hold no other
// unit: a walk of every page with limit 1 sees only readable IDs, and the
// total counts only them, also with a name prefix.
func TestListDeletedUnitsReadScope(t *testing.T) {
	t.Parallel()

	scope := fixedScope{err: nil, ids: []string{"s1", "w2"}, all: false}
	f := buildDeletedFixture(t, fiveDeleted(), selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil, scope)

	var seen []string

	cursor := ""

	for range 5 {
		q := url.Values{"pageSize": {"1"}}
		if cursor != "" {
			q.Set("pageToken", cursor)
		}

		got := decodeDeleted(t, f.serve(t, http.MethodGet, listTarget(q), ""))
		if got.Total != 2 {
			t.Fatalf("total = %d, want 2", got.Total)
		}

		seen = append(seen, unitIDs(got.Units)...)
		cursor = got.NextPageToken

		if cursor == "" {
			break
		}

		if raw, err := base64.RawURLEncoding.DecodeString(cursor); err != nil ||
			!strings.Contains(string(raw), `"s1"`) && !strings.Contains(string(raw), `"w2"`) {
			t.Fatalf("cursor %s names a unit outside the scope", raw)
		}
	}

	if want := "w2,s1"; strings.Join(seen, ",") != want {
		t.Fatalf("pages = %v, want %s", seen, want)
	}

	got := decodeDeleted(t, f.serve(t, http.MethodGet, listTarget(url.Values{"namePrefix": {"web/d"}}), ""))
	if got.Total != 0 || len(got.Units) != 0 {
		t.Fatalf("prefix of an unreadable unit = %+v, want no unit and total 0", got)
	}
}

// TestListDeletedUnitsEmptyScope proves that a caller who may read no
// deleted unit gets an empty list with total 0, and that a scope error
// answers 500.
func TestListDeletedUnitsEmptyScope(t *testing.T) {
	t.Parallel()

	allow := selectiveAuthorizer{deny: map[identity.Action]bool{}}

	f := buildDeletedFixture(t, fiveDeleted(), allow, nil, fixedScope{err: nil, ids: nil, all: false})

	got := decodeDeleted(t, f.serve(t, http.MethodGet, listTarget(url.Values{}), ""))
	if got.Total != 0 || len(got.Units) != 0 || got.NextPageToken != "" {
		t.Fatalf("deleted units = %+v, want none", got)
	}

	failing := buildDeletedFixture(t, fiveDeleted(), allow, nil, fixedScope{err: errBoom, ids: nil, all: false})

	rec := failing.serve(t, http.MethodGet, listTarget(url.Values{}), "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestListDeletedUnitsUnknownNamespace proves that an unknown namespace
// answers 404.
func TestListDeletedUnitsUnknownNamespace(t *testing.T) {
	t.Parallel()

	f := newDeletedFixture(t, nil, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)

	rec := f.serve(t, http.MethodGet, "/api/dustpan/v1/namespaces/other/deleted-units", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// restoreTarget is the restore path of the seeded unit.
const restoreTarget = "/api/inventory/deleted-units/pulumi/" + seededUnitID + "/restore?organization=acme"

// purgeTarget is the purge path of the seeded unit.
const purgeTarget = "/api/inventory/deleted-units/pulumi/" + seededUnitID + "?organization=acme"

// restoredBody is the decoded answer of a restore.
type restoredBody struct {
	Tool      string `json:"tool"`
	Reference string `json:"reference"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UnitID    string `json:"unitID"`
}

// TestRestoreUnit proves that a restore passes the new name, answers the
// live unit, and gives the tool one audit entry for the unit ID.
func TestRestoreUnit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     string
		sentName string
		wantName string
	}{
		{name: "empty body", body: "", sentName: "", wantName: "web/prod"},
		{name: "empty object", body: "{}", sentName: "", wantName: "web/prod"},
		{name: "new name", body: `{"name":"prod2"}`, sentName: "prod2", wantName: "web/prod2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(
				t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil,
			)

			rec := f.serve(t, http.MethodPost, restoreTarget, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
			}

			var got restoredBody
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}

			want := restoredBody{
				Tool: "Pulumi", Reference: "pulumi:acme/" + tc.wantName, Namespace: "acme",
				Name: tc.wantName, UnitID: seededUnitID,
			}
			if got != want {
				t.Fatalf("answer = %+v, want %+v", got, want)
			}

			checkLifecycleCall(t, f.pulumi, tc.sentName, identity.ActionUnitRestore)
		})
	}
}

// checkLifecycleCall checks that the lifecycle got exactly one call for the
// seeded unit with name and an audit entry for action.
func checkLifecycleCall(t *testing.T, l *memLifecycle, name string, action identity.Action) {
	t.Helper()

	if len(l.calls) != 1 {
		t.Fatalf("lifecycle calls = %d, want 1", len(l.calls))
	}

	call := l.calls[0]
	if call.ns != "acme" || call.unitID != seededUnitID || call.name != name {
		t.Fatalf("call = %+v, want acme %s %q", call, seededUnitID, name)
	}

	e := call.entry
	if e.Action != string(action) || e.ObjectID != seededUnitID || e.Object != "pulumi:acme/web/prod" ||
		e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess || e.Subject != "tester" ||
		e.UserAgent != "restore-test" {
		t.Fatalf("audit entry = %+v", e)
	}
}

// TestRestoreUnitErrors proves the status of each restore failure.
func TestRestoreUnitErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target string
		body   string
		err    error
		want   int
	}{
		{
			name:   "name taken",
			target: restoreTarget,
			body:   "{}",
			err:    coreinventory.ErrNameTaken,
			want:   http.StatusConflict,
		},
		{
			name: "invalid name", target: restoreTarget, body: `{"name":"a b"}`, err: coreinventory.ErrInvalidName,
			want: http.StatusBadRequest,
		},
		{
			name: "purged meanwhile", target: restoreTarget, body: "{}", err: coreinventory.ErrNotFound,
			want: http.StatusNotFound,
		},
		{name: "fenced", target: restoreTarget, body: "{}", err: namespace.ErrFenced, want: http.StatusConflict},
		{
			name: "not home", target: restoreTarget, body: "{}", err: namespace.ErrNotHome,
			want: http.StatusMisdirectedRequest,
		},
		{
			name: "other error", target: restoreTarget, body: "{}", err: errBoom,
			want: http.StatusInternalServerError,
		},
		{name: "invalid body", target: restoreTarget, body: "[]", err: nil, want: http.StatusBadRequest},
		{name: "unknown field", target: restoreTarget, body: `{"label":"x"}`, err: nil, want: http.StatusBadRequest},
		{name: "two values", target: restoreTarget, body: `{} {}`, err: nil, want: http.StatusBadRequest},
		{
			name: "long body", target: restoreTarget, body: strings.Repeat(" ", 5000), err: nil,
			want: http.StatusBadRequest,
		},
		{
			name: "unknown unit", target: "/api/inventory/deleted-units/pulumi/other/restore?organization=acme",
			body: "{}", err: coreinventory.ErrNotFound, want: http.StatusNotFound,
		},
		{name: "unknown tool", target: "/api/inventory/deleted-units/other/" + seededUnitID +
			"/restore?organization=acme", body: "{}", err: nil, want: http.StatusNotFound},
		{
			name: "missing organization", target: "/api/inventory/deleted-units/pulumi/" + seededUnitID + "/restore",
			body: "{}", err: nil, want: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(
				t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, tc.err,
			)

			rec := f.serve(t, http.MethodPost, tc.target, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}

			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != tc.want {
				t.Fatalf("error body = %s, want code %d", rec.Body.String(), tc.want)
			}
		})
	}
}

// TestPurgeUnit proves that a purge answers 204 and gives the tool one
// audit entry for the unit ID, and that a failed purge answers its status.
func TestPurgeUnit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "purged", err: nil, want: http.StatusNoContent},
		{name: "purged meanwhile", err: coreinventory.ErrNotFound, want: http.StatusNotFound},
		{name: "fenced", err: namespace.ErrFenced, want: http.StatusConflict},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(
				t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, tc.err,
			)

			rec := f.serve(t, http.MethodDelete, purgeTarget, "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}

			checkLifecycleCall(t, f.pulumi, "", identity.ActionUnitPurge)

			if !f.pulumi.calls[0].purge {
				t.Fatal("lifecycle got a restore, want a purge")
			}
		})
	}
}

// unitCall is one Authorize call with the unit ID of its object.
type unitCall struct {
	action identity.Action
	object string
	unitID string
}

// unitRecorder allows every action and records every call with the unit ID.
type unitRecorder struct {
	calls []unitCall
}

func (r *unitRecorder) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	r.calls = append(r.calls, unitCall{action: action, object: obj.String(), unitID: obj.UnitID()})

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (*unitRecorder) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// TestRestoreAndPurgeAuthorizeTheUnitID proves that the routes authorize
// their action on the deleted unit with its ID, and that a denied action
// changes nothing.
func TestRestoreAndPurgeAuthorizeTheUnitID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		method string
		target string
		action identity.Action
	}{
		{method: http.MethodPost, target: restoreTarget, action: identity.ActionUnitRestore},
		{method: http.MethodDelete, target: purgeTarget, action: identity.ActionUnitPurge},
	}

	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			t.Parallel()

			rec := &unitRecorder{calls: nil}
			f := newDeletedFixture(t, seededDeleted().entries, rec, nil)

			if got := f.serve(t, tc.method, tc.target, ""); got.Code >= http.StatusMultipleChoices {
				t.Fatalf("status = %d (%s)", got.Code, got.Body.String())
			}

			want := unitCall{action: tc.action, object: "pulumi:acme/web/prod", unitID: seededUnitID}
			if len(rec.calls) != 1 || rec.calls[0] != want {
				t.Fatalf("authorize calls = %+v, want one %s on the unit ID", rec.calls, tc.action)
			}

			denied := newDeletedFixture(
				t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{tc.action: true}}, nil,
			)
			if got := denied.serve(t, tc.method, tc.target, ""); got.Code != http.StatusForbidden {
				t.Fatalf("denied status = %d, want %d", got.Code, http.StatusForbidden)
			}

			if len(denied.pulumi.calls) != 0 {
				t.Fatalf("denied request reached the lifecycle: %+v", denied.pulumi.calls)
			}
		})
	}
}

// TestUnknownUnitAnswersLikeAForbiddenOne proves that a caller without the
// action gets 403 for an unknown unit ID and for a deleted unit alike, so the
// answer does not show which IDs exist. A caller with the action gets 404 for
// the unknown ID.
func TestUnknownUnitAnswersLikeAForbiddenOne(t *testing.T) {
	t.Parallel()

	unknown := "/api/inventory/deleted-units/pulumi/stk-unknown?organization=acme"

	cases := []struct {
		action identity.Action
		method string
		known  string
	}{
		{action: identity.ActionUnitRestore, method: http.MethodPost, known: restoreTarget},
		{action: identity.ActionUnitPurge, method: http.MethodDelete, known: purgeTarget},
	}

	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			t.Parallel()

			unknownTarget := unknown
			if tc.method == http.MethodPost {
				unknownTarget = strings.Replace(unknown, "?", "/restore?", 1)
			}

			denied := newDeletedFixture(
				t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{tc.action: true}}, nil,
			)

			for _, target := range []string{tc.known, unknownTarget} {
				if rec := denied.serve(t, tc.method, target, ""); rec.Code != http.StatusForbidden {
					t.Fatalf("denied %s %s = %d, want %d", tc.method, target, rec.Code, http.StatusForbidden)
				}
			}

			allowed := newDeletedFixture(
				t,
				seededDeleted().entries,
				selectiveAuthorizer{deny: map[identity.Action]bool{}},
				coreinventory.ErrNotFound,
			)

			if rec := allowed.serve(t, tc.method, unknownTarget, ""); rec.Code != http.StatusNotFound {
				t.Fatalf("allowed %s %s = %d, want %d", tc.method, unknownTarget, rec.Code, http.StatusNotFound)
			}
		})
	}
}
