package reviewrequesttest

import (
	"slices"
	"testing"
)

// ServiceFactory is the type of the harness of the service of the review
// requests. The service does not exist yet, so the harness has no fields.
type ServiceFactory func(t *testing.T) struct{}

// RunService runs each case of the suite of the service of the review
// requests.
func RunService(t *testing.T, _ ServiceFactory) {
	t.Helper()
	run(t, serviceCases())
}

// ServiceCaseNames returns the name of each case of the service suite, in
// order. Each name starts with the number of the decision that it tests.
// For a decision that is security code, one case covers each of the five
// steps of defense in depth: Boundary, Contain, Limit, Authorize and
// FailClosed.
func ServiceCaseNames() []string { return names(serviceCases()) }

func serviceCases() []suiteCase {
	return slices.Concat(modeCases(), approvalCases(), planCases(), gitHostServiceCases())
}

// modeCases cover the modes and their scope.
func modeCases() []suiteCase {
	return pendingCases(
		"D01ModeIsARequiredFieldWithNoDefault",
		"D01ModeFourAppliesOnMergeOnlyWithTheApplyGrantOnEachUnit",
		"D02ModeFourRefusesToTurnOnWhileARequirementSourceHolds",
		"D02ASaveOfARequirementSourceAboveModeFourFails",
		"D03TheLimitsOfModeOneShowInTheAPI",
		"D04AForcedModeCannotBeOverriddenBelow",
		"D04TheEffectiveModeShowsItsSource",
		"D05AReviewRequestUsesTheStrictestModeAndNamesTheUnit",
		"D05AReviewRequestThatTouchesModeThreeFollowsModeTwo",
		"D06ABranchWideSettingWaitsForEachAffectedUnit",
		"D07AWeakerModeNeedsAFreshAuthentication",
		"D36AChangeOfModeAppliesOnlyToNewChangeOrders",
	)
}

// approvalCases cover the approvals, the merge and the order of applies.
func approvalCases() []suiteCase {
	return pendingCases(
		"D09ASaveOfLevelTwoOnAPulumiScopeFailsAndNeverFallsBack",
		"D10ANewDeleteStopsTheApply",
		"D10ANewReplaceStopsTheApply",
		"D10AnOperationThatNoApprovalHoldsStopsTheApply",
		"D11LevelZeroResetsOnANewCommit",
		"D11LevelOneResetsOnANewSetOfOperations",
		"D11LevelOneKeepsTheApprovalWhenTheSetIsTheSame",
		"D11LevelTwoResetsOnANewPlan",
		"D11AnApproverWhoPushesLosesTheApproval",
		"D12AParentForbidsAWeakerLevel",
		"D12AWeakerLevelNeedsAFreshAuthentication",
		"D13APlanWithNoRefreshForOpenTofuIsAScopeSetting",
		"D37AnOperationIsTheAddressAndTheKindOfChange",
		"D14AnAuthorNeverApprovesOnEitherSide",
		"D14ACoAuthoredByTrailerDoesNotMakeAnAuthor",
		"D14AnUnlinkedPusherBlocksEachApprovalInModesOneAndTwo",
		"D15AMergeByAnUnlinkedAccountDoesNotApply",
		"D15AMergeByAPersonWithNoApplyGrantOnAUnitDoesNotApply",
		"D15TheQueuerIsThePersonWhoMergedForAMergeQueue",
		"D16TheApprovedSetIsTheUnionOfTheMergedChangeOrders",
		"D16TheUnionNeverHoldsAnUnapprovedChangeOrder",
		"D17AMergeAroundTheGateIsMergedUnapprovedAndAudited",
		"D17ABypassedIAMActionStarChangeStopsTheApply",
		"D18Boundary_AMovedHeadFailsTheGate",
		"D18Authorize_AStatusOfOurNameFromAnotherCreatorFailsTheGate",
		"D18FailClosed_AnUnreadableHeadFailsTheGate",
	)
}

// planCases cover plan conflicts, credentials and declared paths.
func planCases() []suiteCase {
	return pendingCases(
		"D19TheFirstApprovedChangeOrderKeepsGreenInOneTransaction",
		"D19AnUnprovenOverlapIsAConflict",
		"D19AnAcceptedOverlapIsAudited",
		"D41AnUnseenReaderInAnotherNamespaceIsAConflict",
		"D38APreMergeApplyHoldsTheUnitsUntilTheMerge",
		"D20Boundary_APushPlanChecksTheOpenerAndEachPusher",
		"D20Boundary_ABaseBranchReplanChecksTheOpenerAndEachPusher",
		"D20Boundary_PlanAgainChecksTheOpenerAndEachPusher",
		"D20Boundary_DustpanPlanChecksTheOpenerAndEachPusher",
		"D20Boundary_TheCatchUpReadChecksTheOpenerAndEachPusher",
		"D20Boundary_AMergeQueueCommitChecksTheOpenerAndEachPusher",
		"D20Contain_APlanWithNoCredentialsHasNoStateNoSecretOutputsAndNoNetwork",
		"D20Limit_APlanWithNoCredentialsCanNeverBeApproved",
		"D20Authorize_ATrustedCommentAloneGivesNoCredentials",
		"D20FailClosed_AnUnknownPusherPlansWithNoCredentials",
		"D21ATrustedCommentBindsToItsHead",
		"D21APushVoidsATrustedComment",
		"D40ATrustedPersonNeedsWriteAccessOnTheGitHost",
		"D22Boundary_ABotIsIdentifiedByItsAppIDNotItsLogin",
		"D22Authorize_LinkingGitHubActionsBotIsRefused",
		"D22FailClosed_AWorkflowThatPushesAsGitHubActionsBotPlansWithNoCredentials",
		"D22FailClosed_ABotPushWithNoKnownPersonPlansWithNoCredentials",
		"D23PlanAsMeBindsToTheHeadAndAPushVoidsIt",
		"D23PlanAsMeIsRefusedForAFork",
		"D23PlanAsMeMakesThePersonAnAuthorAndIsAudited",
		"D24DeclaredPathsOnlyAddFiles",
		"D24AnUnknownClosureStaysPendingUntilAnAcceptance",
		"D24AChangeOfTheDeclaredPathsVoidsTheAcceptance",
	)
}

// gitHostServiceCases cover the plan comment, leaks, webhooks, the user
// token, failures and the records.
func gitHostServiceCases() []suiteCase {
	return pendingCases(
		"D25ThePlanCommentHoldsNoValueByDefault",
		"D26ARedactionInvalidatesThePlan",
		"D26LaterCommentsHoldTheSummaryOnly",
		"D27ARedactionVoidsTheSignaturesAndShredsThePlanParts",
		"D27ARedactionCanHideAddresses",
		"D28Authorize_ARedactionNeedsReadOnTheUnitAndIsAudited",
		"D29ARedeliveryOfAFailedEffectRunsOnce",
		"D29ARedeliveryOfADoneEffectChangesNothing",
		"D29AReceivedCommandGetsAReaction",
		"D30TheWebhookOrTheCatchUpReadRecordsTheMergeOfAWriteBackReviewRequest",
		"D31Boundary_AWebhookWithABadSignatureHasNoEffect",
		"D31Boundary_AReplayHasNoEffect",
		"D31Authorize_AnEventThatSaysApprovedWhileTheAPISaysCommentedHasNoEffect",
		"D31Contain_ARenamedAccountIsNotTheLinkedPerson",
		"D32Contain_TheUserTokenNeverReachesALogAStoreOrARunner",
		"D32Authorize_ATokenOfAnotherAccountIsRefused",
		"D32Limit_TheTokenIsNeverRefreshed",
		"D32FailClosed_AnExpiredTokenTurnsTheControlsOff",
		"D33ANotMergeableAnswerFailsTheCommandWithTheAnswer",
		"D33APartialApplyRecordsNOfM",
		"D33ARetryIsOffWhenALaterCommitChangedTheUnit",
		"D42ADetachNeedsAFreshAuthenticationAndAReasonAndStartsABreakGlassReview",
		"D43OneReviewRequestHasOneChangeOrder",
		"D35AnAgentNeverApproves",
		"D44EachAutomaticActionWritesItsAuditEntry",
		"D45TheGateRecordStoresTheEvidenceWithNoCommentText",
	)
}
