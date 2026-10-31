package githubfake

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Errors of the fake that a test sees.
var (
	// ErrNoRepo reports a repository that the fake does not have.
	ErrNoRepo = errors.New("the fake GitHub host has no such repository")
	// ErrNoRef reports a branch, a tag or a commit that the repository does not have.
	ErrNoRef = errors.New("the fake GitHub host has no such branch, tag or commit")
	// ErrExists reports a repository, a branch or a tag that exists already.
	ErrExists = errors.New("the fake GitHub host has this name already")
)

// DefaultBranch is the default branch of each repository.
const DefaultBranch = "main"

// Commit is a commit of a repository of the fake.
type Commit struct {
	ID      string
	Parent  string
	Message string
	Time    time.Time
}

// commit is a commit with its files. The tree maps a path to a blob ID.
type commit struct {
	meta Commit
	tree map[string]string
}

// repo is one repository: its commits, blobs, branches and tags.
type repo struct {
	name      string
	commits   map[string]*commit
	blobs     map[string][]byte
	branches  map[string]string
	tags      map[string]string
	protected map[string]bool
}

// idLength is the number of hex digits in an ID of a blob or a commit. It
// is the length of a SHA-1 ID of git.
const idLength = 40

// digest returns an ID for parts: the first idLength hex digits of their
// SHA-256. An ID has the form of a git ID, but it is not the SHA-1 that git
// makes, so a test never depends on a real git hash.
func digest(parts ...string) string {
	h := sha256.New()
	writeAll(h, parts...)

	return hex.EncodeToString(h.Sum(nil))[:idLength]
}

// writeAll writes parts to h. The write of a hash does not fail.
func writeAll(h hash.Hash, parts ...string) {
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
	}
}

// BlobID returns the ID that the fake gives to a blob with content. It
// depends on the content only, so equal files have equal IDs.
func BlobID(content []byte) string {
	return digest("blob ", strconv.Itoa(len(content)), "\x00", string(content))
}

// partsPerPath is the number of parts that commitID writes for a file.
const partsPerPath = 4

// commitID returns a deterministic ID for a commit: it depends on the
// parent, the tree listing, the message and the time.
func commitID(parent string, tree map[string]string, message string, at time.Time) string {
	paths := slices.Sorted(maps.Keys(tree))
	head := []string{"parent ", parent, "\ntime ", strconv.FormatInt(at.Unix(), 10), "\n\n", message}
	parts := make([]string, len(head), len(head)+len(paths)*partsPerPath)
	copy(parts, head)

	for _, p := range paths {
		parts = append(parts, "\n", p, "\x00", tree[p])
	}

	return digest(parts...)
}

// AddRepo makes the repository fullName ("owner/name") with the branch main
// and one commit that holds files. It returns that commit.
func (f *Fake) AddRepo(fullName string, files map[string][]byte) (Commit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, found := f.repos[fullName]; found {
		return Commit{}, fmt.Errorf("%w: %s", ErrExists, fullName)
	}

	r := &repo{
		name:      fullName,
		commits:   map[string]*commit{},
		blobs:     map[string][]byte{},
		branches:  map[string]string{},
		tags:      map[string]string{},
		protected: map[string]bool{},
	}
	f.repos[fullName] = r

	c := r.commit("", files, "Initial commit", f.now())
	r.branches[DefaultBranch] = c.meta.ID

	return c.meta, nil
}

// commit adds a commit on top of parent. A nil value in files deletes the
// path. The caller holds the lock.
func (r *repo) commit(parent string, files map[string][]byte, message string, at time.Time) *commit {
	tree := map[string]string{}

	if p, found := r.commits[parent]; found {
		maps.Copy(tree, p.tree)
	}

	for path, content := range files {
		if content == nil {
			delete(tree, path)

			continue
		}

		id := BlobID(content)
		r.blobs[id] = slices.Clone(content)
		tree[path] = id
	}

	c := &commit{
		meta: Commit{ID: commitID(parent, tree, message, at), Parent: parent, Message: message, Time: at},
		tree: tree,
	}
	r.commits[c.meta.ID] = c

	return c
}

// Push commits files on branch with no check, as another author does, and
// returns the commit. A nil value deletes the file. A branch that does not
// exist is made from the head of the default branch.
func (f *Fake) Push(fullName, branch string, files map[string][]byte, message string) (Commit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return Commit{}, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	parent, found := r.branches[branch]
	if !found {
		parent = r.branches[DefaultBranch]
	}

	c := r.commit(parent, files, message, f.now())
	r.branches[branch] = c.meta.ID

	return c.meta, nil
}

// Tag makes the tag name at commitID.
func (f *Fake) Tag(fullName, name, commitID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	if _, found := r.commits[commitID]; !found {
		return fmt.Errorf("%w: %s", ErrNoRef, commitID)
	}

	if _, found := r.tags[name]; found {
		return fmt.Errorf("%w: tag %s", ErrExists, name)
	}

	r.tags[name] = commitID

	return nil
}

// Protect turns on branch protection for branch: a commit by the GraphQL
// mutation is refused.
func (f *Fake) Protect(fullName, branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	r.protected[branch] = true

	return nil
}

// Head returns the commit at the head of a branch or a tag.
func (f *Fake) Head(fullName, ref string) (Commit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return Commit{}, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	c, found := r.resolve(ref)
	if !found {
		return Commit{}, fmt.Errorf("%w: %s", ErrNoRef, ref)
	}

	return c.meta, nil
}

// minAbbrev is the shortest commit ID prefix that resolve accepts.
const minAbbrev = 7

// resolve finds a commit by branch, tag, full commit ID or the unique
// prefix of a commit ID. It never returns a nil commit with true.
func (r *repo) resolve(ref string) (*commit, bool) {
	for _, id := range []string{r.branches[ref], r.tags[ref], ref} {
		if c := r.commits[id]; c != nil {
			return c, true
		}
	}

	if len(ref) < minAbbrev || !isHex(ref) {
		return nil, false
	}

	var matches []*commit

	for id, c := range r.commits {
		if c != nil && strings.HasPrefix(id, ref) {
			matches = append(matches, c)
		}
	}

	if len(matches) != 1 {
		return nil, false
	}

	return matches[0], true
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return s != ""
}

// RenameRepo renames the repository from to to. A request for the old name
// answers a redirect (301) to the new one, as GitHub does.
func (f *Fake) RenameRepo(from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[from]
	if !found {
		return fmt.Errorf("%w: %s", ErrNoRepo, from)
	}

	if _, found := f.repos[to]; found {
		return fmt.Errorf("%w: %s", ErrExists, to)
	}

	delete(f.repos, from)
	r.name = to
	f.repos[to] = r
	f.moved[from] = to

	return nil
}
