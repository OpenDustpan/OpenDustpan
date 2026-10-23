package inventory_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// pageBody is one decoded page of the unit list, with the raw keys so a test
// can tell an omitted nextPageToken from an empty one.
type pageBody struct {
	Units         []unitBody `json:"units"`
	NextPageToken string     `json:"nextPageToken"`
	hasNext       bool
}

func unitIDsOf(units []unitBody) []string {
	out := make([]string, 0, len(units))
	for i := range units {
		out = append(out, units[i].UnitID)
	}

	return out
}

// TestUnitListPaging pages the unit list of four units, which the list orders
// by unit ID: stk-1, stk-2, wsp-1, wsp-2.
func TestUnitListPaging(t *testing.T) {
	t.Parallel()

	allowAll := selectiveAuthorizer{deny: nil}
	denyProd := denyUnits{deny: map[string]bool{"pulumi:acme/web/prod": true}}

	tests := []struct {
		name  string
		authz identity.Authorizer
		// pages lists the query of each request in turn. A request after the
		// first one adds the token of the page before it.
		pages []string
		want  [][]string
		// more is true when the last page that the test reads has a token.
		more bool
	}{
		{
			name: "no page size gives every unit", authz: allowAll,
			pages: []string{""}, want: [][]string{{"stk-1", "stk-2", "wsp-1", "wsp-2"}}, more: false,
		},
		{
			name: "first page", authz: allowAll,
			pages: []string{"pageSize=2"}, want: [][]string{{"stk-1", "stk-2"}}, more: true,
		},
		{
			name: "middle page", authz: allowAll,
			pages: []string{"pageSize=1", "pageSize=1"}, want: [][]string{{"stk-1"}, {"stk-2"}}, more: true,
		},
		{
			name: "last page has no token", authz: allowAll,
			pages: []string{"pageSize=3", "pageSize=3"},
			want:  [][]string{{"stk-1", "stk-2", "wsp-1"}, {"wsp-2"}}, more: false,
		},
		{
			name: "a page of the exact size has no token", authz: allowAll,
			pages: []string{"pageSize=4"}, want: [][]string{{"stk-1", "stk-2", "wsp-1", "wsp-2"}}, more: false,
		},
		{
			name: "the largest page size", authz: allowAll,
			pages: []string{"pageSize=500"}, want: [][]string{{"stk-1", "stk-2", "wsp-1", "wsp-2"}}, more: false,
		},
		{
			name: "a filter and paging", authz: allowAll,
			pages: []string{"label=team%3Dpayments&pageSize=1", "label=team%3Dpayments&pageSize=1"},
			want:  [][]string{{"stk-1"}, {"stk-2"}}, more: false,
		},
		{
			name: "a filter that matches a later unit", authz: allowAll,
			pages: []string{"label=team%3Dplatform&pageSize=1"}, want: [][]string{{"wsp-1"}}, more: false,
		},
		{
			name: "a denied unit is not counted in a page", authz: denyProd,
			pages: []string{"pageSize=2", "pageSize=2"}, want: [][]string{{"stk-1", "wsp-1"}, {"wsp-2"}}, more: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkPages(t, newFilterHandlerFor(t, tc.authz), tc.pages, tc.want, tc.more)
		})
	}
}

// checkPages reads one page for each query in pages, each with the token of the
// page before, and checks the units of each page. The last page has a token
// when more is true.
func checkPages(t *testing.T, handler testHandler, pages []string, want [][]string, more bool) {
	t.Helper()

	token := ""

	for i, query := range pages {
		if token != "" {
			query += "&pageToken=" + url.QueryEscape(token)
		}

		status, page := getPageFrom(t, handler, "acme", query)
		if status != http.StatusOK {
			t.Fatalf("page %d: status = %d, want 200", i+1, status)
		}

		if got := unitIDsOf(page.Units); !slices.Equal(got, want[i]) {
			t.Fatalf("page %d: units = %v, want %v", i+1, got, want[i])
		}

		wantNext := i < len(pages)-1 || more
		if page.hasNext != wantNext {
			t.Fatalf("page %d: nextPageToken present = %v, want %v", i+1, page.hasNext, wantNext)
		}

		token = page.NextPageToken
	}
}

// TestUnitListPagingEveryPage reads every page of a one-unit page size and
// checks that the pages together hold each unit once.
func TestUnitListPagingEveryPage(t *testing.T) {
	t.Parallel()

	handler := newFilterHandlerFor(t, selectiveAuthorizer{deny: nil})

	var seen []string

	token := ""

	for range 10 {
		query := "pageSize=1"
		if token != "" {
			query += "&pageToken=" + url.QueryEscape(token)
		}

		status, page := getPageFrom(t, handler, "acme", query)
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}

		seen = append(seen, unitIDsOf(page.Units)...)

		if !page.hasNext {
			break
		}

		token = page.NextPageToken
	}

	if want := []string{"stk-1", "stk-2", "wsp-1", "wsp-2"}; !slices.Equal(seen, want) {
		t.Fatalf("units of all pages = %v, want %v", seen, want)
	}
}

// TestUnitListPageErrors checks the page size limits and the page tokens that
// the list refuses with 400.
func TestUnitListPageErrors(t *testing.T) {
	t.Parallel()

	handler := newFilterHandlerFor(t, selectiveAuthorizer{deny: nil})

	_, first := getPageFrom(t, handler, "acme", "pageSize=1&label=team%3Dpayments")
	if !first.hasNext {
		t.Fatal("first page has no token")
	}

	token := url.QueryEscape(first.NextPageToken)

	tests := []struct {
		name      string
		namespace string
		query     string
	}{
		{name: "page size zero", namespace: "acme", query: "pageSize=0"},
		{name: "page size below zero", namespace: "acme", query: "pageSize=-1"},
		{name: "page size above the limit", namespace: "acme", query: "pageSize=501"},
		{name: "page size is not a number", namespace: "acme", query: "pageSize=many"},
		{name: "token that is not base64", namespace: "acme", query: "pageToken=!!"},
		{name: "token that is not JSON", namespace: "acme", query: "pageToken=bm90LWpzb24"},
		{name: "token of an empty object", namespace: "acme", query: "pageToken=e30"},
		{name: "token with other filters", namespace: "acme", query: "label=team%3Dplatform&pageToken=" + token},
		{name: "token with no filter", namespace: "acme", query: "pageToken=" + token},
		{name: "token in another namespace", namespace: "other", query: "label=team%3Dpayments&pageToken=" + token},
		{name: "token with the at value", namespace: "acme", query: "at=100&pageToken=" + token},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, _ := getPageFrom(t, handler, tc.namespace, tc.query)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
		})
	}

	t.Run("the token works with the query that made it", func(t *testing.T) {
		t.Parallel()

		status, page := getPageFrom(t, handler, "acme", "pageSize=1&label=team%3Dpayments&pageToken="+token)
		if status != http.StatusOK || !slices.Equal(unitIDsOf(page.Units), []string{"stk-2"}) {
			t.Fatalf("status = %d, units = %v, want 200 and stk-2", status, unitIDsOf(page.Units))
		}
	})
}

// TestUnitListTokenFilterOrder checks that the order of the query values does
// not change which query a token belongs to.
func TestUnitListTokenFilterOrder(t *testing.T) {
	t.Parallel()

	handler := newFilterHandlerFor(t, selectiveAuthorizer{deny: nil})

	_, first := getPageFrom(t, handler, "acme", "pageSize=1&label=app%3Dweb&label=team%3Dpayments")

	status, _ := getPageFrom(t, handler, "acme",
		"pageSize=1&label=team%3Dpayments&label=app%3Dweb&pageToken="+url.QueryEscape(first.NextPageToken))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}

// TestUnitListLabelOutsideGrammar checks that a unit with a label outside the
// grammar of the filter still lists with that label.
func TestUnitListLabelOutsideGrammar(t *testing.T) {
	t.Parallel()

	labels := map[string]string{
		"a key with spaces": "v",
		"long":              strings.Repeat("x", 300),
		"multi":             "one\ntwo",
	}

	handler := newOutsideGrammarHandler(t, labels)

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got labelsPage

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Units) != 1 || len(got.Units[0].Labels) != len(labels) {
		t.Fatalf("units = %+v, want one unit with the 3 labels", got.Units)
	}

	for k, v := range labels {
		if got.Units[0].Labels[k] != v {
			t.Fatalf("label %q = %q, want %q", k, got.Units[0].Labels[k], v)
		}
	}
}

// labelsPage is the labels of the units of a page.
type labelsPage struct {
	Units []labelsOf `json:"units"`
}

// labelsOf is the labels of one unit.
type labelsOf struct {
	Labels map[string]string `json:"labels"`
}
