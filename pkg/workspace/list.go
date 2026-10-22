package workspace

import (
	"strings"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxListLimit is the largest number of workspaces that one QueryWorkspaces
// call reads.
const MaxListLimit = 1000

// ListQuery selects one page of the live workspaces of a namespace, in the
// order of ListWorkspaces. A workspace matches when its name holds
// NameContains: the match is exact, with no change of case. An empty
// NameContains matches every workspace. Offset is the number of matches
// before the page: below zero means zero. Limit is the largest number of
// workspaces in the page: zero or less means one, and a value above
// MaxListLimit means MaxListLimit.
type ListQuery struct {
	Namespace    namespace.Name
	NameContains string
	Offset       int
	Limit        int
}

// ListPage is one page of workspaces and Total, the count of every workspace
// that matches the query, on every page.
type ListPage struct {
	Workspaces []Workspace
	Total      int
}

// PageOffset returns the offset of q after the floor.
func (q ListQuery) PageOffset() int {
	return max(q.Offset, 0)
}

// PageLimit returns the limit of q after the floor and the cap.
func (q ListQuery) PageLimit() int {
	return min(max(q.Limit, 1), MaxListLimit)
}

// Matches reports whether the name of w holds the search of q. It does not
// compare the namespace.
func (q ListQuery) Matches(w Workspace) bool {
	return strings.Contains(w.Ref.Name.String(), q.NameContains)
}

// PageWorkspaces returns the page of q from all, the workspaces of
// ListWorkspaces for the namespace of q, in their order. It defines what
// QueryWorkspaces answers, so a store that holds its workspaces in memory can
// use it.
func PageWorkspaces(all []Workspace, q ListQuery) ListPage {
	page := ListPage{Workspaces: []Workspace{}, Total: 0}

	offset, limit := q.PageOffset(), q.PageLimit()

	for i := range all {
		if !q.Matches(all[i]) {
			continue
		}

		if page.Total >= offset && len(page.Workspaces) < limit {
			page.Workspaces = append(page.Workspaces, all[i])
		}

		page.Total++
	}

	return page
}
