package principaltest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// designOnly is the reason of each skipped case of a port that no
// implementation has yet.
const designOnly = "design only: no implementation of this port exists yet"

// GroupMapperFixture is an implementation of principal.GroupMapper over a
// fresh store, with a way to set the mapping of a source.
type GroupMapperFixture struct {
	// Mapper is the implementation under test.
	Mapper principal.GroupMapper
	// Store is the store of the principals and the groups.
	Store principal.Store
	// SetMapping stores the mapping of value to the group IDs for the
	// source and the source ID, in the form that the implementation uses.
	SetMapping func(t *testing.T, source principal.MembershipSource, sourceID, value string, groupIDs ...string)
}

// RunGroupMapper runs the conformance suite of a principal.GroupMapper. Each
// case names one rule and is skipped until an implementation exists. The
// cases hold for each form of a mapping: an explicit map from a value to a
// group, or a match that a group turns on for one provider. A non-nil
// fixture fails the run until the cases exist.
func RunGroupMapper(t *testing.T, fixture func(t *testing.T) GroupMapperFixture) {
	t.Helper()

	// Defense in depth: a fixture would pass without testing anything, so a
	// non-nil fixture stops the run. This check stays so that a new caller
	// cannot skip it.
	if fixture != nil {
		t.Fatal("RunGroupMapper is design only: its cases need an implementation first")
	}

	skipAll(t, []string{
		"AMappedValueReturnsItsGroups",
		"AValueCanMapToMoreThanOneGroup",
		"AnUnmappedValueHasNoKey",
		"ASourceWithNoMappingIsErrNoGroupMapping",
		"TheMappingOfOneSourceIDNeverAppliesToAnother",
		"TheMappingOfOneSourceNeverAppliesToAnother",
		"AGroupOfAnotherHomeIsNeverReturned",
		"AGroupRenameDoesNotChangeTheMapping",
		"ANewGroupDoesNotJoinAMapping",
		"ADeletedGroupIsNeverReturned",
		"AValueThatDiffersOnlyInCaseIsAnotherValue",
		"AFailedReadIsAnErrorAndNotAnEmptyMapping",
	})
}

// GroupClaimsFixture is an implementation of principal.GroupClaimApplier
// over a fresh store and a mapper, with the store so that a case can make
// principals and groups and read the memberships and the audit entries.
type GroupClaimsFixture struct {
	// Applier is the implementation under test.
	Applier principal.GroupClaimApplier
	// Store is the store of the principals and the groups.
	Store principal.Store
	// Mapping sets the mapping of the mapper that Applier uses.
	Mapping GroupMapperFixture
}

// RunGroupClaims runs the conformance suite of a
// principal.GroupClaimApplier. Each case names one rule and is skipped
// until an implementation exists. A case that sends hostile input tests one
// defense-in-depth layer alone, as if every other layer had failed. A
// non-nil fixture fails the run until the cases exist.
func RunGroupClaims(t *testing.T, fixture func(t *testing.T) GroupClaimsFixture) {
	t.Helper()

	// Defense in depth: a fixture would pass without testing anything, so a
	// non-nil fixture stops the run. This check stays so that a new caller
	// cannot skip it.
	if fixture != nil {
		t.Fatal("RunGroupClaims is design only: its cases need an implementation first")
	}

	skipAll(t, []string{
		"ClaimsNotPresentChangeNothing",
		"NoMappingJoinsNoGroupAndChangesNothing",
		"NoMappingWritesOneAuditEntry",
		"AnEmptyPresentListEndsEachMembershipOfTheSource",
		"AMappedValueAddsAnActiveMembership",
		"AnUnmappedValueIsListedAndJoinsNoGroup",
		"TheCallNeverMakesAGroup",
		"TheMembershipRecordsTheSourceAndTheSourceID",
		"AMembershipThatIsAlreadyActiveIsNotAddedAgain",
		"AGroupMissingFromTheClaimsEndsTheMembership",
		"ALocalMembershipNeverChanges",
		"AMembershipOfAnotherSourceIDNeverChanges",
		"APinnedMembershipNeverChanges",
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
		"UnmappedValuesWriteOneAuditEntryWithTheBoundedList",
		// Layer 1: the boundary.
		"MoreThanTheLimitOfGroupsIsErrInvalid",
		"AGroupValueLongerThanTheLimitIsErrInvalid",
		"AGroupValueWithControlCharactersIsErrInvalid",
		"TheSourceLocalIsErrInvalid",
		"AnEmptySourceIDIsErrInvalid",
		// Layer 4: the principal of the claims.
		"AnUnknownPrincipalIsErrNotFound",
		"ADisabledPrincipalIsErrDisabled",
		"AServicePrincipalIsErrNotSignInTarget",
		"ABreakGlassAccountIsErrNotSignInTarget",
		"ATemporaryAdministratorIsErrNotSignInTarget",
		"AGroupIDOfAnotherHomeFromTheMapperIsRefused",
		// Layer 5: fail closed.
		"AFailedMapperReadChangesNothingAndReturnsTheError",
		"AFailedWriteChangesNothingAndReturnsTheError",
		"AFailedAuditWriteChangesNothing",
	})
}

// skipAll runs one skipped subtest for each name.
func skipAll(t *testing.T, names []string) {
	t.Helper()

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Skip(designOnly)
		})
	}
}
