package access

import (
	"context"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
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

	return &Authorizer{store: cfg.Store, callers: cfg.Callers, collections: cfg.Collections, units: cfg.Units}, nil
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

// deny returns the error of a refused action.
func deny(p identity.Principal, action identity.Action, obj identity.Object) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
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
// action, as grants name them (ADR 52). All is true when a grant on the
// installation covers every object. A unit is in the scope when it, its
// collection or its namespace is in a set. A consumer filters a list with
// the scope in its own query, never with one check for each object.
type Scope struct {
	Namespaces  []identity.Object
	Collections []identity.Object
	Units       []identity.Object
	All         bool
}

// ListObjects returns the scope of the objects where p can do action: one
// query for the subjects, one for the custom roles and one for the grants
// of the subjects with a role that holds action. An administrator has every
// object.
func (a *Authorizer) ListObjects(ctx context.Context, p identity.Principal, action identity.Action) (Scope, error) {
	none := Scope{Namespaces: nil, Collections: nil, Units: nil, All: false}

	if p.Admin {
		none.All = true

		return none, nil
	}

	subjects, err := a.subjectsOf(ctx, p)
	if err != nil || len(subjects) == 0 {
		return none, err
	}

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
		// The name of a unit that no longer exists may name a new unit now:
		// its grants give nothing.
		if grants[i].Object.IsUnit() && !live[grants[i].Object.UnitID()] {
			continue
		}

		if !givenOn(action, grants[i].Object) {
			continue
		}

		out.add(grants[i].Object)
	}

	return out, nil
}

// add puts obj in the set of its kind once. An object on the installation
// covers every object.
func (s *Scope) add(obj identity.Object) {
	kind, _ := KindOf(obj)

	switch kind {
	case ObjectInstallation:
		s.All = true
	case ObjectNamespace:
		s.Namespaces = appendObject(s.Namespaces, obj)
	case ObjectCollection:
		s.Collections = appendObject(s.Collections, obj)
	case ObjectUnit:
		s.Units = appendObject(s.Units, obj)
	default:
	}
}

// appendObject appends obj to objs once.
func appendObject(objs []identity.Object, obj identity.Object) []identity.Object {
	for _, o := range objs {
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

	out := held{set: map[identity.Action]bool{}, grants: nil}

	for i := range grants {
		role, ok := roles[grants[i].Role]
		if !ok {
			continue
		}

		gives := false

		for _, action := range role.Actions {
			if givenOn(action, grants[i].Object) {
				out.set[action], gives = true, true
			}
		}

		// A grant whose role no longer exists, or gives no action on its
		// object, gives nothing: an answer does not list it.
		if gives {
			out.grants = append(out.grants, grants[i])
		}
	}

	return out, nil
}

// givenOn reports whether a grant on obj gives action. A grant on the
// installation gives every action of its role. A grant on any other object
// gives the data actions of its role only. The Service refuses a role with
// another action below the installation, and this rule holds also when a
// role change races a grant.
func givenOn(action identity.Action, obj identity.Object) bool {
	if kind, _ := KindOf(obj); kind == ObjectInstallation {
		return true
	}

	family, _ := FamilyOf(action)

	return family == FamilyData
}

// subjectsOf returns the subjects of p: its principal and each of its
// groups, read once for each request. A principal that is unknown or
// deleted has none.
func (a *Authorizer) subjectsOf(ctx context.Context, p identity.Principal) ([]Subject, error) {
	if p.Subject == "" {
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
// its collection, its namespace and the installation. The caller object
// holds nothing and is in no chain.
func (a *Authorizer) chain(ctx context.Context, obj identity.Object) ([]identity.Object, error) {
	kind, ok := KindOf(obj)
	if !ok {
		return nil, nil
	}

	obj, err := a.withStableID(ctx, kind, obj)
	if err != nil {
		return nil, err
	}

	out := []identity.Object{obj}

	col, inCollection, err := a.collectionOfUnit(ctx, kind, obj)
	if err != nil {
		return nil, err
	}

	if inCollection {
		out = append(out, col)
	}

	if kind != ObjectInstallation && kind != ObjectNamespace {
		out = append(out, identity.NamespaceObject(obj.Namespace()))
	}

	if kind != ObjectInstallation {
		out = append(out, identity.ServerObject())
	}

	return out, nil
}

// withStableID returns obj with the stable ID of its unit or its collection,
// when kind is one of them. Any other object is returned as it is.
func (a *Authorizer) withStableID(ctx context.Context, kind ObjectKind, obj identity.Object) (identity.Object, error) {
	if kind == ObjectUnit {
		return a.withUnitID(ctx, obj)
	}

	if kind == ObjectCollection {
		return a.withCollectionID(ctx, obj)
	}

	return obj, nil
}

// collectionOfUnit returns the collection of the unit obj, with its stable ID,
// and true when the unit is in one. It returns false for any other object, and
// when no collection port is configured.
func (a *Authorizer) collectionOfUnit(
	ctx context.Context, kind ObjectKind, obj identity.Object,
) (identity.Object, bool, error) {
	if kind != ObjectUnit || a.collections == nil {
		return obj, false, nil
	}

	name, inCollection, err := a.collections.CollectionOf(ctx, obj)
	if err != nil {
		return obj, false, fmt.Errorf("read collection of unit: %w", err)
	}

	if !inCollection {
		return obj, false, nil
	}

	col, err := a.withCollectionID(ctx, identity.CollectionObject(obj.Namespace(), name))
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
	out := make(map[string]Role, len(grants))

	var missing []string

	for i := range grants {
		id := grants[i].Role
		if _, done := out[id]; done {
			continue
		}

		if r, ok := BuiltInRole(id); ok {
			out[id] = r

			continue
		}

		if r, ok := cache.role(id); ok {
			out[id] = r

			continue
		}

		if !slices.Contains(missing, id) {
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		return out, nil
	}

	custom, err := a.store.GetRoles(ctx, missing)
	if err != nil {
		return nil, fmt.Errorf("read roles: %w", err)
	}

	for i := range custom {
		out[custom[i].ID] = custom[i]
		cache.keepRole(custom[i])
	}

	return out, nil
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
// that has access, or a grant on a namespace, a collection or a unit. A
// service token gets no access without a grant, so
// server:manage-service-tokens does not count.
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

// withCollectionID returns the collection object with its stable ID. A
// collection that does not exist keeps an empty ID, so no grant matches it.
func (a *Authorizer) withCollectionID(ctx context.Context, obj identity.Object) (identity.Object, error) {
	if a.collections == nil {
		return obj, nil
	}

	id, found, err := a.collections.CollectionID(ctx, obj)
	if err != nil {
		return obj, fmt.Errorf("read collection ID: %w", err)
	}

	if !found {
		return obj, nil
	}

	return obj.WithCollectionID(id), nil
}
