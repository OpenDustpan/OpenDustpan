package tfe_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// queryCounting is the paging workspaces with a count of the page queries.
type queryCounting struct {
	pagingWorkspaces

	queries *atomic.Int64
}

func (q queryCounting) Query(ctx context.Context, lq workspace.ListQuery) (workspace.ListPage, error) {
	q.queries.Add(1)

	return q.pagingWorkspaces.Query(ctx, lq)
}

// scopedAuthorizer allows each action except unit:read on the workspaces in
// deny, and gives scope as the scope of every principal. It says that its
// scope is exact when exact is true.
type scopedAuthorizer struct {
	deny  map[string]bool
	scope identity.Scope
	exact bool
}

func (a scopedAuthorizer) ScopeExact() bool { return a.exact }

// limitedLike is a decorator as gate.LimitedAuthorizer is: it gives the scope
// of the grants, does not filter it, and denies single workspaces in
// Authorize. It does not implement identity.ExactScoper.
type limitedLike struct{ inner scopedAuthorizer }

func (l limitedLike) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	return l.inner.Authorize(ctx, p, action, obj)
}

func (l limitedLike) ListObjects(
	ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	return l.inner.ListObjects(ctx, p, action)
}

// refusingNamespace is an exact authorizer with an All scope that refuses
// unit:read on every unit of every namespace, as a gate does for an address
// that the namespace does not allow.
type refusingNamespace struct{}

func (refusingNamespace) ScopeExact() bool { return true }

func (refusingNamespace) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionUnitRead {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

func (refusingNamespace) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Every(), nil
}

func (a scopedAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionUnitRead && a.deny[obj.UnitName()] {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

func (a scopedAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return a.scope, nil
}

// namespaceScope returns a scope that holds the namespace ns.
func namespaceScope(ns string) identity.Scope {
	return identity.Scope{ //nolint:exhaustruct_v5 // A namespace grant only.
		Namespaces: []identity.Object{identity.NamespaceObject(namespace.Name(ns))},
	}
}

// TestListWorkspacesStorePage proves that a caller whose scope holds the
// namespace gets the page and the count from one store query, with the same
// answers as the list that checks each workspace.
func TestListWorkspacesStorePage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, query string
		want        []string
		total       int
	}{
		{name: "first page", query: "page[number]=1&page[size]=2", want: []string{"acme", "beta"}, total: 3},
		{name: "last page", query: "page[number]=2&page[size]=2", want: []string{"zeta"}, total: 3},
		{name: "after the last page", query: "page[number]=9&page[size]=2", want: []string{}, total: 3},
		{name: "search", query: "search[name]=et", want: []string{"beta", "zeta"}, total: 2},
		{
			name:  "search and page",
			query: "search[name]=et&page[number]=2&page[size]=1",
			want:  []string{"zeta"},
			total: 2,
		},
		{name: "search with no match", query: "search[name]=ET", want: []string{}, total: 0},
		{
			name: "huge page number", query: "page[number]=9223372036854775807&page[size]=100",
			want: []string{}, total: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, scope := range []identity.Scope{namespaceScope("acme"), identity.Every()} {
				checkStorePage(t, scope, tc.query, tc.want, tc.total)
			}
		})
	}
}

// checkStorePage lists the workspaces of acme with query for a caller with
// scope, and checks the names, the total, the one store query, and the
// pagination of the list that checks each workspace.
func checkStorePage(t *testing.T, scope identity.Scope, query string, want []string, total int) {
	t.Helper()

	queries := &atomic.Int64{}
	ws := queryCounting{pagingWorkspaces: newPagingWorkspaces(t), queries: queries}
	handler := newPagingHandlerWith(t, scopedAuthorizer{deny: nil, scope: scope, exact: true}, ws)

	doc := getListPage(t, handler, "/api/v2/organizations/acme/workspaces?"+query)
	if got := listNames(doc); !slices.Equal(got, want) || doc.Meta.Pagination.TotalCount != total {
		t.Fatalf("list = %v of %d, want %v of %d", got, doc.Meta.Pagination.TotalCount, want, total)
	}

	if queries.Load() != 1 {
		t.Fatalf("store page queries = %d, want 1", queries.Load())
	}

	fallback := getListPage(t, newPagingHandler(t, allowAll()), "/api/v2/organizations/acme/workspaces?"+query)
	if !reflect.DeepEqual(fallback.Meta.Pagination, doc.Meta.Pagination) {
		t.Fatalf("pagination = %+v, want the pagination of the checked list %+v",
			doc.Meta.Pagination, fallback.Meta.Pagination)
	}
}

// TestListWorkspacesOtherScopeChecksEach proves that a caller whose scope
// holds another namespace, or single units only, gets the list that checks
// each workspace and no store page.
func TestListWorkspacesOtherScopeChecksEach(t *testing.T) {
	t.Parallel()

	ref, err := identity.UnitObject("opentofu", "acme", "beta")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	for _, scope := range []identity.Scope{
		namespaceScope("other"),
		{Units: []identity.Object{ref}}, //nolint:exhaustruct_v5 // A unit grant only.
	} {
		queries := &atomic.Int64{}
		ws := queryCounting{pagingWorkspaces: newPagingWorkspaces(t), queries: queries}
		authz := scopedAuthorizer{deny: map[string]bool{"acme": true, "zeta": true}, scope: scope, exact: true}

		doc := getListPage(t, newPagingHandlerWith(t, authz, ws), "/api/v2/organizations/acme/workspaces")
		if got := listNames(doc); !slices.Equal(got, []string{"beta"}) || doc.Meta.Pagination.TotalCount != 1 {
			t.Fatalf("list = %v of %d, want [beta] of 1", got, doc.Meta.Pagination.TotalCount)
		}

		if queries.Load() != 0 {
			t.Fatalf("store page queries = %d, want 0", queries.Load())
		}
	}
}

// TestListWorkspacesDeniedWorkspaceCounts proves that the count and the pages
// hold only the workspaces that the caller may read, for an authorizer whose
// scope holds the namespace but whose decisions deny the workspace beta: one
// that does not say its scope is exact, a decorator such as the access limits,
// and one that says so but denies a workspace of the page. Each case must give
// the same answer as the list that checks each workspace.
func TestListWorkspacesDeniedWorkspaceCounts(t *testing.T) {
	t.Parallel()

	deny := map[string]bool{"beta": true}
	inexact := scopedAuthorizer{deny: deny, scope: namespaceScope("acme"), exact: false}
	exact := scopedAuthorizer{deny: deny, scope: namespaceScope("acme"), exact: true}

	authorizers := []struct {
		authz       identity.Authorizer
		name        string
		wantQueries int64
		// pageCheckOnly is true when only the check of the page can find the
		// denied workspace, so that a page without it is out of reach.
		pageCheckOnly bool
	}{
		{name: "scope not exact", authz: inexact, wantQueries: 0, pageCheckOnly: false},
		{name: "decorator", authz: limitedLike{inner: exact}, wantQueries: 0, pageCheckOnly: false},
		{name: "exact scope, denied workspace on the page", authz: exact, wantQueries: 1, pageCheckOnly: true},
	}

	pages := []struct {
		name, query string
		want        []string
		totalPages  int
		holdsDenied bool
	}{
		{name: "one page", query: "", want: []string{"acme", "zeta"}, totalPages: 1, holdsDenied: true},
		{name: "page 1 of size 1", query: "page[size]=1", want: []string{"acme"}, totalPages: 2, holdsDenied: false},
		{
			name: "page 2 of size 1", query: "page[number]=2&page[size]=1", want: []string{"zeta"}, totalPages: 2,
			holdsDenied: true,
		},
		{
			name: "page 3 of size 1", query: "page[number]=3&page[size]=1", want: []string{}, totalPages: 2,
			holdsDenied: false,
		},
	}

	for _, a := range authorizers {
		for _, pg := range pages {
			if a.pageCheckOnly && !pg.holdsDenied {
				continue
			}

			t.Run(a.name+"/"+pg.name, func(t *testing.T) {
				t.Parallel()

				checkDeniedCount(
					t,
					a.authz,
					deniedWant{query: pg.query, names: pg.want, totalPages: pg.totalPages, queries: a.wantQueries},
				)
			})
		}
	}
}

// deniedWant holds the request and the expected answer of checkDeniedCount.
type deniedWant struct {
	query      string
	names      []string
	totalPages int
	queries    int64
}

// checkDeniedCount lists the workspaces of acme with query and checks the
// names, the total count of 2, the total pages, and that the list reads at
// most wantQueries pages from the store.
func checkDeniedCount(t *testing.T, authz identity.Authorizer, want deniedWant) {
	t.Helper()

	queries := &atomic.Int64{}
	ws := queryCounting{pagingWorkspaces: newPagingWorkspaces(t), queries: queries}

	doc := getListPage(t, newPagingHandlerWith(t, authz, ws), "/api/v2/organizations/acme/workspaces?"+want.query)
	if got := listNames(doc); !slices.Equal(got, want.names) {
		t.Fatalf("list = %v, want %v", got, want.names)
	}

	if got := doc.Meta.Pagination; got.TotalCount != 2 || got.TotalPages != want.totalPages {
		t.Fatalf("total-count = %d, total-pages = %d, want 2 and %d", got.TotalCount, got.TotalPages, want.totalPages)
	}

	if queries.Load() > want.queries {
		t.Fatalf("store page queries = %d, want at most %d", queries.Load(), want.queries)
	}
}

// TestListWorkspacesRefusedNamespaceNoStoreCount proves that an authorizer
// with an exact All scope that refuses unit:read in the namespace gets no
// store page and no store count, also for a page after the last page, where
// no row is left for the check of the page to drop.
func TestListWorkspacesRefusedNamespaceNoStoreCount(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"", "page[number]=999", "page[number]=2&page[size]=1"} {
		queries := &atomic.Int64{}
		ws := queryCounting{pagingWorkspaces: newPagingWorkspaces(t), queries: queries}

		handler := newPagingHandlerWith(t, refusingNamespace{}, ws)
		doc := getListPage(t, handler, "/api/v2/organizations/acme/workspaces?"+query)
		if got := listNames(doc); len(got) != 0 || doc.Meta.Pagination.TotalCount != 0 {
			t.Fatalf("%q: list = %v of %d, want none of 0", query, got, doc.Meta.Pagination.TotalCount)
		}

		if queries.Load() != 0 {
			t.Fatalf("%q: store page queries = %d, want 0", query, queries.Load())
		}
	}
}
