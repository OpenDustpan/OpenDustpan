package github_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
)

// entryJSON returns an entry of a directory listing.
func entryJSON(name, path, kind, sha string, size int) map[string]any {
	return map[string]any{"name": name, "path": path, "type": kind, "sha": sha, "size": size}
}

func treeAt(dir string) string {
	return "GET /repos/acme/infra/contents/" + dir + "?ref=" + headSHA
}

// TestTreeRefusesAnEntryItCannotUse checks that a listing with an entry outside
// the directory, a name that is a path, or a value that fails a check is
// ErrUnavailable and never a list.
func TestTreeRefusesAnEntryItCannotUse(t *testing.T) {
	t.Parallel()

	ok := entryJSON("a.txt", "stacks/a.txt", "file", baseSHA, 3)

	tests := []struct {
		name  string
		items []any
	}{
		{"a path outside the directory", []any{entryJSON("a.txt", "other/a.txt", "file", baseSHA, 3)}},
		{"a path that leaves the repository", []any{entryJSON("a.txt", "stacks/../a.txt", "file", baseSHA, 3)}},
		{"a name that is a path", []any{entryJSON("x/a.txt", "stacks/x/a.txt", "file", baseSHA, 3)}},
		{"a name of two dots", []any{entryJSON("..", "stacks/..", "dir", baseSHA, 0)}},
		{"a name in .git", []any{entryJSON(".git", "stacks/.git", "dir", baseSHA, 0)}},
		{"no name", []any{entryJSON("", "stacks/", "file", baseSHA, 3)}},
		{"a kind that is not known", []any{entryJSON("a.txt", "stacks/a.txt", "device", baseSHA, 3)}},
		{"an object ID that is not valid", []any{entryJSON("a.txt", "stacks/a.txt", "file", "xyz", 3)}},
		{"a size below zero", []any{entryJSON("a.txt", "stacks/a.txt", "file", baseSHA, -1)}},
		{"the same name twice", []any{ok, ok}},
		{"an entry that is not an object", []any{"a.txt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := cannedForge(t, map[string]hostAnswer{treeAt("stacks"): okAnswer(tt.items)})

			got, err := f.Tree(t.Context(), "acme/infra", headSHA, "stacks", 10)
			if !errors.Is(err, forge.ErrUnavailable) || len(got) != 0 {
				t.Fatalf("Tree = %+v, %v; want ErrUnavailable and no entry", got, err)
			}
		})
	}
}

func TestTreeRoot(t *testing.T) {
	t.Parallel()

	f := cannedForge(t, map[string]hostAnswer{
		"GET /repos/acme/infra/contents/?ref=" + headSHA: okAnswer([]any{
			entryJSON("z.txt", "z.txt", "file", baseSHA, 1), entryJSON("a", "a", "dir", baseSHA, 0),
		}),
	})

	got, err := f.Tree(t.Context(), "acme/infra", headSHA, "", 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("Tree = %+v, %v", got, err)
	}

	// The entries are in the order of the names.
	if got[0].Name != "a" || got[0].Kind != forge.TreeDir || got[1].Path != "z.txt" || got[1].Size != 1 {
		t.Fatalf("Tree = %+v", got)
	}
}

func TestTreeOfAFile(t *testing.T) {
	t.Parallel()

	f := cannedForge(t, map[string]hostAnswer{
		treeAt("stacks/a.txt"): okAnswer(entryJSON("a.txt", "stacks/a.txt", "file", baseSHA, 3)),
	})

	_, err := f.Tree(t.Context(), "acme/infra", headSHA, "stacks/a.txt", 10)
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Tree of a file = %v, want ErrNotFound", err)
	}
}

func TestTreeLimit(t *testing.T) {
	t.Parallel()

	items := make([]any, 0, 3)

	for i := range 3 {
		name := fmt.Sprintf("f%d", i)
		items = append(items, entryJSON(name, "d/"+name, "file", baseSHA, 1))
	}

	f := cannedForge(t, map[string]hostAnswer{treeAt("d"): okAnswer(items)})

	got, err := f.Tree(t.Context(), "acme/infra", headSHA, "d", 2)
	if !errors.Is(err, forge.ErrTooLarge) || len(got) != 0 {
		t.Fatalf("Tree above the limit = %+v, %v; want ErrTooLarge and no entry", got, err)
	}

	got, err = f.Tree(t.Context(), "acme/infra", headSHA, "d", 3)
	if err != nil || len(got) != 3 {
		t.Fatalf("Tree at the limit = %d entries, %v; want 3", len(got), err)
	}
}

// TestTreeCutByGitHub checks that a listing of 1000 entries, where GitHub cuts
// a longer one with no sign, is refused.
func TestTreeCutByGitHub(t *testing.T) {
	t.Parallel()

	items := make([]any, 0, 1000)

	for i := range 1000 {
		name := fmt.Sprintf("f%04d", i)
		items = append(items, entryJSON(name, "d/"+name, "file", baseSHA, 1))
	}

	f := cannedForge(t, map[string]hostAnswer{treeAt("d"): okAnswer(items)})

	got, err := f.Tree(t.Context(), "acme/infra", headSHA, "d", forge.MaxTreeEntries)
	if !errors.Is(err, forge.ErrTooLarge) || len(got) != 0 {
		t.Fatalf("Tree of 1000 entries = %d entries, %v; want ErrTooLarge", len(got), err)
	}
}

func TestTreeMissingDirectory(t *testing.T) {
	t.Parallel()

	f := cannedForge(t, map[string]hostAnswer{treeAt("none"): statusAnswer(404, `{"message":"Not Found"}`)})

	_, err := f.Tree(t.Context(), "acme/infra", headSHA, "none", 10)
	if !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("Tree of a missing directory = %v, want ErrNotFound", err)
	}

	if strings.Contains(err.Error(), cannedPAT) {
		t.Fatalf("the error holds the token: %v", err)
	}
}
