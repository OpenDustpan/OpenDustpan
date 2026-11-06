package localusersvc_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// TestSignInReturnsTheAssertion checks the Assertion of a good password
// sign-in: one factor, the password, a known time of the check, the
// principal, and the ID of the local user as the subject. The sign-in
// service accepts it.
func TestSignInReturnsTheAssertion(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	c := f.mustCreate(t, "alice", goodPassword)

	before := f.svc.Now()

	got, err := f.svc.SignIn(t.Context(), base(""), "Alice", goodPassword)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	a := got.Assertion
	if a.Method != session.MethodLocalPassword || a.ProviderID != session.LocalProviderID ||
		a.PrincipalID != c.Principal.ID || a.Subject != c.User.ID || a.UserName != "alice" {
		t.Fatalf("Assertion = %+v, want the local user %s of the principal %s", a, c.User.ID, c.Principal.ID)
	}

	checkLocalAssertion(t, a, before, f.svc.Now())
}

// checkLocalAssertion fails unless a holds one factor, the password, a known
// time of the check between before and after, and no groups, and passes the
// check of the sign-in service.
func checkLocalAssertion(t *testing.T, a session.Assertion, before, after time.Time) {
	t.Helper()

	if len(a.Factors) != 1 || a.Factors[0] != session.FactorPassword {
		t.Fatalf("factors = %v, want one factor, the password", a.Factors)
	}

	if !a.AuthTimeKnown || a.AuthTime.Before(before) || a.AuthTime.After(after) {
		t.Fatalf("auth time = %v (known %v), want the time of the check", a.AuthTime, a.AuthTimeKnown)
	}

	if a.GroupsPresent || len(a.Groups) != 0 {
		t.Fatalf("groups = %v (present %v), want none", a.Groups, a.GroupsPresent)
	}

	if err := session.CheckAssertion(a, after); err != nil {
		t.Fatalf("CheckAssertion() = %v", err)
	}
}
