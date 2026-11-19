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
		"userPrincipalName", "rfc822Mailbox", "0.9.2342.19200300.100.1.1", "2.5.4.3", "cn", "displayName", "telephoneNumber", "givenName", "sn", "employeeNumber", "mailPrimaryAddress", "proxyAddresses", "krbPrincipalName", " uid", "uid ", " entryUUID", "entryUUID ", "entry_uuid",
	} {
		t.Run(subject, func(t *testing.T) {
			t.Parallel()

			checkLDAPOptions(t, func(l *ldapFlags) { l.subject = subject }, vars, 0, ldapauth.ErrSubjectAttribute)
		})
	}

	for name, change := range map[string]func(l *ldapFlags){
		"the user name attribute": func(l *ldapFlags) { l.subject, l.userNameAttribute = "nsUniqueId", "NsUniqueId" },
		"the email attribute":     func(l *ldapFlags) { l.subject, l.emailAttribute = "nsUniqueId", "nsuniqueid" },
		"the name attribute":      func(l *ldapFlags) { l.subject, l.nameAttribute = "nsUniqueId", "nsUniqueId" },
	} {
		t.Run("same as "+name, func(t *testing.T) {
			t.Parallel()

			checkLDAPOptions(t, change, vars, 0, ldapauth.ErrSubjectAttribute)
		})
	}

	for _, ok := range []string{"entryUUID", "objectGUID", "nsUniqueId", "ipaUniqueID", "GUID", "orclGUID", "ibm-entryUUID", "NSUNIQUEID", "entryuuid"} {
		t.Run("allowed "+ok, func(t *testing.T) {
			t.Parallel()

			checkLDAPOptions(t, func(l *ldapFlags) { l.subject = ok }, vars, 1, nil)
		})
	}
}

// hostileIssuers returns issuers that the server must refuse, by reason.
func hostileIssuers() map[string]string {
	return map[string]string{
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
		"upper-case scheme":      "HTTPS://idp.example",
		"non-ASCII host":         "https://b\u00fccher.example/",
		"trailing dot host":      "https://idp.example./",
		"port over 65535":        "https://idp.example:99999/",
		"dot-dot segment":        "https://idp.example/a/../b",
		"dot segment":            "https://idp.example/./x",
		"right-to-left override": "https://idp.example/a\u202eb",
	}
}

// TestOIDCIssuerFlagRefusesAHostileIssuer proves that --oidc-issuer stops
// the start for each hostile issuer, and that the error never holds it.
func TestOIDCIssuerFlagRefusesAHostileIssuer(t *testing.T) {
	t.Parallel()

	for name, issuer := range hostileIssuers() {
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

// TestLDAPSubjectFlagRefusesASpaceItself sends a subject with a space at its
// start or its end to the flag check alone. The refusal names the space, so
// the test fails when the flag check does not refuse the space and only the
// allowlist of ldapauth does.
func TestLDAPSubjectFlagRefusesASpaceItself(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{" uid", "uid ", " entryUUID", "entryUUID ", " "} {
		l := completeLDAP()
		l.subject = subject

		err := l.checkSubject()
		if !errors.Is(err, ldapauth.ErrSubjectAttribute) || !strings.Contains(err.Error(), "space at its start") {
			t.Errorf("checkSubject(%q) = %v, want ErrSubjectAttribute about the space", subject, err)
		}
	}
}
