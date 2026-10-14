package access

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// ID prefixes. An ID is opaque: never parse it.
const (
	grantIDPrefix = "grt-"
	roleIDPrefix  = "rol-"
)

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
// the subject of a grant. principal.Store meets it.
type Directory interface {
	// GetPrincipal returns the principal with the ID id, or
	// principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
	// GetPrincipalBySubject returns the principal with the subject, or
	// principal.ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
	// GetGroup returns the group with the ID id, or principal.ErrNotFound.
	GetGroup(ctx context.Context, id string) (principal.Group, error)
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
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// NewID returns a new opaque ID with the prefix it takes. Nil means
	// random IDs.
	NewID func(prefix string) string
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
	now        func() time.Time
	newID      func(prefix string) string
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
		now: now, newID: newID,
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

// CreateRole validates and stores a custom role, with the entry e.
func (s *Service) CreateRole(ctx context.Context, e audit.Entry, in RoleInput) (Role, error) {
	at := s.Now()
	r := Role{
		CreatedAt: at, UpdatedAt: at, ID: s.newID(roleIDPrefix), Name: in.Name,
		Description: in.Description, Actions: normalizeActions(in.Actions), BuiltIn: false,
	}

	if err := validateRole(r); err != nil {
		return Role{}, err
	}

	if err := s.store.CreateRole(ctx, r, change(e, at, RoleRef(r.ID))); err != nil {
		return Role{}, fmt.Errorf("create role %q: %w", in.Name, err)
	}

	return r, nil
}

// UpdateRole replaces the fields of the custom role with the ID id, with
// the entry e. A built-in role answers ErrBuiltIn.
func (s *Service) UpdateRole(ctx context.Context, e audit.Entry, id string, in RoleInput) (Role, error) {
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

	if err := s.store.UpdateRole(ctx, r, change(e, r.UpdatedAt, RoleRef(id))); err != nil {
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
// action goes on the installation only, and that granter holds every action
// of the role on the object, directly or through an object that holds it.
// The caller has already authorized the manage-access action of the object.
func (s *Service) Grant(ctx context.Context, e audit.Entry, granter identity.Principal, in GrantInput) (Grant, error) {
	role, err := s.GetRole(ctx, in.Role)
	if err != nil {
		return Grant{}, err
	}

	if err := s.checkGrant(ctx, granter, in, role); err != nil {
		return Grant{}, err
	}

	g := Grant{
		CreatedAt: s.Now(), Condition: nil, Object: in.Object, ID: s.newID(grantIDPrefix),
		Role: role.ID, CreatedBy: granter.Subject, Subject: in.Subject,
	}

	if err := s.store.CreateGrant(ctx, g, change(e, g.CreatedAt, GrantRef(g))); err != nil {
		return Grant{}, fmt.Errorf("create grant: %w", err)
	}

	return g, nil
}

// Revoke ends the grant with the ID id on obj, with the entry e. A grant on
// another object answers ErrNotFound, so a caller cannot revoke through an
// object that it manages a grant that it does not manage. The caller has
// already authorized the manage-access action of obj.
func (s *Service) Revoke(ctx context.Context, e audit.Entry, obj identity.Object, id string) (Grant, error) {
	g, err := s.store.GetGrant(ctx, id)
	if err != nil {
		return Grant{}, fmt.Errorf("revoke grant %q: %w", id, err)
	}

	if g.Object.String() != obj.String() {
		return Grant{}, fmt.Errorf("revoke grant %q on %q: %w", id, obj.String(), ErrNotFound)
	}

	if err := s.store.RevokeGrant(ctx, id, change(e, s.Now(), GrantRef(g))); err != nil {
		return Grant{}, fmt.Errorf("revoke grant %q: %w", id, err)
	}

	return g, nil
}

// PageRequest asks for one page of the grants on an object. Cursor is the
// opaque NextCursor of the page before, or empty for the first page, at most
// principal.MaxCursorLength bytes. Limit is 1 to MaxLimit. 0 means that the
// request names no limit, and reads DefaultLimit rows: the HTTP adapter
// refuses a limit of 0 that a request names.
type PageRequest struct {
	Cursor string
	Limit  int
}

// Page is one page of grants. Total is the count of every current grant on
// the object. NextCursor is empty on the last page.
type Page struct {
	Grants     []Grant
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

	grants, total, err := s.store.ListGrantsOn(ctx, obj, q)
	if err != nil {
		return Page{}, fmt.Errorf("list grants on %q: %w", obj.String(), err)
	}

	out := Page{Grants: grants, NextCursor: "", Total: total}

	if len(grants) > limit {
		out.Grants = grants[:limit]
		last := out.Grants[limit-1]
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.Subject.String() + "\x00" + last.Role))
	}

	return out, nil
}

// grantQuery validates req and returns the store query of its page.
func grantQuery(req PageRequest) (GrantQuery, error) {
	q := GrantQuery{AfterSubject: "", AfterRole: "", Limit: req.Limit}

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

// checkGrant runs the checks of Grant.
func (s *Service) checkGrant(ctx context.Context, granter identity.Principal, in GrantInput, role Role) error {
	kind, ok := KindOf(in.Object)
	if !ok {
		return fmt.Errorf("%w: object %q takes no grant", ErrInvalid, in.Object.String())
	}

	if err := s.checkSubject(ctx, in.Subject); err != nil {
		return err
	}

	if kind != ObjectInstallation {
		if _, err := s.namespaces.GetNamespace(ctx, in.Object.Namespace()); err != nil {
			return fmt.Errorf("grant on %q: %w", in.Object.String(), err)
		}

		for _, a := range role.Actions {
			if family, _ := FamilyOf(a); family != FamilyData {
				return fmt.Errorf("%w: role %q holds %s, which only a grant on the installation can give",
					ErrInvalid, role.ID, a)
			}
		}
	}

	return s.checkEscalation(ctx, granter, in.Object, role)
}

// checkEscalation refuses a role with an action that granter does not hold
// on obj.
func (s *Service) checkEscalation(
	ctx context.Context,
	granter identity.Principal,
	obj identity.Object,
	role Role,
) error {
	if granter.Admin {
		return nil
	}

	held, err := s.authz.heldActions(ctx, granter, obj)
	if err != nil {
		return err
	}

	for _, a := range role.Actions {
		if !held.has(a) {
			return fmt.Errorf("%w: %s does not hold %s on %q", ErrEscalation, granter.Subject, a, obj.String())
		}
	}

	return nil
}

// checkSubject returns principal.ErrNotFound when the subject does not
// exist, and ErrInvalid for an unknown subject kind.
func (s *Service) checkSubject(ctx context.Context, subject Subject) error {
	var err error

	switch subject.Kind {
	case SubjectPrincipal:
		_, err = s.directory.GetPrincipal(ctx, subject.ID)
	case SubjectGroup:
		_, err = s.directory.GetGroup(ctx, subject.ID)
	default:
		return fmt.Errorf("%w: subject kind %q", ErrInvalid, subject.Kind)
	}

	if err != nil {
		return fmt.Errorf("grant subject %s: %w", subject.String(), err)
	}

	return nil
}
