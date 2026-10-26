package runnerapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/source"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// sourceFilterURL is the path of the source filter of the namespace acme.
const sourceFilterURL = "/api/dustpan/v1/namespaces/acme/source-filter"

// emptyDocument is the answer for a namespace that never set the filter.
const emptyDocument = `{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}`

// fullFilter is the body of a change that sets each field.
const fullFilter = `{"submodules":["https://git.example.com/platform/*"],` +
	`"largeFiles":["https://git.example.com/acme/infra"],"failOnDenied":true,"protectedBranches":["main","release/1"]}`

// sourceEnv is newEnvOn with the authorizer authz, so that a case can hold
// some actions and not others.
func sourceEnv(t *testing.T, open func(t *testing.T) stores, authz identity.Authorizer) *env {
	t.Helper()

	s := open(t)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme", HomeRegion: region,
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.namespaces.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	clock := &skewClock{offset: atomic.Int64{}}
	h, srv, ops := serve(t, s, serveOptions{now: clock.now, authz: authz, claimer: nil, authn: nil})

	return &env{t: t, store: s, server: srv, handler: h, claimer: ops, fence: ns.Fence(), clock: clock}
}

// raw sends a request with a body that the test gives as text, as the
// administrator.
func (e *env) raw(method, path, body string) (int, []byte) {
	e.t.Helper()

	req, err := http.NewRequestWithContext(e.t.Context(), method, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)

	return e.send(req)
}

// managementCode returns the code of an error answer of the management API.
func managementCode(t *testing.T, body []byte) string {
	t.Helper()

	var got struct {
		ErrorCode string `json:"errorCode"`
	}

	decode(t, body, &got)

	return got.ErrorCode
}

// isEmptyFilter reports whether f is the empty filter.
func isEmptyFilter(f *source.Filter) bool {
	return !f.FailOnDenied && len(f.Submodules)+len(f.LargeFiles)+len(f.ProtectedBranches) == 0
}

// storedFilter returns the filter that the store holds for acme.
func storedFilter(t *testing.T, e *env) source.Filter {
	t.Helper()

	f, err := e.store.filters.Filter(t.Context(), "acme")
	if err != nil {
		t.Fatalf("read the stored filter: %v", err)
	}

	return f
}

// filterEntries returns the audit entries of acme that changed the filter,
// oldest first.
func filterEntries(t *testing.T, e *env) []audit.Entry {
	t.Helper()

	listing, err := e.store.audit.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if slices.ContainsFunc(
			listing.Entries[i].Changes,
			func(c audit.Change) bool { return c.Key == "sourceFilter" },
		) {
			out = append(out, listing.Entries[i])
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out
}

func TestSourceFilterDefaultAndRoundTrip(t *testing.T) {
	t.Parallel()

	steps := []struct {
		name, method, body, want string
	}{
		{"GET of a namespace with no filter", http.MethodGet, "", emptyDocument},
		{"PUT of a full filter", http.MethodPut, fullFilter, fullFilter},
		{"GET after the PUT", http.MethodGet, "", fullFilter},
		{"PUT of the empty filter", http.MethodPut, emptyDocument, emptyDocument},
		{"GET after the empty PUT", http.MethodGet, "", emptyDocument},
	}

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			e := sourceEnv(t, b.open, identity.AdminOnly{})

			for _, step := range steps {
				status, body := e.raw(step.method, sourceFilterURL, step.body)
				if status != http.StatusOK || strings.TrimSpace(string(body)) != step.want {
					t.Fatalf("%s = %d %s, want 200 %s", step.name, status, body, step.want)
				}
			}
		})
	}
}

// list returns the JSON list of n texts that name(i) gives, quoted.
func list(n int, name func(i int) string) string {
	items := make([]string, n)
	for i := range items {
		items[i] = strconv.Quote(name(i))
	}

	return "[" + strings.Join(items, ",") + "]"
}

// filterDoc returns a body of a change with the four values as JSON text.
func filterDoc(submodules, largeFiles, failOnDenied, branches string) string {
	return `{"submodules":` + submodules + `,"largeFiles":` + largeFiles + `,"failOnDenied":` + failOnDenied +
		`,"protectedBranches":` + branches + `}`
}

// badChanges are the bodies that a change refuses, with the code of the answer.
func badChanges() []struct{ name, body, code string } {
	patterns := list(source.MaxPatterns+1, func(i int) string { return "https://git.example.com/m" + strconv.Itoa(i) })
	branches := list(source.MaxPatterns+1, func(i int) string { return "b" + strconv.Itoa(i) })
	ok := filterDoc("[]", "[]", "false", "[]")

	return []struct{ name, body, code string }{
		{"not JSON", `not json`, "request.body-invalid"},
		{"an array", `[]`, "request.body-invalid"},
		{"an empty body", ``, "request.body-invalid"},
		{"an unknown field", strings.Replace(ok, "}", `,"x":1}`, 1), "request.body-invalid"},
		{
			"a missing submodules",
			`{"largeFiles":[],"failOnDenied":false,"protectedBranches":[]}`,
			"request.body-invalid",
		},
		{
			"a missing largeFiles",
			`{"submodules":[],"failOnDenied":false,"protectedBranches":[]}`,
			"request.body-invalid",
		},
		{"a missing failOnDenied", `{"submodules":[],"largeFiles":[],"protectedBranches":[]}`, "request.body-invalid"},
		{
			"a missing protectedBranches",
			`{"submodules":[],"largeFiles":[],"failOnDenied":false}`,
			"request.body-invalid",
		},
		{"a null list", filterDoc("null", "[]", "false", "[]"), "request.body-invalid"},
		{"a null switch", filterDoc("[]", "[]", "null", "[]"), "request.body-invalid"},
		{"a wrong type", filterDoc(`"https://git.example.com/a"`, "[]", "false", "[]"), "request.body-invalid"},
		{"a second object", ok + ok, "request.body-invalid"},
		{"data after the object", ok + "x", "request.body-invalid"},
		{"65 submodules", filterDoc(patterns, "[]", "false", "[]"), "source.filter-invalid"},
		{"65 large file patterns", filterDoc("[]", patterns, "false", "[]"), "source.filter-invalid"},
		{"65 protected branches", filterDoc("[]", "[]", "false", branches), "source.filter-invalid"},
		{"a pattern with ftp", filterDoc(`["ftp://git.example.com/a"]`, "[]", "false", "[]"), "source.filter-invalid"},
		{
			"a pattern with a user", filterDoc("[]", `["https://u@git.example.com/a"]`, "false", "[]"),
			"source.filter-invalid",
		},
		{
			"a glob in the middle", filterDoc(`["https://git.example.com/*/a"]`, "[]", "false", "[]"),
			"source.filter-invalid",
		},
		{"a glob branch", filterDoc("[]", "[]", "false", `["release/*"]`), "source.filter-invalid"},
		{"a branch twice", filterDoc("[]", "[]", "false", `["main","main"]`), "source.filter-invalid"},
		{"a branch with a newline", filterDoc("[]", "[]", "false", `["main\nx"]`), "source.filter-invalid"},
		{"an empty branch", filterDoc("[]", "[]", "false", `[""]`), "source.filter-invalid"},
	}
}

func TestSourceFilterRefusesABadChange(t *testing.T) {
	t.Parallel()

	for _, tc := range badChanges() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)

			status, body := e.raw(http.MethodPut, sourceFilterURL, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("PUT = %d %s, want 400", status, body)
			}

			if code := managementCode(t, body); code != tc.code {
				t.Fatalf("PUT code = %q, want %q (%s)", code, tc.code, body)
			}

			if f := storedFilter(t, e); !isEmptyFilter(&f) {
				t.Fatalf("a refused PUT changed the filter: %+v", f)
			}

			if n := len(filterEntries(t, e)); n != 0 {
				t.Fatalf("a refused PUT wrote %d audit entries of a change", n)
			}
		})
	}
}

func TestSourceFilterAccess(t *testing.T) {
	t.Parallel()

	read, readRunners, manage := identity.ActionNamespaceRead, identity.ActionNamespaceReadRunners,
		identity.ActionNamespaceManageRunners
	absent := "/api/dustpan/v1/namespaces/absent/source-filter"

	tests := []struct {
		name   string
		holds  []identity.Action
		method string
		path   string
		want   int
	}{
		{"GET with no action", nil, http.MethodGet, sourceFilterURL, http.StatusNotFound},
		{"PUT with no action", nil, http.MethodPut, sourceFilterURL, http.StatusNotFound},
		{
			"GET with read-runners and no read",
			[]identity.Action{readRunners},
			http.MethodGet, sourceFilterURL,
			http.StatusNotFound,
		},
		{
			"PUT with manage-runners and no read",
			[]identity.Action{manage},
			http.MethodPut, sourceFilterURL,
			http.StatusNotFound,
		},
		{"GET with read only", []identity.Action{read}, http.MethodGet, sourceFilterURL, http.StatusForbidden},
		{
			"GET with read and read-runners",
			[]identity.Action{read, readRunners},
			http.MethodGet, sourceFilterURL,
			http.StatusOK,
		},
		{
			"PUT with read and read-runners",
			[]identity.Action{read, readRunners},
			http.MethodPut, sourceFilterURL,
			http.StatusForbidden,
		},
		{
			"PUT with read and manage-runners",
			[]identity.Action{read, manage},
			http.MethodPut, sourceFilterURL,
			http.StatusOK,
		},
		{
			"GET with manage-runners only",
			[]identity.Action{read, manage},
			http.MethodGet, sourceFilterURL,
			http.StatusForbidden,
		},
		{
			"GET of an unknown namespace",
			[]identity.Action{read, readRunners},
			http.MethodGet, absent,
			http.StatusNotFound,
		},
		{"PUT of an unknown namespace", []identity.Action{read, manage}, http.MethodPut, absent, http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := sourceEnv(t, openSQLite, holdsOnly{actions: tc.holds})

			status, body := e.raw(tc.method, tc.path, fullFilter)
			if status != tc.want {
				t.Fatalf("%s %s = %d %s, want %d", tc.method, tc.path, status, body, tc.want)
			}

			wantEntries := 0
			if tc.method == http.MethodPut && tc.want == http.StatusOK {
				wantEntries = 1
			}

			checkStoredAfter(t, e, wantEntries)
		})
	}
}

// checkStoredAfter proves that a request wrote wantEntries audit entries of a
// change, and that the stored filter is changed exactly when it wrote one. A
// refused or denied request changes nothing and writes none.
func checkStoredAfter(t *testing.T, e *env, wantEntries int) {
	t.Helper()

	f := storedFilter(t, e)
	if changed := !isEmptyFilter(&f); changed != (wantEntries > 0) {
		t.Fatalf("the stored filter is %+v, want a change: %t", f, wantEntries > 0)
	}

	if n := len(filterEntries(t, e)); n != wantEntries {
		t.Fatalf("the request wrote %d change entries, want %d", n, wantEntries)
	}
}

func TestSourceFilterChangeWritesOneAuditEntry(t *testing.T) {
	t.Parallel()

	second := `{"submodules":[],"largeFiles":[],"failOnDenied":false,"protectedBranches":["main"]}`
	changes := []struct{ old, body string }{{emptyDocument, fullFilter}, {fullFilter, second}}

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			e := sourceEnv(t, b.open, identity.AdminOnly{})

			for _, c := range changes {
				if status, got := e.raw(http.MethodPut, sourceFilterURL, c.body); status != http.StatusOK {
					t.Fatalf("PUT = %d %s, want 200", status, got)
				}
			}

			entries := filterEntries(t, e)
			if len(entries) != len(changes) {
				t.Fatalf("%d PUTs wrote %d entries, want one each", len(changes), len(entries))
			}

			for i, c := range changes {
				checkFilterEntry(t, entries[i], c.old, c.body)
			}
		})
	}
}

// checkFilterEntry proves that the entry is one change of the filter by ada
// with manage-runners, from the old text to the new text.
func checkFilterEntry(t *testing.T, got audit.Entry, wantOld, wantNew string) {
	t.Helper()

	if got.Action != string(identity.ActionNamespaceManageRunners) || got.Subject != "ada" ||
		got.Kind != audit.KindChange || got.Object != identity.NamespaceObject("acme").String() ||
		len(got.Changes) != 1 {
		t.Fatalf("entry = %+v, want one change of the filter by ada with manage-runners", got)
	}

	c := got.Changes[0]
	if c.Old == nil || *c.Old != wantOld || c.New != wantNew {
		t.Fatalf("entry change = %v -> %s, want %s -> %s", c.Old, c.New, wantOld, wantNew)
	}
}

// TestSourceFilterSnapshotKey proves that a change of a list or of the switch
// gives a new snapshot key, and that a change of the protected branches alone
// gives the same key, because it does not change the content of a snapshot.
func TestSourceFilterSnapshotKey(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	key := func() string {
		return source.SnapshotKey(&source.Request{
			Namespace: "acme", Connection: "github", Repository: "acme/infra", Commit: strings.Repeat("a", 40),
			Format: source.FormatSHA1, Filter: storedFilter(t, e),
		})
	}

	steps := []struct {
		name string
		body string
		same bool
	}{
		{"the submodules", filterDoc(`["https://git.example.com/m/*"]`, "[]", "false", "[]"), false},
		{"the switch", filterDoc(`["https://git.example.com/m/*"]`, "[]", "true", "[]"), false},
		{"the protected branches", filterDoc(`["https://git.example.com/m/*"]`, "[]", "true", `["main"]`), true},
	}

	previous := key()

	for _, step := range steps {
		if status, got := e.raw(http.MethodPut, sourceFilterURL, step.body); status != http.StatusOK {
			t.Fatalf("PUT of %s = %d %s, want 200", step.name, status, got)
		}

		now := key()
		if (now == previous) != step.same {
			t.Fatalf("a change of %s: same snapshot key = %t, want %t", step.name, now == previous, step.same)
		}

		previous = now
	}
}

// brokenFilters is a source.Filters whose stored filter is not a filter.
type brokenFilters struct {
	source.Filters
}

func (brokenFilters) Filter(context.Context, namespace.Name) (source.Filter, error) {
	return source.Filter{
		Submodules: nil, LargeFiles: nil, FailOnDenied: false, ProtectedBranches: nil,
	}, fmt.Errorf("%w: the stored source filter of acme", source.ErrInvalid)
}

// TestSourceFilterStoredValueIsNotAFilter proves that a read of a stored value
// that is not a filter answers 500 with its own code, and not the 400 of a bad
// request, and that the message names the one action and no stored value.
func TestSourceFilterStoredValueIsNotAFilter(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	_, srv, _ := serve(t, stores{
		namespaces: e.store.namespaces, operations: e.store.operations, jobs: e.store.jobs, units: e.store.units,
		runners: e.store.runners, filters: brokenFilters{Filters: e.store.filters}, audit: e.store.audit,
	}, serveOptions{now: e.clock.now, authz: identity.AdminOnly{}, claimer: nil, authn: nil})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+sourceFilterURL, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	var got struct {
		ErrorCode string `json:"errorCode"`
		Message   string `json:"message"`
	}

	if err = json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.StatusCode != http.StatusInternalServerError || got.ErrorCode != "source.filter-unreadable" {
		t.Fatalf("GET = %d %q, want 500 source.filter-unreadable", resp.StatusCode, got.ErrorCode)
	}

	if !strings.Contains(got.Message, "send a PUT with a valid filter") || strings.Contains(got.Message, "acme") {
		t.Fatalf("message = %q, want the one action and no stored value", got.Message)
	}
}

// TestSourceFilterRoutesAreListed proves that the route table lists both
// routes with their one action, and that a handler with no store of the
// source filter serves none of them.
func TestSourceFilterRoutesAreListed(t *testing.T) {
	t.Parallel()

	want := sourceFilterActions()
	got := map[string]identity.Action{}

	for _, route := range newEnv(t).handler.Routes() {
		if _, ok := want[route.Method+" "+route.Path]; ok && len(route.Actions) == 1 {
			got[route.Method+" "+route.Path] = route.Actions[0]
		}
	}

	if len(got) != len(want) {
		t.Fatalf("route table lists %v, want %v", got, want)
	}

	for key, action := range want {
		if got[key] != action {
			t.Errorf("route %s declares %s, want %s", key, got[key], action)
		}
	}
}

// TestSourceFilterRoutesNeedAStore proves that a handler with no store of the
// source filter serves neither route.
func TestSourceFilterRoutesNeedAStore(t *testing.T) {
	t.Parallel()

	h := handlerWith(t, identity.ActionNamespaceRead, identity.ActionNamespaceReadRunners)

	lister, ok := h.(interface{ RegisteredPatterns() []string })
	if !ok {
		t.Fatal("the handler does not list its patterns")
	}

	for _, pattern := range lister.RegisteredPatterns() {
		if strings.Contains(pattern, "source-filter") {
			t.Errorf("a handler with no store of the filter serves %s", pattern)
		}
	}
}
