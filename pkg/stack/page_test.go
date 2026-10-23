package stack_test

import (
	"reflect"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestPageRanges proves the range of each namespace of a page: the order and
// the copies of the namespaces, the namespaces before the cursor, and the
// cursor with and without the project filter.
func TestPageRanges(t *testing.T) {
	t.Parallel()

	in := func(ns, project, name string) stack.NamespaceRange {
		return stack.NamespaceRange{
			Namespace: namespace.Name(ns), AfterProject: stack.ProjectName(project), AfterName: stack.Name(name),
		}
	}

	after := &stack.PageCursor{Namespace: "b", Project: "web", Name: "prod"}
	all := []namespace.Name{"c", "a", "b", "a"}

	tests := []struct {
		name string
		q    stack.PageQuery
		want []stack.NamespaceRange
	}{
		{
			name: "the first page", q: stack.PageQuery{After: nil, Project: "", Tag: nil, Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("a", "", ""), in("b", "", ""), in("c", "", "")},
		},
		{
			name: "after the cursor", q: stack.PageQuery{After: after, Project: "", Tag: nil, Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("b", "web", "prod"), in("c", "", "")},
		},
		{
			name: "after the cursor in the project",
			q:    stack.PageQuery{After: after, Project: "web", Tag: nil, Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("b", "", "prod"), in("c", "", "")},
		},
		{
			name: "a cursor before the project",
			q:    stack.PageQuery{After: after, Project: "zed", Tag: nil, Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("b", "", ""), in("c", "", "")},
		},
		{
			name: "a cursor after the project",
			q:    stack.PageQuery{After: after, Project: "api", Tag: nil, Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("c", "", "")},
		},
		{
			name: "no namespace", q: stack.PageQuery{After: nil, Project: "", Tag: nil, Namespaces: nil, Limit: 1},
			want: []stack.NamespaceRange{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.q.Ranges(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Ranges = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestPageStacks proves the order, the cursor, the filters and the limit of
// PageStacks.
func TestPageStacks(t *testing.T) {
	t.Parallel()

	all := make([]stack.Stack, 0, 5)

	for _, r := range [][3]string{
		{"b", "web", "prod"}, {"a", "web", "prod"}, {"a", "api", "dev"}, {"c", "web", "dev"}, {"b", "api", "prod"},
	} {
		all = append(all, stack.Stack{ //nolint:exhaustruct_v5 // The key only.
			Ref: stack.Ref{Namespace: namespace.Name(r[0]), Project: stack.ProjectName(r[1]), Stack: stack.Name(r[2])},
		})
	}

	keys := func(stacks []stack.Stack) []string {
		out := make([]string, 0, len(stacks))
		for i := range stacks {
			out = append(out, stacks[i].Ref.String())
		}

		return out
	}

	after := &stack.PageCursor{Namespace: "a", Project: "web", Name: "prod"}

	for _, tc := range []struct {
		q    stack.PageQuery
		want []string
	}{
		{
			q:    stack.PageQuery{After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{"a", "b"}, Limit: 3},
			want: []string{"a/api/dev", "a/web/prod", "b/api/prod"},
		},
		{
			q:    stack.PageQuery{After: after, Project: "", Tag: nil, Namespaces: []namespace.Name{"a", "b", "c"}, Limit: 10},
			want: []string{"b/api/prod", "b/web/prod", "c/web/dev"},
		},
		{
			q:    stack.PageQuery{After: nil, Project: "web", Tag: nil, Namespaces: []namespace.Name{"c", "a"}, Limit: 10},
			want: []string{"a/web/prod", "c/web/dev"},
		},
		{
			q:    stack.PageQuery{After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{"a"}, Limit: 0},
			want: []string{"a/api/dev"},
		},
	} {
		if got := keys(stack.PageStacks(all, tc.q)); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("PageStacks(%+v) = %v, want %v", tc.q, got, tc.want)
		}
	}
}

// TestTagFilterMatches proves that a tag filter keeps a stack with the tag of
// its name, with any value or with its value, and that a nil filter keeps
// every stack.
func TestTagFilterMatches(t *testing.T) {
	t.Parallel()

	blue := map[string]string{"team": "blue"}
	empty := map[string]string{"team": ""}

	anyValue := func(name string) *stack.TagFilter {
		return &stack.TagFilter{Name: name, Value: "", HasValue: false}
	}

	withValue := func(name, value string) *stack.TagFilter {
		return &stack.TagFilter{Name: name, Value: value, HasValue: true}
	}

	for _, tc := range []struct {
		name   string
		filter *stack.TagFilter
		tags   map[string]string
		want   bool
	}{
		{name: "no filter", filter: nil, tags: map[string]string{}, want: true},
		{name: "a name with any value", filter: anyValue("team"), tags: blue, want: true},
		{name: "a name and its value", filter: withValue("team", "blue"), tags: blue, want: true},
		{name: "another value", filter: withValue("team", "red"), tags: blue, want: false},
		{name: "an empty value", filter: withValue("team", ""), tags: empty, want: true},
		{name: "an empty value is a value", filter: withValue("team", ""), tags: blue, want: false},
		{name: "no such tag", filter: anyValue("owner"), tags: blue, want: false},
	} {
		if got := tc.filter.Matches(tc.tags); got != tc.want {
			t.Errorf("%s: Matches(%v) = %v, want %v", tc.name, tc.tags, got, tc.want)
		}
	}
}
