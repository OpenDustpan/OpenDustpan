package stack

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxPageLimit is the largest number of stacks that one QueryStacks call
// reads.
const MaxPageLimit = 1000

// PageCursor is the position after which a page of stacks starts: the
// namespace, the project and the name of the last stack of the page before.
type PageCursor struct {
	Namespace namespace.Name
	Project   ProjectName
	Name      Name
}

// PageQuery selects one page of the live stacks of the namespaces in
// Namespaces, ordered by namespace, project, then name. An empty Namespaces
// matches nothing. An empty Project matches every project. After keeps the
// stacks after the cursor in that order; nil starts at the first stack. Tag
// keeps the stacks that the tag filter matches; nil keeps every stack.
// Limit is the largest number of stacks to read: zero or less means one, and
// a value above MaxPageLimit means MaxPageLimit.
type PageQuery struct {
	After      *PageCursor
	Project    ProjectName
	Tag        *TagFilter
	Namespaces []namespace.Name
	Limit      int
}

// TagFilter selects the stacks that have the tag Name. When HasValue is true,
// the tag must also have the value Value, which can be empty. When HasValue
// is false, any value matches.
type TagFilter struct {
	Name     string
	Value    string
	HasValue bool
}

// Matches reports whether a stack with tags passes f. A nil f matches every
// stack.
func (f *TagFilter) Matches(tags map[string]string) bool {
	if f == nil {
		return true
	}

	value, ok := tags[f.Name]

	return ok && (!f.HasValue || value == f.Value)
}

// PageLimit returns the limit of q after the floor and the cap.
func (q PageQuery) PageLimit() int {
	return min(max(q.Limit, 1), MaxPageLimit)
}

// NamespaceRange is the part of a page in one namespace: the stacks of
// Namespace after AfterProject and AfterName, in the order of project, then
// name. With the project filter of the query, only AfterName counts.
type NamespaceRange struct {
	Namespace    namespace.Name
	AfterProject ProjectName
	AfterName    Name
}

// Ranges returns the part of the page of q in each namespace, in the order of
// the page: each namespace of q once, in byte order, from the
// namespace of the cursor on. A store reads each range with a range of its
// index on namespace, project and name, until the page is full.
func (q PageQuery) Ranges() []NamespaceRange {
	names := slices.Clone(q.Namespaces)
	slices.Sort(names)
	names = slices.Compact(names)

	out := make([]NamespaceRange, 0, len(names))

	for _, ns := range names {
		r := NamespaceRange{Namespace: ns, AfterProject: "", AfterName: ""}

		switch {
		case q.After == nil || ns > q.After.Namespace:
			// The range starts at the first stack of the namespace.
		case ns < q.After.Namespace:
			continue
		case q.Project == "":
			r.AfterProject, r.AfterName = q.After.Project, q.After.Name
		case q.After.Project == q.Project:
			r.AfterName = q.After.Name
		case q.After.Project > q.Project:
			continue
		default:
			// The cursor is before the project: the range starts at the
			// first stack of the project.
		}

		out = append(out, r)
	}

	return out
}

// CursorOf returns the cursor of the page that starts after st.
func CursorOf(st Stack) PageCursor {
	return PageCursor{Namespace: st.Ref.Namespace, Project: st.Ref.Project, Name: st.Ref.Stack}
}

// compareCursor orders c and d by namespace, project, then name, in byte
// order.
func compareCursor(c, d PageCursor) int {
	return cmp.Or(
		cmp.Compare(c.Namespace, d.Namespace), cmp.Compare(c.Project, d.Project), cmp.Compare(c.Name, d.Name),
	)
}

// PageStacks returns the page of q from all, in any order: the stacks of the
// namespaces, the project and the tag filter of q, after the cursor, ordered
// by namespace, project, then name in byte order. It defines what QueryStacks answers. A
// SQL store compares the names in the collation of its text columns, as
// ListStacks orders them; the conformance suite uses names on which both
// orders agree.
func PageStacks(all []Stack, q PageQuery) []Stack {
	out := make([]Stack, 0, min(len(all), q.PageLimit()))

	sorted := slices.Clone(all)
	slices.SortFunc(sorted, func(a, b Stack) int { return compareCursor(CursorOf(a), CursorOf(b)) })

	for i := range sorted {
		if len(out) == q.PageLimit() {
			break
		}

		st := sorted[i]

		switch {
		case !slices.Contains(q.Namespaces, st.Ref.Namespace):
		case q.Project != "" && st.Ref.Project != q.Project:
		case !q.Tag.Matches(st.Tags):
		case q.After != nil && compareCursor(CursorOf(st), *q.After) <= 0:
		default:
			out = append(out, st)
		}
	}

	return out
}

// ListPage returns the page of q from the stacks that list returns, with one
// call for each namespace of q. A store that holds its stacks in memory can
// answer QueryStacks with it and its own ListStacks.
func ListPage(ctx context.Context, list func(context.Context, Filter) ([]Stack, error), q PageQuery) ([]Stack, error) {
	var all []Stack

	for _, ns := range q.Namespaces {
		stacks, err := list(ctx, Filter{Namespace: ns, Project: q.Project})
		if err != nil {
			return nil, fmt.Errorf("list stacks in %q: %w", ns, err)
		}

		all = append(all, stacks...)
	}

	return PageStacks(all, q), nil
}
