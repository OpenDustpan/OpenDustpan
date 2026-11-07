package sessiontest

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// The suites in this file and in method_redirect.go and method_password.go
// test the ports of the sign-in methods: session.RedirectMethod,
// session.PasswordMethod and session.RequestMethod. Each case names one
// rule. A case that sends hostile input tests one defense-in-depth layer
// alone, as if every other layer had failed.
//
// A suite runs each case that applies to every method. A case that applies
// only to a method that has a feature, for example groups, runs when the
// fixture declares the feature. A case of one protocol, for example a SAML
// signature, runs for the methods of that kind only. Until the first adapter
// of a kind writes the body of such a case, the case fails for that kind,
// so the first adapter cannot pass the suite without it.

// TestPerson is a person at the test identity provider or the test
// directory of a fixture.
type TestPerson struct {
	// AuthTime is the time of the authentication that the provider
	// reports. The zero time lets the provider use its own clock.
	AuthTime time.Time
	// Subject is the stable subject at the provider. A fixture whose
	// provider makes its own subjects ignores it.
	Subject string
	// UserName is the user name at the provider.
	UserName string
	// Name is the display name at the provider.
	Name string
	// Email is the email at the provider.
	Email string
	// Groups are the groups of the person at the provider.
	Groups []string
	// GroupsPresent is false for a provider that sends no groups value.
	GroupsPresent bool
	// Disabled is true for a person that the provider reports as disabled.
	Disabled bool
}

// unwrittenCases runs one failing subtest for each name: each case belongs
// to a kind of method whose first adapter must write its body.
func unwrittenCases(t *testing.T, kind session.MethodKind, names []string) {
	t.Helper()

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Fatalf("the case %s of the kind %q has no body: the first adapter of the kind writes it", name, kind)
		})
	}
}

// checkRefused fails unless err is want and a is the zero Assertion.
func checkRefused(t *testing.T, a session.Assertion, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}

	if !zeroAssertion(a) {
		t.Fatalf("the Assertion of an error is %+v, want the zero value", a)
	}
}

// zeroAssertion reports whether a is the zero Assertion.
func zeroAssertion(a session.Assertion) bool {
	return zeroPerson(a) && len(a.Names) == 0 && len(a.Groups) == 0 && len(a.Factors) == 0 &&
		!a.EmailVerified && !a.GroupsPresent && !a.AuthTimeKnown
}

// zeroPerson reports whether a names no person, no method and no time.
func zeroPerson(a session.Assertion) bool {
	return a.AuthTime.IsZero() && a.ProviderID == "" && a.PrincipalID == "" && a.Subject == "" &&
		a.UserName == "" && a.Email == "" && a.Method == ""
}

// checkValid fails unless a passes the check of the sign-in service at
// now, so that no adapter returns an Assertion that the service refuses.
func checkValid(t *testing.T, a session.Assertion, now time.Time) {
	t.Helper()

	if err := session.CheckAssertion(a, now); err != nil {
		t.Fatalf("CheckAssertion() = %v, want nil", err)
	}
}

// checkGroups fails unless a holds the groups of p, as a method with groups
// reports them.
func checkGroups(t *testing.T, a session.Assertion, p TestPerson) {
	t.Helper()

	if !a.GroupsPresent || !slices.Equal(sortedCopy(a.Groups), sortedCopy(p.Groups)) {
		t.Fatalf("groups = %v (present %v), want %v", a.Groups, a.GroupsPresent, p.Groups)
	}
}

// sortedCopy returns a sorted copy of s.
func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)

	return out
}

// manyGroups returns n group names.
func manyGroups(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "group-" + strconv.Itoa(i)
	}

	return out
}
