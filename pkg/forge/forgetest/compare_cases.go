package forgetest

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// changedFiles returns the path, the old path and the status of each file of c,
// as text, so that a case compares a list in one step.
func changedFiles(c forge.Comparison) []string {
	out := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		out = append(out, f.Path+"|"+f.OldPath+"|"+string(f.Status))
	}

	return out
}

func caseCompareFiles(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	base := push(t, h, map[string][]byte{
		"keep.txt": []byte("same\n"), "edit.txt": []byte("old line\n"), "gone.txt": []byte("gone\n"),
	})
	head := push(t, h, map[string][]byte{
		"edit.txt": []byte("new line\n"), "gone.txt": nil, "stacks/new.yaml": []byte("a: 1\n"),
	})

	got, err := h.Forge.Compare(ctx, h.Repo, base.ID, head.ID, 1<<20)
	if err != nil {
		t.Fatalf("Compare = %v", err)
	}

	want := []string{"edit.txt||modified", "gone.txt||deleted", "stacks/new.yaml||added"}
	if !slices.Equal(changedFiles(got), want) {
		t.Fatalf("Compare files = %v, want %v", changedFiles(got), want)
	}

	if !got.BaseIsAncestor || got.Truncated {
		t.Fatalf("Compare ancestor = %v, truncated = %v; want true and false", got.BaseIsAncestor, got.Truncated)
	}

	for _, line := range []string{"-old line", "+new line", "+a: 1", "-gone"} {
		if !bytes.Contains(got.Patch, []byte(line)) {
			t.Errorf("the patch has no line %q: %q", line, got.Patch)
		}
	}

	if bytes.Contains(got.Patch, []byte("same")) {
		t.Errorf("the patch has the unchanged file: %q", got.Patch)
	}

	// The same commit on both sides is an ancestor with no change.
	same, err := h.Forge.Compare(ctx, h.Repo, head.ID, head.ID, 1<<20)
	if err != nil || len(same.Files) != 0 || !same.BaseIsAncestor || len(same.Patch) != 0 {
		t.Fatalf("Compare of one commit = %+v, %v; want no file and an ancestor", same, err)
	}
}

func caseCompareRename(t *testing.T, h Harness) {
	t.Helper()

	base := push(t, h, map[string][]byte{"old/name.yaml": []byte("a: 1\nb: 2\nc: 3\n")})
	head := push(t, h, map[string][]byte{"old/name.yaml": nil, "new/name.yaml": []byte("a: 1\nb: 2\nc: 3\n")})

	got, err := h.Forge.Compare(t.Context(), h.Repo, base.ID, head.ID, 1<<20)
	if err != nil {
		t.Fatalf("Compare = %v", err)
	}

	want := []string{"new/name.yaml|old/name.yaml|renamed"}
	if !slices.Equal(changedFiles(got), want) {
		t.Fatalf("Compare files = %v, want %v", changedFiles(got), want)
	}
}

func caseCompareNotAncestor(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	push(t, h, map[string][]byte{"a.txt": []byte("a\n")})

	side, err := h.Fixture.Push(ctx, "side", map[string][]byte{"side.txt": []byte("side\n")})
	if err != nil {
		t.Fatalf("push to side: %v", err)
	}

	head := push(t, h, map[string][]byte{"main.txt": []byte("main\n")})

	// The two commits are on two lines of history.
	diverged, err := h.Forge.Compare(ctx, h.Repo, side.ID, head.ID, 1<<20)
	if err != nil {
		t.Fatalf("Compare of two lines of history = %v", err)
	}

	if diverged.BaseIsAncestor {
		t.Fatal("Compare says that a commit of another line of history is an ancestor")
	}

	// The head is not an ancestor of an older commit either way round.
	older := push(t, h, map[string][]byte{"newer.txt": []byte("newer\n")})

	back, err := h.Forge.Compare(ctx, h.Repo, older.ID, head.ID, 1<<20)
	if err != nil {
		t.Fatalf("Compare with the newer commit as the base = %v", err)
	}

	if back.BaseIsAncestor {
		t.Fatal("Compare says that a newer commit is an ancestor of an older one")
	}
}

// compareAt calls Compare and fails the test on an error.
func compareAt(t *testing.T, h Harness, base, head string, limit int64) forge.Comparison {
	t.Helper()

	got, err := h.Forge.Compare(t.Context(), h.Repo, base, head, limit)
	if err != nil {
		t.Fatalf("Compare with the limit %d = %v", limit, err)
	}

	return got
}

func caseCompareTruncates(t *testing.T, h Harness) {
	t.Helper()

	const limit = 40

	base := push(t, h, map[string][]byte{"a.txt": []byte("a\n")})
	head := push(t, h, map[string][]byte{
		"a.txt": []byte(
			strings.Repeat("a long line that changes\n", 20),
		),
		"b.txt": []byte("b\n"),
		"c.txt": []byte("c\n"),
	})

	full := compareAt(t, h, base.ID, head.ID, 1<<20)
	if full.Truncated || len(full.Patch) <= limit {
		t.Fatalf("Compare with a large limit = %d bytes, truncated %v; want a patch above %d bytes",
			len(full.Patch), full.Truncated, limit)
	}

	cut := compareAt(t, h, base.ID, head.ID, limit)

	// The patch is the first part of the whole patch, and every file is listed.
	if !cut.Truncated || len(cut.Patch) != limit || !bytes.HasPrefix(full.Patch, cut.Patch) {
		t.Fatalf("Compare above the limit = %d bytes, truncated %v; want the first %d bytes",
			len(cut.Patch), cut.Truncated, limit)
	}

	if !slices.Equal(changedFiles(cut), changedFiles(full)) || len(cut.Files) != 3 {
		t.Fatalf("Compare above the limit lists %v, want %v", changedFiles(cut), changedFiles(full))
	}

	// A patch of exactly the limit is not cut.
	exact := compareAt(t, h, base.ID, head.ID, int64(len(full.Patch)))
	if exact.Truncated || len(exact.Patch) != len(full.Patch) {
		t.Fatalf("Compare at the size of the patch = %d bytes, truncated %v", len(exact.Patch), exact.Truncated)
	}
}

func caseCompareUnknown(t *testing.T, h Harness) {
	t.Helper()

	head := push(t, h, map[string][]byte{"a.txt": []byte("a\n")})

	for _, pair := range [][2]string{{missingCommit(), head.ID}, {head.ID, missingCommit()}} {
		if _, err := h.Forge.Compare(t.Context(), h.Repo, pair[0], pair[1], 1<<20); !errors.Is(err, forge.ErrNotFound) {
			t.Errorf("Compare of %s and %s = %v, want ErrNotFound", pair[0][:7], pair[1][:7], err)
		}
	}
}

func caseCompareInvalid(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	head := push(t, h, map[string][]byte{"a.txt": []byte("a\n")})
	before := callCount(ctx, t, h)

	for _, id := range []string{"", "main", head.ID[:10], strings.ToUpper(head.ID), head.ID + "..main"} {
		if _, err := h.Forge.Compare(ctx, h.Repo, id, head.ID, 1<<20); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Compare with the base %q = %v, want ErrInvalid", id, err)
		}

		if _, err := h.Forge.Compare(ctx, h.Repo, head.ID, id, 1<<20); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Compare with the head %q = %v, want ErrInvalid", id, err)
		}
	}

	for _, limit := range []int64{0, -1} {
		if _, err := h.Forge.Compare(ctx, h.Repo, head.ID, head.ID, limit); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Compare with the limit %d = %v, want ErrInvalid", limit, err)
		}
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid compare made %d calls to the forge, want none", after-before)
	}
}
