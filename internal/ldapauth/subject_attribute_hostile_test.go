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
		"userPrincipalName", "rfc822Mailbox",
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
		"the user name attribute": func(c *ldapauth.Config) { c.UserNameAttribute = "employeeNumber" },
		"the email attribute":     func(c *ldapauth.Config) { c.EmailAttribute = "EMPLOYEENUMBER" },
		"the name attribute":      func(c *ldapauth.Config) { c.NameAttribute = "employeeNumber" },
	} {
		t.Run("same as "+name, func(t *testing.T) {
			t.Parallel()

			cfg := s.configWith(func(c *ldapauth.Config) {
				c.Subject = ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: "employeeNumber"}
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

	for _, name := range []string{"dn", "Mail", "UID", "entryDN"} {
		if err := ldapauth.CheckSubjectAttribute(name); !errors.Is(err, ldapauth.ErrSubjectAttribute) {
			t.Fatalf("CheckSubjectAttribute(%q) = %v, want ErrSubjectAttribute", name, err)
		}
	}

	if err := ldapauth.CheckSubjectAttribute("employeeNumber", "uid", "mail", ""); err != nil {
		t.Fatalf("CheckSubjectAttribute(employeeNumber) = %v, want nil", err)
	}
}
