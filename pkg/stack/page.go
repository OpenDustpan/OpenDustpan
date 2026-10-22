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
// stacks after the cursor in that order; nil starts at the first stack.
// Limit is the largest number of stacks to read: zero or less means one, and
// a value above MaxPageLimit means MaxPageLimit.
type PageQuery struct {
	After      *PageCursor
	Project    ProjectName
	Namespaces []namespace.Name
	Limit      int
}

// PageLimit returns the limit of q after the floor and the cap.
func (q PageQuery) PageLimit() int {
	return min(max(q.Limit, 1), MaxPageLimit)
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
// namespaces and the project of q, after the cursor, ordered by namespace,
// project, then name in byte order. It defines what QueryStacks answers. A
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
