package ldaptest_test

import (
	"crypto/tls"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"opendustpan.org/opendustpan/internal/ldaptest"
)

// dial binds as the service account over TLS.
func dial(t *testing.T, s *ldaptest.Server) *ldap.Conn {
	t.Helper()

	conn, err := ldap.DialURL(s.LDAPSURL(), ldap.DialWithTLSConfig(&tls.Config{
		RootCAs: s.CertPool(), MinVersion: tls.VersionTLS12,
	}))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	if err := conn.Bind(ldaptest.ServiceDN, s.ServicePassword()); err != nil {
		t.Fatalf("bind: %v", err)
	}

	return conn
}

// TestFilters checks that the directory reads a wildcard, a substring and
// a nested filter as a directory does. The tests of a method rely on it:
// a filter injection that passes the escape must find an entry here.
func TestFilters(t *testing.T) {
	t.Parallel()

	s := ldaptest.Start(t)
	s.AddUser(ldaptest.User{Attributes: nil, UserName: "alice", Password: "a"})
	s.AddUser(ldaptest.User{Attributes: nil, UserName: "bob", Password: "b"})

	conn := dial(t, s)

	cases := []struct {
		filter string
		want   int
	}{
		{"(uid=*)", 2},
		{"(uid=al*)", 1},
		{"(uid=*o*)", 1},
		{"(uid=ALICE)", 1},
		{`(uid=\2a)`, 0},
		{"(|(uid=alice)(uid=bob))", 2},
		{"(&(objectClass=person)(!(uid=alice)))", 1},
	}

	for _, tc := range cases {
		res, err := conn.Search(ldap.NewSearchRequest(ldaptest.PeopleDN, ldap.ScopeWholeSubtree,
			ldap.NeverDerefAliases, 0, 0, false, tc.filter, []string{"uid"}, nil))
		if err != nil {
			t.Fatalf("search %s: %v", tc.filter, err)
		}

		if len(res.Entries) != tc.want {
			t.Fatalf("search %s found %d entries, want %d", tc.filter, len(res.Entries), tc.want)
		}
	}
}

// TestBinds checks the binds of the directory, the unauthenticated bind
// included, and that it records no password.
func TestBinds(t *testing.T) {
	t.Parallel()

	s := ldaptest.Start(t)
	dn := s.AddUser(ldaptest.User{Attributes: nil, UserName: "alice", Password: "secret-a"})
	conn := dial(t, s)

	if err := conn.Bind(dn, "wrong"); !ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
		t.Fatalf("bind with a wrong password = %v, want invalid credentials", err)
	}

	if err := conn.UnauthenticatedBind(dn); err != nil {
		t.Fatalf("unauthenticated bind = %v, want success", err)
	}

	if err := conn.Bind(dn, "secret-a"); err != nil {
		t.Fatalf("bind = %v", err)
	}

	for _, b := range s.Binds() {
		if b == "secret-a" || b == "wrong" {
			t.Fatalf("binds = %q, hold a password", s.Binds())
		}
	}
}
