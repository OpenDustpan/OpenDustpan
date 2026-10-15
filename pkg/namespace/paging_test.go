package namespace_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// pagingFake is a namespace.Store that also pages, and records the query
// and the count filter it receives.
type pagingFake struct {
	gotQuery namespace.PageQuery
	gotAll   bool
	gotNames []namespace.Name
	page     []namespace.Namespace
	total    int64
	pageErr  error
	countErr error
}

var (
	_ namespace.Store = (*pagingFake)(nil)
	_ namespace.Pager = (*pagingFake)(nil)
)

func (f *pagingFake) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (f *pagingFake) GetNamespace(_ context.Context, _ namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotFound
}

func (f *pagingFake) SetOverrides(
	_ context.Context, _ namespace.Name, _ namespace.Overrides, _ namespace.Fence,
) error {
	return nil
}

func (f *pagingFake) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (f *pagingFake) Ping(_ context.Context) error {
	return nil
}

func (f *pagingFake) PageNamespaces(_ context.Context, q namespace.PageQuery) ([]namespace.Namespace, error) {
	f.gotQuery = q

	return f.page, f.pageErr
}

func (f *pagingFake) CountNamespaces(_ context.Context, ftr namespace.Filter) (int64, error) {
	f.gotAll = ftr.All
	f.gotNames = ftr.Names

	return f.total, f.countErr
}

// storeOnly is a namespace.Store without the paging port.
type storeOnly struct{}

var _ namespace.Store = (*storeOnly)(nil)

func (f *storeOnly) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (f *storeOnly) GetNamespace(_ context.Context, _ namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotFound
}

func (f *storeOnly) SetOverrides(
	_ context.Context, _ namespace.Name, _ namespace.Overrides, _ namespace.Fence,
) error {
	return nil
}

func (f *storeOnly) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (f *storeOnly) Ping(_ context.Context) error {
	return nil
}

// openService returns a Service on store. Region and Now are fixed, so only
// the store varies between cases.
func openService(t *testing.T, store namespace.Store) *namespace.Service {
	t.Helper()

	svc, err := namespace.NewService(namespace.Config{
		Store:  store,
		Now:    time.Now,
		Region: namespace.Region("eu-west"),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc
}

func TestPageNamespacesRejectsLimits(t *testing.T) {
	t.Parallel()

	svc := openService(t, &pagingFake{
		gotQuery: namespace.PageQuery{After: "", Limit: 0, Filter: namespace.Filter{All: false, Names: nil}},
		gotAll:   false,
		gotNames: nil,
		page:     nil,
		total:    0,
		pageErr:  nil,
		countErr: nil,
	})

	for _, limit := range []int{-1, 0, 1002} {
		_, err := svc.PageNamespaces(t.Context(), namespace.PageQuery{
			After:  "",
			Limit:  limit,
			Filter: namespace.Filter{All: true, Names: nil},
		})
		if !errors.Is(err, namespace.ErrInvalidPage) {
			t.Fatalf("page with limit %d: err = %v, want ErrInvalidPage", limit, err)
		}
	}
}

func TestPageNamespacesNeedsPager(t *testing.T) {
	t.Parallel()

	svc := openService(t, &storeOnly{})

	_, err := svc.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "",
		Limit:  10,
		Filter: namespace.Filter{All: true, Names: nil},
	})
	if !errors.Is(err, namespace.ErrNoPaging) {
		t.Fatalf("page without a pager: err = %v, want ErrNoPaging", err)
	}

	_, err = svc.CountNamespaces(t.Context(), namespace.Filter{All: true, Names: nil})
	if !errors.Is(err, namespace.ErrNoPaging) {
		t.Fatalf("count without a pager: err = %v, want ErrNoPaging", err)
	}
}

func TestPageNamespacesDedupsNames(t *testing.T) {
	t.Parallel()

	fake := &pagingFake{
		gotQuery: namespace.PageQuery{After: "", Limit: 0, Filter: namespace.Filter{All: false, Names: nil}},
		gotAll:   false,
		gotNames: nil,
		page:     []namespace.Namespace{},
		total:    0,
		pageErr:  nil,
		countErr: nil,
	}
	svc := openService(t, fake)

	in := []namespace.Name{"beta", "acme", "beta", "acme", "zeta"}

	got, err := svc.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "acme",
		Limit:  10,
		Filter: namespace.Filter{All: false, Names: in},
	})
	if err != nil {
		t.Fatalf("page namespaces: %v", err)
	}

	if got == nil {
		t.Fatal("page namespaces = nil, want an empty page")
	}

	want := []namespace.Name{"beta", "acme", "zeta"}
	if !slices.Equal(fake.gotQuery.Filter.Names, want) {
		t.Fatalf("paged names = %v, want %v", fake.gotQuery.Filter.Names, want)
	}

	if fake.gotQuery.After != "acme" || fake.gotQuery.Limit != 10 || fake.gotQuery.Filter.All {
		t.Fatalf("paged query = %+v, want after acme limit 10 without all", fake.gotQuery)
	}

	if !slices.Equal(in, []namespace.Name{"beta", "acme", "beta", "acme", "zeta"}) {
		t.Fatalf("input names = %v, want them unchanged", in)
	}
}

// errPageBoom is a distinct error for the store-error test.
var errPageBoom = errors.New("boom")

func TestPageNamespacesWrapsStoreErrors(t *testing.T) {
	t.Parallel()

	fake := &pagingFake{
		gotQuery: namespace.PageQuery{After: "", Limit: 0, Filter: namespace.Filter{All: false, Names: nil}},
		gotAll:   false,
		gotNames: nil,
		page:     nil,
		total:    0,
		pageErr:  errPageBoom,
		countErr: errPageBoom,
	}
	svc := openService(t, fake)

	_, err := svc.PageNamespaces(t.Context(), namespace.PageQuery{
		After:  "",
		Limit:  10,
		Filter: namespace.Filter{All: true, Names: nil},
	})
	if !errors.Is(err, errPageBoom) {
		t.Fatalf("page namespaces: err = %v, want the store error", err)
	}

	_, err = svc.CountNamespaces(t.Context(), namespace.Filter{All: true, Names: nil})
	if !errors.Is(err, errPageBoom) {
		t.Fatalf("count namespaces: err = %v, want the store error", err)
	}
}

func TestCountNamespacesDedupsNames(t *testing.T) {
	t.Parallel()

	fake := &pagingFake{
		gotQuery: namespace.PageQuery{After: "", Limit: 0, Filter: namespace.Filter{All: false, Names: nil}},
		gotAll:   false,
		gotNames: nil,
		page:     nil,
		total:    2,
		pageErr:  nil,
		countErr: nil,
	}
	svc := openService(t, fake)

	in := []namespace.Name{"acme", "beta", "acme"}

	total, err := svc.CountNamespaces(t.Context(), namespace.Filter{All: false, Names: in})
	if err != nil {
		t.Fatalf("count namespaces: %v", err)
	}

	if total != 2 {
		t.Fatalf("count namespaces = %d, want 2", total)
	}

	if !slices.Equal(fake.gotNames, []namespace.Name{"acme", "beta"}) {
		t.Fatalf("counted names = %v, want [acme beta]", fake.gotNames)
	}

	if fake.gotAll {
		t.Fatal("counted all = true, want false")
	}

	if !slices.Equal(in, []namespace.Name{"acme", "beta", "acme"}) {
		t.Fatalf("input names = %v, want them unchanged", in)
	}
}
