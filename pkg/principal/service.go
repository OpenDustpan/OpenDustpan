package principal

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Tokens reports the holders of principal:manage-tokens on a service
// principal, for the audit entry of a membership add. access.Service meets
// it.
type Tokens interface {
	// TokenManagers returns the current holders of principal:manage-tokens
	// on the service principal with the ID id, sorted and each once.
	TokenManagers(ctx context.Context, id string) ([]string, error)
}

// Config holds the dependencies of a Service.
type Config struct {
	Store Store
	// Guard checks that the caller of a membership change, a delete or an enable holds
	// what the grants of the principal or the group give. Nil means
	// no check: only an installation that grants nothing, where every
	// caller is an administrator, leaves it nil.
	Guard Guard
	// Namespaces reads the fence of the namespace of a principal of a
	// namespace. Nil means the Service keeps no principal of a namespace.
	Namespaces Namespaces
	// Tokens reports the token managers of a service principal for the
	// audit entry of a membership add. Nil means the Service writes no
	// such change.
	Tokens Tokens
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// NewID returns a new opaque ID with the prefix it takes. Nil means
	// random IDs.
	NewID func(prefix string) string
}

// Service implements the principal and group use cases.
//
// Each change takes the audit entry of the caller: who acted, from where,
// through which channel, and the action. The Service sets the time from its
// own clock, the scope, the kind change, the outcome success and the record,
// and the store appends the entry in the transaction of the change.
type Service struct {
	store      Store
	guard      Guard
	namespaces Namespaces
	tokens     Tokens
	now        func() time.Time
	newID      func(prefix string) string
}

// NewService returns a Service. Store is required.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: principal store", ErrMissingDependency)
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
		store:      cfg.Store,
		guard:      cfg.Guard,
		namespaces: cfg.Namespaces,
		tokens:     cfg.Tokens,
		now:        now,
		newID:      newID,
	}, nil
}

// Now returns the time of the Service clock, in UTC to the millisecond:
// the time a change and its audit entry carry.
func (s *Service) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Input names the fields of a new principal. An empty display name
// takes the subject.
type Input struct {
	Subject     string
	DisplayName string
	Kind        Kind
}

// CreatePrincipal validates and stores a new principal, with the entry e.
func (s *Service) CreatePrincipal(ctx context.Context, e audit.Entry, in Input) (Principal, error) {
	displayName := in.DisplayName
	if displayName == "" {
		displayName = in.Subject
	}

	at := s.Now()
	p := Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		DisabledAt:  time.Time{},
		ID:          s.newID(principalIDPrefix),
		Subject:     in.Subject,
		DisplayName: displayName,
		Kind:        in.Kind,
		Source:      SourceLocal,
		Namespace:   "", GroupCount: 0, BreakGlass: false,
	}

	if err := p.Validate(); err != nil {
		return Principal{}, err
	}

	if err := s.store.CreatePrincipal(ctx, p, change(e, at, Ref(p.ID))); err != nil {
		return Principal{}, fmt.Errorf("create principal %q: %w", in.Subject, err)
	}

	return p, nil
}

// GetPrincipal returns the principal with the ID id.
func (s *Service) GetPrincipal(ctx context.Context, id string) (Principal, error) {
	p, err := s.store.GetPrincipal(ctx, id)
	if err != nil {
		return Principal{}, fmt.Errorf("get principal %q: %w", id, err)
	}

	return p, nil
}

// ListPrincipals returns one page of the principals, ordered by subject.
// The prefix matches the start of the subject, exactly. A kind that is not
// empty keeps the principals of that kind. An unknown kind returns
// ErrInvalid.
func (s *Service) ListPrincipals(ctx context.Context, req PageRequest, kind Kind) (Page[Principal], error) {
	if kind != "" {
		parsed, err := ParseKind(string(kind))
		if err != nil {
			return Page[Principal]{}, err
		}

		kind = parsed
	}

	q, limit, err := req.query(exactKey)
	if err != nil {
		return Page[Principal]{}, err
	}

	rows, total, err := s.store.ListPrincipals(ctx, q, kind)
	if err != nil {
		return Page[Principal]{}, fmt.Errorf("list principals: %w", err)
	}

	return pageOf(rows, total, limit, func(p Principal) string { return p.Subject }), nil
}

// Change names the fields of a principal that a change sets. A nil field
// keeps its value.
type Change struct {
	DisplayName *string
}

// UpdatePrincipal applies c to the principal with the ID id, with the entry
// e, and returns the stored principal.
func (s *Service) UpdatePrincipal(ctx context.Context, e audit.Entry, id string, c Change) (Principal, error) {
	p, err := s.store.GetPrincipal(ctx, id)
	if err != nil {
		return Principal{}, fmt.Errorf("update principal %q: %w", id, err)
	}

	if c.DisplayName != nil {
		p.DisplayName = *c.DisplayName
		if p.DisplayName == "" {
			p.DisplayName = p.Subject
		}
	}

	p.UpdatedAt = s.Now()

	if err := p.Validate(); err != nil {
		return Principal{}, err
	}

	if err := s.storeUpdate(ctx, e, p); err != nil {
		return Principal{}, fmt.Errorf("update principal %q: %w", id, err)
	}

	return p, nil
}

// DeletePrincipal ends the principal with the ID id and its memberships,
// with the entry e. The store revokes its grants in the same transaction, so
// the Guard checks that caller holds every action that they give. The
// delete of a principal of a namespace goes to the audit log of the
// namespace, with its fence.
func (s *Service) DeletePrincipal(ctx context.Context, e audit.Entry, caller identity.Principal, id string) error {
	if err := s.checkPrincipal(ctx, caller, id); err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
	}

	p, err := s.store.GetPrincipal(ctx, id)
	if err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
	}

	if p.Namespace == "" {
		err = s.store.DeletePrincipal(ctx, id, change(e, s.Now(), Ref(id)))
	} else {
		err = s.endNamespacePrincipal(ctx, e, p)
	}

	if err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
	}

	return nil
}

// EnablePrincipal enables the principal with the ID id again after an
// inactivity disable, with the entry e. The enable restores the use of its
// grants, so the Guard checks that caller holds every action that they give.
func (s *Service) EnablePrincipal(
	ctx context.Context, e audit.Entry, caller identity.Principal, id string,
) error {
	if err := s.checkPrincipal(ctx, caller, id); err != nil {
		return fmt.Errorf("enable principal %q: %w", id, err)
	}

	if err := s.store.EnablePrincipal(ctx, id, change(e, s.Now(), Ref(id))); err != nil {
		return fmt.Errorf("enable principal %q: %w", id, err)
	}

	return nil
}

// GroupInput names the fields of a new group.
type GroupInput struct {
	Name        string
	Description string
}

// CreateGroup validates and stores a new group, with the entry e.
func (s *Service) CreateGroup(ctx context.Context, e audit.Entry, in GroupInput) (Group, error) {
	at := s.Now()
	g := Group{
		CreatedAt:   at,
		UpdatedAt:   at,
		ID:          s.newID(groupIDPrefix),
		Name:        in.Name,
		Description: in.Description,
		Source:      SourceLocal,
		MemberCount: 0,
	}

	if err := g.Validate(); err != nil {
		return Group{}, err
	}

	if err := s.store.CreateGroup(ctx, g, change(e, at, GroupRef(g.ID))); err != nil {
		return Group{}, fmt.Errorf("create group %q: %w", in.Name, err)
	}

	return g, nil
}

// GetGroup returns the group with the ID id.
func (s *Service) GetGroup(ctx context.Context, id string) (Group, error) {
	g, err := s.store.GetGroup(ctx, id)
	if err != nil {
		return Group{}, fmt.Errorf("get group %q: %w", id, err)
	}

	return g, nil
}

// ListGroups returns one page of the groups, ordered by name. The prefix
// matches the start of the name, without regard to case.
func (s *Service) ListGroups(ctx context.Context, req PageRequest) (Page[Group], error) {
	q, limit, err := req.query(strings.ToLower)
	if err != nil {
		return Page[Group]{}, err
	}

	rows, total, err := s.store.ListGroups(ctx, q)
	if err != nil {
		return Page[Group]{}, fmt.Errorf("list groups: %w", err)
	}

	return pageOf(rows, total, limit, groupKey), nil
}

// GroupChange names the fields of a group that a change sets. A nil field
// keeps its value.
type GroupChange struct {
	Name        *string
	Description *string
}

// UpdateGroup applies c to the group with the ID id, with the entry e, and
// returns the stored group.
func (s *Service) UpdateGroup(ctx context.Context, e audit.Entry, id string, c GroupChange) (Group, error) {
	g, err := s.store.GetGroup(ctx, id)
	if err != nil {
		return Group{}, fmt.Errorf("update group %q: %w", id, err)
	}

	if c.Name != nil {
		g.Name = *c.Name
	}

	if c.Description != nil {
		g.Description = *c.Description
	}

	g.UpdatedAt = s.Now()

	if err := g.Validate(); err != nil {
		return Group{}, err
	}

	if err := s.store.UpdateGroup(ctx, g, change(e, g.UpdatedAt, GroupRef(id))); err != nil {
		return Group{}, fmt.Errorf("update group %q: %w", id, err)
	}

	return g, nil
}

// DeleteGroup ends the group with the ID id and its memberships, with the
// entry e. The store revokes its grants in the same transaction, so the
// Guard checks that caller holds every action that they give.
func (s *Service) DeleteGroup(ctx context.Context, e audit.Entry, caller identity.Principal, id string) error {
	if err := s.checkGroup(ctx, caller, id); err != nil {
		return fmt.Errorf("delete group %q: %w", id, err)
	}

	if err := s.store.DeleteGroup(ctx, id, change(e, s.Now(), GroupRef(id))); err != nil {
		return fmt.Errorf("delete group %q: %w", id, err)
	}

	return nil
}

// AddMember puts the principal principalID in the group groupID, with the
// entry e. A membership gives the grants of the group, so it is a grant by
// proxy: the Guard checks that caller holds every action that they give.
//
// The audit entry of a membership add of a service principal names, in the
// change "tokenManagers", every holder of principal:manage-tokens on that
// principal: each of them can act as the principal, with the grants of the
// group too. The entry of a person or a workload names none.
//
// When the grants of the group let its members act as service principals,
// the entry names them in the change "actsAs", one per line, as the access
// review names them: "principal:<id>" for one service principal, the name
// of a namespace for each service principal of that namespace, and
// "@server" for every service principal. The store
// reads them in the transaction of the write, so a grant or a revoke that
// commits first is in the list.
func (s *Service) AddMember(
	ctx context.Context, e audit.Entry, caller identity.Principal, groupID, principalID string,
) (Membership, error) {
	if err := s.checkGroup(ctx, caller, groupID); err != nil {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
	}

	kind := Kind("")
	if p, err := s.store.GetPrincipal(ctx, principalID); err != nil {
		if !errors.Is(err, ErrNotFound) {
			return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
		}
	} else {
		if p.Namespace != "" {
			return Membership{}, fmt.Errorf("add principal %q to group %q: %w: a principal of a namespace cannot be "+
				"a member of a group, because a group belongs to the whole installation", principalID, groupID, ErrInvalid)
		}

		kind = p.Kind
	}

	m := Membership{
		AddedAt: s.Now(), SuspendedAt: time.Time{}, GroupID: groupID, PrincipalID: principalID,
		Source: MembershipSourceLocal, SourceID: "", State: MembershipStateActive, Pinned: false,
	}

	if err := m.Validate(); err != nil {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
	}

	entry := change(e, m.AddedAt, MemberRef(groupID, principalID))
	entry.Changes = append(entry.Changes,
		audit.Change{Key: "source", Old: nil, New: string(m.Source), Layer: "", TakesEffect: ""},
		audit.Change{Key: "sourceID", Old: nil, New: m.SourceID, Layer: "", TakesEffect: ""},
		audit.Change{Key: "state", Old: nil, New: string(m.State), Layer: "", TakesEffect: ""},
		audit.Change{Key: "pinned", Old: nil, New: strconv.FormatBool(m.Pinned), Layer: "", TakesEffect: ""},
	)

	if kind == KindService && s.tokens != nil {
		holders, err := s.tokens.TokenManagers(ctx, principalID)
		if err != nil {
			return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
		}

		entry.Changes = append(entry.Changes, audit.Change{
			Key: "tokenManagers", Old: nil, New: strings.Join(holders, ", "), Layer: "", TakesEffect: "",
		})
	}

	err := s.store.AddMember(ctx, m, entry, actsAsChange(groupID))
	if errors.Is(err, ErrAlreadyExists) {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w: the principal is a member of the group, "+
			"or it left the group in this millisecond. If it is not a member, try again", principalID, groupID, err)
	}

	if err != nil {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
	}

	return m, nil
}

// RemoveMember ends the membership of the principal principalID in the
// group groupID, with the entry e. It takes the grants of the group away, so
// the Guard checks that caller holds every action that they give.
func (s *Service) RemoveMember(
	ctx context.Context, e audit.Entry, caller identity.Principal, groupID, principalID string,
) error {
	if err := s.checkGroup(ctx, caller, groupID); err != nil {
		return fmt.Errorf("remove principal %q from group %q: %w", principalID, groupID, err)
	}

	err := s.store.RemoveMember(ctx, groupID, principalID, change(e, s.Now(), MemberRef(groupID, principalID)))
	if err != nil {
		return fmt.Errorf("remove principal %q from group %q: %w", principalID, groupID, err)
	}

	return nil
}

// ListMembers returns one page of the members of the group groupID, ordered
// by subject. The prefix matches the start of the subject, exactly.
func (s *Service) ListMembers(ctx context.Context, groupID string, req PageRequest) (Page[Member], error) {
	q, limit, err := req.query(exactKey)
	if err != nil {
		return Page[Member]{}, err
	}

	rows, total, err := s.store.ListMembers(ctx, groupID, q)
	if err != nil {
		return Page[Member]{}, fmt.Errorf("list members of group %q: %w", groupID, err)
	}

	return pageOf(rows, total, limit, func(m Member) string { return m.Principal.Subject }), nil
}

// ListGroupsOf returns one page of the groups of the principal principalID,
// ordered by name. The prefix matches the start of the name, without regard
// to case.
func (s *Service) ListGroupsOf(ctx context.Context, principalID string, req PageRequest) (Page[Group], error) {
	q, limit, err := req.query(strings.ToLower)
	if err != nil {
		return Page[Group]{}, err
	}

	rows, total, err := s.store.ListGroupsOf(ctx, principalID, q)
	if err != nil {
		return Page[Group]{}, fmt.Errorf("list groups of principal %q: %w", principalID, err)
	}

	return pageOf(rows, total, limit, groupKey), nil
}

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

// exactKey is the sort key of a subject: the subject itself.
func exactKey(s string) string { return s }

// groupKey is the sort key of a group: its name in lower case.
func groupKey(g Group) string { return strings.ToLower(g.Name) }

// SignInInput names the person of a sign-in through an identity provider.
// Subject is the subject of the principal: "<provider>:<sub>". Names are
// the display name candidates in order of preference; the first one that
// is a valid display name wins, and the subject wins when none is. Create
// allows a new principal when no principal has the subject.
type SignInInput struct {
	Subject string
	// Source is the source of a principal that the sign-in makes:
	// SourceOIDC or SourceOAuth. Empty is SourceOIDC.
	Source Source
	Names  []string
	Create bool
}

// SignInPrincipal returns the principal of a sign-in through an identity
// provider: the principal with the subject in.Subject. When none exists
// and in.Create is true, it makes a person with the source in.Source, no
// grant and no group, with the entry e, and created is true.
//
// It returns ErrNotFound when no principal has the subject and in.Create
// is false, ErrDisabled for a disabled principal, ErrNotSignInTarget for a
// principal that is not a person, a principal of a namespace or a
// break-glass account, and ErrInvalid or ErrReservedSubject for a subject
// that is not valid.
func (s *Service) SignInPrincipal(ctx context.Context, e audit.Entry, in SignInInput) (Principal, bool, error) {
	p, err := s.store.GetPrincipalBySubject(ctx, in.Subject)

	switch {
	case err == nil:
		return p, false, signInTarget(p)
	case !errors.Is(err, ErrNotFound):
		return Principal{}, false, fmt.Errorf("get principal %q: %w", in.Subject, err)
	case !in.Create:
		return Principal{}, false, fmt.Errorf("principal %q: %w", in.Subject, ErrNotFound)
	}

	at := s.Now()
	p = Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		DisabledAt:  time.Time{},
		ID:          s.newID(principalIDPrefix),
		Subject:     in.Subject,
		DisplayName: signInDisplayName(in),
		Kind:        KindPerson,
		Source:      cmp.Or(in.Source, SourceOIDC),
		Namespace:   "", GroupCount: 0, BreakGlass: false,
	}

	if validErr := p.Validate(); validErr != nil {
		return Principal{}, false, validErr
	}

	err = s.store.CreatePrincipal(ctx, p, change(e, at, Ref(p.ID)))
	if errors.Is(err, ErrAlreadyExists) {
		// A parallel first sign-in of the same person made it.
		return s.existingSignIn(ctx, in.Subject)
	}

	if err != nil {
		return Principal{}, false, fmt.Errorf("create principal %q: %w", in.Subject, err)
	}

	return p, true, nil
}

// checkGroup asks the Guard whether caller may change who holds the grants
// of the group groupID.
func (s *Service) checkGroup(ctx context.Context, caller identity.Principal, groupID string) error {
	if s.guard == nil {
		return nil
	}

	return s.guard.CheckGroupGrants(ctx, caller, groupID)
}

// checkPrincipal asks the Guard whether caller may revoke the grants of the
// principal principalID.
func (s *Service) checkPrincipal(ctx context.Context, caller identity.Principal, principalID string) error {
	if s.guard == nil {
		return nil
	}

	return s.guard.CheckPrincipalGrants(ctx, caller, principalID)
}

// existingSignIn returns the principal with the subject for a sign-in,
// after a create found it taken.
func (s *Service) existingSignIn(ctx context.Context, subject string) (Principal, bool, error) {
	p, err := s.store.GetPrincipalBySubject(ctx, subject)
	if err != nil {
		return Principal{}, false, fmt.Errorf("get principal %q: %w", subject, err)
	}

	return p, false, signInTarget(p)
}

// signInTarget returns why a sign-in through an identity provider cannot
// name p, or nil.
func signInTarget(p Principal) error {
	switch {
	case p.Kind != KindPerson || p.Namespace != "" || p.BreakGlass:
		return fmt.Errorf("%w: %s", ErrNotSignInTarget, Ref(p.ID))
	case !p.DisabledAt.IsZero():
		return fmt.Errorf("%w: %s", ErrDisabled, Ref(p.ID))
	default:
		return nil
	}
}

// signInDisplayName returns the first name of in.Names that is a valid,
// non-empty display name, or the subject.
func signInDisplayName(in SignInInput) string {
	for _, name := range in.Names {
		if name != "" && validateText("display name", name, maxDisplayNameLength) == nil {
			return name
		}
	}

	return in.Subject
}

// actsAsChange returns the audit of a membership add that the store runs in
// the transaction of the write: when the grants of the group groupID let
// its members act as service principals, the change "actsAs" names each,
// one per line, so the entry records what the new member can now act as.
func actsAsChange(groupID string) MemberAudit {
	return func(ctx context.Context, r GroupReads) ([]audit.Change, error) {
		ids, err := r.ActsAs(ctx, groupID)
		if err != nil {
			return nil, fmt.Errorf("read what the members of group %q act as: %w", groupID, err)
		}

		if len(ids) == 0 {
			return nil, nil
		}

		return []audit.Change{{Key: "actsAs", Old: nil, New: strings.Join(ids, "\n"), Layer: "", TakesEffect: ""}}, nil
	}
}
