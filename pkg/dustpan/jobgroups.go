package dustpan

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errNoPrincipalStore reports a server whose store keeps no principals, so no
// group membership can be read.
var errNoPrincipalStore = errors.New("the store of this server keeps no principals")

// jobGroups is the job.GroupReader over the principal store. The gate of a job
// names its groups by ID, so the read compares IDs and never names.
type jobGroups struct {
	principals principal.Store
}

var _ job.GroupReader = jobGroups{principals: nil}

// MemberOfAny implements job.GroupReader. A principal that is disabled, or that
// no record names, is not a member of any group. Only an active membership
// counts: a suspended membership gives no rights, and the store ignores it.
// Any error of the store denies, also the refusal of too many group IDs.
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

	member, err := g.principals.IsActiveMemberOfAny(ctx, p.ID, groupIDs)
	if err != nil {
		return false, fmt.Errorf("check the active memberships of the approver: %w", err)
	}

	return member, nil
}
