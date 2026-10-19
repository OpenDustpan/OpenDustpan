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

func references(units unitsBody) []string {
	out := make([]string, 0, len(units.Units))
	for _, u := range units.Units {
		out = append(out, u.Reference)
	}

	return out
}

// TestUnitListFilters checks the label and reference query values, on the path
// under /api/dustpan/v1 and on the older path.
func TestUnitListFilters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "no filter", query: "", want: []string{
			"opentofu:acme/dns", "opentofu:acme/network", "pulumi:acme/web/dev", "pulumi:acme/web/prod",
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
			want: []string{"opentofu:acme/dns", "pulumi:acme/web/dev"},
		},
		{name: "reference of an unknown unit", query: "reference=pulumi:acme/web/none", want: []string{}},
		{
			name:  "reference and label",
			query: "reference=pulumi:acme/web/dev&reference=pulumi:acme/web/prod&label=tier%3Dprod",
			want:  []string{"pulumi:acme/web/prod"},
		},
	}

	paths := []string{"/api/dustpan/v1/namespaces/acme/units?", "/api/inventory/units?organization=acme&"}

	for _, path := range paths {
		for _, tc := range cases {
			t.Run(path+tc.name, func(t *testing.T) {
				t.Parallel()

				handler := newFilterHandler(t, selectiveAuthorizer{deny: nil}, "")

				rec := authorizedGet(t, handler, path+tc.query, "test-token")
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
				}

				if got := references(decodeUnits(t, rec)); !slices.Equal(got, tc.want) {
					t.Fatalf("references = %v, want %v", got, tc.want)
				}
			})
		}
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

// TestUnitListPathsAnswerAlike checks that the path under /api/dustpan/v1 and
// the older path answer the same document, and that an unknown namespace and
// a bad namespace answer as the older path does.
func TestUnitListPathsAnswerAlike(t *testing.T) {
	t.Parallel()

	handler := newFilterHandler(t, selectiveAuthorizer{deny: nil}, "https://dustpan.example.com")

	next := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	older := authorizedGet(t, handler, "/api/inventory/units?organization=acme", "test-token")

	if next.Code != http.StatusOK || older.Code != http.StatusOK || next.Body.String() != older.Body.String() {
		t.Fatalf("answers differ:\n%d %s\n%d %s", next.Code, next.Body.String(), older.Code, older.Body.String())
	}

	for _, tc := range []struct {
		target string
		want   int
	}{
		{"/api/dustpan/v1/namespaces/nowhere/units", http.StatusNotFound},
		{"/api/inventory/units?organization=nowhere", http.StatusNotFound},
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
