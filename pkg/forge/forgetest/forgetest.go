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
// methods that it covers. A case whose capability the forge does not list
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
	// Secret and its EventSecret are values that no other case uses, so that
	// the case "no error and no event holds a part of the secret" can find
	// them in a text.
	Credential forge.Credential
	// Forge is the adapter under test, opened with Connection and Credential,
	// and accepted by the forge.
	Forge forge.Forge
	// Repo is the full path of a repository that only this case uses. Its
	// default branch is "main" with one commit.
	Repo string
	// Fixture changes the repository outside the port, as a person or
	// another tool does.
	Fixture Fixture
}

// Fixture changes the repository of a case outside the port, and makes the
// events that the forge sends.
type Fixture interface {
	// Push commits files on branch with no check, as another author does,
	// and returns the commit. A nil value deletes the file.
	Push(ctx context.Context, branch string, files map[string][]byte) (forge.Commit, error)
	// Protect turns on branch protection for branch, so that a commit
	// through the port is refused.
	Protect(ctx context.Context, branch string) error
	// Merge merges the review request id, as a person does in the forge.
	Merge(ctx context.Context, id string) error
	// Event returns a request of kind as the forge sends it, signed with
	// secret.
	Event(ctx context.Context, kind forge.EventKind, secret []byte) (forge.EventRequest, error)
	// Refuse makes the forge refuse the credential of the connection from
	// now on, as after a revocation.
	Refuse(ctx context.Context) error
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
		{"check answers the account of the credential", "", nil},
		{"open refuses another kind, a missing parameter and a bad secret, and makes no call", "", nil},
		{"a refused credential is ErrAuth on each method", "", nil},
		{"an operation outside the capabilities is unsupported", "", nil},
		{"no error and no event holds a part of the secret", "", nil},
		{"a forge that does not answer is ErrUnavailable", "", nil},
		{"a rate limit is ErrUnavailable with the wait time", "", nil},

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
		{"close of a merged review request is a conflict", forge.CapReview, nil},

		// Tokens.
		{"repo token of an app reads its repository only", forge.CapRepoToken, nil},
		{"repo token of an app cannot write", forge.CapRepoToken, nil},
		{"repo token of an app expires within one hour", forge.CapRepoToken, nil},
		{"repo token of a token connection is not scoped", forge.CapGitAccess, nil},

		// Events.
		{"parse ping event", forge.CapEvents, nil},
		{"parse push event with a valid signature", forge.CapEvents, nil},
		{"parse review event with a valid signature", forge.CapEvents, nil},
		{"parse comment event with a valid signature", forge.CapEvents, nil},
		{"parse event with a wrong signature fails", forge.CapEvents, nil},
		{"parse event with a changed body is a signature error", forge.CapEvents, nil},
		{"parse signed event with a body that does not parse is invalid", forge.CapEvents, nil},
		{"parse event of a kind outside the port is unsupported", forge.CapEvents, nil},
		{"parse event of an app has the installation ID", forge.CapEvents, nil},
		{"each of two active event secrets verifies an event", forge.CapEvents, nil},
		{"a retried delivery keeps its delivery ID", forge.CapEvents, nil},
	}
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
