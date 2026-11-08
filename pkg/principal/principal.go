// Package principal keeps the principals and the groups of an installation
// (ADR 52, decisions 1, 2 and 4).
//
// A principal is a person, a service or a workload. Most principals belong
// to the whole installation. A service principal can instead belong to one
// namespace: it can only hold grants inside that namespace, and it cannot be
// a member of a group. A grant on each object decides what a principal can
// do there. A group is a named set of principals. It
// also belongs to the whole installation. Any principal can be a member of
// more than one group.
//
// Nothing is erased. A delete of a principal or a group ends its record, and
// a removal from a group ends the membership: the rows stay, with the time
// they ended, so the access-review history (ADR 52, decision 10) can read
// them. Each change writes its audit entry in the same transaction as the
// change (fail closed): both commit, or neither.
//
// The package keeps the records only. The Authenticator of package identity
// still turns an access token into a caller, and the bootstrap admin token
// keeps working with no stored principal (ADR 6).
package principal

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports a principal, a group or a membership that does
	// not exist, or that ended.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyExists reports a subject, a group name or a membership that
	// exists.
	ErrAlreadyExists = errors.New("already exists")
	// ErrInvalid reports a principal, a group or a page that fails
	// validation.
	ErrInvalid = errors.New("invalid principal or group")
	// ErrReservedSubject reports a subject that the bootstrap admin tokens
	// use, or that can look like one: admin, or admin and a character that is
	// not a letter (ADR 6).
	ErrReservedSubject = errors.New("reserved subject")
	// ErrManagedByDirectory reports a membership change through the API of
	// a group that a directory sync keeps (ADR 55). The error names the
	// directory.
	ErrManagedByDirectory = errors.New("group is managed by a directory")
	// ErrNotDisabled reports an enable of a principal that is not disabled.
	ErrNotDisabled = errors.New("principal is not disabled")
	// ErrBreakGlassKind reports a break-glass account that is not a person.
	// Only a person can be a break-glass account.
	ErrBreakGlassKind = errors.New("break-glass account must be a person")
	// ErrNoWorkingBreakGlass reports a change that leaves too few working
	// break-glass accounts. An installation keeps its floors of break-glass
	// accounts, as an emergency way in.
	ErrNoWorkingBreakGlass = errors.New("the change leaves too few working break-glass accounts")
	// ErrBreakGlassUnavailable reports that the store cannot count the
	// working break-glass accounts. A caller that cannot count refuses the
	// change (fail closed).
	ErrBreakGlassUnavailable = errors.New("break-glass account count unavailable")
	// ErrBreakGlassPassword reports a write that sets the break-glass flag on
	// a person who has a local password. A break-glass account signs in with
	// a passkey or a hardware key only, so the password goes first.
	ErrBreakGlassPassword = errors.New("break-glass account must have no local password: remove the password first")
	// ErrDisabled reports a principal that is disabled.
	ErrDisabled = errors.New("principal is disabled")
	// ErrNotSignInTarget reports a principal that a sign-in through an
	// identity provider cannot name: one that is not a person, a principal
	// of a namespace, a break-glass account, or a temporary administrator.
	ErrNotSignInTarget = errors.New("principal cannot sign in through an identity provider")
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

// Source names where a principal or a group comes from.
type Source string

// Sources of records. The directory sync sets SourceLDAP and
// SourceSCIM.
const (
	// SourceLocal is a record made through the API.
	SourceLocal Source = "local"
	// SourceLDAP is a record that an LDAP sync keeps.
	SourceLDAP Source = "ldap"
	// SourceSCIM is a record that a SCIM sync keeps.
	SourceSCIM Source = "scim"
	// SourceOIDC is a person that the first sign-in through an OpenID
	// Connect provider made.
	SourceOIDC Source = "oidc"
	// SourceOAuth is a person that the first sign-in through a plain
	// OAuth 2.0 provider made.
	SourceOAuth Source = "oauth"
)

// Limits of the text fields, in bytes.
const (
	maxSubjectLength     = 255
	maxDisplayNameLength = 255
	maxGroupNameLength   = 100
	maxDescriptionLength = 1000
	maxIDLength          = 64
)

// ID prefixes. An ID is opaque: never parse it.
const (
	principalIDPrefix = IDPrefix
	groupIDPrefix     = "grp-"
)

// IDPrefix starts the ID of every principal.
const IDPrefix = "prn-"

// Principal is the stored record of one principal. ID is stable and opaque,
// with the prefix "prn-". Subject is the name that an Authenticator answers
// for the principal. It is unique among the principals that exist, compared
// exactly: OIDC subjects are case-sensitive. DisplayName is a name for people
// to read. Source names where the record comes from. GroupCount is the
// count of its current memberships: a read sets it, and a write ignores it.
// DisabledAt is zero for a principal that the inactivity disable of access
// tokens did not end (ADR 53, decision 4). A disabled principal does not
// authenticate until an administrator enables it again. A write ignores it.
// Namespace is empty for a principal of the installation, and names the
// namespace of a principal of a namespace: a service principal that can only
// hold grants inside that namespace. It does not change after the create.
// BreakGlass marks a break-glass account: a person that signs in without
// the identity provider. A break-glass account is always exempt from the
// inactivity disable. Only a person can have it.
type Principal struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DisabledAt  time.Time
	ID          string
	Subject     string
	DisplayName string
	Kind        Kind
	Source      Source
	Namespace   namespace.Name
	GroupCount  int64
	BreakGlass  bool
}

// Group is the stored record of one group. ID is stable and opaque, with the
// prefix "grp-". Name is unique among the groups that exist, without regard
// to case. Source names where the record comes from. MemberCount is the
// count of its current members: a read sets it, and a write ignores it.
// ClosedToIdentityProvider is true when the identity provider may not add
// members to the group, through SCIM or through a group claim at sign-in.
type Group struct {
	CreatedAt                time.Time
	UpdatedAt                time.Time
	ID                       string
	Name                     string
	Description              string
	Source                   Source
	MemberCount              int64
	ClosedToIdentityProvider bool
}

// MembershipSource names where a group membership comes from: a local
// change through the API, a SCIM call, or a group claim at sign-in.
type MembershipSource string

// Sources of a membership.
const (
	// MembershipSourceLocal is a membership made through the API.
	MembershipSourceLocal MembershipSource = "local"
	// MembershipSourceSCIM is a membership that a SCIM call keeps.
	MembershipSourceSCIM MembershipSource = "scim"
	// MembershipSourceClaim is a membership from a group claim at sign-in.
	MembershipSourceClaim MembershipSource = "claim"
)

// ParseMembershipSource returns the MembershipSource that s names, or
// ErrInvalid.
func ParseMembershipSource(s string) (MembershipSource, error) {
	switch src := MembershipSource(s); src {
	case MembershipSourceLocal, MembershipSourceSCIM, MembershipSourceClaim:
		return src, nil
	default:
		return "", fmt.Errorf("%w: unknown membership source %q", ErrInvalid, s)
	}
}

// MembershipState names whether a group membership is in effect.
type MembershipState string

// States of a membership. A suspended membership gives no rights until it
// is active again.
const (
	// MembershipStateActive is a membership in effect.
	MembershipStateActive MembershipState = "active"
	// MembershipStateSuspended is a membership that gives no rights.
	MembershipStateSuspended MembershipState = "suspended"
)

// ParseMembershipState returns the MembershipState that s names, or
// ErrInvalid.
func ParseMembershipState(s string) (MembershipState, error) {
	switch st := MembershipState(s); st {
	case MembershipStateActive, MembershipStateSuspended:
		return st, nil
	default:
		return "", fmt.Errorf("%w: unknown membership state %q", ErrInvalid, s)
	}
}

// Membership puts one principal in one group. SourceID names the identity
// provider or the SCIM token of a membership from a claim or from SCIM, and
// is empty for a local membership. SuspendedAt is zero while the membership
// is active. Pinned keeps a restored membership through the next directory
// sync.
type Membership struct {
	AddedAt     time.Time
	SuspendedAt time.Time
	GroupID     string
	PrincipalID string
	Source      MembershipSource
	SourceID    string
	State       MembershipState
	Pinned      bool
}

// Validate reports whether m holds usable values. An empty source or state
// takes the default: local and active.
func (m Membership) Validate() error {
	def := m.WithDefaults()

	if _, err := ParseMembershipSource(string(def.Source)); err != nil {
		return err
	}

	if _, err := ParseMembershipState(string(def.State)); err != nil {
		return err
	}

	if def.Source == MembershipSourceLocal && def.SourceID != "" {
		return fmt.Errorf("%w: a local membership names no identity provider or SCIM token", ErrInvalid)
	}

	if def.Source != MembershipSourceLocal && def.SourceID == "" {
		return fmt.Errorf("%w: a membership from %s names its identity provider or SCIM token", ErrInvalid, def.Source)
	}

	if err := validateText("source ID", def.SourceID, maxSubjectLength); err != nil {
		return err
	}

	if def.State == MembershipStateActive && !def.SuspendedAt.IsZero() {
		return fmt.Errorf("%w: an active membership has no suspend time", ErrInvalid)
	}

	if def.State == MembershipStateSuspended && def.SuspendedAt.IsZero() {
		return fmt.Errorf("%w: a suspended membership names when it was suspended", ErrInvalid)
	}

	return nil
}

// WithDefaults returns m with an empty source set to local and an empty
// state set to active. A local add carries neither, so it defaults to both.
func (m Membership) WithDefaults() Membership {
	if m.Source == "" {
		m.Source = MembershipSourceLocal
	}

	if m.State == "" {
		m.State = MembershipStateActive
	}

	return m
}

// Member is one principal in a group, with the time it joined and where the
// membership comes from. SourceID names the identity provider or the SCIM
// token of a membership from a claim or from SCIM, and is empty for a local
// membership. SuspendedAt is zero while the membership is active. Pinned
// keeps a restored membership through the next directory sync.
type Member struct {
	AddedAt     time.Time
	SuspendedAt time.Time
	Principal   Principal
	GroupID     string
	PrincipalID string
	Source      MembershipSource
	SourceID    string
	State       MembershipState
	Pinned      bool
}

// ValidateSubject reports whether s is a usable subject for a principal
// made through the API: 1 to 255 ASCII characters that match
// ^[A-Za-z0-9][A-Za-z0-9._@+:/-]{0,254}$, which covers emails, SPIFFE IDs
// and user names, and not a subject that a bootstrap admin token uses or can
// look like. Subjects from an identity provider do not come through this
// check.
func ValidateSubject(s string) error {
	if err := checkSubjectForm(s); err != nil {
		return err
	}

	if IsDevSubject(s) {
		return fmt.Errorf("%w: %q starts with %q, which only the dev sign-in of opendustpan dev uses",
			ErrReservedSubject, s, DevSubjectPrefix)
	}

	if IsLocalSubject(s) {
		return errLocalSubject(s)
	}

	return refuseNamespaceServiceSubject(s)
}

// refuseNamespaceServiceSubject returns ErrReservedSubject for a subject that
// starts with identity.NamespaceServicePrefix, in any case. Only the server
// builds the namespace service principal, in its own process, and no record
// holds it.
func refuseNamespaceServiceSubject(s string) error {
	if identity.IsNamespaceServiceSubject(s) {
		return fmt.Errorf("%w: %q starts with %q, which only the namespace service principal of the server uses",
			ErrReservedSubject, s, identity.NamespaceServicePrefix)
	}

	return nil
}

// checkSubjectForm checks the form of a subject and that no bootstrap admin
// token can use it. It does not check the prefix of the dev sign-in.
func checkSubjectForm(s string) error {
	if !subjectPattern.MatchString(s) {
		return fmt.Errorf("%w: subject %q: use 1 to 255 ASCII letters, digits and . _ @ + : / -, "+
			"starting with a letter or a digit", ErrInvalid, s)
	}

	if reservedSubject(s) {
		return fmt.Errorf("%w: %q can be read as the subject of a bootstrap admin token", ErrReservedSubject, s)
	}

	return refuseNamespaceServiceSubject(s)
}

// Subjects of the dev sign-in of opendustpan dev. The prefix is reserved:
// ValidateSubject refuses each subject that starts with it, and a server of
// the kind production refuses to authenticate a principal with such a
// subject.
const (
	// DevSubjectPrefix starts the subject of each principal of the dev
	// sign-in. No other path makes a principal with a subject that starts
	// with it.
	DevSubjectPrefix = "dev:"
	// DevAdminSubject is the subject of the person that the one-time code of
	// opendustpan dev signs in.
	DevAdminSubject = DevSubjectPrefix + "admin"
	// DemoSubject is the subject of the demo person of opendustpan dev. The
	// package localuser has the same value as localuser.DemoSubject.
	DemoSubject = DevSubjectPrefix + "demo"
)

// IsDevSubject reports whether the first 4 bytes of s equal DevSubjectPrefix
// when the ASCII letters ignore case. Only the ASCII letters fold: a
// character that Unicode case folding maps to an ASCII letter, such as U+017F
// or U+212A, does not match. The SQL match of each store adapter uses the
// same rule.
func IsDevSubject(s string) bool {
	if len(s) < len(DevSubjectPrefix) {
		return false
	}

	for i := range len(DevSubjectPrefix) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}

		if c != DevSubjectPrefix[i] {
			return false
		}
	}

	return true
}

// LocalSubjectPrefix starts the subject of each local user: "local:" and
// the ID of the local user record, which never changes. Only
// Service.CreateLocalPerson makes a principal with such a subject:
// ValidateSubject refuses each other subject that starts with it, so no
// identity provider and no API call can take the principal of a local user.
const LocalSubjectPrefix = "local:"

// IsLocalSubject reports whether the first 6 bytes of s equal
// LocalSubjectPrefix when the ASCII letters ignore case, with the rule of
// IsDevSubject.
func IsLocalSubject(s string) bool {
	return hasFoldedPrefix(s, LocalSubjectPrefix)
}

// hasFoldedPrefix reports whether the first bytes of s equal prefix when the
// ASCII letters of s ignore case. prefix is in lowercase.
func hasFoldedPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}

	for i := range len(prefix) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}

		if c != prefix[i] {
			return false
		}
	}

	return true
}

// errLocalSubject returns ErrReservedSubject for the subject s of a local
// user.
func errLocalSubject(s string) error {
	return fmt.Errorf("%w: %q starts with %q, which only the local users of the installation use",
		ErrReservedSubject, s, LocalSubjectPrefix)
}

// subjectPattern is the form of a subject made through the API.
var subjectPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+:/-]{0,254}$`)

// IsBootstrapSubject reports whether s is a subject that a bootstrap admin
// token uses: the subjects of the bootstrap admin tokens are admin and
// admin-N, and the API refuses to make a principal with such a subject.
func IsBootstrapSubject(s string) bool {
	return reservedSubject(s)
}

// reservedSubject reports whether s, without regard to case, is "admin" or
// "admin" and a character that is not a letter: the subjects of the
// bootstrap admin tokens are admin and admin-N (identity.NewStaticTokens),
// and admin1, admin. or admin_x can look like them. "administrator" is not
// reserved.
func reservedSubject(s string) bool {
	const word = "admin"

	folded := strings.ToLower(s)
	if !strings.HasPrefix(folded, word) {
		return false
	}

	rest := folded[len(word):]

	return rest == "" || !isASCIILetter(rest[0])
}

// isASCIILetter reports whether c is an ASCII letter.
func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ValidateGroupName reports whether s is a usable group name: 1 to 100
// ASCII letters, digits, '-', '_' and '.', not starting with '.' or '-'.
func ValidateGroupName(s string) error {
	if !ident.Valid(s, maxGroupNameLength) {
		return fmt.Errorf("%w: group name %q", ErrInvalid, s)
	}

	return nil
}

// validateText checks one optional text field. An empty field is valid.
func validateText(field, s string, maxLen int) error {
	switch {
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

		if unicode.Is(unicode.Cf, c) {
			return fmt.Errorf("%w: %s holds the format character %U", ErrInvalid, field, c)
		}
	}

	return nil
}

// Validate reports whether p holds usable values. It does not check ID or
// the times: the Service sets them. It returns ErrBreakGlassKind when a
// principal that is not a person is marked as a break-glass account.
func (p Principal) Validate() error {
	if err := ValidateSubject(p.Subject); err != nil {
		return err
	}

	// Defense in depth: ValidateSubject refuses this subject too; this check
	// stays so that a change to that function never lets a record with the
	// subject of the namespace service principal through Validate.
	if err := refuseNamespaceServiceSubject(p.Subject); err != nil {
		return err
	}

	return p.validateFields()
}

// validateLocalPerson is Validate for the person of a local user: it
// accepts only the subject LocalSubjectPrefix and the ID of the record, in
// lowercase, for a person.
func (p Principal) validateLocalPerson(userID string) error {
	if userID == "" || p.Subject != LocalSubjectPrefix+userID {
		return fmt.Errorf("%w: %q is not the subject of the local user %q", ErrReservedSubject, p.Subject, userID)
	}

	if p.Kind != KindPerson {
		return fmt.Errorf("%w: a local user is a person, not a %s", ErrInvalid, p.Kind)
	}

	if err := checkSubjectForm(p.Subject); err != nil {
		return err
	}

	return p.validateFields()
}

// validateDevAdministrator is Validate for the one principal that the dev
// sign-in makes: it accepts the subject DevAdminSubject and no other subject
// with the prefix of the dev sign-in.
func (p Principal) validateDevAdministrator() error {
	if p.Subject != DevAdminSubject {
		return fmt.Errorf("%w: %q is not the subject of the dev sign-in", ErrReservedSubject, p.Subject)
	}

	if err := checkSubjectForm(p.Subject); err != nil {
		return err
	}

	return p.validateFields()
}

// validateDemoPerson is Validate for the one principal that the demo seed
// makes: it accepts the subject DemoSubject and no other subject with the
// prefix of the dev sign-in.
func (p Principal) validateDemoPerson() error {
	if p.Subject != DemoSubject {
		return fmt.Errorf("%w: %q is not the subject of the demo person", ErrReservedSubject, p.Subject)
	}

	if err := checkSubjectForm(p.Subject); err != nil {
		return err
	}

	return p.validateFields()
}

// validateFields checks the fields of p other than the subject.
func (p Principal) validateFields() error {
	if err := validateText("display name", p.DisplayName, maxDisplayNameLength); err != nil {
		return err
	}

	if _, err := ParseKind(string(p.Kind)); err != nil {
		return err
	}

	if p.BreakGlass && p.Kind != KindPerson {
		return fmt.Errorf("%w: kind %q", ErrBreakGlassKind, p.Kind)
	}

	if err := p.validateNamespace(); err != nil {
		return err
	}

	return validateSource(p.Source)
}

// validateNamespace returns ErrInvalid for a principal of a namespace with
// a namespace name that is not valid, or that is not a service principal.
func (p Principal) validateNamespace() error {
	if p.Namespace == "" {
		return nil
	}

	if _, err := namespace.ParseName(p.Namespace.String()); err != nil {
		return fmt.Errorf("%w: namespace %q", ErrInvalid, p.Namespace.String())
	}

	if p.Kind != KindService {
		return fmt.Errorf("%w: a principal of a namespace must be a service principal, not a %s", ErrInvalid, p.Kind)
	}

	return nil
}

// validateSource returns ErrInvalid for a source that is not local, ldap,
// scim, oidc or oauth.
func validateSource(s Source) error {
	switch s {
	case SourceLocal, SourceLDAP, SourceSCIM, SourceOIDC, SourceOAuth:
		return nil
	default:
		return fmt.Errorf("%w: unknown source %q", ErrInvalid, s)
	}
}

// Validate reports whether g holds usable values. It does not check ID or
// the times: the Service sets them.
func (g Group) Validate() error {
	if err := ValidateGroupName(g.Name); err != nil {
		return err
	}

	if err := validateSource(g.Source); err != nil {
		return err
	}

	return validateText("description", g.Description, maxDescriptionLength)
}

// Audit object names of the records that a change acts on (ADR 60,
// decision 6). An entry names a record as text, never as a request path.
const invalidRef = "invalid"

// Ref returns the audit object of the principal id:
// "principal:<id>". An id that is not a principal ID gives
// "principal:invalid", so request text never reaches the audit log.
func Ref(id string) string {
	return "principal:" + safeID(principalIDPrefix, id)
}

// GroupRef returns the audit object of the group id: "group:<id>", or
// "group:invalid".
func GroupRef(id string) string {
	return "group:" + safeID(groupIDPrefix, id)
}

// MemberRef returns the audit object of the membership of the principal
// principalID in the group groupID: "group:<id>#member:<id>".
func MemberRef(groupID, principalID string) string {
	return GroupRef(groupID) + "#member:" + safeID(principalIDPrefix, principalID)
}

// safeID returns id when it has prefix and is 1 to 64 ID characters, and
// "invalid" otherwise.
func safeID(prefix, id string) string {
	if strings.HasPrefix(id, prefix) && ident.Valid(id, maxIDLength) {
		return id
	}

	return invalidRef
}
