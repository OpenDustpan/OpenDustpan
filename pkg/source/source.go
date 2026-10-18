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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
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
	// snapshot. The server deletes the blobs after it.
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
	Fetch(ctx context.Context, r Request, w io.Writer) (size int64, sum string, err error)
	// Submodules reads the .gitmodules file and the gitlinks of the commit
	// of r from the pack in p, and returns the submodules with their URL
	// and pinned commit. A relative URL resolves against the URL of the
	// repository of r first (ResolveURL), so the policy matches the URL
	// that the server fetches.
	//
	// The adapter that fetches a submodule follows no redirect to another
	// host. It sends the credentials of the forge connection only to the
	// host of the connection (SameHost), and fetches a submodule of another
	// host with no credentials.
	Submodules(ctx context.Context, r Request, p io.ReaderAt, size int64) ([]Submodule, error)
	// LargeFiles reads the Git LFS pointer files of the commit of r from
	// the pack in p, and returns their objects.
	LargeFiles(ctx context.Context, r Request, p io.ReaderAt, size int64) ([]LargeFile, error)
	// LargeFile writes the Git LFS object oid of the repository of r to w.
	// It gets the object only from the standard Git LFS endpoint of the
	// repository on the forge (LFSEndpoint), never from an lfs.url of a
	// .lfsconfig file in the commit. It returns ErrCorrupt when the
	// content does not have the hash oid or the size of the pointer
	// (CheckLargeFile).
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
	// MaxCheckout or MaxFiles. It writes no .git directory.
	Write(ctx context.Context, s Snapshot, p io.ReaderAt, size int64, dir string) error
	// Submodule reads the pack of the submodule at index i of s.Submodules
	// in p, and checks it as Write does. It also checks that the commit of
	// the submodule is the commit of the gitlink at its path in the tree of
	// the superproject that Write checked (CheckGitlink). Then it writes
	// the tree of the submodule at that path in dir. It returns ErrCorrupt
	// for a check that fails.
	Submodule(ctx context.Context, s Snapshot, i int, p io.ReaderAt, size int64, dir string) error
	// LargeFiles replaces each Git LFS pointer file of the checkout in dir
	// whose object s.LargeFiles names with the object from objects. Before
	// it replaces a file, it checks that the pointer names the object, and
	// that the content has the SHA-256 and the size of the pointer
	// (CheckLargeFile). A pointer whose object s does not name stays as it
	// is. It returns ErrCorrupt for a check that fails, and ErrTooLarge
	// above MaxLargeFile or MaxCheckout.
	LargeFiles(ctx context.Context, s Snapshot, dir string, objects LargeFileSource) error
}

// LargeFileSource gives the Git LFS objects of a snapshot to the
// launcher, through its job session.
type LargeFileSource interface {
	// Open returns the content of the object oid. It returns ErrNotFound
	// for an object that the snapshot does not hold.
	Open(ctx context.Context, oid string) (io.ReadCloser, error)
}

// CheckGitlink checks that the submodule sub has the commit of the gitlink
// at its path in the checked tree of the superproject. It returns
// ErrCorrupt otherwise, so a pack of another commit never lands at the
// path.
func CheckGitlink(sub *Submodule, gitlink string) error {
	if sub.Commit == "" || sub.Commit != gitlink {
		return fmt.Errorf("%w: submodule %s is at %s, the superproject pins %s", ErrCorrupt, sub.Path, sub.Commit,
			gitlink)
	}

	return nil
}

// CheckLargeFile reads r to its end and checks that it has the size and
// the SHA-256 of want. It returns ErrTooLarge above MaxLargeFile, and
// ErrCorrupt for another size or hash.
func CheckLargeFile(want LargeFile, r io.Reader) error {
	if want.Size > MaxLargeFile {
		return fmt.Errorf("%w: a Git LFS object has at most %d bytes", ErrTooLarge, MaxLargeFile)
	}

	h := sha256.New()

	n, err := io.Copy(h, io.LimitReader(r, want.Size+1))
	if err != nil {
		return fmt.Errorf("read Git LFS object %s: %w", want.OID, err)
	}

	if n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.OID {
		return fmt.Errorf("%w: Git LFS object %s does not match its pointer", ErrCorrupt, want.OID)
	}

	return nil
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
// host and a path, that has a user, a query, a fragment, a port or a '%',
// whose host has a character other than an ASCII letter, a digit, '.' or
// '-', that has an empty path part, or that has a "*" that is not the
// whole last path part.
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
// the end of the target does not count. A target with a '%' never
// matches, so an encoded '/' or '.' cannot pass a pattern. A target with
// a "." or ".." part never matches. The caller resolves a relative URL
// with ResolveURL first.
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

// String returns the URL with no '/' at its end.
func (u httpsURL) String() string {
	if len(u.parts) == 0 {
		return "https://" + u.host
	}

	return "https://" + u.host + "/" + strings.Join(u.parts, "/")
}

// splitHTTPS splits an https URL with no user, port, query, fragment or
// '%' into its host and its path parts. It takes one '/' off the end of
// the path. It refuses a host with a character other than an ASCII letter,
// a digit, '.' or '-', or with an empty label, and a path part that is
// empty, "." or "..". So "https://h/a/b/" is "https://h/a/b", and
// "https://h/a//b" and "https://h/a/b//" are refused.
func splitHTTPS(s string) (httpsURL, bool) {
	none := httpsURL{host: "", parts: nil}

	rest, found := strings.CutPrefix(s, "https://")
	if !found {
		return none, false
	}

	host, p, _ := strings.Cut(rest, "/")
	if !validHost(host) {
		return none, false
	}

	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return httpsURL{host: host, parts: nil}, true
	}

	if strings.ContainsAny(p, "@?#:%\\ \t\n\r") || strings.ContainsRune(p, 0) {
		return none, false
	}

	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return none, false
		}
	}

	return httpsURL{host: host, parts: parts}, true
}

// validHost reports whether h is empty, or is labels of ASCII letters,
// digits and '-', with '.' between them. An empty host is for the caller
// to refuse. A host with any other character, for example a non-ASCII
// letter that looks like an ASCII one, a '_' or a port, never matches a
// pattern.
func validHost(h string) bool {
	if h == "" {
		return true
	}

	for label := range strings.SplitSeq(h, ".") {
		if label == "" {
			return false
		}

		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}

	return true
}

// ResolveURL returns the URL of a submodule whose .gitmodules URL is ref,
// in the superproject with the repository URL base. A ref that starts with
// "./" or "../" resolves against base as git does: each "../" takes away
// one path part of base, and "./" takes away none. Any other ref is the
// URL as it is. It returns ErrNotAllowed for a ref or a base that is not
// an https URL that splitHTTPS accepts, and for a ref that leaves the host
// of base. The policy then matches the result.
func ResolveURL(base, ref string) (string, error) {
	b, ok := splitHTTPS(base)
	if !ok || b.host == "" {
		return "", fmt.Errorf("%w: superproject URL %q", ErrNotAllowed, base)
	}

	if !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
		u, valid := splitHTTPS(ref)
		if !valid || u.host == "" || len(u.parts) == 0 {
			return "", fmt.Errorf("%w: submodule URL %q", ErrNotAllowed, ref)
		}

		return u.String(), nil
	}

	parts, rest, ok := upParts(b.parts, ref)
	if !ok {
		return "", fmt.Errorf("%w: submodule URL %q leaves the host", ErrNotAllowed, ref)
	}

	out, valid := splitHTTPS("https://" + b.host + "/" + strings.Join(append(parts, rest), "/"))
	if !valid || strings.TrimSuffix(rest, "/") == "" {
		return "", fmt.Errorf("%w: submodule URL %q", ErrNotAllowed, ref)
	}

	return out.String(), nil
}

// upParts takes the leading "./" and "../" off ref, and one part off a
// copy of parts for each "../". It returns false when a "../" has no part
// to take off.
func upParts(parts []string, ref string) ([]string, string, bool) {
	out := slices.Clone(parts)

	for {
		switch {
		case strings.HasPrefix(ref, "./"):
			ref = ref[len("./"):]
		case strings.HasPrefix(ref, "../"):
			if len(out) == 0 {
				return nil, "", false
			}

			out, ref = out[:len(out)-1], ref[len("../"):]
		default:
			return out, ref, true
		}
	}
}

// SameHost reports whether the https URLs a and b have one host, with no
// regard to case. The server sends the credentials of a forge connection
// only to a URL whose host is the host of the connection.
func SameHost(a, b string) bool {
	x, ok := splitHTTPS(a)
	y, ok2 := splitHTTPS(b)

	return ok && ok2 && x.host != "" && strings.EqualFold(x.host, y.host)
}

// LFSEndpoint returns the standard Git LFS endpoint of the repository URL
// repo: the URL with ".git" at its end, and "/info/lfs". The server gets
// Git LFS objects only from it, never from an lfs.url of a .lfsconfig
// file. It returns ErrNotAllowed for a URL that splitHTTPS refuses.
func LFSEndpoint(repo string) (string, error) {
	u, ok := splitHTTPS(repo)
	if !ok || u.host == "" || len(u.parts) == 0 {
		return "", fmt.Errorf("%w: repository URL %q", ErrNotAllowed, repo)
	}

	last := len(u.parts) - 1
	u.parts[last] = strings.TrimSuffix(u.parts[last], ".git") + ".git"

	return u.String() + "/info/lfs", nil
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
