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
// deny, and gives scope as the scope of every principal.
type scopedAuthorizer struct {
	deny  map[string]bool
	scope identity.Scope
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

			for _, scope := range []identity.Scope{namespaceScope("acme"), {All: true}} { //nolint:exhaustruct_v5 // All.
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
	handler := newPagingHandlerWith(t, scopedAuthorizer{deny: nil, scope: scope}, ws)

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
		authz := scopedAuthorizer{deny: map[string]bool{"acme": true, "zeta": true}, scope: scope}

		doc := getListPage(t, newPagingHandlerWith(t, authz, ws), "/api/v2/organizations/acme/workspaces")
		if got := listNames(doc); !slices.Equal(got, []string{"beta"}) || doc.Meta.Pagination.TotalCount != 1 {
			t.Fatalf("list = %v of %d, want [beta] of 1", got, doc.Meta.Pagination.TotalCount)
		}

		if queries.Load() != 0 {
			t.Fatalf("store page queries = %d, want 0", queries.Load())
		}
	}
}

// TestListWorkspacesStorePageRechecks proves the check of unit:read on the
// store page alone: the scope holds the namespace, but the authorizer denies
// one workspace, and the page leaves it out.
func TestListWorkspacesStorePageRechecks(t *testing.T) {
	t.Parallel()

	queries := &atomic.Int64{}
	ws := queryCounting{pagingWorkspaces: newPagingWorkspaces(t), queries: queries}
	authz := scopedAuthorizer{deny: map[string]bool{"beta": true}, scope: namespaceScope("acme")}

	doc := getListPage(t, newPagingHandlerWith(t, authz, ws), "/api/v2/organizations/acme/workspaces")
	if got := listNames(doc); !slices.Equal(got, []string{"acme", "zeta"}) {
		t.Fatalf("list = %v, want [acme zeta]", got)
	}

	if queries.Load() != 1 {
		t.Fatalf("store page queries = %d, want 1", queries.Load())
	}
}
