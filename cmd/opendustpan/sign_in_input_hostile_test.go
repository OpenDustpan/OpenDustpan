package main

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/pkg/session"
)

// The tests in this file send hostile input to the flag layer of the LDAP
// subject and of the OpenID Connect issuer, as if NewServer, ldapauth and
// the session layer did not check it.

// TestLDAPSubjectFlagRefusesAHostileName proves that --ldap-subject stops the
// start for a name that holds a DN, a user name or an email.
func TestLDAPSubjectFlagRefusesAHostileName(t *testing.T) {
	t.Parallel()

	vars := map[string]string{ldapBindEnvVar: ldapTestPassword}

	for _, subject := range []string{
		"dn", "DN", "entryDN", "distinguishedName", "uid", "UID", "mail", "Mail", "userid", "sAMAccountName",
		"userPrincipalName", "rfc822Mailbox",
	} {
		t.Run(subject, func(t *testing.T) {
			t.Parallel()

			checkLDAPOptions(t, func(l *ldapFlags) { l.subject = subject }, vars, 0, ldapauth.ErrSubjectAttribute)
		})
	}

	for name, change := range map[string]func(l *ldapFlags){
		"the user name attribute": func(l *ldapFlags) { l.subject, l.userNameAttribute = "employeeNumber", "EmployeeNumber" },
		"the email attribute":     func(l *ldapFlags) { l.subject, l.emailAttribute = "employeeNumber", "employeenumber" },
		"the name attribute":      func(l *ldapFlags) { l.subject, l.nameAttribute = "employeeNumber", "employeeNumber" },
	} {
		t.Run("same as "+name, func(t *testing.T) {
			t.Parallel()

			checkLDAPOptions(t, change, vars, 0, ldapauth.ErrSubjectAttribute)
		})
	}

	for _, ok := range []string{"entryUUID", "objectGUID", "employeeNumber"} {
		t.Run("allowed "+ok, func(t *testing.T) {
			t.Parallel()

			checkLDAPOptions(t, func(l *ldapFlags) { l.subject = ok }, vars, 1, nil)
		})
	}
}

// hostileIssuers are issuers that the server must refuse, and the reason.
var hostileIssuers = map[string]string{
	"query and fragment":     "https://idp.example?x=1#f",
	"query":                  "https://idp.example/?x=1",
	"fragment":               "https://idp.example#f",
	"empty query":            "https://idp.example?",
	"empty fragment":         "https://idp.example#",
	"user information":       "https://user:" + "pw" + "@idp.example",
	"space":                  "https://idp.example/a b",
	"control character":      "https://idp.example/\x00",
	"line feed":              "https://idp.example/\n",
	"back slash":             "https://idp.example\\@evil.example",
	"other scheme":           "ftp://idp.example",
	"http on a public host":  "http://idp.example",
	"no host":                "https://",
	"relative":               "idp.example",
	"more than 512 bytes":    "https://idp.example/" + strings.Repeat("a", 600),
	"http on a lookalike":    "http://localhost.example",
	"scheme-relative":        "//idp.example",
	"opaque URL":             "https:idp.example",
	"upper-case http scheme": "HTTP://idp.example",
}

// TestOIDCIssuerFlagRefusesAHostileIssuer proves that --oidc-issuer stops
// the start for each hostile issuer, and that the error never holds it.
func TestOIDCIssuerFlagRefusesAHostileIssuer(t *testing.T) {
	t.Parallel()

	for name, issuer := range hostileIssuers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			live := envOf(map[string]string{oidcSecretEnvVar: "client-secret-value"})

			var f serveFlags
			f.lookup = cli.SnapshotEnv(live, cli.DeclaredEnvs())
			f.oidcIssuer, f.oidcProvider, f.oidcClientID = issuer, "corp", "dustpan"

			_, ok, err := f.oidcProviderOption(live)
			if ok || !errors.Is(err, session.ErrIssuer) {
				t.Fatalf("oidcProviderOption(%q) = %v, %v; want ErrIssuer", issuer, ok, err)
			}

			if strings.Contains(err.Error(), "pw@") {
				t.Fatalf("the error holds the user information: %v", err)
			}
		})
	}
}
