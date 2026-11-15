//go:build e2e && upgrade

package e2e_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestMixedVersionTagDiscovery checks the tag decisions of the mixed-version
// matrix without a server and without a CLI: which tags run, what an empty
// tag list means, and how a tag that cannot run names itself.
func TestMixedVersionTagDiscovery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"newest two ancestor release tags run", checkNewestTwoAncestors},
		{"one ancestor release tag runs", checkOneAncestor},
		{"no release tag reports zero old binaries", checkZeroOldBinaries},
		{"a shallow clone names the missing tag fetch", checkShallowTagFetch},
		{"a full clone reports zero old binaries", checkFullCloneFetch},
		{"a tag before the minimum check names the tag", checkPreMinimumTag},
		{"a tag that does not build names the tag", checkMissingTagBuild},
		{"one failed tag still fails the run", checkOneFailedTagFailsTheRun},
		{"a name that is not a release tag has no minimum check", checkNonReleaseName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t)
		})
	}
}

// checkNewestTwoAncestors checks that the matrix runs the newest 2 release
// tags that are ancestors of HEAD and no other tag, and that the report
// names each of them.
func checkNewestTwoAncestors(t *testing.T) {
	t.Helper()

	all := []string{"nightly", "v0.1.0", "v0.2.0", "v0.3.0", "v0.4.0", "release-1"}
	ancestors := map[string]bool{
		"nightly":   true,
		"v0.1.0":    true,
		"v0.2.0":    true,
		"v0.4.0":    true,
		"release-1": true,
	}

	got := e2e.MixedTagChoice(all, mixedReleases, func(tag string) bool { return ancestors[tag] })

	want := []string{"v0.2.0", "v0.4.0"}
	if !slices.Equal(got, want) {
		t.Errorf("the matrix chose %v, want %v: it runs the newest 2 release tags that are ancestors of HEAD "+
			"and no tag that is not a release or not an ancestor", got, want)
	}

	report := e2e.MixedTagReport(want)
	for _, part := range []string{"2 tags", "v0.2.0", "v0.4.0"} {
		if !strings.Contains(report, part) {
			t.Errorf("the 2-tag report %q lacks %q", report, part)
		}
	}
}

// checkOneAncestor checks that one ancestor release tag runs alone.
func checkOneAncestor(t *testing.T) {
	t.Helper()

	all := []string{"v0.1.0", "v0.2.0"}
	ancestors := map[string]bool{"v0.1.0": true}

	got := e2e.MixedTagChoice(all, mixedReleases, func(tag string) bool { return ancestors[tag] })

	if want := []string{"v0.1.0"}; !slices.Equal(got, want) {
		t.Errorf("the matrix chose %v, want %v: one ancestor release tag runs alone", got, want)
	}

	if report := e2e.MixedTagReport([]string{"v0.1.0"}); !strings.Contains(report, "1 tags: v0.1.0") {
		t.Errorf("the 1-tag report %q lacks the tag and its count", report)
	}
}

// checkZeroOldBinaries checks that no release tag gives the 0-tag report,
// which names the second process of the current binary.
func checkZeroOldBinaries(t *testing.T) {
	t.Helper()

	got := e2e.MixedTagChoice([]string{"nightly", "release-1"}, mixedReleases, func(string) bool { return true })
	if len(got) != 0 {
		t.Errorf("the matrix chose %v, want no tag: no tag names a release", got)
	}

	report := e2e.MixedTagReport(got)
	for _, part := range []string{"0 tags", "second process of the current binary"} {
		if !strings.Contains(report, part) {
			t.Errorf("the 0-tag report %q lacks %q", report, part)
		}
	}
}

// checkShallowTagFetch checks that a shallow clone with no release tag fails
// with the fetch command, instead of reporting zero old binaries.
func checkShallowTagFetch(t *testing.T) {
	t.Helper()

	err := e2e.MixedTagFetchError(e2e.FetchShallow)
	if err == nil {
		t.Fatal("a shallow clone that shows no release tag must fail instead of reporting zero old binaries")
	}

	if !strings.Contains(err.Error(), "git fetch --tags") {
		t.Errorf("the shallow-clone error %q lacks the fetch command", err)
	}
}

// checkFullCloneFetch checks that a full clone with no release tag reports
// zero old binaries: git fetched every tag, so the repository has none.
func checkFullCloneFetch(t *testing.T) {
	t.Helper()

	if err := e2e.MixedTagFetchError(e2e.FetchFull); err != nil {
		t.Errorf("a full clone with no release tag must report zero old binaries, got %v", err)
	}
}

// checkPreMinimumTag checks the read that a release tag without the minimum
// binary version check takes, and the failure that follows: the read gives no
// minimum check, and the matrix fails the run with the name of the tag.
func checkPreMinimumTag(t *testing.T) {
	t.Helper()

	err := e2e.MixedMinimumTagError("v0.1.0")
	if err == nil {
		t.Fatal("a tag before the minimum check gives no refusal signal and must fail the run")
	}

	if !strings.Contains(err.Error(), "v0.1.0") {
		t.Errorf("the pre-minimum error %q lacks the tag", err)
	}

	// The read of one tag greps the tree of the tag. This worktree holds no
	// release tag at all and the suite must not make one, so no fixture tag
	// can carry a tree without the check here. The reads below cover the
	// branches that the read of a tag takes.
	//
	// A tree with the check answers true, so the tag of this source runs.
	if !e2e.RefHasMinimumCheck(t.Context(), t, "HEAD") {
		t.Error("the current tree holds the minimum binary version check, so its read must find it")
	}

	// A tree of this repository without the check answers false, which is the
	// state of a release that predates the check: the matrix fails for it.
	if e2e.RefHasMinimumCheck(t.Context(), t, "HEAD:docs") {
		t.Error("a tree without the minimum binary version check must give no minimum check")
	}

	// A release name whose tree git cannot read fails closed, so an
	// incomplete fetch never passes a tag that gives no refusal signal.
	if e2e.TagHasMinimumCheck(t.Context(), t, "v0.0.0-not-a-tag") {
		t.Error("a release tag whose tree git cannot read must give no minimum check")
	}

	// A hostile ref that starts with "-" answers false, so it never becomes
	// an option of the git grep command. Without the guard the read searches
	// the working tree, which holds the sentinel, and answers true.
	if e2e.RefHasMinimumCheck(t.Context(), t, "-q") {
		t.Error("a ref that starts with - must give no minimum check")
	}

	// An empty ref answers false, so a missing name never reads a tree.
	if e2e.RefHasMinimumCheck(t.Context(), t, "") {
		t.Error("an empty ref must give no minimum check")
	}
}

// checkOneFailedTagFailsTheRun checks the isolation of the peer build loop: a
// tag that cannot run fails the run with its name, and the other tag still
// builds, so one working tag never hides a broken one.
func checkOneFailedTagFailsTheRun(t *testing.T) {
	t.Helper()

	const broken = "v0.1.0"

	rep := &mixedBuildRecorder{failures: nil}
	built := make([]string, 0, 2)

	build := func(_ context.Context, tag string) (string, error) {
		if tag == broken {
			return "", e2e.MixedMinimumTagError(tag)
		}

		built = append(built, tag)

		return "peer-" + tag, nil
	}

	peers := mixedBuildTagPeers(t.Context(), rep, []string{broken, "v0.2.0"}, "current-binary", build)

	if len(rep.failures) != 1 || !strings.Contains(rep.failures[0].Error(), broken) {
		t.Errorf(
			"the run reported the failures %v, want one failure that names %s: a tag that cannot run fails the run",
			rep.failures,
			broken,
		)
	}

	want := []string{"v0.2.0"}

	if !slices.Equal(built, want) {
		t.Errorf("the build loop ran for %v, want %v: one broken tag never stops the other tag", built, want)
	}

	if len(peers) != 1 {
		t.Fatalf("the loop kept %d peers (%v), want 1: the tag that builds stays in the matrix", len(peers), peers)
	}

	if peers[0].label != want[0] {
		t.Errorf("the loop kept the peer %q, want %q", peers[0].label, want[0])
	}
}

// mixedBuildRecorder records the failures that the peer build loop reports,
// so a case can check that a tag that cannot run fails the run without
// failing its own run.
type mixedBuildRecorder struct {
	failures []error
}

func (*mixedBuildRecorder) Helper() {}

func (r *mixedBuildRecorder) Error(args ...any) {
	for _, arg := range args {
		if err, ok := arg.(error); ok {
			r.failures = append(r.failures, err)
		}
	}
}

func (*mixedBuildRecorder) Logf(string, ...any) {}

// checkMissingTagBuild checks that a tag that does not build fails with its
// name and the log line.
func checkMissingTagBuild(t *testing.T) {
	t.Helper()

	const missing = "v0.0.0-not-a-tag"

	_, err := e2e.MixedBuildTag(t.Context(), t, missing, t.TempDir())
	if err == nil {
		t.Fatalf("building %s must fail: no such tag exists", missing)
	}

	if !strings.Contains(err.Error(), missing) {
		t.Errorf("the build failure %q lacks the tag", err)
	}
}

// checkNonReleaseName checks that a name that is not a release tag has no
// minimum check, so a wrong name never reads a git tree.
func checkNonReleaseName(t *testing.T) {
	t.Helper()

	if e2e.TagHasMinimumCheck(t.Context(), t, "nightly") {
		t.Error("a name that is not a release tag must have no minimum check")
	}
}
