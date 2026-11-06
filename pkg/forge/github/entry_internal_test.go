package github

import (
	"errors"
	"testing"
)

// TestEntryAtStopsHostilePaths sends paths with an empty, "." or ".." part to
// entryAt alone, as if the check of the commit request were not there. The
// reader holds an empty root tree, so that a path which passes the check reads
// as missing, which is the fail-open: both sides of the compare would be the
// same. Each path must be errUnreadableTree.
func TestEntryAtStopsHostilePaths(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"

	tests := []struct {
		name string
		path string
	}{
		{"an empty path", ""},
		{"a double slash", "a//b"},
		{"a dot part", "a/./b"},
		{"a dot-dot part", "a/../b"},
		{"a trailing slash", "a/"},
		{"a leading slash", "/a"},
		{"a dot", "."},
		{"two dots", ".."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newTreeReader(nil, repoName{owner: "acme", name: "infra"}, "")
			r.trees[commit] = map[string]treeEntry{}

			entry, err := r.entryAt(t.Context(), commit, tt.path)
			if !errors.Is(err, errUnreadableTree) || entry != missingEntry() {
				t.Fatalf("entryAt(%q) = %+v, %v; want errUnreadableTree", tt.path, entry, err)
			}
		})
	}
}

// TestEntryAtReadsAPathWithParts is the control of the test above: a clean
// path on the same reader reads as missing, with no error.
func TestEntryAtReadsAPathWithParts(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"

	r := newTreeReader(nil, repoName{owner: "acme", name: "infra"}, "")
	r.trees[commit] = map[string]treeEntry{}

	entry, err := r.entryAt(t.Context(), commit, "stacks/prod.yaml")
	if err != nil || entry != missingEntry() {
		t.Fatalf("entryAt = %+v, %v; want a missing entry", entry, err)
	}
}
