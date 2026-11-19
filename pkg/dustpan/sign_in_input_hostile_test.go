package dustpan_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// TestNewServerRefusesAHostileLDAPSubject sends a DN, a user name or an email
// attribute to NewServer as the LDAP subject, as if the flag parser did not
// check it.
func TestNewServerRefusesAHostileLDAPSubject(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(p *dustpan.LDAPProvider){
		"dn":                  func(p *dustpan.LDAPProvider) { p.Subject = "dn" },
		"DN":                  func(p *dustpan.LDAPProvider) { p.Subject = "DN" },
		"entryDN":             func(p *dustpan.LDAPProvider) { p.Subject = "entryDN" },
		"distinguishedName":   func(p *dustpan.LDAPProvider) { p.Subject = "distinguishedName" },
		"uid":                 func(p *dustpan.LDAPProvider) { p.Subject = "uid" },
		"mail":                func(p *dustpan.LDAPProvider) { p.Subject = "Mail" },
		"sAMAccountName":      func(p *dustpan.LDAPProvider) { p.Subject = "sAMAccountName" },
		"userPrincipalName":   func(p *dustpan.LDAPProvider) { p.Subject = "userPrincipalName" },
		"user name attribute": func(p *dustpan.LDAPProvider) { p.Subject, p.UserNameAttribute = "employeeNumber", "employeeNumber" },
		"email attribute":     func(p *dustpan.LDAPProvider) { p.Subject, p.EmailAttribute = "employeeNumber", "EMPLOYEENUMBER" },
		"display name":        func(p *dustpan.LDAPProvider) { p.Subject, p.NameAttribute = "employeeNumber", "employeeNumber" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := ldapProvider()
			change(&p)

			opts := append(auditSinkServerOptions(t, openAuditStore(t)),
				dustpan.WithPublicURL("https://dustpan.example"), dustpan.WithLDAPProvider(p))

			_, err := dustpan.NewServer(opts...)
			if err == nil || !strings.Contains(err.Error(), "subject attribute") {
				t.Fatalf("NewServer error = %v, want one about the subject attribute", err)
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
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := oidcProvider("corp")
			p.Issuer = issuer

			opts := append(auditSinkServerOptions(t, openAuditStore(t)),
				dustpan.WithPublicURL("https://dustpan.example"), dustpan.WithOIDCProvider(p))

			_, err := dustpan.NewServer(opts...)
			if err == nil {
				t.Fatalf("NewServer with the issuer %q = nil, want an error", issuer)
			}

			if strings.Contains(err.Error(), "pw@") {
				t.Fatalf("the error holds the user information: %v", err)
			}
		})
	}
}
