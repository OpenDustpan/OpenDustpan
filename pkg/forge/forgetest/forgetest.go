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

	"opendustpan.org/opendustpan/pkg/forge"
)

// Harness is one connection to a fresh repository for one case.
type Harness struct {
	// Forge is the adapter under test, opened with a credential that
	// the forge accepts.
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
	// Protect turns on branch protection for branch, so that a commit or a
	// merge through the port is refused.
	Protect(ctx context.Context, branch string) error
	// Merge merges the review request id, as a person does in the forge.
	Merge(ctx context.Context, id string) error
	// Comment writes body as a comment of the account author on the review
	// request id, and returns the comment.
	Comment(ctx context.Context, id string, author forge.Account, body string) (forge.Comment, error)
	// Event returns a request of kind as the forge sends it, signed with
	// the event secret of the connection.
	Event(ctx context.Context, kind forge.EventKind) (forge.EventRequest, error)
	// Refuse makes the forge refuse the credential of the connection from
	// now on, as after a revocation.
	Refuse(ctx context.Context) error
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
// "not implemented" until its body lands.
func cases() []testCase {
	return []testCase{
		// The connection.
		{"check answers the account of the credential", "", nil},
		{"a refused credential is ErrAuth on each method", "", nil},
		{"an operation outside the capabilities is unsupported", "", nil},
		{"no error and no event holds a part of the secret", "", nil},
		{"a forge that does not answer is ErrUnavailable", "", nil},

		// Files.
		{"head of a branch answers its commit", forge.CapFiles, nil},
		{"head of a missing ref is not found", forge.CapFiles, nil},
		{"read a file at a commit answers its content and blob ID", forge.CapFiles, nil},
		{"read a missing file is not found", forge.CapFiles, nil},
		{"read a file above the limit is too large", forge.CapFiles, nil},
		{"compare lists each changed file and the ancestry", forge.CapCompare, nil},
		{"compare above the limit truncates the patch only", forge.CapCompare, nil},
		{"compare of an unknown commit is not found", forge.CapCompare, nil},

		// Commits.
		{"commit of many files is one commit", forge.CapCommit, nil},
		{"commit on a new branch starts at the base", forge.CapCommit, nil},
		{"commit after a change of an edited file is a conflict", forge.CapCommit, nil},
		{"commit after a change of another file passes", forge.CapCommit, nil},
		{"commit to a new branch that exists with another head is a conflict", forge.CapCommit, nil},
		{"commit to a protected branch is refused", forge.CapCommit, nil},
		{"commit that fails validation makes no call", forge.CapCommit, nil},
		{"commit keeps the author and the trailer", forge.CapCommit, nil},
		{"find commit by its trailer", forge.CapCommit, nil},
		{"find commit with no such trailer is not found", forge.CapCommit, nil},

		// Review requests.
		{"open review request", forge.CapReview, nil},
		{"open review request twice answers the first", forge.CapReview, nil},
		{"read review request after a merge has the merge time and commit", forge.CapReview, nil},
		{"read review request after a close with no merge", forge.CapReview, nil},
		{"close review request twice is nil", forge.CapReview, nil},
		{"close of a merged review request is a conflict", forge.CapReview, nil},
		{"merge review request at its head", forge.CapMerge, nil},
		{"merge with a stale head is a conflict", forge.CapMerge, nil},
		{"merge that a rule of the forge stops is refused", forge.CapMerge, nil},

		// Feedback.
		{"set status twice replaces it", forge.CapStatus, nil},
		{"put comment twice edits one comment", forge.CapComment, nil},
		{"read comment answers the stable ID of the author", forge.CapComment, nil},
		{"read deleted comment is not found", forge.CapComment, nil},

		// Tokens.
		{"repo token reads its repository only", forge.CapRepoToken, nil},
		{"repo token cannot write", forge.CapRepoToken, nil},
		{"repo token expires within one hour", forge.CapRepoToken, nil},

		// Events.
		{"parse push event with a valid signature", forge.CapEvents, nil},
		{"parse review event with a valid signature", forge.CapEvents, nil},
		{"parse comment event with a valid signature", forge.CapEvents, nil},
		{"parse event with a wrong signature fails", forge.CapEvents, nil},
		{"parse event of a kind outside the port is unsupported", forge.CapEvents, nil},
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
