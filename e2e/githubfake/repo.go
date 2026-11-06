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

// repoIDBase is added to a counter to make the ID of a repository, so that an
// ID does not look like the ID of an account.
const repoIDBase = 700000

// Commit is a commit of a repository of the fake.
type Commit struct {
	ID      string
	Parent  string
	Message string
	Time    time.Time
	// Author and Committer are the logins that GitHub shows for the commit.
	// An empty login is an email that no account has.
	Author    string
	Committer string
	// Verified is whether GitHub shows the commit as verified.
	Verified bool
}

// Signature says who made a commit and whether GitHub verifies it.
type Signature struct {
	Author    string
	Committer string
	Verified  bool
}

// Logins that the fake gives to the makers of commits.
const (
	// WebFlow is the committer of a commit that GitHub makes itself, as the
	// mutation createCommitOnBranch does.
	WebFlow = "web-flow"
	// OtherAuthor is a person with push access who is not the connection.
	OtherAuthor = "other-dev"
)

// commit is a commit with its files. The tree maps a path to a blob ID, or to
// the commit ID of a submodule. The modes map a path to a mode that is not
// the mode of a regular file; a path with no mode in the map is a regular file.
type commit struct {
	meta  Commit
	tree  map[string]string
	modes map[string]string
}

// The modes of a tree entry that is not a regular file.
const (
	modeSymlink   = "120000"
	modeSubmodule = "160000"
	modeRegular   = "100644"
	modeTree      = "040000"
)

// modeOf returns the mode of the entry at path.
func (c *commit) modeOf(path string) string {
	if m, found := c.modes[path]; found {
		return m
	}

	return modeRegular
}

// repo is one repository: its commits, blobs, branches and tags.
type repo struct {
	name string
	// id is the number that GitHub gives to the repository. It stays the same
	// when the repository is renamed.
	id int64
	// defaultBranch is the default branch that the repository call answers.
	defaultBranch string
	pulls         []*pull
	commits       map[string]*commit
	blobs         map[string][]byte
	branches      map[string]string
	tags          map[string]string
	protected     map[string]bool
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

// commitIDWithModes is commitID for a tree whose entries have modes. A regular
// file adds nothing, so the ID of a tree of regular files is the same.
func commitIDWithModes(parent string, tree, modes map[string]string, message string, at time.Time) string {
	marked := maps.Clone(tree)

	for p, m := range modes {
		marked[p] += ":" + m
	}

	return commitID(parent, marked, message, at)
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
		name:          fullName,
		id:            f.nextRepoID(),
		defaultBranch: DefaultBranch,
		pulls:         nil,
		commits:       map[string]*commit{},
		blobs:         map[string][]byte{},
		branches:      map[string]string{},
		tags:          map[string]string{},
		protected:     map[string]bool{},
	}
	f.repos[fullName] = r

	c := r.commit("", files, "Initial commit", f.now())
	r.branches[DefaultBranch] = c.meta.ID

	return c.meta, nil
}

// CloneRepo makes the repository dst ("owner/name") with every commit, blob,
// branch and tag of the repository src and a new repository ID, as a copy of a
// repository does before anyone commits to it.
func (f *Fake) CloneRepo(src, dst string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	from, found := f.repos[src]
	if !found {
		return fmt.Errorf("%w: %s", ErrNoRepo, src)
	}

	if _, found = f.repos[dst]; found {
		return fmt.Errorf("%w: %s", ErrExists, dst)
	}

	f.repos[dst] = &repo{
		name:          dst,
		id:            f.nextRepoID(),
		defaultBranch: from.defaultBranch,
		pulls:         nil,
		commits:       maps.Clone(from.commits),
		blobs:         maps.Clone(from.blobs),
		branches:      maps.Clone(from.branches),
		tags:          maps.Clone(from.tags),
		protected:     maps.Clone(from.protected),
	}

	return nil
}

// commit adds a commit on top of parent. A nil value in files deletes the
// path. The caller holds the lock.
func (r *repo) commit(parent string, files map[string][]byte, message string, at time.Time) *commit {
	return r.commitChange(
		parent,
		change{files: files, entries: nil},
		message,
		at,
		Signature{Author: "", Committer: "", Verified: false},
	)
}

// entrySpec is an entry that is not a regular file.
type entrySpec struct {
	mode string
	id   string
}

// change is what a commit changes: regular files (a nil value deletes the
// path) and entries that are not regular files.
type change struct {
	files   map[string][]byte
	entries map[string]entrySpec
}

// place sets the entry at path. As git does, it removes a file at a parent
// name of path and every entry below path.
func (c *commit) place(path, id, mode string) {
	for parent := path; strings.Contains(parent, "/"); {
		parent = parent[:strings.LastIndex(parent, "/")]
		c.remove(parent)
	}

	for p := range c.tree {
		if strings.HasPrefix(p, path+"/") {
			c.remove(p)
		}
	}

	c.tree[path] = id

	if mode != modeRegular {
		c.modes[path] = mode
	} else {
		delete(c.modes, path)
	}
}

// remove deletes the entry at path.
func (c *commit) remove(path string) {
	delete(c.tree, path)
	delete(c.modes, path)
}

// commitChange adds a commit on top of parent. The caller holds the lock.
func (r *repo) commitChange(parent string, ch change, message string, at time.Time, sig Signature) *commit {
	c := &commit{meta: Commit{
		ID: "", Parent: "", Message: "", Time: time.Time{}, Author: "", Committer: "", Verified: false,
	}, tree: map[string]string{}, modes: map[string]string{}}

	if p, found := r.commits[parent]; found {
		maps.Copy(c.tree, p.tree)
		maps.Copy(c.modes, p.modes)
	}

	for _, path := range slices.Sorted(maps.Keys(ch.files)) {
		content := ch.files[path]
		if content == nil {
			c.remove(path)

			continue
		}

		id := BlobID(content)
		r.blobs[id] = slices.Clone(content)
		c.place(path, id, modeRegular)
	}

	for _, path := range slices.Sorted(maps.Keys(ch.entries)) {
		c.place(path, ch.entries[path].id, ch.entries[path].mode)
	}

	c.meta = Commit{
		ID: commitIDWithModes(parent, c.tree, c.modes, message, at), Parent: parent, Message: message, Time: at,
		Author: sig.Author, Committer: sig.Committer, Verified: sig.Verified,
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

	c := r.commitChange(
		parent, change{files: files, entries: nil}, message, f.now(),
		Signature{Author: OtherAuthor, Committer: OtherAuthor, Verified: false},
	)
	r.branches[branch] = c.meta.ID

	return c.meta, nil
}

// PushSigned is Push with a message and a signature that the test chooses, so
// that a commit can pose as a commit of the connection or of GitHub.
func (f *Fake) PushSigned(
	fullName, branch string, files map[string][]byte, message string, sig Signature,
) (Commit, error) {
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

	c := r.commitChange(parent, change{files: files, entries: nil}, message, f.now(), sig)
	r.branches[branch] = c.meta.ID

	return c.meta, nil
}

// EntryKind is the kind of an entry that is not a regular file.
type EntryKind int

// The kinds of entry that PushEntry makes.
const (
	// Symlink is a symbolic link. The variant is its target.
	Symlink EntryKind = iota + 1
	// Submodule is a submodule. The variant picks its commit.
	Submodule
	// Directory is a directory at the path, with one file named by the variant.
	Directory
)

// PushEntry commits an entry that is not a regular file at path on branch
// with no check, as another author does, and returns the commit. A file or an
// entry at path is replaced. The variant makes two entries of one kind differ.
func (f *Fake) PushEntry(fullName, branch, path string, kind EntryKind, variant string) (Commit, error) {
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

	ch := change{files: nil, entries: nil}

	switch kind {
	case Symlink:
		id := BlobID([]byte(variant))
		r.blobs[id] = []byte(variant)
		ch.entries = map[string]entrySpec{path: {mode: modeSymlink, id: id}}
	case Submodule:
		ch.entries = map[string]entrySpec{path: {mode: modeSubmodule, id: digest("submodule ", variant)}}
	case Directory:
		ch.files = map[string][]byte{path + "/" + variant: []byte(variant + "\n")}
	default:
		return Commit{}, fmt.Errorf("%w: entry kind %d", ErrNoRef, kind)
	}

	c := r.commitChange(
		parent, ch, "Push an entry to "+branch, f.now(),
		Signature{Author: OtherAuthor, Committer: OtherAuthor, Verified: false},
	)
	r.branches[branch] = c.meta.ID

	return c.meta, nil
}

// Rewrite replaces the history of branch with one commit that has no parent
// and holds exactly files, as a force push does, and returns that commit. The
// old commits stay in the repository. A branch that does not exist is made.
func (f *Fake) Rewrite(fullName, branch string, files map[string][]byte, message string) (Commit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return Commit{}, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	c := r.commit("", files, message, f.now())
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
