package dustpan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/principal"
)

// The bounds of the read of the groups of one principal. The read stops at the
// first group that matches. A principal in more groups than the bound allows
// is not read to the end: the approval is refused (fail closed).
const (
	jobGroupsPage     = 200
	jobGroupsMaxPages = 50
)

// errNoPrincipalStore reports a server whose store keeps no principals, so no
// group membership can be read.
var errNoPrincipalStore = errors.New("the store of this server keeps no principals")

// errTooManyGroups reports a principal in more groups than one read covers.
var errTooManyGroups = errors.New("the principal is in more groups than one read covers")

// jobGroups is the job.GroupReader over the principal store. The gate of a job
// names its groups by ID, so the read compares IDs and never names.
type jobGroups struct {
	principals principal.Store
}

var _ job.GroupReader = jobGroups{principals: nil}

// MemberOfAny implements job.GroupReader. A principal that is disabled, or that
// no record names, is not a member of any group.
func (g jobGroups) MemberOfAny(ctx context.Context, subject string, groupIDs []string) (bool, error) {
	if g.principals == nil {
		return false, errNoPrincipalStore
	}

	p, err := g.principals.GetPrincipalBySubject(ctx, subject)
	if errors.Is(err, principal.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("get the principal of the approver: %w", err)
	}

	if !p.DisabledAt.IsZero() {
		return false, nil
	}

	want := make(map[string]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		want[id] = struct{}{}
	}

	after := ""

	for range jobGroupsMaxPages {
		rows, _, err := g.principals.ListGroupsOf(ctx, p.ID, principal.Query{
			After: after, Low: "", High: "", Limit: jobGroupsPage,
		})
		if err != nil {
			return false, fmt.Errorf("list the groups of the approver: %w", err)
		}

		for i := range rows {
			if _, ok := want[rows[i].ID]; ok {
				return true, nil
			}
		}

		if len(rows) < jobGroupsPage {
			return false, nil
		}

		// The key of a group in the list is its name in lower case.
		after = strings.ToLower(rows[len(rows)-1].Name)
	}

	return false, errTooManyGroups
}
