// Package limitgroups reads the access limits that a change of the members
// of a group can lift. An access limit that names a group holds for the
// members of that group, so such a change needs a site administrator.
package limitgroups

import (
	"context"
	"errors"
	"fmt"

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

// Reader reads the access limit that a change of the members of a group can
// lift.
type Reader struct {
	store  gate.Store
	groups Directory
}

// New returns a Reader of the access limits of store and the groups of
// groups.
func New(store gate.Store, groups Directory) *Reader {
	return &Reader{store: store, groups: groups}
}

// LimitLiftedBy implements access.LimitGroups. The schema names a group in
// a policy by its name, so the reader compares the name of the group. It
// reports false and nil for a group that does not exist, and for a change
// that no access limit of any scope can lift.
//
// It returns an error when it cannot read the group, when it cannot read the
// access limits, or when a limit does not parse. A caller refuses the change
// then.
func (r *Reader) LimitLiftedBy(
	ctx context.Context, groupID string, change principal.GroupChange,
) (access.LimitRef, bool, error) {
	group, err := r.groups.GetGroup(ctx, groupID)
	if errors.Is(err, principal.ErrNotFound) {
		return access.LimitRef{ID: "", Scope: ""}, false, nil
	}

	if err != nil {
		return access.LimitRef{ID: "", Scope: ""}, false, fmt.Errorf("read the group %q: %w", groupID, err)
	}

	limits, err := r.store.AccessLimits().ListEveryScope(ctx)
	if err != nil {
		return access.LimitRef{ID: "", Scope: ""}, false, fmt.Errorf("read the access limits: %w", err)
	}

	for i := range limits {
		directions, err := cedargo.LimitGroupDirections(limits[i].Policy)
		if err != nil {
			return access.LimitRef{ID: "", Scope: ""}, false, fmt.Errorf(
				"read the access limit %q of %s: %w", limits[i].ID, limits[i].Scope, err,
			)
		}

		if !lifts(directions[group.Name], change) {
			continue
		}

		return access.LimitRef{ID: string(limits[i].ID), Scope: limits[i].Scope.String()}, true, nil
	}

	return access.LimitRef{ID: "", Scope: ""}, false, nil
}

// lifts reports whether the change can lift the forbid that directions
// names for one group. The delete of a group removes all its members, so it
// counts as a remove. The zero directions are of a group that the policy
// does not name: no change can lift it.
func lifts(directions cedargo.GroupDirections, change principal.GroupChange) bool {
	switch change {
	case principal.GroupChangeAddMember:
		return directions.Add
	case principal.GroupChangeRemoveMember, principal.GroupChangeDelete:
		return directions.Remove
	default:
		// Defense in depth: the service passes one of the three changes.
		// An unknown change counts as one that can lift a limit that names
		// the group, so a new change kind is refused until its rule lands.
		return directions.Add || directions.Remove
	}
}
