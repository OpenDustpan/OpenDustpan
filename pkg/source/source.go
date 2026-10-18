// Package source defines the source pipeline of runner jobs. The server
// gets the code of a job from the forge as a raw git pack, for one commit,
// and streams it to the blob store as a source snapshot. The launcher
// reads the snapshot through its job session, checks that the pack holds
// the commit and every object that the commit reaches, and writes the
// files of the commit to its work directory. No runner connects to the
// forge for the code, and no part of the pipeline uses a git library with
// a work tree: the server speaks the git protocol for one fetch, and the
// launcher reads the pack format.
//
// Submodules and Git LFS objects are part of a snapshot only when the
// source policy of the namespace allows their URL. The server gets them
// too.
//
// The package is tool-neutral. It never imports a transport, a store
// adapter or a forge adapter.
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	// ErrInvalid reports a request or a policy with an empty or bad field.
	ErrInvalid = errors.New("source request is not valid")
	// ErrNotFound reports a snapshot that the store does not know.
	ErrNotFound = errors.New("source snapshot not found")
	// ErrNotReady reports a snapshot whose fetch has not ended. The
	// launcher asks again after a delay.
	ErrNotReady = errors.New("source snapshot is not ready")
	// ErrFetchFailed reports a fetch that the forge refused or that failed
	// for good, for example an unknown commit. Its message names the
	// reason.
	ErrFetchFailed = errors.New("fetch of the source failed")
	// ErrNotAllowed reports a submodule or a Git LFS object whose URL the
	// source policy does not allow. The snapshot does not hold it, and the
	// fetch fails when the policy says so.
	ErrNotAllowed = errors.New("the source policy does not allow this URL")
	// ErrCorrupt reports a pack whose hash, objects or commit do not match
	// the snapshot. The launcher stops before the first step.
	ErrCorrupt = errors.New("source snapshot does not match its commit")
	// ErrTooLarge reports a pack, a file or a checkout above a limit of this
	// package.
	ErrTooLarge = errors.New("source is too large")
)

// KindFetch is the durable kind of a fetch. The job store starts one
// operation of the kind in the transaction of the start of a runner job,
// with the start key SnapshotKey, so two jobs on one commit with one
// policy share one fetch.
const KindFetch durable.Kind = "source.fetch"

// Limits of a snapshot. Installation settings can change them later.
const (
	// MaxPack is the largest pack of one repository.
	MaxPack = 2 << 30
	// MaxCheckout is the largest sum of the sizes of the files of a
	// checkout, so that a small pack of large repeated blobs cannot fill a
	// disk.
	MaxCheckout = 8 << 30
	// MaxFiles is the largest number of files of a checkout.
	MaxFiles = 500_000
	// MaxSubmodules is the largest number of submodules of a snapshot.
	MaxSubmodules = 64
	// MaxLargeFiles is the largest number of Git LFS objects of a
	// snapshot.
	MaxLargeFiles = 10_000
	// MaxLargeFile is the largest Git LFS object.
	MaxLargeFile = 2 << 30
	// Retention is the time that the server keeps a snapshot after the
	// last job that names it ends.
	Retention = 7 * 24 * time.Hour
)

// ObjectFormat is the hash of the objects of a repository.
type ObjectFormat string

// The object formats of git.
const (
	FormatSHA1   ObjectFormat = "sha1"
	FormatSHA256 ObjectFormat = "sha256"
)

// ValidCommit reports whether c is a commit ID of the format f: 40 or 64
// lowercase hex characters.
func ValidCommit(f ObjectFormat, c string) bool {
	switch f {
	case FormatSHA1:
		return hexPattern.MatchString(c) && len(c) == 40
	case FormatSHA256:
		return hexPattern.MatchString(c) && len(c) == 64
	default:
		return false
	}
}

var hexPattern = regexp.MustCompile(`^[0-9a-f]+$`)

// Request asks for a snapshot of one commit of one repository of a forge
// connection.
type Request struct {
	Namespace  namespace.Name
	Connection string
	Repository string
	Commit     string
	Format     ObjectFormat
	Policy     Policy
}

// Validate returns ErrInvalid for an empty field, a commit that is not
// valid for the format, or a policy that fails Policy.Validate.
func (r *Request) Validate() error {
	switch {
	case r.Namespace == "" || r.Connection == "" || r.Repository == "":
		return fmt.Errorf("%w: a snapshot needs a namespace, a connection and a repository", ErrInvalid)
	case !ValidCommit(r.Format, r.Commit):
		return fmt.Errorf("%w: commit %q is not a %s commit ID", ErrInvalid, r.Commit, r.Format)
	default:
		return r.Policy.Validate()
	}
}

// SnapshotKey returns the durable start key of the fetch of r, and the key
// of its snapshot: the namespace, the connection, the repository, the
// commit and the hash of the policy. A change of the policy makes a new
// snapshot.
func SnapshotKey(r *Request) string {
	return strings.Join([]string{
		string(r.Namespace), r.Connection, r.Repository, r.Commit, r.Policy.Hash(),
	}, "/")
}

// Snapshot is the record of the code of one commit in the blob store.
type Snapshot struct {
	// ID has the prefix SnapshotIDPrefix.
	ID      string
	Request Request
	// PackSHA256 is the hex SHA-256 of the pack blob, and PackSize its
	// size.
	PackSHA256 string
	PackSize   int64
	Submodules []Submodule
	LargeFiles []LargeFile
	// Ready is true when the fetch ended with success. Failed is the reason
	// of a fetch that failed for good.
	Ready     bool
	Failed    string
	CreatedAt time.Time
	// ExpiresAt is Retention after the end of the last job that names the
	// snapshot. A sweep deletes the blobs after it.
	ExpiresAt time.Time
}

// SnapshotIDPrefix starts the ID of a snapshot.
const SnapshotIDPrefix = "src-"

// Submodule is one submodule in a snapshot: the path in the commit, the
// commit that the superproject pins, and its own pack.
type Submodule struct {
	Path       string
	URL        string
	Commit     string
	PackSHA256 string
	PackSize   int64
}

// LargeFile is one Git LFS object in a snapshot.
type LargeFile struct {
	// OID is the hex SHA-256 of the content.
	OID  string
	Size int64
}

// PackKey returns the blob key of the pack of the repository of the
// snapshot id.
func PackKey(id string) (blob.Key, error) {
	return blob.ParseKey("source/" + id + "/pack")
}

// SubmodulePackKey returns the blob key of the pack of the submodule at
// index i of Snapshot.Submodules of the snapshot id. The index, not the
// path, names the blob, because a path can hold characters that a blob key
// cannot.
func SubmodulePackKey(id string, i int) (blob.Key, error) {
	return blob.ParseKey("source/" + id + "/submodules/" + strconv.Itoa(i) + "/pack")
}

// LargeFileKey returns the blob key of the Git LFS object oid of the
// snapshot id.
func LargeFileKey(id, oid string) (blob.Key, error) {
	return blob.ParseKey("source/" + id + "/lfs/" + oid)
}

// Fetcher is the port of the server to the git protocol of a forge. The
// adapter speaks the git protocol over HTTPS with the credentials of the
// forge connection, and asks for one commit with depth 1. It never writes
// a work tree.
type Fetcher interface {
	// Fetch writes the raw pack of the commit of r, with every object that
	// the commit reaches, to w, and returns its size, its hex SHA-256 and
	// the object format. It returns ErrFetchFailed for a commit or a
	// repository that the forge does not have or does not give, and
	// ErrTooLarge after MaxPack bytes. An error that can pass, for example
	// a timeout, wraps neither, and the durable fetch retries it.
	Fetch(ctx context.Context, r Request, w io.Writer) (size int64, sha256 string, err error)
	// Submodules reads the .gitmodules file and the gitlinks of the commit
	// of r from the pack in p, and returns the submodules with their URL
	// and pinned commit.
	Submodules(ctx context.Context, r Request, p io.ReaderAt, size int64) ([]Submodule, error)
	// LargeFiles reads the Git LFS pointer files of the commit of r from
	// the pack in p, and returns their objects.
	LargeFiles(ctx context.Context, r Request, p io.ReaderAt, size int64) ([]LargeFile, error)
	// LargeFile writes the Git LFS object oid of the repository of r to w.
	// It returns ErrCorrupt when the content does not have the hash oid or
	// the size of the pointer.
	LargeFile(ctx context.Context, r Request, oid string, size int64, w io.Writer) error
}

// Store is the persistence port for snapshots. A snapshot is data of its
// namespace, so every write checks the fence. The blobs of a snapshot are
// in the blob store under PackKey, SubmodulePackKey and LargeFileKey.
type Store interface {
	// Begin returns the snapshot of r, and creates it, not ready, when it
	// does not exist. Two calls with one SnapshotKey give one snapshot.
	Begin(ctx context.Context, r Request, fence namespace.Fence) (Snapshot, error)
	// Complete records the end of a fetch: the hash and the size of the
	// pack, the submodules and the Git LFS objects. It sets Ready. It
	// returns ErrNotFound for an unknown snapshot. A second Complete with
	// the same values changes nothing.
	Complete(ctx context.Context, s Snapshot, fence namespace.Fence) error
	// Fail records a fetch that failed for good, with its reason. The jobs
	// that name the snapshot then fail their phase with the reason.
	Fail(ctx context.Context, id, reason string, fence namespace.Fence) error
	// Get returns the snapshot id, or ErrNotFound.
	Get(ctx context.Context, id string) (Snapshot, error)
}

// Checkout is the port of the launcher to its work directory. The
// adapter reads the pack format and writes files. It never starts git.
type Checkout interface {
	// Write reads the pack in p, checks it, and writes the tree of the
	// commit to dir. It checks, before it writes a file:
	//   - the SHA-256 of the pack is the hash of the snapshot,
	//   - the hash of each object is its ID, with the object format of the
	//     snapshot, after the resolution of each delta,
	//   - the commit of the snapshot is in the pack, and every tree and
	//     blob that it reaches is in the pack,
	//   - no path of the tree has a part "..", ".git" in any case, or a NUL
	//     byte, and no path leaves dir,
	//   - a symbolic link points inside dir.
	//
	// It returns ErrCorrupt for a check that fails, and ErrTooLarge above
	// MaxCheckout or MaxFiles. A submodule writes at its path in the same
	// way. It writes no .git directory.
	Write(ctx context.Context, s Snapshot, p io.ReaderAt, size int64, dir string) error
}

// Policy is the source policy of a namespace: which submodules and which
// Git LFS objects a snapshot may hold. By default a snapshot holds
// neither.
type Policy struct {
	// Submodules lists the URL patterns of the submodules that the server
	// gets. A submodule whose URL matches no pattern is not in the
	// snapshot.
	Submodules []URLPattern
	// LargeFiles lists the URL patterns of the repositories whose Git LFS
	// objects the server gets.
	LargeFiles []URLPattern
	// FailOnDenied fails the fetch when the commit has a submodule or a Git
	// LFS pointer that the policy does not allow. With false, the snapshot
	// holds the gitlink or the pointer file as it is.
	FailOnDenied bool
}

// MaxPatterns bounds each list of a policy.
const MaxPatterns = 64

// Validate returns ErrInvalid for more than MaxPatterns patterns in a list,
// or a pattern that URLPattern.Validate refuses.
func (p *Policy) Validate() error {
	for _, list := range [][]URLPattern{p.Submodules, p.LargeFiles} {
		if len(list) > MaxPatterns {
			return fmt.Errorf("%w: a policy list has at most %d patterns", ErrInvalid, MaxPatterns)
		}

		for _, u := range list {
			if err := u.Validate(); err != nil {
				return err
			}
		}
	}

	return nil
}

// Hash returns a text that changes when the policy changes. It is the
// patterns of each list in order, with the switch.
func (p *Policy) Hash() string {
	parts := make([]string, 0, len(p.Submodules)+len(p.LargeFiles))
	parts = append(parts, "s")

	for _, u := range p.Submodules {
		parts = append(parts, string(u))
	}

	parts = append(parts, "l")

	for _, u := range p.LargeFiles {
		parts = append(parts, string(u))
	}

	parts = append(parts, strconv.FormatBool(p.FailOnDenied))

	return strconv.Quote(strings.Join(parts, " "))
}

// AllowsSubmodule reports whether the policy allows the submodule URL u.
func (p *Policy) AllowsSubmodule(u string) bool {
	return matchAny(p.Submodules, u)
}

// AllowsLargeFiles reports whether the policy allows the Git LFS objects
// of the repository URL u.
func (p *Policy) AllowsLargeFiles(u string) bool {
	return matchAny(p.LargeFiles, u)
}

// matchAny reports whether a pattern of list matches u.
func matchAny(list []URLPattern, u string) bool {
	for _, p := range list {
		if p.Match(u) {
			return true
		}
	}

	return false
}

// URLPattern is an https URL of a repository, with an optional "*" as the
// last path part, which matches one path part. For example
// "https://git.example.com/platform/*" matches
// "https://git.example.com/platform/modules" and
// "https://git.example.com/platform/modules.git", not
// "https://git.example.com/platform/a/b".
type URLPattern string

// Validate returns ErrInvalid for a pattern that is not an https URL with a
// host and a path, that has a user, a query, a fragment or a port, or that
// has a "*" that is not the whole last path part.
func (u URLPattern) Validate() error {
	h, ok := splitHTTPS(string(u))
	if !ok || h.host == "" || len(h.parts) == 0 {
		return fmt.Errorf("%w: pattern %q must be an https URL of a repository", ErrInvalid, u)
	}

	for i, p := range h.parts {
		if p == "" || (strings.Contains(p, "*") && (p != "*" || i != len(h.parts)-1)) {
			return fmt.Errorf("%w: in pattern %q, '*' can only be the whole last path part", ErrInvalid, u)
		}
	}

	return nil
}

// Match reports whether the repository URL target matches u. The host
// matches with no regard to case, the path parts exactly, and a ".git" at
// the end of the target does not count.
func (u URLPattern) Match(target string) bool {
	p, ok := splitHTTPS(string(u))
	t, ok2 := splitHTTPS(target)

	if !ok || !ok2 || !strings.EqualFold(p.host, t.host) || len(p.parts) != len(t.parts) {
		return false
	}

	for i, want := range p.parts {
		got := t.parts[i]
		if i == len(p.parts)-1 {
			got = strings.TrimSuffix(got, ".git")
			want = strings.TrimSuffix(want, ".git")
		}

		if !matchPart(want, got) {
			return false
		}
	}

	return true
}

// matchPart reports whether the path part got matches the pattern part
// want: "*" matches one part that is not empty, "." or "..".
func matchPart(want, got string) bool {
	if want == "*" {
		return got != "" && got != "." && got != ".."
	}

	return want == got
}

// httpsURL is an https URL split into its host and its path parts.
type httpsURL struct {
	host  string
	parts []string
}

// splitHTTPS splits an https URL with no user, port, query or fragment into
// its host and its path parts.
func splitHTTPS(s string) (httpsURL, bool) {
	rest, found := strings.CutPrefix(s, "https://")
	if !found || strings.ContainsAny(rest, "@?#:\\ ") {
		return httpsURL{host: "", parts: nil}, false
	}

	host, p, _ := strings.Cut(rest, "/")
	p = strings.TrimSuffix(p, "/")

	if p == "" {
		return httpsURL{host: host, parts: nil}, true
	}

	return httpsURL{host: host, parts: strings.Split(p, "/")}, true
}

// lfsPointer is the first line of a Git LFS pointer file.
const lfsPointer = "version https://git-lfs.github.com/spec/v1"

// maxPointer is the largest Git LFS pointer file.
const maxPointer = 1024

// ParsePointer parses a Git LFS pointer file. It returns the object and
// true for a valid pointer: the version line first, then the keys in
// order of their names, with "oid sha256:<64 hex>" and "size <bytes>". It
// returns false for any other content, which is an ordinary file.
func ParsePointer(b []byte) (LargeFile, bool) {
	none := LargeFile{OID: "", Size: 0}

	if len(b) > maxPointer || !strings.HasSuffix(string(b), "\n") {
		return none, false
	}

	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) < minPointerLines || lines[0] != lfsPointer {
		return none, false
	}

	keys, ok := pointerKeys(lines[1:])
	if !ok {
		return none, false
	}

	oid, ok := strings.CutPrefix(keys["oid"], "sha256:")
	if !ok || len(oid) != sha256Hex || !hexPattern.MatchString(oid) {
		return none, false
	}

	size, err := strconv.ParseInt(keys["size"], 10, 64)
	if err != nil || size < 0 || strconv.FormatInt(size, 10) != keys["size"] {
		return none, false
	}

	return LargeFile{OID: oid, Size: size}, true
}

// pointerKeys returns the keys and values of the lines of a pointer file
// after its version line. It returns false for a line with no space, or a
// key that is not after the key before it.
func pointerKeys(lines []string) (map[string]string, bool) {
	keys := make(map[string]string, len(lines))
	last := ""

	for _, line := range lines {
		key, val, found := strings.Cut(line, " ")
		if !found || key <= last {
			return nil, false
		}

		keys[key], last = val, key
	}

	return keys, true
}

// minPointerLines is the version line, the oid and the size.
const minPointerLines = 3

// sha256Hex is the length of a SHA-256 in hex.
const sha256Hex = 64
