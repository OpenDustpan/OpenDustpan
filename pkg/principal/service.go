package principal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Config holds the dependencies of a Service.
type Config struct {
	Store Store
	// Guard checks that the caller of a membership change or a delete holds
	// what the grants of the principal or the group give (ADR 52). Nil means
	// no check: only an installation that grants nothing, where every
	// caller is an administrator, leaves it nil.
	Guard Guard
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
	store Store
	guard Guard
	now   func() time.Time
	newID func(prefix string) string
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

	return &Service{store: cfg.Store, guard: cfg.Guard, now: now, newID: newID}, nil
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
		ID:          s.newID(principalIDPrefix),
		Subject:     in.Subject,
		DisplayName: displayName,
		Kind:        in.Kind,
		Source:      SourceLocal,
		GroupCount:  0,
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
// The prefix matches the start of the subject, exactly.
func (s *Service) ListPrincipals(ctx context.Context, req PageRequest) (Page[Principal], error) {
	q, limit, err := req.query(exactKey)
	if err != nil {
		return Page[Principal]{}, err
	}

	rows, total, err := s.store.ListPrincipals(ctx, q)
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

	if err := s.store.UpdatePrincipal(ctx, p, change(e, p.UpdatedAt, Ref(id))); err != nil {
		return Principal{}, fmt.Errorf("update principal %q: %w", id, err)
	}

	return p, nil
}

// DeletePrincipal ends the principal with the ID id and its memberships,
// with the entry e. The store revokes its grants in the same transaction, so
// the Guard checks that caller holds every action that they give.
func (s *Service) DeletePrincipal(ctx context.Context, e audit.Entry, caller identity.Principal, id string) error {
	if err := s.checkPrincipal(ctx, caller, id); err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
	}

	if err := s.store.DeletePrincipal(ctx, id, change(e, s.Now(), Ref(id))); err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
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
func (s *Service) AddMember(
	ctx context.Context, e audit.Entry, caller identity.Principal, groupID, principalID string,
) (Membership, error) {
	if err := s.checkGroup(ctx, caller, groupID); err != nil {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
	}

	m := Membership{AddedAt: s.Now(), GroupID: groupID, PrincipalID: principalID}

	err := s.store.AddMember(ctx, m, change(e, m.AddedAt, MemberRef(groupID, principalID)))
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
