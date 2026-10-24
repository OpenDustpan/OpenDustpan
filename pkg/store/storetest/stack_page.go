package storetest

import (
	"maps"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// stackPageCases are the cases of QueryStacks. Run runs them.
func stackPageCases() []struct {
	test func(t *testing.T, s Store)
	name string
} {
	return []struct {
		test func(t *testing.T, s Store)
		name string
	}{
		{name: "a stack page walks every page in the order of the key", test: testStackPageBoundaries},
		{name: "a stack page orders ties on the project and the name by the rest of the key", test: testStackPageTies},
		{name: "a stack page answers an empty page", test: testStackPageEmpty},
		{name: "a stack page skips a stack deleted between pages", test: testStackPageDeleted},
		{name: "a stack page pages a project filter", test: testStackPageProject},
		{name: "a stack page reads the limit after the floor and the cap", test: testStackPageLimit},
		{name: "a stack page pages a tag filter", test: testStackPageTag},
	}
}

// stackKeys returns namespace/project/name of each stack.
func stackKeys(stacks []stack.Stack) []string {
	out := make([]string, len(stacks))
	for i := range stacks {
		out[i] = stacks[i].Ref.Namespace.String() + "/" + stacks[i].Ref.Project.String() + "/" +
			stacks[i].Ref.Stack.String()
	}

	return out
}

// queryStacks runs q and fails the test on an error.
func queryStacks(t *testing.T, s Store, q stack.PageQuery) []stack.Stack {
	t.Helper()

	got, err := s.QueryStacks(t.Context(), q)
	if err != nil {
		t.Fatalf("query stacks %+v: %v", q, err)
	}

	if got == nil {
		t.Fatalf("query stacks %+v = nil, want a list", q)
	}

	return got
}

// checkStackPage checks the page of q against stack.ListPage over
// ListStacks, with the IDs and the tags, and returns it.
func checkStackPage(t *testing.T, s Store, q stack.PageQuery) []stack.Stack {
	t.Helper()

	want, err := stack.ListPage(t.Context(), s.ListStacks, q)
	if err != nil {
		t.Fatalf("list page: %v", err)
	}

	got := queryStacks(t, s, q)
	if !slices.Equal(stackKeys(got), stackKeys(want)) {
		t.Fatalf("query %+v = %v, want %v", q, stackKeys(got), stackKeys(want))
	}

	for i := range got {
		if got[i].ID != want[i].ID || !maps.Equal(got[i].Tags, want[i].Tags) ||
			!got[i].UpdatedAt.Equal(want[i].UpdatedAt) {
			t.Fatalf("stack %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	return got
}

// walkStackPages reads every page of q with pages of size, from the first,
// and returns the keys and the size of each page.
func walkStackPages(t *testing.T, s Store, q stack.PageQuery, size int) ([]string, []int) {
	t.Helper()

	var (
		keys  []string
		sizes []int
	)

	q.Limit = size
	q.After = nil

	for range 100 {
		page := checkStackPage(t, s, q)
		keys = append(keys, stackKeys(page)...)
		sizes = append(sizes, len(page))

		if len(page) < size {
			return keys, sizes
		}

		c := stack.CursorOf(page[len(page)-1])
		q.After = &c
	}

	t.Fatalf("query stacks %+v did not end after 100 pages", q)

	return nil, nil
}

// createStackRefs creates the stacks named by project and name in ns.
func createStackRefs(t *testing.T, s Store, ns namespace.Namespace, names ...[2]string) []stack.Stack {
	t.Helper()

	out := make([]stack.Stack, 0, len(names))

	for _, nn := range names {
		st := newStack(ns.Name, nn[0], nn[1])
		if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", st.Ref, err)
		}

		out = append(out, st)
	}

	return out
}

// testStackPageBoundaries walks the stacks of two of three namespaces with
// pages of 2: the pages hold 2, 2 and 1 stacks in the order of the key, and
// no stack of the third namespace.
func testStackPageBoundaries(t *testing.T, s Store) {
	t.Helper()

	a := newNamespace(t, s, "a")
	b := newNamespace(t, s, "b")
	c := newNamespace(t, s, "c")

	createStackRefs(t, s, c, [2]string{"web", "prod"}, [2]string{"api", "dev"})
	createStackRefs(t, s, b, [2]string{"web", "prod"})
	createStackRefs(t, s, a, [2]string{"web", "prod"}, [2]string{"api", "prod"}, [2]string{"api", "dev"})

	q := stack.PageQuery{After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{c.Name, a.Name}, Limit: 0}

	keys, sizes := walkStackPages(t, s, q, 2)

	want := []string{"a/api/dev", "a/api/prod", "a/web/prod", "c/api/dev", "c/web/prod"}
	if !slices.Equal(keys, want) || !slices.Equal(sizes, []int{2, 2, 1}) {
		t.Fatalf("walk = %v sized %v, want %v sized [2 2 1]", keys, sizes, want)
	}
}

// testStackPageTies creates stacks that share the project, the name, or
// both across namespaces: a walk with pages of 1 gives each once, in the
// order of namespace, project and name.
func testStackPageTies(t *testing.T, s Store) {
	t.Helper()

	a := newNamespace(t, s, "a")
	b := newNamespace(t, s, "b")

	for _, ns := range []namespace.Namespace{b, a} {
		createStackRefs(t, s, ns, [2]string{"x", "prod"}, [2]string{"y", "prod"}, [2]string{"x", "dev"})
	}

	q := stack.PageQuery{After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{a.Name, b.Name}, Limit: 0}

	keys, _ := walkStackPages(t, s, q, 1)

	want := []string{"a/x/dev", "a/x/prod", "a/y/prod", "b/x/dev", "b/x/prod", "b/y/prod"}
	if !slices.Equal(keys, want) {
		t.Fatalf("walk = %v, want %v", keys, want)
	}
}

// testStackPageEmpty reads no namespace, a namespace with no stack, a
// namespace that does not exist and a cursor after the last stack: each
// answers an empty list.
func testStackPageEmpty(t *testing.T, s Store) {
	t.Helper()

	empty := newNamespace(t, s, "empty")
	full := newNamespace(t, s, "full")
	createStackRefs(t, s, full, [2]string{"web", "prod"})

	last := stack.PageCursor{Namespace: full.Name, Project: "web", Name: "prod"}

	for i, q := range []stack.PageQuery{
		{After: nil, Project: "", Tag: nil, Namespaces: nil, Limit: 10},
		{After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{empty.Name}, Limit: 10},
		{After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{"missing"}, Limit: 10},
		{After: &last, Project: "", Tag: nil, Namespaces: []namespace.Name{full.Name}, Limit: 10},
		{After: nil, Project: "api", Tag: nil, Namespaces: []namespace.Name{full.Name}, Limit: 10},
	} {
		if got := checkStackPage(t, s, q); len(got) != 0 {
			t.Fatalf("query %d = %v, want none", i, stackKeys(got))
		}
	}
}

// testStackPageDeleted reads the first page, deletes the last stack of that
// page and the first stack of the next page, then reads the next page after
// the cursor: it starts at the next live stack.
func testStackPageDeleted(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	stacks := createStackRefs(
		t,
		s,
		ns,
		[2]string{
			"web",
			"a",
		},
		[2]string{"web", "b"},
		[2]string{"web", "c"},
		[2]string{"web", "d"},
		[2]string{"web", "e"},
	)

	q := stack.PageQuery{After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{ns.Name}, Limit: 2}

	first := checkStackPage(t, s, q)
	if !slices.Equal(stackKeys(first), []string{"acme/web/a", "acme/web/b"}) {
		t.Fatalf("first page = %v, want acme/web/a and acme/web/b", stackKeys(first))
	}

	for _, gone := range []stack.Ref{stacks[1].Ref, stacks[2].Ref} {
		err := s.DeleteStack(t.Context(), gone, ns.Fence(), stack.EmptyGuard(),
			inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()})
		if err != nil {
			t.Fatalf("delete stack %s: %v", gone, err)
		}
	}

	c := stack.CursorOf(first[1])
	q.After = &c

	if got := checkStackPage(t, s, q); !slices.Equal(stackKeys(got), []string{"acme/web/d", "acme/web/e"}) {
		t.Fatalf("page after a delete = %v, want acme/web/d and acme/web/e", stackKeys(got))
	}
}

// testStackPageProject walks a project filter over two namespaces with pages
// of 2.
func testStackPageProject(t *testing.T, s Store) {
	t.Helper()

	a := newNamespace(t, s, "a")
	b := newNamespace(t, s, "b")

	for _, ns := range []namespace.Namespace{a, b} {
		createStackRefs(t, s, ns, [2]string{"web", "prod"}, [2]string{"api", "prod"}, [2]string{"web", "dev"},
			[2]string{"webx", "dev"})
	}

	q := stack.PageQuery{After: nil, Project: "web", Tag: nil, Namespaces: []namespace.Name{a.Name, b.Name}, Limit: 0}

	keys, _ := walkStackPages(t, s, q, 2)

	want := []string{"a/web/dev", "a/web/prod", "b/web/dev", "b/web/prod"}
	if !slices.Equal(keys, want) {
		t.Fatalf("walk = %v, want %v", keys, want)
	}
}

// testStackPageLimit reads a limit of zero and one above the cap.
func testStackPageLimit(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	createStackRefs(t, s, ns, [2]string{"web", "a"}, [2]string{"web", "b"}, [2]string{"web", "c"})

	for _, tc := range []struct{ limit, want int }{{0, 1}, {-3, 1}, {stack.MaxPageLimit + 1, 3}} {
		q := stack.PageQuery{After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{ns.Name}, Limit: tc.limit}
		if got := checkStackPage(t, s, q); len(got) != tc.want {
			t.Fatalf("limit %d read %d stacks, want %d", tc.limit, len(got), tc.want)
		}
	}
}

// testStackPageTag pages a tag filter, with and without a project. A name
// alone keeps every stack with the tag, a name with a value keeps the stacks
// with that value, an empty value is a value, and a filter that no stack
// matches gives an empty page.
func testStackPageTag(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")

	for _, tc := range []struct {
		tags map[string]string
		name string
	}{
		{name: "blue", tags: map[string]string{"team": "blue"}},
		{name: "blue2", tags: map[string]string{"team": "blue", "tier": "1"}},
		{name: "empty", tags: map[string]string{"team": ""}},
		{name: "none", tags: map[string]string{}},
		{name: "owner", tags: map[string]string{"owner": "blue"}},
	} {
		st := newStack(ns.Name, "web", tc.name)
		st.Tags = tc.tags

		if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", st.Ref, err)
		}
	}

	for _, tc := range []struct {
		want []string
		tag  stack.TagFilter
	}{
		{
			tag:  stack.TagFilter{Name: "team", Value: "", HasValue: false},
			want: []string{"acme/web/blue", "acme/web/blue2", "acme/web/empty"},
		},
		{
			tag:  stack.TagFilter{Name: "team", Value: "blue", HasValue: true},
			want: []string{"acme/web/blue", "acme/web/blue2"},
		},
		{tag: stack.TagFilter{Name: "team", Value: "", HasValue: true}, want: []string{"acme/web/empty"}},
		{tag: stack.TagFilter{Name: "team", Value: "red", HasValue: true}, want: nil},
		{tag: stack.TagFilter{Name: "owner", Value: "blue", HasValue: true}, want: []string{"acme/web/owner"}},
	} {
		for _, project := range []stack.ProjectName{"", "web"} {
			q := stack.PageQuery{
				After:      nil,
				Project:    project,
				Tag:        &tc.tag,
				Namespaces: []namespace.Name{ns.Name},
				Limit:      0,
			}

			keys, _ := walkStackPages(t, s, q, 2)
			if !slices.Equal(keys, tc.want) {
				t.Fatalf("walk of tag %+v in project %q = %v, want %v", tc.tag, project, keys, tc.want)
			}
		}
	}
}
