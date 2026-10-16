package access

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
)

// readsStore is the Store of an Authorizer inside the transaction of a
// write. It reads the grants, the roles and where a role is granted with
// reads, in that transaction. The checks of a write read nothing else of
// the Store.
type readsStore struct {
	Store

	reads GrantReads
}

func (s readsStore) GrantsOn(ctx context.Context, subjects []Subject, objects []identity.Object) ([]Grant, error) {
	grants, err := s.reads.GrantsOn(ctx, subjects, objects)
	if err != nil {
		return nil, fmt.Errorf("read grants in the write: %w", err)
	}

	return grants, nil
}

func (s readsStore) GetRoles(ctx context.Context, ids []string) ([]Role, error) {
	roles, err := s.reads.GetRoles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read roles in the write: %w", err)
	}

	return roles, nil
}

func (s readsStore) GetRole(ctx context.Context, id string) (Role, error) {
	roles, err := s.GetRoles(ctx, []string{id})
	if err != nil {
		return Role{}, err
	}

	for i := range roles {
		if roles[i].ID == id {
			return roles[i], nil
		}
	}

	return Role{}, fmt.Errorf("%w: role %q", ErrNotFound, id)
}

func (s readsStore) RoleGrantedBelowInstallation(ctx context.Context, role string) (bool, error) {
	below, err := s.reads.RoleGrantedBelowInstallation(ctx, role)
	if err != nil {
		return false, fmt.Errorf("read grants of role in the write: %w", err)
	}

	return below, nil
}

func (s readsStore) RoleGrantedOnPrincipal(ctx context.Context, role string) (bool, error) {
	on, err := s.reads.RoleGrantedOnPrincipal(ctx, role)
	if err != nil {
		return false, fmt.Errorf("read grants of role in the write: %w", err)
	}

	return on, nil
}

// within returns a copy of s that reads the grants, the roles and the
// subjects of callers with reads, in the transaction of a write, and a
// context with no request cache: a change that committed after the request
// read them is visible. The copy reads the chain of obj from chain, which
// the Service read before the write: the copy reads no unit, collection or
// principal record, so the write uses no connection other than its own.
func (s *Service) within(
	ctx context.Context, reads GrantReads, obj identity.Object, chain []identity.Object,
) (*Service, context.Context) {
	st := readsStore{Store: s.store, reads: reads}

	authz := &Authorizer{
		store: st, callers: reads, collections: nil, units: nil, principals: nil,
		chains: map[string][]identity.Object{obj.String(): chain},
	}

	w := *s
	w.store = st
	w.authz = authz

	return &w, withoutRequestCache(ctx)
}

// grantCheck returns the check that a grant of the role with the ID role
// on obj, by granter, runs in its write: granter still holds the
// manage-access action of obj, which the route authorized, the role as it
// is now goes on obj, and granter holds every action of it there.
func (s *Service) grantCheck(
	granter identity.Principal, obj identity.Object, chain []identity.Object, role string,
) GrantCheck {
	return func(ctx context.Context, reads GrantReads) error {
		w, ctx := s.within(ctx, reads, obj, chain)

		r, err := w.GetRole(ctx, role)
		if err != nil {
			return err
		}

		kind, _ := KindOf(obj)
		if err := checkRoleOn(kind, r); err != nil {
			return err
		}

		if err := w.checkEscalation(ctx, granter, obj, r); err != nil {
			return err
		}

		return w.authz.Authorize(ctx, granter, manageAccessAction(kind), obj)
	}
}

// roleCheck returns the check that an update of the custom role to r, by
// caller, runs in its write: caller still holds server:manage-roles, which
// the route authorized, caller holds every action of r, and every
// action that the update takes away from the role as it is now, on the
// installation, and r still fits each object that the role is granted on.
func (s *Service) roleCheck(caller identity.Principal, r Role) GrantCheck {
	return func(ctx context.Context, reads GrantReads) error {
		server := identity.ServerObject()
		w, ctx := s.within(ctx, reads, server, []identity.Object{server})

		current, err := w.store.GetRole(ctx, r.ID)
		if err != nil {
			return err
		}

		if err := w.checkEscalation(ctx, caller, server, r); err != nil {
			return err
		}

		if err := w.checkEscalation(ctx, caller, server, removedActions(current, r)); err != nil {
			return err
		}

		if err := w.checkPlacement(ctx, r); err != nil {
			return err
		}

		return w.authz.Authorize(ctx, caller, identity.ActionServerManageRoles, server)
	}
}

// manageAccessAction returns the action that manages the grants on an
// object of kind: the action that the grant routes authorize on the object.
// The installation and a principal take server:manage-access.
func manageAccessAction(kind ObjectKind) identity.Action {
	if a, ok := manageAccessActions[kind]; ok {
		return a
	}

	return identity.ActionServerManageAccess
}

// manageAccessActions holds the manage-access action of each object kind
// below the installation.
//
//nolint:gochecknoglobals // A constant table of actions; Go has no constant maps.
var manageAccessActions = map[ObjectKind]identity.Action{
	ObjectNamespace:  identity.ActionNamespaceManageAccess,
	ObjectCollection: identity.ActionCollectionManageAccess,
	ObjectUnit:       identity.ActionUnitManageAccess,
}

// revokeCheck returns the check that a revoke of g by caller runs in its
// write: caller holds every action that g gives on its object, with the
// role of g as it is now, and still holds the manage-access action of that
// object, which the route authorized.
func (s *Service) revokeCheck(caller identity.Principal, g Grant, chain []identity.Object) GrantCheck {
	return func(ctx context.Context, reads GrantReads) error {
		w, ctx := s.within(ctx, reads, g.Object, chain)

		if err := w.checkGrantHeld(ctx, caller, g); err != nil {
			return err
		}

		kind, _ := KindOf(g.Object)

		return w.authz.Authorize(ctx, caller, manageAccessAction(kind), g.Object)
	}
}
