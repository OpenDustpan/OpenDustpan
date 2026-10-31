package sessiontest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
)

// The suites in this file test the ports of sessions that the server does
// not store: session.Sealer, session.KeyStore, session.PrincipalStateStore,
// session.ProofVerifier and session.CookieSessions. No adapter implements
// them yet. Each case names one rule and is skipped until the adapter
// exists. A case that sends hostile input tests one defense-in-depth layer
// alone, as if every other layer had failed.

// designOnly is the reason of each skipped case.
const designOnly = "design only: no adapter implements this port yet"

// skipCases runs one skipped subtest for each name.
func skipCases(t *testing.T, names []string) {
	t.Helper()

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Skip(designOnly)
		})
	}
}

// RunSealer runs the conformance suite of a session.Sealer. The factory
// returns a sealer over a fresh key ring.
func RunSealer(t *testing.T, _ func(t *testing.T) session.Sealer) {
	t.Helper()

	skipCases(t, []string{
		"AccessRoundTrip",
		"RefreshRoundTrip",
		"ForgeRoundTrip",
		"SealUsesTheNewestKey",
		"AnOlderKeyThatIsNotRetiredOpens",
		"ARetiredKeyIsUnknown",
		"AnUnknownKeyIsUnknown",
		"TwoSealsOfTheSameClaimsDiffer",
		// Two-phase rotation and the reload of the ring.
		"AVersionBeforeItsSealsFromOpensButDoesNotSeal",
		"AVersionSealsFromItsSealsFrom",
		"ARetiredVersionStopsOpeningWithinOneReloadInterval",
		"AnUnknownKeyIDOfTheRightFormReloadsTheRingOnce",
		"OnDemandReloadsAreAtMostOneEach10Seconds",
		"ASealCountOverTheLimitAddsAVersion",
		"AVersionThatDoesNotUnwrapStopsTheStart",
		"TheKeyRingIsReachableOnlyThroughTheSealer",
		// The server nonce.
		"NonceRoundTrip",
		"ANonceOlderThanTheWindowIsStale",
		"ANonceAfterTheServerTimeIsStale",
		"ANonceDoesNotOpenAsACookie",
		"ACookieDoesNotOpenAsANonce",
		// Boundary: the form and the length, before any open.
		"AValueLongerThanTheLimitIsMalformedBeforeTheOpen",
		"AValueWithNoVersionIsMalformed",
		"AValueOfAnotherVersionIsMalformed",
		"AValueWithBadBase64IsMalformed",
		"AKeyIDOutsideThePatternIsMalformed",
		"AnEmptyValueIsMalformed",
		// Containment: the associated data binds the cookie and the key.
		"OneChangedByteDoesNotOpen",
		"AnAccessValueDoesNotOpenAsARefreshValue",
		"ARefreshValueDoesNotOpenAsAnAccessValue",
		"AnAccessValueDoesNotOpenAsAForgeValue",
		"AForgeValueOfOneConnectionDoesNotOpenForAnother",
		"AValueWithAnotherKeyIDInItsTextDoesNotOpen",
		"APlaintextWithAnUnknownFieldDoesNotOpen",
		"APlaintextWithADuplicateFieldDoesNotOpen",
		// Limit: the size and the expiry.
		"ASealOfAPlaintextOverTheLimitFails",
		"AnAccessValueAfterItsExpiryIsExpired",
		"AForgeValueAfterItsExpiryIsExpired",
		"AnAccessExpiryLaterThanTheLifetimeFailsTheSeal",
		// Containment: no secret in a formatted output.
		"TheForgeTokenIsRedactedInEachFormat",
		"TheCSRFValueIsRedactedInEachFormat",
		"AnErrorHoldsNoPartOfTheValue",
	})
}

// RunKeyStore runs the conformance suite of a session.KeyStore. The factory
// returns an empty store.
func RunKeyStore(t *testing.T, _ func(t *testing.T) session.KeyStore) {
	t.Helper()

	skipCases(t, []string{
		"AddThenListNewestFirst",
		"AddEndsTheSealOfTheEarlierNewestAtSealsFrom",
		"AddWithSealsFromBeforeTheCreationIsInvalid",
		"AddOfATakenIDIsAlreadyExists",
		"AddOfAnInvalidKeyIsInvalid",
		"AddOfAnIDOutsideThePatternIsInvalid",
		"AddWithABadAuditEntryChangesNothing",
		"AddAppendsTheEntryInTheSameTransaction",
		"ListOmitsRetiredKeys",
		"RetireOfAnUnknownKeyIsNotFound",
		"RetireOfARetiredKeyIsRetired",
		"RetireOfTheNewestKeyIsInvalid",
		"RetireWithOneBadIDChangesNothing",
		"RetireWithABadAuditEntryChangesNothing",
		"ConcurrentAddsKeepOneNewest",
		"TheStoreKeepsOnlyWrappedKeys",
	})
}

// RunPrincipalStateStore runs the conformance suite of a
// session.PrincipalStateStore. The factory returns a store with the
// principals of the suite.
func RunPrincipalStateStore(t *testing.T, _ func(t *testing.T) session.PrincipalStateStore) {
	t.Helper()

	skipCases(t, []string{
		"AnUnknownPrincipalIsNotFound",
		"ADeletedPrincipalIsNotFound",
		"AServicePrincipalIsNotFound",
		"ADisabledPrincipalComesBackWithItsDisableTime",
		"ANewPrincipalHasGenerationOne",
		"TheStateHoldsThePoliciesFromTheSameRead",
		"BumpAddsOne",
		"BumpReturnsTheNewGeneration",
		"BumpNeedsNoClock",
		"AZeroGenerationFromAnotherVersionIsRefused",
		"BumpOfAnUnknownCauseIsInvalid",
		"BumpOfAnUnknownPrincipalIsNotFound",
		"BumpWithABadAuditEntryChangesNothing",
		"BumpAppendsTheEntryInTheSameTransaction",
		"ConcurrentBumpsEachAddOne",
	})
}

// RunProofVerifier runs the conformance suite of a session.ProofVerifier.
// The factory returns a verifier.
func RunProofVerifier(t *testing.T, _ func(t *testing.T) session.ProofVerifier) {
	t.Helper()

	skipCases(t, []string{
		"AGoodProofGivesTheRFC7638Thumbprint",
		"TheThumbprintUsesCrvKtyXAndYOnly",
		"AGoodProofGivesItsNonce",
		// Boundary: the form, the type and the algorithm.
		"AProofLongerThanTheLimitIsMalformed",
		"AProofThatIsNotACompactJWSIsMalformed",
		"AProofOfAnotherTypeIsMalformed",
		"AProofWithAlgNoneIsMalformed",
		"AProofWithAnHMACAlgorithmIsMalformed",
		"AProofWithAnRSAAlgorithmIsMalformed",
		"AProofWithAPrivateKeyInItsJWKIsMalformed",
		"AProofWithAKeyTypeOtherThanECIsMalformed",
		"AProofWithACurveOtherThanP256IsMalformed",
		"AProofWithAPointNotOnTheCurveIsMalformed",
		"AProofWithXOrYNotOf32BytesIsMalformed",
		"AProofWithKidJkuX5uOrX5cIgnoresThemAndOpensNoURL",
		"AProofWithACritHeaderIsMalformed",
		"AProofWithB64FalseIsMalformed",
		"AProofInJSONSerializationIsMalformed",
		"AProofWithADuplicateHeaderKeyIsMalformed",
		"AProofWithADuplicateClaimIsMalformed",
		"AProofWithADERSignatureIsMalformed",
		"AProofWithNoNonceHasNoNonce",
		"AProofWithNoJTIIsMalformed",
		"AProofWithAJTIOfLessThan128BitsIsMalformed",
		"AProofWithNoIATIsMalformed",
		// The signature.
		"AProofWithAChangedClaimHasABadSignature",
		"AProofSignedByAnotherKeyHasABadSignature",
		// Limit: the time. The nonce gives the window, the iat a far bound.
		"AProofWithAnIATMoreThan24HoursOffIsStale",
		"AProofWithAnIATTwoMinutesOffPasses",
		// The request that the proof names.
		"AProofForAnotherMethodNamesAnotherRequest",
		"AProofForAnotherPathNamesAnotherRequest",
		"AProofForAnotherOriginNamesAnotherRequest",
		"AProofWithAQueryNamesAnotherRequest",
		"AProofWithATrailingSlashNamesAnotherRequest",
		"AProofWithAnUpperCaseSchemeOrHostMatches",
		"TheHostHeaderNeverChangesTheExpectedURL",
		// Containment: the proof never reaches a log.
		"AnErrorHoldsNoPartOfTheProof",
		// The accepted risk: no list of used proofs.
		"TheSameProofPassesTwiceInsideItsWindow",
	})
}

// RunCookieSessions runs the rules of session.CookieSessions: the check of
// each request and the refresh. The factory returns a service with a clock
// that the suite sets, and stores that the suite can read.
func RunCookieSessions(t *testing.T, _ func(t *testing.T) session.CookieSessions) {
	t.Helper()

	t.Run("Authenticate", func(t *testing.T) {
		t.Parallel()
		skipCases(t, []string{
			"AGoodCookieAuthenticates",
			"NoCookieIsUnauthenticated",
			"AMalformedCookieIsUnauthenticated",
			"ARefreshValueInTheAccessCookieIsUnauthenticated",
			"AnExpiredCookieIsUnauthenticated",
			// Origin and CSRF.
			"AnotherOriginIsRefused",
			"ACrossSiteFetchIsRefused",
			"AChangeWithNoFetchMetadataAndNoOriginIsRefused",
			"AChangeWithNoCSRFValueIsRefused",
			"AChangeWithAWrongCSRFValueIsRefused",
			"ACSRFValueOfTheWrongLengthIsRefused",
			"AReadNeedsNoCSRFValue",
			"AnOriginWithCROrLFIsRefusedAndLoggedWithoutThem",
			// Authorize at use: the principal read of each request.
			"AnUnknownPrincipalIsUnauthenticated",
			"ADisabledPrincipalIsUnauthenticated",
			"APrincipalThatIsNotAPersonIsUnauthenticated",
			"AnOlderGenerationIsUnauthenticated",
			"ANewerGenerationIsUnauthenticated",
			"ABumpEndsTheNextRequest",
			"AnIssueThatRacesABumpFailsAtTheNextRequest",
			"AReplicaClockAheadCannotKeepASessionAfterABump",
			"AnIdleDeadlineThatPassedIsUnauthenticated",
			"AnAbsoluteDeadlineThatPassedIsUnauthenticated",
			"AShorterMaximumLengthAppliesAtTheNextRequest",
			"AnInactiveAccountIsUnauthenticated",
			"ADevSubjectOnAServerThatIsNotDevIsRefusedAndRecorded",
			"TheNamespaceServicePrincipalIsRefused",
			"ARequestNeverExtendsADeadline",
			"AnEnrollOnlySessionCannotCallOtherRoutes",
			"AnEnrollOnlySessionCanEnrollReadRefreshAndSignOut",
			"NoRequestValueReachesALogWithCROrLF",
			"AStoreErrorFailsClosed",
		})
	})

	t.Run("Refresh", func(t *testing.T) {
		t.Parallel()
		skipCases(t, []string{
			"AGoodRefreshSetsBothCookies",
			"ARefreshMovesTheIdleDeadlineToNowPlusTheIdleLimit",
			"ARefreshNeverMovesTheAbsoluteDeadline",
			"ARefreshKeepsTheSignInIDTheKeyAndTheCSRFValue",
			"ARefreshKeepsTheRestriction",
			"ARefreshKeepsTheFreshTimeAndKind",
			"ARefreshSendsANewNonce",
			"AProofFromABrowserWithAClockTwoMinutesOffRefreshes",
			"AProofWithNoNonceGetsUseDPoPNonceAndNoEntry",
			"AProofWithAnOldNonceGetsUseDPoPNonceAndNoEntry",
			"UseDPoPNonceAnswersCountInTheSourceBudget",
			"TwoRefreshCookiesUseTheFirstThatOpensAndMatches",
			"MoreThanTwoRefreshCookiesAreRefused",
			"TheAccessExpiryIsTheEarliestOfFiveMinutesAndTheDeadlines",
			"TheEarlierRefreshCookieStaysValidUntilItsIdleDeadline",
			// Each step, in order, refuses, sets no cookie and records.
			"AnotherOriginIsRefusedFirst",
			"NoCookieIsRefused",
			"AnAccessValueInTheRefreshCookieIsRefused",
			"NoProofIsRefused",
			"AProofOfAnotherKeyIsRefused",
			"AnUnknownPrincipalIsRefused",
			"ADisabledPrincipalIsRefused",
			"AnOlderGenerationIsRefused",
			"AnIdleDeadlineThatPassedIsRefused",
			"AnAbsoluteDeadlineThatPassedIsRefused",
			"TheSignInTimePlusTheCurrentMaximumLengthIsRefused",
			"ADevSubjectOnAServerThatIsNotDevIsRefused",
			"TheStepsRunInTheirOrder",
			"ARefusalSetsNoCookie",
			// Fail closed and record.
			"EachRefusalWritesOneEntryWithItsStep",
			"RefusalEntriesOfOneSourceAndStepAreLimitedToOneAMinute",
			"ARefusalEntryHoldsNoCookieNoProofAndNoToken",
			"AStoreErrorFailsClosed",
			// Limit.
			"ARefreshUpdatesTheActivityTimeAtMostOnceAnHour",
			"ARefreshNeverMovesTheSignInTime",
			"ARefreshWithTheAccessCookieOnlyIsRefused",
			"TheSourceBudgetRefusesARefreshFlood",
			"TheProofNeverReachesALog",
		})
	})

	t.Run("KeyBinding", func(t *testing.T) {
		t.Parallel()
		skipCases(t, []string{
			"AnIdentityProviderSignInBindsTheKeyOfItsStart",
			"APasswordSignInBindsTheKeyOfItsRequest",
			"APasswordSignInWithMFABindsTheKeyOfThePendingSignIn",
			"APasskeySignInBindsTheKeyOfItsChallenge",
			"TheDemoSignInBindsTheKeyOfItsRequest",
			"ABreakGlassSignInBindsTheKeyOfItsRequest",
			"ASignInWithNoProofIssuesNoCookie",
			"ASignInWithAnOldNonceIssuesNoCookie",
			"TheCallbackNeverReadsAKeyFromItsRequest",
			"AFreshStepWithANewKeyKeepsTheOldThumbprint",
			"AChangeThatKeepsTheSessionKeepsTheThumbprint",
			"AnEnrollmentKeepsTheThumbprint",
			"AStolenAccessCookieAndThePasswordCannotBindANewKey",
			"TheSignInEntryRecordsTheThumbprint",
		})
	})

	t.Run("Restriction", func(t *testing.T) {
		t.Parallel()
		skipCases(t, []string{
			"APersonWithNoFactorAfterTheGraceGetsAnEnrollOnlySession",
			"AnEnrollmentBumpsAndIssuesCookiesWithNoRestriction",
			"TheFreshKindIsThePathOfTheStep",
		})
	})

	t.Run("SignOut", func(t *testing.T) {
		t.Parallel()
		skipCases(t, []string{
			"AnExpiredAccessCookieSignsOutAndEndsTheRuns",
			"ARefreshCookieAloneSignsOutAndEndsTheRuns",
			"AnOlderGenerationStillSignsOut",
			"ADisabledPrincipalCanStillSignOut",
			"NoCookieThatOpensEndsNothing",
			"AnotherOriginEndsNothing",
			"AWrongCSRFValueEndsNothing",
			"ASignOutEndsOnlyTheRunsOfItsSignIn",
			"EachCookieExpiresAtItsNameAndPath",
			"TheAnswerNeverClearsTheCookiesOfOtherHosts",
			"ASignOutDoesNotBumpTheGeneration",
		})
	})

	t.Run("Runs", func(t *testing.T) {
		t.Parallel()
		skipCases(t, []string{
			"ARunEndsAtItsIdleDeadline",
			"AReadOfTheRunMovesItsIdleDeadline",
			"TheIdleDeadlineOfARunMovesAtMostOnceAMinute",
			"ARequestOfAnotherSignInDoesNotMoveTheIdleDeadline",
			"ARunEndsAtTheAbsoluteDeadline",
			"ARunEndsAtABump",
			"ARunEndsAtASignOutOfItsSignIn",
			"ARunRecordWithAZeroDeadlineEndsTheRun",
			"ARunRecordWithASignInIDOfTheWrongFormEndsTheRun",
		})
	})

	t.Run("Administration", func(t *testing.T) {
		t.Parallel()
		skipCases(t, []string{
			"EndSessionsNeedsPrincipalManageAReasonAndAFreshAuthentication",
			"EndSessionsBumpsAndWritesTheEntry",
			"AReasonLongerThan512BytesIsRefused",
			"AReasonThatIsNotValidUTF8IsRefused",
			"ABodyWithAnUnknownFieldIsRefused",
			"RetireAllThatIsNotABooleanIsRefused",
			"RetireAllNeedsInstallationManageSignInAndAFreshAuthentication",
			"RetireAllEndsEverySession",
			"SignOutEverywhereBumpsOnlyTheOwnGeneration",
			"ARefusedAdministrationWritesADenialEntry",
		})
	})

	t.Run("ForgeToken", func(t *testing.T) {
		t.Parallel()
		skipCases(t, []string{
			"TheTokenWorksWithAnAccessCookieOfTheSameSignIn",
			"TheTokenIsRefusedWithAnAccessCookieOfAnotherSignIn",
			"TheTokenIsRefusedWithNoAccessCookie",
			"TheTokenIsRefusedAfterABump",
			"AChangeThatKeepsTheSessionEndsTheTokenCookies",
			"TheTokenIsRefusedForAnAccountThatIsNotTheLinkedAccount",
			"ARefusedTokenCookieIsClearedAndTheActionIsOffWithTheReason",
			"TheTokenIsRefusedAfterItsExpiry",
			"TheTokenCookieEndsAtTheAbsoluteDeadline",
			"TheRefreshTokenOfTheGitHostIsDiscarded",
			"MoreThanFourTokenCookiesAreRefused",
			"ASignOutClearsEachTokenCookie",
			"TheTokenNeverReachesALogAnAuditEntryOrAnError",
		})
	})
}

// RunNoSessionAtRest runs the checks that no session and no Git host token
// reaches the database or a backup. The factory returns a server with a
// database and a backup that the suite can read.
func RunNoSessionAtRest(t *testing.T, _ func(t *testing.T) session.CookieSessions) {
	t.Helper()

	skipCases(t, []string{
		"ADatabaseDumpHoldsNoCookieValue",
		"ADatabaseDumpHoldsNoGitHostToken",
		"ADatabaseDumpHoldsNoUnwrappedSessionKey",
		"ADatabaseDumpHoldsNoSessionRow",
		"ADatabaseDumpHoldsNoNonce",
		"ABackupHoldsNoSessionKey",
		"ABackupHoldsNoCookieValueAndNoGitHostToken",
		"ARestoreMakesANewKeyRingAndEndsEverySession",
		"ACookieOfAnotherInstallationDoesNotOpen",
	})
}
