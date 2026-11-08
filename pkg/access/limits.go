package access

import (
	"context"

	"opendustpan.org/opendustpan/pkg/principal"
)

// LimitRef names one access limit: its ID and the text of its scope.
type LimitRef struct {
	// ID is the ID of the limit.
	ID string
	// Scope is the scope of the limit, as its key.
	Scope string
}

// LimitGroups reads the access limits that a change of the members of a
// group can lift. An access limit that names a group holds for the members
// of that group, so a change of its members can change the access that the
// limit gives or takes away.
type LimitGroups interface {
	// LimitLiftedBy returns the access limit that the change can lift for
	// the members of the group with the ID groupID. It reports false and
	// nil when no access limit of the installation can lift for the
	// change.
	//
	// It returns an error when it cannot read the group, when it cannot
	// read the access limits, or when a limit does not parse. A caller
	// refuses the change then.
	LimitLiftedBy(ctx context.Context, groupID string, change principal.GroupChange) (LimitRef, bool, error)
}
