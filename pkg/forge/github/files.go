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
		body: nil, status: f.notFound, oversize: nil, target: "",
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
		bearer: rd.bearer, body: nil, status: f.notFound, oversize: tooLarge(rd.limit), target: "",
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
			body: nil, status: f.notFound, oversize: tooLarge(rd.limit), target: "",
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
// port does too. A file that the contents call does not serve answers 403
// with the error code too_large, and a repository that has a new name answers
// a redirect, which the adapter does not follow.
func (f *gitHub) notFound(r response) error {
	switch {
	case r.status == http.StatusNotFound || r.status == http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: GitHub has no such repository, ref or file", forge.ErrNotFound)
	case r.status == http.StatusForbidden && hasErrorCode(r.body, "too_large"):
		return fmt.Errorf("%w: GitHub does not serve a file of this size through the API", forge.ErrTooLarge)
	case r.status == http.StatusMovedPermanently || r.status == http.StatusTemporaryRedirect ||
		r.status == http.StatusPermanentRedirect:
		return fmt.Errorf("%w: the repository moved or has a new name; use its new path", forge.ErrNotFound)
	}

	return f.statusError(r)
}

// errorBody is the form of the errors in an answer of GitHub.
type errorBody struct {
	Message string      `json:"message"`
	Errors  []errorCode `json:"errors"`
}

type errorCode struct {
	Code string `json:"code"`
}

// hasErrorCode reports whether the answer lists the error code.
func hasErrorCode(body []byte, code string) bool {
	var e errorBody
	if decode(body, &e) != nil {
		return false
	}

	for _, c := range e.Errors {
		if c.Code == code {
			return true
		}
	}

	return false
}

// mintStatus returns the map of the status of the request for an installation
// token with the permission level. GitHub answers 422 for a repository that
// the installation cannot see, and also for a permission that the installation
// does not have: the first is a missing repository. The second is a credential
// that cannot read the contents (ErrAuth), or that cannot write them
// (ErrRefused, because the push is what the installation does not allow).
func (f *gitHub) mintStatus(level string) func(response) error {
	return func(r response) error {
		if r.status != http.StatusUnprocessableEntity {
			return f.notFound(r)
		}

		var e errorBody
		if decode(r.body, &e) == nil && strings.Contains(e.Message, "permissions requested") {
			missing := "the installation does not have the permission to " + missingPermission(level)
			if level == levelWrite || level == levelReview || level == levelStatus || level == levelComment {
				return fmt.Errorf("%w: %s", forge.ErrRefused, missing)
			}

			return fmt.Errorf("%w: %s", forge.ErrAuth, missing)
		}

		return fmt.Errorf("%w: the installation cannot see the repository", forge.ErrNotFound)
	}
}

// missingPermission names the permission that a token of the level asks for, for
// the text of an error. The levels for review requests name the pull requests
// too, so that the text tells which permission of the app is missing.
func missingPermission(level string) string {
	switch level {
	case levelStatus:
		return "write the commit statuses"
	case levelComment:
		return "write the issues"
	case levelCommentRead:
		return "read the issues"
	case levelReview:
		return "write the contents and the pull requests"
	case levelReviewRead:
		return "read the contents and the pull requests"
	case levelWrite:
		return "write the contents"
	default:
		return "read the contents"
	}
}

// MaxReadTokens is the most installation tokens that an app connection keeps
// for reads, one for each repository. When the adapter needs another, it
// drops the tokens that expired, and then the token that expires first.
const MaxReadTokens = 64

// readBearer returns the credential for a read of the repository r: the token
// of a token connection, or an installation token that reads that repository
// only.
func (f *gitHub) readBearer(ctx context.Context, r repoName) (string, error) {
	return f.bearer(ctx, r, levelRead)
}

// writeBearer returns the credential for a write to the repository r: the
// token of a token connection, or an installation token that writes the
// contents of that repository only.
func (f *gitHub) writeBearer(ctx context.Context, r repoName) (string, error) {
	return f.bearer(ctx, r, levelWrite)
}

// reviewBearer returns the credential for the review request calls on the
// repository r: the token of a token connection, or an installation token that
// writes the contents and the pull requests of that repository only.
func (f *gitHub) reviewBearer(ctx context.Context, r repoName) (string, error) {
	return f.bearer(ctx, r, levelReview)
}

// reviewReadBearer returns the credential for the review request calls that only
// read: the token of a token connection, or an installation token that reads the
// contents and the pull requests of that repository only.
func (f *gitHub) reviewReadBearer(ctx context.Context, r repoName) (string, error) {
	return f.bearer(ctx, r, levelReviewRead)
}

// hasOwnKey reports whether the tokens of the level have a key of their own,
// apart from the token that reads the contents.
func hasOwnKey(level string) bool {
	switch level {
	case levelWrite, levelReview, levelReviewRead, levelStatus, levelComment, levelCommentRead:
		return true
	default:
		return false
	}
}

// tokenKey is the key of the installation token of repository r at level in
// the token map. A repository name has no "#", so the keys of two levels never
// meet.
func tokenKey(r repoName, level string) string {
	key := r.owner + "/" + r.name
	if hasOwnKey(level) {
		key += "#" + level
	}

	return key
}

// bearer returns the credential for an access to the repository r at level.
func (f *gitHub) bearer(ctx context.Context, r repoName, level string) (string, error) {
	if f.auth != forge.AuthApp {
		return f.token, nil
	}

	key := tokenKey(r, level)

	f.mu.Lock()
	t, found := f.readTokens[key]
	f.mu.Unlock()

	if found && t.expires.Sub(f.o.now()) > readTokenSkew {
		return t.value, nil
	}

	// Defense in depth: the token request names the repository, and GitHub
	// refuses a repository that the installation cannot see. This check stays
	// so that a connection never asks for a repository of another account,
	// also when the app of the connection is installed there.
	owner, err := f.installationOwner(ctx)
	if err != nil {
		return "", err
	}

	if !strings.EqualFold(owner, r.owner) {
		return "", fmt.Errorf("%w: the repository is not in the account of the installation", forge.ErrNotFound)
	}

	t, err = f.mintToken(ctx, r.name, level)
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	f.keepReadToken(key, t)
	f.mu.Unlock()

	return t.value, nil
}

// keepReadToken stores t under key and keeps the number of tokens at
// MaxReadTokens or less. The caller holds the lock.
func (f *gitHub) keepReadToken(key string, t readToken) {
	if _, found := f.readTokens[key]; !found {
		for len(f.readTokens) >= MaxReadTokens {
			f.dropReadToken()
		}
	}

	f.readTokens[key] = t
}

// dropReadToken drops a token that expired, or else the token that expires
// first. The caller holds the lock.
func (f *gitHub) dropReadToken() {
	var oldest string

	for key, t := range f.readTokens {
		if oldest == "" || t.expires.Before(f.readTokens[oldest].expires) {
			oldest = key
		}
	}

	delete(f.readTokens, oldest)
}

// installationOwner returns the login of the account of the installation. It
// reads the installation once, unless Check read it.
func (f *gitHub) installationOwner(ctx context.Context) (string, error) {
	f.mu.Lock()
	owner := f.owner
	f.mu.Unlock()

	if owner != "" {
		return owner, nil
	}

	jwt, err := signJWT(f.key, f.appID, f.o.now())
	if err != nil {
		return "", err
	}

	if spendErr := f.spendApp(); spendErr != nil {
		return "", spendErr
	}

	resp, err := f.do(ctx, http.MethodGet, "/app/installations/"+f.installationID, jwt, nil)
	if err != nil {
		return "", err
	}

	var inst installation
	if decodeErr := decode(resp.body, &inst); decodeErr != nil {
		return "", decodeErr
	}

	acct, err := accountOf(inst.Account)
	if err != nil {
		return "", err
	}

	f.learn(acct.Login, inst.Permissions)

	return acct.Login, nil
}

// mintToken asks for an installation token with the permission level (read or
// write) on the contents of the repository name, and nothing else. It refuses a
// token that GitHub makes with more permission, or for more repositories, than
// it asked for.
func (f *gitHub) mintToken(ctx context.Context, name, level string) (readToken, error) {
	jwt, err := signJWT(f.key, f.appID, f.o.now())
	if err != nil {
		return readToken{}, err
	}

	body, err := json.Marshal(readTokenRequest{Repositories: []string{name}, Permissions: tokenPermissions(level)})
	if err != nil {
		return readToken{}, fmt.Errorf("%w: the GitHub token request does not encode", forge.ErrInvalid)
	}

	if spendErr := f.spendApp(); spendErr != nil {
		return readToken{}, spendErr
	}

	resp, err := f.send(ctx, call{
		method: http.MethodPost, path: "/app/installations/" + f.installationID + "/access_tokens", bearer: jwt,
		body: body, status: f.mintStatus(level), oversize: nil, target: "",
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

	if grantErr := checkGranted(fields, name, level); grantErr != nil {
		return readToken{}, grantErr
	}

	expires := f.o.now().Add(readTokenFallback)

	var at time.Time
	if unmarshalErr := json.Unmarshal(fields[keyExpiresAt], &at); unmarshalErr == nil && !at.IsZero() {
		expires = at
	}

	return readToken{value: value, expires: expires}, nil
}

// tokenPermissions returns the permissions that a token of the level asks for.
// The level review is the write of the contents, which the branch of a review
// request needs, and the write of the pull requests.
func tokenPermissions(level string) map[string]string {
	switch level {
	case levelReview:
		return map[string]string{permContents: levelWrite, permPulls: levelWrite}
	case levelReviewRead:
		return map[string]string{permContents: levelRead, permPulls: levelRead}
	case levelStatus:
		return map[string]string{permStatuses: levelWrite}
	case levelComment:
		return map[string]string{permIssues: levelWrite}
	case levelCommentRead:
		return map[string]string{permIssues: levelRead}
	}

	return map[string]string{permContents: level}
}

// Fields of the answer to a token request.
const (
	keyPermissions  = "permissions"
	keySelection    = "repository_selection"
	keyRepositories = "repositories"
	selectionChosen = "selected"
)

// checkGranted refuses an answer whose token is wider than the request: a
// permission other than the contents at level (GitHub adds the read of the
// metadata), a level above the request, or a repository other than name.
//
// Defense in depth: GitHub applies the request and the installation applies
// its limits. This check stays so that an answer that is wider than the
// request never becomes a token that the adapter uses.
func checkGranted(fields map[string]json.RawMessage, name, level string) error {
	wide := forge.Unavailable(0, "GitHub gave a token that is wider than the request")

	var perms map[string]string
	if err := json.Unmarshal(fields[keyPermissions], &perms); err != nil {
		return wide
	}

	asked := tokenPermissions(level)

	for perm, got := range perms {
		if (asked[perm] == "" || got != asked[perm]) && (perm != permMetadata || got != levelRead) {
			return wide
		}
	}

	var selection string
	if err := json.Unmarshal(fields[keySelection], &selection); err != nil || selection != selectionChosen {
		return wide
	}

	var repos []repositoryRef
	if err := json.Unmarshal(fields[keyRepositories], &repos); err != nil || len(repos) != 1 || repos[0].Name != name {
		return wide
	}

	return nil
}

type repositoryRef struct {
	Name string `json:"name"`
}

// forget drops the installation tokens of owner/name after GitHub refused one,
// so that the next call asks for a new one. It returns err.
func (f *gitHub) forget(r repoName, err error) error {
	if f.auth == forge.AuthApp && errors.Is(err, forge.ErrAuth) {
		f.mu.Lock()
		delete(f.readTokens, tokenKey(r, levelRead))
		delete(f.readTokens, tokenKey(r, levelWrite))
		delete(f.readTokens, tokenKey(r, levelReview))
		delete(f.readTokens, tokenKey(r, levelReviewRead))
		delete(f.readTokens, tokenKey(r, levelStatus))
		delete(f.readTokens, tokenKey(r, levelComment))
		delete(f.readTokens, tokenKey(r, levelCommentRead))
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
