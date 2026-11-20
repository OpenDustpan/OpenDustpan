package ldapauth_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/ldapauth"
)

// TestNewMethodRefusesAHostileSubjectAttribute sends a DN, a user name or an
// email attribute to NewMethod as the subject, as if the flag parser and
// NewServer did not check it.
func TestNewMethodRefusesAHostileSubjectAttribute(t *testing.T) {
	t.Parallel()

	s := newSetup(t)

	for _, name := range []string{
		"dn", "DN", "entryDN", "distinguishedName", "uid", "UID", "mail", "Mail", "userid", "sAMAccountName",
		"userPrincipalName", "rfc822Mailbox", "0.9.2342.19200300.100.1.1", "2.5.4.3", "cn", "displayName", "telephoneNumber", "givenName", "sn", "employeeNumber", "mailPrimaryAddress", "proxyAddresses", "krbPrincipalName", " uid", "uid ", " entryUUID", "entryUUID ", "", "entry_uuid",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := s.configWith(func(c *ldapauth.Config) {
				c.Subject = ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: name}
			})

			if _, err := ldapauth.NewMethod(cfg); !errors.Is(err, ldapauth.ErrConfig) {
				t.Fatalf("NewMethod() = %v, want ErrConfig", err)
			}
		})
	}

	for name, change := range map[string]func(c *ldapauth.Config){
		"the user name attribute": func(c *ldapauth.Config) { c.UserNameAttribute = "nsUniqueId" },
		"the email attribute":     func(c *ldapauth.Config) { c.EmailAttribute = "NSUNIQUEID" },
		"the name attribute":      func(c *ldapauth.Config) { c.NameAttribute = "nsUniqueId" },
	} {
		t.Run("same as "+name, func(t *testing.T) {
			t.Parallel()

			cfg := s.configWith(func(c *ldapauth.Config) {
				c.Subject = ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: "nsUniqueId"}
				change(c)
			})

			if _, err := ldapauth.NewMethod(cfg); !errors.Is(err, ldapauth.ErrConfig) {
				t.Fatalf("NewMethod() = %v, want ErrConfig", err)
			}
		})
	}
}

// TestCheckSubjectAttribute proves the refusal of the function itself.
func TestCheckSubjectAttribute(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"dn", "Mail", "UID", "entryDN", "0.9.2342.19200300.100.1.1", "2.5.4.3", "cn", "displayName", "telephoneNumber", "givenName", "sn", "employeeNumber", "mailPrimaryAddress", "proxyAddresses", "krbPrincipalName", " uid", "uid ", " entryUUID", "entryUUID ", "", "entry_uuid"} {
		if err := ldapauth.CheckSubjectAttribute(name); !errors.Is(err, ldapauth.ErrSubjectAttribute) {
			t.Fatalf("CheckSubjectAttribute(%q) = %v, want ErrSubjectAttribute", name, err)
		}
	}

	for _, name := range []string{"entryUUID", "objectGUID", "nsUniqueId", "ipaUniqueID", "GUID", "orclGUID", "ibm-entryUUID", "NSUNIQUEID", "entryuuid"} {
		if err := ldapauth.CheckSubjectAttribute(name, "uid", "mail", ""); err != nil {
			t.Fatalf("CheckSubjectAttribute(%q) = %v, want nil", name, err)
		}
	}
}
