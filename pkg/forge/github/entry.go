package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/forge"
)

// The modes of a tree entry that the adapter knows.
const (
	modeFile       = "100644"
	modeExecutable = "100755"
	modeSymlink    = "120000"
	modeSubmodule  = "160000"
	modeTree       = "040000"
	// modeBlocked marks a path that a file, a symlink or a submodule blocks
	// at one of its parent names. It is not a mode of GitHub.
	modeBlocked = "blocked"
)

// modeLength is the length of a mode of a tree entry: six octal digits.
const modeLength = 6

// maxTreeEntries is the most entries of one tree that the adapter reads.
const maxTreeEntries = 20000

// treeEntry is the entry of a path in the tree of a commit: the mode and the
// object ID. The zero value is a path that the commit does not have.
type treeEntry struct {
	mode string
	sha  string
}

// regularOrMissing reports whether the entry is a regular file (not a link,
// not a submodule, not a directory) or is missing.
func (e treeEntry) regularOrMissing() bool {
	return e == treeEntry{mode: "", sha: ""} || e.mode == modeFile || e.mode == modeExecutable
}

// errUnreadableTree reports a tree that the adapter does not read whole or
// does not trust. A caller counts it as a change.
var errUnreadableTree = errors.New("the tree cannot be read whole")

// unreadableTree reports whether err means that the adapter cannot compare
// the entry: a tree that is unreadable, or an object that GitHub does not have.
func unreadableTree(err error) bool {
	return errors.Is(err, errUnreadableTree) || errors.Is(err, forge.ErrNotFound)
}

// wireTree is the answer of the call that reads a tree.
type wireTree struct {
	Tree      []wireTreeEntry `json:"tree"`
	Truncated bool            `json:"truncated"`
}

type wireTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	SHA  string `json:"sha"`
}

// treeReader reads the trees of one repository and keeps them for one check.
type treeReader struct {
	f      *gitHub
	r      repoName
	bearer string
	trees  map[string]map[string]treeEntry
}

func newTreeReader(f *gitHub, r repoName, bearer string) *treeReader {
	return &treeReader{f: f, r: r, bearer: bearer, trees: map[string]map[string]treeEntry{}}
}

// entryAt returns the entry of filePath in the commit, or the zero entry when
// the commit has no such path. A parent name that is not a directory makes the
// entry modeBlocked. It reads one tree for each directory on the path.
func (t *treeReader) entryAt(ctx context.Context, commit, filePath string) (treeEntry, error) {
	// Defense in depth: checkChanges rejects these paths before a commit
	// request gets here. This check stays so that a new caller cannot make a
	// path with an empty, "." or ".." part read as missing on both sides,
	// which would count as unchanged. It does not rely on that check.
	parts := strings.Split(filePath, "/")
	if slices.ContainsFunc(parts, func(p string) bool { return p == "" || p == "." || p == ".." }) {
		return missingEntry(), fmt.Errorf("%w: the path has an empty, \".\" or \"..\" part", errUnreadableTree)
	}

	id := commit

	for i, name := range parts {
		entries, err := t.list(ctx, id)
		if err != nil {
			return missingEntry(), err
		}

		entry, found := entries[name]
		switch {
		case !found:
			return missingEntry(), nil
		case i == len(parts)-1:
			return entry, nil
		case entry.mode != modeTree:
			return treeEntry{mode: modeBlocked, sha: entry.sha}, nil
		}

		id = entry.sha
	}

	return missingEntry(), nil
}

// missingEntry returns the entry of a path that the commit does not have.
func missingEntry() treeEntry { return treeEntry{mode: "", sha: ""} }

// list returns the entries of the tree id by name. The id is a commit ID for
// the root tree, or the object ID of a tree. The answer is host data that is
// not trusted: the adapter checks every field, and a tree that fails a check,
// that is cut by GitHub, or that has a name twice is errUnreadableTree.
func (t *treeReader) list(ctx context.Context, id string) (map[string]treeEntry, error) {
	if cached, found := t.trees[id]; found {
		return cached, nil
	}

	if err := forge.ValidateCommitID(id); err != nil {
		return nil, fmt.Errorf("%w: the tree has an object ID that is not valid", errUnreadableTree)
	}

	resp, err := t.f.send(ctx, call{
		method: http.MethodGet, path: t.r.path() + "/git/trees/" + id, bearer: t.bearer, body: nil,
		status: t.f.commitStatus, oversize: errUnreadableTree, target: "",
	})
	if err != nil {
		return nil, err
	}

	var tree wireTree
	if decodeErr := decode(resp.body, &tree); decodeErr != nil || tree.Truncated || len(tree.Tree) > maxTreeEntries {
		return nil, fmt.Errorf("%w: GitHub cut the tree or gave a tree that does not parse", errUnreadableTree)
	}

	entries := make(map[string]treeEntry, len(tree.Tree))

	for _, e := range tree.Tree {
		if !validTreeEntry(e) {
			return nil, fmt.Errorf("%w: a tree entry is not valid", errUnreadableTree)
		}

		if _, twice := entries[e.Path]; twice {
			return nil, fmt.Errorf("%w: a tree has a name twice", errUnreadableTree)
		}

		entries[e.Path] = treeEntry{mode: e.Mode, sha: e.SHA}
	}

	t.trees[id] = entries

	return entries, nil
}

// validTreeEntry checks an entry of a tree with an allow list: a name of one
// part, a mode of six octal digits, and an object ID.
func validTreeEntry(e wireTreeEntry) bool {
	if e.Path == "" || e.Path == "." || e.Path == ".." || strings.Contains(e.Path, "/") || len(e.Mode) != modeLength {
		return false
	}

	for i := range len(e.Mode) {
		if e.Mode[i] < '0' || e.Mode[i] > '7' {
			return false
		}
	}

	return forge.ValidateCommitID(e.SHA) == nil
}
