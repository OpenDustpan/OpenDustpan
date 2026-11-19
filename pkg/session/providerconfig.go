package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Errors of the sign-in providers that the API manages.
var (
	// ErrProviderNotFound reports a provider ID with no stored provider.
	ErrProviderNotFound = errors.New("sign-in provider not found")
	// ErrProviderExists reports a provider ID that is already in use.
	ErrProviderExists = errors.New("sign-in provider already exists")
	// ErrProviderChanged reports a change of a provider whose stored
	// version is not the version that the change read.
	ErrProviderChanged = errors.New("sign-in provider changed since it was read")
	// ErrProviderInUse reports a delete of a provider ID that a principal
	// has in its subject.
	ErrProviderInUse = errors.New("sign-in provider has principals")
	// ErrInvalidProvider reports a provider that fails validation.
	ErrInvalidProvider = errors.New("invalid sign-in provider")
	// ErrProviderDisabled reports a stored provider that is disabled.
	ErrProviderDisabled = errors.New("sign-in provider is disabled")
)

// Limits of a stored provider.
const (
	maxDisplayName  = 64
	maxClientID     = 256
	maxIssuer       = 512
	maxScopes       = 16
	maxScope        = 64
	maxSealedSecret = 4096
)

// providerIDPattern is the form of a provider ID: a lowercase letter, then
// up to 31 lowercase letters, digits and hyphens.
var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidProviderID reports whether id has the form of a provider ID.
func ValidProviderID(id string) bool {
	return providerIDPattern.MatchString(id)
}

// DevProviderID is the provider ID that the dev sign-in of opendustpan dev
// uses for its sessions. No configured or stored provider can have it, so a
// provider cannot compose the subject of the dev person ("dev:admin").
const DevProviderID = "dev"

// LocalProviderID is the provider ID that the password sign-in of a local
// user records. No identity provider can take it.
const LocalProviderID = "local"

// SubjectPrefix returns the prefix of the subject of each person who signs
// in with the provider id: the subject is "<provider ID>:<sub>".
func SubjectPrefix(id string) string {
	return id + ":"
}

// ProviderConfig is a sign-in provider that the API manages. SealedSecret
// is the client secret, sealed in the installation scope of the provider,
// never the secret itself. Version counts the changes of the provider. A
// provider that is not Enabled refuses each sign-in. A provider in
// ModeOIDC has an Issuer and empty OAuth settings; a provider in ModeOAuth
// has OAuth settings and an empty Issuer.
type ProviderConfig struct {
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ID           string
	Mode         ProviderMode
	Issuer       string
	DisplayName  string
	ClientID     string
	SealedSecret []byte
	Scopes       []string
	OAuth        OAuthSettings
	Version      int64
	AutoCreate   bool
	Enabled      bool
}

// Binding returns what identifies the people of p, for BindProviders.
func (p ProviderConfig) Binding() Provider {
	if p.Mode == ModeOAuth {
		return Provider{
			ID:           p.ID,
			Issuer:       "",
			UserinfoURL:  p.OAuth.UserinfoURL,
			SubjectClaim: p.OAuth.SubjectClaim,
			DirectoryURL: "",
		}
	}

	return Provider{ID: p.ID, Issuer: p.Issuer, UserinfoURL: "", SubjectClaim: "", DirectoryURL: ""}
}

// Validate returns ErrInvalidProvider for a provider that a store must
// not keep.
func (p ProviderConfig) Validate() error {
	if err := p.validateNames(); err != nil {
		return err
	}

	switch {
	case len(p.SealedSecret) == 0 || len(p.SealedSecret) > maxSealedSecret:
		return fmt.Errorf("%w: the sealed client secret must have 1 to %d bytes", ErrInvalidProvider, maxSealedSecret)
	case p.Version < 1:
		return fmt.Errorf("%w: the version must be 1 or more", ErrInvalidProvider)
	case p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt):
		return fmt.Errorf("%w: the provider needs a creation time no later than its update time", ErrInvalidProvider)
	default:
		return validScopes(p.Scopes)
	}
}

// validateNames checks the ID, the mode with its issuer or OAuth
// settings, the display name and the client ID of p.
func (p ProviderConfig) validateNames() error {
	if !ValidProviderID(p.ID) {
		return fmt.Errorf("%w: the provider ID %q does not match %s", ErrInvalidProvider, p.ID, providerIDPattern)
	}

	if p.ID == DevProviderID {
		return fmt.Errorf("%w: the provider ID %q is reserved for the dev sign-in", ErrInvalidProvider, p.ID)
	}

	if p.ID == LocalProviderID {
		return fmt.Errorf("%w: the provider ID %q is reserved for the local users", ErrInvalidProvider, p.ID)
	}

	if err := p.validateMode(); err != nil {
		return err
	}

	switch {
	case p.DisplayName == "" || len(p.DisplayName) > maxDisplayName || hasControl(p.DisplayName):
		return fmt.Errorf("%w: the display name must have 1 to %d bytes and no control character",
			ErrInvalidProvider, maxDisplayName)
	case p.ClientID == "" || len(p.ClientID) > maxClientID || hasControl(p.ClientID):
		return fmt.Errorf("%w: the client ID must have 1 to %d bytes and no control character",
			ErrInvalidProvider, maxClientID)
	default:
		return nil
	}
}

// validateMode checks the mode of p and the issuer or the OAuth settings
// that the mode needs.
func (p ProviderConfig) validateMode() error {
	switch p.Mode {
	case ModeOIDC:
		if !p.OAuth.isZero() {
			return fmt.Errorf("%w: a provider in the mode %q has no OAuth endpoints or claims", ErrInvalidProvider,
				ModeOIDC)
		}

		if !validIssuer(p.Issuer) {
			return fmt.Errorf("%w: the issuer must be an absolute https URL, or http on a loopback host, "+
				"of at most %d bytes", ErrInvalidProvider, maxIssuer)
		}

		return nil
	case ModeOAuth:
		if p.Issuer != "" {
			return fmt.Errorf("%w: a provider in the mode %q has no issuer", ErrInvalidProvider, ModeOAuth)
		}

		return p.OAuth.validate()
	case ModeSAML, ModeCAS, ModeLDAP, ModeHeader:
		// Fail closed: the mode has no settings and no method yet, so a
		// stored provider of the mode could sign no person in.
		return fmt.Errorf("%w: the mode %q has no sign-in method in this version; the mode must be %q or %q",
			ErrInvalidProvider, p.Mode, ModeOIDC, ModeOAuth)
	default:
		return fmt.Errorf("%w: the mode must be %q or %q", ErrInvalidProvider, ModeOIDC, ModeOAuth)
	}
}

// ErrIssuer reports an OpenID Connect issuer URL that the server refuses.
// Its text never holds the issuer, because the issuer can hold a password.
var ErrIssuer = errors.New("the issuer must be an absolute https URL, or http on a loopback host, with a host " +
	"and with no user information, no query and no fragment, so set the issuer (--oidc-issuer) to that form")

// CheckIssuer returns an error that matches ErrIssuer for an issuer that is
// not an absolute URL of this form: the scheme is exactly "https", or exactly
// "http" with a loopback host; the host is a list of ASCII letter, digit and
// hyphen labels with no trailing dot, or an IP address in the form of a
// literal; the port, if present, is from 1 to 65535; each path segment has
// only the characters of a URL path segment, and no segment is "." or "..".
// It refuses user information, a query, a fragment, a space, a control
// character, a byte of 0x7f or more and a text of more than 512 bytes. It does
// not change the issuer to make it valid.
func CheckIssuer(issuer string) error {
	if issuer == "" || len(issuer) > maxIssuer || !issuerBytesAllowed(issuer) {
		return ErrIssuer
	}

	scheme, rest, found := strings.Cut(issuer, "://")
	if !found || (scheme != schemeHTTPS && scheme != schemeHTTP) {
		return ErrIssuer
	}

	authority, path, _ := strings.Cut(rest, "/")
	if path != "" || strings.HasSuffix(rest, "/") {
		path = "/" + path
	}

	host, ok := issuerHost(authority)
	if !ok || (scheme == schemeHTTP && !loopbackHost(host)) || !issuerPathAllowed(path) {
		return ErrIssuer
	}

	return nil
}

// issuerBytesAllowed reports whether each byte of s is a printable ASCII
// character that is not a space, a back slash, a query mark or a fragment mark.
func issuerBytesAllowed(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c <= 0x20 || c >= 0x7f || c == '\\' || c == '?' || c == '#' {
			return false
		}
	}

	return true
}

// issuerHost checks the authority of an issuer and returns its host with no
// brackets. It refuses user information, an empty host and a port that is not
// from 1 to 65535.
func issuerHost(authority string) (string, bool) {
	if authority == "" || strings.Contains(authority, "@") {
		return "", false
	}

	host, port := authority, ""

	if strings.HasPrefix(authority, "[") {
		end := strings.Index(authority, "]")
		if end < 0 {
			return "", false
		}

		host, port = authority[1:end], authority[end+1:]

		a, err := netip.ParseAddr(host)
		if err != nil || a.Zone() != "" {
			return "", false
		}

		if port != "" && (port[0] != ':' || !issuerPort(port[1:])) {
			return "", false
		}

		return host, true
	}

	if i := strings.LastIndex(authority, ":"); i >= 0 {
		host, port = authority[:i], authority[i+1:]
		if !issuerPort(port) {
			return "", false
		}
	}

	if !issuerDomain(host) {
		return "", false
	}

	return host, true
}

// issuerPort reports whether p is a port from 1 to 65535 in decimal digits.
func issuerPort(p string) bool {
	if p == "" || len(p) > 5 {
		return false
	}

	n := 0

	for i := range len(p) {
		if p[i] < '0' || p[i] > '9' {
			return false
		}

		n = n*10 + int(p[i]-'0')
	}

	return n >= 1 && n <= 65535
}

// issuerDomain reports whether host is an IPv4 address, or a list of ASCII
// letter, digit and hyphen labels with no trailing dot. A host of only digits
// and dots must be an IPv4 address.
func issuerDomain(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}

	if strings.Trim(host, "0123456789.") == "" {
		a, err := netip.ParseAddr(host)

		return err == nil && a.Is4()
	}

	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		for i := range len(label) {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}

	return true
}

// issuerPathAllowed reports whether each segment of path has only the
// characters of a URL path segment, with a percent sign only before two hex
// digits, and is not "." or ".." in plain or in percent-encoded form.
func issuerPathAllowed(path string) bool {
	if path == "" {
		return true
	}

	for seg := range strings.SplitSeq(path[1:], "/") {
		if !issuerSegmentAllowed(seg) {
			return false
		}

		dec, err := url.PathUnescape(seg)
		if err != nil || dec == "." || dec == ".." {
			return false
		}
	}

	return true
}

// issuerSegmentAllowed reports whether seg has only unreserved characters,
// sub-delimiters, colon, at sign and well-formed percent escapes.
func issuerSegmentAllowed(seg string) bool {
	for i := 0; i < len(seg); i++ {
		c := seg[i]

		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("-._~!$&'()*+,;=:@", c) >= 0:
		case c == '%' && i+2 < len(seg) && isHex(seg[i+1]) && isHex(seg[i+2]):
			i += 2
		default:
			return false
		}
	}

	return true
}

// isHex reports whether c is a hexadecimal digit.
func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// secureScheme reports whether u uses https, or http on a loopback host.
func secureScheme(u *url.URL) bool {
	return u.Scheme == schemeHTTPS || (u.Scheme == schemeHTTP && loopbackHost(u.Hostname()))
}

// validIssuer reports whether issuer passes CheckIssuer. The sign-in client
// applies the same scheme rule when it fetches the discovery document, so a
// stored issuer never fails it.
func validIssuer(issuer string) bool {
	return CheckIssuer(issuer) == nil
}

// loopbackHost reports whether host is localhost or a loopback IP address.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// validScopes refuses more than maxScopes scopes, an empty scope, a scope
// over maxScope bytes, and a scope with a space or a control character.
func validScopes(scopes []string) error {
	if len(scopes) > maxScopes {
		return fmt.Errorf("%w: at most %d scopes", ErrInvalidProvider, maxScopes)
	}

	for _, s := range scopes {
		if s == "" || len(s) > maxScope || strings.ContainsAny(s, " ") || hasControl(s) {
			return fmt.Errorf("%w: each scope must have 1 to %d bytes and no space", ErrInvalidProvider, maxScope)
		}
	}

	return nil
}

// hasControl reports whether s has a control character.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// ProviderConfigStore keeps the sign-in providers that the API manages.
// Each change appends its audit entry e in the scope @server in the same
// transaction, so the change and its entry commit together or not at all.
type ProviderConfigStore interface {
	// CreateProviderConfig stores p. It returns ErrProviderExists when the
	// ID is in use and ErrInvalidProvider for a provider that fails
	// Validate.
	CreateProviderConfig(ctx context.Context, p ProviderConfig, e audit.Entry) error
	// GetProviderConfig returns the provider id, or ErrProviderNotFound.
	GetProviderConfig(ctx context.Context, id string) (ProviderConfig, error)
	// ListProviderConfigs returns every stored provider, ordered by ID. It
	// returns an empty list when the store holds none.
	ListProviderConfigs(ctx context.Context) ([]ProviderConfig, error)
	// UpdateProviderConfig replaces the provider p.ID with p when its
	// stored version is p.Version-1. It returns ErrProviderNotFound for an
	// unknown ID, ErrProviderChanged for another stored version, and
	// ErrInvalidProvider for a provider that fails Validate. The creation
	// time stays. When p is disabled, the same write ends every active
	// session of a principal whose subject starts with SubjectPrefix(p.ID),
	// at e.At with ReasonEnded, and writes e through WithSessionsEnded.
	UpdateProviderConfig(ctx context.Context, p ProviderConfig, e audit.Entry) error
	// DisableProviderConfig sets the provider id to disabled when its stored
	// version is version, and sets the version to version+1 and the update
	// time to updatedAt. Nothing else of the provider changes. In the same
	// write it ends every active session of a principal whose subject starts
	// with SubjectPrefix(id), at e.At with ReasonEnded, and writes e through
	// WithSessionsEnded. It only narrows access, so it runs in maintenance
	// mode, and UpdateProviderConfig, which can enable a provider and change
	// its issuer and client, does not. It returns ErrProviderNotFound for an
	// unknown ID and ErrProviderChanged for another stored version.
	DisableProviderConfig(ctx context.Context, id string, version int64, updatedAt time.Time, e audit.Entry) error
	// DeleteProviderConfig deletes the provider id. It returns
	// ErrProviderNotFound for an unknown ID, and ErrProviderInUse while a
	// principal, also a deleted one, has a subject that starts with
	// SubjectPrefix(id).
	DeleteProviderConfig(ctx context.Context, id string, e audit.Entry) error
}

// ProviderSessionStore creates the sessions of the stored providers.
type ProviderSessionStore interface {
	// CreateProviderSession is Store.CreateSession for a session of the
	// stored provider s.Provider. In the same transaction, before the
	// insert, it reads the provider with a shared lock, so a disable that
	// commits first refuses the session, and a disable that commits later
	// ends it. It returns ErrProviderNotFound when the provider is gone and
	// ErrProviderDisabled when it is disabled, and then changes nothing.
	// Else it returns the errors of Store.CreateSession.
	CreateProviderSession(ctx context.Context, s Session, d Digest, e audit.Entry) error
}

// ChangeSessionsEnded is the key of the audit change that counts the
// sessions that the disable of a sign-in provider ended.
const ChangeSessionsEnded = "sessionsEnded"

// WithSessionsEnded returns e with the change ChangeSessionsEnded when n
// sessions ended, and e as it is when n is 0. A store calls it inside the
// write that ends the sessions, so the entry holds the count of that write.
func WithSessionsEnded(e audit.Entry, n int64) audit.Entry {
	if n == 0 {
		return e
	}

	e.Changes = append(slices.Clone(e.Changes), audit.Change{
		Key: ChangeSessionsEnded, Old: nil, New: strconv.FormatInt(n, 10), Layer: "", TakesEffect: "",
	})

	return e
}
