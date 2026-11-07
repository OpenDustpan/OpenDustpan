// Package ldapauth is the adapter of the port session.PasswordMethod for an
// LDAP directory. It binds with a service account, searches for the user
// with the filter of the administrator, calls the bind gate of the sign-in
// service with the subject that it found, and then binds as that user to
// check the password. It never reads or compares a password hash, and it
// never follows a referral.
package ldapauth

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
)

// ErrConfig reports a Config that is not valid. NewMethod returns it.
var ErrConfig = errors.New("ldap sign-in settings are not valid")

// Limits of the method.
const (
	// MaxTimeout is the longest time for a connection and for one
	// operation. It is also the default.
	MaxTimeout = 10 * time.Second
	// MaxGroupTimeout is the longest time for the read of the groups of
	// one sign-in, nested groups included. It is also the default.
	MaxGroupTimeout = 30 * time.Second
	// MaxGroups is the most groups that one sign-in reads, nested groups
	// included. More groups refuse the sign-in.
	MaxGroups = session.MaxAssertionGroups
	// MaxPassword is the longest password that the method sends, in bytes.
	MaxPassword = 1024
	// UserPlaceholder is the text in the user filter that the method
	// replaces with the escaped user name.
	UserPlaceholder = "{user}"
	// maxFilter is the longest filter of a setting, in bytes.
	maxFilter = 1024
	// maxDN is the longest DN of a setting, in bytes.
	maxDN = 1024
)

// SubjectKind names the attribute that gives the subject of a person.
type SubjectKind string

// Kinds of the subject.
const (
	// SubjectEntryUUID is the entryUUID attribute (RFC 4530), as lowercase
	// UUID text.
	SubjectEntryUUID SubjectKind = "entryUUID"
	// SubjectObjectGUID is the objectGUID attribute of Active Directory, as
	// the lowercase GUID text that Active Directory shows.
	SubjectObjectGUID SubjectKind = "objectGUID"
	// SubjectAttribute is an attribute that the administrator names. Its
	// one value must be text with the limits of a subject.
	SubjectAttribute SubjectKind = "attribute"
)

// GroupSource names where the method reads the groups of a person.
type GroupSource string

// Sources of the groups.
const (
	// GroupsOff reads no groups. The Assertion has GroupsPresent false.
	GroupsOff GroupSource = "off"
	// GroupsSearch searches for the groups whose member attribute holds
	// the DN of the person, and then the DN of each group that it found.
	GroupsSearch GroupSource = "search"
	// GroupsMemberOf reads the memberOf attribute of the person, and then
	// the memberOf attribute of each group that it found.
	GroupsMemberOf GroupSource = "memberOf"
)

// Subject is the setting of the subject.
type Subject struct {
	// Kind is the kind of the subject.
	Kind SubjectKind
	// Attribute names the attribute of SubjectAttribute. It is empty for
	// the other kinds.
	Attribute string
}

// Groups is the setting of the groups.
type Groups struct {
	// Source is where the method reads the groups.
	Source GroupSource
	// BaseDN is the base of the group search. With GroupsMemberOf, a group
	// outside it is not read and not returned.
	BaseDN string
	// Filter is the filter of a group entry, for example
	// "(objectClass=groupOfNames)". Required for GroupsSearch.
	Filter string
	// MemberAttribute is the attribute of a group that holds the DN of a
	// member, for example "member". Required for GroupsSearch.
	MemberAttribute string
	// MemberOfAttribute is the attribute of an entry that holds the DN of
	// its groups, for example "memberOf". Required for GroupsMemberOf.
	MemberOfAttribute string
	// NameAttribute is the attribute of a group that gives the group name,
	// for example "cn".
	NameAttribute string
}

// Disabled is the setting that tells that the directory reports a person
// as disabled. With an empty Attribute, the method reads no such value.
type Disabled struct {
	// Attribute names the attribute, for example "userAccountControl".
	Attribute string
	// Values are the values of Attribute that mark a disabled person,
	// compared with no case, for example "TRUE" for nsAccountLock. Not
	// used when Mask is set.
	Values []string
	// Mask, when it is not zero, reads Attribute as an unsigned decimal
	// number. The person is disabled when the number and Mask share a
	// bit, for example 2 for userAccountControl.
	Mask uint64
}

// Config configures a Method. The caller reads the settings and the
// password of the service account from their sources. NewMethod checks
// each field.
type Config struct {
	// Enabled reports whether the provider is on. The method calls it at
	// each Verify. It returns an error when the state of the provider
	// cannot be read. Required.
	Enabled func(ctx context.Context) (bool, error)
	// AllowPlain reports whether the installation allows a connection with
	// no TLS. The method calls it at each Verify with a plain URL. An error
	// counts as false. Required.
	AllowPlain func(ctx context.Context) (bool, error)
	// Now is the clock. Nil uses time.Now.
	Now func() time.Time
	// Logger receives the reason of each refused sign-in, with no value
	// that the person typed. Nil discards the records.
	Logger *slog.Logger
	// RootCAs holds the certificate authorities that the method trusts for
	// the directory. Nil means the system roots.
	RootCAs *x509.CertPool
	// ProviderID names the sign-in provider. Required.
	ProviderID string
	// URL is the address of the directory: ldaps://host[:port] or
	// ldap://host[:port], with no path, query, fragment or user
	// information. Required.
	URL string
	// BindDN is the DN of the service account. Required.
	BindDN string
	// BaseDN is the base of the user search. The DN of a user must be
	// below it. Required.
	BaseDN string
	// UserFilter is the filter of the user search. It holds
	// UserPlaceholder at least once. Required.
	UserFilter string
	// UserNameAttribute names the attribute that gives the user name of
	// the Assertion. Empty uses the user name that the person typed.
	UserNameAttribute string
	// EmailAttribute names the attribute of the email, or is empty.
	EmailAttribute string
	// NameAttribute names the attribute of the display name, or is empty.
	NameAttribute string
	// BindPassword is the password of the service account. Required.
	BindPassword session.Password
	// Subject is the setting of the subject.
	Subject Subject
	// Disabled is the setting of a disabled person.
	Disabled Disabled
	// Groups is the setting of the groups.
	Groups Groups
	// Egress holds the rules that each connection to the directory
	// passes, on the resolved address.
	Egress egress.Rules
	// ConnectTimeout is the time for a connection and its TLS handshake.
	// Zero is MaxTimeout. It is at most MaxTimeout.
	ConnectTimeout time.Duration
	// OperationTimeout is the time for one LDAP operation. Zero is
	// MaxTimeout. It is at most MaxTimeout.
	OperationTimeout time.Duration
	// GroupTimeout is the time for the read of the groups of one sign-in.
	// Zero is MaxGroupTimeout. It is at most MaxGroupTimeout.
	GroupTimeout time.Duration
	// StartTLS starts TLS on an ldap:// URL before the first bind.
	StartTLS bool
}

// transport is the security of the connection to the directory.
type transport int

// Kinds of transport.
const (
	transportLDAPS transport = iota + 1
	transportStartTLS
	transportPlain
)

// endpoint is the checked address of the directory.
type endpoint struct {
	host      string
	address   string
	transport transport
}

// configError returns ErrConfig with the reason.
func configError(reason string) error {
	return fmt.Errorf("%w: %s", ErrConfig, reason)
}

// checkConfig checks cfg and returns the endpoint of its URL.
func checkConfig(cfg Config) (endpoint, error) {
	var none endpoint

	for _, check := range []func(Config) error{
		checkBasics, checkDNSettings, checkUserFilter, checkSubject, checkAttributes, checkGroups,
	} {
		if err := check(cfg); err != nil {
			return none, err
		}
	}

	return parseURL(cfg.URL, cfg.StartTLS)
}

// checkBasics checks the functions, the provider ID, the password of the
// service account and the timeouts of cfg.
func checkBasics(cfg Config) error {
	switch {
	case cfg.Enabled == nil:
		return configError("the switch of the provider is missing")
	case cfg.AllowPlain == nil:
		return configError("the plain-transport setting is missing")
	case !session.ValidProviderID(cfg.ProviderID) || cfg.ProviderID == session.LocalProviderID ||
		cfg.ProviderID == session.DevProviderID:
		return configError("the provider ID is not valid")
	case cfg.BindPassword.Reveal() == "":
		// An empty password makes an unauthenticated bind.
		return configError("the password of the service account is empty")
	case len(cfg.BindPassword.Reveal()) > MaxPassword:
		return configError(fmt.Sprintf("the password of the service account is longer than %d bytes", MaxPassword))
	case !validTimeout(cfg.ConnectTimeout, MaxTimeout) || !validTimeout(cfg.OperationTimeout, MaxTimeout) ||
		!validTimeout(cfg.GroupTimeout, MaxGroupTimeout):
		return configError(fmt.Sprintf("a timeout must be 0 to %s, and the group timeout 0 to %s", MaxTimeout,
			MaxGroupTimeout))
	default:
		return nil
	}
}

// validTimeout reports whether d is 0 to limit.
func validTimeout(d, limit time.Duration) bool {
	return d >= 0 && d <= limit
}

// errURLForm is the reason of a URL of the wrong form.
const errURLForm = "the URL must be ldaps://host[:port] or ldap://host[:port]"

// parseURL checks the URL of the directory and returns its endpoint.
func parseURL(raw string, startTLS bool) (endpoint, error) {
	var none endpoint

	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" ||
		u.Fragment != "" || u.Hostname() == "" {
		return none, configError(errURLForm)
	}

	t, port, err := transportOf(u.Scheme, startTLS)
	if err != nil {
		return none, err
	}

	if p := u.Port(); p != "" {
		port = p
	}

	return endpoint{host: u.Hostname(), address: net.JoinHostPort(u.Hostname(), port), transport: t}, nil
}

// transportOf returns the transport and the default port of the scheme.
func transportOf(scheme string, startTLS bool) (transport, string, error) {
	switch {
	case scheme == "ldaps" && !startTLS:
		return transportLDAPS, "636", nil
	case scheme == "ldaps":
		return 0, "", configError("StartTLS needs an ldap:// URL")
	case scheme == "ldap" && startTLS:
		return transportStartTLS, "389", nil
	case scheme == "ldap":
		return transportPlain, "389", nil
	default:
		return 0, "", configError(errURLForm)
	}
}

// checkDNSettings checks the DNs of cfg.
func checkDNSettings(cfg Config) error {
	dns := map[string]string{"bind DN": cfg.BindDN, "base DN": cfg.BaseDN}
	if cfg.Groups.Source == GroupsSearch || cfg.Groups.Source == GroupsMemberOf {
		dns["group base DN"] = cfg.Groups.BaseDN
	}

	for name, dn := range dns {
		if len(dn) > maxDN || !validText(dn) {
			return configError("the " + name + " has control characters or is too long")
		}

		if _, _, err := canonicalDN(dn); err != nil {
			return configError("the " + name + " is not a valid DN")
		}
	}

	return nil
}

// checkUserFilter checks the user filter: it holds the placeholder, it
// compiles, and a value in the place of the placeholder adds no item.
func checkUserFilter(cfg Config) error {
	f := cfg.UserFilter

	if len(f) > maxFilter || !validText(f) || !strings.Contains(f, UserPlaceholder) {
		return configError(fmt.Sprintf("the user filter must hold %s and have at most %d bytes",
			UserPlaceholder, maxFilter))
	}

	if _, err := filterNodes(strings.ReplaceAll(f, UserPlaceholder, "x")); err != nil {
		return configError("the user filter does not compile")
	}

	return nil
}

// checkSubject checks the subject setting of cfg.
func checkSubject(cfg Config) error {
	switch cfg.Subject.Kind {
	case SubjectEntryUUID, SubjectObjectGUID:
		if cfg.Subject.Attribute != "" {
			return configError("the subject attribute is set for a kind that names its own attribute")
		}
	case SubjectAttribute:
		if !validAttributeType(cfg.Subject.Attribute) {
			return configError("the subject attribute is not a valid attribute type")
		}
	default:
		return configError("the subject kind is not known")
	}

	return nil
}

// checkAttributes checks the attribute names and the disabled rule of cfg.
func checkAttributes(cfg Config) error {
	for _, a := range []string{cfg.UserNameAttribute, cfg.EmailAttribute, cfg.NameAttribute, cfg.Disabled.Attribute} {
		if a != "" && !validAttributeType(a) {
			return configError("an attribute name is not a valid attribute type")
		}
	}

	if cfg.Disabled.Attribute == "" && (cfg.Disabled.Mask != 0 || len(cfg.Disabled.Values) > 0) {
		return configError("the disabled rule has no attribute")
	}

	if cfg.Disabled.Attribute != "" && cfg.Disabled.Mask == 0 && len(cfg.Disabled.Values) == 0 {
		return configError("the disabled rule has no mask and no value")
	}

	return nil
}

// checkGroups checks the group setting of cfg.
func checkGroups(cfg Config) error {
	g := cfg.Groups

	switch g.Source {
	case GroupsOff:
		return nil
	case GroupsSearch:
		if !validAttributeType(g.MemberAttribute) {
			return configError("the member attribute is not a valid attribute type")
		}

		if len(g.Filter) > maxFilter || !validText(g.Filter) {
			return configError("the group filter has control characters or is too long")
		}

		if _, err := filterNodes(g.Filter); err != nil {
			return configError("the group filter does not compile")
		}
	case GroupsMemberOf:
		if !validAttributeType(g.MemberOfAttribute) {
			return configError("the memberOf attribute is not a valid attribute type")
		}
	default:
		return configError("the group source is not known")
	}

	if !validAttributeType(g.NameAttribute) {
		return configError("the group name attribute is not a valid attribute type")
	}

	return nil
}

// validText reports whether s is UTF-8 with no control character.
func validText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

// plainHostAllowed reports whether a plain connection to host is allowed
// with no opt-in: only when host is a loopback IP address. A host name,
// "localhost" too, needs the opt-in, because its answer can change.
func plainHostAllowed(host string) bool {
	a, err := netip.ParseAddr(host)

	return err == nil && a.Unmap().IsLoopback()
}

// groupFilter returns the filter of a group search for the member DN, which
// is already escaped.
func groupFilter(g Groups, escapedMember string) string {
	return "(&" + g.Filter + "(" + g.MemberAttribute + "=" + escapedMember + "))"
}
