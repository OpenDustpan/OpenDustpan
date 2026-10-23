package storetest

import (
	"fmt"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// workspacePageCases are the cases of QueryWorkspaces. RunWorkspace runs
// them.
func workspacePageCases() []workspaceCase {
	return []workspaceCase{
		{name: "a workspace page walks every page in the order of the list", test: testWorkspacePageBoundaries},
		{name: "a workspace page counts and pages names that differ in case", test: testWorkspacePageCase},
		{name: "a workspace page answers an empty page", test: testWorkspacePageEmpty},
		{name: "a workspace page skips a workspace deleted between pages", test: testWorkspacePageDeleted},
		{name: "a workspace page pages a search", test: testWorkspacePageSearch},
		{
			name: "a workspace page reads the offset and the limit after the floor and the cap",
			test: testWorkspacePageLimits,
		},
	}
}

// workspacePageNames returns the workspace names of page.
func workspacePageNames(page workspace.ListPage) []string {
	out := make([]string, len(page.Workspaces))
	for i := range page.Workspaces {
		out[i] = page.Workspaces[i].Ref.Name.String()
	}

	return out
}

// queryWorkspaces runs q and fails the test on an error.
func queryWorkspaces(t *testing.T, s WorkspaceStore, q workspace.ListQuery) workspace.ListPage {
	t.Helper()

	page, err := s.QueryWorkspaces(t.Context(), q)
	if err != nil {
		t.Fatalf("query workspaces %+v: %v", q, err)
	}

	if page.Workspaces == nil {
		t.Fatalf("query workspaces %+v: workspaces = nil, want a list", q)
	}

	return page
}

// checkWorkspacePage checks the page of q against workspace.PageWorkspaces
// over ListWorkspaces, by name, ID and total.
func checkWorkspacePage(t *testing.T, s WorkspaceStore, q workspace.ListQuery) workspace.ListPage {
	t.Helper()

	all, err := s.ListWorkspaces(t.Context(), q.Namespace)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}

	want := workspace.PageWorkspaces(all, q)
	got := queryWorkspaces(t, s, q)

	if got.Total != want.Total || !slices.Equal(workspacePageNames(got), workspacePageNames(want)) {
		t.Fatalf(
			"query %+v = %v of %d, want %v of %d",
			q,
			workspacePageNames(got),
			got.Total,
			workspacePageNames(want),
			want.Total,
		)
	}

	for i := range got.Workspaces {
		if got.Workspaces[i].ID != want.Workspaces[i].ID || got.Workspaces[i].Ref != want.Workspaces[i].Ref {
			t.Fatalf("workspace %d = %+v, want %+v", i, got.Workspaces[i], want.Workspaces[i])
		}
	}

	return got
}

// walkWorkspacePages reads every page of q with pages of size, and returns
// the names and the size of each page.
func walkWorkspacePages(t *testing.T, s WorkspaceStore, q workspace.ListQuery, size int) ([]string, []int) {
	t.Helper()

	var (
		names []string
		sizes []int
	)

	q.Limit = size

	for q.Offset = 0; ; q.Offset += size {
		page := checkWorkspacePage(t, s, q)
		names = append(names, workspacePageNames(page)...)
		sizes = append(sizes, len(page.Workspaces))

		if q.Offset+size >= page.Total {
			return names, sizes
		}
	}
}

// testWorkspacePageBoundaries walks 7 workspaces with pages of 3: the pages
// hold 3, 3 and 1, in name order, each with the total 7.
func testWorkspacePageBoundaries(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	other := newWorkspaceNamespace(t, s, "other")

	for _, name := range []string{"g", "c", "a", "e", "b", "f", "d"} {
		createTestWorkspace(t, s, ns, name)
	}

	createTestWorkspace(t, s, other, "a0")

	q := workspace.ListQuery{Namespace: ns.Name, NameContains: "", Offset: 0, Limit: 0}

	names, sizes := walkWorkspacePages(t, s, q, 3)
	if !slices.Equal(sizes, []int{3, 3, 1}) || !slices.Equal(names, []string{"a", "b", "c", "d", "e", "f", "g"}) {
		t.Fatalf("pages = %v sized %v, want a to g sized [3 3 1]", names, sizes)
	}
}

// testWorkspacePageCase creates names that differ only in case: the walk
// gives each once, and a search compares the case.
func testWorkspacePageCase(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")

	for _, name := range []string{"net", "Net", "NET", "net-2"} {
		createTestWorkspace(t, s, ns, name)
	}

	q := workspace.ListQuery{Namespace: ns.Name, NameContains: "", Offset: 0, Limit: 0}

	names, _ := walkWorkspacePages(t, s, q, 1)
	if len(names) != 4 {
		t.Fatalf("walked %v, want 4 workspaces", names)
	}

	q.NameContains = "Ne"
	if got := checkWorkspacePage(t, s, q); got.Total != 1 {
		t.Fatalf("search Ne = %v, want Net only", workspacePageNames(got))
	}
}

// testWorkspacePageEmpty reads a namespace with no workspace, a namespace
// that does not exist, an offset after the last workspace and a search with
// no match: each answers an empty page.
func testWorkspacePageEmpty(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	full := newWorkspaceNamespace(t, s, "full")
	createTestWorkspace(t, s, full, "network")

	for _, tc := range []struct {
		q     workspace.ListQuery
		total int
	}{
		{q: workspace.ListQuery{Namespace: ns.Name, NameContains: "", Offset: 0, Limit: 10}, total: 0},
		{q: workspace.ListQuery{Namespace: namespace.Name("missing"), NameContains: "", Offset: 0, Limit: 10}, total: 0},
		{q: workspace.ListQuery{Namespace: full.Name, NameContains: "", Offset: 1, Limit: 10}, total: 1},
		{q: workspace.ListQuery{Namespace: full.Name, NameContains: "", Offset: 1 << 40, Limit: 10}, total: 1},
		{q: workspace.ListQuery{Namespace: full.Name, NameContains: "dns", Offset: 0, Limit: 10}, total: 0},
	} {
		got := checkWorkspacePage(t, s, tc.q)
		if len(got.Workspaces) != 0 || got.Total != tc.total {
			t.Fatalf("query %+v = %v of %d, want none of %d", tc.q, workspacePageNames(got), got.Total, tc.total)
		}
	}
}

// testWorkspacePageDeleted reads the first page, deletes a workspace of the
// first page, then reads the second page: the total drops by one, and the
// second page starts one workspace earlier, as an offset does.
func testWorkspacePageDeleted(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")

	var first workspace.Workspace

	for i, name := range []string{"a", "b", "c", "d", "e"} {
		w := createTestWorkspace(t, s, ns, name)
		if i == 0 {
			first = w
		}
	}

	q := workspace.ListQuery{Namespace: ns.Name, NameContains: "", Offset: 0, Limit: 2}
	got := checkWorkspacePage(t, s, q)
	if !slices.Equal(workspacePageNames(got), []string{"a", "b"}) || got.Total != 5 {
		t.Fatalf("first page = %v of %d, want [a b] of 5", workspacePageNames(got), got.Total)
	}

	err := s.DeleteWorkspace(t.Context(), first.Ref, ns.Fence(),
		inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()})
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	q.Offset = 2
	got = checkWorkspacePage(t, s, q)
	if !slices.Equal(workspacePageNames(got), []string{"d", "e"}) || got.Total != 4 {
		t.Fatalf("second page = %v of %d, want [d e] of 4", workspacePageNames(got), got.Total)
	}
}

// testWorkspacePageSearch walks searches with pages of 2: a search in the
// middle of the name, at the start, a search with the characters that LIKE
// reads as patterns, and the empty search.
func testWorkspacePageSearch(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")

	for i := range 9 {
		createTestWorkspace(t, s, ns, fmt.Sprintf("app-%d-net", i))
	}

	for _, name := range []string{"app_x", "web"} {
		createTestWorkspace(t, s, ns, name)
	}

	for _, search := range []string{"-net", "app", "_", "%", "p-1", ""} {
		q := workspace.ListQuery{Namespace: ns.Name, NameContains: search, Offset: 0, Limit: 0}
		walkWorkspacePages(t, s, q, 2)
	}

	q := workspace.ListQuery{Namespace: ns.Name, NameContains: "_", Offset: 0, Limit: 10}
	if got := checkWorkspacePage(t, s, q); !slices.Equal(workspacePageNames(got), []string{"app_x"}) {
		t.Fatalf("search _ = %v, want [app_x]: the search is not a pattern", workspacePageNames(got))
	}
}

// testWorkspacePageLimits reads an offset below zero, a limit of zero and a
// limit above the cap.
func testWorkspacePageLimits(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")

	for _, name := range []string{"a", "b", "c"} {
		createTestWorkspace(t, s, ns, name)
	}

	for _, tc := range []struct {
		q    workspace.ListQuery
		want []string
	}{
		{q: workspace.ListQuery{Namespace: ns.Name, NameContains: "", Offset: -4, Limit: 2}, want: []string{"a", "b"}},
		{q: workspace.ListQuery{Namespace: ns.Name, NameContains: "", Offset: 1, Limit: 0}, want: []string{"b"}},
		{
			q:    workspace.ListQuery{Namespace: ns.Name, NameContains: "", Offset: 0, Limit: workspace.MaxListLimit + 1},
			want: []string{"a", "b", "c"},
		},
	} {
		if got := checkWorkspacePage(t, s, tc.q); !slices.Equal(workspacePageNames(got), tc.want) || got.Total != 3 {
			t.Fatalf("query %+v = %v of %d, want %v of 3", tc.q, workspacePageNames(got), got.Total, tc.want)
		}
	}
}
