package access

import (
	"context"

	"opendustpan.org/opendustpan/pkg/principal"
)

// LimitKind is the kind of object that names a group and whose change can
// widen access.
type LimitKind string

const (
	// LimitKindAccessLimit is an access limit.
	LimitKindAccessLimit LimitKind = "access limit"
	// LimitKindGate is a gate.
	LimitKindGate LimitKind = "gate"
	// LimitKindApprovalRule is an approval rule.
	LimitKindApprovalRule LimitKind = "approval rule"
)

// LimitRef names one object that a change of a group can widen access
// through: its kind, its ID and the text of its scope.
type LimitRef struct {
	// ID is the ID of the object.
	ID string
	// Scope is the scope of the object, as its key.
	Scope string
	// Kind is the kind of the object.
	Kind LimitKind
}

// LimitGroups reads the objects that a change of a group can widen access
// through. An access limit that names a group holds for the members of that
// group, a gate can name one in its condition or its unless, and an approval
// rule names one of its approver groups by ID. So a change of the group can
// change the access that the object gives or takes away.
type LimitGroups interface {
	// LimitLiftedBy returns the access limit, gate or approval rule that
	// the change can widen access through for the members of the group with
	// the ID groupID. newName is the name that the group takes in a rename,
	// and "" for another change: a rename removes the members from the old
	// name and adds them to the new name, so an object that names either
	// name can lift.
	//
	// It reports false and nil when no such object of the installation
	// can lift for the change.
	//
	// It returns an error when it cannot read the group, when it cannot
	// read the access limits, gates or approval rules, or when a clause
	// does not parse or holds a membership test of principal that names no
	// group. A caller refuses the change then.
	LimitLiftedBy(ctx context.Context, groupID, newName string, change principal.GroupChange) (LimitRef, bool, error)
}
