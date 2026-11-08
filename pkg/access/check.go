package access

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// principalReader reads one principal in the transaction of a write.
type principalReader interface {
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}

// errNoPrincipalReadInWrite marks a principal read that the transaction of a
// write cannot do.
var errNoPrincipalReadInWrite = errors.New("no principal read in the write")

// writePrincipals is the principal port of an Authorizer inside the
// transaction of a write. It reads one principal in that transaction, and
// never on another connection. With no reader, each read fails, so the check
// denies. A grant check lists no principal of a namespace, so a list fails.
type writePrincipals struct {
	reader principalReader
}

func (p writePrincipals) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	if p.reader == nil {
		return principal.Principal{}, fmt.Errorf("%w: principal %q", errNoPrincipalReadInWrite, id)
	}

	return p.reader.GetPrincipal(ctx, id)
}

func (writePrincipals) ListNamespacePrincipals(
	_ context.Context, ns namespace.Name, _ principal.Query,
) ([]principal.Principal, int64, error) {
	return nil, 0, fmt.Errorf("%w: principals of namespace %q", errNoPrincipalReadInWrite, ns.String())
}

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

// within returns a copy of s that reads the grants, the roles, the subjects
// of callers and the namespace of a principal with reads, in the transaction
// of a write, and a context with no request cache: a change that committed
// after the request read them is visible. The copy reads the chain of obj
// from chain, which the Service read before the write: the copy reads no unit
// or collection record, so the write uses no connection other than its own.
func (s *Service) within(
	ctx context.Context, reads GrantReads, obj identity.Object, chain []identity.Object,
) (*Service, context.Context) {
	st := readsStore{Store: s.store, reads: reads}

	principals := writePrincipals{reader: nil}
	if reader, ok := reads.(principalReader); ok {
		principals.reader = reader
	}

	authz := &Authorizer{
		store: st, callers: reads, collections: nil, units: nil, principals: principals,
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
		if err := s.checkUnitCollection(ctx, reads, obj, chain); err != nil {
			return err
		}

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

// ActsAs returns, sorted and each once, what the members of subject can
// act as through the current grants of subject, as r reads them. Each line
// is the wire form of the object of a grant, as the access review names
// it: "principal:<id>" for a service principal on which a grant gives
// principal:manage-tokens, the name of a namespace whose grant gives
// namespace:manage-principal-tokens, so each service principal of that
// namespace, and "@server" for a grant on the installation that gives
// principal:manage-tokens, so every service principal. It covers grants
// only. A workload trust rule gives no other act-as right to a member: a
// create of a rule needs principal:manage-tokens on its principal, which a
// grant gives. A store calls it with the reads of a membership write.
func ActsAs(ctx context.Context, r GrantReads, subject Subject) ([]string, error) {
	grants, err := r.GrantsOf(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("read grants of %s: %w", subject.String(), err)
	}

	roles, err := rolesOfGrants(ctx, r, grants)
	if err != nil {
		return nil, fmt.Errorf("read roles of %s: %w", subject.String(), err)
	}

	seen := map[string]bool{}

	for i := range grants {
		role, ok := roles[grants[i].Role]
		if ok && givesActAs(role, grants[i].Object) {
			seen[grants[i].Object.String()] = true
		}
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// givesActAs reports whether a grant of role on obj lets its holder make
// the tokens of a service principal: principal:manage-tokens on the
// installation or on a principal, and namespace:manage-principal-tokens on
// a namespace, as the Authorizer decides.
func givesActAs(role Role, obj identity.Object) bool {
	if isNamespaceGrant(obj) {
		return role.Has(identity.ActionNamespaceManagePrincipalTokens)
	}

	return (obj.IsServer() || obj.IsPrincipal()) && role.Has(identity.ActionPrincipalManageTokens)
}

// rolesOfGrants returns the role of each grant of grants that exists, by
// ID: the built-in roles from code, and the custom roles with r in one
// read.
func rolesOfGrants(ctx context.Context, r GrantReads, grants []Grant) (map[string]Role, error) {
	out := map[string]Role{}

	var custom []string

	for i := range grants {
		if role, ok := BuiltInRole(grants[i].Role); ok {
			out[role.ID] = role
		} else if !slices.Contains(custom, grants[i].Role) {
			custom = append(custom, grants[i].Role)
		}
	}

	roles, err := r.GetRoles(ctx, custom)
	if err != nil {
		return nil, err
	}

	for i := range roles {
		out[roles[i].ID] = roles[i]
	}

	return out, nil
}

// revokeCheck returns the check that a revoke of g by caller runs in its
// write: caller holds every action that g gives on its object, with the
// role of g as it is now, and still holds the manage-access action of that
// object, which the route authorized.
func (s *Service) revokeCheck(caller identity.Principal, g Grant, chain []identity.Object) GrantCheck {
	return func(ctx context.Context, reads GrantReads) error {
		if err := s.checkUnitCollection(ctx, reads, g.Object, chain); err != nil {
			return err
		}

		w, ctx := s.within(ctx, reads, g.Object, chain)

		if err := w.checkGrantHeld(ctx, caller, g); err != nil {
			return err
		}

		kind, _ := KindOf(g.Object)

		return w.authz.Authorize(ctx, caller, manageAccessAction(kind), g.Object)
	}
}

// checkUnitCollection refuses with ErrUnitMoved when obj is a unit and the
// collection that it is in now, as reads answers in the transaction of the
// write, is not the collection in chain: the chain that the Service read
// before the write, through the collection port. An Authorizer with no
// collection port puts no unit in a collection, so it checks nothing.
func (s *Service) checkUnitCollection(
	ctx context.Context, reads GrantReads, obj identity.Object, chain []identity.Object,
) error {
	if !obj.IsUnit() || obj.UnitID() == "" || s.authz.collections == nil {
		return nil
	}

	want := ""

	for i := range chain {
		if chain[i].IsCollection() {
			want = chain[i].CollectionID()
		}
	}

	got, found, err := reads.UnitCollectionID(ctx, obj.UnitID())
	if err != nil {
		return fmt.Errorf("read collection of unit in the write: %w", err)
	}

	if !found {
		got = ""
	}

	if got != want {
		return fmt.Errorf("%w: unit %q changed collection during the request; send the request again",
			ErrUnitMoved, obj.String())
	}

	return nil
}
