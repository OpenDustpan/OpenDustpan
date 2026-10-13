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
	// use, or that can look like one: admin, or admin- and a number (ADR 6).
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

// ValidateSubject reports whether s is a usable subject: 1 to 255 bytes of
// UTF-8 with no control or format character, no white space at either end,
// and not a subject that a bootstrap admin token uses or that can look like
// one.
func ValidateSubject(s string) error {
	if err := validateText("subject", s, maxSubjectLength, true); err != nil {
		return err
	}

	for _, c := range s {
		if unicode.Is(unicode.Cf, c) {
			return fmt.Errorf("%w: subject holds the format character %U", ErrInvalid, c)
		}
	}

	if reservedSubject(s) {
		return fmt.Errorf("%w: %q can be read as the subject of a bootstrap admin token", ErrReservedSubject, s)
	}

	return nil
}

// reservedSubject reports whether s can be read as "admin", or "admin-" and
// one or more digits: the subjects of the bootstrap admin tokens
// (identity.NewStaticTokens). The check ignores case and reads a full-width
// form as its ASCII character. Any other non-ASCII character can stand for
// any character, so a Cyrillic lookalike or a non-ASCII digit is reserved
// too, when at least one letter of "admin" is ASCII. The project has no
// Unicode normalization library (golang.org/x/text is not a direct
// dependency), so this rule refuses more than a normalized check would.
func reservedSubject(s string) bool {
	runes := foldFullWidth([]rune(strings.ToLower(s)))

	const word = "admin"
	if len(runes) < len(word) || !readsAs(runes[:len(word)], word) {
		return false
	}

	rest := runes[len(word):]
	if len(rest) == 0 {
		return true
	}

	return len(rest) > 1 && matches(rest[0], '-') && digitsOnly(rest[1:])
}

// foldFullWidth replaces each full-width form in runes with its ASCII
// character, in lower case.
func foldFullWidth(runes []rune) []rune {
	for i, c := range runes {
		if c >= fullWidthFirst && c <= fullWidthLast {
			runes[i] = unicode.ToLower(c - fullWidthOffset)
		}
	}

	return runes
}

// readsAs reports whether runes can be read as word: each rune matches its
// letter, and at least one of them is that ASCII letter.
func readsAs(runes []rune, word string) bool {
	ascii := 0

	for i, want := range []rune(word) {
		if !matches(runes[i], want) {
			return false
		}

		if runes[i] == want {
			ascii++
		}
	}

	return ascii > 0
}

// digitsOnly reports whether each rune can be read as a digit: an ASCII
// digit, or a character that is not ASCII.
func digitsOnly(runes []rune) bool {
	for _, c := range runes {
		if c < utf8.RuneSelf && !unicode.IsDigit(c) {
			return false
		}
	}

	return true
}

// The full-width forms of the ASCII characters '!' to '~' (U+FF01 to
// U+FF5E). A full-width form minus the offset is its ASCII character.
const (
	fullWidthFirst  = 0xFF01
	fullWidthLast   = 0xFF5E
	fullWidthOffset = 0xFEE0
)

// matches reports whether c can be read as the ASCII character want: it is
// want, or it is not ASCII.
func matches(c, want rune) bool {
	return c == want || c >= utf8.RuneSelf
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
