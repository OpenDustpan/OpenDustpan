// Package trustrule keeps the workload trust rules of each namespace.
//
// A trust rule lets a workload, for example a CI job, exchange a JWT from
// its own issuer for a short-lived OpenDustpan access token. The rule names
// the issuer, how the server gets the signing keys of the issuer, the exact
// audience, the conditions on the claims of the JWT, and the principal that a
// match acts as. The grants of that principal decide what the workload can
// do. A rule belongs to one namespace, and its principal is a principal of
// that namespace, so a rule never gives access to another namespace. A rule
// may also name the trust bundle of its namespace and a SPIFFE ID pattern,
// for the mTLS check of a client certificate.
//
// Each change writes its audit entry in the same transaction as the change
// (fail closed), and each write carries the fence of its namespace.
package trustrule

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
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
	// exist, it is not a principal of the namespace of the rule, it is not
	// a service principal, or it is disabled.
	ErrPrincipal = errors.New("the principal cannot be the principal of a trust rule")
	// ErrCrossNamespace reports a write of one namespace that names an
	// object of another namespace: for example a rule whose principal is a
	// principal of another namespace. It also wraps ErrPrincipal. The
	// caller may not write outside the namespace of the path, so the write
	// is refused as a denial, not as input that is not valid.
	ErrCrossNamespace = errors.New("the write names an object of another namespace")
	// ErrIssuerRefused reports an issuer of this installation. A token that
	// this installation signs never becomes an access token of it.
	ErrIssuerRefused = errors.New("the issuer is an issuer of this installation")
	// ErrBundle reports a trust bundle that fails its checks.
	ErrBundle = errors.New("invalid trust bundle")
	// ErrBundleActive reports the delete of the active version of a trust
	// bundle. Only a retired version can be deleted.
	ErrBundleActive = errors.New("the active version of a trust bundle cannot be deleted")
	// ErrStale reports a write with a stale revision: the If-Match header
	// does not match the stored revision.
	ErrStale = errors.New("the bundle changed since it was read")
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
	// MaxBundleBytes is the largest trust bundle that a save accepts, in
	// bytes of PEM text.
	MaxBundleBytes = 64 << 10
	// MaxBundleCAs is the most CA certificates that one trust bundle holds.
	MaxBundleCAs = 10
	// MaxBundleVersions is the most versions that the store keeps for one
	// trust bundle. An older version past this count is retired.
	MaxBundleVersions = 5
	// MaxOverlap is the longest overlap of a rotation, from the time of the
	// rotate to overlapEndsAt.
	MaxOverlap = 24 * time.Hour
	// MaxSpiffePatternLength is the longest SPIFFE ID pattern of a rule, in
	// bytes.
	MaxSpiffePatternLength = 512
	// maxIDLength bounds a rule ID.
	maxIDLength = 64
	// maxPrincipalIDLength bounds the ID of the principal of a rule.
	maxPrincipalIDLength = 64
	// maxBundleIDLength bounds the ID of a trust bundle.
	maxBundleIDLength = 64
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
// of the principal that made the rule. Revision counts the writes of the
// rule, starting at 1. SpiffeIDPattern is the SPIFFE ID pattern of the mTLS
// check, empty when the rule has no mTLS check. Bundle is the ID of the
// trust bundle of the mTLS check, empty with no check.
type Rule struct {
	CreatedAt       time.Time
	ID              ID
	Namespace       namespace.Name
	Name            string
	Issuer          string
	Audience        string
	Keys            KeySource
	StaticKeys      string
	PrincipalID     string
	CreatedBy       string
	Conditions      []Condition
	Revision        int64
	SpiffeIDPattern string
	Bundle          string
}

// BundleID is the stable, opaque ID of a trust bundle: the prefix "trb-"
// and 32 lower-case hex digits. Never parse it.
type BundleID string

// NewBundleID returns a new trust bundle ID from the system random source.
func NewBundleID() BundleID {
	var b [16]byte

	// crypto/rand.Read does not return an error on any supported platform.
	_, _ = rand.Read(b[:])

	return BundleID("trb-" + hex.EncodeToString(b[:]))
}

// Validate returns ErrBundle when id is not the form of a trust bundle ID.
func (id BundleID) Validate() error {
	s := string(id)
	if !strings.HasPrefix(s, "trb-") || len(s) == len("trb-") || !ident.Valid(s, maxBundleIDLength) {
		return fmt.Errorf("%w: the ID %s is not a trust bundle ID", ErrBundle, ident.Echo(s))
	}

	return nil
}

// BundleRef returns the audit object of the bundle id:
// "trust-bundle:<id>". An id that is not a bundle ID gives
// "trust-bundle:invalid", so request text never reaches the audit log.
func BundleRef(id BundleID) string {
	if id.Validate() != nil {
		return "trust-bundle:invalid"
	}

	return "trust-bundle:" + string(id)
}

// BundleVersion is one version of the trust bundle of a namespace.
// Certificates holds the parsed CA certificates of the version. CreatedAt is
// the time of the save. OverlapEndsAt is the end of the rotation overlap of
// a rotated version, and zero for the first save.
type BundleVersion struct {
	Number        int64
	Certificates  []*x509.Certificate
	CreatedAt     time.Time
	OverlapEndsAt time.Time
}

// Bundle is the trust bundle of a namespace: the append-only versions and
// the active pointer. Revision counts the writes of the bundle, starting at
// 1. The ETag of a read is the revision in quotes.
type Bundle struct {
	ID        BundleID
	Namespace namespace.Name
	Active    int64
	Versions  []BundleVersion
	Revision  int64
	CreatedAt time.Time
}

// ETag returns the entity tag of the bundle or of the rule revision: the
// revision in quotes.
func ETag(revision int64) string {
	return `"` + strconv.FormatInt(revision, 10) + `"`
}

// BundleFields holds the fields of a trust bundle write.
type BundleFields struct {
	// PEM is the bundle in PEM text.
	PEM string
	// Overlap is the rotation overlap of a rotate, zero for a save. It must
	// not exceed MaxOverlap.
	Overlap time.Duration
	// Revision is the stored revision that a rotate expects, zero for a save.
	Revision int64
}

// ParseBundle checks the PEM text of a bundle save and returns its CA
// certificates: each certificate in the order of its block, with the index
// for error text. It refuses input outside the size bounds, text outside a
// certificate block, a block that is not a certificate, a certificate that
// fails to parse, a certificate that is not a CA, and more than MaxBundleCAs
// certificates. A CA needs BasicConstraintsValid and IsCA, KeyUsageCertSign,
// NotBefore at or before now, and NotAfter after now. Now is the clock that
// validity is checked against.
func ParseBundle(pemText string, now time.Time) ([]*x509.Certificate, error) {
	certs, err := decodeBundle(pemText, now)
	if err != nil {
		return nil, err
	}

	for i, cert := range certs {
		if now.After(cert.NotAfter) {
			return nil, fmt.Errorf("%w: CA %d expired at %s", ErrBundle, i,
				cert.NotAfter.UTC().Format(time.RFC3339))
		}
	}

	return certs, nil
}

// ParseStoredBundle checks the PEM text of a stored bundle version like
// ParseBundle, but it does not check expiry: a stored version stays readable
// after one of its CAs expires, and the exchange refuses an expired CA when
// it builds a chain. It still refuses a certificate that is not a CA and a
// CA that is not yet valid at now.
func ParseStoredBundle(pemText string, now time.Time) ([]*x509.Certificate, error) {
	return decodeBundle(pemText, now)
}

func decodeBundle(pemText string, now time.Time) ([]*x509.Certificate, error) {
	if pemText == "" || len(pemText) > MaxBundleBytes {
		return nil, fmt.Errorf("%w: the bundle is 1 to %d bytes, got %d", ErrBundle, MaxBundleBytes, len(pemText))
	}

	rest := strings.TrimSpace(pemText)

	var out []*x509.Certificate

	for rest != "" {
		cert, next, err := decodeBundleBlock(rest, now, len(out))
		if err != nil {
			return nil, err
		}

		out = append(out, cert)

		if len(out) > MaxBundleCAs {
			return nil, fmt.Errorf("%w: the bundle holds more than %d CAs", ErrBundle, MaxBundleCAs)
		}

		rest = strings.TrimSpace(next)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: the bundle holds no certificate", ErrBundle)
	}

	return out, nil
}

// decodeBundleBlock decodes the first PEM block of rest and checks the CA at
// index. It returns the certificate and the text after the block.
func decodeBundleBlock(rest string, now time.Time, index int) (*x509.Certificate, string, error) {
	if !strings.HasPrefix(rest, "-----BEGIN CERTIFICATE-----") {
		return nil, "", fmt.Errorf("%w: CA %d is not PEM", ErrBundle, index)
	}

	block, next := pem.Decode([]byte(rest))
	if block == nil {
		return nil, "", fmt.Errorf("%w: CA %d is not PEM", ErrBundle, index)
	}

	// Defense in depth: the prefix check above already requires a
	// CERTIFICATE block, and the handler checks the shape again; this
	// check stays so that a new caller cannot skip it.
	if block.Type != "CERTIFICATE" {
		return nil, "", fmt.Errorf("%w: CA %d is a %s block, not a certificate", ErrBundle, index, block.Type)
	}

	if len(block.Headers) > 0 {
		return nil, "", fmt.Errorf("%w: CA %d holds PEM headers", ErrBundle, index)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("%w: CA %d does not parse: %w", ErrBundle, index, err)
	}

	if err := checkBundleCA(cert, now, index); err != nil {
		return nil, "", err
	}

	return cert, string(next), nil
}

// checkBundleCA returns ErrBundle when cert is not a usable CA at index:
// not a CA, without KeyUsageCertSign, or not yet valid at now.
func checkBundleCA(cert *x509.Certificate, now time.Time, index int) error {
	if !cert.BasicConstraintsValid || !cert.IsCA {
		return fmt.Errorf("%w: CA %d is not a CA", ErrBundle, index)
	}

	if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return fmt.Errorf("%w: CA %d cannot sign certificates", ErrBundle, index)
	}

	if now.Before(cert.NotBefore) {
		return fmt.Errorf("%w: CA %d is not valid before %s", ErrBundle, index,
			cert.NotBefore.UTC().Format(time.RFC3339))
	}

	return nil
}

// ValidateSpiffePattern returns ErrInvalid when pattern is not a usable
// SPIFFE ID pattern: empty, longer than MaxSpiffePatternLength bytes, or
// not of the form "spiffe://<trust-domain>/<path>". The trust domain is
// lowercase [a-z0-9._-] with no port and no wildcard. Each path segment is
// [A-Za-z0-9._-], not empty, not "." or "..", with no percent. A "*" is
// allowed only as the whole last segment, and a pattern needs at least one
// fixed segment before it, so "spiffe://td/*" is refused. The pattern must
// not end with a slash.
func ValidateSpiffePattern(pattern string) error {
	if err := checkText("spiffePattern", pattern, MaxSpiffePatternLength); err != nil {
		return err
	}

	u, err := url.Parse(pattern)
	if err != nil || !spiffeShape(u) {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s is not spiffe://<trust-domain>/<path>",
			ErrInvalid, ident.Echo(pattern))
	}

	rest := strings.TrimPrefix(pattern, "spiffe://")

	slash := strings.Index(rest, "/")
	if slash < 0 {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s is not spiffe://<trust-domain>/<path>",
			ErrInvalid, ident.Echo(pattern))
	}

	domain, rawPath := rest[:slash], rest[slash:]

	if !validSpiffeDomain(domain) {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s holds a trust domain outside [a-z0-9._-]",
			ErrInvalid, ident.Echo(pattern))
	}

	if u.Host != domain || u.Port() != "" {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s holds a port or an escaped trust domain",
			ErrInvalid, ident.Echo(pattern))
	}

	if err := checkSpiffePath(rawPath, pattern); err != nil {
		return err
	}

	return checkSpiffeSegments(strings.Split(rawPath, "/")[1:], pattern)
}

// checkSpiffePath returns ErrInvalid when the raw path of a SPIFFE ID
// pattern ends with a slash or holds a percent escape.
func checkSpiffePath(rawPath, pattern string) error {
	if strings.HasSuffix(rawPath, "/") {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s ends with a slash",
			ErrInvalid, ident.Echo(pattern))
	}

	if strings.Contains(rawPath, "%") {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s holds an escape",
			ErrInvalid, ident.Echo(pattern))
	}

	return nil
}

// checkSpiffeSegments returns ErrInvalid when a path segment is not usable:
// a "*" is allowed only as the whole last segment after at least one fixed
// segment, and any other segment is [A-Za-z0-9._-], not empty, not "." and
// not "..".
func checkSpiffeSegments(segments []string, pattern string) error {
	if len(segments) == 0 {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s holds no path",
			ErrInvalid, ident.Echo(pattern))
	}

	for i := range segments {
		if err := checkSpiffeSegment(segments, i, pattern); err != nil {
			return err
		}
	}

	return nil
}

// checkSpiffeSegment returns ErrInvalid when the segment at index i of a
// SPIFFE ID pattern path is not usable.
func checkSpiffeSegment(segments []string, i int, pattern string) error {
	seg := segments[i]
	last := i == len(segments)-1

	if seg == "*" {
		if !last || len(segments) < 2 {
			return fmt.Errorf("%w: the SPIFFE ID pattern %s holds * outside a trailing /* after a fixed segment",
				ErrInvalid, ident.Echo(pattern))
		}

		return nil
	}

	if seg == "" || seg == "." || seg == ".." {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s holds an empty, dot or dot-dot segment",
			ErrInvalid, ident.Echo(pattern))
	}

	if !validSpiffeSegment(seg) {
		return fmt.Errorf("%w: the SPIFFE ID pattern %s holds a segment outside [A-Za-z0-9._-]",
			ErrInvalid, ident.Echo(pattern))
	}

	return nil
}

// spiffeShape reports whether u has only a scheme, a host and a path.
func spiffeShape(u *url.URL) bool {
	if u.Scheme != "spiffe" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return false
	}

	if u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return false
	}

	return strings.HasPrefix(u.Path, "/")
}

// validSpiffeDomain reports whether d is a trust domain: lowercase
// [a-z0-9._-], with no port and no wildcard.
func validSpiffeDomain(d string) bool {
	if d == "" {
		return false
	}

	for _, c := range d {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}

	return true
}

// validSpiffeSegment reports whether s is a fixed path segment:
// [A-Za-z0-9._-], not empty, not "." or "..".
func validSpiffeSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}

	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}

	return true
}

// CheckBundleRef returns ErrBundle when bundle is set but is not a bundle
// ID, so the store never reads another namespace through a malformed
// reference.
func CheckBundleRef(bundle string) error {
	if bundle == "" {
		return nil
	}

	return BundleID(bundle).Validate()
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

	if r.Revision < 0 {
		return fmt.Errorf("%w: the revision %d is negative", ErrInvalid, r.Revision)
	}

	if r.SpiffeIDPattern != "" {
		if err := ValidateSpiffePattern(r.SpiffeIDPattern); err != nil {
			return err
		}
	}

	if err := CheckBundleRef(r.Bundle); err != nil {
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

	if r, found := ident.FirstHidden(s); found {
		return fmt.Errorf("%w: the %s holds the control, format or separator character %U", ErrInvalid, field, r)
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
