package dustpan_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/session"
)

// TestNewServerRefusesAHostileLDAPSubject sends a DN, a user name or an email
// attribute to NewServer as the LDAP subject, as if the flag parser did not
// check it.
func TestNewServerRefusesAHostileLDAPSubject(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(p *dustpan.LDAPProvider){
		"dn":                              func(p *dustpan.LDAPProvider) { p.Subject = "dn" },
		"DN":                              func(p *dustpan.LDAPProvider) { p.Subject = "DN" },
		"entryDN":                         func(p *dustpan.LDAPProvider) { p.Subject = "entryDN" },
		"distinguishedName":               func(p *dustpan.LDAPProvider) { p.Subject = "distinguishedName" },
		"uid":                             func(p *dustpan.LDAPProvider) { p.Subject = "uid" },
		"mail":                            func(p *dustpan.LDAPProvider) { p.Subject = "Mail" },
		"sAMAccountName":                  func(p *dustpan.LDAPProvider) { p.Subject = "sAMAccountName" },
		"userPrincipalName":               func(p *dustpan.LDAPProvider) { p.Subject = "userPrincipalName" },
		"OID":                             func(p *dustpan.LDAPProvider) { p.Subject = "0.9.2342.19200300.100.1.1" },
		"OID cn":                          func(p *dustpan.LDAPProvider) { p.Subject = "2.5.4.3" },
		"cn":                              func(p *dustpan.LDAPProvider) { p.Subject = "cn" },
		"displayName":                     func(p *dustpan.LDAPProvider) { p.Subject = "displayName" },
		"employeeNumber":                  func(p *dustpan.LDAPProvider) { p.Subject = "employeeNumber" },
		"telephoneNumber":                 func(p *dustpan.LDAPProvider) { p.Subject = "telephoneNumber" },
		"krbPrincipalName":                func(p *dustpan.LDAPProvider) { p.Subject = "krbPrincipalName" },
		"leading space":                   func(p *dustpan.LDAPProvider) { p.Subject = " uid" },
		"trailing space":                  func(p *dustpan.LDAPProvider) { p.Subject = "uid " },
		"leading space on a listed name":  func(p *dustpan.LDAPProvider) { p.Subject = " entryUUID" },
		"trailing space on a listed name": func(p *dustpan.LDAPProvider) { p.Subject = "entryUUID " },
		"user name attribute":             func(p *dustpan.LDAPProvider) { p.Subject, p.UserNameAttribute = "nsUniqueId", "nsUniqueId" },
		"email attribute":                 func(p *dustpan.LDAPProvider) { p.Subject, p.EmailAttribute = "nsUniqueId", "NSUNIQUEID" },
		"display name":                    func(p *dustpan.LDAPProvider) { p.Subject, p.NameAttribute = "nsUniqueId", "nsUniqueId" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := ldapProvider()
			change(&p)

			opts := append(auditSinkServerOptions(t, openAuditStore(t)),
				dustpan.WithPublicURL("https://dustpan.example"), dustpan.WithLDAPProvider(p))

			_, err := dustpan.NewServer(opts...)
			if !errors.Is(err, ldapauth.ErrSubjectAttribute) {
				t.Fatalf("NewServer error = %v, want ErrSubjectAttribute", err)
			}
		})
	}
}

// TestNewServerRefusesAHostileOIDCIssuer sends an issuer with a query, a
// fragment or user information to NewServer, as if the flag parser did not
// check it. The error never holds the user information.
func TestNewServerRefusesAHostileOIDCIssuer(t *testing.T) {
	t.Parallel()

	for name, issuer := range map[string]string{
		"query and fragment": "https://idp.example?x=1#f",
		"query":              "https://idp.example/?x=1",
		"fragment":           "https://idp.example#f",
		"empty query":        "https://idp.example?",
		"user information":   "https://user:" + "pw" + "@idp.example",
		"space":              "https://idp.example/a b",
		"upper-case scheme":  "HTTPS://idp.example",
		"non-ASCII host":     "https://b\u00fccher.example/",
		"trailing dot host":  "https://idp.example./",
		"port over 65535":    "https://idp.example:99999/",
		"dot-dot segment":    "https://idp.example/a/../b",
		"dot segment":        "https://idp.example/./x",
		"right-to-left mark": "https://idp.example/a\u202eb",
		"over 512 bytes":     "https://idp.example/" + strings.Repeat("a", 600),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := oidcProvider("corp")
			p.Issuer = issuer

			opts := append(auditSinkServerOptions(t, openAuditStore(t)),
				dustpan.WithPublicURL("https://dustpan.example"), dustpan.WithOIDCProvider(p))

			_, err := dustpan.NewServer(opts...)
			if !errors.Is(err, session.ErrIssuer) {
				t.Fatalf("NewServer with the issuer %q = %v, want ErrIssuer", issuer, err)
			}

			if strings.Contains(err.Error(), "pw@") {
				t.Fatalf("the error holds the user information: %v", err)
			}
		})
	}
}
