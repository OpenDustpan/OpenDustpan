package githubfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"unicode/utf8"
)

// OtherAccountID is the ID of the account of the person that AddComment
// uses for a comment.
const OtherAccountID = 4242002

// maxCommentChars is the most characters of a comment. A longer comment
// answers 422.
const maxCommentChars = 65536

// reactionContents are the reactions that GitHub accepts.
func reactionContents() []string {
	return []string{"+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"}
}

// issueComment is a comment on a pull request.
type issueComment struct {
	id     int
	number int
	body   string
	user   Account
	// reactions maps a reaction to the logins that made it.
	reactions map[string]map[string]bool
}

// IssueComment is a comment of a pull request as the fake shows it.
type IssueComment struct {
	ID     int
	Number int
	Body   string
	Login  string
	// AuthorID is the ID of the account of the author.
	AuthorID int64
}

func (c *issueComment) shown() IssueComment {
	return IssueComment{ID: c.id, Number: c.number, Body: c.body, Login: c.user.Login, AuthorID: c.user.ID}
}

// commentWire returns the comment as the answer shows it.
func (f *Fake) commentWire(rp *repo, c *issueComment) map[string]any {
	return map[string]any{
		"id":   c.id,
		"body": c.body,
		"html_url": f.URL() + "/" + rp.name + "/pull/" + strconv.Itoa(
			c.number,
		) + "#issuecomment-" + strconv.Itoa(
			c.id,
		),
		"issue_url": f.URL() + "/repos/" + rp.name + "/issues/" + strconv.Itoa(c.number),
		"user":      wireAccount(c.user),
	}
}

// pullNumbered returns the pull request with the number in the path, or nil.
func (r *repo) pullNumbered(raw string) *pull {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}

	for _, p := range r.pulls {
		if p.number == n {
			return p
		}
	}

	return nil
}

// commentNumbered returns the comment with the ID in the path, or nil.
func (r *repo) commentNumbered(raw string) *issueComment {
	id, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}

	for _, c := range r.comments {
		if c.id == id {
			return c
		}
	}

	return nil
}

// issuesGet serves the two reads that share the prefix of the issues: the
// comments of a pull request ("{number}/comments") and one comment
// ("comments/{id}"). The mux cannot hold both patterns.
func (f *Fake) issuesGet(w http.ResponseWriter, r *http.Request) {
	first, second := r.PathValue("first"), r.PathValue("second")

	switch {
	case second == "comments":
		f.listComments(w, r, first)
	case first == "comments":
		f.getComment(w, r, second)
	default:
		f.message(w, http.StatusNotFound, "Not Found")
	}
}

func (f *Fake) listComments(w http.ResponseWriter, r *http.Request, number string) {
	rp, ok := f.permissionOf(w, r, "issues", levelNameRead)
	if !ok {
		return
	}

	p := rp.pullNumbered(number)
	if p == nil {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	var all []map[string]any

	for _, c := range rp.comments {
		if c.number == p.number {
			all = append(all, f.commentWire(rp, c))
		}
	}

	q := r.URL.Query()
	perPage, page := pageOf(q.Get("per_page"), defaultPerPage), pageOf(q.Get("page"), 1)
	from := min((page-1)*perPage, len(all))

	f.writeJSON(w, http.StatusOK, append([]map[string]any{}, all[from:min(from+perPage, len(all))]...))
}

func (f *Fake) getComment(w http.ResponseWriter, r *http.Request, id string) {
	rp, ok := f.permissionOf(w, r, "issues", levelNameRead)
	if !ok {
		return
	}

	c := rp.commentNumbered(id)
	if c == nil {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	f.writeJSON(w, http.StatusOK, f.commentWire(rp, c))
}

// commentText reads the body of a request and answers 422 for a text that is
// missing or has more characters than GitHub allows.
func (f *Fake) commentText(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req map[string]string

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.message(w, http.StatusBadRequest, "Problems parsing JSON")

		return "", false
	}

	switch body := req["body"]; {
	case body == "":
		f.invalidIssueField(w, "body", "missing_field")

		return "", false
	case utf8.RuneCountInString(body) > maxCommentChars:
		f.invalidIssueField(w, "body", "custom")

		return "", false
	default:
		return body, true
	}
}

// invalidIssueField answers 422 for a field of a comment.
func (f *Fake) invalidIssueField(w http.ResponseWriter, field, code string) {
	f.writeJSON(w, http.StatusUnprocessableEntity, wireValidation{
		Message: validationFailed,
		Errors:  []wireValidationError{{Resource: "IssueComment", Field: field, Code: code, Message: ""}},
	})
}

func (f *Fake) createComment(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.permissionOf(w, r, "issues", levelNameWrite)
	if !ok {
		return
	}

	p := rp.pullNumbered(r.PathValue("number"))
	if p == nil {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	body, ok := f.commentText(w, r)
	if !ok {
		return
	}

	c := &issueComment{
		id: commentIDBase + len(rp.comments) + 1, number: p.number, body: body, user: f.actor(r),
		reactions: map[string]map[string]bool{},
	}
	rp.comments = append(rp.comments, c)

	f.writeJSON(w, http.StatusCreated, f.commentWire(rp, c))
}

// commentIDBase is added to a counter to make the ID of a comment.
const commentIDBase = 1_000_000

func (f *Fake) patchComment(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.permissionOf(w, r, "issues", levelNameWrite)
	if !ok {
		return
	}

	c := rp.commentNumbered(r.PathValue("id"))
	if c == nil {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	// An app can edit only the comments of its own bot account.
	if f.bearerToken(r) != nil && c.user.Login != f.actor(r).Login {
		f.message(w, http.StatusForbidden, "Resource not accessible by integration")

		return
	}

	body, ok := f.commentText(w, r)
	if !ok {
		return
	}

	c.body = body

	f.writeJSON(w, http.StatusOK, f.commentWire(rp, c))
}

// react adds a reaction. It answers 201 for a new reaction and 200 for one that
// the account made already.
func (f *Fake) react(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.permissionOf(w, r, "issues", levelNameWrite)
	if !ok {
		return
	}

	c := rp.commentNumbered(r.PathValue("id"))
	if c == nil {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	var req map[string]string

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.message(w, http.StatusBadRequest, "Problems parsing JSON")

		return
	}

	content := req["content"]
	if !slices.Contains(reactionContents(), content) {
		f.invalidIssueField(w, "content", "invalid")

		return
	}

	login := f.actor(r).Login
	if c.reactions[content] == nil {
		c.reactions[content] = map[string]bool{}
	}

	status := http.StatusOK
	if !c.reactions[content][login] {
		status = http.StatusCreated
	}

	c.reactions[content][login] = true

	f.writeJSON(w, status, map[string]any{"id": len(c.reactions), "content": content})
}

// Comments returns the comments of the pull request number of the repository
// fullName, oldest first.
func (f *Fake) Comments(fullName string, number int) ([]IssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	var out []IssueComment

	for _, c := range r.comments {
		if c.number == number {
			out = append(out, c.shown())
		}
	}

	return out, nil
}

// AddComment adds a comment of the account login to the pull request number, as
// a person does in the web page.
func (f *Fake) AddComment(fullName string, number int, login, body string) (IssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return IssueComment{}, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	c := &issueComment{
		id: commentIDBase + len(r.comments) + 1, number: number, body: body,
		user:      Account{ID: OtherAccountID, Login: login},
		reactions: map[string]map[string]bool{},
	}
	r.comments = append(r.comments, c)

	return c.shown(), nil
}

// Reactions returns the reactions to the comment id, as "content:login" in
// order.
func (f *Fake) Reactions(fullName string, id int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	var out []string

	for _, c := range r.comments {
		if c.id != id {
			continue
		}

		for content, logins := range c.reactions {
			for login := range logins {
				out = append(out, content+":"+login)
			}
		}
	}

	slices.Sort(out)

	return out, nil
}
