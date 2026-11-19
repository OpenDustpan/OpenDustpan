package inventory_test

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
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

// countingDeleted is an inventory.DeletedReader that counts the calls it passes
// to inner, so a test can prove that the store was not read.
type countingDeleted struct {
	inner coreinventory.DeletedReader
	reads int
}

func (c *countingDeleted) ListDeletedUnits(
	ctx context.Context, ns namespace.Name,
) ([]coreinventory.DeletedEntry, error) {
	c.reads++

	return c.inner.ListDeletedUnits(ctx, ns)
}

func (c *countingDeleted) QueryDeletedUnits(
	ctx context.Context, q coreinventory.DeletedQuery,
) (coreinventory.DeletedPage, error) {
	c.reads++

	return c.inner.QueryDeletedUnits(ctx, q)
}

func (c *countingDeleted) GetDeletedUnit(
	ctx context.Context, ns namespace.Name, tool, unitID string,
) (coreinventory.DeletedEntry, error) {
	c.reads++

	return c.inner.GetDeletedUnit(ctx, ns, tool, unitID)
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
	handler testHandler
	pulumi  *memLifecycle
	deleted *countingDeleted
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

	return buildGuardedFixture(t, guardSpec{
		entries:      entries,
		authz:        authz,
		lifecycleErr: lifecycleErr,
		scope:        scope,
		guard:        stategate.Off("the test keeps no gates"),
	})
}

// guardSpec holds the parts of a fixture with a state guard.
type guardSpec struct {
	entries      []coreinventory.DeletedEntry
	authz        identity.Authorizer
	lifecycleErr error
	scope        apiinventory.DeletedReadScope
	guard        stategate.Guard
}

// buildGuardedFixture is buildDeletedFixture with the state guard of spec.
func buildGuardedFixture(t *testing.T, spec guardSpec) deletedFixture {
	t.Helper()

	cfg, pulumi := guardedConfig(t, spec)

	deleted := &countingDeleted{inner: cfg.Deleted, reads: 0}
	cfg.Deleted = deleted

	handler, err := newCheckedHandler(t, cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return deletedFixture{handler: handler, pulumi: pulumi, deleted: deleted}
}

// guardedConfig returns the config of a handler over the deleted units of
// spec, with the state guard of spec, and the lifecycle of the Pulumi units.
func guardedConfig(t *testing.T, spec guardSpec) (apiinventory.Config, *memLifecycle) {
	t.Helper()

	entries, authz, lifecycleErr, scope, guard := spec.entries, spec.authz, spec.lifecycleErr, spec.scope, spec.guard

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	namespaces, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	pulumi := &memLifecycle{err: lifecycleErr, calls: nil}

	return apiinventory.Config{
		Placements:    nil,
		OutputSharing: nil,
		Outputs:       nil,
		Views:         nil,
		Audit:         newTestAudit(), OnAuditFailure: nil, Locks: nil, PublicURL: "",
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
		DenialLimiter: nil,
		StateGuard:    guard,
	}, pulumi
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

// firstPageToken returns the nextPageToken of the first page of one deleted
// unit per page.
func firstPageToken(t *testing.T, f deletedFixture) string {
	t.Helper()

	page := decodeDeleted(t, f.serve(t, http.MethodGet, listTarget(url.Values{"pageSize": {"1"}}), ""))
	if page.NextPageToken == "" {
		t.Fatal("first page has no nextPageToken")
	}

	return page.NextPageToken
}

// rewriteToken decodes token, applies change to its JSON object, and encodes
// the result again as a page token.
func rewriteToken(t *testing.T, token string, change func(m map[string]any)) string {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}

	var wire map[string]any
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("token is not one JSON object: %v", err)
	}

	change(wire)

	out, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("encode changed token: %v", err)
	}

	return base64.RawURLEncoding.EncodeToString(out)
}

// TestListDeletedUnitsHostileCursor proves that a cursor which is not
// well-formed, or which another query made, answers 400 at the boundary, before
// the store reads it. The unchanged cursor and a cursor with a normal name
// answer 200.
func TestListDeletedUnitsHostileCursor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		raw    string
		change func(m map[string]any)
		want   int
	}{
		{
			name:   "unknown member",
			raw:    "",
			change: func(m map[string]any) { m["x"] = 1 },
			want:   http.StatusBadRequest,
		},
		{
			name:   "unknown tool",
			raw:    "",
			change: func(m map[string]any) { m["t"] = "terraform" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "empty name",
			raw:    "",
			change: func(m map[string]any) { m["n"] = "" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "control character in name",
			raw:    "",
			change: func(m map[string]any) { m["n"] = "web\nprod" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "long unit ID",
			raw:    "",
			change: func(m map[string]any) { m["u"] = strings.Repeat("a", 1025) },
			want:   http.StatusBadRequest,
		},
		{
			name:   "long name",
			raw:    "",
			change: func(m map[string]any) { m["n"] = strings.Repeat("a", 1025) },
			want:   http.StatusBadRequest,
		},
		{
			name:   "bell character U+0007 in name",
			raw:    "",
			change: func(m map[string]any) { m["n"] = "web\x07prod" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "right-to-left override U+202E in name",
			raw:    "",
			change: func(m map[string]any) { m["n"] = "web\u202eprod" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "line separator U+2028 in unit ID",
			raw:    "",
			change: func(m map[string]any) { m["u"] = "stk\u20281" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "paragraph separator U+2029 in name",
			raw:    "",
			change: func(m map[string]any) { m["n"] = "web\u2029prod" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "normal name",
			raw:    "",
			change: func(m map[string]any) { m["n"] = "web/staging" },
			want:   http.StatusOK,
		},
		{
			name:   "empty unit ID",
			raw:    "",
			change: func(m map[string]any) { m["u"] = "" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "other fingerprint",
			raw:    "",
			change: func(m map[string]any) { m["f"] = "0000000000000000" },
			want:   http.StatusBadRequest,
		},
		{
			name:   "token over the length limit",
			raw:    strings.Repeat("A", 4097),
			change: nil,
			want:   http.StatusBadRequest,
		},
		{
			name:   "unchanged cursor",
			raw:    "",
			change: nil,
			want:   http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(t, fiveDeleted(), selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)

			token := tc.raw
			switch {
			case tc.raw != "":
			case tc.change != nil:
				token = rewriteToken(t, firstPageToken(t, f), tc.change)
			default:
				token = firstPageToken(t, f)
			}

			// The first page above reads the store once. Count only the reads of the request.
			f.deleted.reads = 0

			rec := f.serve(t, http.MethodGet, listTarget(url.Values{"pageSize": {"1"}, "pageToken": {token}}), "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}

			if tc.want == http.StatusBadRequest && !strings.Contains(rec.Body.String(), "request.invalid-query") {
				t.Fatalf("body does not hold request.invalid-query: %s", rec.Body.String())
			}

			if tc.want == http.StatusBadRequest && f.deleted.reads != 0 {
				t.Fatalf("store was called %d times for a hostile cursor, want 0", f.deleted.reads)
			}
		})
	}
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
			name: "manual lock", target: restoreTarget, body: "{}", err: &manuallock.LockedError{
				Lock: manuallock.Lock{
					Since:  time.Date(2026, time.October, 9, 9, 0, 0, 0, time.UTC),
					Target: manuallock.NamespaceTarget("acme"), Holder: "alice", Reason: "freeze",
				},
				Unit: collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"},
				Noun: "stack", Name: "acme/web/prod",
			},
			want: http.StatusConflict,
		},
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

// answerOf is the whole answer of one request: status, body and headers.
type answerOf struct {
	code   int
	body   string
	header http.Header
}

// answerFor returns the answer in rec. The headers that carry a per-request
// value are removed, so that two answers to the same refusal compare equal.
func answerFor(rec *httptest.ResponseRecorder) answerOf {
	h := rec.Header().Clone()
	h.Del("X-Request-Id")
	h.Del("Date")

	return answerOf{code: rec.Code, body: rec.Body.String(), header: h}
}

// sameAnswer fails when two answers differ in status, body or headers.
func sameAnswer(t *testing.T, label string, got, want answerOf) {
	t.Helper()

	if got.code != want.code || got.body != want.body || !maps.EqualFunc(got.header, want.header, slices.Equal) {
		t.Fatalf("%s = %d %q %v, want %d %q %v", label, got.code, got.body, got.header, want.code, want.body, want.header)
	}
}

// TestUnknownToolAnswersLikeAKnownOne proves that a caller without the action
// gets the same 403 (status, body and headers) for a tool that the server
// does not serve, for a tool that it serves and for another namespace, so the
// answer does not show which tools the server serves. A caller with the
// action gets 404 for the unknown tool. A malformed unit ID answers the same
// 400 to every caller, and a malformed tool answers 400.
func TestUnknownToolAnswersLikeAKnownOne(t *testing.T) {
	t.Parallel()

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

			other := strings.Replace(tc.known, "/pulumi/", "/nosuchtool/", 1)

			denied := newDeletedFixture(
				t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{tc.action: true}}, nil,
			)

			want := answerFor(denied.serve(t, tc.method, tc.known, ""))
			if want.code != http.StatusForbidden {
				t.Fatalf("denied %s %s = %d, want %d", tc.method, tc.known, want.code, http.StatusForbidden)
			}

			variants := map[string]string{
				"unknown tool":      other,
				"another namespace": strings.Replace(tc.known, "organization=acme", "organization=other", 1),
			}

			for label, target := range variants {
				sameAnswer(t, label, answerFor(denied.serve(t, tc.method, target, "")), want)
			}

			// A malformed unit ID cannot name a unit, so syntax validation
			// refuses it with the same 400 before any lookup or check: the
			// answer is the same for a denied and an allowed caller.
			badID := strings.Replace(tc.known, seededUnitID, "stk%00..%2F", 1)
			allowedCaller := newDeletedFixture(
				t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil,
			)

			badAnswer := answerFor(denied.serve(t, tc.method, badID, ""))
			if badAnswer.code != http.StatusBadRequest {
				t.Fatalf("malformed unit ID = %d %s, want 400", badAnswer.code, badAnswer.body)
			}

			sameAnswer(t, "malformed unit ID, allowed caller", answerFor(allowedCaller.serve(t, tc.method, badID, "")), badAnswer)

			bad := strings.Replace(tc.known, "/pulumi/", "/Bad!Tool/", 1)

			rec := denied.serve(t, tc.method, bad, "")
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "request.invalid-object") {
				t.Fatalf("malformed tool = %d %s, want 400 request.invalid-object", rec.Code, rec.Body.String())
			}

			allowed := newDeletedFixture(
				t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil,
			)

			if rec := allowed.serve(t, tc.method, other, ""); rec.Code != http.StatusNotFound {
				t.Fatalf("allowed %s %s = %d, want %d", tc.method, other, rec.Code, http.StatusNotFound)
			}
		})
	}
}

// TestUnknownToolReadsTheStoreLikeAKnownOne proves that a denied caller makes
// the same number of store reads for a tool that the server serves as for one
// that it does not serve, so the time of the answer does not show which tools
// the server serves.
func TestUnknownToolReadsTheStoreLikeAKnownOne(t *testing.T) {
	t.Parallel()

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

			deny := selectiveAuthorizer{deny: map[identity.Action]bool{tc.action: true}}
			known := newDeletedFixture(t, seededDeleted().entries, deny, nil)
			unknown := newDeletedFixture(t, seededDeleted().entries, deny, nil)

			known.serve(t, tc.method, tc.known, "")
			unknown.serve(t, tc.method, strings.Replace(tc.known, "/pulumi/", "/nosuchtool/", 1), "")

			if known.deleted.reads == 0 || known.deleted.reads != unknown.deleted.reads {
				t.Fatalf("store reads: served tool %d, unknown tool %d, want equal and not zero",
					known.deleted.reads, unknown.deleted.reads)
			}
		})
	}
}

// TestDeletedUnitOfAnUnservedToolAnswers404 proves that the lifecycle check
// after the authorization stands alone: a stored deleted unit under a tool
// that the server does not serve, and a caller with the action, answer 404
// and never reach a lifecycle (a nil lifecycle would panic).
func TestDeletedUnitOfAnUnservedToolAnswers404(t *testing.T) {
	t.Parallel()

	entries := []coreinventory.DeletedEntry{deletedEntry("nosuchtool", "web/prod", seededUnitID, 30)}
	cases := []struct {
		name   string
		method string
		target string
	}{
		{
			name:   "restore",
			method: http.MethodPost,
			target: "/api/inventory/deleted-units/nosuchtool/" + seededUnitID + "/restore?organization=acme",
		},
		{
			name:   "purge",
			method: http.MethodDelete,
			target: "/api/inventory/deleted-units/nosuchtool/" + seededUnitID + "?organization=acme",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(t, entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)

			if rec := f.serve(t, tc.method, tc.target, ""); rec.Code != http.StatusNotFound {
				t.Fatalf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body.String(), http.StatusNotFound)
			}

			if len(f.pulumi.calls) != 0 {
				t.Fatalf("lifecycle calls = %+v, want none", f.pulumi.calls)
			}
		})
	}
}

// TestDeletedPageTokenBelongsToItsQuery proves that a page token works only
// for the filters that made it: another tool, name prefix or purge window
// answers 400, and another page size keeps the token.
func TestDeletedPageTokenBelongsToItsQuery(t *testing.T) {
	t.Parallel()

	f := newDeletedFixture(t, fiveDeleted(), selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)

	first := decodeDeleted(t, f.serve(t, http.MethodGet, listTarget(url.Values{"pageSize": {"2"}}), ""))
	if first.NextPageToken == "" {
		t.Fatal("first page has no next page token")
	}

	other := []url.Values{
		{"tool": {"OpenTofu"}},
		{"namePrefix": {"s"}},
		{"purgeWithinDays": {"30"}},
	}

	for _, q := range other {
		q.Set("pageToken", first.NextPageToken)

		rec := f.serve(t, http.MethodGet, listTarget(q), "")
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not belong to this query") {
			t.Errorf("token with %v = %d %s, want 400", q, rec.Code, rec.Body.String())
		}
	}

	same := url.Values{"pageSize": {"3"}, "pageToken": {first.NextPageToken}}
	if rec := f.serve(t, http.MethodGet, listTarget(same), ""); rec.Code != http.StatusOK {
		t.Fatalf("token with another page size = %d %s, want 200", rec.Code, rec.Body.String())
	}
}
