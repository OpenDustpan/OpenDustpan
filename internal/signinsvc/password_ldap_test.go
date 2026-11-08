package signinsvc_test

import (
	"context"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/internal/ldaptest"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
)

// ldapUUID is the entryUUID of the test person of the directory.
const ldapUUID = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

// newLDAPMethod returns the LDAP method of the provider ldapID over dir, over
// TLS.
func newLDAPMethod(t *testing.T, dir *ldaptest.Server) *ldapauth.Method {
	t.Helper()

	m, err := ldapauth.NewMethod(ldapauth.Config{
		Enabled:    func(context.Context) (bool, error) { return true, nil },
		AllowPlain: func(context.Context) (bool, error) { return false, nil },
		Now:        testNow, Logger: nil, RootCAs: dir.CertPool(), ProviderID: ldapID, URL: dir.LDAPSURL(),
		BindDN: ldaptest.ServiceDN, BaseDN: ldaptest.BaseDN, UserFilter: "(&(objectClass=person)(uid={user}))",
		UserNameAttribute: "uid", EmailAttribute: "", NameAttribute: "",
		BindPassword: session.NewPassword(dir.ServicePassword()),
		Subject:      ldapauth.Subject{Kind: ldapauth.SubjectEntryUUID, Attribute: ""},
		Disabled:     ldapauth.Disabled{Attribute: "", Values: nil, Mask: 0},
		Groups: ldapauth.Groups{
			Source: ldapauth.GroupsOff, BaseDN: "", Filter: "", MemberAttribute: "", MemberOfAttribute: "",
			NameAttribute: "",
		},
		Egress:         egress.Rules{Allow: nil, LoopbackForTests: true},
		ConnectTimeout: 0, OperationTimeout: 0, GroupTimeout: 0, StartTLS: false,
	})
	if err != nil {
		t.Fatalf("NewMethod() = %v", err)
	}

	return m
}

// TestPasswordSignInOverTheLDAPMethod signs a person in through the LDAP
// method over TLS: the gate counts the entryUUID that the method found, and
// the session starts. A locked name then sends no bind as the person to the
// directory, also with the right password.
func TestPasswordSignInOverTheLDAPMethod(t *testing.T) {
	t.Parallel()

	dir := ldaptest.Start(t)
	dir.AddUser(ldaptest.User{
		Attributes: map[string][]string{"entryUUID": {ldapUUID}}, UserName: "alice", Password: "right",
	})

	h := newPasswordHarness(t, nil)
	h.w.principal.Subject = ldapID + ":" + ldapUUID
	method := newLDAPMethod(t, dir)

	signIn := func(password string) error {
		_, err := h.svc.CompletePassword(t.Context(), &signinsvc.PasswordRequest{
			Entry: callerEntry(), Provider: ldapProvider(), Method: method, UserName: "alice",
			Password: session.NewPassword(password),
		})

		return err
	}

	if err := signIn("right"); err != nil {
		t.Fatalf("sign-in = %v, want a session", err)
	}

	for range lockLimit {
		if err := signIn("wrong"); err == nil {
			t.Fatal("a wrong password signed in")
		}
	}

	before := countOf(dir.Binds(), ldaptest.UserDN("alice"))

	err := signIn("right")
	if got := refusalReason(t, err); got != signinsvc.ReasonNameLocked {
		t.Fatalf("sign-in of a locked name = %q, want %q", got, signinsvc.ReasonNameLocked)
	}

	if after := countOf(dir.Binds(), ldaptest.UserDN("alice")); after != before {
		t.Fatalf("a locked name sent %d binds as the person, want none", after-before)
	}
}

// countOf returns how many times v is in list.
func countOf(list []string, v string) int {
	n := 0

	for i := range slices.Values(list) {
		if i == v {
			n++
		}
	}

	return n
}
