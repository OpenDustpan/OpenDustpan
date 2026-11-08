package access

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

var _ principal.Guard = (*Service)(nil)

// ID prefixes. An ID is opaque: never parse it.
const (
	grantIDPrefix = GrantIDPrefix
	roleIDPrefix  = "rol-"
)

// GrantIDPrefix starts the ID of every grant.
const GrantIDPrefix = "grt-"

// Limits of the text fields of a custom role, in bytes.
const (
	maxRoleNameLength        = 64
	maxRoleDescriptionLength = 1000
)

// Page limits of the grant lists, as for the principal lists.
const (
	DefaultLimit = principal.DefaultLimit
	MaxLimit     = principal.MaxLimit
)

// Directory is what the service reads of the principals and groups, to check
// the subject of a grant and to list the service principals in a group for
// the audit entry of a grant to the group. principal.Store meets it.
type Directory interface {
	// GetPrincipal returns the principal with the ID id, or
	// principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
	// GetPrincipalBySubject returns the principal with the subject, or
	// principal.ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
	// GetGroup returns the group with the ID id, or principal.ErrNotFound.
	GetGroup(ctx context.Context, id string) (principal.Group, error)
	// ListMembers returns the members of the group groupID, ordered by
	// subject, or principal.ErrNotFound when the group does not exist.
	ListMembers(ctx context.Context, groupID string, q principal.Query) ([]principal.Member, int64, error)
}

// Namespaces is what the service reads of the namespaces: one namespace, so
// that a grant names one that exists. namespace.Store meets it.
type Namespaces interface {
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// ServiceConfig holds the dependencies of a Service.
type ServiceConfig struct {
	Store      Store
	Directory  Directory
	Namespaces Namespaces
	Authorizer *Authorizer
	// Reach holds the ports that the compare of a grant on a service
	// principal reads: the grants, the roles, the callers and the
	// principals. A grant of a role with a delegation action on a service
	// principal needs it: the reach of the principal must fit inside the
	// reach of the granter. A port that is nil refuses such a grant.
	Reach ReachConfig
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// NewID returns a new opaque ID with the prefix it takes. Nil means
	// random IDs.
	NewID func(prefix string) string
	// BootstrapAdmins are the subjects of the bootstrap admin tokens (ADR
	// 6). They hold every action with no grant, so an access-review export
	// shows one row for each.
	BootstrapAdmins []string
	// LimitGroups reads the access limits that a change of the members of
	// a group can lift. Nil keeps the check of a group to its grants: a
	// setup that keeps no access limits names no group.
	LimitGroups LimitGroups
}

// Service implements the role and grant use cases.
//
// Each change takes the audit entry of the caller: who acted, from where,
// through which channel, and the action. The Service sets the time from its
// own clock, the scope, the kind change, the outcome success and the record,
// and the store appends the entry in the transaction of the change.
type Service struct {
	store      Store
	directory  Directory
	namespaces Namespaces
	authz      *Authorizer
	reach      ReachConfig
	now        func() time.Time
	newID      func(prefix string) string
	bootstrap  []string
	limits     LimitGroups
}

// NewService returns a Service. Store, Directory, Namespaces and Authorizer
// are required.
func NewService(cfg ServiceConfig) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: access store", ErrMissingDependency)
	case cfg.Directory == nil:
		return nil, fmt.Errorf("%w: directory", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespaces", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	newID := cfg.NewID
	if newID == nil {
		newID = ident.NewID
	}

	return &Service{
		store: cfg.Store, directory: cfg.Directory, namespaces: cfg.Namespaces, authz: cfg.Authorizer,
		reach: cfg.Reach, now: now, newID: newID, bootstrap: slices.Clone(cfg.BootstrapAdmins), limits: cfg.LimitGroups,
	}, nil
}

// Authorizer returns the authorizer that the service checks grants with.
func (s *Service) Authorizer() *Authorizer {
	return s.authz
}

// Now returns the time of the Service clock, in UTC to the millisecond:
// the time a change and its audit entry carry.
func (s *Service) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// ListRoles returns the built-in roles, then the custom roles by name.
func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	custom, err := s.store.ListRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}

	return append(BuiltInRoles(), custom...), nil
}

// GetRole returns the built-in or custom role with the ID id.
func (s *Service) GetRole(ctx context.Context, id string) (Role, error) {
	return s.authz.role(ctx, id)
}

// RoleInput names the fields of a custom role.
type RoleInput struct {
	Name        string
	Description string
	Actions     []identity.Action
}

// CreateRole validates and stores a custom role, with the entry e. caller
// must hold every action of the role on the installation: a role is a grant
// waiting to happen, so it cannot hold more than its maker.
func (s *Service) CreateRole(
	ctx context.Context,
	e audit.Entry,
	caller identity.Principal,
	in RoleInput,
) (Role, error) {
	at := s.Now()
	r := Role{
		CreatedAt: at, UpdatedAt: at, ID: s.newID(roleIDPrefix), Name: in.Name,
		Description: in.Description, Actions: normalizeActions(in.Actions), BuiltIn: false,
	}

	if err := validateRole(r); err != nil {
		return Role{}, err
	}

	if err := s.checkEscalation(ctx, caller, identity.ServerObject(), r); err != nil {
		return Role{}, err
	}

	if err := s.store.CreateRole(ctx, r, change(e, at, RoleRef(r.ID))); err != nil {
		return Role{}, fmt.Errorf("create role %q: %w", in.Name, err)
	}

	return r, nil
}

// UpdateRole replaces the fields of the custom role with the ID id, with
// the entry e. A built-in role answers ErrBuiltIn. caller must hold every
// action of the new set on the installation, because each current grant of
// the role gives the new set at once. caller must also hold every action
// that the change takes away, because each current grant of the role loses
// it at once, as a revoke would. A role that a current grant gives on an
// object other than the installation keeps data actions only (ErrInUse).
// The store runs these checks in the transaction of the write.
func (s *Service) UpdateRole(
	ctx context.Context, e audit.Entry, caller identity.Principal, id string, in RoleInput,
) (Role, error) {
	if _, ok := BuiltInRole(id); ok {
		return Role{}, fmt.Errorf("%w: %q", ErrBuiltIn, id)
	}

	r, err := s.store.GetRole(ctx, id)
	if err != nil {
		return Role{}, fmt.Errorf("update role %q: %w", id, err)
	}

	r.Name = in.Name
	r.Description = in.Description
	r.Actions = normalizeActions(in.Actions)
	r.UpdatedAt = s.Now()

	if err := validateRole(r); err != nil {
		return Role{}, err
	}

	// The store runs the checks in the transaction of the write, so a
	// revoke, a membership change, a grant or a role update that commits
	// first is visible to them.
	if err := s.store.UpdateRole(ctx, r, change(e, r.UpdatedAt, RoleRef(id)), s.roleCheck(caller, r)); err != nil {
		return Role{}, fmt.Errorf("update role %q: %w", id, err)
	}

	return r, nil
}

// DeleteRole ends the custom role with the ID id, with the entry e. A
// built-in role answers ErrBuiltIn, and a role that a current grant holds
// ErrInUse.
func (s *Service) DeleteRole(ctx context.Context, e audit.Entry, id string) error {
	if _, ok := BuiltInRole(id); ok {
		return fmt.Errorf("%w: %q", ErrBuiltIn, id)
	}

	if err := s.store.DeleteRole(ctx, id, change(e, s.Now(), RoleRef(id))); err != nil {
		return fmt.Errorf("delete role %q: %w", id, err)
	}

	return nil
}

// normalizeActions returns actions sorted, without duplicates.
func normalizeActions(actions []identity.Action) []identity.Action {
	out := make([]identity.Action, 0, len(actions))

	for _, a := range actions {
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}

	sortActions(out)

	return out
}

// validateRole checks the name, the description and the actions of a custom
// role. A name holds 1 to 64 letters, digits, '-', '_' and '.', and is not
// the name of a built-in role. Each action is classified, and no action is
// a self action, which every signed-in principal holds anyway.
func validateRole(r Role) error {
	if !ident.Valid(r.Name, maxRoleNameLength) {
		return fmt.Errorf("%w: role name %q", ErrInvalid, r.Name)
	}

	if _, builtIn := BuiltInRole(strings.ToLower(r.Name)); builtIn {
		return fmt.Errorf("%w: %q is the name of a built-in role", ErrInvalid, r.Name)
	}

	if len(r.Description) > maxRoleDescriptionLength || strings.IndexFunc(r.Description, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: role description", ErrInvalid)
	}

	if len(r.Actions) == 0 {
		return fmt.Errorf("%w: a role needs at least one action", ErrInvalid)
	}

	for _, a := range r.Actions {
		family, known := FamilyOf(a)
		if !known || family == FamilySelf {
			return fmt.Errorf("%w: unknown action %q", ErrInvalid, a)
		}
	}

	return nil
}

// GrantInput names a new grant.
type GrantInput struct {
	Object  identity.Object
	Role    string
	Subject Subject
}

// Grant gives the role in to the subject in on the object in, for granter,
// with the entry e. It checks that the subject exists, that the namespace
// of the object exists, that a role with an operator or administration
// action goes on the installation only, that a grant on a principal names a
// service principal and a role with delegation actions only, and that
// granter holds every action of the role on the object, directly or through
// an object that holds it. A role with a delegation action also needs
// server:manage-access on the installation. The caller has already
// authorized the manage-access action of the object. The store runs the
// checks of the role and of the actions of granter in the transaction of
// the write.
//
// The audit entry of a grant to a service principal names, in the change
// "tokenManagers", every holder of principal:manage-tokens on that
// principal: each of them can act as the principal, with the new grant
// too. The audit entry of a grant to a group names, in one change
// "tokenManagers", the same list for each service principal that is a
// member of the group, as one line per principal in the form
// "<principal ID>: <holder>, <holder>", with the lines sorted by principal
// ID.
func (s *Service) Grant(ctx context.Context, e audit.Entry, granter identity.Principal, in GrantInput) (Grant, error) {
	role, err := s.GetRole(ctx, in.Role)
	if err != nil {
		return Grant{}, err
	}

	if in.Object, err = s.existingObject(ctx, in.Object); err != nil {
		return Grant{}, err
	}

	fence, err := s.checkGrant(ctx, in, role)
	if err != nil {
		return Grant{}, err
	}

	// Defense in depth: the check of the actions of granter runs again in
	// the transaction of the write. The reach of the principal is read
	// here, before the write: the reach reads the grants, the roles, the
	// callers and the principals of the whole installation.
	if reachErr := s.checkPrincipalReach(ctx, granter, in.Object, role); reachErr != nil {
		return Grant{}, reachErr
	}

	chain, err := s.authz.chain(ctx, in.Object)
	if err != nil {
		return Grant{}, err
	}

	if e, err = s.withTokenManagers(ctx, e, in.Subject); err != nil {
		return Grant{}, err
	}

	g := Grant{
		CreatedAt: s.Now(), Condition: nil, Object: in.Object, ID: s.newID(grantIDPrefix),
		Role: role.ID, CreatedBy: granter.Subject, Subject: in.Subject,
	}

	// The store checks the role and the actions of granter again in the
	// transaction of the write, so a revoke, a membership change or a role
	// update that commits first is visible to the check.
	check := s.grantCheck(granter, in.Object, chain, role.ID)

	if err := s.store.CreateGrant(ctx, g, fence, grantChange(e, g.CreatedAt, fence, GrantRef(g)), check); err != nil {
		return Grant{}, fmt.Errorf("create grant: %w", err)
	}

	return g, nil
}

// GetGrant returns the current grant with the ID id, or ErrNotFound.
func (s *Service) GetGrant(ctx context.Context, id string) (Grant, error) {
	g, err := s.store.GetGrant(ctx, id)
	if err != nil {
		return Grant{}, fmt.Errorf("get grant %q: %w", id, err)
	}

	return g, nil
}

// Revoke ends the grant with the ID id on obj for caller, with the entry e.
// A grant on another object answers ErrNotFound, so a caller cannot revoke
// through an object that it manages a grant that it does not manage. caller
// must hold every action that the grant gives on its object (ErrEscalation),
// as it must to make the grant. The caller has already authorized the
// manage-access action of obj.
func (s *Service) Revoke(
	ctx context.Context, e audit.Entry, caller identity.Principal, obj identity.Object, id string,
) (Grant, error) {
	g, err := s.store.GetGrant(ctx, id)
	if err != nil {
		return Grant{}, fmt.Errorf("revoke grant %q: %w", id, err)
	}

	if obj, err = s.existingObject(ctx, obj); err != nil {
		return Grant{}, err
	}

	if !sameTarget(g.Object, obj) {
		return Grant{}, fmt.Errorf("revoke grant %q on %q: %w", id, obj.String(), ErrNotFound)
	}

	return s.revokeStored(ctx, e, caller, g, id)
}

// RevokeByID ends the grant with the ID id, whatever its object, for
// caller, with the entry e. It reads the grant and revokes it on its own
// object, with every check of Revoke. An unknown ID answers ErrNotFound,
// and a grant that gives an action the caller does not hold answers
// ErrEscalation. The caller authorizes the manage-access action of the
// grant's object before this call.
func (s *Service) RevokeByID(ctx context.Context, e audit.Entry, caller identity.Principal, id string) (Grant, error) {
	g, err := s.store.GetGrant(ctx, id)
	if err != nil {
		return Grant{}, fmt.Errorf("revoke grant %q: %w", id, err)
	}

	return s.revokeStored(ctx, e, caller, g, id)
}

// PageRequest asks for one page of the grants on an object. Cursor is the
// opaque NextCursor of the page before, or empty for the first page, at most
// principal.MaxCursorLength bytes. Limit is 1 to MaxLimit. 0 means that the
// request names no limit, and reads DefaultLimit rows: the HTTP adapter
// refuses a limit of 0 that a request names. Holder, when it is not empty,
// keeps the grants of one subject (the text form, ErrInvalid otherwise).
// Role, when it is not empty, keeps the grants of one role.
type PageRequest struct {
	Cursor string
	Holder string
	Role   string
	Limit  int
}

// Page is one page of grants with their holders. Total is the count of
// every current grant on the object that matches the filters. NextCursor is
// empty on the last page.
type Page struct {
	Grants     []ListedGrant
	NextCursor string
	Total      int64
}

// ListGrants returns one page of the current grants on obj, ordered by
// subject then role.
func (s *Service) ListGrants(ctx context.Context, obj identity.Object, req PageRequest) (Page, error) {
	q, err := grantQuery(req)
	if err != nil {
		return Page{}, err
	}

	limit := q.Limit
	q.Limit++

	if obj, err = s.existingObject(ctx, obj); err != nil {
		return Page{}, err
	}

	listed, total, err := s.store.ListGrantsOn(ctx, obj, q)
	if err != nil {
		return Page{}, fmt.Errorf("list grants on %q: %w", obj.String(), err)
	}

	if err := s.currentListedUnits(ctx, listed); err != nil {
		return Page{}, err
	}

	out := Page{Grants: listed, NextCursor: "", Total: total}

	if len(listed) > limit {
		out.Grants = listed[:limit]
		last := out.Grants[limit-1].Grant
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.Subject.String() + "\x00" + last.Role))
	}

	return out, nil
}

// grantFilters checks the holder and role filters of req and sets them on
// q.
func grantFilters(q *GrantQuery, req PageRequest) error {
	if req.Holder != "" {
		holder, err := ParseSubject(req.Holder)
		if err != nil {
			return err
		}

		q.Subject = holder.String()
	}

	if req.Role != "" && !ident.Valid(req.Role, maxRoleFilterLength) {
		return fmt.Errorf("%w: role filter %q is not a role ID", ErrInvalid, req.Role)
	}

	q.Role = req.Role

	return nil
}

// maxRoleFilterLength bounds the role filter of a grant list, in bytes. A
// role ID is far shorter.
const maxRoleFilterLength = 128

// grantQuery validates req and returns the store query of its page.
func grantQuery(req PageRequest) (GrantQuery, error) {
	q := GrantQuery{AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: req.Limit}

	if err := grantFilters(&q, req); err != nil {
		return GrantQuery{}, err
	}

	switch {
	case q.Limit == 0:
		q.Limit = DefaultLimit
	case q.Limit < 1 || q.Limit > MaxLimit:
		return GrantQuery{}, fmt.Errorf("%w: limit %d is not 1 to %d", ErrInvalid, req.Limit, MaxLimit)
	default:
	}

	if req.Cursor == "" {
		return q, nil
	}

	if len(req.Cursor) > principal.MaxCursorLength {
		return GrantQuery{}, fmt.Errorf("%w: cursor longer than %d bytes", ErrInvalid, principal.MaxCursorLength)
	}

	raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)

	subject, role, found := strings.Cut(string(raw), "\x00")
	if err != nil || !found || !utf8.ValidString(subject+role) || strings.ContainsRune(role, 0) {
		return GrantQuery{}, fmt.Errorf("%w: cursor", ErrInvalid)
	}

	q.AfterSubject, q.AfterRole = subject, role

	return q, nil
}

// CreateNamespace stores ns with the entry e. When creator is a stored
// principal, it stores the grant of the namespace-admin role on ns to
// creator in the same transaction. The bootstrap admin token has no stored
// principal: it already holds every action, so it gets no grant.
func (s *Service) CreateNamespace(
	ctx context.Context,
	e audit.Entry,
	creator identity.Principal,
	ns namespace.Namespace,
) (*Grant, error) {
	record, err := s.directory.GetPrincipalBySubject(ctx, creator.Subject)

	switch {
	case errors.Is(err, principal.ErrNotFound) && creator.Admin:
		var none Grant // A grant with no ID stores the namespace alone.

		entry := change(e, s.Now(), "namespace:"+ns.Name.String())
		if createErr := s.store.CreateNamespaceWithGrant(ctx, ns, none, entry); createErr != nil {
			return nil, fmt.Errorf("create namespace %q: %w", ns.Name, createErr)
		}

		return nil, nil //nolint:nilnil // The bootstrap admin gets no grant, and nothing failed.
	case err != nil:
		return nil, fmt.Errorf("create namespace %q: %w", ns.Name, err)
	}

	g := Grant{
		CreatedAt: s.Now(), Condition: nil, Object: identity.NamespaceObject(ns.Name), ID: s.newID(grantIDPrefix),
		Role: RoleNamespaceAdmin, CreatedBy: creator.Subject,
		Subject: Subject{Kind: SubjectPrincipal, ID: record.ID},
	}

	if err := s.store.CreateNamespaceWithGrant(ctx, ns, g, change(e, g.CreatedAt, GrantRef(g))); err != nil {
		return nil, fmt.Errorf("create namespace %q: %w", ns.Name, err)
	}

	return &g, nil
}

// RoleRef returns the audit object of the role id: "role:<id>".
func RoleRef(id string) string {
	return "role:" + id
}

// GrantRef returns the audit object of g. It names the whole tuple, so an
// entry still says what a grant gave after a revoke:
// "grant:<id> <subject> <role> <object>".
func GrantRef(g Grant) string {
	return "grant:" + g.ID + " " + g.Subject.String() + " " + g.Role + " " + g.Object.String()
}

// GrantIDRef returns the audit object of the grant id when the grant is not
// read: "grant:<id>", or "grant:invalid" for a text that is not a grant ID,
// so request text never reaches the audit log.
func GrantIDRef(id string) string {
	if strings.HasPrefix(id, grantIDPrefix) && ident.Valid(id, maxGrantIDLength) {
		return "grant:" + id
	}

	return "grant:invalid"
}

// maxGrantIDLength bounds a grant ID, in bytes.
const maxGrantIDLength = 64

// change completes the entry of the caller for one change of the record
// object at the time at.
func change(e audit.Entry, at time.Time, object string) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = object

	return e
}

// grantChange completes the entry of the caller for one change of the
// grant record object at the time at, in the audit scope of fence: the
// namespace of the grant, or audit.ServerScope for the installation.
func grantChange(e audit.Entry, at time.Time, fence namespace.Fence, object string) audit.Entry {
	e = change(e, at, object)
	if fence.Namespace != "" {
		e.Namespace = fence.Namespace
	}

	return e
}

// CheckGroupGrants implements principal.Guard: caller must hold every action
// that the current grants of the group give, on each of their objects.
//
// An access limit of a scope that names the group holds for the members of
// the group, so the change can lift the limit for one member: an add to a
// group of an unless clause, a remove from a group of a when clause or of
// the principal scope. A rename removes the members from the old name and
// adds them to the new name, so it can lift a limit that names either name.
// The caller must be a site administrator then: the bootstrap administrator,
// or a caller whom the access decision allows every action of the
// site-administrator role on the installation. A change that can only add a
// forbid keeps the grants check, and a read or a parse that fails refuses
// the change.
func (s *Service) CheckGroupGrants(
	ctx context.Context, caller identity.Principal, groupID, newName string, change principal.GroupChange,
) error {
	if err := s.checkSubjectGrants(ctx, caller, Subject{Kind: SubjectGroup, ID: groupID}); err != nil {
		return err
	}

	if s.limits == nil {
		return nil
	}

	siteAdministrator, err := s.isSiteAdministrator(ctx, caller)
	if err != nil {
		return fmt.Errorf("%w: check the site administrator actions of the caller: %w", ErrEscalation, err)
	}

	if siteAdministrator {
		return nil
	}

	limit, lifted, err := s.limits.LimitLiftedBy(ctx, groupID, newName, change)
	if err != nil {
		return fmt.Errorf("%w: read the access limits that name the group %q: %w", ErrEscalation, groupID, err)
	}

	if !lifted {
		return nil
	}

	if !s.limitReadable(ctx, caller, limit) {
		return fmt.Errorf(
			"%w: an access limit that you cannot read names the group %q: only a site administrator can %s",
			ErrEscalation, groupID, groupChangeText(change),
		)
	}

	return fmt.Errorf(
		"%w: access limit %q of %s names the group %q: only a site administrator can %s",
		ErrEscalation, limit.ID, limit.Scope, groupID, groupChangeText(change),
	)
}

// groupChangeText returns the text of the change for the refusal of a group
// that an access limit names.
func groupChangeText(change principal.GroupChange) string {
	switch change {
	case principal.GroupChangeAddMember:
		return "add a member"
	case principal.GroupChangeRemoveMember:
		return "remove a member"
	case principal.GroupChangeDelete:
		return "delete the group"
	case principal.GroupChangeRename:
		return "rename the group"
	default:
		return "change the members"
	}
}

// CheckPrincipalGrants implements principal.Guard: caller must hold every
// action that the current grants of the principal give, on each of their
// objects. The delete of a principal also ends its memberships, and the
// enable restores them, so the grants of each of its current groups count
// too.
func (s *Service) CheckPrincipalGrants(ctx context.Context, caller identity.Principal, principalID string) error {
	if caller.Admin {
		return nil
	}

	p, err := s.directory.GetPrincipal(ctx, principalID)
	if errors.Is(err, principal.ErrNotFound) {
		// Nothing to take away or restore: the store answers the delete or the enable.
		return nil
	}

	if err != nil {
		return fmt.Errorf("read principal %q: %w", principalID, err)
	}

	subjects, err := s.authz.subjectsOf(
		ctx,
		identity.Principal{
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Subject:                p.Subject,
			DisplayName:            "",
			Admin:                  false,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			Kind:                   "",
			Credential:             "",
			JobID:                  "",
		},
	)
	if err != nil {
		return err
	}

	for _, subject := range subjects {
		if err := s.checkSubjectGrants(ctx, caller, subject); err != nil {
			return err
		}
	}

	return nil
}

// CheckGroupReach implements principal.GroupReach: caller must hold the
// reach of every service principal that the current grants of the group
// give. A membership gives the grants of the group, and a grant on a
// service principal with a delegation action lets its holder act as that
// principal, with every grant that it has now or gets later. It returns an
// error that wraps identity.ErrDenied when the reach of the caller does not
// hold every action, and when it cannot read what it needs.
func (s *Service) CheckGroupReach(ctx context.Context, caller identity.Principal, groupID string) error {
	if caller.Admin {
		return nil
	}

	grants, err := s.store.GrantsOf(ctx, Subject{Kind: SubjectGroup, ID: groupID})
	if err != nil {
		return fmt.Errorf("%w: read the grants of group %q: %w", identity.ErrDenied, groupID, err)
	}

	for i := range grants {
		role, err := s.GetRole(ctx, grants[i].Role)
		if errors.Is(err, ErrNotFound) {
			// A deleted custom role gives nothing, so it checks nothing.
			continue
		}

		if err != nil {
			return fmt.Errorf("%w: read the role %q: %w", identity.ErrDenied, grants[i].Role, err)
		}

		if err := s.checkPrincipalReach(ctx, caller, grants[i].Object, role); err != nil {
			return fmt.Errorf("%w: %w", identity.ErrDenied, err)
		}
	}

	return nil
}

// CheckMove runs the escalation check of a move. A grant on the target that is
// added after the check and before the move is written is not checked here.
// Only a caller with manage-access on the collection can add a grant to it, and
// that caller's own escalation check covers the grant, so the result is a
// normal grant on the collection and not a silent escalation.
//
// CheckMove refuses a move of the unit into the collection target unless the
// caller holds, on the unit, every action of every role that the target
// grants. A move gives the unit each grant of the target, so the check is the
// same one that a direct grant on the unit runs. It returns ErrNotFound when
// the unit or the target does not exist, and an error that wraps
// identity.ErrDenied and ErrEscalation when a grant of the target gives an
// action that the caller does not hold on the unit. A grant whose role no
// longer exists gives nothing, so it is skipped. On success it returns the
// stable ID of the target that it checked. The move is written with that ID,
// so a collection deleted and created again after the check is refused.
func (s *Service) CheckMove(
	ctx context.Context, caller identity.Principal, unit, target identity.Object,
) (string, error) {
	withID, err := s.existingObject(ctx, unit)
	if err != nil {
		return "", fmt.Errorf("move %s: %w", unit.String(), err)
	}

	targetWithID, err := s.existingObject(ctx, target)
	if err != nil {
		return "", fmt.Errorf("move %s into %s: %w", unit.String(), target.String(), err)
	}

	cursor := ""

	for {
		page, err := s.ListGrants(ctx, targetWithID, PageRequest{Cursor: cursor, Limit: MaxLimit, Holder: "", Role: ""})
		if err != nil {
			return "", fmt.Errorf("grants of %s: %w", target.String(), err)
		}

		for i := range page.Grants {
			if err := s.checkMoveGrant(ctx, caller, withID, page.Grants[i].Grant); err != nil {
				return "", err
			}
		}

		if page.NextCursor == "" {
			return targetWithID.CollectionID(), nil
		}

		cursor = page.NextCursor
	}
}

// isSiteAdministrator reports whether the access decision makes caller a
// site administrator: the bootstrap administrator, or a caller whom the
// decision allows every action of the site-administrator role on the
// installation. A caller that the decision refuses one action is not a site
// administrator. It returns the error of a decision that fails for another
// reason than a denial.
func (s *Service) isSiteAdministrator(ctx context.Context, caller identity.Principal) (bool, error) {
	if caller.Admin {
		return true, nil
	}

	for _, action := range builtInActions(RoleSiteAdministrator) {
		err := s.authz.Authorize(ctx, caller, action, identity.ServerObject())
		if err == nil {
			continue
		}

		if errors.Is(err, identity.ErrDenied) {
			return false, nil
		}

		return false, fmt.Errorf("decide %s on the installation: %w", action, err)
	}

	return true, nil
}

// limitReadable reports whether caller may read the gate configuration of
// the namespace of limit. The refusal of a change that can lift a limit
// names the limit, so it names it only when caller may read it. A scope
// that does not parse, and a scope with no namespace, name none that caller
// may read, so the text stays hidden.
func (s *Service) limitReadable(ctx context.Context, caller identity.Principal, limit LimitRef) bool {
	scope, err := gate.ParseScopeKey(limit.Scope)
	if err != nil {
		return false
	}

	ns := scope.Namespace()
	if ns == "" {
		return false
	}

	return s.authz.Authorize(ctx, caller, identity.ActionNamespaceReadGates, identity.NamespaceObject(ns)) == nil
}

// checkMoveGrant checks one grant of a move target against the unit it would
// reach. A role that no longer exists gives nothing, so it passes.
func (s *Service) checkMoveGrant(ctx context.Context, caller identity.Principal, unit identity.Object, g Grant) error {
	role, err := s.GetRole(ctx, g.Role)
	if errors.Is(err, ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("role of grant %s: %w", g.ID, err)
	}

	if err := s.checkEscalation(ctx, caller, unit, role); err != nil {
		return fmt.Errorf("move %s: %w: %w", unit.String(), identity.ErrDenied, err)
	}

	return nil
}

// revokeStored ends the stored grant g with the ID id for caller, with the
// entry e. It holds the checks that Revoke and RevokeByID share: the caller
// holds every action the grant gives, and the entry goes in the scope of
// the grant. The store runs the check of the caller's actions in the
// transaction of the revoke.
func (s *Service) revokeStored(
	ctx context.Context, e audit.Entry, caller identity.Principal, g Grant, id string,
) (Grant, error) {
	fence, err := s.fenceOf(ctx, g.Object)
	if err != nil {
		return Grant{}, fmt.Errorf("revoke grant %q: %w", id, err)
	}

	chain, err := s.authz.chain(ctx, g.Object)
	if err != nil {
		return Grant{}, err
	}

	// The store checks that caller holds every action of g in the
	// transaction of the revoke, so a revoke, a membership change or a role
	// update that commits first is visible to the check.
	check := s.revokeCheck(caller, g, chain)

	if err := s.store.RevokeGrant(ctx, id, fence, grantChange(e, s.Now(), fence, GrantRef(g)), check); err != nil {
		return Grant{}, fmt.Errorf("revoke grant %q: %w", id, err)
	}

	return g, nil
}

// checkGrant runs the checks of Grant that do not read the actions of the
// granter. It returns the fence of the namespace of the object, or the zero
// fence for the installation.
func (s *Service) checkGrant(ctx context.Context, in GrantInput, role Role) (namespace.Fence, error) {
	var none namespace.Fence

	kind, ok := KindOf(in.Object)
	if !ok {
		return none, fmt.Errorf("%w: object %q takes no grant", ErrInvalid, in.Object.String())
	}

	if err := s.checkSubject(ctx, in.Subject); err != nil {
		return none, err
	}

	if err := s.checkSubjectNamespace(ctx, in.Subject, in.Object); err != nil {
		return none, err
	}

	fence, err := s.fenceOf(ctx, in.Object)
	if err != nil {
		return none, fmt.Errorf("grant on %q: %w", in.Object.String(), err)
	}

	if kind == ObjectPrincipal {
		if err := s.checkPrincipalObject(ctx, in.Object); err != nil {
			return none, err
		}
	}

	if err := checkRoleOn(kind, role); err != nil {
		return none, err
	}

	return fence, nil
}

// checkRoleOn refuses role on an object of kind: a role with an action
// outside the data family on a namespace, a collection or a unit, and a
// role with no action or an action outside the delegation family on a
// principal.
func checkRoleOn(kind ObjectKind, role Role) error {
	switch kind {
	case ObjectInstallation:
	case ObjectPrincipal:
		if len(role.Actions) == 0 {
			return fmt.Errorf("%w: role %q holds no principal action; a grant on a principal needs one, "+
				"for example the role %s", ErrInvalid, role.ID, RoleTokenManager)
		}

		if a, ok := actionOutside(role, FamilyDelegation); ok {
			return fmt.Errorf("%w: role %q holds %s; a grant on a principal can give principal actions only, "+
				"for example the role %s", ErrInvalid, role.ID, a, RoleTokenManager)
		}
	case ObjectNamespace, ObjectCollection, ObjectUnit:
		if a, ok := actionOutside(role, FamilyData); ok {
			return fmt.Errorf("%w: role %q holds %s, which only a grant on the installation can give",
				ErrInvalid, role.ID, a)
		}
	default:
		return fmt.Errorf("%w: object kind %q takes no grant", ErrInvalid, kind)
	}

	return nil
}

// checkSubjectNamespace refuses a grant to a principal of a namespace on an
// object that is not that namespace or inside it (ErrInvalid). The store
// checks it again in the transaction of the grant.
func (s *Service) checkSubjectNamespace(ctx context.Context, subject Subject, obj identity.Object) error {
	if subject.Kind != SubjectPrincipal {
		return nil
	}

	p, err := s.directory.GetPrincipal(ctx, subject.ID)
	if err != nil {
		return fmt.Errorf("grant subject %s: %w", subject.String(), err)
	}

	if p.Namespace != "" && p.Namespace != obj.Namespace() {
		return fmt.Errorf("%w: %s is a principal of the namespace %q, and can only hold grants inside it",
			ErrInvalid, subject.String(), p.Namespace.String())
	}

	return nil
}

// checkPrincipalObject refuses a grant on obj, a principal, unless the
// principal is a service principal of the installation. It returns
// principal.ErrNotFound when the principal does not exist.
func (s *Service) checkPrincipalObject(ctx context.Context, obj identity.Object) error {
	p, err := s.directory.GetPrincipal(ctx, obj.PrincipalID())
	if err != nil {
		return fmt.Errorf("grant on %q: %w", obj.String(), err)
	}

	if p.Kind != principal.KindService {
		return fmt.Errorf("%w: principal %q is a %s principal; only a service principal takes a grant",
			ErrInvalid, p.ID, p.Kind)
	}

	if p.Namespace != "" {
		return fmt.Errorf("%w: principal %q belongs to the namespace %q; a grant on it is not available yet",
			ErrInvalid, p.ID, p.Namespace.String())
	}

	return nil
}

// checkDelegationGranter refuses a role with a delegation action unless
// granter holds server:manage-access on the installation. A delegation
// action lets its holder act as a service principal, with every grant that
// the principal has, so only a principal that manages every grant can give
// it.
func (s *Service) checkDelegationGranter(ctx context.Context, granter identity.Principal, role Role) error {
	if !delegationAction(role) {
		return nil
	}

	err := s.authz.Authorize(ctx, granter, identity.ActionServerManageAccess, identity.ServerObject())
	if errors.Is(err, identity.ErrDenied) {
		return fmt.Errorf("%w: role %q holds a principal action, which needs %s on the installation",
			ErrEscalation, role.ID, identity.ActionServerManageAccess)
	}

	return err
}

// delegationAction reports whether role holds an action of the delegation
// family, for example principal:manage-tokens.
func delegationAction(role Role) bool {
	return slices.ContainsFunc(role.Actions, func(a identity.Action) bool { return families[a] == FamilyDelegation })
}

// servicePrincipalsOfGroup returns the sorted IDs of the service
// principals that are members of the group groupID. It returns
// principal.ErrNotFound when the group does not exist.
func (s *Service) servicePrincipalsOfGroup(ctx context.Context, groupID string) ([]string, error) {
	var out []string

	after := ""

	for {
		members, _, err := s.directory.ListMembers(
			ctx,
			groupID,
			principal.Query{After: after, Low: "", High: "", Limit: principal.MaxLimit},
		)
		if err != nil {
			return nil, fmt.Errorf("list members of group %q: %w", groupID, err)
		}

		for i := range members {
			if members[i].Principal.Kind == principal.KindService {
				out = append(out, members[i].Principal.ID)
			}
		}

		if len(members) < principal.MaxLimit {
			break
		}

		after = members[len(members)-1].Principal.Subject
	}

	slices.Sort(out)

	return out, nil
}

// installationTokenHolders returns the sorted holders of
// principal:manage-tokens on the installation: the subjects of the bootstrap
// admin tokens and of the current grants on the installation with a role
// that holds the action.
func (s *Service) installationTokenHolders(ctx context.Context) ([]string, error) {
	roles, err := s.authz.rolesWith(ctx, identity.ActionPrincipalManageTokens)
	if err != nil {
		return nil, err
	}

	want := make(map[string]bool, len(roles))
	for _, r := range roles {
		want[r] = true
	}

	seen := make(map[string]bool, len(s.bootstrap))
	for _, b := range s.bootstrap {
		seen[b] = true
	}

	afterSubject, afterRole := "", ""

	for {
		listed, _, err := s.store.ListGrantsOn(ctx, identity.ServerObject(), GrantQuery{
			AfterSubject: afterSubject, AfterRole: afterRole, Subject: "", Role: "", Limit: MaxLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("list grants on %q: %w", identity.ServerObject().String(), err)
		}

		for i := range listed {
			if want[listed[i].Grant.Role] {
				seen[listed[i].Grant.Subject.String()] = true
			}
		}

		if len(listed) < MaxLimit {
			break
		}

		last := listed[len(listed)-1].Grant
		afterSubject, afterRole = last.Subject.String(), last.Role
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// directTokenHolders returns the subjects of the current grants on the
// service principal with the ID id. Every grant on a service principal holds
// principal:manage-tokens, so every subject is a holder.
func (s *Service) directTokenHolders(ctx context.Context, id string) ([]string, error) {
	obj := identity.PrincipalObject(id)
	var out []string

	afterSubject, afterRole := "", ""

	for {
		listed, _, err := s.store.ListGrantsOn(ctx, obj, GrantQuery{
			AfterSubject: afterSubject, AfterRole: afterRole, Subject: "", Role: "", Limit: MaxLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("list grants on %q: %w", obj.String(), err)
		}

		for i := range listed {
			out = append(out, listed[i].Grant.Subject.String())
		}

		if len(listed) < MaxLimit {
			break
		}

		last := listed[len(listed)-1].Grant
		afterSubject, afterRole = last.Subject.String(), last.Role
	}

	return out, nil
}

// checkNamespaceTokenGranter refuses a role with
// namespace:manage-principal-tokens unless granter holds every data action
// on the namespace of obj, or server:manage-access on the installation. A
// grant on the installation needs server:manage-access. The action reaches
// the tokens of the principals of one namespace, so only a principal with
// the whole data access of that namespace can give it.
func (s *Service) checkNamespaceTokenGranter(
	ctx context.Context, granter identity.Principal, obj identity.Object, role Role,
) error {
	if !role.Has(identity.ActionNamespaceManagePrincipalTokens) {
		return nil
	}

	if obj.IsServer() || obj.Namespace() == "" {
		err := s.authz.Authorize(ctx, granter, identity.ActionServerManageAccess, identity.ServerObject())
		if errors.Is(err, identity.ErrDenied) {
			return fmt.Errorf("%w: role %q holds %s, which needs %s on the installation",
				ErrEscalation, role.ID, identity.ActionNamespaceManagePrincipalTokens,
				identity.ActionServerManageAccess)
		}

		return err
	}

	if err := s.authz.Authorize(ctx, granter, identity.ActionServerManageAccess, identity.ServerObject()); err == nil {
		return nil
	} else if !errors.Is(err, identity.ErrDenied) {
		return err
	}

	held, err := s.authz.heldActions(ctx, granter, identity.NamespaceObject(obj.Namespace()))
	if err != nil {
		return err
	}

	for _, a := range Actions(FamilyData) {
		if !held.has(a) {
			return fmt.Errorf("%w: role %q holds %s, which needs every data action on namespace %q or %s "+
				"on the installation",
				ErrEscalation, role.ID, identity.ActionNamespaceManagePrincipalTokens, obj.Namespace().String(),
				identity.ActionServerManageAccess)
		}
	}

	return nil
}

// withTokenManagers returns e with the change "tokenManagers" when subject
// is a service principal: the sorted holders of principal:manage-tokens on
// it. When subject is a group, it returns e with one change "tokenManagers"
// that lists each service principal member and its holders, as lines sorted
// by principal ID, one line per principal in the form
// "<principal ID>: <holder>, <holder>". It returns e as it is for any other
// subject.
func (s *Service) withTokenManagers(ctx context.Context, e audit.Entry, subject Subject) (audit.Entry, error) {
	switch subject.Kind {
	case SubjectPrincipal:
		p, err := s.directory.GetPrincipal(ctx, subject.ID)
		if err != nil {
			return e, fmt.Errorf("grant subject %s: %w", subject.String(), err)
		}

		if p.Kind != principal.KindService {
			return e, nil
		}

		holders, err := s.TokenManagers(ctx, p.ID)
		if err != nil {
			return e, err
		}

		e.Changes = append(slices.Clone(e.Changes), audit.Change{
			Key: "tokenManagers", Old: nil, New: strings.Join(holders, ", "), Layer: "", TakesEffect: "",
		})

		return e, nil
	case SubjectGroup:
		return s.groupTokenManagers(ctx, e, subject)
	default:
		return e, nil
	}
}

// groupTokenManagers returns e with one change "tokenManagers" that lists
// each service principal member of subject and its holders, as lines sorted
// by principal ID, one line per principal in the form
// "<principal ID>: <holder>, <holder>". It returns e as it is when the
// group has no service principal members.
func (s *Service) groupTokenManagers(
	ctx context.Context, e audit.Entry, subject Subject,
) (audit.Entry, error) {
	ids, err := s.servicePrincipalsOfGroup(ctx, subject.ID)
	if err != nil {
		return e, fmt.Errorf("grant subject %s: %w", subject.String(), err)
	}

	if len(ids) == 0 {
		return e, nil
	}

	installation, err := s.installationTokenHolders(ctx)
	if err != nil {
		return e, err
	}

	base := slices.Clone(e.Changes)
	lines := make([]string, 0, len(ids))

	for _, id := range ids {
		holders, err := s.tokenHoldersOf(ctx, installation, id)
		if err != nil {
			return e, err
		}

		lines = append(lines, id+": "+strings.Join(holders, ", "))
	}

	base = append(base, audit.Change{
		Key: "tokenManagers", Old: nil, New: strings.Join(lines, "\n"), Layer: "", TakesEffect: "",
	})
	e.Changes = base

	return e, nil
}

// tokenHoldersOf returns the sorted union of installation and the direct
// holders of principal:manage-tokens on the service principal with the ID
// id.
func (s *Service) tokenHoldersOf(ctx context.Context, installation []string, id string) ([]string, error) {
	direct, err := s.directTokenHolders(ctx, id)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(installation)+len(direct))
	for _, h := range installation {
		seen[h] = true
	}

	for _, h := range direct {
		seen[h] = true
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// fenceOf returns the fence of the namespace of obj, or the zero fence for
// the installation. It returns namespace.ErrNotFound when the namespace
// does not exist.
func (s *Service) fenceOf(ctx context.Context, obj identity.Object) (namespace.Fence, error) {
	if obj.Namespace() == "" {
		return namespace.Fence{Namespace: "", FailoverVersion: 0}, nil
	}

	ns, err := s.namespaces.GetNamespace(ctx, obj.Namespace())
	if err != nil {
		return namespace.Fence{}, err
	}

	return ns.Fence(), nil
}

// checkEscalation refuses a role with an action that granter does not hold
// on obj, a role with a delegation action when granter does not hold
// server:manage-access on the installation, and a role with
// namespace:manage-principal-tokens when granter holds neither every data
// action on the namespace of obj nor server:manage-access. A grant, a
// revoke, a change of who holds a grant and a change of a role all run it.
func (s *Service) checkEscalation(
	ctx context.Context,
	granter identity.Principal,
	obj identity.Object,
	role Role,
) error {
	if granter.Admin {
		return nil
	}

	if err := s.checkDelegationGranter(ctx, granter, role); err != nil {
		return err
	}

	if err := s.checkNamespaceTokenGranter(ctx, granter, obj, role); err != nil {
		return err
	}

	held, err := s.authz.heldActions(ctx, granter, obj)
	if err != nil {
		return err
	}

	var missing []HeldAction

	for _, a := range role.Actions {
		if !held.has(a) {
			missing = append(missing, HeldAction{Action: a, Object: obj})
		}
	}

	if len(missing) == 0 {
		return nil
	}

	slices.SortFunc(missing, func(a, b HeldAction) int {
		if c := strings.Compare(string(a.Action), string(b.Action)); c != 0 {
			return c
		}

		return strings.Compare(a.Object.String(), b.Object.String())
	})

	return &escalationError{
		text: fmt.Sprintf("%s: %s does not hold %s on %q",
			ErrEscalation, granter.Subject, missing[0].Action, obj.String()),
		missing: missing,
	}
}

// escalationError reports a role with actions that the granter does not hold
// on the object. It wraps ErrEscalation, and it carries the missing actions
// for the audit entry. Its text names the first action, as before.
type escalationError struct {
	text    string
	missing []HeldAction
}

// Error implements error.
func (e *escalationError) Error() string {
	return e.text
}

// Unwrap lets errors.Is match ErrEscalation.
func (e *escalationError) Unwrap() error {
	return ErrEscalation
}

// MissingActions returns the actions of the role that the granter does not
// hold, with the object of each, sorted.
func (e *escalationError) MissingActions() []HeldAction {
	return slices.Clone(e.missing)
}

// checkPrincipalReach refuses a grant of a role with a delegation action on
// a service principal when the reach of the principal does not fit inside
// the reach of granter. A holder of such a grant acts as the principal,
// with every grant that it has now or gets later, so the grant must not
// give more than the granter holds.
//
// It reads the reaches before the write, not in the transaction of the
// write: the reach reads the grants, the roles, the callers and the
// principals of the whole installation, and the transaction of a write
// holds its own connection and its own reads.
//
// It returns nil when granter is an administrator, when obj is not a
// service principal, and when the role holds no delegation action. A
// missing port, a granter with no principal record and a failed read refuse
// the grant. The refusal names the principal, and no cause.
func (s *Service) checkPrincipalReach(
	ctx context.Context, granter identity.Principal, obj identity.Object, role Role,
) error {
	if granter.Admin {
		return nil
	}

	kind, ok := KindOf(obj)
	if !ok || kind != ObjectPrincipal || !delegationAction(role) {
		return nil
	}

	principalID := obj.PrincipalID()

	holder, missing, err := s.missingReachActions(ctx, granter, principalID)
	if err != nil {
		return principalReachRefusal(ctx, holder, principalID, missing, err)
	}

	if len(missing) > 0 {
		return principalReachRefusal(ctx, holder, principalID, missing, nil)
	}

	return nil
}

// missingReachActions returns the reach of granter and the actions of the
// reach of the service principal principalID that it does not hold. A
// missing port and a granter with no principal record answer an error: the
// caller then refuses the grant.
func (s *Service) missingReachActions(
	ctx context.Context, granter identity.Principal, principalID string,
) (Reach, []HeldAction, error) {
	if err := s.reachReady(); err != nil {
		return Reach{}, nil, err
	}

	p, err := s.directory.GetPrincipalBySubject(ctx, granter.Subject)
	if err != nil {
		return Reach{}, nil, fmt.Errorf("read the principal of the caller %q: %w", granter.Subject, err)
	}

	holder, err := ReachOf(ctx, s.reach, p.ID)
	if err != nil {
		return Reach{}, nil, fmt.Errorf("read the reach of the caller: %w", err)
	}

	target, err := ReachOf(ctx, s.reach, principalID)
	if err != nil {
		return holder, nil, fmt.Errorf("read the reach of principal %q: %w", principalID, err)
	}

	missing, err := holder.Missing(ctx, target)
	if err != nil {
		return holder, nil, fmt.Errorf("compare the reaches: %w", err)
	}

	return holder, missing, nil
}

// reachReady returns an error when the reach config does not hold the ports
// that the reach compare reads. The caller then refuses the grant: the
// compare never skips.
func (s *Service) reachReady() error {
	if s.reach.Grants == nil || s.reach.Roles == nil || s.reach.Callers == nil || s.reach.Principals == nil {
		return fmt.Errorf("%w: the reach needs the grants, the roles, the callers and the principals",
			ErrMissingDependency)
	}

	return nil
}

// principalReachRefusal returns the refusal of a grant on the service
// principal principalID whose reach is not inside the reach of the granter,
// and of a grant whose reaches the server cannot compare. Its text names
// the principal only when holder reads the principals of the installation,
// so the refusal never names an object that the caller cannot read. The
// text never holds cause, which may hold driver detail. missing holds the
// actions of the reach of the principal that the granter does not hold, for
// the audit entry.
func principalReachRefusal(
	ctx context.Context, holder Reach, principalID string, missing []HeldAction, cause error,
) error {
	text := "the reach of an object that you cannot read is not inside the reach of the caller"

	readable, err := holder.Has(ctx, identity.ActionServerReadPrincipals, identity.PrincipalObject(principalID))
	if err == nil && readable {
		text = fmt.Sprintf("the reach of principal %q is not inside the reach of the caller", principalID)
	}

	return &principalReachError{text: text, missing: slices.Clone(missing), cause: cause}
}

// principalReachError reports a grant that the reach compare refused. It
// wraps ErrEscalation and the cause, which may hold driver detail. Its text
// names the principal and not the cause.
type principalReachError struct {
	text    string
	missing []HeldAction
	cause   error
}

// Error implements error.
func (r *principalReachError) Error() string {
	return ErrEscalation.Error() + ": " + r.text
}

// MissingActions returns the actions of the reach of the principal that the
// granter does not hold, with the object of each, sorted.
func (r *principalReachError) MissingActions() []HeldAction {
	return slices.Clone(r.missing)
}

// Unwrap lets errors.Is match ErrEscalation and the cause.
func (r *principalReachError) Unwrap() []error {
	if r.cause == nil {
		return []error{ErrEscalation}
	}

	return []error{ErrEscalation, r.cause}
}

// checkSubject returns principal.ErrNotFound when the subject does not
// exist, and ErrInvalid for an unknown subject kind. It returns ErrInvalid,
// wrapped with principal.ErrReservedSubject, for a principal record with the
// subject of the namespace service principal.
func (s *Service) checkSubject(ctx context.Context, subject Subject) error {
	switch subject.Kind {
	case SubjectPrincipal:
		p, err := s.directory.GetPrincipal(ctx, subject.ID)
		if err != nil {
			return fmt.Errorf("grant subject %s: %w", subject.String(), err)
		}

		// Defense in depth: the principal API refuses this subject, so no
		// record should have it, and the Authorizer ignores every stored
		// grant of it. This check stays so that a record from a restore or a
		// row of another version never takes a grant, and no role, also not
		// one with unit:run-plan, goes to the namespace service principal.
		if identity.IsNamespaceServiceSubject(p.Subject) {
			return fmt.Errorf("%w: %s is the namespace service principal, which takes no grant: %w",
				ErrInvalid, subject.String(), principal.ErrReservedSubject)
		}
	case SubjectGroup:
		if _, err := s.directory.GetGroup(ctx, subject.ID); err != nil {
			return fmt.Errorf("grant subject %s: %w", subject.String(), err)
		}
	default:
		return fmt.Errorf("%w: subject kind %q", ErrInvalid, subject.Kind)
	}

	return nil
}

// existingObject resolves the stable ID of a unit or a collection, and returns
// ErrNotFound when it does not exist: a grant can only name an object that
// exists. Any other object is returned as it is.
func (s *Service) existingObject(ctx context.Context, obj identity.Object) (identity.Object, error) {
	if obj.IsCollection() {
		return s.existingCollection(ctx, obj)
	}

	return s.existingUnit(ctx, obj)
}

// existingCollection resolves the stable ID of a collection. Without a
// collection port no collection is found, so every grant on one answers
// ErrNotFound.
func (s *Service) existingCollection(ctx context.Context, obj identity.Object) (identity.Object, error) {
	if s.authz.collections == nil {
		return obj, fmt.Errorf("%w: %w: collection %q does not exist", ErrNotFound, ErrCollectionMissing, obj.String())
	}

	id, found, err := s.authz.collections.CollectionID(ctx, obj)
	if err != nil {
		return obj, fmt.Errorf("read collection %q: %w", obj.String(), err)
	}

	if !found {
		return obj, fmt.Errorf("%w: %w: collection %q does not exist", ErrNotFound, ErrCollectionMissing, obj.String())
	}

	return obj.WithCollectionID(id), nil
}

// existingUnit returns obj with its stable ID when obj is a unit, and
// ErrNotFound when no such unit exists: a grant names a unit that exists, so
// that it follows the unit and not the name. Any other object returns as
// it is.
func (s *Service) existingUnit(ctx context.Context, obj identity.Object) (identity.Object, error) {
	if !obj.IsUnit() {
		return obj, nil
	}

	withID, err := s.authz.withUnitID(ctx, obj)
	if err != nil {
		return obj, err
	}

	if withID.UnitID() == "" {
		return obj, fmt.Errorf("%w: %w: unit %q does not exist", ErrNotFound, ErrUnitMissing, obj.String())
	}

	return withID, nil
}

// sameTarget reports whether a and b are the same object for a grant: the
// same stable ID for units, the same wire form for any other object.
func sameTarget(a, b identity.Object) bool {
	if a.IsCollection() || b.IsCollection() {
		return a.IsCollection() && b.IsCollection() && a.CollectionID() != "" && a.CollectionID() == b.CollectionID()
	}

	if a.IsUnit() || b.IsUnit() {
		return a.IsUnit() && b.IsUnit() && a.UnitID() != "" && a.UnitID() == b.UnitID()
	}

	return a.String() == b.String()
}

// checkGrantHeld refuses when caller does not hold every action that g
// gives on its object: the rule of a revoke, and of a change of who holds g.
func (s *Service) checkGrantHeld(ctx context.Context, caller identity.Principal, g Grant) error {
	role, err := s.GetRole(ctx, g.Role)
	if errors.Is(err, ErrNotFound) {
		// A deleted custom role gives nothing, so nothing is taken away.
		return nil
	}

	if err != nil {
		return err
	}

	return s.checkEscalation(ctx, caller, g.Object, role)
}

// checkPlacement refuses a role with an action outside the data family
// while a current grant gives it on a namespace, a collection or a unit,
// and a role with an action outside the delegation family while a current
// grant gives it on a principal: such a grant would then give those actions
// on an object that must not take them.
func (s *Service) checkPlacement(ctx context.Context, r Role) error {
	if _, ok := actionOutside(r, FamilyData); ok {
		below, err := s.store.RoleGrantedBelowInstallation(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("read grants of role %q: %w", r.ID, err)
		}

		if below {
			return fmt.Errorf("%w: role %q is granted below the installation, so it can hold data actions only",
				ErrInUse, r.ID)
		}
	}

	if _, ok := actionOutside(r, FamilyDelegation); ok || len(r.Actions) == 0 {
		onPrincipal, err := s.store.RoleGrantedOnPrincipal(ctx, r.ID)
		if err != nil {
			return fmt.Errorf("read grants of role %q: %w", r.ID, err)
		}

		if onPrincipal {
			return fmt.Errorf("%w: role %q is granted on a principal, so it can hold principal actions only",
				ErrInUse, r.ID)
		}
	}

	return nil
}

// removedActions returns r with each action of before that r does not
// hold, and no other action.
func removedActions(before, r Role) Role {
	removed := r
	removed.Actions = slices.DeleteFunc(slices.Clone(before.Actions), func(a identity.Action) bool {
		return slices.Contains(r.Actions, a)
	})

	return removed
}

// actionOutside returns the first action of r that is not in the family f,
// and false when every action of r is in f.
func actionOutside(r Role, f Family) (identity.Action, bool) {
	for _, a := range r.Actions {
		if family, _ := FamilyOf(a); family != f {
			return a, true
		}
	}

	return "", false
}

// checkSubjectGrants checks each current grant of subject with
// checkGrantHeld.
func (s *Service) checkSubjectGrants(ctx context.Context, caller identity.Principal, subject Subject) error {
	if caller.Admin {
		return nil
	}

	grants, err := s.store.GrantsOf(ctx, subject)
	if err != nil {
		return fmt.Errorf("read grants of %s: %w", subject.String(), err)
	}

	for i := range grants {
		if err := s.checkGrantHeld(ctx, caller, grants[i]); err != nil {
			return err
		}
	}

	return nil
}

// currentListedUnits puts the current name of each unit in the grants of
// listed.
func (s *Service) currentListedUnits(ctx context.Context, listed []ListedGrant) error {
	grants := make([]Grant, 0, len(listed))
	for i := range listed {
		grants = append(grants, listed[i].Grant)
	}

	if _, err := s.authz.currentUnits(ctx, grants); err != nil {
		return err
	}

	for i := range listed {
		listed[i].Grant.Object = grants[i].Object
	}

	return nil
}
