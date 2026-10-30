// Package trustrule keeps the workload trust rules of each namespace.
//
// A trust rule lets a workload, for example a CI job, exchange a JWT from
// its own issuer for a short-lived OpenDustpan access token. The rule names
// the issuer, how the server gets the signing keys of the issuer, the exact
// audience, the conditions on the claims of the JWT, and the principal that a
// match acts as. The grants of that principal decide what the workload can
// do. A rule belongs to one namespace, and its principal is a principal of
// that namespace, so a rule never gives access to another namespace.
//
// Each change writes its audit entry in the same transaction as the change
// (fail closed), and each write carries the fence of its namespace.
package trustrule

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports a rule that does not exist.
	ErrNotFound = errors.New("trust rule not found")
	// ErrExists reports a rule ID, or a rule name in its namespace, that is
	// taken.
	ErrExists = errors.New("trust rule already exists")
	// ErrInvalid reports a rule that fails validation.
	ErrInvalid = errors.New("invalid trust rule")
	// ErrLimit reports a namespace that holds MaxPerNamespace rules.
	ErrLimit = errors.New("the namespace holds the maximum number of trust rules")
	// ErrPrincipal reports a principal that a rule cannot name: it does not
	// exist, it is not a principal of the namespace of the rule, it is a
	// person, or it is disabled.
	ErrPrincipal = errors.New("the principal cannot be the principal of a trust rule")
	// ErrIssuerRefused reports an issuer of this installation. A token that
	// this installation signs never becomes an access token of it.
	ErrIssuerRefused = errors.New("the issuer is an issuer of this installation")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

// Limits of a rule.
const (
	// MaxPerNamespace is the most rules that one namespace holds.
	MaxPerNamespace = 100
	// MaxNameLength is the longest name, in bytes.
	MaxNameLength = 100
	// MaxIssuerLength is the longest issuer, in bytes.
	MaxIssuerLength = 512
	// MaxAudienceLength is the longest audience, in bytes.
	MaxAudienceLength = 255
	// MaxConditions is the most conditions of one rule.
	MaxConditions = 16
	// MaxValues is the most values of one condition.
	MaxValues = 32
	// MaxValueLength is the longest value of a condition, in bytes.
	MaxValueLength = 512
	// MaxClaimLength is the longest claim name of a condition, in bytes.
	MaxClaimLength = 64
	// MaxStaticKeysBytes is the largest JWKS that a rule holds.
	MaxStaticKeysBytes = 64 << 10
	// maxIDLength bounds a rule ID.
	maxIDLength = 64
	// maxPrincipalIDLength bounds the ID of the principal of a rule.
	maxPrincipalIDLength = 64
)

// SubjectClaim is the claim that each rule must have a condition on. An
// issuer such as a public CI service signs tokens for every one of its
// customers, so a rule with no condition on the subject would match the
// tokens of every customer.
const SubjectClaim = "sub"

// idPrefix starts each rule ID.
const idPrefix = "trr-"

// ID is the stable, opaque ID of a rule: the prefix "trr-" and 32 lower-case
// hex digits. Never parse it.
type ID string

// NewID returns a new rule ID from the system random source.
func NewID() ID {
	var b [16]byte

	// crypto/rand.Read does not return an error on any supported platform.
	_, _ = rand.Read(b[:])

	return ID(idPrefix + hex.EncodeToString(b[:]))
}

// Validate returns ErrInvalid when id is not the form of a rule ID.
func (id ID) Validate() error {
	s := string(id)
	if !strings.HasPrefix(s, idPrefix) || len(s) == len(idPrefix) || !ident.Valid(s, maxIDLength) {
		return fmt.Errorf("%w: the ID %s is not a trust rule ID", ErrInvalid, ident.Echo(s))
	}

	return nil
}

// Ref returns the audit object of the rule id: "trust-rule:<id>". An id that
// is not a rule ID gives "trust-rule:invalid", so request text never reaches
// the audit log.
func Ref(id ID) string {
	if id.Validate() != nil {
		return "trust-rule:invalid"
	}

	return "trust-rule:" + string(id)
}

// KeySource names where the server gets the signing keys of the issuer of a
// rule.
type KeySource string

// The key sources (any issuer: an OIDC provider, or a JWT issuer with keys
// that an administrator gives).
const (
	// KeySourceDiscovery reads the keys from the jwks_uri of the OIDC
	// discovery document of the issuer. The server reads both through the
	// egress rules.
	KeySourceDiscovery KeySource = "discovery"
	// KeySourceStatic uses the JWKS that the rule holds. The server makes no
	// outbound request for it.
	KeySourceStatic KeySource = "static"
)

// Condition is one condition on the claims of a JWT: the claim Claim must be
// a JSON string that is exactly one of Values. The comparison is exact and
// case-sensitive. A value has no wildcard: each character, also '*', matches
// only itself.
type Condition struct {
	Claim  string
	Values []string
}

// Rule is one stored trust rule. CreatedAt is the time of the create.
// Namespace is the namespace of the rule, and PrincipalID names a principal
// of that namespace. Issuer is compared exactly with the iss claim, and
// Audience exactly with the aud claim. Keys names the key source. StaticKeys
// holds the JWKS of KeySourceStatic, and is empty for KeySourceDiscovery.
// Conditions are in the order of their claim names. CreatedBy is the subject
// of the principal that made the rule.
type Rule struct {
	CreatedAt   time.Time
	ID          ID
	Namespace   namespace.Name
	Name        string
	Issuer      string
	Audience    string
	Keys        KeySource
	StaticKeys  string
	PrincipalID string
	CreatedBy   string
	Conditions  []Condition
}

// reservedClaims are the claims that the exchange checks on its own. A
// condition cannot name them: a condition on them would give a second, weaker
// rule for the same check.
func reservedClaims() []string {
	return []string{"iss", "aud", "exp", "nbf", "iat", "jti"}
}

// Validate returns ErrInvalid when r does not hold usable values. It checks
// the form of each field. It does not check that the principal exists: the
// service checks that.
func (r *Rule) Validate() error {
	if err := r.ID.Validate(); err != nil {
		return err
	}

	if _, err := namespace.ParseName(r.Namespace.String()); err != nil {
		return fmt.Errorf("%w: namespace %s", ErrInvalid, ident.Echo(r.Namespace.String()))
	}

	if err := checkText("name", r.Name, MaxNameLength); err != nil {
		return err
	}

	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: missing creation time", ErrInvalid)
	}

	if !ident.Valid(r.PrincipalID, maxPrincipalIDLength) {
		return fmt.Errorf("%w: the principal ID %s is not a principal ID", ErrInvalid, ident.Echo(r.PrincipalID))
	}

	if err := checkText("audience", r.Audience, MaxAudienceLength); err != nil {
		return err
	}

	if err := r.validateKeys(); err != nil {
		return err
	}

	return ValidateConditions(r.Conditions)
}

// validateKeys checks the issuer and the key source of r.
func (r *Rule) validateKeys() error {
	switch r.Keys {
	case KeySourceDiscovery:
		if r.StaticKeys != "" {
			return fmt.Errorf("%w: a rule with key source discovery holds no static keys", ErrInvalid)
		}

		return ValidateDiscoveryIssuer(r.Issuer)
	case KeySourceStatic:
		if err := checkText("issuer", r.Issuer, MaxIssuerLength); err != nil {
			return err
		}

		if len(r.StaticKeys) > MaxStaticKeysBytes {
			return fmt.Errorf("%w: the static keys are larger than %d bytes", ErrInvalid, MaxStaticKeysBytes)
		}

		if _, err := ParseKeySet([]byte(r.StaticKeys)); err != nil {
			return fmt.Errorf("%w: static keys: %w", ErrInvalid, err)
		}

		return nil
	default:
		return fmt.Errorf("%w: key source %s: use discovery or static", ErrInvalid, ident.Echo(string(r.Keys)))
	}
}

// ValidateDiscoveryIssuer returns ErrInvalid for an issuer that the server
// cannot read a discovery document from by its form: an issuer that is not
// an https URL with a host, that holds user information, a query or a
// fragment, or that is longer than MaxIssuerLength. The service checks the
// issuer against the egress rules of the server when a person saves a rule,
// and the egress rules check the address of each connection again when it
// opens.
func ValidateDiscoveryIssuer(issuer string) error {
	if err := checkText("issuer", issuer, MaxIssuerLength); err != nil {
		return err
	}

	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || u.ForceQuery || u.Opaque != "" {
		return fmt.Errorf("%w: the issuer %s is not an https URL with a host and no user, query or fragment",
			ErrInvalid, ident.Echo(issuer))
	}

	return nil
}

// ValidateConditions returns ErrInvalid for conditions that a rule cannot
// hold: none, more than MaxConditions, no condition on SubjectClaim, a claim
// name that is not 1 to MaxClaimLength ASCII letters, digits, '_', '-', '.'
// or ':', a reserved claim, a claim that two conditions name, conditions
// that are not in the order of their claim names, or a condition with no
// value, with more than MaxValues values, with a value that is empty, longer
// than MaxValueLength bytes, not UTF-8 or holds a control or format
// character, or with a value twice.
func ValidateConditions(cs []Condition) error {
	if len(cs) == 0 || len(cs) > MaxConditions {
		return fmt.Errorf("%w: a rule holds 1 to %d conditions", ErrInvalid, MaxConditions)
	}

	hasSubject := false

	for i := range cs {
		c := &cs[i]

		if err := checkClaimName(c.Claim); err != nil {
			return err
		}

		if i > 0 && cs[i-1].Claim >= c.Claim {
			return fmt.Errorf("%w: the conditions are not in the order of their claim names, or a claim is twice",
				ErrInvalid)
		}

		if c.Claim == SubjectClaim {
			hasSubject = true
		}

		if err := checkValues(c); err != nil {
			return err
		}
	}

	if !hasSubject {
		return fmt.Errorf("%w: a rule needs a condition on the claim %q", ErrInvalid, SubjectClaim)
	}

	return nil
}

// checkClaimName returns ErrInvalid for a claim name that a condition cannot
// name.
func checkClaimName(name string) error {
	if name == "" || len(name) > MaxClaimLength {
		return fmt.Errorf("%w: a claim name is 1 to %d characters", ErrInvalid, MaxClaimLength)
	}

	for _, c := range []byte(name) {
		if !claimNameByte(c) {
			return fmt.Errorf("%w: the claim name %s: use only ASCII letters, digits, '_', '-', '.' and ':' in it",
				ErrInvalid, ident.Echo(name))
		}
	}

	if slices.Contains(reservedClaims(), name) {
		return fmt.Errorf("%w: the claim %q is checked by the exchange, so a condition cannot name it",
			ErrInvalid, name)
	}

	return nil
}

// claimNameByte reports whether c can be in a claim name.
func claimNameByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
		c == '_' || c == '-' || c == '.' || c == ':'
}

// checkValues returns ErrInvalid for the values of c that a condition cannot
// hold.
func checkValues(c *Condition) error {
	if len(c.Values) == 0 || len(c.Values) > MaxValues {
		return fmt.Errorf("%w: the condition on %q holds 1 to %d values", ErrInvalid, c.Claim, MaxValues)
	}

	for i, v := range c.Values {
		if err := checkText("value of "+c.Claim, v, MaxValueLength); err != nil {
			return err
		}

		if slices.Contains(c.Values[:i], v) {
			return fmt.Errorf("%w: the condition on %q holds the value %s twice", ErrInvalid, c.Claim, ident.Echo(v))
		}
	}

	return nil
}

// checkText returns ErrInvalid when s is empty, longer than maxLen bytes, not
// UTF-8, or holds a control or format character or white space at the start
// or the end.
func checkText(field, s string, maxLen int) error {
	switch {
	case s == "":
		return fmt.Errorf("%w: missing %s", ErrInvalid, field)
	case len(s) > maxLen:
		return fmt.Errorf("%w: the %s is longer than %d bytes", ErrInvalid, field, maxLen)
	case !utf8.ValidString(s):
		return fmt.Errorf("%w: the %s is not UTF-8", ErrInvalid, field)
	case strings.TrimSpace(s) != s:
		return fmt.Errorf("%w: the %s starts or ends with white space", ErrInvalid, field)
	}

	for _, c := range s {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return fmt.Errorf("%w: the %s holds the control or format character %U", ErrInvalid, field, c)
		}
	}

	return nil
}

// SortConditions returns a copy of cs in the order of their claim names, with
// the values of each condition in the order that they came.
func SortConditions(cs []Condition) []Condition {
	out := make([]Condition, 0, len(cs))
	for _, c := range cs {
		out = append(out, Condition{Claim: c.Claim, Values: slices.Clone(c.Values)})
	}

	slices.SortStableFunc(out, func(a, b Condition) int { return strings.Compare(a.Claim, b.Claim) })

	return out
}
