package sessiontest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// The suites in this file test the ports of the sign-in methods:
// session.RedirectMethod, session.PasswordMethod and
// session.RequestMethod. No adapter implements them yet. Each case names
// one rule and is skipped until the adapter exists. A case that sends
// hostile input tests one defense-in-depth layer alone, as if every other
// layer had failed.

// TestPerson is a person at the test identity provider or the test
// directory of a fixture.
type TestPerson struct {
	// AuthTime is the time of the authentication that the provider
	// reports.
	AuthTime time.Time
	// Subject is the stable subject at the provider.
	Subject string
	// UserName is the user name at the provider.
	UserName string
	// Email is the email at the provider.
	Email string
	// Groups are the groups of the person at the provider.
	Groups []string
	// GroupsPresent is false for a provider that sends no groups value.
	GroupsPresent bool
	// Disabled is true for a person that the provider reports as disabled.
	Disabled bool
}

// RedirectFixture is a redirect method with a test identity provider.
type RedirectFixture struct {
	// Method is the method under test.
	Method session.RedirectMethod
	// Answer returns the callback that the test identity provider gives
	// for the redirect r, for the person p.
	Answer func(t *testing.T, r session.Redirect, p TestPerson) session.Callback
	// Stop makes the test identity provider stop answering.
	Stop func(t *testing.T)
}

// RunRedirectMethod runs the conformance suite of a session.RedirectMethod.
// The factory returns a fixture over a fresh test identity provider.
func RunRedirectMethod(t *testing.T, _ func(t *testing.T) RedirectFixture) {
	t.Helper()

	skipCases(t, []string{
		"BeginReturnsAnHTTPSURL",
		"BeginKeepsTheStateInThePendingRequest",
		"BeginWithFreshAsksForANewAuthentication",
		"FinishReturnsTheAssertionOfThePerson",
		"FinishReturnsTheSubjectOfTheConfiguredAttribute",
		"TheAssertionHasTheKindOfTheMethod",
		"FinishReturnsNoFactor",
		"FinishReturnsGroupsWhenTheProviderSendsThem",
		"FinishReturnsGroupsPresentFalseWhenTheProviderSendsNone",
		"AnEmptyGroupsValueIsPresentAndEmpty",
		"AFreshAnswerHasAKnownAuthTimeNotBeforeTheStart",
		"AMethodWithNoAuthTimeReturnsAuthTimeKnownFalse",
		"ADisabledPersonReturnsErrSubjectDisabledWithTheSubject",
		"ADisabledProviderReturnsErrProviderDisabled",
		// Layer 1: the boundary.
		"ACallbackOverTheSizeLimitIsErrMethodInput",
		"ACallbackWithControlCharactersIsErrMethodInput",
		"ATicketOutsideTheAllowlistIsErrMethodInput",
		"MoreGroupsThanTheLimitAreErrMethodAnswer",
		"AGroupLongerThanTheLimitIsErrMethodAnswer",
		"AGroupsClaimOfTheWrongTypeIsErrMethodAnswer",
		"AnXMLAnswerWithADTDIsErrMethodAnswer",
		"AnXMLAnswerDeeperThanTheLimitIsErrMethodAnswer",
		// Layer 2: encode or contain.
		"TheServiceURLComesFromTheServerNotFromTheCallback",
		"AnUnsignedAssertionIsErrMethodAnswer",
		"AResponseWithTwoAssertionsIsErrMethodAnswer",
		"ASignatureAlgorithmOutsideTheAllowlistIsErrMethodAnswer",
		"ASignatureOverAnotherElementIsErrMethodAnswer",
		"AnotherAudienceIsErrMethodAnswer",
		"AnotherIssuerIsErrMethodAnswer",
		// Layer 3: limit the effect.
		"AnAnswerOutsideTheClockSkewIsErrMethodAnswer",
		"AnAuthTimeInTheFutureIsErrMethodAnswer",
		"ASecondUseOfAnAssertionIDIsErrReplay",
		"ASecondUseOfATicketIsErrReplay",
		"AProviderThatDoesNotAnswerInTimeIsErrMethodUnavailable",
		// Layer 4: the binding to the browser.
		"AnotherStateIsErrRequestBinding",
		"AnotherRequestIDIsErrRequestBinding",
		"ACallbackWithNoPendingRequestIsErrRequestBinding",
		"APendingRequestOfAnotherProviderIsErrRequestBinding",
		// Layer 5: fail closed.
		"AnErrorAnswerOfTheProviderIsErrMethodRejected",
		"AnUnknownAnswerIsErrMethodAnswer",
		"GroupsOnAndNoGroupsIsErrGroupsMissing",
		"AStoppedProviderIsErrMethodUnavailable",
		"EachErrorReturnsTheZeroAssertion",
	})
}

// PasswordFixture is a password method with a test directory.
type PasswordFixture struct {
	// Method is the method under test.
	Method session.PasswordMethod
	// AddUser adds the person p with the password to the test directory.
	AddUser func(t *testing.T, p TestPerson, password string)
	// Stop makes the test directory stop answering.
	Stop func(t *testing.T)
}

// RunPasswordMethod runs the conformance suite of a session.PasswordMethod.
// The factory returns a fixture over a fresh test directory.
func RunPasswordMethod(t *testing.T, _ func(t *testing.T) PasswordFixture) {
	t.Helper()

	skipCases(t, []string{
		"AGoodPasswordReturnsTheAssertionOfThePerson",
		"TheSubjectIsTheStableAttributeNotTheUserName",
		"TheAssertionHoldsTheFactorPassword",
		"TheAuthTimeIsKnownAndIsTheTimeOfTheBind",
		"TheAssertionHasTheKindOfTheMethod",
		"GroupsComeFromTheConfiguredSource",
		"NestedGroupsAreFlat",
		"ADisabledUserIsErrSubjectDisabledWithTheSubject",
		"ADisabledProviderReturnsErrProviderDisabled",
		// Layer 1: the boundary.
		"AnEmptyUserNameIsErrMethodInput",
		"AUserNameLongerThanTheLimitIsErrMethodInput",
		"AUserNameWithControlCharactersIsErrMethodInput",
		"AnEmptyPasswordIsErrMethodInput",
		// Layer 2: encode or contain.
		"FilterCharactersInTheUserNameAreEscaped",
		"DNCharactersInTheUserNameAreEscaped",
		"AUserNameWithAWildcardFindsNoOtherUser",
		"ThePasswordIsNeverInAnErrorOrALog",
		// Layer 3: limit the effect.
		"ASearchOverTheSizeLimitIsErrMethodAnswer",
		"AGroupSearchOverTheLimitIsErrMethodAnswer",
		"ADirectoryThatDoesNotAnswerInTimeIsErrMethodUnavailable",
		"APlainConnectionIsRefusedUnlessTheSettingAllowsIt",
		// Layer 4: the person of the check.
		"TheBindIsAsTheUserThatTheSearchFound",
		"TwoUsersWithOneUserNameAreErrMethodAnswer",
		// Layer 5: fail closed.
		"AnUnknownUserNameIsErrMethodRejected",
		"AWrongPasswordIsErrMethodRejected",
		"AnUnknownUserAndAWrongPasswordGiveTheSameError",
		"GroupsOnAndNoGroupsIsErrGroupsMissing",
		"AStoppedDirectoryIsErrMethodUnavailable",
		"EachErrorReturnsTheZeroAssertion",
	})
}

// RequestFixture is a request method with a test proxy.
type RequestFixture struct {
	// Method is the method under test.
	Method session.RequestMethod
	// Facts returns the facts of a request that the trusted test proxy
	// sends for the person p, with each proof of the proxy.
	Facts func(t *testing.T, p TestPerson) session.RequestFacts
	// Stop makes the key endpoint of the test issuer stop answering.
	Stop func(t *testing.T)
}

// RunRequestMethod runs the conformance suite of a session.RequestMethod.
// The factory returns a fixture over a fresh test proxy.
func RunRequestMethod(t *testing.T, _ func(t *testing.T) RequestFixture) {
	t.Helper()

	skipCases(t, []string{
		"ARequestOfTheTrustedProxyReturnsTheAssertion",
		"HeadersNamesEachHeaderThatTheMethodReads",
		"TheAssertionHasAuthTimeKnownFalse",
		"TheAssertionHoldsNoFactor",
		"TheAssertionHasTheKindOfTheMethod",
		"GroupsComeFromTheGroupsHeaderOrClaim",
		"AnEmptyGroupsHeaderIsPresentAndEmpty",
		"ARequestWithNoHeaderIsErrNoAssertion",
		"ADisabledProviderReturnsErrProviderDisabled",
		// Layer 1: the boundary.
		"APeerOutsideTheTrustedProxiesIsErrUntrustedSource",
		"ASubjectOutsideTheAllowlistIsErrMethodInput",
		"AHeaderWithControlCharactersIsErrMethodInput",
		"TwoValuesOfTheUserHeaderAreErrMethodInput",
		"MoreGroupsThanTheLimitAreErrMethodAnswer",
		// Layer 2: encode or contain.
		"AHeaderThatTheMethodDoesNotNameIsNotRead",
		"AJWTWithAnotherIssuerIsErrMethodAnswer",
		"AJWTWithAnotherAudienceIsErrMethodAnswer",
		"AJWTSignedWithAnotherKeyIsErrMethodAnswer",
		"AJWTWithTheAlgorithmNoneIsErrMethodAnswer",
		// Layer 3: limit the effect.
		"AJWTOutsideTheClockSkewIsErrMethodAnswer",
		"AnExpiredJWTIsErrMethodAnswer",
		"AKeyEndpointThatDoesNotAnswerInTimeIsErrMethodUnavailable",
		// Layer 4: the second proof of the proxy.
		"APlainHeaderFromATrustedPeerWithNoSecondProofIsErrUntrustedSource",
		"AWrongSharedSecretIsErrUntrustedSource",
		"AClientCertificateOfAnotherBundleIsErrUntrustedSource",
		// Layer 5: fail closed.
		"GroupsOnAndNoGroupsHeaderIsErrGroupsMissing",
		"AStoppedKeyEndpointIsErrMethodUnavailable",
		"EachErrorReturnsTheZeroAssertion",
	})
}
