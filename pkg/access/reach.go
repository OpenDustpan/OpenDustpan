package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// ServicePrincipals lists the service principals of the installation, for
// the act-as edges of a reach. Both SQL stores implement it.
type ServicePrincipals interface {
	// ListServicePrincipals returns the IDs of every service principal that
	// exists, of the installation and of every namespace, ordered by
	// subject. A deleted principal is left out. It returns no sentinel
	// error.
	ListServicePrincipals(ctx context.Context) ([]string, error)
}

// ReachConfig holds the dependencies of ReachOf. Grants, Roles, Callers
// and Principals are required. Collections answers the collection
// of a unit: nil means no unit is in a collection, so no grant on a
// collection gives anything. Units resolves the stable ID of a unit: nil
// means no grant on a unit gives anything.
type ReachConfig struct {
	Grants      GrantLookups
	Roles       RoleStore
	Callers     Callers
	Principals  PrincipalNamespaces
	Collections UnitCollections
	Units       Units
}

// Reach is the set of actions on objects that one principal can do: the
// union of the actions of its grants, of the grants of its groups, and of
// the grants of each service principal that it can act as. An act-as edge
// is a hold of principal:manage-tokens on a service principal, held
// directly or through namespace:manage-principal-tokens on the namespace
// of the principal. The reach follows the direct and namespace edges
// transitively and stops on a cycle. A hold of principal:manage-tokens on
// the installation reaches no service principal through that hold, so an
// account administrator holds no operator action through it.
type Reach struct {
	entries     map[string]*reachEntry
	collections UnitCollections
	units       Units
	principals  PrincipalNamespaces
}

// reachEntry is the actions that a reach holds on one object.
type reachEntry struct {
	object  identity.Object
	actions map[identity.Action]bool
}

// reachTarget returns the key of obj in a reach: unit:<stable ID> for a
// unit, collection:<stable ID> for a collection, and the wire form for
// every other object. A unit or a collection with no stable ID keys by its
// wire form and matches no grant, as in the store.
func reachTarget(obj identity.Object) string {
	if obj.IsUnit() {
		if obj.UnitID() == "" {
			return obj.String()
		}

		return "unit:" + obj.UnitID()
	}

	if obj.IsCollection() {
		if obj.CollectionID() == "" {
			return obj.String()
		}

		return "collection:" + obj.CollectionID()
	}

	return obj.String()
}

// Has reports whether r holds action on obj: on obj itself or on an object
// that holds it, along the chain unit, collection, namespace,
// installation, as the authorizer reads it. A grant on a deleted unit or on
// a collection with another stable ID gives nothing. It returns an error
// only when a resolver fails.
func (r Reach) Has(ctx context.Context, action identity.Action, obj identity.Object) (bool, error) {
	chain, err := r.chain(ctx, obj)
	if err != nil {
		return false, err
	}

	for i := range chain {
		if r.entryHas(reachTarget(chain[i]), action) {
			return true, nil
		}
	}

	return false, nil
}

// Covers reports whether r holds every action of other on every object of
// other. An empty reach covers only another empty reach. It returns an
// error only when a resolver fails.
func (r Reach) Covers(ctx context.Context, other Reach) (bool, error) {
	for key := range other.entries {
		e := other.entries[key]
		if e == nil {
			continue
		}

		for action := range e.actions {
			holds, err := r.Has(ctx, action, e.object)
			if err != nil {
				return false, err
			}

			if !holds {
				return false, nil
			}
		}
	}

	return true, nil
}

// HeldAction is one action of a reach on one object.
type HeldAction struct {
	Action identity.Action
	Object identity.Object
}

// ReachRefusal is a refusal of a reach check that carries the actions of the
// reach of a principal that the caller does not hold. The audit entry of the
// refusal reads them with errors.As, so its reason needs no parsing of text.
type ReachRefusal interface {
	error
	MissingActions() []HeldAction
}

// MaxReachReasonItems bounds the actions that a reach reason names. A longer
// list ends with the count of the actions that it leaves out.
const MaxReachReasonItems = 20

// MissingActionsReason returns the audit reason of a reach refusal: the
// missing actions with their objects, sorted as Missing returns them, after
// "outside the reach of the caller: ". It names at most MaxReachReasonItems
// actions and then the count of the rest, and it stays inside the limit of
// an audit reason.
func MissingActionsReason(missing []HeldAction) string {
	const prefix = "outside the reach of the caller: "

	parts := make([]string, 0, min(len(missing), MaxReachReasonItems))
	total := len(prefix)

	for i := range missing {
		if i == MaxReachReasonItems {
			break
		}

		part := string(missing[i].Action) + " on " + reasonObjectText(missing[i].Object)

		sep := ""
		if i > 0 {
			sep = ", "
		}

		rest := len(missing) - i - 1
		suffix := ""
		if rest > 0 {
			suffix = fmt.Sprintf(", and %d more", rest)
		}

		if total+len(sep)+len(part)+len(suffix) > audit.MaxReasonLen {
			break
		}

		parts = append(parts, part)
		total += len(sep) + len(part)
	}

	if len(parts) == len(missing) {
		return prefix + strings.Join(parts, ", ")
	}

	return prefix + strings.Join(parts, ", ") + fmt.Sprintf(", and %d more", len(missing)-len(parts))
}

// reasonObjectText names the object of an action in a reach reason, for
// example "namespace:team", "unit:team/web" and "principal:prn-x".
func reasonObjectText(obj identity.Object) string {
	switch {
	case obj.IsServer():
		return "the installation"
	case obj.IsCaller():
		return "the caller"
	case obj.IsPrincipal():
		return obj.String()
	case obj.IsCollection():
		return "collection:" + obj.String()
	case obj.IsUnit():
		return "unit:" + obj.Namespace().String() + "/" + obj.UnitName()
	default:
		return "namespace:" + obj.String()
	}
}

// Missing returns the actions of other that r does not hold, with the
// object of each, sorted by action and then by object. An empty result
// means that r covers other. It returns an error only when a resolver
// fails.
func (r Reach) Missing(ctx context.Context, other Reach) ([]HeldAction, error) {
	var out []HeldAction

	for key := range other.entries {
		e := other.entries[key]
		if e == nil {
			continue
		}

		for action := range e.actions {
			holds, err := r.Has(ctx, action, e.object)
			if err != nil {
				return nil, err
			}

			if !holds {
				out = append(out, HeldAction{Action: action, Object: e.object})
			}
		}
	}

	slices.SortFunc(out, func(a, b HeldAction) int {
		if c := strings.Compare(string(a.Action), string(b.Action)); c != 0 {
			return c
		}

		return strings.Compare(a.Object.String(), b.Object.String())
	})

	return out, nil
}

// ActionsOn returns the sorted actions that r holds on obj. It returns an
// error only when a resolver fails.
func (r Reach) ActionsOn(ctx context.Context, obj identity.Object) ([]identity.Action, error) {
	seen := map[identity.Action]bool{}

	var out []identity.Action

	for key := range r.entries {
		e := r.entries[key]
		if e == nil {
			continue
		}

		for action := range e.actions {
			holds, err := r.Has(ctx, action, obj)
			if err != nil {
				return nil, err
			}

			if !seen[action] && holds {
				seen[action] = true
				out = append(out, action)
			}
		}
	}

	sortActions(out)

	return out, nil
}

// entryHas reports whether the entry under key holds action. A missing or nil
// entry holds nothing.
func (r Reach) entryHas(key string, action identity.Action) bool {
	e, ok := r.entries[key]

	return ok && e != nil && e.actions[action]
}

// reachAdd puts action on obj in r.
func reachAdd(r *Reach, obj identity.Object, action identity.Action) {
	if r.entries == nil {
		r.entries = map[string]*reachEntry{}
	}

	key := reachTarget(obj)
	e, ok := r.entries[key]

	if !ok || e == nil {
		e = &reachEntry{object: obj, actions: map[identity.Action]bool{}}
		r.entries[key] = e
	}

	if e.actions == nil {
		e.actions = map[identity.Action]bool{}
	}

	e.actions[action] = true
}

// ReachOf returns the reach of the principal with the ID id: the actions
// of its grants, of the grants of its groups, and of the grants of each
// service principal that it can act as through principal:manage-tokens,
// followed transitively. An unknown or deleted principal has an empty
// reach. A grant whose role no longer exists gives nothing, as does a grant
// on a deleted unit or on a collection with another stable ID.
//
// A direct act-as edge and a namespace edge reach every target. A hold of
// principal:manage-tokens on the installation reaches no service principal
// through that hold: a principal with it reaches the actions of a service
// principal only through its own grants and its own edges. A disabled
// principal is never an act-as target: its tokens do not work.
//
// An act-as edge comes from a grant that holds principal:manage-tokens on
// a service principal directly, or from a grant that holds
// namespace:manage-principal-tokens on a namespace, which reaches the
// service principals of that namespace. A workload trust rule adds no edge:
// a create of a rule needs principal:manage-tokens on the principal of the
// rule, so a principal that can make a rule already reaches that principal
// through the grant that gives the action. The issuer of a rule is outside
// the installation and holds no grant.
func ReachOf(ctx context.Context, cfg ReachConfig, id string) (Reach, error) {
	switch {
	case cfg.Grants == nil:
		return Reach{}, fmt.Errorf("%w: grants", ErrMissingDependency)
	case cfg.Roles == nil:
		return Reach{}, fmt.Errorf("%w: roles", ErrMissingDependency)
	case cfg.Callers == nil:
		return Reach{}, fmt.Errorf("%w: callers", ErrMissingDependency)
	case cfg.Principals == nil:
		return Reach{}, fmt.Errorf("%w: principals", ErrMissingDependency)
	}

	w := newReachWalker(cfg, id)
	w.enqueue(id)

	if err := w.close(ctx); err != nil {
		return Reach{}, err
	}

	return w.finish(ctx)
}

// emptyReach returns the reach with no entries over cfg.
func emptyReach(cfg ReachConfig) Reach {
	return Reach{
		entries:     map[string]*reachEntry{},
		collections: cfg.Collections,
		units:       cfg.Units,
		principals:  cfg.Principals,
	}
}

// reachWalker collects the grants of a reach breadth first.
type reachWalker struct {
	cfg           ReachConfig
	root          string
	queue         []string
	visited       map[string]bool
	queued        map[string]bool
	disabled      map[string]bool
	grants        map[string]Grant
	roles         map[string]Role
	namespaceBots map[namespace.Name][]string
	namespaces    map[namespace.Name]bool
}

// newReachWalker returns a walker over cfg for the principal root.
func newReachWalker(cfg ReachConfig, root string) *reachWalker {
	return &reachWalker{
		cfg: cfg, root: root, queue: nil,
		visited: map[string]bool{}, queued: map[string]bool{}, disabled: map[string]bool{},
		grants: map[string]Grant{}, roles: map[string]Role{},
		namespaceBots: map[namespace.Name][]string{}, namespaces: map[namespace.Name]bool{},
	}
}

// enqueue adds id to the queue once.
func (w *reachWalker) enqueue(id string) {
	if id == "" || w.visited[id] || w.queued[id] {
		return
	}

	w.queued[id] = true
	w.queue = append(w.queue, id)
}

// close visits each queued principal and queues its act-as targets until
// nothing is left.
func (w *reachWalker) close(ctx context.Context) error {
	for len(w.queue) > 0 {
		next := w.queue[0]
		w.queue = w.queue[1:]

		if err := w.visit(ctx, next); err != nil {
			return err
		}

		if err := w.expand(ctx); err != nil {
			return err
		}
	}

	return nil
}

// visit adds the grants of one principal and queues the principals that
// its new grants can act as. An unknown or deleted principal adds none. A
// disabled principal other than the root adds none: its tokens do not work,
// so it is no act-as target.
func (w *reachWalker) visit(ctx context.Context, id string) error {
	w.visited[id] = true

	rec, err := w.cfg.Principals.GetPrincipal(ctx, id)
	if errors.Is(err, principal.ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read principal %q: %w", id, err)
	}

	if !rec.DisabledAt.IsZero() && id != w.root {
		w.disabled[id] = true

		return nil
	}

	subjects, err := w.cfg.Callers.SubjectsOf(ctx, rec.Subject)
	if err != nil {
		return fmt.Errorf("read subjects of caller: %w", err)
	}

	var fresh []Grant

	for _, subject := range subjects {
		grants, err := w.cfg.Grants.GrantsOf(ctx, subject)
		if err != nil {
			return fmt.Errorf("read grants of %s: %w", subject.String(), err)
		}

		for i := range grants {
			if _, done := w.grants[grants[i].ID]; done {
				continue
			}

			w.grants[grants[i].ID] = grants[i]
			fresh = append(fresh, grants[i])
		}
	}

	return w.follow(ctx, fresh)
}

// follow records the act-as targets of grants: the service principals that
// a hold of principal:manage-tokens reaches directly or through a
// namespace.
func (w *reachWalker) follow(ctx context.Context, grants []Grant) error {
	roles, err := w.rolesOf(ctx, grants)
	if err != nil {
		return err
	}

	for i := range grants {
		role, ok := roles[grants[i].Role]
		if !ok {
			continue
		}

		if role.Has(identity.ActionPrincipalManageTokens) {
			obj := grants[i].Object

			if obj.IsPrincipal() {
				w.enqueue(obj.PrincipalID())
			}
		}

		if role.Has(identity.ActionNamespaceManagePrincipalTokens) {
			if kind, _ := KindOf(grants[i].Object); kind == ObjectNamespace {
				w.namespaces[grants[i].Object.Namespace()] = true
			}
		}
	}

	return nil
}

// expand queues the service principals of each namespace-wide act-as edge
// found so far.
func (w *reachWalker) expand(ctx context.Context) error {
	for ns := range w.namespaces {
		if _, done := w.namespaceBots[ns]; done {
			continue
		}

		ids, err := w.namespacePrincipals(ctx, ns)
		if err != nil {
			return err
		}

		w.namespaceBots[ns] = ids

		for _, id := range ids {
			w.enqueue(id)
		}
	}

	return nil
}

// namespacePrincipals returns the IDs of the enabled principals of the
// namespace ns that exist, in subject order. A disabled principal is no
// act-as target.
func (w *reachWalker) namespacePrincipals(ctx context.Context, ns namespace.Name) ([]string, error) {
	var out []string

	after := ""

	for {
		rows, _, err := w.cfg.Principals.ListNamespacePrincipals(
			ctx, ns, principal.Query{After: after, Low: "", High: "", Limit: principal.MaxLimit},
		)
		if err != nil {
			return nil, fmt.Errorf("list principals of namespace %q: %w", ns.String(), err)
		}

		if len(rows) == 0 {
			return out, nil
		}

		for i := range rows {
			if rows[i].DisabledAt.IsZero() {
				out = append(out, rows[i].ID)
			}
		}

		if len(rows) < principal.MaxLimit {
			return out, nil
		}

		after = rows[len(rows)-1].Subject
	}
}

// rolesOf returns the roles of grants by ID: the built-in roles from code
// and the custom roles in one query. A role that no longer exists is left
// out, so its grants give nothing.
func (w *reachWalker) rolesOf(ctx context.Context, grants []Grant) (map[string]Role, error) {
	get := func(id string) (Role, bool) {
		r, ok := w.roles[id]

		return r, ok
	}
	keep := func(r Role) {
		w.roles[r.ID] = r
	}

	return rolesByID(ctx, grants, get, keep, w.cfg.Roles)
}

// finish builds the reach from the collected grants. A grant on a deleted
// unit or on a collection with another stable ID gives nothing.
func (w *reachWalker) finish(ctx context.Context) (Reach, error) {
	out := emptyReach(w.cfg)

	grants := make([]Grant, 0, len(w.grants))
	for id := range w.grants {
		grants = append(grants, w.grants[id])
	}

	roles, err := w.rolesOf(ctx, grants)
	if err != nil {
		return Reach{}, err
	}

	live, err := w.liveUnits(ctx, grants)
	if err != nil {
		return Reach{}, err
	}

	for i := range grants {
		role, ok := roles[grants[i].Role]
		if !ok {
			continue
		}

		gives, err := w.grantLive(ctx, grants[i].Object, live)
		if err != nil {
			return Reach{}, err
		}

		if !gives {
			continue
		}

		for _, action := range role.Actions {
			if givenOn(action, grants[i].Object) {
				reachAdd(&out, grants[i].Object, action)
			}
		}
	}

	w.deriveNamespaceTokens(&out, grants, roles)

	return out, nil
}

// liveUnits returns the stable IDs of the units of grants that exist. With
// no unit port no unit is live, so no grant on a unit gives anything.
func (w *reachWalker) liveUnits(ctx context.Context, grants []Grant) (map[string]bool, error) {
	live := map[string]bool{}

	if w.cfg.Units == nil {
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

	current, err := w.cfg.Units.UnitObjects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read units of grants: %w", err)
	}

	for id := range current {
		live[id] = true
	}

	return live, nil
}

// grantLive reports whether the grant on obj still gives something: a unit
// that exists, a collection that still holds its stable ID, or any other
// object.
func (w *reachWalker) grantLive(
	ctx context.Context, obj identity.Object, live map[string]bool,
) (bool, error) {
	switch {
	case obj.IsUnit():
		return live[obj.UnitID()], nil
	case obj.IsCollection():
		return w.liveCollection(ctx, obj)
	default:
		return true, nil
	}
}

// liveCollection reports whether obj names the collection that holds its
// stable ID now. It is false when no collection port answers, and when the
// name is gone or has another ID.
func (w *reachWalker) liveCollection(ctx context.Context, obj identity.Object) (bool, error) {
	if w.cfg.Collections == nil {
		return false, nil
	}

	id, found, err := w.cfg.Collections.CollectionID(ctx, obj)
	if err != nil {
		return false, fmt.Errorf("read collection %q: %w", obj.String(), err)
	}

	return found && id == obj.CollectionID(), nil
}

// deriveNamespaceTokens adds principal:manage-tokens on each service
// principal of a namespace where a grant holds
// namespace:manage-principal-tokens. A grant on the namespace gives no
// other action on the principal.
func (w *reachWalker) deriveNamespaceTokens(out *Reach, grants []Grant, roles map[string]Role) {
	namespaces := map[namespace.Name]bool{}

	for i := range grants {
		role, ok := roles[grants[i].Role]
		if !ok {
			continue
		}

		if role.Has(identity.ActionNamespaceManagePrincipalTokens) {
			if kind, _ := KindOf(grants[i].Object); kind == ObjectNamespace {
				namespaces[grants[i].Object.Namespace()] = true
			}
		}
	}

	for ns := range namespaces {
		for _, id := range w.namespaceBots[ns] {
			if !w.disabled[id] {
				reachAdd(out, identity.PrincipalObject(id), identity.ActionPrincipalManageTokens)
			}
		}
	}
}

// chain returns obj and every object that holds it, innermost first, as the
// authorizer reads it.
func (r Reach) chain(ctx context.Context, obj identity.Object) ([]identity.Object, error) {
	return chainObjects(ctx, obj, r.collections, r.withUnitID, r.principals)
}

// withUnitID returns the unit obj with its stable ID. A unit that already
// has one, that does not exist, or queried with no unit port, keeps what it
// has: a grant on the unit then matches nothing.
func (r Reach) withUnitID(ctx context.Context, obj identity.Object) (identity.Object, error) {
	if obj.UnitID() != "" || r.units == nil {
		return obj, nil
	}

	id, found, err := r.units.UnitID(ctx, obj)
	if err != nil {
		return obj, fmt.Errorf("read unit ID: %w", err)
	}

	if !found {
		return obj, nil
	}

	return obj.WithUnitID(id), nil
}
