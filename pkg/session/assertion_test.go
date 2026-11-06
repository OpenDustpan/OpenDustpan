package session_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// checkNow is the time of each CheckAssertion call of the tests.
func checkNow() time.Time {
	return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
}

// oidcAssertion returns a valid Assertion of an OIDC sign-in.
func oidcAssertion() session.Assertion {
	return session.Assertion{
		AuthTime: checkNow().Add(-time.Minute), ProviderID: "corp", PrincipalID: "", Subject: "alice-1",
		UserName: "alice", Email: "alice@example.com", Method: session.MethodOIDC,
		Names: []string{"Alice Example", "alice"}, Groups: nil, Factors: nil, EmailVerified: true,
		GroupsPresent: false, AuthTimeKnown: true,
	}
}

// localAssertion returns a valid Assertion of a local password sign-in.
func localAssertion() session.Assertion {
	return session.Assertion{
		AuthTime: checkNow(), ProviderID: session.LocalProviderID, PrincipalID: "prn-1", Subject: "usr-1",
		UserName: "alice", Email: "", Method: session.MethodLocalPassword, Names: nil, Groups: nil,
		Factors: []session.Factor{session.FactorPassword}, EmailVerified: false, GroupsPresent: false,
		AuthTimeKnown: true,
	}
}

// oauthAssertion returns a valid Assertion of a plain OAuth 2.0 sign-in.
func oauthAssertion() session.Assertion {
	a := oidcAssertion()
	a.Method, a.AuthTime, a.AuthTimeKnown, a.EmailVerified = session.MethodOAuth, time.Time{}, false, false

	return a
}

// TestCheckAssertionAccepts checks that a valid Assertion of each existing
// method passes.
func TestCheckAssertionAccepts(t *testing.T) {
	t.Parallel()

	withGroups := oidcAssertion()
	withGroups.Groups, withGroups.GroupsPresent = []string{"ops", strings.Repeat("g", session.MaxAssertionGroup)}, true

	emptyGroups := oidcAssertion()
	emptyGroups.Groups, emptyGroups.GroupsPresent = []string{}, true

	skew := oidcAssertion()
	skew.AuthTime = checkNow().Add(session.AssertionSkew)

	longest := oidcAssertion()
	longest.Subject = strings.Repeat("s", session.MaxAssertionSubject)
	longest.Email = strings.Repeat("e", session.MaxAssertionName)

	cases := []struct {
		name string
		a    session.Assertion
	}{
		{"oidc", oidcAssertion()},
		{"oauth", oauthAssertion()},
		{"local password", localAssertion()},
		{"groups", withGroups},
		{"empty groups that are present", emptyGroups},
		{"auth time at the edge of the skew", skew},
		{"longest values", longest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := session.CheckAssertion(tc.a, checkNow()); err != nil {
				t.Fatalf("CheckAssertion() = %v, want nil", err)
			}
		})
	}
}

// TestCheckAssertionRefusesHostileInput sends each hostile Assertion to the
// check of the sign-in service directly, as if the adapter had failed to
// refuse it. Each one is refused with ErrInvalidAssertion, and the error
// never holds a value of the Assertion.
func TestCheckAssertionRefusesHostileInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(a *session.Assertion)
		base   func() session.Assertion
	}{
		{"empty subject", func(a *session.Assertion) { a.Subject = "" }, oidcAssertion},
		{"empty local subject", func(a *session.Assertion) { a.Subject = "" }, localAssertion},
		{"future auth time", func(a *session.Assertion) {
			a.AuthTime = checkNow().Add(session.AssertionSkew + time.Second)
		}, oidcAssertion},
		{"future auth time of a password", func(a *session.Assertion) {
			a.AuthTime = checkNow().Add(time.Hour)
		}, localAssertion},
		{"subject over the limit", func(a *session.Assertion) {
			a.Subject = strings.Repeat("s", session.MaxAssertionSubject+1)
		}, oidcAssertion},
		{"subject with a newline", func(a *session.Assertion) { a.Subject = "alice\nadmin" }, oidcAssertion},
		{"subject with a C1 control", func(a *session.Assertion) { a.Subject = "alice\u0085" }, oidcAssertion},
		{"subject not UTF-8", func(a *session.Assertion) { a.Subject = "\xff\xfe" }, oidcAssertion},
		{"name over the limit", func(a *session.Assertion) {
			a.Names = []string{strings.Repeat("n", session.MaxAssertionName+1)}
		}, oidcAssertion},
		{"email with a control", func(a *session.Assertion) { a.Email = "a\x00@example.com" }, oidcAssertion},
		{"verified empty email", func(a *session.Assertion) { a.Email = "" }, oidcAssertion},
		{"unknown method", func(a *session.Assertion) { a.Method = "kerberos" }, oidcAssertion},
		{"empty method", func(a *session.Assertion) { a.Method = "" }, oidcAssertion},
		{"empty provider", func(a *session.Assertion) { a.ProviderID = "" }, oidcAssertion},
		{"provider local on a method port", func(a *session.Assertion) {
			a.ProviderID = session.LocalProviderID
		}, oidcAssertion},
		{"provider dev", func(a *session.Assertion) { a.ProviderID = session.DevProviderID }, oidcAssertion},
		{"provider with a colon", func(a *session.Assertion) { a.ProviderID = "corp:x" }, oidcAssertion},
		{"principal from a method port", func(a *session.Assertion) { a.PrincipalID = "prn-1" }, oidcAssertion},
		{"local with no principal", func(a *session.Assertion) { a.PrincipalID = "" }, localAssertion},
		{"local with another provider", func(a *session.Assertion) { a.ProviderID = "corp" }, localAssertion},
		{"groups that are not present", func(a *session.Assertion) { a.Groups = []string{"ops"} }, oidcAssertion},
		{"too many groups", func(a *session.Assertion) {
			a.GroupsPresent, a.Groups = true, make([]string, session.MaxAssertionGroups+1)
			for i := range a.Groups {
				a.Groups[i] = "g"
			}
		}, oidcAssertion},
		{"empty group", func(a *session.Assertion) { a.GroupsPresent, a.Groups = true, []string{""} }, oidcAssertion},
		{"group over the limit", func(a *session.Assertion) {
			a.GroupsPresent, a.Groups = true, []string{strings.Repeat("g", session.MaxAssertionGroup+1)}
		}, oidcAssertion},
		{"group with a control", func(a *session.Assertion) {
			a.GroupsPresent, a.Groups = true, []string{"ops\r\nadmins"}
		}, oidcAssertion},
		{"factor from a redirect method", func(a *session.Assertion) {
			a.Factors = []session.Factor{session.FactorPassword}
		}, oidcAssertion},
		{"unknown factor", func(a *session.Assertion) {
			a.Factors = append(a.Factors, "fingerprint")
		}, localAssertion},
		{"local password with no password factor", func(a *session.Assertion) {
			a.Factors = []session.Factor{session.FactorTOTP}
		}, localAssertion},
		{"zero known auth time", func(a *session.Assertion) { a.AuthTime = time.Time{} }, oidcAssertion},
		{"unknown auth time that is set", func(a *session.Assertion) {
			a.AuthTimeKnown = false
		}, oidcAssertion},
		{"password with an unknown auth time", func(a *session.Assertion) {
			a.AuthTimeKnown, a.AuthTime = false, time.Time{}
		}, localAssertion},
		{"plain OAuth with a known auth time", func(a *session.Assertion) {
			a.AuthTimeKnown, a.AuthTime = true, checkNow()
		}, oauthAssertion},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := tc.base()
			tc.change(&a)

			err := session.CheckAssertion(a, checkNow())
			if !errors.Is(err, session.ErrInvalidAssertion) {
				t.Fatalf("CheckAssertion() = %v, want ErrInvalidAssertion", err)
			}

			for _, v := range []string{a.Subject, a.Email, a.PrincipalID} {
				if len(v) > 3 && strings.Contains(err.Error(), v) {
					t.Fatalf("the error %q holds the value %q of the Assertion", err, v)
				}
			}
		})
	}
}

// TestProviderModesOfTheMethods checks that each mode names the kind of its
// method, and that a mode with no method yet fails the validation of a
// stored provider, so that such a provider is never stored.
func TestProviderModesOfTheMethods(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mode     session.ProviderMode
		kind     session.MethodKind
		storable bool
	}{
		{session.ModeOIDC, session.MethodOIDC, true},
		{session.ModeOAuth, session.MethodOAuth, true},
		{session.ModeSAML, session.MethodSAML, false},
		{session.ModeCAS, session.MethodCAS, false},
		{session.ModeLDAP, session.MethodLDAP, false},
		{session.ModeHeader, session.MethodHeader, false},
	}

	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			t.Parallel()

			kind, ok := tc.mode.MethodKind()
			if !ok || kind != tc.kind {
				t.Fatalf("MethodKind() = %q, %v, want %q", kind, ok, tc.kind)
			}

			if tc.storable {
				return
			}

			p := validProviderConfig()
			p.Mode, p.Issuer = tc.mode, ""

			if err := p.Validate(); !errors.Is(err, session.ErrInvalidProvider) {
				t.Fatalf("Validate() = %v, want ErrInvalidProvider", err)
			}
		})
	}

	if kind, ok := session.ProviderMode("kerberos").MethodKind(); ok || kind != "" {
		t.Fatalf("MethodKind() of an unknown mode = %q, %v, want false", kind, ok)
	}
}
