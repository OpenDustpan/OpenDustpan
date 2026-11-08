// Package forgetest is the conformance suite for forge.Forge
// implementations. Every forge adapter runs Run against a recorded or a
// fake API of its forge:
//
//	func TestForgeConformance(t *testing.T) {
//		t.Parallel()
//		forgetest.Run(t, func(t *testing.T) forgetest.Harness { return newHarness(t) })
//	}
//
// The cases come before the first adapter. A case with no body skips with
// "not implemented", and gets its body before an adapter implements the
// methods that it covers. The cases of the groups "The connection", "Files",
// "Commits", "Review requests", "Statuses", "Comments" and "Tree" have bodies.
// CaseNamesWithBody lists the cases that have a body. A case whose capability the forge does not list
// skips for that forge, and the case "an operation outside the
// capabilities is unsupported" checks that such a method returns
// forge.ErrUnsupported.
package forgetest

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Harness is one connection to a fresh repository for one case.
type Harness struct {
	// Opener is the adapter under test. A case opens the forge again with
	// another connection or credential, for example to test a bad secret or
	// two active credential sets.
	Opener forge.Opener
	// Connection is the connection that Forge was opened with.
	Connection forge.Connection
	// Credential is the credential set that Forge was opened with. Its
	// Secret and the EventSecret are values that no other case uses, so that
	// the case "no error and no event holds a part of the secret" can find
	// them in a text.
	Credential forge.Credential
	// EventSecret is the active event secret of the app registration or of
	// the hook of the connection. A case gives it to ParseEvent.
	EventSecret []byte
	// Forge is the adapter under test, opened with Connection and Credential,
	// and accepted by the forge.
	Forge forge.Forge
	// Account is the account that the forge gives for Credential: the ID
	// and the login that Check must answer.
	Account forge.Account
	// Repo is the full path of a repository that only this case uses. Its
	// default branch is "main" with one commit.
	Repo string
	// Fixture changes the repository outside the port, as a person or
	// another tool does.
	Fixture Fixture
}

// Fixture changes the repository of a case outside the port, and makes the
// events that the forge sends.
type Fixture interface { //nolint:interfacebloat // One fixture changes the repository, makes events and breaks the forge for the whole suite.
	// Push commits files on branch with no check, as another author does,
	// and returns the commit. A nil value deletes the file.
	Push(ctx context.Context, branch string, files map[string][]byte) (forge.Commit, error)
	// PushAs commits files on branch with no check, with the message and the
	// origin that the case gives, and returns the commit. It lets a case make
	// a commit that poses as a commit of the connection. A nil value
	// deletes the file.
	PushAs(
		ctx context.Context,
		branch string,
		files map[string][]byte,
		message string,
		origin Origin,
	) (forge.Commit, error)
	// CommitMessage returns the full message of the commit commitID in the
	// repository repo, as the forge shows it. A case copies the message of a
	// commit of the connection, with its proof, into a commit that it pushes.
	CommitMessage(ctx context.Context, repo, commitID string) (string, error)
	// Clone makes another repository on the forge, in the same account, with
	// the commits and the branches of the repository of the case and another
	// repository ID, and returns its path. The connection can commit to it.
	Clone(ctx context.Context) (string, error)
	// PushEntry commits an entry that is not a regular file at path on
	// branch with no check, as another author does, and returns the commit.
	// The entry replaces the file or the entry that the path has. The
	// variant makes two entries of one kind differ: the target of a symlink,
	// the commit of a submodule, the name of the file in a directory.
	PushEntry(ctx context.Context, branch, path string, kind EntryKind, variant string) (forge.Commit, error)
	// Rewrite replaces the history of branch with one new commit that has no
	// parent and holds exactly files, as a force push does, and returns it.
	// The commits before stay in the repository.
	Rewrite(ctx context.Context, branch string, files map[string][]byte) (forge.Commit, error)
	// Protect turns on branch protection for branch, so that a commit
	// through the port is refused.
	Protect(ctx context.Context, branch string) error
	// Tag makes the tag name at the commit with the ID commitID.
	Tag(ctx context.Context, name, commitID string) error
	// Merge merges the review request id, as a person does in the forge.
	Merge(ctx context.Context, id string) error
	// Close closes the review request id with no merge, as a person does in
	// the forge.
	Close(ctx context.Context, id string) error
	// Branch makes the branch name at the head of main with no new commit.
	Branch(ctx context.Context, name string) error
	// SetDefaultBranch makes branch the default branch that the forge
	// answers for the repository. The forge does not refuse a delete of it.
	SetDefaultBranch(ctx context.Context, branch string) error
	// ForkReview makes a fork of the repository, in the account of another
	// person, with the branch of the same name as branch and its commits. The
	// person opens a review request from the branch of the fork into target
	// of the repository, and the method returns its ID.
	ForkReview(ctx context.Context, branch, target string) (string, error)
	// SetMergeState makes the forge answer the merge state state, as a text of
	// the forge, for the review request id. The text "unknown" is the state
	// before the forge computes the merge.
	SetMergeState(ctx context.Context, id, state string) error
	// Comments returns the comments of the review request id as the forge
	// shows them, oldest first.
	Comments(ctx context.Context, id string) ([]forge.Comment, error)
	// AddComment adds a comment with the text body to the review request id,
	// as another person does in the forge, and returns it.
	AddComment(ctx context.Context, id, body string) (forge.Comment, error)
	// Reactions returns the reactions to the comment commentID as
	// "reaction:login", in order.
	Reactions(ctx context.Context, commentID string) ([]string, error)
	// Statuses returns the current status of each context of the commit
	// commitID, as the forge shows it, in the order of the contexts. The newest
	// status of a context replaces the older ones.
	Statuses(ctx context.Context, commitID string) ([]forge.Status, error)
	// Oversize makes the forge answer each review request with a body above
	// the size limit of the adapter, until the returned function restores it.
	Oversize(ctx context.Context) (restore func(), err error)
	// Event returns a request of kind as the forge sends it, signed with
	// secret. The kind may be one that the port does not name, to test
	// ErrUnsupported.
	Event(ctx context.Context, kind forge.EventKind, secret []byte) (forge.EventRequest, error)
	// Redeliver returns the request again, as the forge sends a retry: the
	// same body, the same delivery ID and a new signature time.
	Redeliver(ctx context.Context, r forge.EventRequest) (forge.EventRequest, error)
	// Refuse makes the forge refuse the credential of the connection from
	// now on, as after a revocation.
	Refuse(ctx context.Context) error
	// Calls returns the number of calls that the forge got from the
	// connection since the case began, so that a case can check that Open
	// makes none and that RotateSelf makes one.
	Calls(ctx context.Context) (int, error)
	// SetTokenExpiry sets the expiry that the forge gives for the token of a
	// connection with the auth kind token. The zero time removes the expiry,
	// so that the forge says that the token has none. A fixture of an auth
	// kind with no token expiry returns an error.
	SetTokenExpiry(ctx context.Context, at time.Time) error
	// Garble makes the forge answer each call with a success status and a
	// body that does not parse, until the returned function restores it.
	Garble(ctx context.Context) (restore func(), err error)
	// Unavailable makes the forge fail with a server error, or with a rate
	// limit and the wait time retryAfter when it is above zero, until the
	// returned function restores it.
	Unavailable(ctx context.Context, retryAfter time.Duration) (restore func(), err error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

type testCase struct {
	name string
	// need is the capability that the case tests. Empty runs the case on
	// every forge.
	need forge.Capability
	run  func(t *testing.T, h Harness)
}

// cases lists every case of the suite. A case with a nil run skips with
// "not implemented" until its body lands. The methods that the design
// proposes for feedback and comment commands (merge) have no case until they
// are in the port.
func cases() []testCase {
	return []testCase{
		// The connection.
		{"check answers the account of the credential", "", caseCheckAccount},
		{"check answers the expiry of a token that has one", "", caseCheckExpiry},
		{"check of a token with no expiry says so", "", caseCheckNoExpiry},
		{"open refuses another kind, a missing parameter and a bad secret, and makes no call", "", caseOpenRefuses},
		{"a refused credential is ErrAuth on each method", "", caseRefusedIsAuth},
		{"a missing repository is not found on each method", "", caseMissingRepo},
		{"an operation outside the capabilities is unsupported", "", caseUnsupported},
		{"no error and no event holds a part of the secret", "", caseNoSecretInErrors},
		{"a forge that does not answer is ErrUnavailable", "", caseUnavailable},
		{"a rate limit is ErrUnavailable with the wait time", "", caseRateLimit},

		// Files.
		{"head of a branch answers its commit", forge.CapFiles, caseHeadBranch},
		{"head of a tag answers its commit", forge.CapFiles, caseHeadTag},
		{"head of a missing ref is not found", forge.CapFiles, caseHeadMissing},
		{"read a file at a commit answers its content and blob ID", forge.CapFiles, caseReadFile},
		{"read a missing file is not found", forge.CapFiles, caseReadMissingFile},
		{"read a file at a missing commit is not found", forge.CapFiles, caseReadMissingCommit},
		{"read a file above the limit is too large", forge.CapFiles, caseReadTooLarge},
		{
			"read a file with a path that leaves the repository is invalid and makes no call",
			forge.CapFiles,
			caseReadBadPath,
		},
		{"compare lists each changed file and the ancestry", forge.CapCompare, caseCompareFiles},
		{"compare of a rename has the old path", forge.CapCompare, caseCompareRename},
		{"compare of a base that is not an ancestor says so", forge.CapCompare, caseCompareNotAncestor},
		{"compare above the limit truncates the patch only", forge.CapCompare, caseCompareTruncates},
		{"compare of an unknown commit is not found", forge.CapCompare, caseCompareUnknown},
		{"compare with a bad commit ID or limit is invalid and makes no call", forge.CapCompare, caseCompareInvalid},

		// Commits.
		{"commit lands with its files and its trailer", forge.CapCommit, caseCommitLands},
		{"commit of a delete removes the file", forge.CapCommit, caseCommitDelete},
		{"commit on a new branch starts at the base", forge.CapCommit, caseCommitNewBranch},
		{"commit after a change of an edited file is a conflict", forge.CapCommit, caseCommitStale},
		{"commit after a change of another file passes", forge.CapCommit, caseCommitOtherFile},
		{
			"commit to a new branch that exists with another head is a conflict",
			forge.CapCommit,
			caseCommitNewBranchTaken,
		},
		{"two commits to the same new branch: one passes, one is a conflict", forge.CapCommit, caseCommitNewBranchRace},
		{"commit to a protected branch is refused", forge.CapCommit, caseCommitProtected},
		{"commit to a missing branch is not found", forge.CapCommit, caseCommitMissingBranch},
		{"commit of a delete of a missing file is invalid", forge.CapCommit, caseCommitInvalidDelete},
		{"commit after a force push that keeps the files is a conflict", forge.CapCommit, caseCommitForcePushed},
		{"commit that the forge refuses with status 422 is invalid", forge.CapCommit, caseCommitRefused422},
		{"a connection with no push access lists no commit and no review", "", caseNoPushNoWrite},
		{"commit that fails validation is invalid and makes no call", forge.CapCommit, caseCommitInvalid},
		{"commit above a size limit is too large and makes no call", forge.CapCommit, caseCommitTooLarge},
		{"find commit by its trailer", forge.CapCommit, caseFindCommit},
		{"find commit before the since time is not found", forge.CapCommit, caseFindCommitSince},
		{"find commit with no such trailer is not found", forge.CapCommit, caseFindCommitNoTrailer},
		{"find commit on a missing ref is not found", forge.CapCommit, caseFindCommitMissingRef},
		{"find commit with a bad trailer or ref is invalid and makes no call", forge.CapCommit, caseFindCommitInvalid},
		{
			"find commit with a bad branch or changes digest is invalid and makes no call",
			forge.CapCommit,
			caseFindCommitInvalidDelivery,
		},
		{
			"find commit never returns a pushed commit that has the trailer",
			forge.CapCommit,
			caseFindCommitIgnoresPushedTrailer,
		},
		{
			"find commit stops at a pushed commit with the trailer that is newer than the own",
			forge.CapCommit,
			caseFindCommitPushedTrailerBeforeOwn,
		},
		{
			"find commit needs the trailer line to equal the line of the commit",
			forge.CapCommit,
			caseFindCommitTrailerIsExact,
		},
		{
			"find commit needs a proof for the trailer, the base commit and the job",
			forge.CapCommit,
			caseFindCommitNeedsTheProof,
		},
		{
			"find commit never counts the proof of a commit in another repository",
			forge.CapCommit,
			caseFindCommitProofIsOfThisRepository,
		},
		{
			"commit after a symlink, a submodule or a directory at a changed path is a conflict",
			forge.CapCommit,
			caseCommitMovedHeadEntry,
		},

		// Review requests.
		{"open review request", forge.CapReview, caseReviewOpen},
		{"open review request twice answers the first", forge.CapReview, caseReviewOpenTwice},
		{
			"open review request of the same branch into another target opens another",
			forge.CapReview,
			caseReviewOpenOtherTarget,
		},
		{
			"open review request of a missing branch or target is not found",
			forge.CapReview,
			caseReviewOpenMissingBranch,
		},
		{"open review request that the forge does not allow is refused", forge.CapReview, caseReviewOpenRefused},
		{
			"open review request that fails validation is invalid and makes no call",
			forge.CapReview,
			caseReviewOpenInvalid,
		},
		{"open review request never answers the review request of a fork", forge.CapReview, caseReviewOpenIgnoresFork},
		{"read an open review request has its head, base and test merge", forge.CapReview, caseReviewReadHead},
		{"read review request after a merge has the merge time and commit", forge.CapReview, caseReviewReadMerged},
		{"read review request after a close with no merge", forge.CapReview, caseReviewReadClosed},
		{"read a missing review request is not found", forge.CapReview, caseReviewReadMissing},
		{
			"read or close with an ID that is not a number is invalid and makes no call",
			forge.CapReview,
			caseReviewIDInvalid,
		},
		{"read review request from a fork shows another head repository", forge.CapReview, caseReviewReadFork},
		{
			"read review request maps each merge state, and an unknown one is unknown",
			forge.CapReview,
			caseReviewMergeStates,
		},
		{
			"read review request with an unknown merge state has no test merge",
			forge.CapReview,
			caseReviewUnknownHasNoTestMerge,
		},
		{"close review request twice is nil", forge.CapReview, caseReviewCloseTwice},
		{"close a missing review request is not found", forge.CapReview, caseReviewCloseMissing},
		{"close of a merged review request is a conflict", forge.CapReview, caseReviewCloseMerged},
		{"close of a review request outside the write-back is refused", forge.CapReview, caseReviewCloseForeign},
		{
			"close of a closed review request outside the write-back is refused",
			forge.CapReview,
			caseReviewCloseForeignClosed,
		},
		{"delete branch of a write-back", forge.CapReview, caseDeleteBranch},
		{
			"delete the base of an open review request is refused",
			forge.CapReview,
			caseDeleteBranchBaseOfOpenReview,
		},
		{"delete a missing branch is not found", forge.CapReview, caseDeleteBranchMissing},
		{
			"delete a branch outside the write-back is invalid and makes no call",
			forge.CapReview,
			caseDeleteBranchOutsideWriteBack,
		},
		{"delete the default branch is refused", forge.CapReview, caseDeleteDefaultBranch},
		{"delete the head of an open review request is refused", forge.CapReview, caseDeleteBranchWithOpenReview},
		{"delete a protected branch is refused", forge.CapReview, caseDeleteProtectedBranch},
		{"review methods on a forge that fails are ErrUnavailable", forge.CapReview, caseReviewServerError},
		{"review methods on an answer that does not parse are ErrUnavailable", forge.CapReview, caseReviewGarbled},
		{"review methods on an answer above the size limit are ErrUnavailable", forge.CapReview, caseReviewOversize},

		// Statuses.
		{"set status lands on the commit", forge.CapStatus, caseStatusLands},
		{"set status again for a context replaces it and keeps the others", forge.CapStatus, caseStatusReplaces},
		{"set status on a missing commit is not found", forge.CapStatus, caseStatusMissingCommit},
		{"set status that fails validation is invalid and makes no call", forge.CapStatus, caseStatusInvalid},
		{"set status at the limits of each field passes", forge.CapStatus, caseStatusBoundaries},

		// Comments.
		{"upsert comment makes a comment and a second call edits it", forge.CapComment, caseCommentUpsertEdits},
		{
			"upsert comment never edits a comment of a person or one without the marker",
			forge.CapComment,
			caseCommentUpsertOwnOnly,
		},
		{"upsert comment above the limit cuts the text with a notice", forge.CapComment, caseCommentCut},
		{"comment calls that fail validation are invalid and make no call", forge.CapComment, caseCommentInvalid},
		{"comment calls on a missing review request or comment are not found", forge.CapComment, caseCommentMissing},
		{"react is idempotent", forge.CapComment, caseCommentReact},

		// Tree.
		{"tree lists the direct children of a directory", forge.CapTree, caseTreeLists},
		{"tree tells a file, a directory, a symlink and a submodule", forge.CapTree, caseTreeKinds},
		{"tree reads the commit that is asked for", forge.CapTree, caseTreeAtACommit},
		{"tree above the limit is too large and lists nothing", forge.CapTree, caseTreeLimit},
		{
			"tree with a path outside the directory or a bad limit is invalid and makes no call",
			forge.CapTree,
			caseTreeInvalid,
		},
		{"tree of a missing directory, a file or a missing commit is not found", forge.CapTree, caseTreeNotFound},

		// Tokens.
		{"repo token of an app reads its repository only", forge.CapRepoToken, nil},
		{"repo token of an app cannot write", forge.CapRepoToken, nil},
		{"repo token of an app expires within one hour", forge.CapRepoToken, nil},
		// Skips when Connection.Auth is not AuthToken.
		{"repo token of a token connection is not scoped", forge.CapGitAccess, nil},
		{"a forge with repo-token also lists git-access", forge.CapRepoToken, caseRepoTokenImpliesGitAccess},

		// Rotation.
		{"rotate self answers a new token that check accepts", forge.CapSelfRotate, nil},
		{"rotate self revokes the old token", forge.CapSelfRotate, nil},
		{"rotate self keeps the expiry that the call asks for", forge.CapSelfRotate, nil},
		{"rotate self with a revoked token is ErrAuth", forge.CapSelfRotate, nil},
		{"rotate self makes one call and never retries after an error", forge.CapSelfRotate, nil},
		{"rotate self returns no part of the old or the new secret in an error", forge.CapSelfRotate, nil},

		// Events.
		{"parse ping event", forge.CapEvents, nil},
		{"parse review event with a valid signature", forge.CapEvents, nil},
		{"parse event with no secret or an empty secret fails", forge.CapEvents, nil},
		{"parse event with a wrong signature fails", forge.CapEvents, nil},
		{"parse event with a changed body is a signature error", forge.CapEvents, nil},
		{"parse signed event with a body that does not parse is invalid", forge.CapEvents, nil},
		{"parse event of a kind outside the port is unsupported", forge.CapEvents, nil},
		// Skips when Connection.Auth is not AuthApp.
		{"parse event of an app has the installation ID", forge.CapEvents, nil},
		{"each of two active event secrets verifies an event", forge.CapEvents, nil},
		{"a retried delivery keeps its delivery ID", forge.CapEvents, nil},
	}
}

// CaseNamesWithBody returns the name of each case that has a body, in the
// order of Run. A case with no body skips with "not implemented".
func CaseNamesWithBody() []string {
	var names []string

	for _, c := range cases() {
		if c.run != nil {
			names = append(names, c.name)
		}
	}

	return names
}

// CaseNames returns the name of each case, in the order of Run.
func CaseNames() []string {
	all := cases()
	names := make([]string, 0, len(all))

	for _, c := range all {
		names = append(names, c.name)
	}

	return names
}

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if c.run == nil {
				t.Skip("not implemented")
			}

			h := newHarness(t)
			if c.need != "" && !h.Forge.Capabilities().Has(c.need) {
				t.Skipf("the forge does not have the capability %q", c.need)
			}

			c.run(t, h)
		})
	}
}
