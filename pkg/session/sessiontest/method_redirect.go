package sessiontest

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// RedirectFixture is a redirect method with a test identity provider.
type RedirectFixture struct {
	// Method is the method under test.
	Method session.RedirectMethod
	// Answer returns the callback that the test identity provider gives
	// for the redirect r, for the person p.
	Answer func(t *testing.T, r session.Redirect, p TestPerson) session.Callback
	// Broken returns a callback for the redirect r and the person p whose
	// answer of the identity provider fails a check of the method, for
	// example a bad signature.
	Broken func(t *testing.T, r session.Redirect, p TestPerson) session.Callback
	// AsksFresh reports whether the URL of the redirect r asks the identity
	// provider for a new authentication.
	AsksFresh func(t *testing.T, r session.Redirect) bool
	// Stop makes the test identity provider stop answering.
	Stop func(t *testing.T)
	// Disable turns the provider of the method off.
	Disable func(t *testing.T)
	// Now is the clock of the method.
	Now func() time.Time
	// ProviderID names the provider of the method.
	ProviderID string
	// AuthTime is true for a method that proves the time of the
	// authentication: OIDC, SAML and CAS.
	AuthTime bool
	// Groups is true for a method that reads groups.
	Groups bool
	// ReportsDisabled is true for a method whose provider can report a
	// person as disabled.
	ReportsDisabled bool
	// CAS holds the hooks of the cases of the CAS protocol. It is nil for
	// each other kind.
	CAS *CASFixture
}

// redirectCase is one case of RunRedirectMethod.
type redirectCase struct {
	name string
	run  func(t *testing.T, f RedirectFixture)
	// when reports whether the case applies to the features of the
	// fixture; nil applies to every fixture.
	when func(f RedirectFixture) bool
}

// RunRedirectMethod runs the conformance suite of a session.RedirectMethod.
// The factory returns a fixture over a fresh test identity provider.
func RunRedirectMethod(t *testing.T, factory func(t *testing.T) RedirectFixture) {
	t.Helper()

	// The features of one fixture choose the cases. Each case then runs on
	// a fixture of its own.
	probe := factory(t)

	cases := redirectCases()
	cases = append(cases, redirectGroupCases()...)
	cases = append(cases, redirectErrorCases()...)

	for _, c := range cases {
		// A case of a feature that the method does not have is not run: the
		// method has no such rule. Each feature has a case for the method
		// without it too, for example AMethodWithNoGroupsReturnsGroupsPresentFalse.
		if c.when != nil && !c.when(probe) {
			continue
		}

		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, factory(t))
		})
	}

	if kind := probe.Method.Kind(); kind == session.MethodCAS {
		runCASCases(t, probe, factory)
	} else if kind == session.MethodSAML {
		unwrittenCases(t, kind, redirectProtocolCases()[kind])
	}
}

// redirectProtocolCases returns the cases of each protocol of a redirect
// method. The first adapter of the kind writes their bodies.
func redirectProtocolCases() map[session.MethodKind][]string {
	return map[session.MethodKind][]string{
		session.MethodSAML: {
			"FinishReturnsTheSubjectOfTheConfiguredAttribute",
			"AnXMLAnswerWithADTDIsErrMethodAnswer",
			"AnXMLAnswerDeeperThanTheLimitIsErrMethodAnswer",
			"AnUnsignedAssertionIsErrMethodAnswer",
			"AResponseWithTwoAssertionsIsErrMethodAnswer",
			"ASignatureAlgorithmOutsideTheAllowlistIsErrMethodAnswer",
			"ASignatureOverAnotherElementIsErrMethodAnswer",
			"AnotherAudienceIsErrMethodAnswer",
			"AnotherIssuerIsErrMethodAnswer",
			"AnAssertionAnywhereOutsideTheSignedElementIsCounted",
			"AnEncryptedAssertionCountsAsAnAssertion",
			"AValueOutsideTheSignedElementIsNeverRead",
			"AReferenceURIThatIsNotTheIDOfTheSignedElementIsErrMethodAnswer",
			"ADigestAlgorithmOutsideTheAllowlistIsErrMethodAnswer",
			"ANewSigningCertificateInFetchedMetadataIsNotUsedUntilAccepted",
			"EachOutboundConnectionGoesThroughTheEgressRules",
			"AnEndpointThatResolvesToARefusedAddressIsErrMethodUnavailable",
			"AnAnswerOutsideTheClockSkewIsErrMethodAnswer",
			"ANotOnOrAfterThatIsMissingIsErrMethodAnswer",
			"ANotOnOrAfterTooFarInTheFutureIsErrMethodAnswer",
			"ASecondUseOfAnAssertionIDIsErrReplay",
			"AProviderThatDoesNotAnswerInTimeIsErrMethodUnavailable",
			"AnotherRequestIDIsErrRequestBinding",
		},
	}
}

// hasAuthTime and the other conditions select the cases of a feature.
func hasAuthTime(f RedirectFixture) bool     { return f.AuthTime }
func hasNoAuthTime(f RedirectFixture) bool   { return !f.AuthTime }
func hasGroups(f RedirectFixture) bool       { return f.Groups }
func hasNoGroups(f RedirectFixture) bool     { return !f.Groups }
func reportsDisabled(f RedirectFixture) bool { return f.ReportsDisabled }

// suiteState is the state of each Begin of the suite.
const suiteState = "c3VpdGUtc3RhdGUtb2YtdGhlLXJlZGlyZWN0LW1ldGhvZA"

// begin starts a sign-in of the fixture and fails the test on an error.
func begin(t *testing.T, f RedirectFixture, fresh bool) session.Redirect {
	t.Helper()

	r, err := f.Method.Begin(t.Context(), session.BeginInput{
		ProviderID: f.ProviderID, State: suiteState, Fresh: fresh,
	})
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}

	return r
}

// alice is the person of most cases. Her provider sends an empty groups
// value, so that a method with groups on accepts her too. A case of the
// groups sets her groups itself.
func alice() TestPerson {
	return TestPerson{
		AuthTime: time.Time{}, Subject: "alice-0001", UserName: "alice", Name: "Alice Example",
		Email: "alice@example.com", Groups: []string{}, GroupsPresent: true, Disabled: false,
	}
}

// signIn runs a full sign-in of p and returns the Assertion.
func signIn(t *testing.T, f RedirectFixture, p TestPerson, fresh bool) (session.Redirect, session.Assertion) {
	t.Helper()

	r := begin(t, f, fresh)

	a, err := f.Method.Finish(t.Context(), f.Answer(t, r, p))
	if err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	checkValid(t, a, f.Now())

	return r, a
}

// redirectCases are the cases of the start and the result of a sign-in.
func redirectCases() []redirectCase {
	return []redirectCase{
		{name: "BeginReturnsAnHTTPSURL", when: nil, run: rdBeginReturnsAnHTTPSURL},
		{name: "BeginKeepsTheStateInThePendingRequest", when: nil, run: rdBeginKeepsTheStateInThePendingRequest},
		{name: "BeginWithFreshAsksForANewAuthentication", when: nil, run: rdBeginWithFreshAsksForANewAuthentication},
		{name: "FinishReturnsTheAssertionOfThePerson", when: nil, run: rdFinishReturnsTheAssertionOfThePerson},
		{name: "TheAssertionHasTheKindOfTheMethod", when: nil, run: rdTheAssertionHasTheKindOfTheMethod},
		{name: "FinishReturnsNoFactor", when: nil, run: rdFinishReturnsNoFactor},
		{
			name: "AFreshAnswerHasAKnownAuthTimeNotBeforeTheStart", when: hasAuthTime,
			run: rdAFreshAnswerHasAKnownAuthTimeNotBeforeTheStart,
		},
		{
			name: "AFreshAnswerOlderThanItsRequestIsErrMethodAnswer", when: hasAuthTime,
			run: rdAFreshAnswerOlderThanItsRequestIsErrMethodAnswer,
		},
		{
			name: "AMethodWithNoAuthTimeReturnsAuthTimeKnownFalse", when: hasNoAuthTime,
			run: rdAMethodWithNoAuthTimeReturnsAuthTimeKnownFalse,
		},
		{
			name: "ADisabledProviderReturnsErrProviderDisabled",
			when: nil,
			run:  rdADisabledProviderReturnsErrProviderDisabled,
		},
		{
			name: "ADisabledPersonReturnsErrSubjectDisabledWithTheSubject", when: reportsDisabled,
			run: rdADisabledPersonReturnsErrSubjectDisabledWithTheSubject,
		},
	}
}

// redirectGroupCases are the cases of the groups of a sign-in.
func redirectGroupCases() []redirectCase {
	return []redirectCase{
		{
			name: "AMethodWithNoGroupsReturnsGroupsPresentFalse", when: hasNoGroups,
			run: rdAMethodWithNoGroupsReturnsGroupsPresentFalse,
		},
		{
			name: "FinishReturnsGroupsWhenTheProviderSendsThem", when: hasGroups,
			run: rdFinishReturnsGroupsWhenTheProviderSendsThem,
		},
		{name: "AnEmptyGroupsValueIsPresentAndEmpty", when: hasGroups, run: rdAnEmptyGroupsValueIsPresentAndEmpty},
		{
			name: "MoreGroupsThanTheLimitAreErrMethodAnswer", when: hasGroups,
			run: rdMoreGroupsThanTheLimitAreErrMethodAnswer,
		},
		{
			name: "AGroupLongerThanTheLimitIsErrMethodAnswer", when: hasGroups,
			run: rdAGroupLongerThanTheLimitIsErrMethodAnswer,
		},
		{name: "GroupsOnAndNoGroupsIsErrGroupsMissing", when: hasGroups, run: rdGroupsOnAndNoGroupsIsErrGroupsMissing},
	}
}

// redirectErrorCases are the cases of hostile input, one layer each.
func redirectErrorCases() []redirectCase {
	return []redirectCase{
		// Layer 1: the boundary.
		{
			name: "ACallbackOverTheSizeLimitIsErrMethodInput",
			when: nil,
			run:  rdACallbackOverTheSizeLimitIsErrMethodInput,
		},
		{
			name: "ACallbackWithControlCharactersIsErrMethodInput", when: nil,
			run: rdACallbackWithControlCharactersIsErrMethodInput,
		},
		{name: "ABeginWithHostileInputIsErrMethodInput", when: nil, run: rdABeginWithHostileInputIsErrMethodInput},
		// Layer 4: the binding to the browser.
		{name: "AnotherStateIsErrRequestBinding", when: nil, run: rdAnotherStateIsErrRequestBinding},
		{name: "ANilPendingRequestIsErrRequestBinding", when: nil, run: rdANilPendingRequestIsErrRequestBinding},
		{
			name: "APendingRequestOfAnotherProviderIsErrRequestBinding", when: nil,
			run: rdAPendingRequestOfAnotherProviderIsErrRequestBinding,
		},
		// Layer 3: limit the effect.
		{
			name: "AnAuthTimeInTheFutureIsErrMethodAnswer",
			when: hasAuthTime,
			run:  rdAnAuthTimeInTheFutureIsErrMethodAnswer,
		},
		// Layer 5: fail closed.
		{
			name: "AnErrorAnswerOfTheProviderIsErrMethodRejected", when: nil,
			run: rdAnErrorAnswerOfTheProviderIsErrMethodRejected,
		},
		{name: "AnUnknownAnswerIsErrMethodAnswer", when: nil, run: rdAnUnknownAnswerIsErrMethodAnswer},
		{name: "AStoppedProviderIsErrMethodUnavailable", when: nil, run: rdAStoppedProviderIsErrMethodUnavailable},
		{
			name: "EachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion", when: nil,
			run: rdEachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion,
		},
	}
}

// rdBeginReturnsAnHTTPSURL is the case BeginReturnsAnHTTPSURL.
func rdBeginReturnsAnHTTPSURL(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)

	u, err := url.Parse(r.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		t.Fatalf("URL = %q, want an absolute https URL", r.URL)
	}
}

// rdBeginKeepsTheStateInThePendingRequest is the case BeginKeepsTheStateInThePendingRequest.
func rdBeginKeepsTheStateInThePendingRequest(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)

	p := r.Pending
	if p.State != suiteState || p.ProviderID != f.ProviderID || p.StartedAt.IsZero() || p.Fresh ||
		p.PrincipalID != "" || p.SignInID != "" {
		t.Fatalf("pending = state %q, provider %q, started %v, fresh %v", p.State, p.ProviderID,
			p.StartedAt, p.Fresh)
	}
}

// rdBeginWithFreshAsksForANewAuthentication is the case BeginWithFreshAsksForANewAuthentication.
func rdBeginWithFreshAsksForANewAuthentication(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, true)
	if !r.Pending.Fresh {
		t.Fatal("the pending request of a fresh Begin has Fresh false")
	}

	// A method with no time of the authentication has no parameter
	// to ask for a new one.
	if got := f.AsksFresh(t, r); got != f.AuthTime {
		t.Fatalf("the URL asks for a new authentication = %v, want %v", got, f.AuthTime)
	}

	if f.AsksFresh(t, begin(t, f, false)) {
		t.Fatal("the URL of a Begin that is not fresh asks for a new authentication")
	}
}

// rdFinishReturnsTheAssertionOfThePerson is the case FinishReturnsTheAssertionOfThePerson.
func rdFinishReturnsTheAssertionOfThePerson(t *testing.T, f RedirectFixture) {
	t.Helper()

	p := alice()
	_, a := signIn(t, f, p, false)

	if a.ProviderID != f.ProviderID || a.Subject != p.Subject || a.UserName != p.UserName ||
		a.Email != p.Email || a.PrincipalID != "" {
		t.Fatalf("Assertion = %+v, want the person %+v", a, p)
	}

	for _, name := range []string{p.Name, p.UserName, p.Email} {
		if !strings.Contains(strings.Join(a.Names, "\n"), name) {
			t.Fatalf("names = %v, want %q among them", a.Names, name)
		}
	}
}

// rdTheAssertionHasTheKindOfTheMethod is the case TheAssertionHasTheKindOfTheMethod.
func rdTheAssertionHasTheKindOfTheMethod(t *testing.T, f RedirectFixture) {
	t.Helper()

	if _, a := signIn(t, f, alice(), false); a.Method != f.Method.Kind() {
		t.Fatalf("method = %q, want %q", a.Method, f.Method.Kind())
	}
}

// rdFinishReturnsNoFactor is the case FinishReturnsNoFactor.
func rdFinishReturnsNoFactor(t *testing.T, f RedirectFixture) {
	t.Helper()

	if _, a := signIn(t, f, alice(), false); len(a.Factors) != 0 {
		t.Fatalf("factors = %v, want none", a.Factors)
	}
}

// rdAFreshAnswerHasAKnownAuthTimeNotBeforeTheStart is the case AFreshAnswerHasAKnownAuthTimeNotBeforeTheStart.
func rdAFreshAnswerHasAKnownAuthTimeNotBeforeTheStart(t *testing.T, f RedirectFixture) {
	t.Helper()

	r, a := signIn(t, f, alice(), true)
	if !a.AuthTimeKnown || a.AuthTime.Before(r.Pending.StartedAt.Add(-session.AssertionSkew)) {
		t.Fatalf("auth time = %v (known %v), want not before %v", a.AuthTime, a.AuthTimeKnown,
			r.Pending.StartedAt)
	}
}

// rdAFreshAnswerOlderThanItsRequestIsErrMethodAnswer is the case AFreshAnswerOlderThanItsRequestIsErrMethodAnswer.
func rdAFreshAnswerOlderThanItsRequestIsErrMethodAnswer(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, true)
	p := alice()
	p.AuthTime = r.Pending.StartedAt.Add(-time.Hour)

	a, err := f.Method.Finish(t.Context(), f.Answer(t, r, p))
	checkRefused(t, a, err, session.ErrMethodAnswer)
}

// rdAMethodWithNoAuthTimeReturnsAuthTimeKnownFalse is the case AMethodWithNoAuthTimeReturnsAuthTimeKnownFalse.
func rdAMethodWithNoAuthTimeReturnsAuthTimeKnownFalse(t *testing.T, f RedirectFixture) {
	t.Helper()

	for _, fresh := range []bool{false, true} {
		if _, a := signIn(t, f, alice(), fresh); a.AuthTimeKnown || !a.AuthTime.IsZero() {
			t.Fatalf("fresh %v: auth time = %v (known %v), want unknown and zero", fresh, a.AuthTime,
				a.AuthTimeKnown)
		}
	}
}

// rdADisabledProviderReturnsErrProviderDisabled is the case ADisabledProviderReturnsErrProviderDisabled.
func rdADisabledProviderReturnsErrProviderDisabled(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	f.Disable(t)

	a, err := f.Method.Finish(t.Context(), f.Answer(t, r, alice()))
	checkRefused(t, a, err, session.ErrProviderDisabled)

	_, err = f.Method.Begin(t.Context(), session.BeginInput{
		ProviderID: f.ProviderID, State: suiteState, Fresh: false,
	})
	if !errors.Is(err, session.ErrProviderDisabled) {
		t.Fatalf("Begin() = %v, want ErrProviderDisabled", err)
	}
}

// rdADisabledPersonReturnsErrSubjectDisabledWithTheSubject is the case
// ADisabledPersonReturnsErrSubjectDisabledWithTheSubject.
func rdADisabledPersonReturnsErrSubjectDisabledWithTheSubject(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	p := alice()
	p.Disabled = true

	a, err := f.Method.Finish(t.Context(), f.Answer(t, r, p))
	if !errors.Is(err, session.ErrSubjectDisabled) || a.Subject != p.Subject ||
		a.ProviderID != f.ProviderID || a.Method != f.Method.Kind() {
		t.Fatalf("Finish() = %+v, %v, want ErrSubjectDisabled with the subject", a, err)
	}
}

// rdAMethodWithNoGroupsReturnsGroupsPresentFalse is the case AMethodWithNoGroupsReturnsGroupsPresentFalse.
func rdAMethodWithNoGroupsReturnsGroupsPresentFalse(t *testing.T, f RedirectFixture) {
	t.Helper()

	p := alice()
	p.Groups, p.GroupsPresent = []string{"ops"}, true

	if _, a := signIn(t, f, p, false); a.GroupsPresent || len(a.Groups) != 0 {
		t.Fatalf("groups = %v (present %v), want none", a.Groups, a.GroupsPresent)
	}
}

// rdFinishReturnsGroupsWhenTheProviderSendsThem is the case FinishReturnsGroupsWhenTheProviderSendsThem.
func rdFinishReturnsGroupsWhenTheProviderSendsThem(t *testing.T, f RedirectFixture) {
	t.Helper()

	p := alice()
	p.Groups, p.GroupsPresent = []string{"ops", "dev"}, true

	_, a := signIn(t, f, p, false)
	checkGroups(t, a, p)
}

// rdAnEmptyGroupsValueIsPresentAndEmpty is the case AnEmptyGroupsValueIsPresentAndEmpty.
func rdAnEmptyGroupsValueIsPresentAndEmpty(t *testing.T, f RedirectFixture) {
	t.Helper()

	p := alice()
	p.Groups, p.GroupsPresent = []string{}, true

	if _, a := signIn(t, f, p, false); !a.GroupsPresent || len(a.Groups) != 0 {
		t.Fatalf("groups = %v (present %v), want present and empty", a.Groups, a.GroupsPresent)
	}
}

// rdMoreGroupsThanTheLimitAreErrMethodAnswer is the case MoreGroupsThanTheLimitAreErrMethodAnswer.
func rdMoreGroupsThanTheLimitAreErrMethodAnswer(t *testing.T, f RedirectFixture) {
	t.Helper()

	p := alice()
	p.Groups, p.GroupsPresent = manyGroups(session.MaxAssertionGroups+1), true

	r := begin(t, f, false)
	a, err := f.Method.Finish(t.Context(), f.Answer(t, r, p))
	checkRefused(t, a, err, session.ErrMethodAnswer)
}

// rdAGroupLongerThanTheLimitIsErrMethodAnswer is the case AGroupLongerThanTheLimitIsErrMethodAnswer.
func rdAGroupLongerThanTheLimitIsErrMethodAnswer(t *testing.T, f RedirectFixture) {
	t.Helper()

	p := alice()
	p.Groups, p.GroupsPresent = []string{strings.Repeat("g", session.MaxAssertionGroup+1)}, true

	r := begin(t, f, false)
	a, err := f.Method.Finish(t.Context(), f.Answer(t, r, p))
	checkRefused(t, a, err, session.ErrMethodAnswer)
}

// rdGroupsOnAndNoGroupsIsErrGroupsMissing is the case GroupsOnAndNoGroupsIsErrGroupsMissing.
func rdGroupsOnAndNoGroupsIsErrGroupsMissing(t *testing.T, f RedirectFixture) {
	t.Helper()

	p := alice()
	p.Groups, p.GroupsPresent = nil, false

	r := begin(t, f, false)
	a, err := f.Method.Finish(t.Context(), f.Answer(t, r, p))
	checkRefused(t, a, err, session.ErrGroupsMissing)
}

// rdACallbackOverTheSizeLimitIsErrMethodInput is the case ACallbackOverTheSizeLimitIsErrMethodInput.
func rdACallbackOverTheSizeLimitIsErrMethodInput(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	cb := f.Answer(t, r, alice())
	cb.Code = strings.Repeat("c", 64<<10)

	a, err := f.Method.Finish(t.Context(), cb)
	checkRefused(t, a, err, session.ErrMethodInput)
}

// rdACallbackWithControlCharactersIsErrMethodInput is the case ACallbackWithControlCharactersIsErrMethodInput.
func rdACallbackWithControlCharactersIsErrMethodInput(t *testing.T, f RedirectFixture) {
	t.Helper()

	for _, change := range []func(cb *session.Callback){
		func(cb *session.Callback) { cb.Code += "\r\nSet-Cookie: x=1" },
		func(cb *session.Callback) { cb.State += "\x00" },
		func(cb *session.Callback) { cb.ProviderError = "denied\n" },
	} {
		r := begin(t, f, false)
		cb := f.Answer(t, r, alice())
		change(&cb)

		a, err := f.Method.Finish(t.Context(), cb)
		checkRefused(t, a, err, session.ErrMethodInput)
	}
}

// rdABeginWithHostileInputIsErrMethodInput is the case ABeginWithHostileInputIsErrMethodInput.
func rdABeginWithHostileInputIsErrMethodInput(t *testing.T, f RedirectFixture) {
	t.Helper()

	for _, in := range []session.BeginInput{
		{ProviderID: f.ProviderID, State: "", Fresh: false},
		{ProviderID: f.ProviderID, State: "a\r\nb", Fresh: false},
		{ProviderID: f.ProviderID, State: strings.Repeat("s", 64<<10), Fresh: false},
		{ProviderID: "other", State: suiteState, Fresh: false},
	} {
		if _, err := f.Method.Begin(t.Context(), in); !errors.Is(err, session.ErrMethodInput) {
			t.Fatalf("Begin(%q, state of %d bytes) = %v, want ErrMethodInput", in.ProviderID,
				len(in.State), err)
		}
	}
}

// rdAnotherStateIsErrRequestBinding is the case AnotherStateIsErrRequestBinding.
func rdAnotherStateIsErrRequestBinding(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	cb := f.Answer(t, r, alice())
	cb.State = "YW5vdGhlci1zdGF0ZS1vZi1hbm90aGVyLWJyb3dzZXI"

	a, err := f.Method.Finish(t.Context(), cb)
	checkRefused(t, a, err, session.ErrRequestBinding)
}

// rdANilPendingRequestIsErrRequestBinding is the case ANilPendingRequestIsErrRequestBinding.
func rdANilPendingRequestIsErrRequestBinding(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	cb := f.Answer(t, r, alice())
	cb.Pending = nil

	a, err := f.Method.Finish(t.Context(), cb)
	checkRefused(t, a, err, session.ErrRequestBinding)
}

// rdAPendingRequestOfAnotherProviderIsErrRequestBinding is the case
// APendingRequestOfAnotherProviderIsErrRequestBinding.
func rdAPendingRequestOfAnotherProviderIsErrRequestBinding(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	cb := f.Answer(t, r, alice())
	other := *cb.Pending
	other.ProviderID = "other"
	cb.Pending = &other

	a, err := f.Method.Finish(t.Context(), cb)
	checkRefused(t, a, err, session.ErrRequestBinding)
}

// rdAnAuthTimeInTheFutureIsErrMethodAnswer is the case AnAuthTimeInTheFutureIsErrMethodAnswer.
func rdAnAuthTimeInTheFutureIsErrMethodAnswer(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	p := alice()
	p.AuthTime = f.Now().Add(time.Hour)

	a, err := f.Method.Finish(t.Context(), f.Answer(t, r, p))
	checkRefused(t, a, err, session.ErrMethodAnswer)
}

// rdAnErrorAnswerOfTheProviderIsErrMethodRejected is the case AnErrorAnswerOfTheProviderIsErrMethodRejected.
func rdAnErrorAnswerOfTheProviderIsErrMethodRejected(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	cb := f.Answer(t, r, alice())
	cb.Code, cb.ProviderError = "", "access_denied"

	a, err := f.Method.Finish(t.Context(), cb)
	checkRefused(t, a, err, session.ErrMethodRejected)
}

// rdAnUnknownAnswerIsErrMethodAnswer is the case AnUnknownAnswerIsErrMethodAnswer.
func rdAnUnknownAnswerIsErrMethodAnswer(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)

	a, err := f.Method.Finish(t.Context(), f.Broken(t, r, alice()))
	checkRefused(t, a, err, session.ErrMethodAnswer)
}

// rdAStoppedProviderIsErrMethodUnavailable is the case AStoppedProviderIsErrMethodUnavailable.
func rdAStoppedProviderIsErrMethodUnavailable(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	cb := f.Answer(t, r, alice())
	f.Stop(t)

	a, err := f.Method.Finish(t.Context(), cb)
	checkRefused(t, a, err, session.ErrMethodUnavailable)
}

// rdEachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion is the case
// EachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion.
func rdEachErrorExceptErrSubjectDisabledReturnsTheZeroAssertion(t *testing.T, f RedirectFixture) {
	t.Helper()

	r := begin(t, f, false)
	good := f.Answer(t, r, alice())

	for _, change := range []func(cb *session.Callback){
		func(cb *session.Callback) { cb.Pending = nil },
		func(cb *session.Callback) { cb.State = "b3RoZXI" },
		func(cb *session.Callback) { cb.Code, cb.Ticket = "", "" },
		func(cb *session.Callback) { cb.Code, cb.Ticket, cb.ProviderError = "", "", "server_error" },
		func(cb *session.Callback) { cb.Ticket = "ST-1" },
		func(cb *session.Callback) { cb.Code = "not-a-code-of-the-provider" },
	} {
		cb := good
		change(&cb)

		a, err := f.Method.Finish(t.Context(), cb)
		if err == nil || errors.Is(err, session.ErrSubjectDisabled) || !zeroAssertion(a) {
			t.Fatalf("Finish() = %+v, %v, want an error and the zero Assertion", a, err)
		}
	}
}
