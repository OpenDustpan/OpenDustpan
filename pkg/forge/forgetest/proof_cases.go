package forgetest

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// proofKeyPrefix starts the trailer line that holds the proof of a commit.
const proofKeyPrefix = "Dustpan-Proof: "

// proofLineOf returns the line of message that holds the proof, or an empty
// text.
func proofLineOf(message string) string {
	for line := range strings.SplitSeq(message, "\n") {
		if strings.HasPrefix(line, proofKeyPrefix) {
			return line
		}
	}

	return ""
}

// caseFindCommitNeedsTheProof checks that a commit with the trailer counts only
// when its proof opens for the trailer, the base commit, the branch, the
// digest of the changes and the repository.
// Each pushed commit below has the origin of a commit of the connection, so
// that the checks of the author, the committer and the verification pass and
// only the proof can stop it.
func caseFindCommitNeedsTheProof(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	good, goodMessage := genuine(t, h, head, "j-60")
	twiceBase := mainHead(t, h)
	_, twiceMessage := genuine(t, h, twiceBase, "j-64")
	proof := proofLineOf(goodMessage)

	if proof == "" {
		t.Fatalf("the message of the commit has no proof line: %q", goodMessage)
	}

	found, err := h.Forge.FindCommit(
		ctx,
		h.Repo,
		"main",
		forge.DeliveryOf(genuineRequest(head.ID, "j-60")),
		head.Time,
	)
	if err != nil || found.ID != good.ID {
		t.Fatalf("FindCommit with a good proof = %+v, %v; want the commit %q", found, err, good.ID)
	}

	// Each row but the first ones differs from the good delivery in one part
	// only, so only the binding of that part can stop it.
	// A delivery of another branch never reaches the proof: FindCommit
	// refuses a ref that is not the branch of the delivery (see
	// caseFindCommitInvalidDelivery).
	good60 := forge.DeliveryOf(genuineRequest(head.ID, "j-60"))
	otherChanges := good60
	otherChanges.Changes = forge.ChangesDigest([]forge.FileChange{writeFile("g-j-60.txt", "y\n")})
	otherBase := good60
	otherBase.Base = anyBase

	tests := []struct {
		name     string
		message  string
		delivery forge.Delivery
	}{
		{
			"no proof", "Update the stacks\n\n" + jobTrailer("j-61"),
			forge.DeliveryOf(genuineRequest(head.ID, "j-61")),
		},
		{
			"the proof of another job",
			"Update the stacks\n\n" + jobTrailer("j-62") + "\n" + proof,
			forge.DeliveryOf(genuineRequest(head.ID, "j-62")),
		},
		{
			"a proof that is not a proof",
			"Update the stacks\n\n" + jobTrailer("j-63") + "\n" + proofKeyPrefix + "AAAA",
			forge.DeliveryOf(genuineRequest(head.ID, "j-63")),
		},
		{"the proof of another base commit", goodMessage, otherBase},
		{"the proof of other changes", goodMessage, otherChanges},
		{
			"two proofs", twiceMessage + "\n" + proofLineOf(twiceMessage),
			forge.DeliveryOf(genuineRequest(twiceBase.ID, "j-64")),
		},
	}

	for i, tt := range tests {
		pushAs(t, h, map[string][]byte{"p" + string(rune('a'+i)) + ".txt": []byte("x\n")}, tt.message, ownCommit())

		got, findErr := h.Forge.FindCommit(
			ctx,
			h.Repo,
			"main",
			tt.delivery,
			head.Time,
		)
		if problem := judgeFind(got, findErr, "", forge.ErrUntrustedCommit); problem != "" {
			t.Errorf("%s: %s", tt.name, problem)
		}
	}
}

// caseFindCommitProofIsOfThisRepository checks that the proof of a commit that
// the connection made in another repository, with the same trailer and the same
// base commit, does not count in this repository.
func caseFindCommitProofIsOfThisRepository(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := mainHead(t, h)

	clone, err := h.Fixture.Clone(ctx)
	if err != nil {
		t.Fatalf("Clone = %v", err)
	}

	req := commitOn("main", head.ID, "j-70", writeFile("a.txt", "1\n"))

	own, err := h.Forge.Commit(ctx, clone, req)
	if err != nil {
		t.Fatalf("Commit in the other repository = %v", err)
	}

	message, err := h.Fixture.CommitMessage(ctx, clone, own.ID)
	if err != nil {
		t.Fatalf("CommitMessage = %v", err)
	}

	// The commit counts in the repository that it was made in.
	found, err := h.Forge.FindCommit(
		ctx,
		clone,
		"main",
		forge.DeliveryOf(req),
		head.Time,
	)
	if err != nil || found.ID != own.ID {
		t.Fatalf("FindCommit in the other repository = %+v, %v; want the commit %q", found, err, own.ID)
	}

	// The same message, pushed here as a commit of the connection, does not.
	pushAs(t, h, map[string][]byte{"copy.txt": []byte("x\n")}, message, ownCommit())

	got, findErr := h.Forge.FindCommit(
		ctx,
		h.Repo,
		"main",
		forge.DeliveryOf(req),
		head.Time,
	)
	if problem := judgeFind(got, findErr, "", forge.ErrUntrustedCommit); problem != "" {
		t.Errorf("the proof of another repository: %s", problem)
	}
}
