// Package limitgroups reads the access limits, gates and approval rules that
// a change of a group can widen access through. An access limit or a gate
// that names a group holds for the members of that group, and an approval
// rule names its approver groups by ID. So such a change needs a site
// administrator.
package limitgroups

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Directory reads a group by its ID. principal.Store and access.Directory
// meet it.
type Directory interface {
	// GetGroup returns the group with the ID id, or principal.ErrNotFound.
	GetGroup(ctx context.Context, id string) (principal.Group, error)
}

// Reader reads the access limit, gate or approval rule that a change of the
// members of a group can widen access through.
type Reader struct {
	store  gate.Store
	groups Directory
}

// New returns a Reader of the access limits, gates and approval rules of
// store and the groups of groups.
func New(store gate.Store, groups Directory) *Reader {
	return &Reader{store: store, groups: groups}
}

// LimitLiftedBy implements access.LimitGroups. The schema names a group in a
// policy by its name, so the reader compares the name of the group. A rename
// removes the members of the group from its old name and adds them to the
// name newName, so an object that names either name can lift. It reports
// false and nil for a group that does not exist, and for a change that no
// access limit, gate or approval rule of any scope can lift.
//
// It reads every gate, whatever its decision point, mode or state: each gate
// restricts its members at its own point, so a change that can lift any gate
// widens access. It reads the approval rules only for an add of a member: an
// add makes the member an approver, and a remove, a delete or a rename does
// not widen access through a rule that names the group by its ID.
//
// It returns an error when it cannot read the group, when it cannot read the
// access limits, gates or approval rules, or when a clause does not parse or
// names no group in a membership test of principal. A caller refuses the
// change then.
func (r *Reader) LimitLiftedBy(
	ctx context.Context, groupID, newName string, change principal.GroupChange,
) (access.LimitRef, bool, error) {
	group, err := r.groups.GetGroup(ctx, groupID)
	if errors.Is(err, principal.ErrNotFound) {
		return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, nil
	}

	if err != nil {
		return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, fmt.Errorf("read the group %q: %w", groupID, err)
	}

	ref, lifted, err := r.accessLimitLiftedBy(ctx, group.Name, newName, change)
	if err != nil || lifted {
		return ref, lifted, err
	}

	ref, lifted, err = r.gateLiftedBy(ctx, group.Name, newName, change)
	if err != nil || lifted {
		return ref, lifted, err
	}

	if change != principal.GroupChangeAddMember {
		return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, nil
	}

	return r.ruleLiftedBy(ctx, groupID)
}

// accessLimitLiftedBy returns the access limit that the change can lift for a
// group with the name name.
func (r *Reader) accessLimitLiftedBy(
	ctx context.Context, name, newName string, change principal.GroupChange,
) (access.LimitRef, bool, error) {
	limits, err := r.store.AccessLimits().ListEveryScope(ctx)
	if err != nil {
		return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, fmt.Errorf("read the access limits: %w", err)
	}

	for i := range limits {
		directions, directionErr := cedargo.LimitGroupDirections(limits[i].Policy)
		if directionErr != nil {
			return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, fmt.Errorf(
				"read the access limit %q of %s: %w", limits[i].ID, limits[i].Scope, directionErr,
			)
		}

		if !liftsNames(directions, name, newName, change) {
			continue
		}

		return access.LimitRef{
			ID: string(limits[i].ID), Scope: limits[i].Scope.String(), Kind: access.LimitKindAccessLimit,
		}, true, nil
	}

	return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, nil
}

// gateLiftedBy returns the gate that the change can lift for a group with the
// name name.
func (r *Reader) gateLiftedBy(
	ctx context.Context, name, newName string, change principal.GroupChange,
) (access.LimitRef, bool, error) {
	gates, err := r.store.Gates().ListEveryScope(ctx)
	if err != nil {
		return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, fmt.Errorf("read the gates: %w", err)
	}

	for i := range gates {
		directions, directionErr := cedargo.GateGroupDirections(gates[i].Condition, gates[i].Unless)
		if directionErr != nil {
			return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, fmt.Errorf(
				"read the gate %q of %s: %w", gates[i].ID, gates[i].Scope, directionErr,
			)
		}

		if !liftsNames(directions, name, newName, change) {
			continue
		}

		return access.LimitRef{
			ID: string(gates[i].ID), Scope: gates[i].Scope.String(), Kind: access.LimitKindGate,
		}, true, nil
	}

	return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, nil
}

// ruleLiftedBy returns the approval rule whose approvers hold the group with
// the ID groupID.
func (r *Reader) ruleLiftedBy(ctx context.Context, groupID string) (access.LimitRef, bool, error) {
	rules, err := r.store.ApprovalRules().ListEveryScope(ctx)
	if err != nil {
		return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, fmt.Errorf("read the approval rules: %w", err)
	}

	for i := range rules {
		if !slices.Contains(rules[i].Approvers.GroupIDs, groupID) {
			continue
		}

		return access.LimitRef{
			ID: string(rules[i].ID), Scope: rules[i].Scope.String(), Kind: access.LimitKindApprovalRule,
		}, true, nil
	}

	return access.LimitRef{ID: "", Scope: "", Kind: ""}, false, nil
}

// liftsNames reports whether change can lift the access limit or gate that
// directions names for the group that is named name. A rename removes the
// members from the old name and adds them to newName, so it can lift an
// object that names either name.
func liftsNames(
	directions map[string]cedargo.GroupDirections, name, newName string, change principal.GroupChange,
) bool {
	if change == principal.GroupChangeRename {
		return lifts(directions[name], principal.GroupChangeRemoveMember) ||
			lifts(directions[newName], principal.GroupChangeAddMember)
	}

	return lifts(directions[name], change)
}

// lifts reports whether the change can lift the access limit or gate that
// directions names for one group. The delete of a group removes all its
// members, so it counts as a remove. The zero directions are of a group that
// the object does not name: no change can lift it.
func lifts(directions cedargo.GroupDirections, change principal.GroupChange) bool {
	switch change {
	case principal.GroupChangeAddMember:
		return directions.Add
	case principal.GroupChangeRemoveMember, principal.GroupChangeDelete:
		return directions.Remove
	case principal.GroupChangeRename:
		// A rename removes the members from the name and adds them to
		// another, so either direction can lift the forbid.
		return lifts(directions, principal.GroupChangeRemoveMember) ||
			lifts(directions, principal.GroupChangeAddMember)
	default:
		// Defense in depth: the service passes one of the four changes.
		// An unknown change counts as one that can lift a limit that names
		// the group, so a new change kind is refused until its rule lands.
		return directions.Add || directions.Remove
	}
}
