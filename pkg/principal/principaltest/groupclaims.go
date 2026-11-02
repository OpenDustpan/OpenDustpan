package principaltest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// designOnly is the reason of each skipped case of a port that no
// implementation has yet.
const designOnly = "design only: no implementation of this port exists yet"

// GroupClaimsFixture is an implementation of principal.GroupClaimApplier
// over a fresh store, with the store so that a case can make principals and
// groups and read the memberships and the audit entries.
type GroupClaimsFixture struct {
	// Applier is the implementation under test.
	Applier principal.GroupClaimApplier
	// Store is the store of the principals and the groups.
	Store principal.Store
}

// RunGroupClaims runs the conformance suite of a
// principal.GroupClaimApplier. Each case names one rule and is skipped
// until an implementation exists. A case that sends hostile input tests one
// defense-in-depth layer alone, as if every other layer had failed.
func RunGroupClaims(t *testing.T, _ func(t *testing.T) GroupClaimsFixture) {
	t.Helper()

	for _, name := range []string{
		"ClaimsNotPresentChangeNothing",
		"AnEmptyPresentListEndsEachMembershipOfTheSource",
		"ANameAddsAnActiveMembershipToTheGroupOfThatName",
		"ANameMatchesOnlyAGroupOfTheHomeOfThePrincipal",
		"ANameThatMatchesNoGroupIsUnknownAndMakesNoGroup",
		"TheMembershipRecordsTheSourceAndTheSourceID",
		"AMembershipThatIsAlreadyActiveIsNotAddedAgain",
		"AGroupMissingFromTheClaimsEndsTheMembership",
		"ALocalMembershipNeverChanges",
		"AMembershipOfAnotherSourceIDNeverChanges",
		"APinnedMembershipNeverChanges",
		"AClaimOnlyJoinsALocalGroupOrADirectoryGroupOfItsSource",
		// The rules for groups from an identity provider.
		"AProtectedGroupRefusesTheAdd",
		"AProtectedGroupThatTrustsTheIdentityProviderAcceptsTheAdd",
		"AGroupClosedToTheIdentityProviderRefusesTheAdd",
		"FullSyncAcceptsTheAddToAProtectedGroup",
		"ARemovalFromAProtectedGroupSuspendsTheMembership",
		"ARemovalFromAProtectedGroupWithFullSyncIsFinal",
		"ASuspendedMembershipThatTheClaimsNameAgainStaysSuspended",
		"RemovalsCountForTheMassSuspensionAlert",
		// Audit entries.
		"EachAddRemovalSuspensionAndRefusalWritesAnAuditEntry",
		"TheAuditEntriesAreInTheTransactionOfTheChanges",
		"AnUnknownNameWritesOneAuditEntryWithTheBoundedList",
		// Layer 1: the boundary.
		"MoreThanTheLimitOfGroupsIsErrInvalid",
		"AGroupNameLongerThanTheLimitIsErrInvalid",
		"AGroupNameWithControlCharactersIsErrInvalid",
		"TheSourceLocalIsErrInvalid",
		"AnEmptySourceIDIsErrInvalid",
		// Layer 4: the principal of the claims.
		"AnUnknownPrincipalIsErrNotFound",
		"ADisabledPrincipalIsErrDisabled",
		"AServicePrincipalIsErrNotSignInTarget",
		"ABreakGlassAccountIsErrNotSignInTarget",
		"ATemporaryAdministratorIsErrNotSignInTarget",
		// Layer 5: fail closed.
		"AFailedWriteChangesNothingAndReturnsTheError",
		"AFailedAuditWriteChangesNothing",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Skip(designOnly)
		})
	}
}
