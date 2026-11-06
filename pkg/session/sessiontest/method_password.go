package sessiontest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// PasswordFixture is a password method with a test directory.
type PasswordFixture struct {
	// Method is the method under test.
	Method session.PasswordMethod
	// AddUser adds the person p with the password to the test directory, and
	// returns the subject that the method reports for p. A method that
	// makes its own subjects ignores p.Subject.
	AddUser func(t *testing.T, p TestPerson, password string) string
	// UserNameForms returns other forms of a user name that the method
	// takes as the same user, for example the name in upper case.
	UserNameForms func(name string) []string
	// Stop makes the test directory stop answering.
	Stop func(t *testing.T)
	// Disable turns the provider of the method off.
	Disable func(t *testing.T)
	// Now is the clock of the method.
	Now func() time.Time
	// ProviderID names the provider of the method.
	ProviderID string
	// Groups is true for a method that reads groups.
	Groups bool
	// ReportsDisabled is true for a method whose directory can report a
	// person as disabled.
	ReportsDisabled bool
}

// errSubjectLocked is the error of a gate that stops a bind.
var errSubjectLocked = errors.New("the subject is locked")

// Passwords of the suite. Each one passes the password rules of the local
// users.
const (
	suitePassword      = "violet-harbor-tin-81-ledger"
	otherSuitePassword = "copper-meadow-quiet-47-lantern"
)

// passwordCase is one case of RunPasswordMethod.
type passwordCase struct {
	name string
	run  func(t *testing.T, f PasswordFixture)
	// when reports whether the case applies to the features of the
	// fixture; nil applies to every fixture.
	when func(f PasswordFixture) bool
}

// RunPasswordMethod runs the conformance suite of a session.PasswordMethod.
// The factory returns a fixture over a fresh test directory.
func RunPasswordMethod(t *testing.T, factory func(t *testing.T) PasswordFixture) {
	t.Helper()

	// The features of one fixture choose the cases. Each case then runs on
	// a fixture of its own.
	probe := factory(t)

	cases := passwordCases()
	cases = append(cases, passwordErrorCases()...)

	for _, c := range cases {
		// A case of a feature that the method does not have is not run: the
		// method has no such rule. Each feature has a case for the method
		// without it too.
		if c.when != nil && !c.when(probe) {
			continue
		}

		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, factory(t))
		})
	}

	if kind := probe.Method.Kind(); kind == session.MethodLDAP {
		unwrittenCases(t, kind, passwordProtocolCases())
	}
}

// passwordProtocolCases returns the cases of the LDAP protocol. The first
// LDAP adapter writes their bodies.
func passwordProtocolCases() []string {
	return []string{
		"AnObjectGUIDSubjectHasTheCanonicalTextForm",
		"AnEntryUUIDSubjectIsLowercase",
		"GroupsComeFromTheConfiguredSource",
		"NestedGroupsAreFlat",
		"FilterCharactersInTheUserNameAreEscaped",
		"DNCharactersInTheUserNameAreEscaped",
		"AReferralIsNeverFollowed",
		"TheConnectionGoesThroughTheEgressRules",
		"ASearchOverTheSizeLimitIsErrMethodAnswer",
		"AGroupSearchOverTheLimitIsErrMethodAnswer",
		"ADirectoryThatDoesNotAnswerInTimeIsErrMethodUnavailable",
		"APlainConnectionIsRefusedUnlessTheSettingAllowsIt",
		"TwoUsersWithOneUserNameAreErrMethodAnswer",
		"GroupsOnAndNoGroupsIsErrGroupsMissing",
	}
}

// pwGroups and the other conditions select the cases of a feature.
func pwGroups(f PasswordFixture) bool          { return f.Groups }
func pwNoGroups(f PasswordFixture) bool        { return !f.Groups }
func pwReportsDisabled(f PasswordFixture) bool { return f.ReportsDisabled }

// recordingGate is a session.BindGate that records each subject and stops
// each bind when stop is set.
type recordingGate struct {
	stop     error
	subjects []string
	mu       sync.Mutex
}

// BeforeBind implements session.BindGate.
func (g *recordingGate) BeforeBind(_ context.Context, subject string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.subjects = append(g.subjects, subject)

	return g.stop
}

// seen returns the subjects that the gate received.
func (g *recordingGate) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return slices.Clone(g.subjects)
}

// newGate returns a gate that lets each bind through.
func newGate() *recordingGate {
	return &recordingGate{stop: nil, subjects: nil, mu: sync.Mutex{}}
}

// person returns the person with the user name.
func person(userName string) TestPerson {
	return TestPerson{
		AuthTime: time.Time{}, Subject: userName + "-0001", UserName: userName, Name: "",
		Email: "", Groups: nil, GroupsPresent: false, Disabled: false,
	}
}

// verify calls Verify with the user name and the password.
func verify(
	t *testing.T, f PasswordFixture, gate session.BindGate, userName, password string,
) (session.Assertion, error) {
	t.Helper()

	return f.Method.Verify(t.Context(), session.Credentials{
		ProviderID: f.ProviderID, UserName: userName, Password: session.NewPassword(password),
	}, gate)
}

// mustVerify signs in with the user name and the password and returns the
// Assertion, which passes the check of the sign-in service.
func mustVerify(t *testing.T, f PasswordFixture, gate session.BindGate, userName, password string) session.Assertion {
	t.Helper()

	a, err := verify(t, f, gate, userName, password)
	if err != nil {
		t.Fatalf("Verify() = %v", err)
	}

	checkValid(t, a, f.Now())

	return a
}

// passwordCases are the cases of a good sign-in.
func passwordCases() []passwordCase {
	return []passwordCase{
		{
			name: "AGoodPasswordReturnsTheAssertionOfThePerson",
			when: nil,
			run:  pwAGoodPasswordReturnsTheAssertionOfThePerson,
		},
		{
			name: "TheSubjectIsTheStableAttributeNotTheUserName",
			when: nil,
			run:  pwTheSubjectIsTheStableAttributeNotTheUserName,
		},
		{
			name: "BeforeBindReceivesTheSubjectBeforeThePasswordIsSent", when: nil,
			run: pwBeforeBindReceivesTheSubjectBeforeThePasswordIsSent,
		},
		{
			name: "AnErrorOfBeforeBindStopsTheBindAndIsReturnedWrapped", when: nil,
			run: pwAnErrorOfBeforeBindStopsTheBindAndIsReturnedWrapped,
		},
		{
			name: "EachFormOfOneUserNameGivesTheSameSubjectToBeforeBind", when: nil,
			run: pwEachFormOfOneUserNameGivesTheSameSubjectToBeforeBind,
		},
		{name: "TheAssertionHoldsTheFactorPassword", when: nil, run: pwTheAssertionHoldsTheFactorPassword},
		{name: "TheAuthTimeIsKnownAndIsTheTimeOfTheBind", when: nil, run: pwTheAuthTimeIsKnownAndIsTheTimeOfTheBind},
		{name: "TheAssertionHasTheKindOfTheMethod", when: nil, run: pwTheAssertionHasTheKindOfTheMethod},
		{
			name: "AMethodWithNoGroupsReturnsGroupsPresentFalse", when: pwNoGroups,
			run: pwAMethodWithNoGroupsReturnsGroupsPresentFalse,
		},
		{
			name: "AGoodPasswordReturnsTheGroupsOfThePerson", when: pwGroups,
			run: pwAGoodPasswordReturnsTheGroupsOfThePerson,
		},
		{
			name: "ADisabledUserIsErrSubjectDisabledWithTheSubject", when: pwReportsDisabled,
			run: pwADisabledUserIsErrSubjectDisabledWithTheSubject,
		},
		{
			name: "ADisabledProviderReturnsErrProviderDisabled",
			when: nil,
			run:  pwADisabledProviderReturnsErrProviderDisabled,
		},
	}
}

// passwordErrorCases are the cases of hostile input, one layer each.
func passwordErrorCases() []passwordCase {
	return []passwordCase{
		// Layer 1: the boundary.
		{name: "AnEmptyUserNameIsErrMethodInput", when: nil, run: pwAnEmptyUserNameIsErrMethodInput},
		{
			name: "AUserNameLongerThanTheLimitIsErrMethodInput",
			when: nil,
			run:  pwAUserNameLongerThanTheLimitIsErrMethodInput,
		},
		{
			name: "AUserNameWithControlCharactersIsErrMethodInput", when: nil,
			run: pwAUserNameWithControlCharactersIsErrMethodInput,
		},
		{
			name: "AnEmptyPasswordIsErrMethodInputAndSendsNoBind", when: nil,
			run: pwAnEmptyPasswordIsErrMethodInputAndSendsNoBind,
		},
		{
			name: "ACredentialOfAnotherProviderIsErrMethodInput", when: nil,
			run: pwACredentialOfAnotherProviderIsErrMethodInput,
		},
		{name: "ANilGateIsErrMethodInputAndSendsNoBind", when: nil, run: pwANilGateIsErrMethodInputAndSendsNoBind},
		// Layer 2: encode or contain.
		{name: "AUserNameWithAWildcardFindsNoOtherUser", when: nil, run: pwAUserNameWithAWildcardFindsNoOtherUser},
		{name: "ThePasswordIsNeverInAnError", when: nil, run: pwThePasswordIsNeverInAnError},
		// Layer 4: the person of the check.
		{name: "TheBindIsAsTheUserThatTheSearchFound", when: nil, run: pwTheBindIsAsTheUserThatTheSearchFound},
		// Layer 5: fail closed.
		{name: "AnUnknownUserNameIsErrMethodRejected", when: nil, run: pwAnUnknownUserNameIsErrMethodRejected},
		{name: "AWrongPasswordIsErrMethodRejected", when: nil, run: pwAWrongPasswordIsErrMethodRejected},
		{
			name: "AnUnknownUserAndAWrongPasswordGiveTheSameError", when: nil,
			run: pwAnUnknownUserAndAWrongPasswordGiveTheSameError,
		},
		{name: "AStoppedDirectoryIsErrMethodUnavailable", when: nil, run: pwAStoppedDirectoryIsErrMethodUnavailable},
		{
			name: "EachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion", when: nil,
			run: pwEachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion,
		},
	}
}

// pwAGoodPasswordReturnsTheAssertionOfThePerson is the case AGoodPasswordReturnsTheAssertionOfThePerson.
func pwAGoodPasswordReturnsTheAssertionOfThePerson(t *testing.T, f PasswordFixture) {
	t.Helper()

	p := person("alice")
	subject := f.AddUser(t, p, suitePassword)

	a := mustVerify(t, f, newGate(), p.UserName, suitePassword)
	if a.Subject != subject || a.UserName != p.UserName || a.ProviderID != f.ProviderID {
		t.Fatalf("Assertion = %+v, want subject %q, user %q and provider %q", a, subject, p.UserName,
			f.ProviderID)
	}
}

// pwTheSubjectIsTheStableAttributeNotTheUserName is the case TheSubjectIsTheStableAttributeNotTheUserName.
func pwTheSubjectIsTheStableAttributeNotTheUserName(t *testing.T, f PasswordFixture) {
	t.Helper()

	p := person("alice")
	subject := f.AddUser(t, p, suitePassword)

	if subject == "" || subject == p.UserName {
		t.Fatalf("subject = %q, want a stable value that is not the user name", subject)
	}
}

// pwBeforeBindReceivesTheSubjectBeforeThePasswordIsSent is the case
// BeforeBindReceivesTheSubjectBeforeThePasswordIsSent.
func pwBeforeBindReceivesTheSubjectBeforeThePasswordIsSent(t *testing.T, f PasswordFixture) {
	t.Helper()

	p := person("alice")
	subject := f.AddUser(t, p, suitePassword)
	gate := newGate()

	mustVerify(t, f, gate, p.UserName, suitePassword)

	if got := gate.seen(); !slices.Equal(got, []string{subject}) {
		t.Fatalf("subjects of BeforeBind = %q, want [%q]", got, subject)
	}
}

// pwAnErrorOfBeforeBindStopsTheBindAndIsReturnedWrapped is the case
// AnErrorOfBeforeBindStopsTheBindAndIsReturnedWrapped.
func pwAnErrorOfBeforeBindStopsTheBindAndIsReturnedWrapped(t *testing.T, f PasswordFixture) {
	t.Helper()

	p := person("alice")
	f.AddUser(t, p, suitePassword)

	stop := errSubjectLocked
	gate := newGate()
	gate.stop = stop

	for _, password := range []string{suitePassword, otherSuitePassword} {
		a, err := verify(t, f, gate, p.UserName, password)
		checkRefused(t, a, err, stop)
	}
}

// pwEachFormOfOneUserNameGivesTheSameSubjectToBeforeBind is the case
// EachFormOfOneUserNameGivesTheSameSubjectToBeforeBind.
func pwEachFormOfOneUserNameGivesTheSameSubjectToBeforeBind(t *testing.T, f PasswordFixture) {
	t.Helper()

	p := person("alice")
	subject := f.AddUser(t, p, suitePassword)

	for _, form := range f.UserNameForms(p.UserName) {
		gate := newGate()
		mustVerify(t, f, gate, form, suitePassword)

		if got := gate.seen(); !slices.Equal(got, []string{subject}) {
			t.Fatalf("form %q: subjects of BeforeBind = %q, want [%q]", form, got, subject)
		}
	}
}

// pwTheAssertionHoldsTheFactorPassword is the case TheAssertionHoldsTheFactorPassword.
func pwTheAssertionHoldsTheFactorPassword(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	if a := mustVerify(t, f, newGate(), "alice", suitePassword); !slices.Equal(a.Factors,
		[]session.Factor{session.FactorPassword}) {
		t.Fatalf("factors = %v, want [%s]", a.Factors, session.FactorPassword)
	}
}

// pwTheAuthTimeIsKnownAndIsTheTimeOfTheBind is the case TheAuthTimeIsKnownAndIsTheTimeOfTheBind.
func pwTheAuthTimeIsKnownAndIsTheTimeOfTheBind(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	before := f.Now().Truncate(time.Millisecond)
	a := mustVerify(t, f, newGate(), "alice", suitePassword)
	after := f.Now()

	if !a.AuthTimeKnown || a.AuthTime.Before(before) || a.AuthTime.After(after) {
		t.Fatalf("auth time = %v (known %v), want between %v and %v", a.AuthTime, a.AuthTimeKnown, before,
			after)
	}
}

// pwTheAssertionHasTheKindOfTheMethod is the case TheAssertionHasTheKindOfTheMethod.
func pwTheAssertionHasTheKindOfTheMethod(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	if a := mustVerify(t, f, newGate(), "alice", suitePassword); a.Method != f.Method.Kind() {
		t.Fatalf("method = %q, want %q", a.Method, f.Method.Kind())
	}
}

// pwAMethodWithNoGroupsReturnsGroupsPresentFalse is the case AMethodWithNoGroupsReturnsGroupsPresentFalse.
func pwAMethodWithNoGroupsReturnsGroupsPresentFalse(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	if a := mustVerify(t, f, newGate(), "alice", suitePassword); a.GroupsPresent || len(a.Groups) != 0 {
		t.Fatalf("groups = %v (present %v), want none", a.Groups, a.GroupsPresent)
	}
}

// pwAGoodPasswordReturnsTheGroupsOfThePerson is the case AGoodPasswordReturnsTheGroupsOfThePerson.
func pwAGoodPasswordReturnsTheGroupsOfThePerson(t *testing.T, f PasswordFixture) {
	t.Helper()

	p := person("alice")
	p.Groups, p.GroupsPresent = []string{"ops", "dev"}, true
	f.AddUser(t, p, suitePassword)

	checkGroups(t, mustVerify(t, f, newGate(), p.UserName, suitePassword), p)
}

// pwADisabledUserIsErrSubjectDisabledWithTheSubject is the case ADisabledUserIsErrSubjectDisabledWithTheSubject.
func pwADisabledUserIsErrSubjectDisabledWithTheSubject(t *testing.T, f PasswordFixture) {
	t.Helper()

	p := person("alice")
	p.Disabled = true
	subject := f.AddUser(t, p, suitePassword)

	a, err := verify(t, f, newGate(), p.UserName, suitePassword)
	if !errors.Is(err, session.ErrSubjectDisabled) || a.Subject != subject ||
		a.ProviderID != f.ProviderID {
		t.Fatalf("Verify() = %+v, %v, want ErrSubjectDisabled with the subject", a, err)
	}
}

// pwADisabledProviderReturnsErrProviderDisabled is the case ADisabledProviderReturnsErrProviderDisabled.
func pwADisabledProviderReturnsErrProviderDisabled(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)
	f.Disable(t)

	a, err := verify(t, f, newGate(), "alice", suitePassword)
	checkRefused(t, a, err, session.ErrProviderDisabled)
}

// pwAnEmptyUserNameIsErrMethodInput is the case AnEmptyUserNameIsErrMethodInput.
func pwAnEmptyUserNameIsErrMethodInput(t *testing.T, f PasswordFixture) {
	t.Helper()

	a, err := verify(t, f, newGate(), "", suitePassword)
	checkRefused(t, a, err, session.ErrMethodInput)
}

// pwAUserNameLongerThanTheLimitIsErrMethodInput is the case AUserNameLongerThanTheLimitIsErrMethodInput.
func pwAUserNameLongerThanTheLimitIsErrMethodInput(t *testing.T, f PasswordFixture) {
	t.Helper()

	a, err := verify(t, f, newGate(), strings.Repeat("a", session.MaxUserName+1), suitePassword)
	checkRefused(t, a, err, session.ErrMethodInput)
}

// pwAUserNameWithControlCharactersIsErrMethodInput is the case AUserNameWithControlCharactersIsErrMethodInput.
func pwAUserNameWithControlCharactersIsErrMethodInput(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	for _, name := range []string{"alice\x00", "alice\r\nadmin", "\talice", "alice\u0085"} {
		a, err := verify(t, f, newGate(), name, suitePassword)
		checkRefused(t, a, err, session.ErrMethodInput)
	}
}

// pwAnEmptyPasswordIsErrMethodInputAndSendsNoBind is the case AnEmptyPasswordIsErrMethodInputAndSendsNoBind.
func pwAnEmptyPasswordIsErrMethodInputAndSendsNoBind(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)
	gate := newGate()

	a, err := verify(t, f, gate, "alice", "")
	checkRefused(t, a, err, session.ErrMethodInput)

	if got := gate.seen(); len(got) != 0 {
		t.Fatalf("BeforeBind received %q for an empty password, want no call", got)
	}
}

// pwACredentialOfAnotherProviderIsErrMethodInput is the case ACredentialOfAnotherProviderIsErrMethodInput.
func pwACredentialOfAnotherProviderIsErrMethodInput(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	a, err := f.Method.Verify(t.Context(), session.Credentials{
		ProviderID: "other", UserName: "alice", Password: session.NewPassword(suitePassword),
	}, newGate())
	checkRefused(t, a, err, session.ErrMethodInput)
}

// pwANilGateIsErrMethodInputAndSendsNoBind is the case ANilGateIsErrMethodInputAndSendsNoBind.
func pwANilGateIsErrMethodInputAndSendsNoBind(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	a, err := verify(t, f, nil, "alice", suitePassword)
	checkRefused(t, a, err, session.ErrMethodInput)
}

// pwAUserNameWithAWildcardFindsNoOtherUser is the case AUserNameWithAWildcardFindsNoOtherUser.
func pwAUserNameWithAWildcardFindsNoOtherUser(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	for _, name := range []string{"*", "a*", "al%", "alic?", "(uid=alice)"} {
		a, err := verify(t, f, newGate(), name, suitePassword)
		if !errors.Is(err, session.ErrMethodRejected) && !errors.Is(err, session.ErrMethodInput) {
			t.Fatalf("Verify(%q) = %v, want ErrMethodRejected or ErrMethodInput", name, err)
		}

		if !zeroAssertion(a) {
			t.Fatalf("Verify(%q) returned %+v, want the zero Assertion", name, a)
		}
	}
}

// pwThePasswordIsNeverInAnError is the case ThePasswordIsNeverInAnError.
func pwThePasswordIsNeverInAnError(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	for _, name := range []string{"alice", "bob", ""} {
		_, err := verify(t, f, newGate(), name, otherSuitePassword)
		if err == nil {
			t.Fatalf("Verify(%q) with a wrong password = nil", name)
		}

		if text := fmt.Sprintf("%v %+v %#v", err, err, err); strings.Contains(text, otherSuitePassword) {
			t.Fatalf("the error of Verify(%q) holds the password", name)
		}
	}
}

// pwTheBindIsAsTheUserThatTheSearchFound is the case TheBindIsAsTheUserThatTheSearchFound.
func pwTheBindIsAsTheUserThatTheSearchFound(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)
	bob := f.AddUser(t, person("bob"), otherSuitePassword)

	a, err := verify(t, f, newGate(), "alice", otherSuitePassword)
	checkRefused(t, a, err, session.ErrMethodRejected)

	if got := mustVerify(t, f, newGate(), "bob", otherSuitePassword); got.Subject != bob {
		t.Fatalf("subject = %q, want the subject %q of bob", got.Subject, bob)
	}
}

// pwAnUnknownUserNameIsErrMethodRejected is the case AnUnknownUserNameIsErrMethodRejected.
func pwAnUnknownUserNameIsErrMethodRejected(t *testing.T, f PasswordFixture) {
	t.Helper()

	a, err := verify(t, f, newGate(), "carol", suitePassword)
	checkRefused(t, a, err, session.ErrMethodRejected)
}

// pwAWrongPasswordIsErrMethodRejected is the case AWrongPasswordIsErrMethodRejected.
func pwAWrongPasswordIsErrMethodRejected(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	a, err := verify(t, f, newGate(), "alice", otherSuitePassword)
	checkRefused(t, a, err, session.ErrMethodRejected)
}

// pwAnUnknownUserAndAWrongPasswordGiveTheSameError is the case AnUnknownUserAndAWrongPasswordGiveTheSameError.
func pwAnUnknownUserAndAWrongPasswordGiveTheSameError(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	_, unknown := verify(t, f, newGate(), "carol", suitePassword)
	_, wrong := verify(t, f, newGate(), "alice", otherSuitePassword)

	if unknown == nil || wrong == nil || unknown.Error() != wrong.Error() {
		t.Fatalf("errors = %v and %v, want the same error", unknown, wrong)
	}
}

// pwAStoppedDirectoryIsErrMethodUnavailable is the case AStoppedDirectoryIsErrMethodUnavailable.
func pwAStoppedDirectoryIsErrMethodUnavailable(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)
	f.Stop(t)

	a, err := verify(t, f, newGate(), "alice", suitePassword)
	checkRefused(t, a, err, session.ErrMethodUnavailable)
}

// pwEachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion is the case
// EachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion.
func pwEachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion(t *testing.T, f PasswordFixture) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	for _, c := range []struct{ name, password string }{
		{"alice", otherSuitePassword},
		{"carol", suitePassword},
		{"", suitePassword},
		{"alice", ""},
		{"alice\n", suitePassword},
	} {
		a, err := verify(t, f, newGate(), c.name, c.password)
		if err == nil || errors.Is(err, session.ErrSubjectDisabled) || !zeroAssertion(a) {
			t.Fatalf("Verify(%q) = %+v, %v, want an error and the zero Assertion", c.name, a, err)
		}
	}
}
