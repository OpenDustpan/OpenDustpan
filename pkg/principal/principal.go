// Package principal keeps the principals and the groups of an installation
// (ADR 52, decisions 1, 2 and 4).
//
// A principal is a person, a service or a workload. It belongs to the whole
// installation, not to one namespace. A grant on each object decides what a
// principal can do there (RFC 19). A group is a named set of principals. It
// also belongs to the whole installation. Any principal can be a member of
// more than one group.
//
// The package keeps the records only. The Authenticator of package identity
// still turns an access token into a caller, and the bootstrap admin token
// keeps working with no stored principal (ADR 6).
package principal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports a principal, a group or a membership that does
	// not exist.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyExists reports a subject, a group name or a membership that
	// exists.
	ErrAlreadyExists = errors.New("already exists")
	// ErrInvalid reports a principal or a group that fails validation.
	ErrInvalid = errors.New("invalid principal or group")
	// ErrReservedSubject reports a subject that the bootstrap admin tokens
	// use: admin, or admin- and a number (ADR 6).
	ErrReservedSubject = errors.New("reserved subject")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

// Kind names what a principal is.
type Kind string

// Kinds of principal (ADR 52, decision 1).
const (
	// KindPerson is a person who signs in.
	KindPerson Kind = "person"
	// KindService is a machine that uses a service token (ADR 53).
	KindService Kind = "service"
	// KindWorkload is a workload that proves its identity with a JWT or a
	// client certificate (ADR 54).
	KindWorkload Kind = "workload"
)

// ParseKind returns the Kind that s names, or ErrInvalid.
func ParseKind(s string) (Kind, error) {
	switch k := Kind(s); k {
	case KindPerson, KindService, KindWorkload:
		return k, nil
	default:
		return "", fmt.Errorf("%w: unknown kind %q", ErrInvalid, s)
	}
}

// Limits of the text fields, in bytes.
const (
	maxSubjectLength     = 255
	maxDisplayNameLength = 255
	maxGroupNameLength   = 100
	maxDescriptionLength = 1000
)

// ID prefixes. An ID is opaque: never parse it.
const (
	principalIDPrefix = "prn-"
	groupIDPrefix     = "grp-"
)

// Principal is the stored record of one principal. ID is stable and opaque,
// with the prefix "prn-". Subject is the name that an Authenticator answers
// for the principal. It is unique in the installation, compared exactly.
// DisplayName is a name for people to read. A disabled principal keeps its
// record, its memberships and its grants.
type Principal struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ID          string
	Subject     string
	DisplayName string
	Kind        Kind
	Disabled    bool
}

// Group is the stored record of one group. ID is stable and opaque, with the
// prefix "grp-". Name is unique in the installation without regard to case.
type Group struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ID          string
	Name        string
	Description string
}

// Member is one principal in a group, with the time it joined.
type Member struct {
	AddedAt   time.Time
	Principal Principal
}

// Membership puts one principal in one group.
type Membership struct {
	AddedAt     time.Time
	GroupID     string
	PrincipalID string
}

// ValidateSubject reports whether s is a usable subject: 1 to 255 bytes of
// UTF-8 with no control character, no white space at either end, and not a
// subject that the bootstrap admin tokens use.
func ValidateSubject(s string) error {
	if err := validateText("subject", s, maxSubjectLength, true); err != nil {
		return err
	}

	if reservedSubject(s) {
		return fmt.Errorf("%w: %q belongs to a bootstrap admin token", ErrReservedSubject, s)
	}

	return nil
}

// reservedSubject reports whether s is admin, or admin- and a number: the
// subjects of the bootstrap admin tokens (identity.NewStaticTokens).
func reservedSubject(s string) bool {
	if s == "admin" {
		return true
	}

	rest, ok := strings.CutPrefix(s, "admin-")
	if !ok || rest == "" {
		return false
	}

	for _, c := range rest {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}

// ValidateGroupName reports whether s is a usable group name: 1 to 100
// ASCII letters, digits, '-', '_' and '.', not starting with '.' or '-'.
func ValidateGroupName(s string) error {
	if !ident.Valid(s, maxGroupNameLength) {
		return fmt.Errorf("%w: group name %q", ErrInvalid, s)
	}

	return nil
}

// validateText checks one text field. A required field must not be empty.
func validateText(field, s string, maxLen int, required bool) error {
	switch {
	case s == "" && required:
		return fmt.Errorf("%w: missing %s", ErrInvalid, field)
	case len(s) > maxLen:
		return fmt.Errorf("%w: %s longer than %d bytes", ErrInvalid, field, maxLen)
	case !utf8.ValidString(s):
		return fmt.Errorf("%w: %s is not UTF-8", ErrInvalid, field)
	case strings.TrimSpace(s) != s:
		return fmt.Errorf("%w: %s starts or ends with white space", ErrInvalid, field)
	}

	for _, c := range s {
		if unicode.IsControl(c) {
			return fmt.Errorf("%w: %s holds a control character", ErrInvalid, field)
		}
	}

	return nil
}

// Validate reports whether p holds usable values. It does not check ID or
// the times: the Service sets them.
func (p Principal) Validate() error {
	if err := ValidateSubject(p.Subject); err != nil {
		return err
	}

	if err := validateText("display name", p.DisplayName, maxDisplayNameLength, false); err != nil {
		return err
	}

	if _, err := ParseKind(string(p.Kind)); err != nil {
		return err
	}

	return nil
}

// Validate reports whether g holds usable values. It does not check ID or
// the times: the Service sets them.
func (g Group) Validate() error {
	if err := ValidateGroupName(g.Name); err != nil {
		return err
	}

	return validateText("description", g.Description, maxDescriptionLength, false)
}

// Store is the persistence port for principals, groups and memberships.
// The records belong to the installation, so no write carries a namespace
// fence.
type Store interface {
	RecordStore
	GroupStore
	MemberStore
}

// RecordStore keeps the records of the principals.
type RecordStore interface {
	// CreatePrincipal persists p. It returns ErrAlreadyExists when the ID or
	// the subject is taken.
	CreatePrincipal(ctx context.Context, p Principal) error
	// GetPrincipal returns the principal with the ID id, or ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (Principal, error)
	// GetPrincipalBySubject returns the principal with the subject subject,
	// or ErrNotFound.
	GetPrincipalBySubject(ctx context.Context, subject string) (Principal, error)
	// ListPrincipals returns every principal, ordered by subject.
	ListPrincipals(ctx context.Context) ([]Principal, error)
	// UpdatePrincipal replaces the display name, the disabled flag and the
	// update time of the principal with the ID p.ID. The subject, the kind
	// and the creation time do not change. It returns ErrNotFound when no
	// such principal exists.
	UpdatePrincipal(ctx context.Context, p Principal) error
	// DeletePrincipal deletes the principal with the ID id and every
	// membership of it, in one transaction. It returns ErrNotFound when no
	// such principal exists.
	DeletePrincipal(ctx context.Context, id string) error
}

// GroupStore keeps the group records.
type GroupStore interface {
	// CreateGroup persists g. It returns ErrAlreadyExists when the ID is
	// taken, or when the name is taken without regard to case.
	CreateGroup(ctx context.Context, g Group) error
	// GetGroup returns the group with the ID id, or ErrNotFound.
	GetGroup(ctx context.Context, id string) (Group, error)
	// ListGroups returns every group, ordered by name without regard to
	// case.
	ListGroups(ctx context.Context) ([]Group, error)
	// UpdateGroup replaces the name, the description and the update time of
	// the group with the ID g.ID. It returns ErrNotFound when no such group
	// exists, and ErrAlreadyExists when another group has the name without
	// regard to case.
	UpdateGroup(ctx context.Context, g Group) error
	// DeleteGroup deletes the group with the ID id and every membership in
	// it, in one transaction. It returns ErrNotFound when no such group
	// exists.
	DeleteGroup(ctx context.Context, id string) error
}

// MemberStore keeps the memberships of principals in groups.
type MemberStore interface {
	// AddMember persists m. It returns ErrNotFound when the group or the
	// principal does not exist, and ErrAlreadyExists when the principal is
	// a member of the group.
	AddMember(ctx context.Context, m Membership) error
	// RemoveMember deletes the membership of the principal principalID in
	// the group groupID. It returns ErrNotFound when no such membership
	// exists.
	RemoveMember(ctx context.Context, groupID, principalID string) error
	// ListMembers returns the members of the group groupID, ordered by
	// subject. It returns ErrNotFound when the group does not exist.
	ListMembers(ctx context.Context, groupID string) ([]Member, error)
	// ListGroupsOf returns the groups of the principal principalID, ordered
	// by name without regard to case. It returns ErrNotFound when the
	// principal does not exist.
	ListGroupsOf(ctx context.Context, principalID string) ([]Group, error)
}

// Config holds the dependencies of a Service.
type Config struct {
	Store Store
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// NewID returns a new opaque ID with the prefix it takes. Nil means
	// random IDs.
	NewID func(prefix string) string
}

// Service implements the principal and group use cases.
type Service struct {
	store Store
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

	return &Service{store: cfg.Store, now: now, newID: newID}, nil
}

// CreatePrincipal validates and stores a new principal. An empty display
// name takes the subject.
func (s *Service) CreatePrincipal(ctx context.Context, subject, displayName string, kind Kind) (Principal, error) {
	if displayName == "" {
		displayName = subject
	}

	at := s.now().UTC().Truncate(time.Millisecond)
	p := Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		ID:          s.newID(principalIDPrefix),
		Subject:     subject,
		DisplayName: displayName,
		Kind:        kind,
		Disabled:    false,
	}

	if err := p.Validate(); err != nil {
		return Principal{}, err
	}

	if err := s.store.CreatePrincipal(ctx, p); err != nil {
		return Principal{}, fmt.Errorf("create principal %q: %w", subject, err)
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

// ListPrincipals returns every principal, ordered by subject.
func (s *Service) ListPrincipals(ctx context.Context) ([]Principal, error) {
	out, err := s.store.ListPrincipals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list principals: %w", err)
	}

	return out, nil
}

// Change names the fields of a principal that a change sets. A nil field
// keeps its value.
type Change struct {
	DisplayName *string
	Disabled    *bool
}

// UpdatePrincipal applies change to the principal with the ID id and returns
// the stored principal.
func (s *Service) UpdatePrincipal(ctx context.Context, id string, change Change) (Principal, error) {
	p, err := s.store.GetPrincipal(ctx, id)
	if err != nil {
		return Principal{}, fmt.Errorf("update principal %q: %w", id, err)
	}

	if change.DisplayName != nil {
		p.DisplayName = *change.DisplayName
		if p.DisplayName == "" {
			p.DisplayName = p.Subject
		}
	}

	if change.Disabled != nil {
		p.Disabled = *change.Disabled
	}

	p.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)

	if err := p.Validate(); err != nil {
		return Principal{}, err
	}

	if err := s.store.UpdatePrincipal(ctx, p); err != nil {
		return Principal{}, fmt.Errorf("update principal %q: %w", id, err)
	}

	return p, nil
}

// DeletePrincipal deletes the principal with the ID id and its memberships.
func (s *Service) DeletePrincipal(ctx context.Context, id string) error {
	if err := s.store.DeletePrincipal(ctx, id); err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
	}

	return nil
}

// CreateGroup validates and stores a new group.
func (s *Service) CreateGroup(ctx context.Context, name, description string) (Group, error) {
	at := s.now().UTC().Truncate(time.Millisecond)
	g := Group{
		CreatedAt:   at,
		UpdatedAt:   at,
		ID:          s.newID(groupIDPrefix),
		Name:        name,
		Description: description,
	}

	if err := g.Validate(); err != nil {
		return Group{}, err
	}

	if err := s.store.CreateGroup(ctx, g); err != nil {
		return Group{}, fmt.Errorf("create group %q: %w", name, err)
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

// ListGroups returns every group, ordered by name.
func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	out, err := s.store.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}

	return out, nil
}

// GroupChange names the fields of a group that a change sets. A nil field
// keeps its value.
type GroupChange struct {
	Name        *string
	Description *string
}

// UpdateGroup applies change to the group with the ID id and returns the
// stored group.
func (s *Service) UpdateGroup(ctx context.Context, id string, change GroupChange) (Group, error) {
	g, err := s.store.GetGroup(ctx, id)
	if err != nil {
		return Group{}, fmt.Errorf("update group %q: %w", id, err)
	}

	if change.Name != nil {
		g.Name = *change.Name
	}

	if change.Description != nil {
		g.Description = *change.Description
	}

	g.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)

	if err := g.Validate(); err != nil {
		return Group{}, err
	}

	if err := s.store.UpdateGroup(ctx, g); err != nil {
		return Group{}, fmt.Errorf("update group %q: %w", id, err)
	}

	return g, nil
}

// DeleteGroup deletes the group with the ID id and its memberships.
func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	if err := s.store.DeleteGroup(ctx, id); err != nil {
		return fmt.Errorf("delete group %q: %w", id, err)
	}

	return nil
}

// AddMember puts the principal principalID in the group groupID.
func (s *Service) AddMember(ctx context.Context, groupID, principalID string) (Membership, error) {
	m := Membership{
		AddedAt:     s.now().UTC().Truncate(time.Millisecond),
		GroupID:     groupID,
		PrincipalID: principalID,
	}

	if err := s.store.AddMember(ctx, m); err != nil {
		return Membership{}, fmt.Errorf("add principal %q to group %q: %w", principalID, groupID, err)
	}

	return m, nil
}

// RemoveMember takes the principal principalID out of the group groupID.
func (s *Service) RemoveMember(ctx context.Context, groupID, principalID string) error {
	if err := s.store.RemoveMember(ctx, groupID, principalID); err != nil {
		return fmt.Errorf("remove principal %q from group %q: %w", principalID, groupID, err)
	}

	return nil
}

// ListMembers returns the members of the group groupID, ordered by subject.
func (s *Service) ListMembers(ctx context.Context, groupID string) ([]Member, error) {
	out, err := s.store.ListMembers(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("list members of group %q: %w", groupID, err)
	}

	return out, nil
}

// ListGroupsOf returns the groups of the principal principalID, ordered by
// name.
func (s *Service) ListGroupsOf(ctx context.Context, principalID string) ([]Group, error) {
	out, err := s.store.ListGroupsOf(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("list groups of principal %q: %w", principalID, err)
	}

	return out, nil
}
