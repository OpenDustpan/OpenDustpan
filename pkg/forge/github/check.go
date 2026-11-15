package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
)

// Names of the headers that GitHub sends, in the canonical form that the
// header map uses. The names are not case sensitive on the wire.
const (
	headerTokenExpiry = "Github-Authentication-Token-Expiration"
	headerScopes      = "X-Oauth-Scopes"
	headerRetryAfter  = "Retry-After"
	headerLimitLeft   = "X-Ratelimit-Remaining"
	headerLimitReset  = "X-Ratelimit-Reset"
)

// tokenExpiryLayouts are the forms of the token expiry header that the
// adapter reads. An expiry in another form counts as unknown.
//
//nolint:gochecknoglobals // A fixed list; Go has no constant slices.
var tokenExpiryLayouts = []string{"2006-01-02 15:04:05 MST", time.RFC3339}

// response is the answer of GitHub to one request. The body is read and
// limited.
type response struct {
	header http.Header
	body   []byte
	status int
}

// Check calls GitHub and returns the account of the credential. For an app
// it signs a JWT, reads the installation, and asks for an installation
// token to prove that the installation can make one. For a token it reads
// the user of the token and the expiry that GitHub gives it.
func (f *gitHub) Check(ctx context.Context) (forge.Account, error) {
	if f.auth == forge.AuthApp {
		return f.checkApp(ctx)
	}

	return f.checkToken(ctx)
}

// learn keeps the login of the account of the installation and whether the
// installation can read and write the contents, for the reads and for Capabilities.
func (f *gitHub) learn(login string, permissions map[string]string) {
	read, write := contentsMissing, contentsMissing
	if level := permissions[permContents]; level == levelRead || level == levelWrite {
		read = contentsGranted
	}

	if permissions[permContents] == levelWrite {
		write = contentsGranted
	}

	pulls := contentsMissing
	if permissions[permPulls] == levelWrite {
		pulls = contentsGranted
	}

	statuses := contentsMissing
	if permissions[permStatuses] == levelWrite {
		statuses = contentsGranted
	}

	issues := contentsMissing
	if permissions[permIssues] == levelWrite {
		issues = contentsGranted
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.owner, f.contents, f.writes, f.pulls, f.statuses, f.issues = login, read, write, pulls, statuses, issues
}

// The names of the permission and of its levels that the adapter reads.
const (
	permContents = "contents"
	permMetadata = "metadata"
	permPulls    = "pull_requests"
	permStatuses = "statuses"
	permIssues   = "issues"
	levelRead    = "read"
	levelWrite   = "write"
	// levelReview is the level of a token for the review request calls. It is
	// not a permission level of GitHub: it asks for the write of the contents
	// and of the pull requests.
	levelReview = "review"
	// levelReviewRead is the level of a token for a review request call that
	// only reads: the read of the contents and of the pull requests.
	levelReviewRead = "review-read"
	// levelStatus is the level of a token for the call that sets a commit
	// status. It is not a permission level of GitHub: it asks for the write of
	// the commit statuses and nothing else.
	levelStatus = "status"
	// levelComment is the level of a token for the calls that write comments
	// and reactions. It is not a permission level of GitHub: it asks for the
	// write of the issues and nothing else.
	levelComment = "comment"
	// levelCommentRead is the level of a token for the call that reads a
	// comment: the read of the issues.
	levelCommentRead = "comment-read"
)

// installation is the part of the installation answer that Check reads.
type installation struct {
	Account     account           `json:"account"`
	Permissions map[string]string `json:"permissions"`
}

// account is a GitHub account in an answer. The ID is a number.
type account struct {
	ID    json.Number `json:"id"`
	Login string      `json:"login"`
}

func (f *gitHub) checkApp(ctx context.Context) (forge.Account, error) {
	jwt, err := signJWT(f.key, f.appID, f.o.now())
	if err != nil {
		return forge.Account{}, err
	}

	path := "/app/installations/" + f.installationID

	if spendErr := f.spendApp(); spendErr != nil {
		return forge.Account{}, spendErr
	}

	resp, err := f.do(ctx, http.MethodGet, path, jwt, nil)
	if err != nil {
		return forge.Account{}, err
	}

	var inst installation
	if decodeErr := decode(resp.body, &inst); decodeErr != nil {
		return forge.Account{}, decodeErr
	}

	acct, err := accountOf(inst.Account)
	if err != nil {
		return forge.Account{}, err
	}

	if probeErr := f.probeToken(ctx, path, jwt); probeErr != nil {
		return forge.Account{}, probeErr
	}

	acct.Scopes = scopesOf(inst.Permissions)
	f.learn(acct.Login, inst.Permissions)

	return acct, nil
}

// checkTokenBody asks for the smallest permission that GitHub gives a token:
// read of the metadata. The token proves that the installation can make one,
// and the check needs nothing more.
const checkTokenBody = `{"permissions":{"metadata":"read"}}`

// revokeTimeout is the longest time that the revoke of the check token takes.
const revokeTimeout = 10 * time.Second

// probeToken proves that the key can make an installation token. It asks for
// the smallest permission, reads no field of the answer except the token, and
// revokes the token at once. The adapter never keeps or logs the token.
func (f *gitHub) probeToken(ctx context.Context, path, jwt string) error {
	if err := f.spendApp(); err != nil {
		return err
	}

	resp, err := f.do(ctx, http.MethodPost, path+"/access_tokens", jwt, []byte(checkTokenBody))
	if err != nil {
		return err
	}

	var minted struct {
		Token string `json:"token"`
	}

	if decodeErr := decode(resp.body, &minted); decodeErr != nil {
		return decodeErr
	}

	if minted.Token == "" {
		return forge.Unavailable(0, "GitHub gave an answer with no token")
	}

	// The revoke does not depend on the context of the caller: a canceled
	// call must not leave a live token. A failure does not change the answer,
	// because the token expires in one hour and holds the metadata permission
	// only.
	revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revokeTimeout)
	defer cancel()

	//nolint:errcheck // Best effort, see above.
	_, _ = f.do(revokeCtx, http.MethodDelete, "/installation/token", minted.Token, nil)

	return nil
}

func (f *gitHub) checkToken(ctx context.Context) (forge.Account, error) {
	resp, err := f.do(ctx, http.MethodGet, "/user", f.token, nil)
	if err != nil {
		return forge.Account{}, err
	}

	var user account
	if decodeErr := decode(resp.body, &user); decodeErr != nil {
		return forge.Account{}, decodeErr
	}

	acct, err := accountOf(user)
	if err != nil {
		return forge.Account{}, err
	}

	if raw := resp.header.Get(headerTokenExpiry); raw == "" {
		acct.NoExpiry = true
	} else if at, ok := parseExpiry(raw); ok {
		acct.ExpiresAt = at
	}

	for s := range strings.SplitSeq(resp.header.Get(headerScopes), ",") {
		if s = strings.TrimSpace(s); s != "" {
			acct.Scopes = append(acct.Scopes, s)
		}
	}

	return acct, nil
}

// parseExpiry reads the expiry header. It returns false for a form that it
// does not know.
func parseExpiry(raw string) (time.Time, bool) {
	for _, layout := range tokenExpiryLayouts {
		if at, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return at.UTC(), true
		}
	}

	return time.Time{}, false
}

// accountOf checks and converts an account of an answer.
func accountOf(a account) (forge.Account, error) {
	if !validID(a.ID.String()) || a.Login == "" {
		return forge.Account{}, forge.Unavailable(0, "GitHub gave an answer with no account")
	}

	return forge.Account{ID: a.ID.String(), Login: a.Login, ExpiresAt: time.Time{}, NoExpiry: false, Scopes: nil}, nil
}

// scopesOf lists the permissions of an installation as "name:level", in
// order.
func scopesOf(permissions map[string]string) []string {
	out := make([]string, 0, len(permissions))
	for name, level := range permissions {
		out = append(out, name+":"+level)
	}

	slices.Sort(out)

	return out
}

// decode reads one JSON value from body. It returns forge.ErrUnavailable
// for a body that does not parse, with no part of the body in the text.
func decode(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	if err := dec.Decode(v); err != nil {
		return forge.Unavailable(0, "GitHub gave an answer that does not parse")
	}

	return nil
}

// call describes one request to GitHub.
type call struct {
	method string
	path   string
	// bearer is the credential. It is never put in an error.
	bearer string
	body   []byte
	// status maps a status that is not a success to an error. Nil means
	// statusError.
	status func(response) error
	// oversize is the error for an answer above the body limit. Nil means a
	// GitHub that gave an answer that cannot be read.
	oversize error
	// target is the full address, for a call that is not under the API
	// address. Empty means the API address and path.
	target string
}

// do sends one request to GitHub with the bearer credential bearer and
// returns the answer, or an error that wraps a sentinel of the port. The
// text of an error holds no part of the credential or of the answer.
func (f *gitHub) do(ctx context.Context, method, path, bearer string, body []byte) (response, error) {
	return f.send(ctx, call{
		method: method, path: path, bearer: bearer, body: body, status: nil, oversize: nil, target: "",
	})
}

// send sends c, as do does.
func (f *gitHub) send(ctx context.Context, c call) (response, error) {
	target := f.base + c.path
	if c.target != "" {
		target = c.target
	}

	var reader io.Reader
	if c.body != nil {
		reader = bytes.NewReader(c.body)
	}

	req, err := http.NewRequestWithContext(ctx, c.method, target, reader)
	if err != nil {
		return response{}, fmt.Errorf("%w: the GitHub request does not build", forge.ErrInvalid)
	}

	req.Header.Set("Authorization", "Bearer "+c.bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-Github-Api-Version", "2022-11-28")

	if c.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := f.o.client.Do(req)
	if err != nil {
		return response{}, c.clientError(err)
	}

	return f.answer(c, resp)
}

// clientError maps an error of the HTTP client. A read maps the status of a
// redirect that was refused, for example a repository that has a new name.
// The adapter did not follow the redirect.
func (c call) clientError(err error) error {
	var redirect *redirectError
	if c.status != nil && errors.As(err, &redirect) {
		return c.status(response{status: redirect.status, header: nil, body: nil})
	}

	return networkError(err)
}

// answer reads the body of resp and maps a status that is not a success. It
// closes the body.
func (f *gitHub) answer(c call, resp *http.Response) (response, error) {
	data, readErr := egresshttp.ReadBody(resp)
	closeErr := resp.Body.Close()

	if readErr != nil {
		if c.oversize != nil && errors.Is(readErr, egresshttp.ErrTooLarge) {
			return response{}, c.oversize
		}

		return response{}, forge.Unavailable(0, "GitHub gave an answer that cannot be read")
	}

	if closeErr != nil {
		return response{}, forge.Unavailable(0, "GitHub gave an answer that cannot be read")
	}

	out := response{status: resp.StatusCode, header: resp.Header, body: data}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if c.status != nil {
			return response{}, c.status(out)
		}

		return response{}, f.statusError(out)
	}

	return out, nil
}

// networkError maps an error of the HTTP client. An address that the rules
// refuse is a bad connection, and any other error is a GitHub that does
// not answer.
func networkError(err error) error {
	switch {
	case errors.Is(err, egress.ErrRefused):
		return fmt.Errorf("%w: the outbound rules refuse the GitHub address", forge.ErrInvalid)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return forge.Unavailable(0, "the call to GitHub did not end in time")
	default:
		var uerr *url.Error
		if errors.As(err, &uerr) && uerr.Timeout() {
			return forge.Unavailable(0, "the call to GitHub timed out")
		}

		return forge.Unavailable(0, "GitHub did not answer")
	}
}

// statusError maps a status that is not a success to a sentinel.
func (f *gitHub) statusError(r response) error {
	switch {
	case r.status == http.StatusTooManyRequests, r.status == http.StatusForbidden && limited(r.header):
		return forge.Unavailable(f.waitTime(r.header), "GitHub limited the rate of the connection")
	case r.status == http.StatusUnauthorized, r.status == http.StatusForbidden, r.status == http.StatusNotFound:
		return fmt.Errorf("%w: GitHub refused the credential", forge.ErrAuth)
	case r.status >= http.StatusInternalServerError:
		return forge.Unavailable(0, "GitHub answered a server error")
	default:
		return forge.Unavailable(0, "GitHub gave the status "+strconv.Itoa(r.status))
	}
}

// limited reports whether the headers show a rate limit.
func limited(h http.Header) bool {
	return h.Get(headerRetryAfter) != "" || h.Get(headerLimitLeft) == "0"
}

// maxWait is the longest wait that waitTime returns. The rate limit of
// GitHub resets each hour, so a longer wait is not a value to trust.
const maxWait = time.Hour

// waitTime returns the time that GitHub asks the caller to wait, from
// Retry-After or from the reset time of the rate limit, up to maxWait. It
// returns zero when GitHub gave none.
func (f *gitHub) waitTime(h http.Header) time.Duration {
	if s, err := strconv.ParseInt(h.Get(headerRetryAfter), 10, 64); err == nil && s > 0 {
		// Compare the seconds before the multiplication, because a large
		// count overflows time.Duration.
		if s > int64(maxWait/time.Second) {
			return maxWait
		}

		return time.Duration(s) * time.Second
	}

	if reset, err := strconv.ParseInt(h.Get(headerLimitReset), 10, 64); err == nil {
		if d := time.Unix(reset, 0).Sub(f.o.now()); d > 0 {
			return min(d, maxWait)
		}
	}

	return 0
}
