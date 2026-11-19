package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrIssuerChanged reports a provider ID of the console sign-in whose
// issuer is not the issuer that the store recorded for it.
var ErrIssuerChanged = errors.New("the issuer of a sign-in provider changed")

// ProviderStore keeps the issuer of each provider ID of the console
// sign-in. The subject of a person holds the provider ID and the sub claim,
// not the issuer, so one provider ID must keep one issuer.
type ProviderStore interface {
	// BindProvider records issuer for the provider ID id at the time at,
	// when id has no record, and returns the issuer of the record: issuer
	// itself after the first call, or the issuer of an earlier call. It
	// writes no audit entry.
	BindProvider(ctx context.Context, id, issuer string, at time.Time) (string, error)
}

// issuerChangedError is the refusal of a provider ID whose issuer changed.
// Its text is one sentence that names the provider ID, both issuers and
// the fix: in most cases the configuration has a mistake, and a new
// provider ID would give each person a new principal.
type issuerChangedError struct {
	id, recorded, configured string
}

func (e issuerChangedError) Error() string {
	// A stored row from an older version or a restored backup can hold an
	// issuer that the rule refuses, with user information. Its text is not
	// shown, and the advice is not to restore it.
	if CheckIssuer(e.recorded) != nil {
		return fmt.Sprintf("sign-in provider %q has an issuer in the database that the server now refuses, so "+
			"disable the provider and set a new provider ID for the issuer in the configuration", e.id)
	}

	return fmt.Sprintf("sign-in provider %q has the issuer %q in the database and the issuer %q in the "+
		"configuration, so set the issuer (--oidc-issuer) back to %q, or set a new provider ID only if the "+
		"identity provider changed", e.id, e.recorded, e.configured, e.recorded)
}

// Unwrap returns ErrIssuerChanged.
func (issuerChangedError) Unwrap() error { return ErrIssuerChanged }

// Provider is one provider of the console sign-in: its ID and what
// identifies its people. For an OpenID provider that is its Issuer. For a
// plain OAuth 2.0 provider, Issuer is empty, and its UserinfoURL and
// SubjectClaim identify its people instead. For an LDAP provider, Issuer is
// empty and DirectoryURL, the URL of its directory, identifies its people.
type Provider struct {
	ID           string
	Issuer       string
	UserinfoURL  string
	SubjectClaim string
	DirectoryURL string
}

// directoryBindingPrefix starts the binding of an LDAP provider, so that the
// binding of a directory is never the issuer of another kind of provider.
const directoryBindingPrefix = "ldap:"

// binding returns the form in which a store records p: the issuer in the
// form of NormalizeIssuer, or for a plain OAuth 2.0 provider the user-info
// URL in the form of NormalizeEndpoint, then "#", then the subject claim, or
// for an LDAP provider "ldap:" and the directory URL in the form of
// NormalizeDirectoryURL. BindProviders checks the directory URL before it
// calls binding, so the fallback to the raw text is not reachable there.
func (p Provider) binding() string {
	if p.DirectoryURL != "" {
		n, err := NormalizeDirectoryURL(p.DirectoryURL)
		if err != nil {
			return directoryBindingPrefix + p.DirectoryURL
		}

		return directoryBindingPrefix + n
	}

	if p.Issuer == "" {
		return NormalizeEndpoint(p.UserinfoURL) + "#" + p.SubjectClaim
	}

	return NormalizeIssuer(p.Issuer)
}

// matches reports whether the recorded binding is the binding of p. An
// issuer compares in the form of NormalizeIssuer. The binding of a plain
// OAuth 2.0 provider compares exactly, so a change of the path of its
// user-info URL is a change.
func (p Provider) matches(recorded string) bool {
	if p.Issuer == "" {
		return recorded == p.binding()
	}

	return NormalizeIssuer(recorded) == p.binding()
}

// BindProviders records the issuer of each provider of providers at the
// time at, and checks that no provider ID changed its issuer. It returns
// an error that matches ErrIssuerChanged, whose text is one sentence that
// names the provider ID, both issuers and the fix, for the first provider
// whose recorded issuer is another one.
//
// It records and compares the issuer in the form of NormalizeIssuer, so a
// change of the case of the scheme or the host, or a trailing slash, is
// the same issuer.
func BindProviders(ctx context.Context, store ProviderStore, providers []Provider, at time.Time) error {
	for _, p := range providers {
		if err := checkProviderText(p); err != nil {
			return err
		}

		recorded, err := store.BindProvider(ctx, p.ID, p.binding(), at)
		if err != nil {
			return fmt.Errorf("record the issuer of sign-in provider %q: %w", p.ID, err)
		}

		if p.matches(recorded) {
			continue
		}

		if p.DirectoryURL != "" {
			return directoryChangedError{id: p.ID, recorded: recorded, configured: p.binding()}
		}

		if p.Issuer == "" {
			return identityChangedError{id: p.ID, recorded: recorded, configured: p.binding()}
		}

		return issuerChangedError{id: p.ID, recorded: recorded, configured: p.Issuer}
	}

	return nil
}

// checkProviderText refuses a provider whose directory URL or issuer has a
// form that the server never records.
func checkProviderText(p Provider) error {
	// Defense in depth: ldapauth.parseURL checks the directory URL when
	// the server starts. This check stays so that a caller of the session
	// layer cannot bind a provider to a URL with user information, a
	// path or another scheme. It does not rely on that check.
	if p.DirectoryURL != "" {
		if _, err := NormalizeDirectoryURL(p.DirectoryURL); err != nil {
			return fmt.Errorf("sign-in provider %q: %w", p.ID, err)
		}
	}

	// Defense in depth: the flag parser, NewServer and the sign-in client
	// check the issuer too. This check stays so that a caller of the
	// session layer cannot record an issuer with a query, a fragment or
	// user information. It does not rely on those checks.
	if p.Issuer != "" {
		if err := CheckIssuer(p.Issuer); err != nil {
			return fmt.Errorf("sign-in provider %q: %w", p.ID, err)
		}
	}

	return nil
}

// identityChangedError is the refusal of a plain OAuth 2.0 provider whose
// user-info URL or subject claim is not the one that the store recorded
// for its ID. Its text is one sentence that names the provider ID, both
// bindings and the fix.
type identityChangedError struct {
	id, recorded, configured string
}

func (e identityChangedError) Error() string {
	return fmt.Sprintf("sign-in provider %q identifies its people with %q in the database and with %q in the "+
		"request, so set the user-info URL and the subject claim back, or use a new provider ID only if the "+
		"identity provider changed", e.id, e.recorded, e.configured)
}

// Unwrap returns ErrIssuerChanged.
func (identityChangedError) Unwrap() error { return ErrIssuerChanged }

// directoryChangedError is the refusal of an LDAP provider whose directory
// URL is not the one that the store recorded for its ID. Its text is one
// sentence that names the provider ID, both URLs and the fix: a person of
// another directory with the same subject would sign in as the old
// principal.
type directoryChangedError struct {
	id, recorded, configured string
}

func (e directoryChangedError) Error() string {
	recorded := displayRecordedDirectory(e.recorded)

	return fmt.Sprintf("sign-in provider %q has the directory URL %q in the database and the directory URL %q in "+
		"the configuration, so set the URL (--ldap-url) back to %q, or set a new provider ID (--ldap-provider) "+
		"only if the directory changed", e.id, recorded, displayRecordedDirectory(e.configured),
		recorded)
}

// notValidDirectoryURL is the text that the refusal shows in place of a
// recorded row that is not a valid directory binding.
const notValidDirectoryURL = "a directory URL that is not valid"

// displayRecordedDirectory returns the text of a recorded row for the
// refusal: the normalized directory URL, or a fixed phrase. The row can come
// from another version or from a manual edit, and it can hold user
// information, so the refusal never shows its text as stored.
//
// Defense in depth: BindProviders writes only normalized rows, and this
// layer does not rely on that. It normalizes the row again, and on an error
// or a row with no "ldap:" prefix it shows no part of the row.
func displayRecordedDirectory(row string) string {
	raw, ok := strings.CutPrefix(row, directoryBindingPrefix)
	if !ok {
		return notValidDirectoryURL
	}

	n, err := NormalizeDirectoryURL(raw)
	if err != nil {
		return notValidDirectoryURL
	}

	return n
}

// Unwrap returns ErrIssuerChanged.
func (directoryChangedError) Unwrap() error { return ErrIssuerChanged }

// NormalizeIssuer returns issuer with a lowercase scheme and host and with
// no trailing slash. An issuer that is not a URL keeps its text, with no
// trailing slash.
func NormalizeIssuer(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(issuer, "/")
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")

	return u.String()
}

// ErrDirectoryURL reports a directory URL of an LDAP provider that is not
// valid. Its text never holds the URL, because the URL can hold a password.
var ErrDirectoryURL = errors.New("the directory URL must be an ldap:// or ldaps:// URL with a host, and with " +
	"no user information, no path, no query and no fragment, so set the URL (--ldap-url) to that form")

// NormalizeDirectoryURL returns the form of the directory URL raw that the
// server records for an LDAP provider: a lowercase scheme and host, no
// trailing root dot on the host, an IP address in its canonical text form, no
// path, and no port when the port is the default of the scheme (636 for
// ldaps, 389 for ldap). The port is a number, so "0636" is 636. An empty
// port after the colon is the default port, as in the LDAP client. The
// schemes ldap and ldaps stay different. An internationalized host name and
// its punycode form stay different.
//
// It returns an error that matches ErrDirectoryURL for any other URL: one
// with another scheme, no host, user information, a path other than "/", a
// query, a fragment or a port above 65535. The error holds no part of raw.
func NormalizeDirectoryURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" ||
		u.Fragment != "" {
		return "", ErrDirectoryURL
	}

	scheme := u.Scheme

	defaultPort := directoryDefaultPort(scheme)
	if defaultPort == "" {
		return "", ErrDirectoryURL
	}

	host := normalizeDirectoryHost(u.Hostname())
	port, ok := normalizeDirectoryPort(u.Port(), defaultPort)

	if host == "" || !ok {
		return "", ErrDirectoryURL
	}

	if port == defaultPort {
		return scheme + "://" + bracket(host), nil
	}

	return scheme + "://" + net.JoinHostPort(host, port), nil
}

// directoryDefaultPort returns the default port of an LDAP URL scheme, or an
// empty string for another scheme.
func directoryDefaultPort(scheme string) string {
	switch scheme {
	case "ldaps":
		return "636"
	case "ldap":
		return "389"
	default:
		return ""
	}
}

// normalizeDirectoryPort returns the port as a number in decimal text, or
// defaultPort when text is empty. It reports false for port 0 and for a value
// above 65535.
func normalizeDirectoryPort(text, defaultPort string) (string, bool) {
	if text == "" {
		return defaultPort, true
	}

	n, err := strconv.ParseUint(text, 10, 16)
	if err != nil || n == 0 {
		return "", false
	}

	return strconv.FormatUint(n, 10), true
}

// normalizeDirectoryHost returns host with no trailing root dot. An IP
// address has its canonical text form, with its IPv6 zone as given: a zone
// is a name of this computer, so its case counts. Any other host has its ASCII
// letters in lowercase and no other change. Unicode case folding would join
// two different host names, for example the Kelvin sign and "k".
func normalizeDirectoryHost(host string) string {
	host = strings.TrimSuffix(host, ".")

	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String()
	}

	b := []byte(host)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}

	return string(b)
}

// bracket puts an IPv6 address in square brackets.
func bracket(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}

	return host
}
