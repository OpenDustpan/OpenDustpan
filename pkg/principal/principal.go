// Package principal keeps the principals and the groups of an installation
// (ADR 52, decisions 1, 2 and 4).
//
// A principal is a person, a service or a workload. It belongs to the whole
// installation, not to one namespace. A grant on each object decides what a
// principal can do there (RFC 19). A group is a named set of principals. It
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

// Sources of records (ADR 55). Only SourceLocal exists today: the directory
// sync sets the others.
const (
	// SourceLocal is a record made through the API.
	SourceLocal Source = "local"
	// SourceLDAP is a record that an LDAP sync keeps.
	SourceLDAP Source = "ldap"
	// SourceSCIM is a record that a SCIM sync keeps.
	SourceSCIM Source = "scim"
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
	principalIDPrefix = "prn-"
	groupIDPrefix     = "grp-"
)

// Principal is the stored record of one principal. ID is stable and opaque,
// with the prefix "prn-". Subject is the name that an Authenticator answers
// for the principal. It is unique among the principals that exist, compared
// exactly: OIDC subjects are case-sensitive. DisplayName is a name for people
// to read. Source names where the record comes from. GroupCount is the
// count of its current memberships: a read sets it, and a write ignores it.
type Principal struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ID          string
	Subject     string
	DisplayName string
	Kind        Kind
	Source      Source
	GroupCount  int64
}

// Group is the stored record of one group. ID is stable and opaque, with the
// prefix "grp-". Name is unique among the groups that exist, without regard
// to case. Source names where the record comes from. MemberCount is the
// count of its current members: a read sets it, and a write ignores it.
type Group struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ID          string
	Name        string
	Description string
	Source      Source
	MemberCount int64
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

// ValidateSubject reports whether s is a usable subject for a principal
// made through the API: 1 to 255 ASCII characters that match
// ^[A-Za-z0-9][A-Za-z0-9._@+:/-]{0,254}$, which covers emails, SPIFFE IDs
// and user names, and not a subject that a bootstrap admin token uses or can
// look like. Subjects from an identity provider do not come through this
// check.
func ValidateSubject(s string) error {
	if !subjectPattern.MatchString(s) {
		return fmt.Errorf("%w: subject %q: use 1 to 255 ASCII letters, digits and . _ @ + : / -, "+
			"starting with a letter or a digit", ErrInvalid, s)
	}

	if reservedSubject(s) {
		return fmt.Errorf("%w: %q can be read as the subject of a bootstrap admin token", ErrReservedSubject, s)
	}

	return nil
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

		if unicode.Is(unicode.Cf, c) {
			return fmt.Errorf("%w: %s holds the format character %U", ErrInvalid, field, c)
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

	return validateSource(p.Source)
}

// validateSource returns ErrInvalid for a source that is not local, ldap or
// scim.
func validateSource(s Source) error {
	switch s {
	case SourceLocal, SourceLDAP, SourceSCIM:
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

	return validateText("description", g.Description, maxDescriptionLength, false)
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
