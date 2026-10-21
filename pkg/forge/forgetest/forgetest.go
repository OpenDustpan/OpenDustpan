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
// methods that it covers. The cases of the group "The connection" have
// bodies, except "a missing repository is not found on each method", which
// waits for the methods that read a repository. CaseNamesWithBody lists the
// cases that have a body. A case whose capability the forge does not list
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
	// Protect turns on branch protection for branch, so that a commit
	// through the port is refused.
	Protect(ctx context.Context, branch string) error
	// Merge merges the review request id, as a person does in the forge.
	Merge(ctx context.Context, id string) error
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
// proposes for feedback and comment commands (merge, status, comment) have
// no case until they are in the port.
func cases() []testCase {
	return []testCase{
		// The connection.
		{"check answers the account of the credential", "", caseCheckAccount},
		{"check answers the expiry of a token that has one", "", caseCheckExpiry},
		{"check of a token with no expiry says so", "", caseCheckNoExpiry},
		{"open refuses another kind, a missing parameter and a bad secret, and makes no call", "", caseOpenRefuses},
		{"a refused credential is ErrAuth on each method", "", caseRefusedIsAuth},
		{"a missing repository is not found on each method", "", nil},
		{"an operation outside the capabilities is unsupported", "", caseUnsupported},
		{"no error and no event holds a part of the secret", "", caseNoSecretInErrors},
		{"a forge that does not answer is ErrUnavailable", "", caseUnavailable},
		{"a rate limit is ErrUnavailable with the wait time", "", caseRateLimit},

		// Files.
		{"head of a branch answers its commit", forge.CapFiles, nil},
		{"head of a tag answers its commit", forge.CapFiles, nil},
		{"head of a missing ref is not found", forge.CapFiles, nil},
		{"read a file at a commit answers its content and blob ID", forge.CapFiles, nil},
		{"read a missing file is not found", forge.CapFiles, nil},
		{"read a file at a missing commit is not found", forge.CapFiles, nil},
		{"read a file above the limit is too large", forge.CapFiles, nil},
		{"compare lists each changed file and the ancestry", forge.CapCompare, nil},
		{"compare of a rename has the old path", forge.CapCompare, nil},
		{"compare of a base that is not an ancestor says so", forge.CapCompare, nil},
		{"compare above the limit truncates the patch only", forge.CapCompare, nil},
		{"compare of an unknown commit is not found", forge.CapCompare, nil},

		// Commits.
		{"commit of many files is one commit", forge.CapCommit, nil},
		{"commit of a delete removes the file", forge.CapCommit, nil},
		{"commit on a new branch starts at the base", forge.CapCommit, nil},
		{"commit after a change of an edited file is a conflict", forge.CapCommit, nil},
		{"commit after a change of another file passes", forge.CapCommit, nil},
		{"commit to a new branch that exists with another head is a conflict", forge.CapCommit, nil},
		{"two commits to the same new branch: one passes, one is a conflict", forge.CapCommit, nil},
		{"commit to a protected branch is refused", forge.CapCommit, nil},
		{"commit to a missing branch is not found", forge.CapCommit, nil},
		{"a connection with no push access lists no commit and no review", "", caseNoPushNoWrite},
		{"commit that fails validation makes no call", forge.CapCommit, nil},
		{"commit above the size limit is too large and makes no call", forge.CapCommit, nil},
		{"commit keeps the author and the trailer", forge.CapCommit, nil},
		{"find commit by its trailer", forge.CapCommit, nil},
		{"find commit before the since time is not found", forge.CapCommit, nil},
		{"find commit with no such trailer is not found", forge.CapCommit, nil},
		{"find commit on a missing ref is not found", forge.CapCommit, nil},

		// Review requests.
		{"open review request", forge.CapReview, nil},
		{"open review request twice answers the first", forge.CapReview, nil},
		{"open review request of the same branch into another target opens another", forge.CapReview, nil},
		{"open review request of a missing branch is not found", forge.CapReview, nil},
		{"open review request that the forge does not allow is refused", forge.CapReview, nil},
		{"read an open review request has its head", forge.CapReview, nil},
		{"read review request after a merge has the merge time and commit", forge.CapReview, nil},
		{"read review request after a close with no merge", forge.CapReview, nil},
		{"read a missing review request is not found", forge.CapReview, nil},
		{"close review request twice is nil", forge.CapReview, nil},
		{"close a missing review request is not found", forge.CapReview, nil},
		{"close of a merged review request is a conflict", forge.CapReview, nil},

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
