package dustpan

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/egress/plainconn"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
)

// LDAPProvider is a sign-in provider of the configuration of the server that
// checks a user name and a password at an LDAP directory. A person signs in
// with POST /api/dustpan/v1/sign-in/providers/<ID>/password. The server checks
// each setting when it starts.
//
// URL is ldaps://host[:port], or ldap://host[:port] with StartTLS. A
// connection with no TLS is refused, except to a loopback IP address or when
// the setting egress.allowPlainConnections is on; the server reads that
// setting at each sign-in. RootCAs are the certificate authorities of the
// directory: nil uses the system roots. AllowedRanges are the address ranges
// of a private network that a connection to the directory may reach, in
// addition to public addresses; NewServer refuses a range that is too wide
// (see egress.ParseAllowedRanges). BindPassword is the password of the
// service account, as a session.Password: its String and GoString never show
// the value, so a log of the provider does not hold it. Subject is
// "entryUUID", "objectGUID" or the name of another attribute. Groups is
// "off", "search" or "memberOf"; the server reads the groups of a person and
// applies no membership. AutoCreate makes a principal at the first sign-in of
// a person, with no grant and no group.
type LDAPProvider struct {
	RootCAs                *x509.CertPool
	AllowedRanges          []netip.Prefix
	ID                     string
	DisplayName            string
	URL                    string
	BindDN                 string
	BindPassword           session.Password
	BaseDN                 string
	UserFilter             string
	UserNameAttribute      string
	EmailAttribute         string
	NameAttribute          string
	Subject                string
	DisabledAttribute      string
	Groups                 string
	GroupBaseDN            string
	GroupFilter            string
	GroupMemberAttribute   string
	GroupMemberOfAttribute string
	GroupNameAttribute     string
	DisabledValues         []string
	DisabledMask           uint64
	StartTLS               bool
	AutoCreate             bool
}

// WithLDAPProvider adds an LDAP provider to the sign-in. The sign-in needs the
// public URL (WithPublicURL) with HTTPS, and a store that keeps principals,
// sessions and the lock counts of a sign-in: both SQL adapters do.
func WithLDAPProvider(p LDAPProvider) Option {
	return func(o *settings) { o.ldapProviders = append(o.ldapProviders, p) }
}

// WithLDAPLoopbackForTests lets the LDAP sign-in reach a directory on a
// loopback address, for tests. The egress rules refuse loopback otherwise.
func WithLDAPLoopbackForTests() Option {
	return func(o *settings) { o.ldapLoopback = true }
}

// errLDAPSetup reports an LDAP provider that the server cannot use.
var errLDAPSetup = errors.New("ldap sign-in")

// passwordProviders returns the LDAP providers of cfg with their methods. The
// method reads egress.allowPlainConnections through settingsSvc at each
// sign-in, so a change of the setting applies at the next sign-in. A nil
// settingsSvc reads the setting as off.
func passwordProviders(cfg *settings, settingsSvc *setting.Service) ([]serverapi.PasswordProvider, error) {
	out := make([]serverapi.PasswordProvider, 0, len(cfg.ldapProviders))

	for i := range cfg.ldapProviders {
		p := &cfg.ldapProviders[i]

		// Defense in depth: the flag parser refuses a range that is too wide,
		// and the egress rules never open loopback or metadata addresses. This
		// check stays so that an embedder cannot pass a wide range.
		for _, r := range p.AllowedRanges {
			if rangeErr := egress.CheckAllowedRange(r); rangeErr != nil {
				return nil, fmt.Errorf("%w: provider %q: allowed ranges: %w", errLDAPSetup, p.ID, rangeErr)
			}
		}

		m, err := ldapauth.NewMethod(ldapConfig(cfg, p, settingsSvc))
		if err != nil {
			return nil, fmt.Errorf("%w: provider %q: %w", errLDAPSetup, p.ID, err)
		}

		out = append(out, serverapi.PasswordProvider{
			Method: m, ID: p.ID, DisplayName: displayNameOr(p.DisplayName, p.ID), AutoCreate: p.AutoCreate,
			LDAP: serverapi.LDAPSettings{
				AllowedRanges: rangeTexts(
					p.AllowedRanges,
				),
				URL:                    p.URL,
				BindDN:                 p.BindDN,
				BaseDN:                 p.BaseDN,
				UserFilter:             p.UserFilter,
				UserNameAttribute:      p.UserNameAttribute,
				EmailAttribute:         p.EmailAttribute,
				NameAttribute:          p.NameAttribute,
				Subject:                subjectOr(p.Subject),
				DisabledAttribute:      p.DisabledAttribute,
				Groups:                 groupsOr(p.Groups),
				GroupBaseDN:            p.GroupBaseDN,
				GroupFilter:            p.GroupFilter,
				GroupMemberAttribute:   p.GroupMemberAttribute,
				GroupMemberOfAttribute: p.GroupMemberOfAttribute,
				GroupNameAttribute:     p.GroupNameAttribute,
				StartTLS:               p.StartTLS,
			},
		})
	}

	return out, nil
}

// ldapConfig returns the configuration of the method of p.
func ldapConfig(cfg *settings, p *LDAPProvider, settingsSvc *setting.Service) ldapauth.Config {
	subject := ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: subjectOr(p.Subject)}

	switch subject.Attribute {
	case string(ldapauth.SubjectEntryUUID):
		subject = ldapauth.Subject{Kind: ldapauth.SubjectEntryUUID, Attribute: ""}
	case string(ldapauth.SubjectObjectGUID):
		subject = ldapauth.Subject{Kind: ldapauth.SubjectObjectGUID, Attribute: ""}
	default:
		// Another name is an attribute that the administrator names. The
		// method checks the name.
	}

	return ldapauth.Config{
		Enabled: func(context.Context) (bool, error) { return true, nil },
		AllowPlain: func(ctx context.Context) (bool, error) {
			// Read at each sign-in: a change of the setting applies to the next
			// connection. PlainAllowed fails closed on a read error.
			return plainconn.PlainAllowed(ctx, settingsSvc), nil
		},
		Now: cfg.now, Logger: cfg.logger, RootCAs: p.RootCAs, ProviderID: p.ID, URL: p.URL,
		BindDN: p.BindDN, BaseDN: p.BaseDN, UserFilter: p.UserFilter, UserNameAttribute: p.UserNameAttribute,
		EmailAttribute: p.EmailAttribute, NameAttribute: p.NameAttribute,
		BindPassword: p.BindPassword, Subject: subject,
		Disabled: ldapauth.Disabled{Attribute: p.DisabledAttribute, Values: p.DisabledValues, Mask: p.DisabledMask},
		Groups: ldapauth.Groups{
			Source: ldapauth.GroupSource(groupsOr(p.Groups)), BaseDN: p.GroupBaseDN, Filter: p.GroupFilter,
			MemberAttribute: p.GroupMemberAttribute, MemberOfAttribute: p.GroupMemberOfAttribute,
			NameAttribute: p.GroupNameAttribute,
		},
		Egress:         egress.Rules{Allow: p.AllowedRanges, LoopbackForTests: cfg.ldapLoopback},
		ConnectTimeout: 0, OperationTimeout: 0, GroupTimeout: 0, StartTLS: p.StartTLS,
	}
}

// subjectOr returns the subject setting s, or entryUUID when it is empty.
func subjectOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return string(ldapauth.SubjectEntryUUID)
	}

	return s
}

// groupsOr returns the groups setting g, or off when it is empty.
func groupsOr(g string) string {
	if strings.TrimSpace(g) == "" {
		return string(ldapauth.GroupsOff)
	}

	return g
}

// displayNameOr returns name, or id when name is empty.
func displayNameOr(name, id string) string {
	if strings.TrimSpace(name) == "" {
		return id
	}

	return name
}

// ldapIDs returns the provider IDs of the LDAP providers of cfg.
func ldapIDs(cfg *settings) []string {
	out := make([]string, 0, len(cfg.ldapProviders))
	for i := range cfg.ldapProviders {
		out = append(out, cfg.ldapProviders[i].ID)
	}

	return out
}

// rangeTexts returns the text of each range of ranges.
func rangeTexts(ranges []netip.Prefix) []string {
	out := make([]string, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, r.String())
	}

	return out
}
