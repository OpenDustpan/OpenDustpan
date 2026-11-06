package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Limits of the comment calls.
const (
	// maxSearchedComments is the most comments that UpsertComment reads to find
	// its comment. A review request with more comments is forge.ErrTooLarge: a
	// search that stops early would make a second comment.
	maxSearchedComments = 1000
	// maxCommentIDDigits is the most digits in the ID of a comment.
	maxCommentIDDigits = 19
)

// Fields of the answer for a comment.
const (
	keyID       = "id"
	keyBody     = "body"
	keyLogin    = "login"
	keyIssueURL = "issue_url"
	keyContent  = "content"
)

// commentAllowed returns forge.ErrUnsupported for a connection that
// Capabilities does not list forge.CapComment for.
func (f *gitHub) commentAllowed(method string) error {
	if !f.Capabilities().Has(forge.CapComment) {
		return fmt.Errorf(
			"%w: GitHub %s needs a connection with push access and, for an app, the permission to write the issues",
			forge.ErrUnsupported, method,
		)
	}

	return nil
}

// commentStatus maps the status of a comment call. GitHub answers 410 for a
// repository that has issues turned off.
func (f *gitHub) commentStatus(r response) error {
	switch r.status {
	case http.StatusGone:
		return fmt.Errorf("%w: GitHub has comments turned off for this repository", forge.ErrRefused)
	case http.StatusNotFound:
		return fmt.Errorf("%w: GitHub has no such repository, review request or comment", forge.ErrNotFound)
	}

	return f.commitStatus(r)
}

// validCommentID checks the ID of a comment: a whole number above zero.
func validCommentID(id string) error {
	bad := id == "" || len(id) > maxCommentIDDigits || id[0] == '0'

	for i := 0; i < len(id) && !bad; i++ {
		bad = id[i] < '0' || id[i] > '9'
	}

	if bad {
		return fmt.Errorf("%w: the ID of a comment is a number", forge.ErrInvalid)
	}

	return nil
}

// UpsertComment edits the comment of the connection that has the marker of
// req on the pull request reviewID, or makes it. It reads the comments of the
// pull request page by page, and edits a comment only when its first line is
// the marker line and its author is the account of the connection.
//
// It returns the errors that forge.Comments names.
func (f *gitHub) UpsertComment(
	ctx context.Context, repo, reviewID string, req forge.CommentRequest,
) (forge.Comment, error) {
	if err := f.commentAllowed("UpsertComment"); err != nil {
		return forge.Comment{}, err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return forge.Comment{}, err
	}

	if idErr := validReviewID(reviewID); idErr != nil {
		return forge.Comment{}, idErr
	}

	text, cut, err := forge.CommentText(req)
	if err != nil {
		return forge.Comment{}, err
	}

	if checkErr := checkCommentText(text); checkErr != nil {
		return forge.Comment{}, checkErr
	}

	marker := firstLine(text)

	own, err := f.commitIdentity(ctx)
	if err != nil {
		return forge.Comment{}, err
	}

	bearer, err := f.bearer(ctx, r, levelComment)
	if err != nil {
		return forge.Comment{}, f.forget(r, err)
	}

	ownID, err := f.commentIdentityID(ctx, own, bearer)
	if err != nil {
		return forge.Comment{}, f.forget(r, err)
	}

	cw := commentWrite{
		repo: r, bearer: bearer, review: reviewID, text: text, marker: marker, own: own, ownID: ownID,
	}

	got, err := f.writeComment(ctx, cw)
	if err != nil {
		return forge.Comment{}, f.forget(r, err)
	}

	got.Truncated = cut

	return got, nil
}

// commentWrite describes one upsert.
type commentWrite struct {
	repo   repoName
	bearer string
	review string
	text   string
	marker string
	own    string
	// ownID is the numeric ID of the account of the connection. A comment is
	// the own comment only when the login and the ID both match.
	ownID string
}

// writeComment edits the comment of the connection with the marker, or makes it.
func (f *gitHub) writeComment(ctx context.Context, cw commentWrite) (forge.Comment, error) {
	id, err := f.findOwnComment(ctx, cw)
	if err != nil {
		return forge.Comment{}, err
	}

	body, err := json.Marshal(map[string]string{keyBody: cw.text})
	if err != nil {
		return forge.Comment{}, fmt.Errorf("%w: the GitHub comment request does not encode", forge.ErrInvalid)
	}

	method, path := http.MethodPost, cw.repo.path()+"/issues/"+cw.review+"/comments"

	if id != "" {
		method, path = http.MethodPatch, cw.repo.path()+"/issues/comments/"+id
	}

	resp, err := f.send(ctx, call{
		method: method, path: path, bearer: cw.bearer, body: body, status: f.commentStatus,
		oversize: nil, target: "",
	})
	if err != nil {
		return forge.Comment{}, err
	}

	got, login, err := parseComment(resp.body)
	if err != nil {
		return forge.Comment{}, err
	}

	// Defense in depth: GitHub edits the comment that the ID names, and the
	// search above chose an own comment. This check stays so that a comment
	// of another account, or one without the marker, is never the answer.
	if !cw.isOwn(login, got.AuthorID) || firstLine(got.Body) != cw.marker || (id != "" && got.ID != id) {
		return forge.Comment{}, forge.Unavailable(0, "GitHub gave a comment that does not match the request")
	}

	return got, nil
}

// commentPageSizes returns the page sizes of the search, from the largest. The
// first is the most that GitHub gives, and each size divides the size before it.
func commentPageSizes() []int { return []int{100, 25, 5, 1} }

// errPageTooBig is the error of a page of comments above the size limit of an
// answer. findOwnComment asks again with a smaller page.
var errPageTooBig = errors.New("a page of comments is above the size limit")

// findOwnComment returns the ID of the oldest comment of the pull request with
// the marker as its first line and the connection as its author, or an empty
// text when there is none.
//
// The comments of other accounts are untrusted and can be large: a page with
// many large comments is above the size limit of an answer. Then the search
// asks for the same comments again with a smaller page, so that no comment of
// another account stops the search. A page size divides the size before it, so
// the offset of the next comment is a whole number of pages for each size.
func (f *gitHub) findOwnComment(ctx context.Context, cw commentWrite) (string, error) {
	level, offset := 0, 0
	sizes := commentPageSizes()

	for offset < maxSearchedComments {
		size := sizes[level]

		resp, err := f.send(ctx, call{
			method: http.MethodGet, bearer: cw.bearer, body: nil, status: f.commentStatus, target: "",
			path: cw.repo.path() + "/issues/" + cw.review + "/comments?per_page=" + strconv.Itoa(size) +
				"&page=" + strconv.Itoa(offset/size+1),
			oversize: errPageTooBig,
		})
		if errors.Is(err, errPageTooBig) && level+1 < len(sizes) {
			level++

			continue
		}

		if errors.Is(err, errPageTooBig) {
			return "", forge.Unavailable(
				0, "no comment of the server was found: GitHub gave a comment above the size limit of an answer",
			)
		}

		if err != nil {
			return "", err
		}

		found, count, err := ownCommentIn(resp.body, cw)
		if err != nil || found != "" {
			return found, err
		}

		if count < size {
			return "", nil
		}

		offset += size
	}

	return "", fmt.Errorf("%w: the review request has more than %d comments", forge.ErrTooLarge, maxSearchedComments)
}

// ownCommentIn returns the ID of the first comment of a page that has the
// marker as its first line and the connection as its author, and the number of
// comments in the page.
//
// A comment of another account is untrusted: it can be large, or have a field
// that fails the strict checks of parseComment. The search reads only the login
// of the author and the first line of the text, and skips a comment that it
// cannot read, because such a comment cannot be the own comment. Only the
// comment that matches goes through parseComment, and it fails closed.
func ownCommentIn(body []byte, cw commentWrite) (string, int, error) {
	var items []json.RawMessage
	if err := decode(body, &items); err != nil {
		return "", 0, err
	}

	for _, raw := range items {
		if !looksOwn(raw, cw) {
			continue
		}

		got, login, err := parseComment(raw)
		if err != nil {
			return "", 0, err
		}

		// Defense in depth: looksOwn chose the comment by its author and its
		// first line. The strict parse checks the same two values again, so that
		// a comment that only the lenient reader accepts is never edited.
		if !cw.isOwn(login, got.AuthorID) || firstLine(got.Body) != cw.marker {
			return "", 0, forge.Unavailable(0, "GitHub gave a comment that the adapter cannot use")
		}

		return got.ID, len(items), nil
	}

	return "", len(items), nil
}

// isOwn reports whether the author with the login and the numeric ID is the
// account of the connection. Both must match: a login can pass to another
// account, and an ID cannot.
func (cw commentWrite) isOwn(login, authorID string) bool {
	return cw.ownID != "" && authorID == cw.ownID && strings.EqualFold(login, cw.own)
}

// looksOwn reports whether a comment of a page has the connection as its author
// (the login and the numeric ID) and the marker as its first line. It reads only
// these values, and a comment that it cannot read is not the own comment.
func looksOwn(raw json.RawMessage, cw commentWrite) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}

	var user map[string]json.RawMessage
	if json.Unmarshal(fields[keyUser], &user) != nil {
		return false
	}

	var number wholeNumber
	if json.Unmarshal(user[keyID], &number) != nil {
		return false
	}

	authorID, ok := positiveNumber(number)

	return ok && cw.isOwn(textField(user, keyLogin), authorID) && firstLine(textField(fields, keyBody)) == cw.marker
}

// commentIdentityID returns the numeric ID of the account of the connection,
// which is the account with the login. It asks GitHub once and keeps the answer
// in memory. The call needs no permission, and it works for the bot account of an
// app and for the user of a token.
func (f *gitHub) commentIdentityID(ctx context.Context, login, bearer string) (string, error) {
	f.mu.Lock()
	known := f.identityID
	f.mu.Unlock()

	if known != "" {
		return known, nil
	}

	resp, err := f.do(ctx, http.MethodGet, "/users/"+url.PathEscape(login), bearer, nil)
	if err != nil {
		return "", err
	}

	var fields map[string]json.RawMessage
	if decodeErr := decode(resp.body, &fields); decodeErr != nil {
		return "", decodeErr
	}

	var number wholeNumber

	id, ok := "", json.Unmarshal(fields[keyID], &number) == nil
	if ok {
		id, ok = positiveNumber(number)
	}

	// Defense in depth: the path names the login. This check stays so that an
	// answer for another account is never the identity that decides which
	// comment is edited.
	if !ok || !strings.EqualFold(textField(fields, keyLogin), login) {
		return "", forge.Unavailable(0, "GitHub gave an account that does not match the login of the connection")
	}

	f.mu.Lock()
	f.identityID = id
	f.mu.Unlock()

	return id, nil
}

// ReadComment returns the comment commentID.
//
// It returns the errors that forge.Comments names.
func (f *gitHub) ReadComment(ctx context.Context, repo, commentID string) (forge.Comment, error) {
	if err := f.commentAllowed("ReadComment"); err != nil {
		return forge.Comment{}, err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return forge.Comment{}, err
	}

	if idErr := validCommentID(commentID); idErr != nil {
		return forge.Comment{}, idErr
	}

	bearer, err := f.bearer(ctx, r, levelCommentRead)
	if err != nil {
		return forge.Comment{}, f.forget(r, err)
	}

	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/issues/comments/" + commentID, bearer: bearer, body: nil,
		status: f.commentStatus, oversize: nil, target: "",
	})
	if err != nil {
		return forge.Comment{}, f.forget(r, err)
	}

	got, _, err := parseComment(resp.body)
	if err != nil {
		return forge.Comment{}, err
	}

	// Defense in depth: the path names the comment. This check stays so that
	// an answer for another comment is never the comment that was asked for.
	if got.ID != commentID {
		return forge.Comment{}, forge.Unavailable(0, "GitHub gave another comment than the one asked for")
	}

	return got, nil
}

// React adds the reaction to the comment commentID. GitHub answers 201 for a
// new reaction and 200 for one that the account made already; both are nil.
//
// It returns the errors that forge.Comments names.
func (f *gitHub) React(ctx context.Context, repo, commentID string, reaction forge.Reaction) error {
	if err := f.commentAllowed("React"); err != nil {
		return err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return err
	}

	if idErr := validCommentID(commentID); idErr != nil {
		return idErr
	}

	if reactErr := forge.ValidateReaction(reaction); reactErr != nil {
		return reactErr
	}

	body, err := json.Marshal(map[string]string{keyContent: string(reaction)})
	if err != nil {
		return fmt.Errorf("%w: the GitHub reaction request does not encode", forge.ErrInvalid)
	}

	bearer, err := f.bearer(ctx, r, levelComment)
	if err != nil {
		return f.forget(r, err)
	}

	resp, err := f.send(ctx, call{
		method: http.MethodPost, path: r.path() + "/issues/comments/" + commentID + "/reactions", bearer: bearer,
		body: body, status: f.commentStatus, oversize: nil, target: "",
	})
	if err != nil {
		return f.forget(r, err)
	}

	var fields map[string]json.RawMessage
	if decodeErr := decode(resp.body, &fields); decodeErr != nil {
		return decodeErr
	}

	if textField(fields, keyContent) != string(reaction) {
		return forge.Unavailable(0, "GitHub gave a reaction that does not match the request")
	}

	return nil
}

// parseComment reads a comment from an answer and returns it with the login of
// its author. Every field is host data that is not trusted: the ID, the author
// and the address must pass the checks, and the review ID is read from the
// address of the pull request.
func parseComment(raw []byte) (forge.Comment, string, error) {
	bad := forge.Unavailable(0, "GitHub gave a comment that the adapter cannot use")

	var fields map[string]json.RawMessage
	if err := decode(raw, &fields); err != nil {
		return forge.Comment{}, "", err
	}

	var number wholeNumber
	if json.Unmarshal(fields[keyID], &number) != nil {
		return forge.Comment{}, "", bad
	}

	id, ok := positiveNumber(number)
	if !ok || validCommentID(id) != nil {
		return forge.Comment{}, "", bad
	}

	var user map[string]json.RawMessage
	if json.Unmarshal(fields[keyUser], &user) != nil {
		return forge.Comment{}, "", bad
	}

	var authorNumber wholeNumber
	if json.Unmarshal(user[keyID], &authorNumber) != nil {
		return forge.Comment{}, "", bad
	}

	author, ok := positiveNumber(authorNumber)
	login := textField(user, keyLogin)

	body := textField(fields, keyBody)
	if !ok || !validLogin(login) || !utf8.ValidString(body) || len(body) > forge.MaxCommentBytes*2 {
		return forge.Comment{}, "", bad
	}

	address := textField(fields, keyHTMLURL)
	if !reviewURL(address) {
		return forge.Comment{}, "", bad
	}

	return forge.Comment{
		ID: id, ReviewID: reviewOfIssueURL(textField(fields, keyIssueURL)), URL: address, AuthorID: author,
		Body: body, Truncated: false,
	}, login, nil
}

// reviewOfIssueURL returns the number at the end of the address of the issue
// of a comment, or an empty text when the address does not end with one.
func reviewOfIssueURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return ""
	}

	last := u.Path[strings.LastIndex(u.Path, "/")+1:]
	if validReviewID(last) != nil {
		return ""
	}

	return last
}

// firstLine returns the first line of text, with no line break.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")

	return strings.TrimSuffix(line, "\r")
}

// checkCommentText checks the text that goes into a call.
//
// Defense in depth: forge.CommentText builds and limits the text, the caller of
// the port may check it too, and GitHub limits it. This check stays so that a
// text without the marker line, or above the limit, never goes into a call.
func checkCommentText(text string) error {
	if !utf8.ValidString(text) || len(text) > forge.MaxCommentBytes ||
		!strings.HasPrefix(text, forge.CommentMarkerPrefix) {
		return fmt.Errorf("%w: the comment text has no marker line or is above the limit", forge.ErrInvalid)
	}

	if line := firstLine(text); !strings.HasSuffix(line, " -->") || strings.Count(line, "-->") != 1 {
		return fmt.Errorf("%w: the marker line of the comment is not valid", forge.ErrInvalid)
	}

	return nil
}
