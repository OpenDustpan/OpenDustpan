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

// ProtectedGroups reports for each group ID of ids whether the group is
// protected, as Protected reports it for one group: the same walk, the same
// rules and the same errors. It reads the grants of all the groups with one
// GrantsOfSubjects call. Each other read of the walk is kept for the rest of
// the call, so a page of groups costs about one read for each distinct object
// that the walks meet, not reads for each group.
//
// A group ID that is empty or malformed is ErrInvalid, and so is a list of
// more than principal.MaxLimit IDs. A read that fails, or a group that
// Protected cannot check, makes the call return no result and the error: the
// caller must not show a false flag for any group of the list.
func ProtectedGroups(ctx context.Context, cfg ReachConfig, ids []string) (map[string]bool, error) {
	if err := checkReachConfig(cfg); err != nil {
		return nil, err
	}

	if len(ids) > principal.MaxLimit {
		return nil, fmt.Errorf("%w: %d groups, at most %d", ErrInvalid, len(ids), principal.MaxLimit)
	}

	subjects := make([]Subject, 0, len(ids))
	seen := make(map[string]bool, len(ids))

	for _, id := range ids {
		// Defense in depth: GroupReachOf checks the ID too. This check stays
		// so that no ID reaches the batch read of the grants unchecked.
		if !validSubjectID(id) {
			return nil, fmt.Errorf("%w: group ID %q", ErrInvalid, id)
		}

		if !seen[id] {
			seen[id] = true
			subjects = append(subjects, Subject{Kind: SubjectGroup, ID: id})
		}
	}

	if len(subjects) == 0 {
		return map[string]bool{}, nil
	}

	grants, err := cfg.Grants.GrantsOfSubjects(ctx, subjects)
	if err != nil {
		return nil, fmt.Errorf("read grants of %d groups: %w", len(subjects), err)
	}

	cache := newReachCache(cfg)
	if err := cache.seedGrants(subjects, grants); err != nil {
		return nil, err
	}

	cached := cache.config()

	// The custom roles and the units of the grants of all the groups are
	// read once here, so that the walk of each group finds them kept.
	if err := prefetch(ctx, cached, grants); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(subjects))

	for _, subject := range subjects {
		protected, err := Protected(ctx, cached, subject.ID)
		if err != nil {
			return nil, fmt.Errorf("protected flag of group %q: %w", subject.ID, err)
		}

		out[subject.ID] = protected
	}

	return out, nil
}

// prefetch reads the custom roles and the units of grants through the cached
// ports of cfg, one read for each.
func prefetch(ctx context.Context, cfg ReachConfig, grants []Grant) error {
	var roles []string

	seenRoles := map[string]bool{}

	for i := range grants {
		id := grants[i].Role
		if _, builtIn := BuiltInRole(id); !builtIn && !seenRoles[id] {
			seenRoles[id] = true
			roles = append(roles, id)
		}
	}

	if len(roles) > 0 {
		if _, err := cfg.Roles.GetRoles(ctx, roles); err != nil {
			return fmt.Errorf("read roles: %w", err)
		}
	}

	if cfg.Units == nil {
		return nil
	}

	var units []string

	seenUnits := map[string]bool{}

	for i := range grants {
		if id := grants[i].Object.UnitID(); id != "" && !seenUnits[id] {
			seenUnits[id] = true
			units = append(units, id)
		}
	}

	if len(units) > 0 {
		if _, err := cfg.Units.UnitObjects(ctx, units); err != nil {
			return fmt.Errorf("read units of grants: %w", err)
		}
	}

	return nil
}

// reachCache keeps the reads of the walks of one ProtectedGroups call. Each
// method answers as the port of the same name, from the first answer of the
// port for the same input. It keeps no error: a read that failed is read
// again. It lives for one call only, so it never answers a read of another
// request.
type reachCache struct {
	cfg        ReachConfig
	grants     map[string][]Grant
	roles      map[string]Role
	noRoles    map[string]bool
	subjects   map[string][]Subject
	principals map[string]principal.Principal
	noPrincip  map[string]bool
	members    map[namespacePage]namespaceRows
	collection map[string]collectionAnswer
	units      map[string]identity.Object
	noUnits    map[string]bool
}

// namespacePage is the input of one ListNamespacePrincipals read.
type namespacePage struct {
	ns    namespace.Name
	query principal.Query
}

// namespaceRows is the answer of one ListNamespacePrincipals read.
type namespaceRows struct {
	rows  []principal.Principal
	total int64
}

// collectionAnswer is the answer of one CollectionID read.
type collectionAnswer struct {
	id    string
	found bool
}

func newReachCache(cfg ReachConfig) *reachCache {
	return &reachCache{
		cfg: cfg, grants: map[string][]Grant{}, roles: map[string]Role{}, noRoles: map[string]bool{},
		subjects: map[string][]Subject{}, principals: map[string]principal.Principal{}, noPrincip: map[string]bool{},
		members: map[namespacePage]namespaceRows{}, collection: map[string]collectionAnswer{},
		units: map[string]identity.Object{}, noUnits: map[string]bool{},
	}
}

// seedGrants keeps the grants of each subject of subjects, as GrantsOf
// answers them. A grant of a subject that the read did not ask for is
// ErrInvalid: the store answered for another subject, so the check fails
// closed.
func (c *reachCache) seedGrants(subjects []Subject, grants []Grant) error {
	for _, subject := range subjects {
		c.grants[subject.String()] = []Grant{}
	}

	for i := range grants {
		key := grants[i].Subject.String()

		// Defense in depth: the store reads only the subjects of the list.
		// This check stays so that a grant of another subject never counts
		// for a group of the list.
		kept, ok := c.grants[key]
		if !ok {
			return fmt.Errorf("%w: grant %s of subject %s that the read did not ask for",
				ErrInvalid, grants[i].ID, key)
		}

		c.grants[key] = append(kept, grants[i])
	}

	return nil
}

// config returns cfg with each port behind the cache. A nil Collections or
// Units port stays nil, because the walk reads nil as "no unit is in a
// collection" and "no unit is live".
func (c *reachCache) config() ReachConfig {
	out := ReachConfig{
		Grants:      cachedGrants{GrantLookups: c.cfg.Grants, c: c},
		Roles:       cachedRoles{RoleStore: c.cfg.Roles, c: c},
		Callers:     cachedCallers{c: c},
		Principals:  cachedPrincipals{c: c},
		Collections: nil,
		Units:       nil,
	}

	if c.cfg.Collections != nil {
		out.Collections = cachedCollections{UnitCollections: c.cfg.Collections, c: c}
	}

	if c.cfg.Units != nil {
		out.Units = cachedUnits{Units: c.cfg.Units, c: c}
	}

	return out
}

// cachedGrants is GrantLookups behind the cache.
type cachedGrants struct {
	GrantLookups

	c *reachCache
}

func (g cachedGrants) GrantsOf(ctx context.Context, subject Subject) ([]Grant, error) {
	key := subject.String()
	if kept, ok := g.c.grants[key]; ok {
		return slices.Clone(kept), nil
	}

	grants, err := g.GrantLookups.GrantsOf(ctx, subject)
	if err != nil {
		return nil, err
	}

	g.c.grants[key] = slices.Clone(grants)

	return grants, nil
}

// cachedRoles is RoleStore behind the cache. Only GetRoles is kept.
type cachedRoles struct {
	RoleStore

	c *reachCache
}

func (r cachedRoles) GetRoles(ctx context.Context, ids []string) ([]Role, error) {
	out := make([]Role, 0, len(ids))

	var missing []string

	for _, id := range ids {
		switch role, ok := r.c.roles[id]; {
		case ok:
			out = append(out, role)
		case r.c.noRoles[id]:
			// The role did not exist at the first read: its grants give nothing.
		default:
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		return out, nil
	}

	read, err := r.RoleStore.GetRoles(ctx, missing)
	if err != nil {
		return nil, err
	}

	for i := range read {
		r.c.roles[read[i].ID] = read[i]
	}

	for _, id := range missing {
		if _, ok := r.c.roles[id]; !ok {
			r.c.noRoles[id] = true
		}
	}

	return append(out, read...), nil
}

// cachedCallers is Callers behind the cache.
type cachedCallers struct {
	c *reachCache
}

func (k cachedCallers) SubjectsOf(ctx context.Context, subject string) ([]Subject, error) {
	if kept, ok := k.c.subjects[subject]; ok {
		return slices.Clone(kept), nil
	}

	subjects, err := k.c.cfg.Callers.SubjectsOf(ctx, subject)
	if err != nil {
		return nil, err
	}

	k.c.subjects[subject] = slices.Clone(subjects)

	return subjects, nil
}

// cachedPrincipals is PrincipalNamespaces behind the cache. It keeps
// principal.ErrNotFound as an answer, and no other error.
type cachedPrincipals struct {
	c *reachCache
}

func (p cachedPrincipals) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	if rec, ok := p.c.principals[id]; ok {
		return rec, nil
	}

	if p.c.noPrincip[id] {
		return principal.Principal{}, fmt.Errorf("principal %q: %w", id, principal.ErrNotFound)
	}

	rec, err := p.c.cfg.Principals.GetPrincipal(ctx, id)

	switch {
	case errors.Is(err, principal.ErrNotFound):
		p.c.noPrincip[id] = true

		return principal.Principal{}, err
	case err != nil:
		return principal.Principal{}, err
	default:
		p.c.principals[id] = rec

		return rec, nil
	}
}

func (p cachedPrincipals) ListNamespacePrincipals(
	ctx context.Context, ns namespace.Name, q principal.Query,
) ([]principal.Principal, int64, error) {
	key := namespacePage{ns: ns, query: q}
	if kept, ok := p.c.members[key]; ok {
		return slices.Clone(kept.rows), kept.total, nil
	}

	rows, total, err := p.c.cfg.Principals.ListNamespacePrincipals(ctx, ns, q)
	if err != nil {
		return nil, 0, err
	}

	p.c.members[key] = namespaceRows{rows: slices.Clone(rows), total: total}

	return rows, total, nil
}

// cachedCollections is UnitCollections behind the cache. Only CollectionID
// is kept.
type cachedCollections struct {
	UnitCollections

	c *reachCache
}

func (k cachedCollections) CollectionID(ctx context.Context, obj identity.Object) (string, bool, error) {
	key := obj.String()
	if kept, ok := k.c.collection[key]; ok {
		return kept.id, kept.found, nil
	}

	id, found, err := k.UnitCollections.CollectionID(ctx, obj)
	if err != nil {
		return "", false, err
	}

	k.c.collection[key] = collectionAnswer{id: id, found: found}

	return id, found, nil
}

// cachedUnits is Units behind the cache. Only UnitObjects is kept: a unit
// that the first read did not find stays not found for the call.
type cachedUnits struct {
	Units

	c *reachCache
}

func (u cachedUnits) UnitObjects(ctx context.Context, ids []string) (map[string]identity.Object, error) {
	out := make(map[string]identity.Object, len(ids))

	var missing []string

	for _, id := range ids {
		switch obj, ok := u.c.units[id]; {
		case ok:
			out[id] = obj
		case u.c.noUnits[id]:
			// The unit did not exist at the first read.
		default:
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		return out, nil
	}

	read, err := u.Units.UnitObjects(ctx, missing)
	if err != nil {
		return nil, err
	}

	for _, id := range missing {
		obj, ok := read[id]
		if !ok {
			u.c.noUnits[id] = true

			continue
		}

		u.c.units[id] = obj
		out[id] = obj
	}

	return out, nil
}
