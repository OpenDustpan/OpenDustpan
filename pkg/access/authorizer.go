package access

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Callers answers the subjects of a caller: what the authorizer reads of
// the principals. Both SQL stores implement it.
type Callers interface {
	// SubjectsOf returns the subjects of the principal that exists with the
	// subject subject: the principal and each group it is a current member
	// of, in one query. An unknown subject has none, with no error.
	SubjectsOf(ctx context.Context, subject string) ([]Subject, error)
}

// UnitCollections answers the collection of a unit (ADR 51). The collection
// package implements it. CollectionOf returns the name of the collection
// that holds unit, and false when the unit is in none, for example when it
// does not exist.
type UnitCollections interface {
	CollectionOf(ctx context.Context, unit identity.Object) (string, bool, error)
	// CollectionID returns the stable ID of the collection obj, and false when no
	// such collection exists. A grant on a collection is keyed by this ID.
	CollectionID(ctx context.Context, collection identity.Object) (string, bool, error)
}

// PrincipalNamespaces reads the principals of namespaces, for the tokens
// of a namespace. Both SQL stores implement it.
type PrincipalNamespaces interface {
	// GetPrincipal returns the principal with the ID id, or
	// principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
	// ListNamespacePrincipals returns the principals of the namespace ns
	// that exist, ordered by subject: the rows of q, with the subject as
	// the key.
	ListNamespacePrincipals(
		ctx context.Context, ns namespace.Name, q principal.Query,
	) ([]principal.Principal, int64, error)
}

// AuthorizerConfig holds the dependencies of an Authorizer.
type AuthorizerConfig struct {
	Store   Store
	Callers Callers
	// Collections answers the collection of a unit. Nil means no unit is in
	// a collection: a grant on a collection then covers the collection only.
	Collections UnitCollections
	// Units resolves the stable ID of a unit. Nil means that no unit has
	// one: a grant on a unit then matches nothing, and only the grants on
	// the objects that hold the unit apply.
	Units Units
	// Principals reads the principals of namespaces. Nil means no principal
	// belongs to a namespace for the Authorizer.
	Principals PrincipalNamespaces
}

// Authorizer decides with grants (ADR 52). It implements identity.Authorizer
// behind the action declarations of every route (ADR 17).
//
// One decision reads the subjects of the caller in one query, the grants on
// the chain of the object in one query, and the custom roles of those
// grants in one query. Within a context from WithRequestCache, the subjects
// and the roles are read once for each request. No cache lives across
// requests, so a change of a membership, a grant or a role takes effect on
// the next request.
type Authorizer struct {
	store       Store
	callers     Callers
	collections UnitCollections
	units       Units
	principals  PrincipalNamespaces
	// chains holds the chain of an object by its wire form, read before
	// the transaction of a write: the copy of an Authorizer that checks in
	// that transaction reads no other port. Nil reads each chain.
	chains map[string][]identity.Object
}

var _ identity.Authorizer = (*Authorizer)(nil)

// NewAuthorizer returns an Authorizer. Store and Callers are required.
func NewAuthorizer(cfg AuthorizerConfig) (*Authorizer, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: access store", ErrMissingDependency)
	case cfg.Callers == nil:
		return nil, fmt.Errorf("%w: callers", ErrMissingDependency)
	}

	return &Authorizer{
		store: cfg.Store, callers: cfg.Callers, collections: cfg.Collections, units: cfg.Units,
		principals: cfg.Principals, chains: nil,
	}, nil
}

// Authorize allows action on obj when a grant of one of the subjects of p,
// on obj or on an object that holds obj, gives a role with action. An
// administrator (the bootstrap admin token, ADR 6) can do every action. A
// self action needs only a signed-in principal. An action that no family
// holds, and a principal that is unknown or deleted, are denied.
func (a *Authorizer) Authorize(
	ctx context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	// The namespace service principal is decided before every other rule,
	// also before the administrator shortcut, and reads no store.
	if identity.IsNamespaceServiceSubject(p.Subject) {
		return authorizeNamespaceService(p, action, obj)
	}

	if p.Admin {
		return nil
	}

	family, known := FamilyOf(action)
	if !known {
		return deny(p, action, obj)
	}

	if family == FamilySelf {
		return nil
	}

	held, err := a.heldActions(ctx, p, obj)
	if err != nil {
		return err
	}

	if !held.has(action) {
		return deny(p, action, obj)
	}

	return nil
}

// authorizeNamespaceService decides an action of a principal with the subject
// of a namespace service principal. The principal holds one action,
// identity.ActionUnitRunPlan, on the units of its own namespace. Every other
// case is denied: another credential, the administrator flag, a malformed
// subject, another action, another namespace and an object that is not a
// unit.
func authorizeNamespaceService(p identity.Principal, action identity.Action, obj identity.Object) error {
	ns, valid := identity.NamespaceServiceOf(p.Subject)

	// Defense in depth: the principal API and the token services refuse this
	// subject too; this rule stays so that a principal that came from
	// another path never gets an action, also with Admin set.
	allowed := valid && p.IsNamespaceService() &&
		action == identity.ActionUnitRunPlan &&
		obj.IsUnit() && obj.Namespace() == ns

	if !allowed {
		return deny(p, action, obj)
	}

	return nil
}

// deny returns the error of a refused action.
func deny(p identity.Principal, action identity.Action, obj identity.Object) error {
	return &identity.DeniedError{Subject: p.Subject, Action: action, Object: obj}
}

// Access is what a principal can do on one object, and the grants that give
// it (ADR 52, decision 14).
type Access struct {
	Object  identity.Object
	Actions []identity.Action
	Grants  []Grant
}

// Explain answers the actions that p holds on obj and the grants that give
// them. Only a grant that gives at least one of the actions is in the
// answer: a grant whose role no longer exists gives none. An administrator
// holds every classified action through no grant.
func (a *Authorizer) Explain(ctx context.Context, p identity.Principal, obj identity.Object) (Access, error) {
	out := Access{Object: obj, Actions: nil, Grants: nil}

	// The namespace service principal holds no stored grant: it explains
	// no action, also with the administrator flag set.
	if identity.IsNamespaceServiceSubject(p.Subject) {
		return out, nil
	}

	if p.Admin {
		for action := range families {
			out.Actions = append(out.Actions, action)
		}

		sortActions(out.Actions)

		return out, nil
	}

	held, err := a.heldActions(ctx, p, obj)
	if err != nil {
		return Access{}, err
	}

	if _, err := a.currentUnits(ctx, held.grants); err != nil {
		return Access{}, err
	}

	out.Actions = held.actions()
	out.Grants = held.grants

	return out, nil
}

// held is the union of the actions of a set of grants.
type held struct {
	set    map[identity.Action]bool
	grants []Grant
}

func (h held) has(action identity.Action) bool {
	return h.set[action]
}

func (h held) actions() []identity.Action {
	out := make([]identity.Action, 0, len(h.set))
	for action := range h.set {
		out = append(out, action)
	}

	sortActions(out)

	return out
}

// Scope is the set of objects of one kind where a principal can do one
// action, as grants name them. All is true when a grant on the installation
// covers every object. It is the scope of the identity.Authorizer port.
type Scope = identity.Scope

// ScopeExact reports that the scope is exact: Authorize and ListObjects read
// the same grants, so a unit in the scope passes Authorize.
func (a *Authorizer) ScopeExact() bool {
	return true
}

// ListObjects returns the scope of the objects where p can do action: one
// query for the subjects, one for the custom roles and one for the grants
// of the subjects with a role that holds action. An administrator has every
// object.
func (a *Authorizer) ListObjects(ctx context.Context, p identity.Principal, action identity.Action) (Scope, error) {
	none := Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}

	// The namespace service principal has no scope here: the rule of its one
	// action is a decision on one object, not a list of grants.
	if identity.IsNamespaceServiceSubject(p.Subject) {
		return none, nil
	}

	if p.Admin {
		none = identity.Every()

		return none, nil
	}

	subjects, err := a.subjectsOf(ctx, p)
	if err != nil || len(subjects) == 0 {
		return none, err
	}

	out, err := a.scopeFor(ctx, subjects, action)
	if err != nil {
		return none, err
	}

	if action != identity.ActionPrincipalManageTokens || out.CoversEveryObject() {
		return out, nil
	}

	out, err = a.namespaceTokenPrincipals(ctx, subjects, out)
	if err != nil {
		return none, err
	}

	return out, nil
}

// scopeFor returns the scope of the grants with a role that holds action.
func (a *Authorizer) scopeFor(ctx context.Context, subjects []Subject, action identity.Action) (Scope, error) {
	none := Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}

	roles, err := a.rolesWith(ctx, action)
	if err != nil || len(roles) == 0 {
		return none, err
	}

	grants, err := a.store.GrantsWithRoles(ctx, subjects, roles)
	if err != nil {
		return none, fmt.Errorf("read grants: %w", err)
	}

	live, err := a.currentUnits(ctx, grants)
	if err != nil {
		return none, err
	}

	out := none

	for i := range grants {
		add, addErr := a.grantInScope(ctx, grants[i].Object, action, live)
		if addErr != nil {
			return none, addErr
		}

		if add {
			addToScope(&out, grants[i].Object)
		}
	}

	return out, nil
}

// grantInScope reports whether a grant on obj with action belongs in a
// scope, and checks that a grant on a unit or a collection still lives.
func (a *Authorizer) grantInScope(
	ctx context.Context, obj identity.Object, action identity.Action, live map[string]bool,
) (bool, error) {
	if !givenOn(action, obj) {
		return false, nil
	}

	ok, err := a.grantLive(ctx, obj, live)
	if err != nil {
		return false, err
	}

	return ok, nil
}

// namespaceTokenPrincipals adds the principals of each namespace where the
// caller holds namespace:manage-principal-tokens through a grant on that
// namespace. It lists all pages. With no principal port it adds none.
func (a *Authorizer) namespaceTokenPrincipals(ctx context.Context, subjects []Subject, out Scope) (Scope, error) {
	if a.principals == nil {
		return out, nil
	}

	roles, err := a.rolesWith(ctx, identity.ActionNamespaceManagePrincipalTokens)
	if err != nil || len(roles) == 0 {
		return out, err
	}

	grants, err := a.store.GrantsWithRoles(ctx, subjects, roles)
	if err != nil {
		return out, fmt.Errorf("read grants: %w", err)
	}

	seen := map[namespace.Name]bool{}

	for i := range grants {
		kind, _ := KindOf(grants[i].Object)
		if kind != ObjectNamespace {
			continue
		}

		if !givenOn(identity.ActionNamespaceManagePrincipalTokens, grants[i].Object) {
			continue
		}

		ns := grants[i].Object.Namespace()
		if seen[ns] {
			continue
		}

		seen[ns] = true

		var listErr error

		out, listErr = a.addNamespacePrincipals(ctx, ns, out)
		if listErr != nil {
			return out, listErr
		}
	}

	return out, nil
}

// addNamespacePrincipals adds every principal of the namespace ns to out.
func (a *Authorizer) addNamespacePrincipals(ctx context.Context, ns namespace.Name, out Scope) (Scope, error) {
	after := ""

	for {
		rows, _, err := a.principals.ListNamespacePrincipals(
			ctx, ns, principal.Query{After: after, Low: "", High: "", Limit: principal.MaxLimit},
		)
		if err != nil {
			return out, fmt.Errorf("list principals of namespace %q: %w", ns.String(), err)
		}

		if len(rows) == 0 {
			return out, nil
		}

		for i := range rows {
			obj, parseErr := identity.ParsePrincipalObject(rows[i].ID)
			if parseErr != nil {
				return out, parseErr
			}

			addToScope(&out, obj)
		}

		if len(rows) < principal.MaxLimit {
			return out, nil
		}

		after = rows[len(rows)-1].Subject
	}
}

// grantLive reports whether a grant on obj still gives something. The name
// of a unit that no longer exists may name a new unit now, and a collection
// that was deleted and created again under its name is a new collection, so
// the grants of the old one give nothing.
func (a *Authorizer) grantLive(ctx context.Context, obj identity.Object, live map[string]bool) (bool, error) {
	switch {
	case obj.IsUnit():
		return live[obj.UnitID()], nil
	case obj.IsCollection():
		return a.liveCollection(ctx, obj)
	default:
		return true, nil
	}
}

// liveCollection reports whether obj names the collection that holds its
// stable ID now. It is false when no collection port is configured, and when
// the name is gone or has another ID.
func (a *Authorizer) liveCollection(ctx context.Context, obj identity.Object) (bool, error) {
	if a.collections == nil {
		return false, nil
	}

	id, found, err := a.collections.CollectionID(ctx, obj)
	if err != nil {
		return false, fmt.Errorf("read collection %q: %w", obj.String(), err)
	}

	return found && id == obj.CollectionID(), nil
}

// addToScope puts obj in the set of its kind in s once. An object on the
// installation covers every object.
func addToScope(s *Scope, obj identity.Object) {
	kind, _ := KindOf(obj)

	switch kind {
	case ObjectInstallation:
		every := identity.Every()
		every.Namespaces = s.Namespaces
		every.Collections = s.Collections
		every.Units = s.Units
		every.Principals = s.Principals
		*s = every
	case ObjectNamespace:
		s.Namespaces = appendObject(s.Namespaces, obj)
	case ObjectCollection:
		s.Collections = appendObject(s.Collections, obj)
	case ObjectUnit:
		s.Units = appendObject(s.Units, obj)
	case ObjectPrincipal:
		s.Principals = appendObject(s.Principals, obj)
	default:
	}
}

// appendObject appends obj to objs once.
func appendObject(objs []identity.Object, obj identity.Object) []identity.Object {
	for i := range objs {
		o := objs[i]
		if o.String() == obj.String() {
			return objs
		}
	}

	return append(objs, obj)
}

// sortActions sorts actions by name.
func sortActions(actions []identity.Action) {
	slices.Sort(actions)
}

// heldActions returns the actions that the grants of p give on obj, through
// obj and every object that holds it. An unknown or deleted principal holds
// none. The self actions are not part of it.
func (a *Authorizer) heldActions(ctx context.Context, p identity.Principal, obj identity.Object) (held, error) {
	none := held{set: map[identity.Action]bool{}, grants: nil}

	subjects, err := a.subjectsOf(ctx, p)
	if err != nil || len(subjects) == 0 {
		return none, err
	}

	chain, err := a.chain(ctx, obj)
	if err != nil {
		return none, err
	}

	grants, err := a.store.GrantsOn(ctx, subjects, chain)
	if err != nil {
		return none, fmt.Errorf("read grants: %w", err)
	}

	roles, err := a.rolesOf(ctx, grants)
	if err != nil {
		return none, err
	}

	targetNS := namespaceOfChain(chain, obj)

	return heldFromGrants(grants, roles, targetNS), nil
}

// heldFromGrants returns the actions that grants give on their target: the
// union of the actions of each grant. A grant whose role no longer exists,
// or gives no action on its object, gives nothing and is not listed.
// targetNS is the namespace of the principal of a namespace, and empty for
// any other target.
func heldFromGrants(grants []Grant, roles map[string]Role, targetNS namespace.Name) held {
	out := held{set: map[identity.Action]bool{}, grants: nil}

	for i := range grants {
		role, ok := roles[grants[i].Role]
		if !ok {
			continue
		}

		if addHeldGrant(role, grants[i], targetNS, &out) {
			out.grants = append(out.grants, grants[i])
		}
	}

	return out
}

// addHeldGrant adds the actions that one grant gives to out, and reports
// whether it gives any. A grant on the namespace of a principal of a
// namespace gives no data action on the principal: it gives only
// principal:manage-tokens when its role holds
// namespace:manage-principal-tokens. targetNS is empty for any other
// target.
func addHeldGrant(role Role, g Grant, targetNS namespace.Name, out *held) bool {
	gives := false
	namespaced := targetNS != ""

	if targetNS == "" || !isNamespaceGrant(g.Object) {
		for _, action := range role.Actions {
			if givenOn(action, g.Object) {
				out.set[action] = true
				gives = true
			}
		}
	}

	if namespaced && givesNamespaceToken(role, g.Object, targetNS) {
		out.set[identity.ActionPrincipalManageTokens] = true
		gives = true
	}

	return gives
}

// namespaceOfChain returns the namespace of the principal that obj names,
// or empty when obj is not a principal of a namespace: the chain of such
// a principal is the principal, its namespace and the installation.
func namespaceOfChain(chain []identity.Object, obj identity.Object) namespace.Name {
	if !obj.IsPrincipal() {
		return ""
	}

	for i := range chain {
		kind, _ := KindOf(chain[i])
		if kind == ObjectNamespace {
			return chain[i].Namespace()
		}
	}

	return ""
}

// isNamespaceGrant reports whether obj is a grant on a namespace.
func isNamespaceGrant(obj identity.Object) bool {
	kind, _ := KindOf(obj)

	return kind == ObjectNamespace
}

// givesNamespaceToken reports whether a grant on grantObj with role gives
// principal:manage-tokens on a principal of the namespace targetNS: the
// role holds namespace:manage-principal-tokens, and the grant is on that
// namespace. A grant on the namespace gives no other action on the
// principal. On the installation, only principal:manage-tokens reaches the
// tokens of a principal.
func givesNamespaceToken(role Role, grantObj identity.Object, targetNS namespace.Name) bool {
	return role.Has(identity.ActionNamespaceManagePrincipalTokens) && isNamespaceGrant(grantObj) &&
		grantObj.Namespace() == targetNS
}

// givenOn reports whether a grant on obj gives action. A grant on the
// installation gives every action of its role. A grant on a principal gives
// the delegation actions of its role only. A grant on any other object
// gives the data actions of its role only. The Service refuses a role with
// another action on such an object, and this rule holds also when a role
// change races a grant.
func givenOn(action identity.Action, obj identity.Object) bool {
	family, _ := FamilyOf(action)

	switch kind, _ := KindOf(obj); kind {
	case ObjectInstallation:
		return true
	case ObjectPrincipal:
		return family == FamilyDelegation
	case ObjectNamespace, ObjectCollection, ObjectUnit:
		return family == FamilyData
	default:
		return false
	}
}

// principalNamespaceOf returns the namespace of the principal with the ID
// id, and true when it belongs to a namespace. An unknown principal has
// none, with no error. With no principal port, no principal belongs to a
// namespace.
func principalNamespaceOf(
	ctx context.Context, id string, principals PrincipalNamespaces,
) (namespace.Name, bool, error) {
	if principals == nil {
		return "", false, nil
	}

	p, err := principals.GetPrincipal(ctx, id)
	if errors.Is(err, principal.ErrNotFound) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("read principal %q: %w", id, err)
	}

	if p.Namespace == "" {
		return "", false, nil
	}

	return p.Namespace, true, nil
}

// subjectsOf returns the subjects of p: its principal and each of its
// groups, read once for each request. A principal that is unknown or
// deleted has none.
func (a *Authorizer) subjectsOf(ctx context.Context, p identity.Principal) ([]Subject, error) {
	if p.Subject == "" {
		return nil, nil
	}

	// Defense in depth: the callers above handle this subject before they
	// call here; this check stays so that a new caller never reads the
	// stores for a namespace service principal, which has no record.
	if identity.IsNamespaceServiceSubject(p.Subject) {
		return nil, nil
	}

	cache := cacheOf(ctx)
	if subjects, ok := cache.subjectsOf(p.Subject); ok {
		return subjects, nil
	}

	subjects, err := a.callers.SubjectsOf(ctx, p.Subject)
	if err != nil {
		return nil, fmt.Errorf("read subjects of caller: %w", err)
	}

	cache.keepSubjects(p.Subject, subjects)

	return subjects, nil
}

// chain returns obj and every object that holds it, innermost first: a unit,
// its collection, its namespace and the installation, a principal of a
// namespace with its namespace and the installation, or any other
// principal and the installation. The caller object holds nothing and is
// in no chain. An unknown principal keeps the principal and the
// installation.
func (a *Authorizer) chain(ctx context.Context, obj identity.Object) ([]identity.Object, error) {
	if chain, ok := a.chains[obj.String()]; ok {
		return chain, nil
	}

	return chainObjects(ctx, obj, a.collections, a.withUnitID, a.principals)
}

// chainObjects returns obj and every object that holds it, innermost first,
// for the authorizer and the reach. collections answers the collection of a
// unit, resolveUnit answers the stable ID of a unit, and principals answers
// the namespace of a principal of a namespace. Each answers nothing when it
// is nil, as in the configs.
func chainObjects(
	ctx context.Context,
	obj identity.Object,
	collections UnitCollections,
	resolveUnit func(context.Context, identity.Object) (identity.Object, error),
	principals PrincipalNamespaces,
) ([]identity.Object, error) {
	kind, ok := KindOf(obj)
	if !ok {
		return nil, nil
	}

	obj, err := stableIDOf(ctx, kind, obj, collections, resolveUnit)
	if err != nil {
		return nil, err
	}

	if kind == ObjectPrincipal {
		ns, found, nsErr := principalNamespaceOf(ctx, obj.PrincipalID(), principals)
		if nsErr != nil {
			return nil, nsErr
		}

		if found {
			return []identity.Object{obj, identity.NamespaceObject(ns), identity.ServerObject()}, nil
		}

		return []identity.Object{obj, identity.ServerObject()}, nil
	}

	out := []identity.Object{obj}

	col, inCollection, err := collectionOfUnitObject(ctx, kind, obj, collections)
	if err != nil {
		return nil, err
	}

	if inCollection {
		out = append(out, col)
	}

	if kind == ObjectCollection || kind == ObjectUnit {
		out = append(out, identity.NamespaceObject(obj.Namespace()))
	}

	if kind != ObjectInstallation {
		out = append(out, identity.ServerObject())
	}

	return out, nil
}

// stableIDOf returns obj with the stable ID of its unit or its collection,
// when kind is one of them. Any other object is returned as it is.
func stableIDOf(
	ctx context.Context,
	kind ObjectKind,
	obj identity.Object,
	collections UnitCollections,
	resolveUnit func(context.Context, identity.Object) (identity.Object, error),
) (identity.Object, error) {
	if kind == ObjectUnit {
		return resolveUnit(ctx, obj)
	}

	if kind == ObjectCollection {
		return collectionIDOf(ctx, obj, collections)
	}

	return obj, nil
}

// collectionOfUnitObject returns the collection of the unit obj, with its
// stable ID, and true when the unit is in one. It returns false for any
// other object, and when no collection port answers.
func collectionOfUnitObject(
	ctx context.Context, kind ObjectKind, obj identity.Object, collections UnitCollections,
) (identity.Object, bool, error) {
	if kind != ObjectUnit || collections == nil {
		return obj, false, nil
	}

	name, inCollection, err := collections.CollectionOf(ctx, obj)
	if err != nil {
		return obj, false, fmt.Errorf("read collection of unit: %w", err)
	}

	if !inCollection {
		return obj, false, nil
	}

	col, err := collectionIDOf(ctx, identity.CollectionObject(obj.Namespace(), name), collections)
	if err != nil {
		return obj, false, err
	}

	return col, true, nil
}

// rolesOf returns the roles that grants name, by ID: the built-in roles
// from code, and the custom roles in one query, read once for each request.
// A role that no longer exists is left out, so its grants give nothing.
func (a *Authorizer) rolesOf(ctx context.Context, grants []Grant) (map[string]Role, error) {
	cache := cacheOf(ctx)

	return rolesByID(ctx, grants, cache.role, cache.keepRole, a.store)
}

// role returns the built-in or custom role with the ID id.
func (a *Authorizer) role(ctx context.Context, id string) (Role, error) {
	if r, ok := BuiltInRole(id); ok {
		return r, nil
	}

	r, err := a.store.GetRole(ctx, id)
	if err != nil {
		return Role{}, fmt.Errorf("read role %q: %w", id, err)
	}

	return r, nil
}

// rolesWith returns the IDs of the built-in and custom roles that hold
// action.
func (a *Authorizer) rolesWith(ctx context.Context, action identity.Action) ([]string, error) {
	var out []string

	builtIn := BuiltInRoles()
	for i := range builtIn {
		if builtIn[i].Has(action) {
			out = append(out, builtIn[i].ID)
		}
	}

	custom, err := a.store.ListRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}

	for i := range custom {
		if custom[i].Has(action) {
			out = append(out, custom[i].ID)
		}
	}

	return out, nil
}

// reviewRoles returns the roles that put a grant on a container in an
// access-review export: each role that gives a data action or manages
// access. The map holds the roles that manage access.
func (a *Authorizer) reviewRoles(ctx context.Context) ([]string, map[string]bool, error) {
	var out []string

	grantAccess := map[string]bool{}

	custom, err := a.store.ListRoles(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list roles: %w", err)
	}

	all := append(BuiltInRoles(), custom...)
	for i := range all {
		manages := managesAccess(all[i])
		if manages {
			grantAccess[all[i].ID] = true
		}

		if manages || givesData(all[i]) {
			out = append(out, all[i].ID)
		}
	}

	return out, grantAccess, nil
}

// managesAccess reports whether r holds an action that changes who has
// access to an object: a grant, a custom role, a membership of a group
// that has access, or a grant on a namespace, a collection or a unit. The
// issue of the tokens of a service principal gives no grant, so it does
// not count: the export lists its holders apart, for each service
// principal.
func managesAccess(r Role) bool {
	return slices.ContainsFunc(r.Actions, func(a identity.Action) bool {
		return slices.Contains(accessManagementActions(), a)
	})
}

// accessManagementActions are the actions that managesAccess looks for.
func accessManagementActions() []identity.Action {
	return []identity.Action{
		identity.ActionServerManageAccess, identity.ActionServerManageRoles, identity.ActionServerManageGroups,
		identity.ActionNamespaceManageAccess, identity.ActionCollectionManageAccess, identity.ActionUnitManageAccess,
	}
}

// givesData reports whether r holds a data action.
func givesData(r Role) bool {
	for _, action := range r.Actions {
		if families[action] == FamilyData {
			return true
		}
	}

	return false
}

// withUnitID returns the unit obj with its stable ID, read once for each
// request. A unit that does not exist, or an Authorizer with no Units,
// keeps no ID: a grant on the unit then matches nothing.
func (a *Authorizer) withUnitID(ctx context.Context, obj identity.Object) (identity.Object, error) {
	if obj.UnitID() != "" || a.units == nil {
		return obj, nil
	}

	cache := cacheOf(ctx)
	if id, ok := cache.unitID(obj.String()); ok {
		return obj.WithUnitID(id), nil
	}

	id, found, err := a.units.UnitID(ctx, obj)
	if err != nil {
		return obj, fmt.Errorf("read unit ID: %w", err)
	}

	if !found {
		id = ""
	}

	cache.keepUnitID(obj.String(), id)

	return obj.WithUnitID(id), nil
}

// currentUnits replaces the object of each unit grant in grants with the
// current reference of its unit, so a list shows the name that the unit
// has now. A grant whose unit no longer exists keeps the name it had. It
// returns the stable IDs of the units that exist.
func (a *Authorizer) currentUnits(ctx context.Context, grants []Grant) (map[string]bool, error) {
	live := map[string]bool{}

	if a.units == nil {
		return live, nil
	}

	var ids []string

	for i := range grants {
		if id := grants[i].Object.UnitID(); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		return live, nil
	}

	current, err := a.units.UnitObjects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read units of grants: %w", err)
	}

	for i := range grants {
		if obj, ok := current[grants[i].Object.UnitID()]; ok {
			grants[i].Object = obj
			live[obj.UnitID()] = true
		}
	}

	return live, nil
}

// collectionIDOf returns the collection obj with its stable ID. A
// collection that already has one, that does not exist, or queried with no
// collection port, keeps what it has.
func collectionIDOf(
	ctx context.Context, obj identity.Object, collections UnitCollections,
) (identity.Object, error) {
	if obj.CollectionID() != "" || collections == nil {
		return obj, nil
	}

	id, found, err := collections.CollectionID(ctx, obj)
	if err != nil {
		return obj, fmt.Errorf("read collection ID: %w", err)
	}

	if !found {
		return obj, nil
	}

	return obj.WithCollectionID(id), nil
}
