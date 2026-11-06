package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Fields of an entry of a directory listing.
const (
	keyName = "name"
	keyPath = "path"
	keyType = "type"
	keySHA  = "sha"
	keySize = "size"
)

// githubListingCut is the number of entries at which GitHub cuts the listing of
// a directory with no sign. A listing of this size may be a part of the
// directory, so Tree refuses it.
const githubListingCut = 1000

// treeAllowed returns forge.ErrUnsupported for a connection that Capabilities
// does not list forge.CapTree for.
func (f *gitHub) treeAllowed() error {
	if !f.Capabilities().Has(forge.CapTree) {
		return fmt.Errorf("%w: GitHub Tree needs the permission to read the contents", forge.ErrUnsupported)
	}

	return nil
}

// Tree lists the direct children of the directory dir at commit.
//
// It returns the errors that forge.Files names for Tree.
func (f *gitHub) Tree(ctx context.Context, repo, commit, dir string, limit int) ([]forge.TreeEntry, error) {
	if err := f.treeAllowed(); err != nil {
		return nil, err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}

	if idErr := forge.ValidateCommitID(commit); idErr != nil {
		return nil, fmt.Errorf("the commit: %w", idErr)
	}

	if dir != "" {
		if pathErr := validateFilePath(dir); pathErr != nil {
			return nil, pathErr
		}
	}

	if limit < 1 || limit > forge.MaxTreeEntries {
		return nil, fmt.Errorf("%w: the limit of a tree read is 1 to %d", forge.ErrInvalid, forge.MaxTreeEntries)
	}

	bearer, err := f.readBearer(ctx, r)
	if err != nil {
		return nil, f.forget(r, err)
	}

	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/contents/" + escapePath(dir) + "?ref=" + commit, bearer: bearer,
		body: nil, status: f.notFound, oversize: nil, target: "",
	})
	if err != nil {
		return nil, f.forget(r, err)
	}

	// A file answers one object, not a list.
	if !strings.HasPrefix(strings.TrimSpace(string(resp.body)), "[") {
		return nil, fmt.Errorf("%w: the path is not a directory", forge.ErrNotFound)
	}

	return treeEntries(resp.body, dir, limit)
}

// treeEntries reads the listing of a directory and checks each entry.
func treeEntries(body []byte, dir string, limit int) ([]forge.TreeEntry, error) {
	var items []map[string]json.RawMessage
	if err := decode(body, &items); err != nil {
		return nil, err
	}

	if len(items) >= githubListingCut || len(items) > limit {
		return nil, fmt.Errorf(
			"%w: the directory has more than %d entries",
			forge.ErrTooLarge,
			min(limit, githubListingCut-1),
		)
	}

	entries := make([]forge.TreeEntry, 0, len(items))
	seen := make(map[string]bool, len(items))

	for _, item := range items {
		entry, err := treeEntryOf(item, dir)
		if err != nil {
			return nil, err
		}

		if seen[entry.Name] {
			return nil, forge.Unavailable(0, "GitHub gave a directory with the same name twice")
		}

		seen[entry.Name] = true
		entries = append(entries, entry)
	}

	slices.SortFunc(entries, func(a, b forge.TreeEntry) int { return strings.Compare(a.Name, b.Name) })

	return entries, nil
}

// treeEntryOf checks one entry of a listing of dir and maps it.
//
// Defense in depth: GitHub lists the children of the directory that the call
// names, and the caller of the port may check paths too. This check stays so
// that an entry outside dir, or a name that is a path, never reaches a caller.
func treeEntryOf(item map[string]json.RawMessage, dir string) (forge.TreeEntry, error) {
	bad := forge.Unavailable(0, "GitHub gave a directory entry that the adapter cannot use")
	empty := forge.TreeEntry{Path: "", Name: "", Kind: "", ID: "", Size: 0}

	name, path := textField(item, keyName), textField(item, keyPath)
	want := name

	if dir != "" {
		want = dir + "/" + name
	}

	if strings.Contains(name, "/") || validateFilePath(name) != nil || path != want {
		return empty, bad
	}

	kind, known := treeKindOf(textField(item, keyType))
	if !known {
		return empty, bad
	}

	id := textField(item, keySHA)
	if forge.ValidateCommitID(id) != nil {
		return empty, bad
	}

	var size int64
	if kind == forge.TreeFile && (json.Unmarshal(item[keySize], &size) != nil || size < 0) {
		return empty, bad
	}

	return forge.TreeEntry{Path: path, Name: name, Kind: kind, ID: id, Size: size}, nil
}

// treeKindOf maps the type of an entry of GitHub to a kind of the port.
func treeKindOf(kind string) (forge.TreeKind, bool) {
	switch kind {
	case "file":
		return forge.TreeFile, true
	case "dir":
		return forge.TreeDir, true
	case "symlink":
		return forge.TreeSymlink, true
	case "submodule":
		return forge.TreeSubmodule, true
	default:
		return "", false
	}
}
