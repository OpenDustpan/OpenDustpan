package githubfake

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// The types of a tree entry in the answer.
const (
	typeBlob   = "blob"
	typeTree   = "tree"
	typeCommit = "commit"
)

// dirEntriesOf lists the direct children of the directory dir ("" is the
// root) of the commit with their modes and object IDs, in the order of their
// names. A directory has the mode 040000 and the ID of its own listing.
func dirEntriesOf(c *commit, dir string) []wireTreeEntry {
	prefix := dir
	if prefix != "" {
		prefix += "/"
	}

	children := map[string]bool{}

	for p := range c.tree {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			name, _, nested := strings.Cut(rest, "/")
			children[name] = nested
		}
	}

	out := make([]wireTreeEntry, 0, len(children))

	for _, name := range slices.Sorted(maps.Keys(children)) {
		if children[name] {
			out = append(out, wireTreeEntry{Path: name, Mode: modeTree, Type: typeTree, SHA: treeID(c, prefix+name)})

			continue
		}

		mode, kind := c.modeOf(prefix+name), typeBlob
		if mode == modeSubmodule {
			kind = typeCommit
		}

		out = append(out, wireTreeEntry{Path: name, Mode: mode, Type: kind, SHA: c.tree[prefix+name]})
	}

	return out
}

// treeID returns the object ID of the directory dir of the commit. It depends
// on the listing of the directory only, so equal directories have equal IDs.
func treeID(c *commit, dir string) string {
	entries := dirEntriesOf(c, dir)
	parts := make([]string, 1, 1+len(entries)*partsPerTreeEntry)
	parts[0] = "tree"

	for _, e := range entries {
		parts = append(parts, "\n", e.Mode, " ", e.Path, "\x00", e.SHA)
	}

	return digest(parts...)
}

// partsPerTreeEntry is the number of parts that treeID writes for an entry.
const partsPerTreeEntry = 6

// directoriesOf returns the directories of the commit, with the root "".
func directoriesOf(c *commit) map[string]bool {
	dirs := map[string]bool{"": true}

	for p := range c.tree {
		for i := range len(p) {
			if p[i] == '/' {
				dirs[p[:i]] = true
			}
		}
	}

	return dirs
}

// findTree returns the listing of the tree with the ID id: the root tree of a
// commit when id is a commit ID, or a directory of a commit when id is a tree ID.
func (r *repo) findTree(id string) ([]wireTreeEntry, bool) {
	if c := r.commits[id]; c != nil {
		return dirEntriesOf(c, ""), true
	}

	for _, c := range r.commits {
		for dir := range directoriesOf(c) {
			if treeID(c, dir) == id {
				return dirEntriesOf(c, dir), true
			}
		}
	}

	return nil, false
}

// getTree answers the call that reads one tree, with no recursion. The
// answer holds the mode and the object ID of each entry.
func (f *Fake) getTree(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	id := r.PathValue("sha")

	entries, found := rp.findTree(id)
	if !found {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	f.writeJSON(w, http.StatusOK, wireTree{SHA: id, Tree: entries, Truncated: false})
}

// getApp answers the call that reads the app of a JWT, with the slug.
func (f *Fake) getApp(w http.ResponseWriter, r *http.Request) {
	if !f.authorize(w, r, BearerApp) {
		return
	}

	f.writeJSON(w, http.StatusOK, wireApp{ID: json.Number(f.cfg.AppID), Slug: f.cfg.AppSlug})
}
