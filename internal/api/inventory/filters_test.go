package inventory_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// labeledEntries returns four units in acme with labels: two Pulumi stacks
// and two OpenTofu workspaces, one of which has no label.
func labeledEntries() []coreinventory.Entry {
	entry := func(tool, name, id string, labels map[string]string) coreinventory.Entry {
		return coreinventory.Entry{
			LastChanged: changeTime(), Namespace: "acme", Tool: tool, Name: name, UnitID: id,
			DisplayName: name, Status: "", ResourceCount: nil, Lock: nil, Collection: "", Labels: labels,
		}
	}

	return []coreinventory.Entry{
		entry("opentofu", "network", "wsp-1", map[string]string{"team": "platform"}),
		entry("opentofu", "dns", "wsp-2", nil),
		entry("pulumi", "web/dev", "stk-1", map[string]string{"app": "web", "team": "payments"}),
		entry("pulumi", "web/prod", "stk-2", map[string]string{"app": "web", "team": "payments", "tier": "prod"}),
	}
}

// newFilterHandler returns a handler over labeledEntries with the public URL.
func newFilterHandler(t *testing.T, authz identity.Authorizer, publicURL string) *apiinventory.Handler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	reader := &memReader{entries: map[namespace.Name][]coreinventory.Entry{"acme": labeledEntries()}, err: nil}

	return newBehaviorHandlerWith(t, nsStore, reader, authz, behaviorWiring{
		sessions: nil, lifecycles: testLifecycles(), publicURL: publicURL,
	})
}

// newFilterHandlerFor is newFilterHandler with no public URL.
func newFilterHandlerFor(t *testing.T, authz identity.Authorizer) *apiinventory.Handler {
	t.Helper()

	return newFilterHandler(t, authz, "")
}

// newOutsideGrammarHandler returns a handler over one unit in acme with the
// labels labels, whatever their form.
func newOutsideGrammarHandler(t *testing.T, labels map[string]string) *apiinventory.Handler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	entry := coreinventory.Entry{
		LastChanged: changeTime(), Namespace: "acme", Tool: "pulumi", Name: "web/prod", UnitID: "stk-1",
		DisplayName: "web/prod", Status: "", ResourceCount: nil, Lock: nil, Collection: "", Labels: labels,
	}
	reader := &memReader{entries: map[namespace.Name][]coreinventory.Entry{"acme": {entry}}, err: nil}

	return newBehaviorHandlerWith(t, nsStore, reader, selectiveAuthorizer{deny: nil}, behaviorWiring{
		sessions: nil, lifecycles: testLifecycles(), publicURL: "",
	})
}

// getPageFrom fetches the unit list of the namespace ns with the query q and
// decodes it. A body that is not a page decodes to the zero page.
func getPageFrom(t *testing.T, handler *apiinventory.Handler, ns, q string) (int, pageBody) {
	t.Helper()

	target := "/api/dustpan/v1/namespaces/" + ns + "/units"
	if q != "" {
		target += "?" + q
	}

	rec := authorizedGet(t, handler, target, "test-token")

	var page pageBody

	if rec.Code != http.StatusOK {
		return rec.Code, page
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v (%s)", err, rec.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode page keys: %v", err)
	}

	_, page.hasNext = raw["nextPageToken"]

	return rec.Code, page
}

func references(units unitsBody) []string {
	out := make([]string, 0, len(units.Units))
	for i := range units.Units {
		out = append(out, units.Units[i].Reference)
	}

	return out
}

// TestUnitListFilters checks the label and reference query values.
func TestUnitListFilters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "no filter", query: "", want: []string{
			"pulumi:acme/web/dev", "pulumi:acme/web/prod", "opentofu:acme/network", "opentofu:acme/dns",
		}},
		{name: "one label", query: "label=app%3Dweb", want: []string{"pulumi:acme/web/dev", "pulumi:acme/web/prod"}},
		{name: "two labels", query: "label=app%3Dweb&label=tier%3Dprod", want: []string{"pulumi:acme/web/prod"}},
		{name: "label matches no unit", query: "label=team%3Dnobody", want: []string{}},
		{name: "same key, two values", query: "label=team%3Dplatform&label=team%3Dpayments", want: []string{}},
		{name: "empty value", query: "label=team%3D", want: []string{}},
		{name: "value with equals sign", query: "label=team%3Da%3Db", want: []string{}},
		{name: "one reference", query: "reference=pulumi:acme/web/dev", want: []string{"pulumi:acme/web/dev"}},
		{
			name: "two references", query: "reference=pulumi:acme/web/dev&reference=opentofu:acme/dns",
			want: []string{"pulumi:acme/web/dev", "opentofu:acme/dns"},
		},
		{name: "reference of an unknown unit", query: "reference=pulumi:acme/web/none", want: []string{}},
		{
			name:  "reference and label",
			query: "reference=pulumi:acme/web/dev&reference=pulumi:acme/web/prod&label=tier%3Dprod",
			want:  []string{"pulumi:acme/web/prod"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newFilterHandler(t, selectiveAuthorizer{deny: nil}, "")

			rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units?"+tc.query, "test-token")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}

			if got := references(decodeUnits(t, rec)); !slices.Equal(got, tc.want) {
				t.Fatalf("references = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUnitListFilterErrors checks that a bad label or reference answers 400.
func TestUnitListFilterErrors(t *testing.T) {
	t.Parallel()

	tooManyLabels := make([]string, 0, 11)
	for range 11 {
		tooManyLabels = append(tooManyLabels, "label=a%3Db")
	}

	tooManyRefs := make([]string, 0, 21)
	for range 21 {
		tooManyRefs = append(tooManyRefs, "reference=pulumi:acme/web/dev")
	}

	cases := []struct {
		name  string
		query string
	}{
		{name: "label without equals sign", query: "label=app"},
		{name: "label with empty key", query: "label=%3Dweb"},
		{name: "label key with a space", query: "label=my%20key%3Dweb"},
		{name: "label key too long", query: "label=" + strings.Repeat("k", 129) + "%3Dv"},
		{name: "label value too long", query: "label=k%3D" + strings.Repeat("v", 257)},
		{name: "label value with a control character", query: "label=k%3Da%0Ab"},
		{name: "eleven labels", query: strings.Join(tooManyLabels, "&")},
		{name: "reference is not a unit", query: "reference=acme"},
		{name: "reference without a tool", query: "reference=acme/web/dev"},
		{name: "reference in another namespace", query: "reference=pulumi:other/web/dev"},
		{
			name:  "one reference in another namespace",
			query: "reference=pulumi:acme/web/dev&reference=pulumi:other/web/dev",
		},
		{name: "twenty-one references", query: strings.Join(tooManyRefs, "&")},
		{name: "reference with a bad escape", query: "reference=%zz"},
		{name: "label with a bad escape", query: "label=a%zz"},
		{name: "semicolon in the query", query: "label=a%3Db;label=c%3Dd"},
		{name: "label with the at value", query: "label=app%3Dweb&at=1"},
		{name: "reference with the at value", query: "reference=pulumi:acme/web/dev&at=1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newFilterHandler(t, selectiveAuthorizer{deny: nil}, "")

			rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units?"+tc.query, "test-token")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}

			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != http.StatusBadRequest ||
				body.Message == "" {
				t.Fatalf("error body = %s, want code 400 and a message", rec.Body.String())
			}
		})
	}
}

// TestUnitListFilterKeepsAuthorization checks that a filter never shows a
// unit that the caller may not read, and that a filter by reference does not
// tell a forbidden unit from a missing one.
func TestUnitListFilterKeepsAuthorization(t *testing.T) {
	t.Parallel()

	handler := newFilterHandler(t, denyUnits{deny: map[string]bool{"pulumi:acme/web/prod": true}}, "")

	rec := authorizedGet(t, handler,
		"/api/dustpan/v1/namespaces/acme/units?reference=pulumi:acme/web/prod&reference=pulumi:acme/web/dev",
		"test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if got := references(decodeUnits(t, rec)); !slices.Equal(got, []string{"pulumi:acme/web/dev"}) {
		t.Fatalf("references = %v, want only the unit that the caller may read", got)
	}
}

// TestUnitListLabelsAndConsoleURL checks the labels and the consoleUrl of an entry.
func TestUnitListLabelsAndConsoleURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		publicURL  string
		wantPrefix string
	}{
		{name: "no public URL", publicURL: "", wantPrefix: ""},
		{name: "public URL", publicURL: "https://dustpan.example.com", wantPrefix: "https://dustpan.example.com"},
		{
			name: "public URL with a slash at the end", publicURL: "https://dustpan.example.com/",
			wantPrefix: "https://dustpan.example.com",
		},
		{
			name: "public URL with a path", publicURL: "https://dustpan.example.com/console/",
			wantPrefix: "https://dustpan.example.com/console",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newFilterHandler(t, selectiveAuthorizer{deny: nil}, tc.publicURL)

			rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}

			var raw struct {
				Units []map[string]json.RawMessage `json:"units"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode: %v", err)
			}

			byID := map[string]map[string]json.RawMessage{}

			for _, u := range raw.Units {
				var id string
				if err := json.Unmarshal(u["unitID"], &id); err != nil {
					t.Fatalf("decode unit ID: %v", err)
				}

				byID[id] = u
			}

			checkConsoleURL(t, byID, tc.wantPrefix)
			checkLabels(t, byID)
		})
	}
}

func checkConsoleURL(t *testing.T, byID map[string]map[string]json.RawMessage, prefix string) {
	t.Helper()

	for id, u := range byID {
		raw, has := u["consoleUrl"]
		if prefix == "" {
			if has {
				t.Fatalf("unit %s has consoleUrl %s, want none", id, raw)
			}

			continue
		}

		var got string
		if err := json.Unmarshal(raw, &got); err != nil || got != prefix+"/units/"+url.PathEscape(id) {
			t.Fatalf("unit %s consoleUrl = %s, want %s/units/%s", id, raw, prefix, id)
		}
	}
}

func checkLabels(t *testing.T, byID map[string]map[string]json.RawMessage) {
	t.Helper()

	var labels map[string]string
	if err := json.Unmarshal(byID["stk-2"]["labels"], &labels); err != nil ||
		labels["app"] != "web" || labels["team"] != "payments" || labels["tier"] != "prod" || len(labels) != 3 {
		t.Fatalf("labels of stk-2 = %s, want app, team and tier", byID["stk-2"]["labels"])
	}

	if raw, has := byID["wsp-2"]["labels"]; has {
		t.Fatalf("labels of wsp-2 = %s, want none", raw)
	}
}

// TestUnitListNamespaceErrors checks that an unknown namespace answers 404 and
// a namespace name that is not valid answers 400.
func TestUnitListNamespaceErrors(t *testing.T) {
	t.Parallel()

	handler := newFilterHandler(t, selectiveAuthorizer{deny: nil}, "")

	for _, tc := range []struct {
		target string
		want   int
	}{
		{"/api/dustpan/v1/namespaces/nowhere/units", http.StatusNotFound},
		{"/api/dustpan/v1/namespaces/Bad%20Name/units", http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authorizedRequest(t, tc.target))

		if rec.Code != tc.want {
			t.Fatalf("%s: status = %d, want %d: %s", tc.target, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func authorizedRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.Header.Set("Authorization", "token test-token")

	return req
}
