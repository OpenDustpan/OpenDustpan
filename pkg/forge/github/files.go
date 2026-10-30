package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

// MaxFileBytes is the largest file that ReadFile returns, also when the
// caller gives a larger limit. The answer of GitHub holds the file in base64
// inside a body that the adapter reads up to the body limit of the egress
// rules (4 MiB), so a file of 2 MiB is the largest that always fits.
const MaxFileBytes = 2 << 20

// Limits of the names that the adapter sends to GitHub.
const (
	maxRepoPartBytes = 100
	maxPathBytes     = 4096
	maxRefBytes      = 255
)

// readTokenSkew is how long before its expiry the adapter stops to use an
// installation token for reads.
const readTokenSkew = time.Minute

// readTokenFallback is the life that the adapter assumes for an installation
// token when GitHub gives no usable expiry.
const readTokenFallback = 5 * time.Minute

// keyExpiresAt is the field of the answer that gives the expiry of an
// installation token.
const keyExpiresAt = "expires_at"

// readToken is an installation token for the reads of one repository.
type readToken struct {
	value   string
	expires time.Time
}

// readTokenRequest asks for a token that reads the contents of one
// repository and nothing else.
type readTokenRequest struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

// Head returns the commit at the head of the branch or tag ref. It returns
// forge.ErrNotFound for a repository or a ref that does not exist, and for a
// repository that the credential cannot see, and forge.ErrInvalid for a name
// that fails the checks of the adapter, before any call.
func (f *gitHub) Head(ctx context.Context, repo, ref string) (forge.Commit, error) {
	r, err := splitRepo(repo)
	if err != nil {
		return forge.Commit{}, err
	}

	if refErr := validateRef(ref); refErr != nil {
		return forge.Commit{}, refErr
	}

	bearer, err := f.readBearer(ctx, r)
	if err != nil {
		return forge.Commit{}, err
	}

	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/commits/" + escapePath(ref), bearer: bearer,
		body: nil, status: f.notFound, oversize: nil,
	})
	if err != nil {
		return forge.Commit{}, f.forget(r, err)
	}

	var c commitAnswer
	if decodeErr := decode(resp.body, &c); decodeErr != nil {
		return forge.Commit{}, decodeErr
	}

	if forge.ValidateCommitID(c.SHA) != nil || c.Commit.Committer.Date.IsZero() {
		return forge.Commit{}, forge.Unavailable(0, "GitHub gave an answer with no commit")
	}

	return forge.Commit{ID: c.SHA, Time: c.Commit.Committer.Date.UTC()}, nil
}

// commitAnswer is the part of the commit answer that Head reads.
type commitAnswer struct {
	SHA    string         `json:"sha"`
	Commit commitInnerRef `json:"commit"`
}

type commitInnerRef struct {
	Committer committerRef `json:"committer"`
}

type committerRef struct {
	Date time.Time `json:"date"`
}

// contents is the answer of GitHub for one path at a commit.
type contents struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Size     int64  `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

// ReadFile returns the file at filePath in commit, which is a full commit ID.
// It returns forge.ErrNotFound for a missing repository, commit or file, and
// for a path that is a directory, a link or a submodule. It returns
// forge.ErrTooLarge for a file above limit bytes, or above MaxFileBytes,
// before the adapter reads the content, and forge.ErrInvalid for a name, a
// commit ID, a path or a limit that fails the checks of the adapter, before
// any call.
func (f *gitHub) ReadFile(ctx context.Context, repo, commit, filePath string, limit int64) (forge.File, error) {
	r, err := splitRepo(repo)
	if err != nil {
		return forge.File{}, err
	}

	if idErr := forge.ValidateCommitID(commit); idErr != nil {
		return forge.File{}, fmt.Errorf("the commit: %w", idErr)
	}

	if pathErr := validateFilePath(filePath); pathErr != nil {
		return forge.File{}, pathErr
	}

	if limit <= 0 {
		return forge.File{}, fmt.Errorf("%w: the size limit of a file read must be above zero", forge.ErrInvalid)
	}

	bearer, err := f.readBearer(ctx, r)
	if err != nil {
		return forge.File{}, err
	}

	file, err := f.readContents(ctx, fileRead{
		base: r.path(), bearer: bearer, commit: commit, path: filePath, limit: min(limit, MaxFileBytes),
	})
	if err != nil {
		return forge.File{}, f.forget(r, err)
	}

	return file, nil
}

// fileRead describes one read of a file. The limit is below MaxFileBytes.
type fileRead struct {
	// base is the path of the repository in the API.
	base   string
	bearer string
	commit string
	path   string
	limit  int64
}

// tooLarge is the error for a file above the limit.
func tooLarge(limit int64) error {
	return fmt.Errorf("%w: the file has more than %d bytes", forge.ErrTooLarge, limit)
}

func (f *gitHub) readContents(ctx context.Context, rd fileRead) (forge.File, error) {
	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: rd.base + "/contents/" + escapePath(rd.path) + "?ref=" + rd.commit,
		bearer: rd.bearer, body: nil, status: f.notFound, oversize: tooLarge(rd.limit),
	})
	if err != nil {
		return forge.File{}, err
	}

	// A directory answers a list.
	if trimmed := strings.TrimSpace(string(resp.body)); strings.HasPrefix(trimmed, "[") {
		return forge.File{}, fmt.Errorf("%w: the path is a directory", forge.ErrNotFound)
	}

	var c contents
	if decodeErr := decode(resp.body, &c); decodeErr != nil {
		return forge.File{}, decodeErr
	}

	if c.Type != "file" {
		return forge.File{}, fmt.Errorf("%w: the path is not a file", forge.ErrNotFound)
	}

	// Defense in depth: the size is checked here before the content is
	// decoded, and again after the decode. The cap of the body is a third
	// layer. None relies on another.
	if c.Size < 0 || c.Size > rd.limit {
		return forge.File{}, tooLarge(rd.limit)
	}

	if forge.ValidateCommitID(c.SHA) != nil || c.Path != rd.path {
		return forge.File{}, forge.Unavailable(0, "GitHub gave an answer for another file")
	}

	data, err := f.fileBytes(ctx, rd, c)
	if err != nil {
		return forge.File{}, err
	}

	if int64(len(data)) > rd.limit {
		return forge.File{}, tooLarge(rd.limit)
	}

	if int64(len(data)) != c.Size {
		return forge.File{}, forge.Unavailable(0, "GitHub gave a file that does not have its size")
	}

	return forge.File{Path: rd.path, Content: data, BlobID: c.SHA}, nil
}

// fileBytes returns the content of the file in c. GitHub gives it inline up
// to 1 MiB. For a larger file, it gives the encoding "none", and the adapter
// reads the blob.
func (f *gitHub) fileBytes(ctx context.Context, rd fileRead, c contents) ([]byte, error) {
	switch c.Encoding {
	case "base64":
		return decodeBase64(c.Content)
	case "none":
		resp, err := f.send(ctx, call{
			method: http.MethodGet, path: rd.base + "/git/blobs/" + c.SHA, bearer: rd.bearer,
			body: nil, status: f.notFound, oversize: tooLarge(rd.limit),
		})
		if err != nil {
			return nil, err
		}

		var b contents
		if decodeErr := decode(resp.body, &b); decodeErr != nil {
			return nil, decodeErr
		}

		if b.Encoding != "base64" || b.Size != c.Size || b.SHA != c.SHA {
			return nil, forge.Unavailable(0, "GitHub gave a blob that does not match the file")
		}

		return decodeBase64(b.Content)
	default:
		return nil, forge.Unavailable(0, "GitHub gave a file in an encoding that the adapter does not know")
	}
}

// decodeBase64 reads the base64 content of GitHub, which has a newline after
// each 60 characters.
func decodeBase64(s string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(s))
	if err != nil {
		return nil, forge.Unavailable(0, "GitHub gave a file that does not decode")
	}

	return data, nil
}

// notFound maps the status of a read. A repository, a ref or a file that is
// not there answers 404, and a ref that no commit has answers 422. GitHub
// answers 404 also for a repository that the credential cannot see, so the
// port does too.
func (f *gitHub) notFound(r response) error {
	if r.status == http.StatusNotFound || r.status == http.StatusUnprocessableEntity {
		return fmt.Errorf("%w: GitHub has no such repository, ref or file", forge.ErrNotFound)
	}

	return f.statusError(r)
}

// readBearer returns the credential for a read of owner/name: the token of a
// token connection, or an installation token that reads that repository only.
func (f *gitHub) readBearer(ctx context.Context, r repoName) (string, error) {
	if f.auth != forge.AuthApp {
		return f.token, nil
	}

	key := r.owner + "/" + r.name

	f.mu.Lock()
	t, found := f.readTokens[key]
	f.mu.Unlock()

	if found && t.expires.Sub(f.o.now()) > readTokenSkew {
		return t.value, nil
	}

	t, err := f.mintReadToken(ctx, r.name)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	f.readTokens[key] = t
	f.mu.Unlock()

	return t.value, nil
}

// mintReadToken asks for an installation token with the permission to read
// the contents of the repository name, and nothing else.
func (f *gitHub) mintReadToken(ctx context.Context, name string) (readToken, error) {
	jwt, err := signJWT(f.key, f.appID, f.o.now())
	if err != nil {
		return readToken{}, err
	}

	body, err := json.Marshal(readTokenRequest{
		Repositories: []string{name}, Permissions: map[string]string{"contents": "read"},
	})
	if err != nil {
		return readToken{}, fmt.Errorf("%w: the GitHub token request does not encode", forge.ErrInvalid)
	}

	resp, err := f.send(ctx, call{
		method: http.MethodPost, path: "/app/installations/" + f.installationID + "/access_tokens", bearer: jwt,
		body: body, status: f.notFound, oversize: nil,
	})
	if err != nil {
		return readToken{}, err
	}

	var fields map[string]json.RawMessage
	if decodeErr := decode(resp.body, &fields); decodeErr != nil {
		return readToken{}, decodeErr
	}

	var value string
	if unmarshalErr := json.Unmarshal(fields["token"], &value); unmarshalErr != nil || value == "" {
		return readToken{}, forge.Unavailable(0, "GitHub gave an answer with no token")
	}

	expires := f.o.now().Add(readTokenFallback)

	var at time.Time
	if unmarshalErr := json.Unmarshal(fields[keyExpiresAt], &at); unmarshalErr == nil && !at.IsZero() {
		expires = at
	}

	return readToken{value: value, expires: expires}, nil
}

// forget drops the installation token of owner/name after GitHub refused it,
// so that the next read asks for a new one. It returns err.
func (f *gitHub) forget(r repoName, err error) error {
	if f.auth == forge.AuthApp && errors.Is(err, forge.ErrAuth) {
		f.mu.Lock()
		delete(f.readTokens, r.owner+"/"+r.name)
		f.mu.Unlock()
	}

	return err
}

// repoName is a repository path "owner/name" that passed splitRepo.
type repoName struct {
	owner string
	name  string
}

// path returns the path of the repository in the API.
func (r repoName) path() string {
	return "/repos/" + url.PathEscape(r.owner) + "/" + url.PathEscape(r.name)
}

// escapePath escapes each part of a path or of a ref that has slashes.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}

	return strings.Join(parts, "/")
}

// splitRepo checks a repository path "owner/name" with an allow list. Each
// part has 1 to 100 letters, digits, ".", "_" or "-", and is not "." or "..".
func splitRepo(repo string) (repoName, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || !validRepoPart(owner) || !validRepoPart(name) {
		return repoName{}, fmt.Errorf("%w: the repository path must have the form owner/name", forge.ErrInvalid)
	}

	return repoName{owner: owner, name: name}, nil
}

func validRepoPart(p string) bool {
	if p == "" || len(p) > maxRepoPartBytes || p == "." || p == ".." {
		return false
	}

	for _, r := range p {
		if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-", r)) {
			return false
		}
	}

	return true
}

// validateRef checks a branch or tag name with an allow list: letters,
// digits and "._-+/", with no empty part, no part that starts with "." or
// "-", no "..", and no part that ends with "." or ".lock".
//
// Defense in depth: the caller checks names too, and GitHub checks them. This
// check stays so that a new caller cannot send a name that changes the path
// of the request.
func validateRef(ref string) error {
	if !refRunesAllowed(ref) || strings.Contains(ref, "..") || strings.HasPrefix(ref, "refs/") ||
		!refPartsAllowed(ref) {
		return fmt.Errorf("%w: the ref is not a valid branch or tag name", forge.ErrInvalid)
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

// validateFilePath checks a file path in a repository with an allow list.
// Each rune is a graphic character, and the path has no backslash. The path
// is relative and clean, and no part is "..", or ".git" in any case.
//
// Defense in depth: the code that calls the port may check paths too, and
// GitHub reads a path inside the repository only. This check stays so that
// a path that leaves the repository never goes into a request.
func validateFilePath(p string) error {
	if !pathRunesAllowed(p) {
		return fmt.Errorf(
			"%w: the file path is empty, too long or has a character that is not allowed",
			forge.ErrInvalid,
		)
	}

	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return fmt.Errorf("%w: the file path is not relative and clean", forge.ErrInvalid)
	}

	for part := range strings.SplitSeq(p, "/") {
		switch {
		case part == "" || part == ".":
			return fmt.Errorf("%w: the file path is not relative and clean", forge.ErrInvalid)
		case part == "..":
			return fmt.Errorf("%w: the file path leaves the repository", forge.ErrInvalid)
		case strings.EqualFold(part, ".git"):
			return fmt.Errorf("%w: the file path has a .git part", forge.ErrInvalid)
		}
	}

	return nil
}

func pathRunesAllowed(p string) bool {
	if p == "" || p == "." || len(p) > maxPathBytes || !utf8.ValidString(p) {
		return false
	}

	for _, r := range p {
		if r == '\\' || !unicode.IsGraphic(r) {
			return false
		}
	}

	return true
}
