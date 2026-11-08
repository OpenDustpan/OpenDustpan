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

// LimitGroups reads the access limits that a change of a group can lift. An
// access limit that names a group holds for the members of that group, so a
// change of the group can change the access that the limit gives or takes
// away.
type LimitGroups interface {
	// LimitLiftedBy returns the access limit that the change can lift for
	// the members of the group with the ID groupID. newName is the name
	// that the group takes in a rename, and "" for another change: a rename
	// removes the members from the old name and adds them to the new name,
	// so a limit that names either name can lift.
	//
	// It reports false and nil when no access limit of the installation
	// can lift for the change.
	//
	// It returns an error when it cannot read the group, when it cannot
	// read the access limits, or when a limit does not parse or holds a
	// membership test of principal that names no group. A caller refuses
	// the change then.
	LimitLiftedBy(ctx context.Context, groupID, newName string, change principal.GroupChange) (LimitRef, bool, error)
}
