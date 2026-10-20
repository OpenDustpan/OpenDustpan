package inventory_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// viewDoc decodes a saved view.
type viewDoc struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	Query     struct {
		Labels     []struct{ Key, Value string } `json:"labels"`
		References []string                      `json:"references"`
	} `json:"query"`
}

const payments = `{"name":"Payments","query":{"labels":[{"key":"team","value":"payments"}],"references":[]}}`

func TestViewLifecycle(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)

	created := f.do(t, http.MethodPost, viewsPath, tokenAna, payments)
	if created.status != http.StatusCreated {
		t.Fatalf("create = %d (%s)", created.status, created.body)
	}

	var doc viewDoc
	if err := json.Unmarshal([]byte(created.body), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if err := savedview.ID(doc.ID).Validate(); err != nil || doc.Namespace != "acme" || doc.Name != "Payments" ||
		doc.CreatedAt == "" || len(doc.Query.Labels) != 1 || doc.Query.References == nil {
		t.Fatalf("created view = %+v (%v)", doc, err)
	}

	if want := viewsPath + "/" + doc.ID; created.header.Get("Location") != want {
		t.Fatalf("Location = %q, want %q", created.header.Get("Location"), want)
	}

	// The answer never holds the owner: the principal ID stays on the server.
	if strings.Contains(created.body, "prn-") {
		t.Fatalf("the answer holds the owner: %s", created.body)
	}

	if got := f.do(t, http.MethodGet, viewsPath+"/"+doc.ID, tokenAna, ""); got.status != http.StatusOK || got.body != created.body {
		t.Fatalf("get = %d %s, want the created view %s", got.status, got.body, created.body)
	}

	list := f.do(t, http.MethodGet, viewsPath, tokenAna, "")
	if list.status != http.StatusOK || !strings.Contains(list.body, doc.ID) {
		t.Fatalf("list = %d %s", list.status, list.body)
	}

	if dup := f.do(t, http.MethodPost, viewsPath, tokenAna, payments); dup.status != http.StatusConflict {
		t.Fatalf("second create with the same name = %d, want 409", dup.status)
	}

	if del := f.do(t, http.MethodDelete, viewsPath+"/"+doc.ID, tokenAna, ""); del.status != http.StatusNoContent || del.body != "" {
		t.Fatalf("delete = %d %q, want 204 and no body", del.status, del.body)
	}

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if gone := f.do(t, method, viewsPath+"/"+doc.ID, tokenAna, ""); gone.status != http.StatusNotFound {
			t.Fatalf("%s after delete = %d, want 404", method, gone.status)
		}
	}

	empty := f.do(t, http.MethodGet, viewsPath, tokenAna, "")
	if empty.status != http.StatusOK || strings.TrimSpace(empty.body) != `{"views":[]}` {
		t.Fatalf("list after delete = %d %s", empty.status, empty.body)
	}
}

// TestViewCreateRefusesInvalidBodies sends bodies that fail at the boundary.
// Each one answers 400 and stores nothing.
func TestViewCreateRefusesInvalidBodies(t *testing.T) {
	t.Parallel()

	manyLabels := make([]string, 11)
	for i := range manyLabels {
		manyLabels[i] = fmt.Sprintf(`{"key":"k%d","value":"v"}`, i)
	}

	manyRefs := make([]string, 21)
	for i := range manyRefs {
		manyRefs[i] = fmt.Sprintf(`"pulumi:acme/web/p%d"`, i)
	}

	tests := []struct {
		name string
		body string
	}{
		{"not JSON", `name=Payments`},
		{"empty body", ``},
		{"array", `[]`},
		{"no name", `{"query":{}}`},
		{"null name", `{"name":null}`},
		{"empty name", `{"name":""}`},
		{"name of 129 characters", `{"name":"` + strings.Repeat("a", 129) + `"}`},
		{"space at the start", `{"name":" Payments"}`},
		{"space at the end", `{"name":"Payments "}`},
		{"zero width space", "{\"name\":\"Pay​ments\"}"},
		{"line separator", "{\"name\":\"Pay ments\"}"},
		{"newline escape", `{"name":"Pay\nments"}`},
		{"NUL escape", `{"name":"Pay\u0000ments"}`},
		{"unknown field", `{"name":"x","owner":"prn-evil"}`},
		{"unknown field in query", `{"name":"x","query":{"sort":"name"}}`},
		{"unknown field in label", `{"name":"x","query":{"labels":[{"key":"a","value":"b","x":1}]}}`},
		{"text after the object", `{"name":"x"} {"name":"y"}`},
		{"eleven labels", `{"name":"x","query":{"labels":[` + strings.Join(manyLabels, ",") + `]}}`},
		{"twenty-one references", `{"name":"x","query":{"references":[` + strings.Join(manyRefs, ",") + `]}}`},
		{"label key with a space", `{"name":"x","query":{"labels":[{"key":"a b","value":"c"}]}}`},
		{"empty label key", `{"name":"x","query":{"labels":[{"key":"","value":"c"}]}}`},
		{"label key with equals", `{"name":"x","query":{"labels":[{"key":"a=b","value":"c"}]}}`},
		{"label value with a control character", `{"name":"x","query":{"labels":[{"key":"a","value":"b\u0007"}]}}`},
		{"label value over 256 characters", `{"name":"x","query":{"labels":[{"key":"a","value":"` + strings.Repeat("v", 257) + `"}]}}`},
		{"reference of another namespace", `{"name":"x","query":{"references":["pulumi:other/web/prod"]}}`},
		{"reference that is a namespace", `{"name":"x","query":{"references":["acme"]}}`},
		{"reference with a control character", `{"name":"x","query":{"references":["pulumi:acme/web\n/prod"]}}`},
		{"reference that is text", `{"name":"x","query":{"references":["not a reference"]}}`},
		{"body over 16 KiB", `{"name":"x","padding":"` + strings.Repeat("a", savedview.MaxBodyBytes) + `"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newViewFixture(t)

			res := f.do(t, http.MethodPost, viewsPath, tokenAna, tc.body)
			if res.status != http.StatusBadRequest {
				t.Fatalf("create = %d (%s), want 400", res.status, res.body)
			}

			if list := f.do(t, http.MethodGet, viewsPath, tokenAna, ""); strings.TrimSpace(list.body) != `{"views":[]}` {
				t.Fatalf("a refused create stored a view: %s", list.body)
			}
		})
	}
}

// TestViewCreateAcceptsBoundaryValues sends the largest valid values.
func TestViewCreateAcceptsBoundaryValues(t *testing.T) {
	t.Parallel()

	labels := make([]string, savedview.MaxLabelFilters)
	for i := range labels {
		labels[i] = fmt.Sprintf(`{"key":"k%d","value":""}`, i)
	}

	refs := make([]string, savedview.MaxReferenceFilters)
	for i := range refs {
		refs[i] = fmt.Sprintf(`"pulumi:acme/web/p%d"`, i)
	}

	tests := []struct{ name, body string }{
		{"name of 128 characters", `{"name":"` + strings.Repeat("é", savedview.MaxNameLen) + `"}`},
		{"no query", `{"name":"x"}`},
		{"empty query", `{"name":"x","query":{}}`},
		{"ten labels and twenty references", `{"name":"x","query":{"labels":[` + strings.Join(labels, ",") +
			`],"references":[` + strings.Join(refs, ",") + `]}}`},
		{"label value of 256 characters", `{"name":"x","query":{"labels":[{"key":"a","value":"` + strings.Repeat("v", 256) + `"}]}}`},
		{"label key of 128 characters", `{"name":"x","query":{"labels":[{"key":"` + strings.Repeat("k", 128) + `","value":"v"}]}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newViewFixture(t)
			f.createView(t, tokenAna, tc.body)
		})
	}
}

// TestViewLimitPerOwner proves the largest number of views of one person, and
// that another person has its own.
func TestViewLimitPerOwner(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)

	for i := range savedview.MaxPerOwner {
		f.createView(t, tokenAna, fmt.Sprintf(`{"name":"view %03d"}`, i))
	}

	over := f.do(t, http.MethodPost, viewsPath, tokenAna, `{"name":"one more"}`)
	if over.status != http.StatusConflict || !strings.Contains(over.body, "limit") {
		t.Fatalf("create over the limit = %d (%s), want 409 and the limit", over.status, over.body)
	}

	f.createView(t, tokenBob, `{"name":"one more"}`)
}

// TestViewsAreRefusedForEveryCallerThatIsNotAPerson sends each route as each
// kind of caller that must not own a view. Every answer is the same 403 with
// the same message, and the refusal is audited.
func TestViewsAreRefusedForEveryCallerThatIsNotAPerson(t *testing.T) {
	t.Parallel()

	callers := map[string]string{
		"bootstrap admin token":                  tokenAdmin,
		"service":                                tokenService,
		"workload":                               tokenWorkload,
		"job principal":                          tokenJob,
		"unknown kind":                           tokenUnknown,
		"person with no record":                  tokenNoRecord,
		"person whose record is disabled":        tokenDisabled,
		"person whose record is of another kind": tokenOther,
	}

	const wantMessage = "only a person has saved views: sign in as a person"

	for name, token := range callers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newViewFixture(t)
			id := f.createView(t, tokenAna, payments)

			for _, call := range []struct{ method, target, body string }{
				{http.MethodPost, viewsPath, payments},
				{http.MethodGet, viewsPath, ""},
				{http.MethodGet, viewsPath + "/" + id, ""},
				{http.MethodGet, viewsPath + "/" + id + "/units", ""},
				{http.MethodDelete, viewsPath + "/" + id, ""},
			} {
				res := f.do(t, call.method, call.target, token, call.body)
				if res.status != http.StatusForbidden || !strings.Contains(res.body, wantMessage) {
					t.Fatalf("%s %s = %d (%s), want 403 and %q", call.method, call.target, res.status, res.body, wantMessage)
				}
			}

			// Nothing changed: the view of the person is still there.
			if got := f.do(t, http.MethodGet, viewsPath+"/"+id, tokenAna, ""); got.status != http.StatusOK {
				t.Fatalf("view after the refusals = %d", got.status)
			}

			var denied int

			for _, e := range f.auditEntries(t) {
				if e.Outcome == audit.OutcomeDenied {
					denied++
				}
			}

			if denied == 0 {
				t.Fatalf("the refusals left no denial entry in the audit log")
			}
		})
	}
}

// TestViewOfAnotherPersonIsNotFound proves that a person cannot read, open or
// delete the view of another person, and that the answer is the one of a view
// that does not exist.
func TestViewOfAnotherPersonIsNotFound(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	id := f.createView(t, tokenAna, payments)
	unknown := savedview.NewID()

	for _, call := range []struct{ method, suffix string }{
		{http.MethodGet, ""}, {http.MethodGet, "/units"}, {http.MethodDelete, ""},
	} {
		other := f.do(t, call.method, viewsPath+"/"+id+call.suffix, tokenBob, "")
		missing := f.do(t, call.method, viewsPath+"/"+string(unknown)+call.suffix, tokenBob, "")

		if other.status != http.StatusNotFound || other.body != missing.body {
			t.Fatalf("%s%s as another person = %d %s; an unknown view = %d %s; want the same 404",
				call.method, call.suffix, other.status, other.body, missing.status, missing.body)
		}
	}

	if list := f.do(t, http.MethodGet, viewsPath, tokenBob, ""); strings.TrimSpace(list.body) != `{"views":[]}` {
		t.Fatalf("list as another person = %s", list.body)
	}

	if got := f.do(t, http.MethodGet, viewsPath+"/"+id, tokenAna, ""); got.status != http.StatusOK {
		t.Fatalf("the view after the attempts = %d", got.status)
	}
}

// TestViewOfANewPrincipalWithTheSameSubject proves that the owner is the ID of
// the principal and not the subject: when the record of ana is replaced by a
// record with another ID, the new principal sees none of the old views.
func TestViewOfANewPrincipalWithTheSameSubject(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	id := f.createView(t, tokenAna, payments)

	f.people.replace("ana", "prn-anaanaanaanaanaana9")

	for _, call := range []struct{ method, suffix string }{
		{http.MethodGet, ""}, {http.MethodGet, "/units"}, {http.MethodDelete, ""},
	} {
		if res := f.do(t, call.method, viewsPath+"/"+id+call.suffix, tokenAna, ""); res.status != http.StatusNotFound {
			t.Fatalf("%s%s as the new principal = %d, want 404", call.method, call.suffix, res.status)
		}
	}

	if list := f.do(t, http.MethodGet, viewsPath, tokenAna, ""); strings.TrimSpace(list.body) != `{"views":[]}` {
		t.Fatalf("list as the new principal = %s", list.body)
	}

	// The new principal can use the same name: the old view does not block it.
	f.createView(t, tokenAna, payments)
}

// TestViewHandlerChecksTheOwner proves that the handler checks the owner
// itself. The store here ignores the owner of a read and returns the views of
// another person, as a bug in a store would. The handler must not serve them.
func TestViewHandlerChecksTheOwner(t *testing.T) {
	t.Parallel()

	f := newViewFixtureWith(t, viewFixtureOptions{
		views: func(inner savedview.Store) savedview.Store { return &ownerBlindStore{Store: inner, blind: false} },
	})

	// ana makes a view with the honest store. Then the store turns blind.
	id := f.createView(t, tokenAna, payments)

	blind, ok := findBlind(f)
	if !ok {
		t.Fatal("the fixture holds no blind store")
	}

	blind.blind = true

	for _, call := range []struct{ method, suffix string }{
		{http.MethodGet, ""}, {http.MethodGet, "/units"}, {http.MethodDelete, ""},
	} {
		res := f.do(t, call.method, viewsPath+"/"+id+call.suffix, tokenBob, "")
		if res.status != http.StatusNotFound {
			t.Fatalf("%s%s as bob with a blind store = %d (%s), want 404", call.method, call.suffix, res.status, res.body)
		}
	}

	list := f.do(t, http.MethodGet, viewsPath, tokenBob, "")
	if list.status != http.StatusOK || strings.TrimSpace(list.body) != `{"views":[]}` {
		t.Fatalf("list as bob with a blind store = %d %s, want no view", list.status, list.body)
	}

	// The blind store also hides nothing from the owner.
	if own := f.do(t, http.MethodGet, viewsPath+"/"+id, tokenAna, ""); own.status != http.StatusOK {
		t.Fatalf("get as the owner = %d", own.status)
	}

	// The view of ana is still there.
	blind.blind = false

	if own := f.do(t, http.MethodGet, viewsPath+"/"+id, tokenAna, ""); own.status != http.StatusOK {
		t.Fatalf("view after the attempts = %d", own.status)
	}
}

// TestViewPathValuesAreAllowlisted sends hostile view IDs and namespaces.
func TestViewPathValuesAreAllowlisted(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	f.createView(t, tokenAna, payments)

	ids := []string{
		"..%2Facme", "view-", "view-" + strings.Repeat("0", 31), "view-" + strings.Repeat("0", 33),
		"view-" + strings.Repeat("A", 32), "VIEW-" + strings.Repeat("0", 32), "view-" + strings.Repeat("g", 32),
		"%2e%2e", "view-" + strings.Repeat("0", 31) + "%00", "view-" + strings.Repeat("0", 31) + "%0a",
		strings.Repeat("a", 4096), "%E2%80%AE", "view-%25s", "'%20OR%20'1'='1",
	}

	for _, id := range ids {
		for _, suffix := range []string{"", "/units"} {
			res := f.do(t, http.MethodGet, viewsPath+"/"+id+suffix, tokenAna, "")
			if res.status != http.StatusNotFound && res.status != http.StatusBadRequest {
				t.Errorf("GET view %q%s = %d, want 404 or 400", id, suffix, res.status)
			}
		}

		if res := f.do(t, http.MethodDelete, viewsPath+"/"+id, tokenAna, ""); res.status != http.StatusNotFound &&
			res.status != http.StatusBadRequest {
			t.Errorf("DELETE view %q = %d, want 404 or 400", id, res.status)
		}
	}

	for _, ns := range []string{"Acme", "ac me", "-", "%00", "unknown", strings.Repeat("a", 300)} {
		target := "/api/dustpan/v1/namespaces/" + url.PathEscape(ns) + "/views"

		res := f.do(t, http.MethodGet, target, tokenAna, "")
		if res.status != http.StatusNotFound && res.status != http.StatusBadRequest {
			t.Errorf("GET views of namespace %q = %d, want 404 or 400", ns, res.status)
		}
	}

	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		if res := f.do(t, method, viewsPath, tokenAna, payments); res.status != http.StatusMethodNotAllowed {
			t.Errorf("%s views = %d, want 405", method, res.status)
		}
	}
}

// TestViewActions proves every route declares the actions of the family on the
// namespace, and that a denied action answers 403 before the handler runs.
func TestViewActions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		method  string
		suffix  string
		body    string
		actions []identity.Action
	}{
		{"create", http.MethodPost, "", payments, []identity.Action{identity.ActionViewCreate}},
		{"list", http.MethodGet, "", "", []identity.Action{identity.ActionViewRead}},
		{"get", http.MethodGet, "/ID", "", []identity.Action{identity.ActionViewRead}},
		{"delete", http.MethodDelete, "/ID", "", []identity.Action{identity.ActionViewDelete}},
		{"units", http.MethodGet, "/ID/units", "", []identity.Action{identity.ActionViewRead, identity.ActionNamespaceRead}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newViewFixture(t)
			id := f.createView(t, tokenBob, `{"name":"seed"}`)
			target := viewsPath + strings.ReplaceAll(tc.suffix, "ID", id)

			// Allowed: the actions are asked on the namespace.
			f.authz.asked = nil
			f.do(t, tc.method, target, tokenBob, tc.body)

			for _, action := range tc.actions {
				if !containsCall(f.authz.calls(), recordedCall{action: action, object: "acme"}) {
					t.Fatalf("%s never asks %s on acme (calls %v)", tc.name, action, f.authz.calls())
				}
			}

			for _, action := range tc.actions {
				f.authz.denyAction = map[identity.Action]bool{action: true}

				if res := f.do(t, tc.method, target, tokenBob, tc.body); res.status != http.StatusForbidden {
					t.Fatalf("%s with %s denied = %d (%s), want 403", tc.name, action, res.status, res.body)
				}
			}
		})
	}
}

// TestViewAuditEntries proves one audit entry for each write, with the view ID
// and with no name.
func TestViewAuditEntries(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	id := f.createView(t, tokenAna, `{"name":"Reorg of the payments team"}`)

	if res := f.do(t, http.MethodDelete, viewsPath+"/"+id, tokenAna, ""); res.status != http.StatusNoContent {
		t.Fatalf("delete = %d", res.status)
	}

	// Reads and refused writes add nothing.
	f.do(t, http.MethodGet, viewsPath, tokenAna, "")
	f.do(t, http.MethodPost, viewsPath, tokenAna, `{"name":""}`)

	var views []audit.Entry

	for _, e := range f.auditEntries(t) {
		if strings.HasPrefix(e.Action, "view:") {
			views = append(views, e)
		}
	}

	if len(views) != 2 || views[0].Action != "view:create" || views[1].Action != "view:delete" {
		t.Fatalf("view audit entries = %+v, want a create and a delete", views)
	}

	for _, e := range views {
		if e.ObjectID != id || e.Subject != "ana" || e.Object != "acme" || e.Outcome != audit.OutcomeSuccess ||
			e.PrincipalKind != audit.PrincipalKindPerson || strings.Contains(fmt.Sprintf("%+v", e), "Reorg") {
			t.Fatalf("audit entry = %+v: want the view ID and no name", e)
		}
	}
}

// TestViewQueryParameter proves that a view answers the units of its query
// only. A request to the units route that carries a filter answers 400.
func TestViewUnitsRouteTakesPagingOnly(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	id := f.createView(t, tokenAna, payments)

	for _, q := range []string{
		"label=team%3Dplatform", "reference=pulumi%3Aacme%2Fweb%2Fprod", "at=1700000000000", "sort=name", "organization=acme",
		"pageSize=1&label=x%3Dy", "pageToken=x&other=1",
	} {
		res := f.do(t, http.MethodGet, viewsPath+"/"+id+"/units?"+q, tokenAna, "")
		if res.status != http.StatusBadRequest {
			t.Errorf("units with %q = %d (%s), want 400", q, res.status, res.body)
		}
	}

	for _, q := range []string{"pageSize=0", "pageSize=501", "pageSize=x", "pageToken=%%%", "pageToken=e30"} {
		res := f.do(t, http.MethodGet, viewsPath+"/"+id+"/units?"+q, tokenAna, "")
		if res.status != http.StatusBadRequest {
			t.Errorf("units with %q = %d (%s), want 400", q, res.status, res.body)
		}
	}
}

// TestViewUnitsAreTheUnitsOfTheSameQuery runs one query through the unit list
// and through the units of a view, and compares the pages, page by page.
func TestViewUnitsAreTheUnitsOfTheSameQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		query  string // the stored query, as JSON
		params string // the same query as query values of the unit list
	}{
		{"no filter", `{}`, ""},
		{"one label", `{"labels":[{"key":"team","value":"payments"}]}`, "label=team%3Dpayments"},
		{"two labels", `{"labels":[{"key":"team","value":"payments"},{"key":"tier","value":"prod"}]}`,
			"label=team%3Dpayments&label=tier%3Dprod"},
		{"a label twice", `{"labels":[{"key":"app","value":"web"},{"key":"app","value":"web"}]}`,
			"label=app%3Dweb&label=app%3Dweb"},
		{"labels that no unit has", `{"labels":[{"key":"team","value":"nobody"}]}`, "label=team%3Dnobody"},
		{"one reference", `{"references":["pulumi:acme/web/prod"]}`, "reference=pulumi%3Aacme%2Fweb%2Fprod"},
		{"a reference twice", `{"references":["pulumi:acme/web/prod","pulumi:acme/web/prod"]}`,
			"reference=pulumi%3Aacme%2Fweb%2Fprod&reference=pulumi%3Aacme%2Fweb%2Fprod"},
		{"two references", `{"references":["pulumi:acme/web/prod","opentofu:acme/network"]}`,
			"reference=pulumi%3Aacme%2Fweb%2Fprod&reference=opentofu%3Aacme%2Fnetwork"},
		{"a reference of a unit that does not exist", `{"references":["pulumi:acme/web/gone"]}`,
			"reference=pulumi%3Aacme%2Fweb%2Fgone"},
		{"label and reference", `{"labels":[{"key":"team","value":"payments"}],"references":["pulumi:acme/web/dev"]}`,
			"label=team%3Dpayments&reference=pulumi%3Aacme%2Fweb%2Fdev"},
	}

	for _, tc := range tests {
		for _, size := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("%s with page size %d", tc.name, size), func(t *testing.T) {
				t.Parallel()

				f := newViewFixture(t)
				id := f.createView(t, tokenAna, `{"name":"q","query":`+tc.query+`}`)

				compareUnitPages(t, f, tokenAna, viewsPath+"/"+id+"/units", unitsPath, tc.params, size)
			})
		}
	}
}

// compareUnitPages reads the pages of both routes and compares them: the same
// units, the same next page token and the same JSON.
func compareUnitPages(t *testing.T, f *viewFixture, token, viewTarget, listTarget, params string, size int) {
	t.Helper()

	var viewToken, listToken string

	for page := range 10 {
		viewQ, listQ := url.Values{}, url.Values{}

		if size > 0 {
			viewQ.Set("pageSize", fmt.Sprint(size))
			listQ.Set("pageSize", fmt.Sprint(size))
		}

		if viewToken != "" {
			viewQ.Set("pageToken", viewToken)
		}

		if listToken != "" {
			listQ.Set("pageToken", listToken)
		}

		listURL := listTarget + "?" + listQ.Encode()
		if params != "" {
			listURL += "&" + params
		}

		opened := f.do(t, http.MethodGet, viewTarget+"?"+viewQ.Encode(), token, "")
		listed := f.do(t, http.MethodGet, listURL, token, "")

		if opened.status != http.StatusOK || listed.status != http.StatusOK || opened.body != listed.body {
			t.Fatalf("page %d: view units = %d %s; unit list = %d %s", page, opened.status, opened.body, listed.status, listed.body)
		}

		var next struct {
			Next string `json:"nextPageToken"`
		}

		if err := json.Unmarshal([]byte(opened.body), &next); err != nil {
			t.Fatalf("decode page %d: %v", page, err)
		}

		if next.Next == "" {
			return
		}

		viewToken, listToken = next.Next, next.Next
	}

	t.Fatal("the pages never ended")
}

// TestViewUnitsCheckUnitReadForEachUnit proves the units of a view are the units
// the caller may read: a unit that the caller may not read is not in the page,
// as in the unit list, and it does not change the next page token.
func TestViewUnitsCheckUnitReadForEachUnit(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	id := f.createView(t, tokenAna, `{"name":"q","query":{"labels":[{"key":"team","value":"payments"}]}}`)

	all := f.do(t, http.MethodGet, viewsPath+"/"+id+"/units", tokenAna, "")
	if !strings.Contains(all.body, "web/prod") || !strings.Contains(all.body, "web/dev") {
		t.Fatalf("units = %s, want both stacks", all.body)
	}

	f.authz.denyUnits = map[string]bool{"pulumi:acme/web/prod": true}

	limited := f.do(t, http.MethodGet, viewsPath+"/"+id+"/units", tokenAna, "")
	if limited.status != http.StatusOK || strings.Contains(limited.body, "web/prod") || !strings.Contains(limited.body, "web/dev") {
		t.Fatalf("units with unit:read denied on web/prod = %d %s", limited.status, limited.body)
	}

	// The same denial in the unit list gives the same page.
	listed := f.do(t, http.MethodGet, unitsPath+"?label=team%3Dpayments", tokenAna, "")
	if listed.body != limited.body {
		t.Fatalf("unit list = %s, view units = %s: want the same page", listed.body, limited.body)
	}

	// A reference does not grant access: a view of a unit the caller may not
	// read opens as an empty page.
	only := f.createView(t, tokenAna, `{"name":"r","query":{"references":["pulumi:acme/web/prod"]}}`)

	res := f.do(t, http.MethodGet, viewsPath+"/"+only+"/units", tokenAna, "")
	if res.status != http.StatusOK || strings.Contains(res.body, "web/prod") {
		t.Fatalf("units of a view of a unit the caller may not read = %d %s", res.status, res.body)
	}

	// The check is asked for each unit, on the unit.
	asked := false

	for _, c := range f.authz.calls() {
		if c.action == identity.ActionUnitRead && c.object == "pulumi:acme/web/prod" {
			asked = true
		}
	}

	if !asked {
		t.Fatalf("unit:read was never asked on the unit (calls %v)", f.authz.calls())
	}
}
