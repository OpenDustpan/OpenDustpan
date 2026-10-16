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

// ServicePrincipals lists the service principals of the installation, for
// the act-as edges of a reach. Both SQL stores implement it.
type ServicePrincipals interface {
	// ListServicePrincipals returns the IDs of every service principal that
	// exists, of the installation and of every namespace, ordered by
	// subject. A deleted principal is left out. It returns no sentinel
	// error.
	ListServicePrincipals(ctx context.Context) ([]string, error)
}

// ReachConfig holds the dependencies of ReachOf. Grants, Roles, Callers,
// Principals and Services are required.
type ReachConfig struct {
	Grants     GrantLookups
	Roles      RoleStore
	Callers    Callers
	Principals PrincipalNamespaces
	Services   ServicePrincipals
}

// Reach is the set of actions on objects that one principal can do: the
// union of the actions of its grants, of the grants of its groups, and of
// the grants of each service principal that it can act as. An act-as edge
// is a hold of principal:manage-tokens on a service principal. The reach
// follows those edges transitively and stops on a cycle.
type Reach struct {
	entries map[string]*reachEntry
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

// Has reports whether r holds action on obj: on obj itself, on the
// installation, or on the namespace of obj for a namespace, a collection
// or a unit inside it. A grant on the installation covers every object. A
// unit or a collection matches by its stable ID, so an object with none
// matches no grant on one.
func (r Reach) Has(action identity.Action, obj identity.Object) bool {
	if len(r.entries) == 0 {
		return false
	}

	if r.entryHas(reachTarget(obj), action) {
		return true
	}

	if r.entryHas(identity.ServerObject().String(), action) {
		return true
	}

	ns := obj.Namespace()
	if ns == "" || obj.IsPrincipal() {
		return false
	}

	kind, _ := KindOf(obj)
	if kind != ObjectNamespace && kind != ObjectCollection && kind != ObjectUnit {
		return false
	}

	return r.entryHas(identity.NamespaceObject(ns).String(), action)
}

// Covers reports whether r holds every action of other on every object of
// other. An empty reach covers only another empty reach.
func (r Reach) Covers(other Reach) bool {
	for key := range other.entries {
		e := other.entries[key]
		if e == nil {
			continue
		}

		for action := range e.actions {
			if !r.Has(action, e.object) {
				return false
			}
		}
	}

	return true
}

// ActionsOn returns the sorted actions that r holds on obj.
func (r Reach) ActionsOn(obj identity.Object) []identity.Action {
	seen := map[identity.Action]bool{}

	var out []identity.Action

	for key := range r.entries {
		e := r.entries[key]
		if e == nil {
			continue
		}

		for action := range e.actions {
			if !seen[action] && r.Has(action, obj) {
				seen[action] = true
				out = append(out, action)
			}
		}
	}

	sortActions(out)

	return out
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
// reach. A grant whose role no longer exists gives nothing.
//
// An act-as edge comes from a grant that holds principal:manage-tokens on
// a service principal directly, from a grant that holds it on the
// installation, which reaches every service principal, or from a grant
// that holds namespace:manage-principal-tokens on a namespace, which
// reaches the service principals of that namespace. Trust rules will add
// another edge source at trustRuleTargets.
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
	case cfg.Services == nil:
		return Reach{}, fmt.Errorf("%w: services", ErrMissingDependency)
	}

	w := reachWalker{
		cfg: cfg, visited: map[string]bool{}, queued: map[string]bool{}, queue: nil,
		grants: map[string]Grant{}, roles: map[string]Role{},
		namespaceBots: map[namespace.Name][]string{}, namespaces: map[namespace.Name]bool{},
		installation: false, installationID: false,
	}

	w.enqueue(id)

	for len(w.queue) > 0 {
		next := w.queue[0]
		w.queue = w.queue[1:]

		if err := w.visit(ctx, next); err != nil {
			return Reach{}, err
		}

		if err := w.expand(ctx); err != nil {
			return Reach{}, err
		}
	}

	return w.finish(ctx)
}

// reachWalker collects the grants of a reach breadth first.
type reachWalker struct {
	cfg            ReachConfig
	queue          []string
	visited        map[string]bool
	queued         map[string]bool
	grants         map[string]Grant
	roles          map[string]Role
	namespaceBots  map[namespace.Name][]string
	namespaces     map[namespace.Name]bool
	installation   bool
	installationID bool
}

// enqueue adds id to the queue once.
func (w *reachWalker) enqueue(id string) {
	if id == "" || w.visited[id] || w.queued[id] {
		return
	}

	w.queued[id] = true
	w.queue = append(w.queue, id)
}

// visit adds the grants of one principal and queues the principals that
// its new grants can act as. An unknown or deleted principal adds none.
func (w *reachWalker) visit(ctx context.Context, id string) error {
	w.visited[id] = true

	rec, err := w.cfg.Principals.GetPrincipal(ctx, id)
	if errors.Is(err, principal.ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read principal %q: %w", id, err)
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

// follow queues the act-as targets of grants: the service principals that
// a hold of principal:manage-tokens reaches.
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

			if obj.IsServer() {
				w.installation = true
			} else if obj.IsPrincipal() {
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

// expand queues the service principals of each installation-wide and
// namespace-wide act-as edge found so far.
func (w *reachWalker) expand(ctx context.Context) error {
	if w.installation && !w.installationID {
		w.installationID = true

		ids, err := w.cfg.Services.ListServicePrincipals(ctx)
		if err != nil {
			return fmt.Errorf("list service principals: %w", err)
		}

		for _, id := range ids {
			w.enqueue(id)
		}
	}

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

	for _, id := range trustRuleTargets() {
		w.enqueue(id)
	}

	return nil
}

// trustRuleTargets returns the principals that the trust rules of the
// installation let the reach act as. Trust rules are not stored yet, so it
// returns none. It is the place to add that edge source later.
func trustRuleTargets() []string {
	return nil
}

// namespacePrincipals returns the IDs of the principals of the namespace
// ns that exist, in subject order.
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
			out = append(out, rows[i].ID)
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
	out := map[string]Role{}

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

		if r, ok := w.roles[id]; ok {
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

	custom, err := w.cfg.Roles.GetRoles(ctx, missing)
	if err != nil {
		return nil, fmt.Errorf("read roles: %w", err)
	}

	for i := range custom {
		out[custom[i].ID] = custom[i]
		w.roles[custom[i].ID] = custom[i]
	}

	return out, nil
}

// finish builds the reach from the collected grants.
func (w *reachWalker) finish(ctx context.Context) (Reach, error) {
	var out Reach

	grants := make([]Grant, 0, len(w.grants))
	for id := range w.grants {
		grants = append(grants, w.grants[id])
	}

	roles, err := w.rolesOf(ctx, grants)
	if err != nil {
		return Reach{}, err
	}

	for i := range grants {
		role, ok := roles[grants[i].Role]
		if !ok {
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
			reachAdd(out, identity.PrincipalObject(id), identity.ActionPrincipalManageTokens)
		}
	}
}
