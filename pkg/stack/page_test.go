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
			name: "the first page", q: stack.PageQuery{After: nil, Project: "", Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("a", "", ""), in("b", "", ""), in("c", "", "")},
		},
		{
			name: "after the cursor", q: stack.PageQuery{After: after, Project: "", Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("b", "web", "prod"), in("c", "", "")},
		},
		{
			name: "after the cursor in the project",
			q:    stack.PageQuery{After: after, Project: "web", Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("b", "", "prod"), in("c", "", "")},
		},
		{
			name: "a cursor before the project",
			q:    stack.PageQuery{After: after, Project: "zed", Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("b", "", ""), in("c", "", "")},
		},
		{
			name: "a cursor after the project",
			q:    stack.PageQuery{After: after, Project: "api", Namespaces: all, Limit: 1},
			want: []stack.NamespaceRange{in("c", "", "")},
		},
		{
			name: "no namespace", q: stack.PageQuery{After: nil, Project: "", Namespaces: nil, Limit: 1},
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
			q:    stack.PageQuery{After: nil, Project: "", Namespaces: []namespace.Name{"a", "b"}, Limit: 3},
			want: []string{"a/api/dev", "a/web/prod", "b/api/prod"},
		},
		{
			q:    stack.PageQuery{After: after, Project: "", Namespaces: []namespace.Name{"a", "b", "c"}, Limit: 10},
			want: []string{"b/api/prod", "b/web/prod", "c/web/dev"},
		},
		{
			q:    stack.PageQuery{After: nil, Project: "web", Namespaces: []namespace.Name{"c", "a"}, Limit: 10},
			want: []string{"a/web/prod", "c/web/dev"},
		},
		{
			q:    stack.PageQuery{After: nil, Project: "", Namespaces: []namespace.Name{"a"}, Limit: 0},
			want: []string{"a/api/dev"},
		},
	} {
		if got := keys(stack.PageStacks(all, tc.q)); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("PageStacks(%+v) = %v, want %v", tc.q, got, tc.want)
		}
	}
}
