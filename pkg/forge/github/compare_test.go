package github_test

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

// comparePath is the key of a compare call of the canned host for a page.
func comparePath(page int) string {
	return fmt.Sprintf(
		"GET /repos/acme/infra/compare/%s...%s?per_page=100&page=%d", baseSHA, headSHA, page,
	)
}

// compareWith returns the answer of the compare call with the files.
func compareWith(status, mergeBase string, files ...map[string]any) hostAnswer {
	return okAnswer(map[string]any{
		"status": status, "merge_base_commit": map[string]any{"sha": mergeBase}, "files": files,
	})
}

func file(name, status string) map[string]any {
	return map[string]any{"filename": name, "status": status, "patch": "@@ -1 +1 @@\n-a\n+b"}
}

// TestCompareReadsTheAnswer checks the files, the patch and the ancestry that
// Compare takes from the answer of GitHub.
func TestCompareReadsTheAnswer(t *testing.T) {
	t.Parallel()

	renamed := file("new.txt", "renamed")
	renamed["previous_filename"] = "old.txt"
	renamed["patch"] = ""

	f := cannedForge(t, map[string]hostAnswer{
		comparePath(1): compareWith("ahead", baseSHA,
			file("a.txt", "modified"), file("b.txt", "added"), file("c.txt", "removed"), renamed,
			file("d.txt", "unchanged"), file("e.txt", "changed"), file("f.txt", "copied")),
	})

	got, err := f.Compare(t.Context(), "acme/infra", baseSHA, headSHA, 1<<20)
	if err != nil {
		t.Fatalf("Compare = %v", err)
	}

	want := []forge.ChangedFile{
		{Path: "a.txt", OldPath: "", Status: forge.FileModified},
		{Path: "b.txt", OldPath: "", Status: forge.FileAdded},
		{Path: "c.txt", OldPath: "", Status: forge.FileDeleted},
		{Path: "new.txt", OldPath: "old.txt", Status: forge.FileRenamed},
		{Path: "e.txt", OldPath: "", Status: forge.FileModified},
		{Path: "f.txt", OldPath: "", Status: forge.FileAdded},
	}

	if len(got.Files) != len(want) {
		t.Fatalf("Compare files = %+v, want %+v", got.Files, want)
	}

	for i := range want {
		if got.Files[i] != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, got.Files[i], want[i])
		}
	}

	if !got.BaseIsAncestor || got.Truncated {
		t.Fatalf("ancestor = %v, truncated = %v; want true and false", got.BaseIsAncestor, got.Truncated)
	}

	for _, part := range []string{
		"diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n",
		"diff --git a/b.txt b/b.txt\n--- /dev/null\n+++ b/b.txt\n",
		"diff --git a/c.txt b/c.txt\n--- a/c.txt\n+++ /dev/null\n",
		"diff --git a/old.txt b/new.txt\nrename from old.txt\nrename to new.txt\ndiff --git a/e.txt",
	} {
		if !bytes.Contains(got.Patch, []byte(part)) {
			t.Errorf("the patch has no %q: %q", part, got.Patch)
		}
	}
}

// TestCompareAncestry checks that only the status "ahead" or "identical" with
// the base as the merge base makes the base an ancestor.
func TestCompareAncestry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    string
		mergeBase string
		want      bool
	}{
		{"ahead", "ahead", baseSHA, true},
		{"identical", "identical", baseSHA, true},
		{"diverged", "diverged", baseSHA, false},
		{"behind", "behind", baseSHA, false},
		{"ahead of another merge base", "ahead", mergeSHA, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{comparePath(1): compareWith(tt.status, tt.mergeBase)})

			got, err := f.Compare(t.Context(), "acme/infra", baseSHA, headSHA, 1<<20)
			if err != nil || got.BaseIsAncestor != tt.want {
				t.Fatalf("Compare = ancestor %v, %v; want %v", got.BaseIsAncestor, err, tt.want)
			}
		})
	}
}

// TestCompareRefusesAnAnswerItCannotUse checks that an answer with a path that
// leaves the repository, a status that the adapter does not know or no merge
// base is ErrUnavailable and never a comparison.
func TestCompareRefusesAnAnswerItCannotUse(t *testing.T) {
	t.Parallel()

	noPrevious := file("new.txt", "renamed")
	badPrevious := file("new.txt", "renamed")
	badPrevious["previous_filename"] = "../old.txt"

	tests := []struct {
		name   string
		answer hostAnswer
	}{
		{"a path above the repository", compareWith("ahead", baseSHA, file("../x", "modified"))},
		{"a path in .git", compareWith("ahead", baseSHA, file(".git/config", "modified"))},
		{"an absolute path", compareWith("ahead", baseSHA, file("/etc/passwd", "added"))},
		{"a path with a control character", compareWith("ahead", baseSHA, file("a\nb", "added"))},
		{"no path", compareWith("ahead", baseSHA, file("", "added"))},
		{"a status of a file that is not known", compareWith("ahead", baseSHA, file("a", "teleported"))},
		{"a rename with no old path", compareWith("ahead", baseSHA, noPrevious)},
		{"a rename from a path above the repository", compareWith("ahead", baseSHA, badPrevious)},
		{"a status of the compare that is not known", compareWith("sideways", baseSHA)},
		{"no merge base", compareWith("ahead", "")},
		{"a merge base that is not a commit ID", compareWith("ahead", "main")},
		{"an answer that does not parse", statusAnswer(http.StatusOK, `{"files": [`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{comparePath(1): tt.answer})

			got, err := f.Compare(t.Context(), "acme/infra", baseSHA, headSHA, 1<<20)
			if !errors.Is(err, forge.ErrUnavailable) || len(got.Files) != 0 || len(got.Patch) != 0 {
				t.Fatalf("Compare = %+v, %v; want ErrUnavailable and an empty comparison", got, err)
			}

			if err != nil && strings.Contains(err.Error(), cannedPAT) {
				t.Fatalf("the error holds the token: %v", err)
			}
		})
	}
}

// TestComparePages checks that Compare reads the next page after a full
// page, and that more than 1000 files are ErrTooLarge.
func TestComparePages(t *testing.T) {
	t.Parallel()

	page := func(prefix string, n int) []map[string]any {
		files := make([]map[string]any, 0, n)
		for i := range n {
			files = append(files, map[string]any{"filename": fmt.Sprintf("%s/%03d.txt", prefix, i), "status": "added"})
		}

		return files
	}

	t.Run("two pages", func(t *testing.T) {
		t.Parallel()

		f := cannedForge(t, map[string]hostAnswer{
			comparePath(1): compareWith("ahead", baseSHA, page("p1", 100)...),
			comparePath(2): compareWith("ahead", baseSHA, page("p2", 3)...),
		})

		got, err := f.Compare(t.Context(), "acme/infra", baseSHA, headSHA, 1<<20)
		if err != nil || len(got.Files) != 103 {
			t.Fatalf("Compare = %d files, %v; want 103 files", len(got.Files), err)
		}
	})

	t.Run("more than the most", func(t *testing.T) {
		t.Parallel()

		answers := map[string]hostAnswer{}
		for p := 1; p <= 10; p++ {
			answers[comparePath(p)] = compareWith("ahead", baseSHA, page(fmt.Sprintf("p%d", p), 100)...)
		}

		f := cannedForge(t, answers)

		if _, err := f.Compare(t.Context(), "acme/infra", baseSHA, headSHA, 1<<20); !errors.Is(err, forge.ErrTooLarge) {
			t.Fatalf("Compare of 1000 files and a next page = %v, want ErrTooLarge", err)
		}
	})
}

// TestCompareMapsTheStatus checks the sentinels for the failures of the call.
func TestCompareMapsTheStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer hostAnswer
		want   error
	}{
		{"a missing commit", statusAnswer(http.StatusNotFound, `{"message":"Not Found"}`), forge.ErrNotFound},
		{
			"a commit that is not found by name",
			statusAnswer(http.StatusUnprocessableEntity, `{"message":"x"}`),
			forge.ErrNotFound,
		},
		{"a bad gateway", statusAnswer(http.StatusBadGateway, ``), forge.ErrUnavailable},
		{"a credential that is refused", statusAnswer(http.StatusUnauthorized, ``), forge.ErrAuth},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{comparePath(1): tt.answer})

			if _, err := f.Compare(t.Context(), "acme/infra", baseSHA, headSHA, 1<<20); !errors.Is(err, tt.want) {
				t.Fatalf("Compare = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestCompareCutsThePatchAtARuneBoundary checks that a limit that falls inside
// a multi-byte character cuts the patch at the start of that character.
func TestCompareCutsThePatchAtARuneBoundary(t *testing.T) {
	t.Parallel()

	f := cannedForge(t, map[string]hostAnswer{
		comparePath(1): compareWith("ahead", baseSHA, map[string]any{
			"filename": "a.txt",
			"status":   "modified",
			"patch":    "@@ -1 +1 @@\n-héllo\n+wörld 日本😀\n",
		}),
	})

	full, err := f.Compare(t.Context(), "acme/infra", baseSHA, headSHA, 1<<20)
	if err != nil {
		t.Fatalf("Compare = %v", err)
	}

	if full.Truncated {
		t.Fatal("Compare at a large limit = truncated; want the full patch")
	}

	for limit := 1; limit <= len(full.Patch); limit++ {
		got, err := f.Compare(t.Context(), "acme/infra", baseSHA, headSHA, int64(limit))
		if err != nil {
			t.Errorf("Compare with the limit %d = %v", limit, err)
			continue
		}

		if !utf8.Valid(got.Patch) {
			t.Errorf("Compare with the limit %d = a patch that is not valid UTF-8: %q", limit, got.Patch)
		}

		if !bytes.HasPrefix(full.Patch, got.Patch) {
			t.Errorf("Compare with the limit %d = a patch that is not a prefix of the full patch: %q", limit, got.Patch)
		}

		if len(got.Patch) > limit {
			t.Errorf("Compare with the limit %d = a patch of %d bytes", limit, len(got.Patch))
		}

		if got.Truncated != (limit < len(full.Patch)) {
			t.Errorf("Compare with the limit %d = truncated %v; want %v", limit, got.Truncated, limit < len(full.Patch))
		}
	}
}
