package forgetest

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// reviewFiles is the change that a branch of these cases holds.
func reviewFiles() map[string][]byte {
	return map[string][]byte{"stacks/prod.yaml": []byte("a: 1\n")}
}

// reviewOn returns the request for a review request from branch into target.
func reviewOn(branch, target string) forge.ReviewRequest {
	return forge.ReviewRequest{
		Branch: branch, Target: target, Title: "Update the stacks", Body: "Change of the job j-1.\n",
	}
}

// pushBranch makes branch with one commit that main lacks, and returns the
// commit.
func pushBranch(t *testing.T, h Harness, branch string) forge.Commit {
	t.Helper()

	c, err := h.Fixture.Push(t.Context(), branch, reviewFiles())
	if err != nil {
		t.Fatalf("push %s: %v", branch, err)
	}

	return c
}

// openReview pushes branch and opens a review request from it into main.
func openReview(t *testing.T, h Harness, branch string) forge.Review {
	t.Helper()

	pushBranch(t, h, branch)

	got, err := h.Forge.OpenReview(t.Context(), h.Repo, reviewOn(branch, "main"))
	if err != nil {
		t.Fatalf("OpenReview = %v", err)
	}

	return got
}

// readReview reads a review request and fails the test on an error.
func readReview(t *testing.T, h Harness, id string) forge.Review {
	t.Helper()

	got, err := h.Forge.ReadReview(t.Context(), h.Repo, id)
	if err != nil {
		t.Fatalf("ReadReview(%s) = %v", id, err)
	}

	return got
}

// isNumber reports whether s is a positive decimal number with no sign and no
// leading zero.
func isNumber(s string) bool {
	n, err := strconv.ParseUint(s, 10, 63)

	return err == nil && n > 0 && strconv.FormatUint(n, 10) == s
}

// branchExists reports whether the forge has the branch.
func branchExists(t *testing.T, h Harness, branch string) bool {
	t.Helper()

	_, err := h.Forge.Head(t.Context(), h.Repo, branch)
	if err != nil && !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Head(%s) = %v", branch, err)
	}

	return err == nil
}

func caseReviewOpen(t *testing.T, h Harness) {
	t.Helper()

	main := mainHead(t, h)
	pushed := pushBranch(t, h, "dustpan/j-1")

	got, err := h.Forge.OpenReview(t.Context(), h.Repo, reviewOn("dustpan/j-1", "main"))
	if err != nil {
		t.Fatalf("OpenReview = %v", err)
	}

	if !isNumber(got.ID) || got.State != forge.ReviewOpen || got.Branch != "dustpan/j-1" || got.Target != "main" {
		t.Fatalf("OpenReview = %+v, want an open review request of dustpan/j-1 into main with a number", got)
	}

	if got.Head != pushed.ID || got.BaseCommit != main.ID {
		t.Fatalf("OpenReview head, base = %q, %q; want %q, %q", got.Head, got.BaseCommit, pushed.ID, main.ID)
	}

	checkOpenedFields(t, got)

	if again := readReview(t, h, got.ID); again.ID != got.ID || again.Head != got.Head {
		t.Fatalf("ReadReview = %+v, want the review request that OpenReview made: %+v", again, got)
	}
}

// checkOpenedFields checks the fields of a review request that is new: an https
// address, the same repository for the base and the head, an author, and a
// merge state.
func checkOpenedFields(t *testing.T, got forge.Review) {
	t.Helper()

	if !strings.HasPrefix(got.URL, "https://") {
		t.Fatalf("OpenReview URL = %q, want an https address", got.URL)
	}

	if !isNumber(got.BaseRepoID) || got.HeadRepoID != got.BaseRepoID || got.FromFork() {
		t.Fatalf("OpenReview repositories = %q, %q; want the same repository ID", got.BaseRepoID, got.HeadRepoID)
	}

	if !isNumber(got.AuthorID) || got.Mergeable == "" {
		t.Fatalf(
			"OpenReview author, merge state = %q, %q; want an author ID and a merge state",
			got.AuthorID,
			got.Mergeable,
		)
	}
}

func caseReviewOpenTwice(t *testing.T, h Harness) {
	t.Helper()

	first := openReview(t, h, "dustpan/j-2")

	again := reviewOn("dustpan/j-2", "main")
	again.Title = "Another title"

	second, err := h.Forge.OpenReview(t.Context(), h.Repo, again)
	if err != nil {
		t.Fatalf("OpenReview again = %v", err)
	}

	if second.ID != first.ID || second.State != forge.ReviewOpen {
		t.Fatalf("OpenReview again = %+v, want the open review request %s", second, first.ID)
	}
}

func caseReviewOpenOtherTarget(t *testing.T, h Harness) {
	t.Helper()

	first := openReview(t, h, "dustpan/j-3")

	if _, err := h.Fixture.Push(t.Context(), "release", map[string][]byte{"RELEASE": []byte("1\n")}); err != nil {
		t.Fatalf("push release: %v", err)
	}

	second, err := h.Forge.OpenReview(t.Context(), h.Repo, reviewOn("dustpan/j-3", "release"))
	if err != nil {
		t.Fatalf("OpenReview into release = %v", err)
	}

	if second.ID == first.ID || second.Target != "release" || first.Target != "main" {
		t.Fatalf("OpenReview into release = %+v, want another review request than %s", second, first.ID)
	}
}

func caseReviewOpenMissingBranch(t *testing.T, h Harness) {
	t.Helper()

	_, err := h.Forge.OpenReview(t.Context(), h.Repo, reviewOn("dustpan/no-such-branch", "main"))
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("OpenReview of a missing branch = %v, want ErrNotFound", err)
	}

	pushBranch(t, h, "dustpan/j-4")

	_, err = h.Forge.OpenReview(t.Context(), h.Repo, reviewOn("dustpan/j-4", "no-such-target"))
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("OpenReview into a missing target = %v, want ErrNotFound", err)
	}
}

func caseReviewOpenRefused(t *testing.T, h Harness) {
	t.Helper()

	// A branch at the head of main has no commit that main lacks.
	if err := h.Fixture.Branch(t.Context(), "dustpan/j-5"); err != nil {
		t.Fatalf("branch: %v", err)
	}

	_, err := h.Forge.OpenReview(t.Context(), h.Repo, reviewOn("dustpan/j-5", "main"))
	if !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("OpenReview of a branch with no new commit = %v, want ErrRefused", err)
	}
}

func caseReviewOpenInvalid(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	before := callCount(ctx, t, h)

	bad := map[string]forge.ReviewRequest{
		"an empty branch":            {Branch: "", Target: "main", Title: "t", Body: ""},
		"a branch with a dot-dot":    {Branch: "dustpan/../main", Target: "main", Title: "t", Body: ""},
		"a full ref as branch":       {Branch: "refs/heads/dustpan/j-1", Target: "main", Title: "t", Body: ""},
		"a target with a space":      {Branch: "dustpan/j-1", Target: "ma in", Title: "t", Body: ""},
		"the same branch and target": {Branch: "main", Target: "main", Title: "t", Body: ""},
		"an empty title":             {Branch: "dustpan/j-1", Target: "main", Title: " ", Body: ""},
		"a title with a line break":  {Branch: "dustpan/j-1", Target: "main", Title: "a\nb", Body: ""},
		"a body with a NUL":          {Branch: "dustpan/j-1", Target: "main", Title: "t", Body: "a\x00b"},
		"a body that is not UTF-8":   {Branch: "dustpan/j-1", Target: "main", Title: "t", Body: "a\xffb"},
		"a long title": {
			Branch: "dustpan/j-1", Target: "main", Title: strings.Repeat("t", forge.MaxReviewTitleBytes+1), Body: "",
		},
		"a long body": {
			Branch: "dustpan/j-1", Target: "main", Title: "t", Body: strings.Repeat("b", forge.MaxReviewBodyBytes+1),
		},
	}

	for name, req := range bad {
		_, err := h.Forge.OpenReview(ctx, h.Repo, req)
		if !errors.Is(err, forge.ErrInvalid) && !errors.Is(err, forge.ErrTooLarge) {
			t.Errorf("OpenReview with %s = %v, want ErrInvalid or ErrTooLarge", name, err)
		}
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid review request made %d calls to the forge, want none", after-before)
	}
}

func caseReviewReadHead(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	opened := openReview(t, h, "dustpan/j-6")

	moved, err := h.Fixture.Push(ctx, "dustpan/j-6", map[string][]byte{"stacks/dev.yaml": []byte("b: 2\n")})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	got := readReview(t, h, opened.ID)
	if got.Head != moved.ID || got.Head == opened.Head || got.State != forge.ReviewOpen {
		t.Fatalf(
			"ReadReview head = %q, state %q; want the new head %q of an open review request",
			got.Head,
			got.State,
			moved.ID,
		)
	}

	if got.Mergeable != forge.MergeClean {
		t.Fatalf("ReadReview mergeable = %q, want %q", got.Mergeable, forge.MergeClean)
	}

	if forge.ValidateCommitID(got.TestMergeCommit) != nil || got.TestMergeCommit == got.Head ||
		got.TestMergeCommit == got.BaseCommit {
		t.Fatalf(
			"ReadReview test merge commit = %q, want a commit other than the head and the base",
			got.TestMergeCommit,
		)
	}

	if !got.MergedAt.IsZero() || got.MergeCommit != "" {
		t.Fatalf("ReadReview of an open review request = %+v, want no merge time and no merge commit", got)
	}
}

func caseReviewReadMerged(t *testing.T, h Harness) {
	t.Helper()

	opened := openReview(t, h, "dustpan/j-7")

	if err := h.Fixture.Merge(t.Context(), opened.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := readReview(t, h, opened.ID)
	if got.State != forge.ReviewMerged || got.MergedAt.IsZero() || forge.ValidateCommitID(got.MergeCommit) != nil {
		t.Fatalf("ReadReview after a merge = %+v, want state merged with the merge time and commit", got)
	}

	if got.MergeCommit == got.Head || got.TestMergeCommit != "" {
		t.Fatalf("ReadReview after a merge = %+v, want a merge commit other than the head and no test merge", got)
	}
}

func caseReviewReadClosed(t *testing.T, h Harness) {
	t.Helper()

	opened := openReview(t, h, "dustpan/j-8")

	if err := h.Fixture.Close(t.Context(), opened.ID); err != nil {
		t.Fatalf("close: %v", err)
	}

	got := readReview(t, h, opened.ID)
	if got.State != forge.ReviewClosed || !got.MergedAt.IsZero() || got.MergeCommit != "" || got.TestMergeCommit != "" {
		t.Fatalf("ReadReview after a close = %+v, want state closed with no merge", got)
	}
}

func caseReviewReadMissing(t *testing.T, h Harness) {
	t.Helper()

	_, err := h.Forge.ReadReview(t.Context(), h.Repo, "99999")
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("ReadReview of a missing review request = %v, want ErrNotFound", err)
	}
}

func caseReviewIDInvalid(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	before := callCount(ctx, t, h)

	for _, id := range []string{"", "0", "-1", "+1", "01", "1.0", "1/../2", "1?x=y", "1 2", "abc", "99999999999999999999", "１"} {
		if _, err := h.Forge.ReadReview(ctx, h.Repo, id); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("ReadReview(%q) = %v, want ErrInvalid", id, err)
		}

		if err := h.Forge.CloseReview(ctx, h.Repo, id); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("CloseReview(%q) = %v, want ErrInvalid", id, err)
		}
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid review ID made %d calls to the forge, want none", after-before)
	}
}

func caseReviewReadFork(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	own := openReview(t, h, "dustpan/j-9")

	id, err := h.Fixture.ForkReview(ctx, "dustpan/j-9", "main")
	if err != nil {
		t.Fatalf("fork review: %v", err)
	}

	got := readReview(t, h, id)
	if !got.FromFork() || got.HeadRepoID == got.BaseRepoID || !isNumber(got.HeadRepoID) ||
		got.BaseRepoID != own.BaseRepoID {
		t.Fatalf("ReadReview of a fork = %+v, want a head repository other than the base %q", got, own.BaseRepoID)
	}

	// The fork has a branch of the same name. The review request of the fork
	// is not the review request of the branch of the base repository.
	if got.Branch != own.Branch || got.ID == own.ID {
		t.Fatalf("ReadReview of a fork = %+v, want the same branch name and another number than %s", got, own.ID)
	}

	if again := readReview(t, h, own.ID); again.FromFork() {
		t.Fatalf("ReadReview = %+v, want a review request of the base repository", again)
	}
}

func caseReviewOpenIgnoresFork(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	pushBranch(t, h, "dustpan/j-10")

	forkID, err := h.Fixture.ForkReview(ctx, "dustpan/j-10", "main")
	if err != nil {
		t.Fatalf("fork review: %v", err)
	}

	got, err := h.Forge.OpenReview(ctx, h.Repo, reviewOn("dustpan/j-10", "main"))
	if err != nil {
		t.Fatalf("OpenReview = %v", err)
	}

	if got.ID == forkID || got.FromFork() {
		t.Fatalf("OpenReview = %+v, want a new review request of the base repository, not the fork %s", got, forkID)
	}
}

func caseReviewMergeStates(t *testing.T, h Harness) {
	t.Helper()

	opened := openReview(t, h, "dustpan/j-11")

	tests := []struct {
		host string
		want forge.MergeState
	}{
		{"unknown", forge.MergeUnknown},
		{"dirty", forge.MergeConflict},
		{"blocked", forge.MergeBlocked},
		{"behind", forge.MergeBehind},
		{"unstable", forge.MergeUnstable},
		{"draft", forge.MergeDraft},
		{"clean", forge.MergeClean},
		{"has_hooks", forge.MergeUnknown},
		{"a state that the adapter does not know", forge.MergeUnknown},
		{"CLEAN", forge.MergeUnknown},
	}

	for _, tt := range tests {
		if err := h.Fixture.SetMergeState(t.Context(), opened.ID, tt.host); err != nil {
			t.Fatalf("set the merge state %q: %v", tt.host, err)
		}

		got := readReview(t, h, opened.ID)
		if got.Mergeable != tt.want {
			t.Errorf("ReadReview with the host state %q = %q, want %q", tt.host, got.Mergeable, tt.want)
		}

		if got.State != forge.ReviewOpen || got.Head != opened.Head {
			t.Errorf("ReadReview with the host state %q = %+v, want the same open review request", tt.host, got)
		}
	}
}

func caseReviewUnknownHasNoTestMerge(t *testing.T, h Harness) {
	t.Helper()

	opened := openReview(t, h, "dustpan/j-12")

	if err := h.Fixture.SetMergeState(t.Context(), opened.ID, "unknown"); err != nil {
		t.Fatalf("set the merge state: %v", err)
	}

	got := readReview(t, h, opened.ID)
	if got.Mergeable != forge.MergeUnknown || got.TestMergeCommit != "" {
		t.Fatalf("ReadReview = %+v, want an unknown merge state and no test merge commit", got)
	}
}

func caseReviewCloseTwice(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	opened := openReview(t, h, "dustpan/j-13")

	for i := range 2 {
		if err := h.Forge.CloseReview(ctx, h.Repo, opened.ID); err != nil {
			t.Fatalf("CloseReview #%d = %v", i+1, err)
		}
	}

	if got := readReview(t, h, opened.ID); got.State != forge.ReviewClosed || got.MergeCommit != "" {
		t.Fatalf("ReadReview after CloseReview = %+v, want state closed", got)
	}
}

func caseReviewCloseMissing(t *testing.T, h Harness) {
	t.Helper()

	err := h.Forge.CloseReview(t.Context(), h.Repo, "99999")
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("CloseReview of a missing review request = %v, want ErrNotFound", err)
	}
}

func caseReviewCloseMerged(t *testing.T, h Harness) {
	t.Helper()

	opened := openReview(t, h, "dustpan/j-14")

	if err := h.Fixture.Merge(t.Context(), opened.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}

	err := h.Forge.CloseReview(t.Context(), h.Repo, opened.ID)
	if !errors.Is(err, forge.ErrConflict) {
		t.Fatalf("CloseReview of a merged review request = %v, want ErrConflict", err)
	}

	if got := readReview(t, h, opened.ID); got.State != forge.ReviewMerged {
		t.Fatalf("ReadReview = %+v, want state merged", got)
	}
}

func caseReviewCloseForeign(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()

	// A person's branch in the base repository, and the same name in a fork.
	person := openReview(t, h, "feature/x")

	if err := h.Forge.CloseReview(ctx, h.Repo, person.ID); !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("CloseReview of a branch outside the write-back = %v, want ErrRefused", err)
	}

	pushBranch(t, h, "dustpan/j-15")

	forkID, err := h.Fixture.ForkReview(ctx, "dustpan/j-15", "main")
	if err != nil {
		t.Fatalf("fork review: %v", err)
	}

	if closeErr := h.Forge.CloseReview(ctx, h.Repo, forkID); !errors.Is(closeErr, forge.ErrRefused) {
		t.Fatalf("CloseReview of a review request from a fork = %v, want ErrRefused", closeErr)
	}

	for _, id := range []string{person.ID, forkID} {
		if got := readReview(t, h, id); got.State != forge.ReviewOpen {
			t.Fatalf("ReadReview(%s) = %+v, want the review request still open", id, got)
		}
	}
}

func caseReviewCloseForeignClosed(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	person := openReview(t, h, "feature/y")

	if err := h.Fixture.Close(ctx, person.ID); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := h.Forge.CloseReview(ctx, h.Repo, person.ID); !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("CloseReview of a closed review request outside the write-back = %v, want ErrRefused", err)
	}
}

func caseDeleteBranchBaseOfOpenReview(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	pushBranch(t, h, "dustpan/base")
	pushBranch(t, h, "feature/z")

	// A review request from feature/z into the write-back branch.
	if _, err := h.Forge.OpenReview(ctx, h.Repo, reviewOn("feature/z", "dustpan/base")); err != nil {
		t.Fatalf("OpenReview = %v", err)
	}

	if err := h.Forge.DeleteBranch(ctx, h.Repo, "dustpan/base"); !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("DeleteBranch of the base of an open review request = %v, want ErrRefused", err)
	}

	if !branchExists(t, h, "dustpan/base") {
		t.Fatal("the branch is gone after a refused delete")
	}
}

func caseDeleteBranch(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	opened := openReview(t, h, "dustpan/j-16")

	if err := h.Fixture.Merge(ctx, opened.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}

	if err := h.Forge.DeleteBranch(ctx, h.Repo, "dustpan/j-16"); err != nil {
		t.Fatalf("DeleteBranch = %v", err)
	}

	if branchExists(t, h, "dustpan/j-16") {
		t.Fatal("the branch dustpan/j-16 exists after DeleteBranch")
	}

	if got := readReview(t, h, opened.ID); got.State != forge.ReviewMerged {
		t.Fatalf("ReadReview after DeleteBranch = %+v, want state merged", got)
	}

	if err := h.Forge.DeleteBranch(ctx, h.Repo, "dustpan/j-16"); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("DeleteBranch again = %v, want ErrNotFound", err)
	}
}

func caseDeleteBranchMissing(t *testing.T, h Harness) {
	t.Helper()

	err := h.Forge.DeleteBranch(t.Context(), h.Repo, "dustpan/no-such-branch")
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("DeleteBranch of a missing branch = %v, want ErrNotFound", err)
	}
}

func caseDeleteBranchOutsideWriteBack(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	pushBranch(t, h, "feature/x")
	pushBranch(t, h, "dustpan-other")

	before := callCount(ctx, t, h)

	for _, branch := range []string{
		"main", "feature/x", "dustpan-other", "dustpan/", "release/dustpan/x", "Dustpan/j-1", "",
		"dustpan/../main", "refs/heads/dustpan/j-1", "dustpan/j 1", "dustpan/j-1\n",
	} {
		if err := h.Forge.DeleteBranch(ctx, h.Repo, branch); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("DeleteBranch(%q) = %v, want ErrInvalid", branch, err)
		}
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("a refused delete made %d calls to the forge, want none", after-before)
	}

	for _, branch := range []string{"main", "feature/x", "dustpan-other"} {
		if !branchExists(t, h, branch) {
			t.Errorf("the branch %s is gone after a refused delete", branch)
		}
	}
}

func caseDeleteBranchWithOpenReview(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	opened := openReview(t, h, "dustpan/j-19")

	if err := h.Forge.DeleteBranch(ctx, h.Repo, "dustpan/j-19"); !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("DeleteBranch of the head of an open review request = %v, want ErrRefused", err)
	}

	if !branchExists(t, h, "dustpan/j-19") {
		t.Fatal("the branch is gone after a refused delete")
	}

	if got := readReview(t, h, opened.ID); got.State != forge.ReviewOpen {
		t.Fatalf("ReadReview = %+v, want the review request still open", got)
	}
}

func caseDeleteDefaultBranch(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	pushBranch(t, h, "dustpan/trunk")

	if err := h.Fixture.SetDefaultBranch(ctx, "dustpan/trunk"); err != nil {
		t.Fatalf("set the default branch: %v", err)
	}

	if err := h.Forge.DeleteBranch(ctx, h.Repo, "dustpan/trunk"); !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("DeleteBranch of the default branch = %v, want ErrRefused", err)
	}

	if !branchExists(t, h, "dustpan/trunk") {
		t.Fatal("the default branch is gone after a refused delete")
	}
}

func caseDeleteProtectedBranch(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	pushBranch(t, h, "dustpan/j-17")

	if err := h.Fixture.Protect(ctx, "dustpan/j-17"); err != nil {
		t.Fatalf("protect: %v", err)
	}

	if err := h.Forge.DeleteBranch(ctx, h.Repo, "dustpan/j-17"); !errors.Is(err, forge.ErrRefused) {
		t.Fatalf("DeleteBranch of a protected branch = %v, want ErrRefused", err)
	}

	if !branchExists(t, h, "dustpan/j-17") {
		t.Fatal("the protected branch is gone after a refused delete")
	}
}

// reviewCalls lists the calls of the review methods that need a review
// request, so that a case can break the forge once for all of them.
func reviewCalls(h Harness, id string) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"OpenReview": func(ctx context.Context) error {
			_, err := h.Forge.OpenReview(ctx, h.Repo, reviewOn("dustpan/j-18", "main"))

			return err
		},
		"ReadReview": func(ctx context.Context) error {
			_, err := h.Forge.ReadReview(ctx, h.Repo, id)

			return err
		},
		"CloseReview": func(ctx context.Context) error { return h.Forge.CloseReview(ctx, h.Repo, id) },
		"DeleteBranch": func(ctx context.Context) error {
			return h.Forge.DeleteBranch(ctx, h.Repo, "dustpan/j-18")
		},
	}
}

func caseReviewServerError(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	opened := openReview(t, h, "dustpan/j-18")

	restore, err := h.Fixture.Unavailable(ctx, 0)
	if err != nil {
		t.Fatalf("unavailable: %v", err)
	}

	defer restore()

	for name, run := range reviewCalls(h, opened.ID) {
		if err := run(ctx); !errors.Is(err, forge.ErrUnavailable) {
			t.Errorf("%s with a forge that fails = %v, want ErrUnavailable", name, err)
		}
	}
}

func caseReviewGarbled(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	opened := openReview(t, h, "dustpan/j-18")

	restore, err := h.Fixture.Garble(ctx)
	if err != nil {
		t.Fatalf("garble: %v", err)
	}

	defer restore()

	for _, name := range []string{"OpenReview", "ReadReview", "CloseReview"} {
		if err := reviewCalls(h, opened.ID)[name](ctx); !errors.Is(err, forge.ErrUnavailable) {
			t.Errorf("%s with an answer that does not parse = %v, want ErrUnavailable", name, err)
		}
	}
}

func caseReviewOversize(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	opened := openReview(t, h, "dustpan/j-18")

	restore, err := h.Fixture.Oversize(ctx)
	if err != nil {
		t.Fatalf("oversize: %v", err)
	}

	defer restore()

	for _, name := range []string{"OpenReview", "ReadReview", "CloseReview"} {
		if err := reviewCalls(h, opened.ID)[name](ctx); !errors.Is(err, forge.ErrUnavailable) {
			t.Errorf("%s with an answer above the size limit = %v, want ErrUnavailable", name, err)
		}
	}

	restore()

	if got := readReview(t, h, opened.ID); got.State != forge.ReviewOpen {
		t.Fatalf("ReadReview after the restore = %+v, want the open review request", got)
	}
}
