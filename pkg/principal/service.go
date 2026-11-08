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

// GroupReach checks a membership add against the reach of the service
// principals that the grants of the group reach. A membership gives the
// grants of the group, and a grant on a service principal with a delegation
// action lets its holder act as that principal, with every grant that it
// has now or gets later, so the reach of each such principal must fit
// inside the reach of the caller. access.Service meets it.
type GroupReach interface {
	// CheckGroupReach returns an error that wraps identity.ErrDenied when
	// the reach of caller does not hold every action that the current
	// grants of the group groupID reach, and when it cannot read what it
	// needs.
	CheckGroupReach(ctx context.Context, caller identity.Principal, groupID string) error
}

// Config holds the dependencies of a Service.
type Config struct {
	Store Store
	// Guard checks that the caller of a membership change, a delete or an enable holds
	// what the grants of the principal or the group give. It is required.
	// An installation that keeps no grants passes AdminOnlyGuard, which
	// allows only an administrator.
	Guard Guard
	// GroupReach checks a membership add against the reach of the service
	// principals that the grants of the group reach. Nil allows an
	// administrator only, like AdminOnlyGuard, because the Service cannot
	// compare the reaches without the port. An installation that keeps
	// grants passes the access service.
	GroupReach GroupReach
	// Namespaces reads the fence of the namespace of a principal of a
	// namespace. Nil means the Service keeps no principal of a namespace.
	Namespaces Namespaces
	// Tokens reports the token managers of a service principal for the
	// audit entry of a membership add. Nil means the Service writes no
	// such change.
	Tokens Tokens
	// TempAdmins tells the temporary administrators among the principals,
	// so a sign-in through an identity provider cannot name one. Nil means
	// the Service keeps no temporary administrator.
	TempAdmins TempAdmins
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// NewID returns a new opaque ID with the prefix it takes. Nil means
	// random IDs.
	NewID func(prefix string) string
}

// TempAdmins tells the temporary administrators among the principals.
type TempAdmins interface {
	// IsTempAdmin reports whether a temporary administrator record, that
	// works or that ended, names the principal id.
	IsTempAdmin(ctx context.Context, id string) (bool, error)
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
	groupReach GroupReach
	namespaces Namespaces
	tempAdmins TempAdmins
	tokens     Tokens
	now        func() time.Time
	newID      func(prefix string) string
}

// NewService returns a Service. Store and Guard are required.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: principal store", ErrMissingDependency)
	}

	if cfg.Guard == nil {
		return nil, fmt.Errorf("%w: principal guard", ErrMissingDependency)
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
		groupReach: cfg.GroupReach,
		namespaces: cfg.Namespaces,
		tempAdmins: cfg.TempAdmins,
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

	if err := refuseNamespaceServiceSubject(p.Subject); err != nil {
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
// the Guard checks that caller holds every action that they give. It
// returns ErrNoWorkingBreakGlass when the principal is the last enabled
// break-glass account. The delete of a principal of a namespace goes to the
// audit log of the namespace, with its fence.
func (s *Service) DeletePrincipal(ctx context.Context, e audit.Entry, caller identity.Principal, id string) error {
	if err := s.checkPrincipal(ctx, caller, id); err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
	}

	p, err := s.store.GetPrincipal(ctx, id)
	if err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
	}

	if err = refuseNamespaceServiceSubject(p.Subject); err != nil {
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

	if err := s.refuseServiceRecord(ctx, id); err != nil {
		return fmt.Errorf("enable principal %q: %w", id, err)
	}

	if err := s.store.EnablePrincipal(ctx, id, change(e, s.Now(), Ref(id))); err != nil {
		return fmt.Errorf("enable principal %q: %w", id, err)
	}

	return nil
}

// DisablePrincipal disables the principal with the ID id, with the entry e. It
// only narrows access: the grants of the principal stay and it cannot use
// them until EnablePrincipal clears the disable. The Guard still checks that
// caller holds every action that the grants of the principal give, so that a
// caller cannot lock out a principal with more rights than their own. The
// check is a read and the write works in maintenance mode, as a revocation
// does. It returns ErrNotFound when no such principal exists, ErrDisabled
// when it is disabled, and ErrNoWorkingBreakGlass when it is the last enabled
// break-glass account: the store refuses that in the same transaction as the
// write.
func (s *Service) DisablePrincipal(
	ctx context.Context, e audit.Entry, caller identity.Principal, id string,
) error {
	if err := s.checkPrincipal(ctx, caller, id); err != nil {
		return fmt.Errorf("disable principal %q: %w", id, err)
	}

	if err := s.refuseServiceRecord(ctx, id); err != nil {
		return fmt.Errorf("disable principal %q: %w", id, err)
	}

	if err := s.store.DisablePrincipal(ctx, id, change(e, s.Now(), Ref(id))); err != nil {
		return fmt.Errorf("disable principal %q: %w", id, err)
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
		CreatedAt:                at,
		UpdatedAt:                at,
		ID:                       s.newID(groupIDPrefix),
		Name:                     in.Name,
		Description:              in.Description,
		Source:                   SourceLocal,
		MemberCount:              0,
		ClosedToIdentityProvider: false,
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

// GroupUpdate names the fields of a group that a change sets. A nil field
// keeps its value. A change that sets ClosedToIdentityProvider to its
// current value is not an error.
type GroupUpdate struct {
	Name        *string
	Description *string
	// ClosedToIdentityProvider sets the switch of the group: true closes
	// the group to the identity provider.
	ClosedToIdentityProvider *bool
}

// UpdateGroup applies c to the group with the ID id, with the entry e, and
// returns the stored group. A change of the switch ClosedToIdentityProvider
// records the new value in the reason of the entry. A change of the name or
// the description keeps its entry.
//
// A rename moves the members of the group from its old name to its new
// name, so an access limit that names either name can lift for them: the
// Guard checks the rename. A change of another field names the group as it
// is, so it keeps the grants check.
func (s *Service) UpdateGroup(
	ctx context.Context, e audit.Entry, caller identity.Principal, id string, c GroupUpdate,
) (Group, error) {
	g, err := s.store.GetGroup(ctx, id)
	if err != nil {
		return Group{}, fmt.Errorf("update group %q: %w", id, err)
	}

	old := g.Name

	if c.Name != nil {
		g.Name = *c.Name
	}

	if c.Description != nil {
		g.Description = *c.Description
	}

	entry := change(e, s.Now(), GroupRef(id))

	if c.ClosedToIdentityProvider != nil && *c.ClosedToIdentityProvider != g.ClosedToIdentityProvider {
		g.ClosedToIdentityProvider = *c.ClosedToIdentityProvider

		entry.Reason = identityProviderMembersOn
		if g.ClosedToIdentityProvider {
			entry.Reason = identityProviderMembersOff
		}
	}

	g.UpdatedAt = entry.At

	if err := g.Validate(); err != nil {
		return Group{}, err
	}

	if g.Name != old {
		if err := s.checkGroup(ctx, caller, id, g.Name, GroupChangeRename); err != nil {
			return Group{}, fmt.Errorf("update group %q: %w", id, err)
		}
	}

	if err := s.store.UpdateGroup(ctx, g, entry); err != nil {
		return Group{}, fmt.Errorf("update group %q: %w", id, err)
	}

	return g, nil
}

// The reasons of the audit entry of a change of the switch that closes a
// group to the identity provider: the identity provider may add members only
// while the group is open.
const (
	identityProviderMembersOn  = "the identity provider may add members: on"
	identityProviderMembersOff = "the identity provider may add members: off"
)

// DeleteGroup ends the group with the ID id and its memberships, with the
// entry e. The store revokes its grants in the same transaction, so the
// Guard checks that caller holds every action that they give.
func (s *Service) DeleteGroup(ctx context.Context, e audit.Entry, caller identity.Principal, id string) error {
	if err := s.checkGroup(ctx, caller, id, "", GroupChangeDelete); err != nil {
		return fmt.Errorf("delete group %q: %w", id, err)
	}

	if err := s.store.DeleteGroup(ctx, id, change(e, s.Now(), GroupRef(id))); err != nil {
		return fmt.Errorf("delete group %q: %w", id, err)
	}

	return nil
}

// AddMember puts the principal principalID in the group groupID, with the
// entry e. A membership gives the grants of the group, so it is a grant by
// proxy: the Guard checks that caller holds every action that they give,
// and GroupReach checks that the reach of caller holds the reach of every
// service principal that they reach.
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
	if err := s.checkGroup(ctx, caller, groupID, "", GroupChangeAddMember); err != nil {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
	}

	// Defense in depth: the Guard checks the actions that the grants of the
	// group give, in the transaction of the write, and the reach of the
	// service principals that they reach is checked here, before the write.
	if err := s.checkGroupReach(ctx, caller, groupID); err != nil {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
	}

	kind, kindErr := s.memberKind(ctx, principalID)
	if kindErr != nil {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, kindErr)
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
	if err := s.checkGroup(ctx, caller, groupID, "", GroupChangeRemoveMember); err != nil {
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
// principal that is not a person, a principal of a namespace, a
// break-glass account or a temporary administrator, and ErrInvalid or
// ErrReservedSubject for a subject that is not valid.
func (s *Service) SignInPrincipal(ctx context.Context, e audit.Entry, in SignInInput) (Principal, bool, error) {
	// Defense in depth: Validate refuses the subject too, but only for a
	// principal that this call makes. This check runs before the read, so
	// a provider that composes "dev:admin" never reaches the dev person
	// that already exists.
	if IsDevSubject(in.Subject) {
		return Principal{}, false, fmt.Errorf(
			"%w: %q starts with %q, which only the dev sign-in of opendustpan dev uses",
			ErrReservedSubject,
			in.Subject,
			DevSubjectPrefix,
		)
	}

	// Defense in depth: Validate refuses the subject too, but only for a
	// principal that this call makes. This check runs before the read, so a
	// provider that composes a subject with the local prefix never reaches
	// the person of a local user. The provider configuration refuses the
	// provider ID of the local users too, and this check does not rely on it.
	if IsLocalSubject(in.Subject) {
		return Principal{}, false, errLocalSubject(in.Subject)
	}

	// Defense in depth: Validate refuses the subject of the namespace service
	// principal too, but only for a principal that this call makes. This
	// check runs before the read, so a record of another version never signs
	// in.
	if err := refuseNamespaceServiceSubject(in.Subject); err != nil {
		return Principal{}, false, err
	}

	return s.signIn(ctx, e, in, Principal.Validate)
}

// CreateLocalPerson makes the person of the local user userID, with the
// subject LocalSubjectPrefix and userID, and the entry e. An empty display
// name takes the subject. It is the only path that makes a principal with a
// subject that starts with LocalSubjectPrefix. It returns ErrReservedSubject
// for a subject that is not the one of userID, ErrInvalid for a value that
// is not valid, and the errors of RecordStore.CreatePrincipal.
func (s *Service) CreateLocalPerson(ctx context.Context, e audit.Entry, userID, displayName string) (Principal, error) {
	subject := LocalSubjectPrefix + userID
	if displayName == "" {
		displayName = subject
	}

	at := s.Now()
	p := Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		DisabledAt:  time.Time{},
		ID:          s.newID(principalIDPrefix),
		Subject:     subject,
		DisplayName: displayName,
		Kind:        KindPerson,
		Source:      SourceLocal,
		Namespace:   "", GroupCount: 0, BreakGlass: false,
	}

	if err := p.validateLocalPerson(userID); err != nil {
		return Principal{}, err
	}

	if err := s.store.CreatePrincipal(ctx, p, change(e, at, Ref(p.ID))); err != nil {
		return Principal{}, fmt.Errorf("create principal %q: %w", subject, err)
	}

	return p, nil
}

// SignInDevAdministrator returns the person DevAdminSubject for the dev
// sign-in of opendustpan dev, and makes it when none exists: a local person
// with no grant and no group. It takes no subject, so it cannot make any
// other principal. It is the only path that makes a principal with a subject
// that starts with DevSubjectPrefix. It returns the errors of
// SignInPrincipal, and created is true when it made the person.
func (s *Service) SignInDevAdministrator(
	ctx context.Context, e audit.Entry, displayName string,
) (Principal, bool, error) {
	in := SignInInput{Subject: DevAdminSubject, Source: SourceLocal, Names: []string{displayName}, Create: true}

	return s.signIn(ctx, e, in, Principal.validateDevAdministrator)
}

// SeedDemoPerson returns the person DemoSubject for the demo seed of
// opendustpan dev, and makes it when none exists: a local person with no
// grant and no group. It takes no subject, so it cannot make any other
// principal. created is true when it made the person.
func (s *Service) SeedDemoPerson(ctx context.Context, e audit.Entry, displayName string) (Principal, bool, error) {
	in := SignInInput{Subject: DemoSubject, Source: SourceLocal, Names: []string{displayName}, Create: true}

	return s.signIn(ctx, e, in, Principal.validateDemoPerson)
}

// signIn is SignInPrincipal. validate checks the principal that it makes.
func (s *Service) signIn(
	ctx context.Context, e audit.Entry, in SignInInput, validate func(Principal) error,
) (Principal, bool, error) {
	p, err := s.store.GetPrincipalBySubject(ctx, in.Subject)

	switch {
	case err == nil:
		return p, false, s.signInTarget(ctx, p)
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

	if validErr := validate(p); validErr != nil {
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

// memberKind returns the kind of the principal with the ID principalID, for
// the add of a member, and empty when no such principal exists: the store
// refuses the membership then. It refuses a principal that cannot be a
// member of a group: a principal of a namespace, and a record with the
// subject of the namespace service principal.
func (s *Service) memberKind(ctx context.Context, principalID string) (Kind, error) {
	p, err := s.store.GetPrincipal(ctx, principalID)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	if refuseErr := refuseNamespaceServiceSubject(p.Subject); refuseErr != nil {
		return "", refuseErr
	}

	if p.Namespace != "" {
		return "", fmt.Errorf("%w: a principal of a namespace cannot be a member of a group, "+
			"because a group belongs to the whole installation", ErrInvalid)
	}

	return p.Kind, nil
}

// refuseServiceRecord returns ErrReservedSubject when the principal with the
// ID id exists and has the subject of a namespace service principal. No record
// should have it, so no API changes one that a restore or a row of another
// version put in the store. An unknown principal returns nil: the store
// reports it.
func (s *Service) refuseServiceRecord(ctx context.Context, id string) error {
	p, err := s.store.GetPrincipal(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("get principal %q: %w", id, err)
	}

	return refuseNamespaceServiceSubject(p.Subject)
}

// checkGroup asks the Guard whether caller may make the change of the group
// groupID. newName is the name that the group takes in a rename, and "" for
// another change.
func (s *Service) checkGroup(
	ctx context.Context, caller identity.Principal, groupID, newName string, change GroupChange,
) error {
	// Defense in depth: NewService refuses a missing Guard. This check stays so
	// that a Service built any other way allows only an administrator.
	if s.guard == nil {
		return AdminOnlyGuard{}.CheckGroupGrants(ctx, caller, groupID, newName, change)
	}

	return s.guard.CheckGroupGrants(ctx, caller, groupID, newName, change)
}

// checkPrincipal asks the Guard whether caller may revoke the grants of the
// principal principalID.
func (s *Service) checkPrincipal(ctx context.Context, caller identity.Principal, principalID string) error {
	// Defense in depth: NewService refuses a missing Guard. This check stays so
	// that a Service built any other way allows only an administrator.
	if s.guard == nil {
		return AdminOnlyGuard{}.CheckPrincipalGrants(ctx, caller, principalID)
	}

	return s.guard.CheckPrincipalGrants(ctx, caller, principalID)
}

// checkGroupReach asks GroupReach whether the reach of caller holds the
// reach of every service principal that the grants of the group groupID
// reach.
func (s *Service) checkGroupReach(ctx context.Context, caller identity.Principal, groupID string) error {
	// Defense in depth: NewService allows a missing GroupReach, for an
	// installation that keeps no grants. This check stays so that a Service
	// built any other way fails closed: without the port it allows only an
	// administrator, like AdminOnlyGuard.
	if s.groupReach == nil {
		if caller.Admin {
			return nil
		}

		return fmt.Errorf(
			"%w: only an administrator adds a member when the server cannot check the reach of the group",
			identity.ErrDenied,
		)
	}

	return s.groupReach.CheckGroupReach(ctx, caller, groupID)
}

// existingSignIn returns the principal with the subject for a sign-in,
// after a create found it taken.
func (s *Service) existingSignIn(ctx context.Context, subject string) (Principal, bool, error) {
	p, err := s.store.GetPrincipalBySubject(ctx, subject)
	if err != nil {
		return Principal{}, false, fmt.Errorf("get principal %q: %w", subject, err)
	}

	return p, false, s.signInTarget(ctx, p)
}

// signInTarget returns why a sign-in through an identity provider cannot
// name p, or nil. A temporary administrator signs in only with its host
// token, never through an identity provider. A failed read of the records
// refuses the sign-in.
func (s *Service) signInTarget(ctx context.Context, p Principal) error {
	if err := signInKind(p); err != nil {
		return err
	}

	if s.tempAdmins == nil {
		return nil
	}

	temp, err := s.tempAdmins.IsTempAdmin(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("read temporary administrator of %s: %w", Ref(p.ID), err)
	}

	if temp {
		return fmt.Errorf("%w: %s is a temporary administrator", ErrNotSignInTarget, Ref(p.ID))
	}

	return nil
}

// signInKind returns why a sign-in through an identity provider cannot
// name p by its kind, its scope or its state, or nil.
func signInKind(p Principal) error {
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
