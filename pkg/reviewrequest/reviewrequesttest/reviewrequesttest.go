// Package reviewrequesttest holds the conformance suites of the ports of
// package reviewrequest: Store, GitHost, ClosureResolver and
// ConflictChecker. A new adapter runs the suite of its port with a factory.
//
// Each case states one rule of its port. The ports are new, and no adapter
// exists yet, so each case skips with the reason pending. The first adapter
// of a port writes the body of each case of that port before it lands.
package reviewrequesttest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/reviewrequest"
)

// pending is the reason of each case that has no body yet.
const pending = "pending: the case gets its body with the first adapter of the port"

type suiteCase struct {
	name string
	test func(t *testing.T)
}

func run(t *testing.T, cases []suiteCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.test(t)
		})
	}
}

func skip(t *testing.T) {
	t.Helper()
	t.Skip(pending)
}

func names(cases []suiteCase) []string {
	out := make([]string, 0, len(cases))

	for _, tc := range cases {
		out = append(out, tc.name)
	}

	return out
}

func pendingCases(list ...string) []suiteCase {
	out := make([]suiteCase, 0, len(list))

	for _, name := range list {
		out = append(out, suiteCase{name: name, test: skip})
	}

	return out
}

// StoreFactory returns a fresh, empty store for one subtest.
type StoreFactory func(t *testing.T) reviewrequest.Store

// RunStore runs each case of the Store suite.
func RunStore(t *testing.T, _ StoreFactory) {
	t.Helper()
	run(t, storeCases())
}

// StoreCaseNames returns the name of each case of the Store suite, in order.
func StoreCaseNames() []string { return names(storeCases()) }

func storeCases() []suiteCase {
	return pendingCases(
		"PutThenGetReturnsTheRecordAsStored",
		"GetOfAnotherNamespaceIsNotFound",
		"PutWithAStaleVersionIsAConflictAndChangesNothing",
		"PutWithAStaleFenceIsFencedAndChangesNothing",
		"PutAppendsTheAuditEntryInTheSameTransaction",
		"PutWithAnInvalidAuditEntryChangesNothing",
		"ByChangeOrderFindsTheRecordThatCarriesIt",
		"ByChangeOrderOfAnotherNamespaceIsNotFound",
		"PutRefusesAnInvalidReference",
	)
}

// GitHostFactory returns a GitHost over a fake Git host for one subtest.
type GitHostFactory func(t *testing.T) reviewrequest.GitHost

// RunGitHost runs each case of the GitHost suite.
func RunGitHost(t *testing.T, _ GitHostFactory) {
	t.Helper()
	run(t, gitHostCases())
}

// GitHostCaseNames returns the name of each case of the GitHost suite, in
// order.
func GitHostCaseNames() []string { return names(gitHostCases()) }

func gitHostCases() []suiteCase {
	return pendingCases(
		"ReadReturnsTheHeadTheReviewsAndTheRequirements",
		"ReadNamesTheCommitOfEachReviewWhereTheGitHostHasOne",
		"ReadLeavesOutTheStatusesOfOpenDustpanFromTheRequirements",
		"ReadOfAMergeStateThatIsNotComputedIsUnknown",
		"ReadNeverAsksTheGitHostToComputeTheMergeStateAgain",
		"ReadOfAnUnknownReviewRequestIsNotFound",
		"ReadReturnsAuthorsByImmutableAccountID",
		"ReadDoesNotMakeACoAuthoredByTrailerAnAuthor",
		"ReadNamesThePersonBehindABotPushOrNoPerson",
		"PostStatusTwiceWithOneNameReplacesIt",
		"PutCommentEditsTheCommentThatItNames",
		"DeleteCommentDeletesTheComment",
		"ReactAddsTheReaction",
		"MergeOfAMovedHeadIsHeadMoved",
		"MergeThatTheGitHostRefusesCarriesItsAnswer",
		"MergeNeverUsesABypass",
		"ATimeoutIsUnavailable",
	)
}

// ClosureFactory returns a ClosureResolver over a fixture repository.
type ClosureFactory func(t *testing.T) reviewrequest.ClosureResolver

// RunClosure runs each case of the ClosureResolver suite.
func RunClosure(t *testing.T, _ ClosureFactory) {
	t.Helper()
	run(t, closureCases())
}

// ClosureCaseNames returns the name of each case of the ClosureResolver
// suite, in order.
func ClosureCaseNames() []string { return names(closureCases()) }

func closureCases() []suiteCase {
	return pendingCases(
		"ClosureHoldsTheFilesOfTheUnitDirectory",
		"ClosureHoldsALocalModuleOutsideTheUnitDirectory",
		"ClosureHoldsTheLockFile",
		"ClosureFollowsASymlinkInsideTheRepository",
		"ClosureOfAnImportThatTheResolverCannotReadIsNotKnown",
		"ClosureOfARemoteModuleIsNotKnown",
		"ClosureRefusesAnInvalidCommit",
	)
}

// ConflictFactory returns a ConflictChecker for one subtest.
type ConflictFactory func(t *testing.T) reviewrequest.ConflictChecker

// RunConflicts runs each case of the ConflictChecker suite.
func RunConflicts(t *testing.T, _ ConflictFactory) {
	t.Helper()
	run(t, conflictCases())
}

// ConflictCaseNames returns the name of each case of the ConflictChecker
// suite, in order.
func ConflictCaseNames() []string { return names(conflictCases()) }

func conflictCases() []suiteCase {
	return pendingCases(
		"TwoPlansOnSeparateResourcesAreDisjoint",
		"TwoPlansOnOneResourceConflict",
		"APlanThatReadsAnOutputOfTheOtherConflicts",
		"AProviderChangeIsNeverDisjoint",
		"AReplaceIsNeverDisjoint",
		"ARenameThroughAMovedBlockOrAnAliasIsNeverDisjoint",
		"ADataSourceIsNeverDisjoint",
		"ADeleteIsNeverDisjoint",
		"AChangeOfAParentResourceIsNeverDisjoint",
		"InlineRulesAndASeparateRuleResourceConflict",
		"ForEachOverADataSourceIsNeverDisjoint",
		"RemoteStateOfAnUnmanagedUnitIsNeverDisjoint",
		"APulumiDynamicProviderIsNeverDisjoint",
		"DriftIsNeverDisjoint",
		"ResourcesNameOnlyTheCollidingResources",
	)
}
