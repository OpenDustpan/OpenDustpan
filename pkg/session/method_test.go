package session_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// TestPasswordNeverPrints checks that no format verb shows a password, also
// not inside Credentials.
func TestPasswordNeverPrints(t *testing.T) {
	t.Parallel()

	const secret = "correct horse battery staple"

	p := session.NewPassword(secret)
	if p.Reveal() != secret {
		t.Fatalf("Reveal() = %q, want the password", p.Reveal())
	}

	c := session.Credentials{ProviderID: "ldap", UserName: "alice", Password: p}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		for _, v := range []any{p, c} {
			if got := fmt.Sprintf(verb, v); strings.Contains(got, secret) {
				t.Errorf("Sprintf(%q) = %q, which shows the password", verb, got)
			}
		}
	}
}

// assertion returns an Assertion with the person fields set and each other
// field empty.
func assertion(provider, subject, principalID string, method session.MethodKind) session.Assertion {
	return session.Assertion{
		AuthTime: time.Time{}, ProviderID: provider, PrincipalID: principalID, Subject: subject,
		UserName: "", Email: "", Method: method, Names: nil, Groups: nil, Factors: nil,
		EmailVerified: false, GroupsPresent: false, AuthTimeKnown: false,
	}
}

// TestCheckFreshSubject checks that a fresh authentication passes only for
// the person of the session, and fails closed for every other input.
func TestCheckFreshSubject(t *testing.T) {
	t.Parallel()

	want := session.FreshTarget{PrincipalID: "prn-1", Subject: "corp:alice"}
	saml := session.MethodSAML

	cases := []struct {
		name string
		want session.FreshTarget
		a    session.Assertion
		ok   bool
	}{
		{"same subject", want, assertion("corp", "alice", "", saml), true},
		{"other subject", want, assertion("corp", "bob", "", saml), false},
		{"other provider", want, assertion("corp2", "alice", "", saml), false},
		{"provider joined into the subject", want, assertion("", "corp:alice", "", saml), false},
		{"case differs", want, assertion("corp", "Alice", "", saml), false},
		{"empty assertion", want, assertion("", "", "", ""), false},
		{"local same principal", want, assertion("local", "", "prn-1", session.MethodPasskey), true},
		{"local other principal", want, assertion("local", "", "prn-2", session.MethodLocalPassword), false},
		{"principal from a method port", want, assertion("corp", "", "prn-1", session.MethodOIDC), false},
		{
			"empty target",
			session.FreshTarget{PrincipalID: "", Subject: ""},
			assertion("corp", "alice", "", saml),
			false,
		},
		{
			"target with no subject",
			session.FreshTarget{PrincipalID: "prn-1", Subject: ""},
			assertion("local", "", "prn-1", session.MethodPasskey),
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := session.CheckFreshSubject(tc.want, tc.a)
			if tc.ok && err != nil {
				t.Fatalf("CheckFreshSubject() = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, session.ErrFreshSubject) {
				t.Fatalf("CheckFreshSubject() = %v, want ErrFreshSubject", err)
			}
		})
	}
}
