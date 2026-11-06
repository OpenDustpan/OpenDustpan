package forgetest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// treeNames returns the "kind name" text of each entry, so that a case compares
// a list in one step.
func treeNames(entries []forge.TreeEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, string(e.Kind)+" "+e.Name)
	}

	return out
}

func caseTreeLists(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	c := push(t, h, map[string][]byte{
		"README.md":            []byte("hello\n"),
		"stacks/prod.yaml":     []byte("a: 1\n"),
		"stacks/stage.yaml":    []byte("a: 2\n"),
		"stacks/sub/deep.yaml": []byte("a: 3\n"),
	})

	root, err := h.Forge.Tree(ctx, h.Repo, c.ID, "", 100)
	if err != nil {
		t.Fatalf("Tree of the root = %v", err)
	}

	// The entries are in the order of the names.
	if want := []string{"file README.md", "dir stacks"}; !slices.Equal(treeNames(root), want) {
		t.Fatalf("Tree of the root = %v, want %v", treeNames(root), want)
	}

	dir, err := h.Forge.Tree(ctx, h.Repo, c.ID, "stacks", 100)
	if err != nil {
		t.Fatalf("Tree of stacks = %v", err)
	}

	want := []string{"file prod.yaml", "file stage.yaml", "dir sub"}
	if got := treeNames(dir); !slices.Equal(got, want) {
		t.Fatalf("Tree of stacks = %v, want %v", got, want)
	}

	// Each entry is inside the directory, and a file has its blob ID and size.
	file, err := h.Forge.ReadFile(ctx, h.Repo, c.ID, "stacks/prod.yaml", 1<<20)
	if err != nil {
		t.Fatalf("ReadFile = %v", err)
	}

	for _, e := range dir {
		if e.Path != "stacks/"+e.Name || strings.Contains(e.Name, "/") || e.ID == "" {
			t.Errorf("the entry %+v is not a direct child of stacks", e)
		}
	}

	if dir[0].ID != file.BlobID || dir[0].Size != int64(len("a: 1\n")) {
		t.Errorf("the entry of prod.yaml = %+v, want the blob %s and %d bytes", dir[0], file.BlobID, len("a: 1\n"))
	}
}

func caseTreeKinds(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	push(t, h, map[string][]byte{"plain.txt": []byte("x\n")})

	if _, err := h.Fixture.PushEntry(ctx, "main", "link", EntrySymlink, "target"); err != nil {
		t.Fatalf("push a symlink: %v", err)
	}

	c, err := h.Fixture.PushEntry(ctx, "main", "module", EntrySubmodule, "v1")
	if err != nil {
		t.Fatalf("push a submodule: %v", err)
	}

	got, err := h.Forge.Tree(ctx, h.Repo, c.ID, "", 100)
	if err != nil {
		t.Fatalf("Tree = %v", err)
	}

	kinds := map[string]forge.TreeKind{}
	for _, e := range got {
		kinds[e.Name] = e.Kind
	}

	if kinds["link"] != forge.TreeSymlink || kinds["module"] != forge.TreeSubmodule ||
		kinds["plain.txt"] != forge.TreeFile {
		t.Fatalf("the kinds are %v", kinds)
	}
}

func caseTreeAtACommit(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	first := push(t, h, map[string][]byte{"dir/a.txt": []byte("a")})
	second := push(t, h, map[string][]byte{"dir/b.txt": []byte("b"), "dir/a.txt": nil})

	old, err := h.Forge.Tree(ctx, h.Repo, first.ID, "dir", 10)
	if err != nil || !slices.Equal(treeNames(old), []string{"file a.txt"}) {
		t.Fatalf("Tree at the first commit = %v, %v", treeNames(old), err)
	}

	now, err := h.Forge.Tree(ctx, h.Repo, second.ID, "dir", 10)
	if err != nil || !slices.Equal(treeNames(now), []string{"file b.txt"}) {
		t.Fatalf("Tree at the second commit = %v, %v", treeNames(now), err)
	}
}

func caseTreeLimit(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	files := map[string][]byte{}

	for i := range 5 {
		files[fmt.Sprintf("many/f%d.txt", i)] = []byte("x")
	}

	c := push(t, h, files)

	got, err := h.Forge.Tree(ctx, h.Repo, c.ID, "many", 5)
	if err != nil || len(got) != 5 {
		t.Fatalf("Tree at the limit = %d entries, %v; want 5", len(got), err)
	}

	// One entry above the limit is an error, never a part of the list.
	got, err = h.Forge.Tree(ctx, h.Repo, c.ID, "many", 4)
	if !errors.Is(err, forge.ErrTooLarge) || len(got) != 0 {
		t.Fatalf("Tree above the limit = %d entries, %v; want ErrTooLarge and no entry", len(got), err)
	}
}

func caseTreeInvalid(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	c := push(t, h, map[string][]byte{"stacks/prod.yaml": []byte("a")})
	before := callCount(ctx, t, h)

	// A directory that is outside the repository, or not a clean relative path.
	for _, dir := range []string{
		"..", "../x", "stacks/../..", "/", "/stacks", "stacks/", "a//b", "a/./b", ".", ".git", "a/.GIT", `a\b`,
		"a\x00b", "a\nb",
	} {
		if _, err := h.Forge.Tree(ctx, h.Repo, c.ID, dir, 10); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Tree of the directory %q = %v, want ErrInvalid", dir, err)
		}
	}

	for _, id := range []string{"", "main", c.ID[:10], strings.ToUpper(c.ID)} {
		if _, err := h.Forge.Tree(ctx, h.Repo, id, "", 10); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Tree at %q = %v, want ErrInvalid", id, err)
		}
	}

	for _, limit := range []int{0, -1, forge.MaxTreeEntries + 1} {
		if _, err := h.Forge.Tree(ctx, h.Repo, c.ID, "", limit); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("Tree with the limit %d = %v, want ErrInvalid", limit, err)
		}
	}

	if after := callCount(ctx, t, h); after != before {
		t.Fatalf("an invalid tree read made %d calls to the forge, want none", after-before)
	}
}

func caseTreeNotFound(t *testing.T, h Harness) {
	t.Helper()

	ctx := t.Context()
	c := push(t, h, map[string][]byte{"stacks/prod.yaml": []byte("a")})

	if _, err := h.Forge.Tree(ctx, h.Repo, c.ID, "none", 10); !errors.Is(err, forge.ErrNotFound) {
		t.Errorf("Tree of a missing directory = %v, want ErrNotFound", err)
	}

	if _, err := h.Forge.Tree(ctx, h.Repo, c.ID, "stacks/prod.yaml", 10); !errors.Is(err, forge.ErrNotFound) {
		t.Errorf("Tree of a file = %v, want ErrNotFound", err)
	}

	if _, err := h.Forge.Tree(ctx, h.Repo, missingCommit(), "", 10); !errors.Is(err, forge.ErrNotFound) {
		t.Errorf("Tree at a missing commit = %v, want ErrNotFound", err)
	}
}
