// Package forge holds the Forge port: the one way in which the server uses
// a forge. A forge is a git hosting service with an API, for example GitHub
// or GitLab. Through the port, the server reads files and diffs at a
// commit, commits file changes, opens and closes review requests, gets a
// token for the git protocol, and checks the events that a forge sends.
//
// The port is tool-neutral and forge-neutral. No type, method or error in
// it exists only because one forge or one infrastructure-as-code tool needs
// it. A forge that cannot do an operation returns ErrUnsupported, and its
// Capabilities do not list the operation. Needs gives the capabilities that
// each consumer of the port needs, so the server can refuse a connection
// for a feature before a job starts.
//
// The package imports no adapter, no transport and no driver. An adapter
// for each forge implements Opener and Forge, and runs the conformance
// suite in package forgetest.
package forge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Sentinel errors returned by the port. An adapter wraps one of them with
// fmt.Errorf and %w, and never returns an error of its HTTP client or of
// the forge API alone.
var (
	// ErrNotFound reports a repository, a ref, a commit, a file or a review
	// request that the forge does not have, or that the credential of the
	// connection cannot see. The forge APIs answer both
	// cases the same, so the port does too.
	ErrNotFound = errors.New("object not found on the Git host")
	// ErrConflict reports a change whose base moved: a file of a commit
	// that changed after the base commit, a branch that exists with
	// another head, or a review request that merged.
	ErrConflict = errors.New("object on the Git host changed after the base")
	// ErrRefused reports a change that the forge refused for a rule of the
	// forge: branch protection, a ruleset, a required check or review, or
	// a permission that the credential does not have. A retry with the
	// same request fails again.
	ErrRefused = errors.New("change refused by the Git host")
	// ErrAuth reports a credential that the forge does not accept: expired,
	// revoked, or for another forge. The server then tries an older
	// credential set of the connection, if one exists. RotateSelf is the
	// exception: after an error from it, the server tries no other set.
	ErrAuth = errors.New("credential refused by the Git host")
	// ErrUnavailable reports a forge that did not answer, answered a server
	// error, or limited the rate of the connection. A retry with backoff
	// can pass. UnavailableError wraps it with the wait time that the forge
	// gave.
	ErrUnavailable = errors.New("the Git host is not available")
	// ErrTooLarge reports a file or a request above the limit that
	// the caller gave or that the forge applies.
	ErrTooLarge = errors.New("object on the Git host is too large")
	// ErrUnsupported reports an operation that the forge, or the auth kind
	// of the connection, cannot do. Capabilities does not list it.
	ErrUnsupported = errors.New("the Git host does not support the operation")
	// ErrSignature reports an event whose signature or token does not
	// match an active event secret of the registration. It also reports an
	// event that was checked against no secret, or against empty secrets
	// only.
	ErrSignature = errors.New("event signature from the Git host does not match")
	// ErrInvalid reports a request that fails validation before the
	// adapter calls the forge, or connection parameters that the adapter
	// cannot use.
	ErrInvalid = errors.New("invalid Git host request")
	// ErrUntrustedCommit reports a commit that has the trailer of a write-back
	// but that the connection did not make: another author, another
	// committer, or a commit that the host does not show as verified. The
	// commit is not the delivery of the job. A caller must not record it as
	// the delivery, and must not retry: an operator reads the audit record
	// and decides.
	ErrUntrustedCommit = errors.New("a commit with the trailer was not made by the connection")
)

// UnavailableError is an ErrUnavailable with the wait time that the forge
// gave, for example in the header Retry-After of a rate limit. An adapter
// returns it through Unavailable. RetryAfter reads it.
type UnavailableError struct {
	// After is the time to wait before a retry. Zero means the forge gave
	// no time.
	After time.Duration
	// Reason says what the forge did, with no part of a secret.
	Reason string
}

// Error returns the text of ErrUnavailable and the reason.
func (e *UnavailableError) Error() string {
	if e.Reason == "" {
		return ErrUnavailable.Error()
	}

	return ErrUnavailable.Error() + ": " + e.Reason
}

// Unwrap returns ErrUnavailable, so that errors.Is finds it.
func (e *UnavailableError) Unwrap() error {
	return ErrUnavailable
}

// Unavailable returns an UnavailableError.
func Unavailable(after time.Duration, reason string) error {
	return &UnavailableError{After: after, Reason: reason}
}

// RetryAfter returns the wait time that the forge gave in err, and whether
// err holds an UnavailableError with a time above zero.
func RetryAfter(err error) (time.Duration, bool) {
	var u *UnavailableError
	if errors.As(err, &u) && u.After > 0 {
		return u.After, true
	}

	return 0, false
}

// Kind names the adapter of a forge connection.
type Kind string

// The kinds of forge connections.
const (
	KindGitHub Kind = "github"
	KindGitLab Kind = "gitlab"
)

// AuthKind names how a connection authenticates to its forge.
type AuthKind string

// The auth kinds.
const (
	// AuthApp is an app installation: a GitHub App with its private key
	// and installation ID. The adapter mints a short-lived token for each
	// call, and can limit it to one repository and to read access.
	AuthApp AuthKind = "app"
	// AuthToken is one long-lived token of the forge, for example a GitHub
	// fine-grained token or a GitLab group or project access token.
	AuthToken AuthKind = "token"
)

// Connection is a forge connection of a namespace, as the adapter gets it.
// The credential is not in it. The server keeps the credentials sealed,
// and gives one set to Opener.Open.
type Connection struct {
	// ID is unique in the namespace.
	ID   string
	Kind Kind
	Auth AuthKind
	// BaseURL is the API address of the forge, for example
	// https://api.github.com or the address of a GitLab instance. The
	// server checks it against its outbound address rules before Open. The
	// adapter checks the resolved address again when it opens the network
	// connection.
	BaseURL string
	// Params holds the values of the auth kind that are not secret, for
	// example the app ID and the installation ID of a GitHub App.
	Params map[string]string
	// Push is true when the connection may write to repositories. A
	// connection with no push access reads only: its Capabilities list no
	// CapCommit and no CapReview.
	Push bool
}

// The keys of Connection.Params that the GitHub adapter reads. Both hold
// decimal numbers as strings.
const (
	// ParamAppID is the ID of the GitHub App.
	ParamAppID = "app_id"
	// ParamInstallationID is the ID of the installation of the GitHub App.
	ParamInstallationID = "installation_id"
)

// Credential is one credential set of a connection, unsealed for one
// Open. Secret holds the private key of an app or the token. The secret
// that signs the events of the forge is not here: it belongs to the app
// registration of the forge, not to a connection, and the server gives it
// to ParseEvent. The adapter never logs a secret and never returns a part
// of one in an error or in an event.
type Credential struct {
	// SetID names the credential set, so that a log line can name the set
	// that failed without a part of the secret.
	SetID     string
	Secret    []byte
	CreatedAt time.Time
}

// Opener makes a Forge for one connection and one credential set. Each
// adapter implements it.
type Opener interface {
	// Kind names the forge of the adapter.
	Kind() Kind
	// Open returns the Forge of c with the credential cred. It makes no
	// call to the forge. It returns ErrInvalid for a connection of another
	// kind, an auth kind that the adapter does not support, a missing
	// parameter, or a secret that it cannot parse.
	Open(ctx context.Context, c Connection, cred Credential) (Forge, error)
}

// Forge is one forge connection with one credential set. Each method takes
// the repository by its full path on the forge, for example "acme/infra"
// or "group/subgroup/infra". Each method returns ErrAuth for a credential
// that the forge refuses, and ErrUnavailable for a forge that does not
// answer, in addition to the errors that it names.
type Forge interface {
	// Capabilities returns what the forge and the auth kind of the
	// connection can do. It makes no call to the forge.
	Capabilities() Capabilities
	// Check calls the forge and returns the account of the credential,
	// with the expiry and the scopes of a token when the forge gives them.
	// A test of a new connection, a rotation of its credentials and the
	// periodic check of the server call it.
	Check(ctx context.Context) (Account, error)

	Files
	Commits
	Reviews
	Tokens
	Events
	Rotation
}

// Files reads the code of a repository. Write-back, triggers, the change
// order review and the request files of Self-service use it.
type Files interface {
	// Head returns the commit at the head of the branch or tag ref. It
	// returns ErrNotFound for a repository or a ref that does not exist.
	Head(ctx context.Context, repo, ref string) (Commit, error)
	// ReadFile returns the file at filePath in commit, which is a full
	// commit ID. It returns ErrNotFound for a missing repository, commit or
	// file, and ErrTooLarge for a file above limit bytes.
	ReadFile(ctx context.Context, repo, commit, filePath string, limit int64) (File, error)
	// Compare returns the files that changed from base to head, the patch
	// up to limit bytes, and whether base is an ancestor of head. Base and
	// head are full commit IDs. A patch above limit answers its first part
	// with Truncated set, and Files still lists every changed file. It
	// returns ErrInvalid for a commit ID that fails ValidateCommitID or a
	// limit that is not above zero, before any call, ErrNotFound for a
	// missing repository or commit, and ErrTooLarge when the changes have
	// more files than the adapter lists.
	Compare(ctx context.Context, repo, base, head string, limit int64) (Comparison, error)
	// Tree lists the direct children of the directory dir at commit, which
	// is a full commit ID, sorted by name. dir is empty for the root of the
	// repository, or a relative clean path. At most limit entries are
	// listed: a directory with more entries is ErrTooLarge, never a part of
	// the list. Each entry is inside dir. It returns ErrInvalid for a dir,
	// a commit ID or a limit (below one, or above MaxTreeEntries) that fails
	// the checks, before any call, and ErrNotFound for a missing repository,
	// commit or directory, and for a dir that is a file.
	Tree(ctx context.Context, repo, commit, dir string, limit int) ([]TreeEntry, error)
}

// Commits writes to a repository through the API of the forge, never with
// a git client. Write-back uses it.
type Commits interface {
	// Commit makes one commit with all changes of req on req.Branch, and
	// returns it. The commit holds every change or none. With
	// req.NewBranch, it creates the branch at req.Base. It returns
	// ErrConflict when a file of req.Changes differs at the head of the
	// branch from req.Base, or when a new branch exists with another
	// head. It returns ErrNotFound for a missing repository, and for a
	// missing branch when req.NewBranch is not set. It returns ErrRefused
	// when branch protection or a missing permission stops the commit, and
	// ErrInvalid for a request that fails ValidateCommit.
	Commit(ctx context.Context, repo string, req CommitRequest) (Commit, error)
	// FindCommit returns the newest commit on ref, at or after since,
	// whose message has the trailer line trailer, for example
	// "Dustpan-Job: j-123", in its last paragraph. A line of the text of a
	// message is not a trailer, and the line must equal trailer: a longer
	// or shorter line is another trailer. Only a commit that the connection
	// made counts: its author is the identity that the connection commits
	// as, its committer is the Git host, and the host reports it as
	// verified. It returns
	// ErrNotFound when no such commit exists, ErrUntrustedCommit when the
	// newest commit with the trailer fails one of these checks, and
	// ErrInvalid for a trailer that is not one line of the form
	// "Key: value". A takeover of a write-back calls it before a second
	// commit.
	//
	// d names the commit by the Trailer, the Base, the Branch and the
	// digest of the Changes of the CommitRequest that made it (see
	// DeliveryOf). Commit adds a proof to the commit that binds the
	// repository, the trailer, the base, the branch and the digest, and
	// FindCommit counts a commit only when its proof opens for the same five
	// values. A commit with no proof, or with the proof of another value of
	// one of them, is ErrUntrustedCommit. ref must be d.Branch: FindCommit
	// searches the branch that the request committed to, so the branch in
	// the proof is the branch where the search finds the commit. It returns
	// ErrInvalid for a ref that is not d.Branch, for a delivery that fails
	// ValidateDelivery, and ErrUnsupported when the connection has no key to
	// open the proof.
	//
	// The proof shows that the server made a commit for these changes. It
	// does not show that the commit holds them: FindCommit does not compare
	// the digest with the diff of the commit.
	FindCommit(ctx context.Context, repo, ref string, d Delivery, since time.Time) (Commit, error)
}

// Reviews manages review requests: a pull request of GitHub or a merge
// request of GitLab. Write-back uses it. It holds Statuses and Comments,
// because the status of the head commit and the comment are how a rule of the
// Git host and a person see the result of the server for a review request.
type Reviews interface {
	Statuses
	Comments

	// OpenReview opens a review request from req.Branch into req.Target.
	// When an open review request for the same req.Branch and the same
	// req.Target exists, it returns that one and opens no second one. When
	// the forge says that a review request exists for the branch, and it is
	// not one of the base repository (for example one from a fork with a
	// branch of the same name), it returns ErrConflict: a retry fails the
	// same way. It
	// returns ErrInvalid for a request that fails ValidateReviewRequest,
	// before any call. It returns ErrNotFound for a missing repository,
	// branch or target, and ErrRefused when the forge does not allow the
	// review request, for example when the branch has no commit that the
	// target lacks, or when the credential cannot write review requests.
	OpenReview(ctx context.Context, repo string, req ReviewRequest) (Review, error)
	// ReadReview returns the review request id with its state, its base,
	// its head commit and head repository, its author, whether the forge
	// lets it merge and, for a merge, the merge time and the merge commit.
	// It returns ErrInvalid for an id that is not a review number, before
	// any call, and ErrNotFound for a review request that does not exist.
	// An answer that the adapter cannot check is ErrUnavailable: the
	// adapter never returns a review request that it did not check. A
	// review request whose merge state the forge has not computed yet has
	// Mergeable MergeUnknown. ReadReview does not wait and does not retry:
	// the caller reads the review request again later.
	ReadReview(ctx context.Context, repo, id string) (Review, error)
	// CloseReview closes the review request id with no merge. A review
	// request that is closed already returns nil. It returns ErrNotFound
	// for a review request that does not exist, ErrConflict for a
	// review request that merged, and ErrRefused for a review request that
	// is not one of the write-back. The rule is fixed: CloseReview acts on a
	// review request only when its head branch passes IsWriteBackBranch and
	// its head repository is the base repository (see Review.FromFork). It
	// checks this first, for each state: a review request of a person, or from
	// a fork, is never closed, and also gets ErrRefused when it is closed or
	// merged already.
	CloseReview(ctx context.Context, repo, id string) error
	// DeleteBranch deletes the branch of a write-back after its review
	// request closed or merged. It returns ErrInvalid for a name that
	// fails IsWriteBackBranch, before any call, and ErrRefused for the
	// default branch of the repository, for a protected branch, for a
	// branch that an open review request has as its head or its base, and for a missing
	// permission. It returns ErrNotFound for a missing repository or branch,
	// so a caller that wants an idempotent delete ignores ErrNotFound.
	DeleteBranch(ctx context.Context, repo, branch string) error
}

// Statuses sets the commit status of a review request. The review request
// feature uses it, so that a rule of the Git host can require the result of
// the server.
type Statuses interface {
	// SetStatus sets the status s on the commit sha, which is a full commit
	// ID. A commit has one current status for each s.Context: a second call
	// with the same context replaces the first, and a call with another
	// context leaves it. It returns ErrInvalid for a status that fails
	// ValidateStatus or a commit ID that fails ValidateCommitID, before any
	// call, ErrNotFound for a missing repository or commit, ErrRefused when
	// the credential cannot write statuses or the Git host refuses the
	// status, and ErrUnsupported for a connection that Capabilities does not
	// list CapStatus for.
	SetStatus(ctx context.Context, repo, sha string, s Status) error
}

// Comments writes the comment of the server on a review request. The comment
// has a hidden marker, so that the server finds it again and edits it, and never
// makes a second one.
type Comments interface {
	// UpsertComment finds the comment of the connection on the review request
	// reviewID that has the marker req.Marker, and edits it. When there is
	// none, it makes one. A comment of another account is never edited, also
	// when its text has the marker, and a comment of the connection without
	// the marker is never edited. A text above MaxCommentBytes is cut and has
	// a notice, and Comment.Truncated is set. It returns ErrInvalid for a
	// request that fails ValidateComment or an ID that is not a review number,
	// before any call, ErrNotFound for a missing repository or review request,
	// ErrRefused when the credential cannot write comments or the Git host
	// refuses the comment (for example a locked conversation), ErrTooLarge
	// when the review request has more comments than the adapter reads, and
	// ErrUnsupported for a connection that Capabilities does not list
	// CapComment for.
	UpsertComment(ctx context.Context, repo, reviewID string, req CommentRequest) (Comment, error)
	// ReadComment returns the comment commentID of the repository. It returns
	// ErrInvalid for an ID that is not a number, before any call, and
	// ErrNotFound for a comment that does not exist.
	ReadComment(ctx context.Context, repo, commentID string) (Comment, error)
	// React adds the reaction r of the connection to the comment commentID.
	// It is idempotent: a reaction that the connection made already is nil.
	// It returns ErrInvalid for a reaction that is not known or an ID that is
	// not a number, before any call, and ErrNotFound for a comment that does
	// not exist.
	React(ctx context.Context, repo, commentID string, r Reaction) error
}

// Tokens gives a token for the git protocol. The source fetch of the server
// and the git credential helper of a runner step use it. A runner never gets
// a token for the source of its job: it gets the source only as the pack
// that the server fetched. The git credential helper serves other git
// fetches of a step, for example a private Git module source.
type Tokens interface {
	// RepoToken returns a token for the git protocol over HTTPS for repo.
	// An app connection answers a token that reads only repo and expires in
	// at most one hour, with Scoped set. A token connection answers its own
	// token with Scoped not set. It returns ErrUnsupported when the auth
	// kind has no token for the git protocol, and ErrNotFound for a
	// missing repository. A token with Scoped not set
	// stays in the server: a caller that gives a token to a runner refuses
	// it.
	RepoToken(ctx context.Context, repo string) (Token, error)
}

// Events checks an event that the forge sent to the server.
type Events interface {
	// ParseEvent checks the signature or the token of r against each of
	// secrets, which are the active event secrets of the app registration
	// or of the hook, and returns the event. The event is valid when it
	// matches any one of them. It ignores an empty secret. It returns
	// ErrSignature for a request that matches none of them, and for a list
	// with no secret or with empty secrets only. It returns ErrInvalid for a
	// body that it cannot parse, and ErrUnsupported for a kind of event
	// that the port does not name. It makes no call to the forge. An
	// event is a hint: the server reads the object through the API before
	// it changes a record.
	ParseEvent(ctx context.Context, r EventRequest, secrets [][]byte) (Event, error)
}

// Rotation replaces the token of a connection with a new token from the
// forge.
type Rotation interface {
	// RotateSelf asks the forge for a new token in place of the token of
	// the credential, and returns the new token. The forge revokes the old
	// token when it makes the new one. The caller seals and stores the new
	// token in the same step that receives it. The adapter makes one call and
	// never retries it. The caller never tries an older credential set after
	// an error, and never runs two rotations of one connection at once.
	// After any error, the caller
	// never calls RotateSelf again with the old token: the forge may have
	// rotated it already, and a second rotation with a revoked token can
	// make the forge revoke each token made from it. expiresAt is the expiry
	// of the new token. The zero time lets the forge choose it. It returns
	// ErrUnsupported when the forge or the auth kind cannot rotate a token,
	// and ErrAuth when the forge refuses the old token.
	RotateSelf(ctx context.Context, expiresAt time.Time) (RotatedToken, error)
}

// RotatedToken is the new token of a rotation. Secret has the same form as
// Credential.Secret. ExpiresAt is the expiry that the forge gave.
type RotatedToken struct {
	Secret    []byte
	ExpiresAt time.Time
}

// Account is an account of a forge. ID is the stable ID that the forge
// gives, for example the numeric user ID of GitHub. Login can change, so a
// link to a principal holds the ID.
//
// ExpiresAt is the expiry of the token of the credential, when the forge
// gives one. NoExpiry is true when the forge says that the token has no
// expiry. The server flags such a token. With ExpiresAt zero and NoExpiry
// false, the forge gave no expiry, for example for an app, whose tokens are
// short-lived. Scopes are the scopes of the token, when the forge gives them.
type Account struct {
	ID        string
	Login     string
	ExpiresAt time.Time
	NoExpiry  bool
	Scopes    []string
}

// Commit is a commit on the forge. Time is the time that the forge gives
// for the commit. A write-back records it as the delivery time.
type Commit struct {
	ID   string
	Time time.Time
}

// File is one file at one commit. BlobID is the ID of the git blob.
type File struct {
	Path    string
	Content []byte
	BlobID  string
}

// FileChange is one change of a commit: new content for Path, or a delete
// of Path.
type FileChange struct {
	Path    string
	Content []byte
	Delete  bool
}

// Person is the author of a commit, as the commit shows it.
type Person struct {
	Name  string
	Email string
}

// Limits of a CommitRequest.
const (
	// MaxChanges is the most file changes in one commit.
	MaxChanges = 64
	// MaxChangeBytes is the most bytes of content of all changes of one
	// commit.
	MaxChangeBytes = 4 << 20
	// MaxMessageBytes is the most bytes of a commit message.
	MaxMessageBytes = 64 << 10
)

// CommitRequest asks for one commit. Base is the full ID of the commit that
// the changes start from. Author names the person who made the change. The
// adapter writes it as the git author, or as a Co-authored-by trailer when
// the forge makes the commit as the account of the connection. The
// committer is always the connection.
//
// Trailer is the trailer line that FindCommit searches for, for example
// "Dustpan-Job: j-123". It must be a line of the last paragraph of Message.
// The adapter adds a proof next to it that binds Trailer, Base, Branch, the
// digest of Changes and the repository, so that FindCommit can tell this
// commit from a commit that a person pushed with the same line.
type CommitRequest struct {
	Branch    string
	NewBranch bool
	Base      string
	Message   string
	Trailer   string
	Author    Person
	Changes   []FileChange
}

// Delivery names a commit that a write-back made: the trailer line, the base
// commit, the branch and the digest of the changes of its CommitRequest.
// FindCommit takes it. DeliveryOf makes it from the request.
type Delivery struct {
	Trailer string
	Base    string
	Branch  string
	// Changes is ChangesDigest of the changes of the request.
	Changes string
}

// DeliveryOf returns the Delivery of the commit that req asks for.
func DeliveryOf(req CommitRequest) Delivery {
	return Delivery{Trailer: req.Trailer, Base: req.Base, Branch: req.Branch, Changes: ChangesDigest(req.Changes)}
}

// changesDigestPrefix starts each digest of changes. It names the hash.
const changesDigestPrefix = "sha256:"

// changesDigestPurpose starts the bytes that ChangesDigest hashes, so that
// the digest of changes is never the hash of other data of the same bytes.
const changesDigestPurpose = "dustpan.forge.changes.v1"

// changesLengthBase is the base of the length before each part that
// ChangesDigest hashes.
const changesLengthBase = 10

// ChangesDigest returns the digest of changes: "sha256:" and the SHA-256, as
// lowercase hex, of the changes in their order. Each change gives its path,
// whether it is a delete, and its content. Each part has its length before
// it, in decimal digits and then a colon, so that no path or content can move
// bytes into the next part. The content of a delete is not part of it.
func ChangesDigest(changes []FileChange) string {
	h := sha256.New()

	part := func(b []byte) {
		var n []byte

		n = strconv.AppendInt(n, int64(len(b)), changesLengthBase)
		n = append(n, ':')
		_, _ = h.Write(n) // The write of a hash does not fail.
		_, _ = h.Write(b) // The write of a hash does not fail.
	}

	part([]byte(changesDigestPurpose))
	part([]byte(strconv.Itoa(len(changes))))

	for _, c := range changes {
		part([]byte(c.Path))

		if c.Delete {
			part([]byte("delete"))
			part(nil)
		} else {
			part([]byte("write"))
			part(c.Content)
		}
	}

	return changesDigestPrefix + hex.EncodeToString(h.Sum(nil))
}

// ValidateChangesDigest checks that d has the form of a ChangesDigest:
// "sha256:" and 64 lowercase hex digits. It returns an error that wraps
// ErrInvalid.
func ValidateChangesDigest(d string) error {
	hexPart, ok := strings.CutPrefix(d, changesDigestPrefix)
	if !ok || len(hexPart) != sha256.Size*2 {
		return fmt.Errorf("%w: a digest of changes is sha256: and 64 hex digits", ErrInvalid)
	}

	for i := range len(hexPart) {
		c := hexPart[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: a digest of changes has only the digits 0-9 and a-f", ErrInvalid)
		}
	}

	return nil
}

// ValidateDelivery checks d before an adapter calls the forge: the trailer is
// set, the base is a full commit ID, the branch is a valid branch name, and
// the digest of the changes has its form. It returns an error that wraps
// ErrInvalid. An adapter checks the text of the trailer with its own rules
// too.
func ValidateDelivery(d Delivery) error {
	if d.Trailer == "" {
		return fmt.Errorf("%w: the delivery has no trailer line", ErrInvalid)
	}

	if err := ValidateCommitID(d.Base); err != nil {
		return fmt.Errorf("the base commit: %w", err)
	}

	if err := validateRef(d.Branch); err != nil {
		return fmt.Errorf("the branch of the delivery: %w", err)
	}

	if err := ValidateChangesDigest(d.Changes); err != nil {
		return fmt.Errorf("the changes of the delivery: %w", err)
	}

	return nil
}

// ValidateCommit checks req before an adapter calls the forge. It returns
// an error that wraps ErrInvalid or ErrTooLarge.
func ValidateCommit(req CommitRequest) error {
	if err := validateRef(req.Branch); err != nil {
		return err
	}

	if err := ValidateCommitID(req.Base); err != nil {
		return fmt.Errorf("the base commit: %w", err)
	}

	if req.Message == "" {
		return fmt.Errorf("%w: the commit message is empty", ErrInvalid)
	}

	if req.Trailer == "" {
		return fmt.Errorf("%w: the commit has no trailer line", ErrInvalid)
	}

	if len(req.Message) > MaxMessageBytes {
		return fmt.Errorf("%w: the commit message has more than %d bytes", ErrTooLarge, MaxMessageBytes)
	}

	if len(req.Changes) == 0 || len(req.Changes) > MaxChanges {
		return fmt.Errorf("%w: a commit has 1 to %d file changes, not %d", ErrInvalid, MaxChanges, len(req.Changes))
	}

	return validateChanges(req.Changes)
}

func validateChanges(changes []FileChange) error {
	seen := make(map[string]bool, len(changes))
	total := 0

	for _, c := range changes {
		if err := validatePath(c.Path); err != nil {
			return err
		}

		if seen[c.Path] {
			return fmt.Errorf("%w: the path %q is in the commit twice", ErrInvalid, c.Path)
		}

		seen[c.Path] = true

		if c.Delete && len(c.Content) > 0 {
			return fmt.Errorf("%w: the delete of %q has content", ErrInvalid, c.Path)
		}

		total += len(c.Content)
		if total > MaxChangeBytes {
			return fmt.Errorf("%w: the changes have more than %d bytes", ErrTooLarge, MaxChangeBytes)
		}
	}

	return nil
}

// Limits of the names that the port accepts.
const (
	maxPathBytes = 4096
	maxRefBytes  = 255
)

// validatePath checks a file path in a repository with an allow list. Each
// rune is a graphic character: a letter, a mark, a number, a punctuation
// mark, a symbol or a space. A control character, a format character (for
// example a bidirectional override) and a backslash are not valid. The path
// is relative and clean, and no part is "..", or ".git" in any case.
func validatePath(p string) error {
	switch {
	case p == "", p == ".":
		return fmt.Errorf("%w: the file path is empty", ErrInvalid)
	case len(p) > maxPathBytes:
		return fmt.Errorf("%w: the file path has more than %d bytes", ErrInvalid, maxPathBytes)
	case !utf8.ValidString(p):
		return fmt.Errorf("%w: the file path is not valid UTF-8", ErrInvalid)
	}

	for _, r := range p {
		if r == '\\' || !unicode.IsGraphic(r) {
			return fmt.Errorf("%w: the file path %q has a character that is not allowed", ErrInvalid, p)
		}
	}

	if strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return fmt.Errorf("%w: the file path %q is not relative and clean", ErrInvalid, p)
	}

	for part := range strings.SplitSeq(p, "/") {
		switch {
		case part == "..":
			return fmt.Errorf("%w: the file path %q leaves the repository", ErrInvalid, p)
		case strings.EqualFold(part, ".git"):
			return fmt.Errorf("%w: the file path %q has a .git part", ErrInvalid, p)
		}
	}

	return nil
}

// validateRef checks a branch or tag name with an allow list. Each rune is
// a letter, a digit, or one of "._-+/". Each part between slashes is not
// empty, does not start with "." or "-", and does not end with "." or
// ".lock". The name is not a full ref and has no "..". The allow list
// refuses a control character, "@{" and each character that git refuses.
func validateRef(ref string) error {
	if !refRunesAllowed(ref) || strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "..") ||
		!refPartsAllowed(ref) {
		return fmt.Errorf("%w: the branch name %q is not valid", ErrInvalid, ref)
	}

	return nil
}

func refRunesAllowed(ref string) bool {
	if ref == "" || len(ref) > maxRefBytes || !utf8.ValidString(ref) {
		return false
	}

	for _, r := range ref {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-+/", r) {
			return false
		}
	}

	return true
}

func refPartsAllowed(ref string) bool {
	for part := range strings.SplitSeq(ref, "/") {
		if part == "" || part[0] == '.' || part[0] == '-' || strings.HasSuffix(part, ".") ||
			strings.HasSuffix(part, ".lock") {
			return false
		}
	}

	return true
}

// ValidateCommitID checks a full commit ID: 40 hex digits (SHA-1) or 64
// (SHA-256), in lower case. It returns an error that wraps ErrInvalid. An
// adapter calls it before it sends a commit ID to the forge.
func ValidateCommitID(id string) error {
	if len(id) != 40 && len(id) != 64 {
		return fmt.Errorf("%w: a commit ID has 40 or 64 hex digits, not %d characters", ErrInvalid, len(id))
	}

	for i := range len(id) {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: a commit ID has only the digits 0-9 and a-f", ErrInvalid)
		}
	}

	return nil
}

// MinEventSecretBytes is the least length of an event secret. The guide
// gives the command "openssl rand -hex 32", which makes 64 characters from
// 32 random bytes. The server cannot measure the entropy of a secret, so it
// checks the length only.
const MinEventSecretBytes = 32

// ValidateEventSecret checks an event secret when a person sets it. It
// returns an error that wraps ErrInvalid for a secret shorter than
// MinEventSecretBytes. The error text holds no part of the secret.
func ValidateEventSecret(secret []byte) error {
	if len(secret) < MinEventSecretBytes {
		return fmt.Errorf(
			"%w: the event secret has %d bytes, the least is %d", ErrInvalid, len(secret), MinEventSecretBytes,
		)
	}

	return nil
}

// ChangedFile is one file of a Comparison. OldPath is set for a rename.
type ChangedFile struct {
	Path    string
	OldPath string
	Status  FileStatus
}

// FileStatus is the change of one file in a Comparison.
type FileStatus string

// The file statuses.
const (
	FileAdded    FileStatus = "added"
	FileModified FileStatus = "modified"
	FileDeleted  FileStatus = "deleted"
	FileRenamed  FileStatus = "renamed"
)

// Comparison is the answer of Compare. Files lists every changed file,
// also when Patch is truncated. BaseIsAncestor is true when base is an
// ancestor of head, or equal to it.
type Comparison struct {
	Files          []ChangedFile
	Patch          []byte
	Truncated      bool
	BaseIsAncestor bool
}

// Limits of the comments and of the tree reads.
const (
	// MaxCommentBytes is the most bytes of a comment, the marker and a notice
	// of a cut included.
	MaxCommentBytes = 65536
	// MaxCommentMarkerBytes is the most bytes of the key of a marker.
	MaxCommentMarkerBytes = 64
	// MaxTreeEntries is the most entries that Tree lists.
	MaxTreeEntries = 1000
	// CommentMarkerPrefix starts the hidden marker line of each comment of the
	// server.
	CommentMarkerPrefix = "<!-- opendustpan:"
	commentMarkerSuffix = " -->"
	// CommentCutNotice ends a comment that was cut to MaxCommentBytes.
	CommentCutNotice = "\n\n_The text is cut: it has more than 65536 bytes._"
)

// CommentRequest asks for the comment of the server with the key Marker. The
// key names one comment, for example "plan": a second call with the same key
// edits the comment. Body is Markdown.
type CommentRequest struct {
	Marker string
	Body   string
}

// Comment is a comment on a review request. Body is the text as the Git host
// shows it, with the marker line. Truncated is set when UpsertComment cut the
// text of the request.
type Comment struct {
	ID        string
	ReviewID  string
	URL       string
	AuthorID  string
	Body      string
	Truncated bool
}

// Reaction is a reaction to a comment.
type Reaction string

// The reactions. They are the ones that GitHub has; a forge that has fewer
// refuses the others with ErrInvalid.
const (
	ReactionPlusOne  Reaction = "+1"
	ReactionMinusOne Reaction = "-1"
	ReactionLaugh    Reaction = "laugh"
	ReactionConfused Reaction = "confused"
	ReactionHeart    Reaction = "heart"
	ReactionHooray   Reaction = "hooray"
	ReactionRocket   Reaction = "rocket"
	ReactionEyes     Reaction = "eyes"
)

// ValidateReaction checks r. It returns an error that wraps ErrInvalid.
func ValidateReaction(r Reaction) error {
	switch r {
	case ReactionPlusOne, ReactionMinusOne, ReactionLaugh, ReactionConfused, ReactionHeart, ReactionHooray,
		ReactionRocket, ReactionEyes:
		return nil
	}

	return fmt.Errorf("%w: the reaction %q is not known", ErrInvalid, string(r))
}

// CommentMarker returns the hidden marker line for the key: an HTML comment
// that the Git host does not show. It returns an error that wraps ErrInvalid
// for a key that is not 1 to MaxCommentMarkerBytes characters of a-z, 0-9 and
// "._-", starting with a letter or a digit.
func CommentMarker(key string) (string, error) {
	if !validMarkerKey(key) {
		return "", fmt.Errorf(
			"%w: the comment marker key has 1 to %d characters of a-z, 0-9 and \"._-\", and starts with a letter or a digit",
			ErrInvalid,
			MaxCommentMarkerBytes,
		)
	}

	return CommentMarkerPrefix + key + commentMarkerSuffix, nil
}

// validMarkerKey reports whether key is a key that CommentMarker accepts.
func validMarkerKey(key string) bool {
	if key == "" || len(key) > MaxCommentMarkerBytes || key[0] == '.' || key[0] == '-' || key[0] == '_' {
		return false
	}

	for i := range len(key) {
		if !markerChar(key[i]) {
			return false
		}
	}

	return true
}

// markerChar reports whether c is a character that a marker key may have.
func markerChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
}

// ValidateComment checks req before an adapter calls the forge. It returns an
// error that wraps ErrInvalid. A body above MaxCommentBytes is not an error:
// CommentText cuts it.
func ValidateComment(req CommentRequest) error {
	if _, err := CommentMarker(req.Marker); err != nil {
		return err
	}

	switch {
	case !utf8.ValidString(req.Body):
		return fmt.Errorf("%w: the comment text is not valid UTF-8", ErrInvalid)
	case strings.TrimSpace(req.Body) == "":
		return fmt.Errorf("%w: the comment text is empty", ErrInvalid)
	}

	for _, r := range req.Body {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return fmt.Errorf("%w: the comment text has a control character", ErrInvalid)
		}
	}

	return nil
}

// CommentText returns the text that an adapter sends for req: the marker line,
// a line break and the body. A text above MaxCommentBytes is cut at the end of a
// character and ends with CommentCutNotice, so that the whole text has at most
// MaxCommentBytes bytes. The boolean says whether it cut. It returns an error
// that wraps ErrInvalid for a request that fails ValidateComment.
func CommentText(req CommentRequest) (string, bool, error) {
	if err := ValidateComment(req); err != nil {
		return "", false, err
	}

	marker, err := CommentMarker(req.Marker)
	if err != nil {
		return "", false, err
	}

	text := marker + "\n" + req.Body
	if len(text) <= MaxCommentBytes {
		return text, false, nil
	}

	end := MaxCommentBytes - len(CommentCutNotice)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}

	return text[:end] + CommentCutNotice, true, nil
}

// TreeKind is the kind of an entry of a directory.
type TreeKind string

// The kinds of entries.
const (
	TreeFile      TreeKind = "file"
	TreeDir       TreeKind = "dir"
	TreeSymlink   TreeKind = "symlink"
	TreeSubmodule TreeKind = "submodule"
)

// TreeEntry is one direct child of a directory. Path is the path from the root
// of the repository, and Name the last part of it. ID is the object ID of the
// blob, the tree or the commit of a submodule. Size is the size of a file and
// zero for the other kinds.
type TreeEntry struct {
	Path string
	Name string
	Kind TreeKind
	ID   string
	Size int64
}

// StatusState is the state of a commit status.
type StatusState string

// The status states.
const (
	// StatusPending is a result that is not ready.
	StatusPending StatusState = "pending"
	// StatusSuccess is a result that passed.
	StatusSuccess StatusState = "success"
	// StatusFailure is a result that did not pass.
	StatusFailure StatusState = "failure"
	// StatusError is a result that the server could not make.
	StatusError StatusState = "error"
)

// Status is the status of one commit for one context. Context names the
// check, for example "opendustpan/plan", and a rule of the Git host can
// require it. Description is one short line and can be empty. TargetURL is the
// https address of the page that shows the result and can be empty.
type Status struct {
	State       StatusState
	Context     string
	Description string
	TargetURL   string
}

// Limits of a Status.
const (
	// MaxStatusContextBytes is the most bytes of the context of a status.
	MaxStatusContextBytes = 255
	// MaxStatusDescriptionBytes is the most bytes of the description of a
	// status.
	MaxStatusDescriptionBytes = 140
	// MaxStatusURLBytes is the most bytes of the address of a status.
	MaxStatusURLBytes = 2048
)

// ValidateStatus checks s before an adapter calls the forge. It returns an
// error that wraps ErrInvalid or ErrTooLarge. The context and the description
// have valid UTF-8 and no control character, and the context is not empty. The
// address is empty or an https address with a host and no user information.
func ValidateStatus(s Status) error {
	switch s.State {
	case StatusPending, StatusSuccess, StatusFailure, StatusError:
	default:
		return fmt.Errorf("%w: the status state %q is not known", ErrInvalid, s.State)
	}

	if err := validateStatusText(s.Context, MaxStatusContextBytes); err != nil {
		return fmt.Errorf("the status context: %w", err)
	}

	if strings.TrimSpace(s.Context) == "" {
		return fmt.Errorf("the status context: %w: the text is empty", ErrInvalid)
	}

	if err := validateStatusText(s.Description, MaxStatusDescriptionBytes); err != nil {
		return fmt.Errorf("the status description: %w", err)
	}

	return validateStatusURL(s.TargetURL)
}

// validateStatusText checks one line of text: at most limit bytes, valid
// UTF-8 and no control character.
func validateStatusText(text string, limit int) error {
	switch {
	case len(text) > limit:
		return fmt.Errorf("%w: the text has more than %d bytes", ErrTooLarge, limit)
	case !utf8.ValidString(text):
		return fmt.Errorf("%w: the text is not valid UTF-8", ErrInvalid)
	case strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }):
		return fmt.Errorf("%w: the text has a control character", ErrInvalid)
	}

	return nil
}

// validateStatusURL checks the address of a status. An empty address is valid.
func validateStatusURL(raw string) error {
	if raw == "" {
		return nil
	}

	if len(raw) > MaxStatusURLBytes {
		return fmt.Errorf("the status address: %w: the text has more than %d bytes", ErrTooLarge, MaxStatusURLBytes)
	}

	u, err := url.Parse(raw)

	switch {
	case err != nil, u.Scheme != "https", u.Host == "", u.User != nil, u.Fragment != "",
		strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }):
		return fmt.Errorf("%w: the status address must be an https address with a host", ErrInvalid)
	}

	return nil
}

// ReviewRequest asks for a review request from Branch into Target.
type ReviewRequest struct {
	Branch string
	Target string
	Title  string
	Body   string
}

// Limits of a review request that the port accepts.
const (
	// MaxReviewTitleBytes is the most bytes of the title of a review request.
	MaxReviewTitleBytes = 256
	// MaxReviewBodyBytes is the most bytes of the description of a review
	// request.
	MaxReviewBodyBytes = 64 << 10
)

// WriteBackBranchPrefix starts the name of each branch that write-back makes
// for a review request: "dustpan/" and the ID of the job. DeleteBranch and
// CloseReview act on such a branch only.
const WriteBackBranchPrefix = "dustpan/"

// IsWriteBackBranch reports whether name is a valid branch name that starts
// with WriteBackBranchPrefix. A valid branch name has no empty part, so the
// name has more text after the prefix.
func IsWriteBackBranch(name string) bool {
	return strings.HasPrefix(name, WriteBackBranchPrefix) && validateRef(name) == nil
}

// ValidateReviewRequest checks req before an adapter calls the forge. It
// returns an error that wraps ErrInvalid or ErrTooLarge.
func ValidateReviewRequest(req ReviewRequest) error {
	if err := validateRef(req.Branch); err != nil {
		return err
	}

	if err := validateRef(req.Target); err != nil {
		return err
	}

	if req.Branch == req.Target {
		return fmt.Errorf("%w: the branch and the target of a review request are the same", ErrInvalid)
	}

	if err := validateReviewTitle(req.Title); err != nil {
		return fmt.Errorf("the review request title: %w", err)
	}

	if err := validateReviewBody(req.Body); err != nil {
		return fmt.Errorf("the review request description: %w", err)
	}

	return nil
}

// validateReviewTitle checks the title of a review request: one line of valid
// UTF-8 that is not empty and has no control character. A title above
// MaxReviewTitleBytes is ErrTooLarge.
func validateReviewTitle(text string) error {
	switch {
	case len(text) > MaxReviewTitleBytes:
		return fmt.Errorf("%w: the text has more than %d bytes", ErrTooLarge, MaxReviewTitleBytes)
	case !utf8.ValidString(text):
		return fmt.Errorf("%w: the text is not valid UTF-8", ErrInvalid)
	case strings.TrimSpace(text) == "":
		return fmt.Errorf("%w: the text is empty", ErrInvalid)
	case strings.ContainsFunc(text, unicode.IsControl):
		return fmt.Errorf("%w: the text has a control character", ErrInvalid)
	}

	return nil
}

// validateReviewBody checks the description of a review request: valid UTF-8
// with no control character, except the line break and the tab. A description
// above MaxReviewBodyBytes is ErrTooLarge.
func validateReviewBody(text string) error {
	switch {
	case len(text) > MaxReviewBodyBytes:
		return fmt.Errorf("%w: the text has more than %d bytes", ErrTooLarge, MaxReviewBodyBytes)
	case !utf8.ValidString(text):
		return fmt.Errorf("%w: the text is not valid UTF-8", ErrInvalid)
	}

	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return fmt.Errorf("%w: the text has a control character", ErrInvalid)
		}
	}

	return nil
}

// ReviewState is the state of a review request.
type ReviewState string

// The review states.
const (
	ReviewOpen   ReviewState = "open"
	ReviewMerged ReviewState = "merged"
	ReviewClosed ReviewState = "closed"
)

// MergeState says whether the forge lets a review request merge.
type MergeState string

// The merge states. Only MergeClean says that the forge lets the review
// request merge now. Each other value, and each value that an adapter does
// not know, is a reason to wait or to refuse: a caller treats it as "not
// mergeable".
const (
	// MergeUnknown is a merge state that the forge has not computed yet, or
	// that the adapter does not know.
	MergeUnknown MergeState = "unknown"
	// MergeClean is a review request with no conflict and no rule that stops
	// the merge.
	MergeClean MergeState = "clean"
	// MergeConflict is a review request whose head conflicts with the base.
	MergeConflict MergeState = "conflict"
	// MergeBlocked is a review request that a rule of the forge stops: a
	// required review or a required check.
	MergeBlocked MergeState = "blocked"
	// MergeBehind is a review request whose head lacks commits of the base,
	// and a rule requires an up-to-date head.
	MergeBehind MergeState = "behind"
	// MergeUnstable is a review request whose checks that are not required
	// fail.
	MergeUnstable MergeState = "unstable"
	// MergeDraft is a review request that is a draft.
	MergeDraft MergeState = "draft"
)

// Review is a review request on the forge. ID is the number or the ID that
// the forge gives, as a string. Branch is the head branch and Target the
// base branch. BaseCommit is the commit at the head of Target that the forge
// compared with the head. Head is the head commit. BaseRepoID and HeadRepoID
// are the stable IDs of the repository of the base and of the head, as
// strings: a review request from a fork has another HeadRepoID, and a name is
// never the key. AuthorID is the stable ID of the account that opened it.
// TestMergeCommit is the commit that the forge made to test the merge of an
// open review request, empty when the forge made none. Mergeable says
// whether the forge lets it merge. MergedAt and MergeCommit are set when
// State is ReviewMerged.
type Review struct {
	ID              string
	URL             string
	State           ReviewState
	Branch          string
	Target          string
	Head            string
	BaseCommit      string
	BaseRepoID      string
	HeadRepoID      string
	AuthorID        string
	TestMergeCommit string
	Mergeable       MergeState
	MergedAt        time.Time
	MergeCommit     string
}

// FromFork reports whether the head of the review request is in another
// repository than its base. It is true also when the forge gave no ID for
// the head or for the base, for example after the delete of the fork, so that
// a caller never takes an unknown origin for the base repository.
func (r Review) FromFork() bool {
	return r.BaseRepoID == "" || r.HeadRepoID == "" || r.BaseRepoID != r.HeadRepoID
}

// Token is a token for the git protocol over HTTPS. Username is the user
// name that the forge expects with the token. Scoped is true when the token
// reads only the repository that RepoToken got, and expires in at most one
// hour. ExpiresAt is zero when the token does not expire.
type Token struct {
	Username  string
	Value     []byte
	ExpiresAt time.Time
	Scoped    bool
}

// EventRequest is an event as the HTTP adapter received it: the header and
// the body. The port has no HTTP type, so the domain imports no transport.
type EventRequest struct {
	Header map[string][]string
	Body   []byte
}

// EventKind is the kind of a forge event.
type EventKind string

// The event kinds.
const (
	// EventPing is the test event that a forge sends when a person sets
	// up a webhook.
	EventPing EventKind = "ping"
	// EventReview is a review request that opened, changed its head,
	// merged or closed.
	EventReview EventKind = "review"
)

// Event is a forge event. DeliveryID is the ID that the forge gives to the
// delivery, and stays the same across retries of the forge. ReviewID is set
// for EventReview. InstallationID is set
// when the forge sends the events of many installations of one app to one
// address, for example a GitHub App. The server maps it to the connection.
type Event struct {
	DeliveryID     string
	Kind           EventKind
	Repo           string
	ReviewID       string
	InstallationID string
	Sender         Account
}

// Capability is one group of operations of the port.
type Capability string

// The capabilities. Each one names the methods that it covers.
const (
	// CapFiles covers Head and ReadFile.
	CapFiles Capability = "files"
	// CapCompare covers Compare.
	CapCompare Capability = "compare"
	// CapCommit covers Commit and FindCommit.
	CapCommit Capability = "commit"
	// CapReview covers OpenReview, ReadReview, CloseReview and DeleteBranch.
	CapReview Capability = "review"
	// CapStatus covers SetStatus.
	CapStatus Capability = "status"
	// CapComment covers UpsertComment, ReadComment and React.
	CapComment Capability = "comment"
	// CapTree covers Tree.
	CapTree Capability = "tree"
	// CapGitAccess covers RepoToken: a token for the git protocol,
	// with Scoped set or not.
	CapGitAccess Capability = "git-access"
	// CapRepoToken covers RepoToken when it answers a token with Scoped
	// set: one repository, read only, one hour at most. A forge that lists
	// CapRepoToken also lists CapGitAccess.
	CapRepoToken Capability = "repo-token"
	// CapEvents covers ParseEvent.
	CapEvents Capability = "events"
	// CapSelfRotate covers RotateSelf.
	CapSelfRotate Capability = "self-rotate"
)

// Capabilities is the set of capabilities of a forge connection. With Push
// not set, the list has no commit, no review, no status and no comment.
type Capabilities []Capability

// Has reports whether c holds capability.
func (c Capabilities) Has(capability Capability) bool {
	return slices.Contains(c, capability)
}

// Missing returns each capability of need that c does not hold, in the
// order of need.
func (c Capabilities) Missing(need Capabilities) Capabilities {
	var missing Capabilities

	for _, n := range need {
		if !c.Has(n) {
			missing = append(missing, n)
		}
	}

	return missing
}

// Consumer is a feature of the server that uses a forge connection.
type Consumer string

// The consumers.
const (
	// ConsumerWriteBack commits a change to a branch that is not a review
	// request.
	ConsumerWriteBack Consumer = "write-back"
	// ConsumerWriteBackReview commits a change and opens a review request.
	// It watches the review request until the merge or the close.
	ConsumerWriteBackReview Consumer = "write-back-review"
	// ConsumerFetch gets the code of a job with a git fetch in the server.
	ConsumerFetch Consumer = "fetch"
	// ConsumerRunnerGit gives a read-only, short-lived token to the git
	// credential helper of a runner step, for a git fetch other than the
	// source of the job, for example a private Git module source. It never
	// gives a long-lived token.
	ConsumerRunnerGit Consumer = "runner-git"
	// ConsumerSelfService reads the request files of Self-service.
	ConsumerSelfService Consumer = "self-service"
	// ConsumerCauseDiff reads the cause diff of a change order.
	ConsumerCauseDiff Consumer = "cause-diff"
)

// Needs returns the capabilities that consumer needs from a connection. It
// returns nil for an unknown consumer.
func Needs(consumer Consumer) Capabilities {
	switch consumer {
	case ConsumerWriteBack:
		return Capabilities{CapFiles, CapCompare, CapCommit}
	case ConsumerWriteBackReview:
		return Capabilities{CapFiles, CapCompare, CapCommit, CapReview}
	case ConsumerFetch:
		return Capabilities{CapGitAccess}
	case ConsumerRunnerGit:
		return Capabilities{CapRepoToken}
	case ConsumerSelfService:
		return Capabilities{CapFiles}
	case ConsumerCauseDiff:
		return Capabilities{CapCompare}
	}

	return nil
}
