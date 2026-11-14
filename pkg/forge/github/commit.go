package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Limits of the commit calls.
const (
	// maxTrailerBytes is the longest trailer line that FindCommit accepts.
	maxTrailerBytes = 256
	// maxPersonBytes is the longest name or email of a commit author.
	maxPersonBytes = 255
	// commitsPerPage is the size of the pages that FindCommit reads, the most
	// that GitHub gives.
	commitsPerPage = 100
	// maxCommitPages is the most pages that FindCommit reads. A search that
	// reaches the last page with no answer fails, so that it never reports
	// "not found" for a commit that it did not look at.
	maxCommitPages = 5
)

// The types of error that the GraphQL call of a commit gives.
const (
	gqlStale        = "STALE_DATA"
	gqlNotFound     = "NOT_FOUND"
	gqlForbidden    = "FORBIDDEN"
	gqlScopes       = "INSUFFICIENT_SCOPES"
	gqlUnprocessble = "UNPROCESSABLE"
)

// mutation is the GraphQL call that makes one commit on an existing branch.
// The commit holds all file changes or none, and GitHub signs it as the app.
const mutation = `mutation($input: CreateCommitOnBranchInput!) {` +
	` createCommitOnBranch(input: $input) { commit { oid committedDate } } }`

// Commit makes one commit with all changes of req on req.Branch, with the
// GraphQL mutation createCommitOnBranch. The commit is the commit of the
// connection (the app or the owner of the token), and the person of
// req.Author is a Co-authored-by trailer at the end of the message. The
// expected head of the branch is req.Base. A branch that moved gives
// forge.ErrConflict when a file of the changes differs at the new head from
// req.Base, and else the commit goes on top of the new head. With
// req.NewBranch, it first makes the branch at req.Base.
//
// It returns forge.ErrInvalid for a request that fails the checks, before any
// call, and for a request that GitHub refuses as not valid (for example the
// delete of a file that the branch does not have). It returns forge.ErrRefused
// for a branch rule or a missing permission, forge.ErrNotFound for a missing
// repository, branch or base commit, and forge.ErrUnsupported for a
// connection that Capabilities does not list the capability for.
func (f *gitHub) Commit(ctx context.Context, repo string, req forge.CommitRequest) (forge.Commit, error) {
	if err := f.commitAllowed("Commit"); err != nil {
		return forge.Commit{}, err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return forge.Commit{}, err
	}

	msg, err := checkCommit(req)
	if err != nil {
		return forge.Commit{}, err
	}

	bearer, err := f.writeBearer(ctx, r)
	if err != nil {
		return forge.Commit{}, f.forget(r, err)
	}

	msg, err = f.withProof(ctx, r, bearer, req, msg)
	if err != nil {
		return forge.Commit{}, f.forget(r, err)
	}

	if req.NewBranch {
		if branchErr := f.startBranch(ctx, r, bearer, req); branchErr != nil {
			return forge.Commit{}, f.forget(r, branchErr)
		}
	}

	c, err := f.createCommit(ctx, r, bearer, req, msg)
	if errors.Is(err, forge.ErrConflict) {
		c, err = f.commitAfterMove(ctx, r, bearer, req, msg)
	}

	if err != nil {
		return forge.Commit{}, f.forget(r, err)
	}

	return c, nil
}

// withProof adds the proof of the commit to the trailer block of msg. The
// proof binds the trailer, the base commit, the branch, the digest of the
// changes and the repository. A commit that follows a branch that moved keeps
// the base of the request, so that one proof serves every try.
func (f *gitHub) withProof(
	ctx context.Context, r repoName, bearer string, req forge.CommitRequest, msg message,
) (message, error) {
	// Defense in depth: commitAllowed refuses a connection with no Prover. This
	// check stays so that a commit never leaves with no proof.
	if !canProve(f.o.prover) {
		return message{}, fmt.Errorf("%w: the connection has no key to seal the proof", forge.ErrUnsupported)
	}

	repoID, err := f.repositoryID(ctx, r, bearer)
	if err != nil {
		return message{}, err
	}

	proof, err := f.o.prover.Seal(ctx, proofBinding(repoID, forge.DeliveryOf(req)))
	if err != nil || len(proof) == 0 {
		return message{}, forge.Unavailable(0, "the proof of the commit cannot be sealed")
	}

	return message{headline: msg.headline, body: addTrailerLine(msg.body, proofLine(proof))}, nil
}

// maxMoves is how many times Commit follows a branch that moved while it
// worked, before it returns forge.ErrConflict.
const maxMoves = 3

// commitAfterMove runs after GitHub said that the head of the branch is not
// req.Base. The commit goes on when no file of the changes differs between
// req.Base and the new head, with the new head as the expected head. It
// returns forge.ErrConflict when a file differs, or when the branch moves
// maxMoves times.
func (f *gitHub) commitAfterMove(
	ctx context.Context, r repoName, bearer string, req forge.CommitRequest, msg message,
) (forge.Commit, error) {
	conflict := fmt.Errorf("%w: the branch moved after the base commit", forge.ErrConflict)

	for range maxMoves {
		head, err := f.branchHead(ctx, r, bearer, req.Branch)
		if err != nil {
			return forge.Commit{}, err
		}

		if head == req.Base {
			return forge.Commit{}, conflict
		}

		// Files that are the same at the base and at the head are not enough:
		// a force push can keep the files and rewrite the history.
		if ancestorErr := f.requireAncestor(ctx, r, bearer, req.Base, head); ancestorErr != nil {
			return forge.Commit{}, ancestorErr
		}

		differs, err := f.changedSince(ctx, r, bearer, req, head)
		if err != nil {
			return forge.Commit{}, err
		}

		if differs {
			return forge.Commit{}, conflict
		}

		onHead := req
		onHead.Base = head

		c, err := f.createCommit(ctx, r, bearer, onHead, msg)
		if !errors.Is(err, forge.ErrConflict) {
			return c, err
		}
	}

	return forge.Commit{}, fmt.Errorf("%w: the branch moved again and again", forge.ErrConflict)
}

// Fields of the answer of the compare call.
const (
	keyStatus    = "status"
	keyMergeBase = "merge_base_commit"
)

// requireAncestor returns nil when the commit base is an ancestor of the
// commit head, and forge.ErrConflict when it is not, or when the answer of
// GitHub does not show that it is. An error of the credential or a GitHub that
// does not answer is returned as it is: no commit is made then either.
func (f *gitHub) requireAncestor(ctx context.Context, r repoName, bearer, base, head string) error {
	conflict := fmt.Errorf("%w: the base commit is not an ancestor of the head of the branch", forge.ErrConflict)

	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/compare/" + base + "..." + head + "?per_page=1", bearer: bearer,
		body: nil, status: f.commitStatus, oversize: conflict, target: "",
	})
	if errors.Is(err, forge.ErrNotFound) {
		return conflict
	}

	if err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if decode(resp.body, &fields) != nil {
		return conflict
	}

	var (
		status    string
		mergeBase refObject
	)

	if json.Unmarshal(fields[keyStatus], &status) != nil || json.Unmarshal(fields[keyMergeBase], &mergeBase) != nil {
		return conflict
	}

	// Defense in depth: "ahead" and "identical" say that the base is an
	// ancestor. The merge base must be the base too, so that an answer for
	// other commits never passes.
	if (status != compareAhead && status != compareIdentical) || mergeBase.SHA != base {
		return conflict
	}

	return nil
}

// changedSince reports whether a path of the changes of req is not the same
// at req.Base and at head. It compares the tree entry of the path, its mode
// and its object ID, so that a symlink, a submodule and a directory count as
// what they are. An entry that is not a regular file, at the base or at the
// head, counts as changed, and so does a tree that the adapter cannot read
// whole. The check fails closed: only two equal entries of regular files, or
// two missing entries, are the same.
func (f *gitHub) changedSince(
	ctx context.Context, r repoName, bearer string, req forge.CommitRequest, head string,
) (bool, error) {
	trees := newTreeReader(f, r, bearer)

	for _, c := range req.Changes {
		before, err := trees.entryAt(ctx, req.Base, c.Path)
		if unreadableTree(err) {
			return true, nil
		}

		if err != nil {
			return false, err
		}

		after, err := trees.entryAt(ctx, head, c.Path)
		if unreadableTree(err) {
			return true, nil
		}

		if err != nil {
			return false, err
		}

		// Defense in depth: entryAt reads only entries that it knows. These
		// checks stay so that an entry that is not a regular file never counts
		// as the same, also when the two sides are equal.
		if !before.regularOrMissing() || !after.regularOrMissing() || before != after {
			return true, nil
		}
	}

	return false, nil
}

// commitAllowed returns forge.ErrUnsupported for a connection that
// Capabilities does not list forge.CapCommit for.
func (f *gitHub) commitAllowed(method string) error {
	if !f.Capabilities().Has(forge.CapCommit) {
		return fmt.Errorf(
			"%w: GitHub %s needs a connection with push access and, for an app, the permission to write the contents",
			forge.ErrUnsupported, method,
		)
	}

	return nil
}

// message is the commit message in the two parts that GitHub takes.
type message struct {
	headline string
	body     string
}

// checkCommit checks req and returns its message in the form for GitHub.
//
// Defense in depth: forge.ValidateCommit checks the request, the caller of the
// port may check it too, and GitHub checks it. The checks below stay so that
// a request that fails one of them never goes into a call, also when the
// check of the port changes. None relies on another.
func checkCommit(req forge.CommitRequest) (message, error) {
	if err := forge.ValidateCommit(req); err != nil {
		return message{}, fmt.Errorf("the commit request: %w", err)
	}

	if err := validateRef(req.Branch); err != nil {
		return message{}, err
	}

	if err := forge.ValidateCommitID(req.Base); err != nil {
		return message{}, fmt.Errorf("the base commit: %w", err)
	}

	if err := checkChanges(req.Changes); err != nil {
		return message{}, err
	}

	if err := checkRequestTrailer(req); err != nil {
		return message{}, err
	}

	if len(req.Message) > forge.MaxMessageBytes {
		return message{}, fmt.Errorf(
			"%w: the commit message has more than %d bytes",
			forge.ErrTooLarge,
			forge.MaxMessageBytes,
		)
	}

	return buildMessage(req.Message, req.Author)
}

// checkRequestTrailer checks that the trailer of the request is one trailer
// line, that the last paragraph of the message has it, and that neither the
// trailer nor the message has a line with the key of the proof, which only the
// adapter writes.
func checkRequestTrailer(req forge.CommitRequest) error {
	if err := validTrailer(req.Trailer); err != nil {
		return err
	}

	if hasProofKey(req.Trailer) || hasProofKey(req.Message) {
		return fmt.Errorf("%w: the %s line is for the adapter only", forge.ErrInvalid, proofKey)
	}

	if !hasTrailer(req.Message, req.Trailer) {
		return fmt.Errorf("%w: the last paragraph of the message has no line equal to the trailer", forge.ErrInvalid)
	}

	// The adapter adds the proof, and the person of the request, to the last
	// paragraph. They join it only when each line of it is a trailer. Else they
	// make a new paragraph, and the trailer of the request is no longer in the
	// last paragraph, so FindCommit would never find the commit.
	text := strings.TrimRight(strings.ReplaceAll(req.Message, "\r\n", "\n"), " \t\n")
	if !isTrailerBlock(lastParagraph(text)) {
		return fmt.Errorf("%w: each line of the last paragraph of the message must be a trailer", forge.ErrInvalid)
	}

	return nil
}

// checkChanges checks the file changes with the limits and the allow list of
// the adapter.
func checkChanges(changes []forge.FileChange) error {
	if len(changes) == 0 || len(changes) > forge.MaxChanges {
		return fmt.Errorf(
			"%w: a commit has 1 to %d file changes, not %d",
			forge.ErrInvalid,
			forge.MaxChanges,
			len(changes),
		)
	}

	seen := make(map[string]bool, len(changes))
	total := 0

	for _, c := range changes {
		if err := validateFilePath(c.Path); err != nil {
			return err
		}

		if seen[c.Path] {
			return fmt.Errorf("%w: a file is in the commit twice", forge.ErrInvalid)
		}

		seen[c.Path] = true

		if c.Delete && len(c.Content) > 0 {
			return fmt.Errorf("%w: the delete of a file has content", forge.ErrInvalid)
		}

		total += len(c.Content)
		if total > forge.MaxChangeBytes {
			return fmt.Errorf("%w: the changes have more than %d bytes", forge.ErrTooLarge, forge.MaxChangeBytes)
		}
	}

	return nil
}

// buildMessage splits text in the first line (the headline) and the rest, and
// adds the Co-authored-by trailer for author. The trailer joins the trailer
// block at the end of the text when it has one, and else it makes one.
func buildMessage(text string, author forge.Person) (message, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")

	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return message{}, fmt.Errorf("%w: the commit message is not valid text", forge.ErrInvalid)
	}

	headline, rest, _ := strings.Cut(text, "\n")

	headline = strings.TrimRight(headline, " \t\r")
	if headline == "" {
		return message{}, fmt.Errorf("%w: the commit message has no first line", forge.ErrInvalid)
	}

	body := strings.TrimRight(strings.TrimLeft(rest, "\n"), " \t\r\n")

	if author != (forge.Person{Name: "", Email: ""}) {
		if err := validPerson(author); err != nil {
			return message{}, err
		}

		body = addCoAuthor(body, author)
	}

	return message{headline: headline, body: body}, nil
}

// addCoAuthor adds the Co-authored-by trailer of author to body.
func addCoAuthor(body string, author forge.Person) string {
	return addTrailerLine(body, "Co-authored-by: "+author.Name+" <"+author.Email+">")
}

// addTrailerLine adds the trailer line to body. The line joins the trailer
// block at the end of body when it has one, and else it makes one.
func addTrailerLine(body, line string) string {
	if body == "" {
		return line
	}

	if isTrailerBlock(lastParagraph(body)) {
		return body + "\n" + line
	}

	return body + "\n\n" + line
}

// lastParagraph returns the lines after the last empty line of text, or all
// of text when it has no empty line.
func lastParagraph(text string) string {
	if _, last, found := strings.CutLast(text, "\n\n"); found {
		return last
	}

	return text
}

// isTrailerBlock reports whether each line of paragraph is a trailer.
func isTrailerBlock(paragraph string) bool {
	if paragraph == "" {
		return false
	}

	for line := range strings.SplitSeq(paragraph, "\n") {
		if !isTrailer(line) {
			return false
		}
	}

	return true
}

// isTrailer reports whether line has the form "Key: value". The key has ASCII
// letters, digits and "-", and starts with a letter. The value is not empty and
// has no space at its start or end.
func isTrailer(line string) bool {
	key, value, found := strings.Cut(line, ": ")

	return found && validTrailerKey(key) && value != "" && strings.TrimSpace(value) == value
}

// validTrailerKey reports whether key has ASCII letters, digits and "-" only,
// and starts with a letter.
func validTrailerKey(key string) bool {
	for i := range len(key) {
		c := key[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		digitOrDash := c >= '0' && c <= '9' || c == '-'

		if !letter && (i == 0 || !digitOrDash) {
			return false
		}
	}

	return key != ""
}

// validTrailer checks the trailer line that FindCommit searches for, with an
// allow list: one line "Key: value" of graphic characters.
func validTrailer(trailer string) error {
	bad := trailer == "" || len(trailer) > maxTrailerBytes || !utf8.ValidString(trailer) ||
		strings.TrimSpace(trailer) != trailer

	for _, r := range trailer {
		bad = bad || !unicode.IsGraphic(r)
	}

	if bad || !isTrailer(trailer) {
		return fmt.Errorf(
			"%w: the trailer must be one line of the form \"Key: value\" with no control character",
			forge.ErrInvalid,
		)
	}

	return nil
}

// validPerson checks the author of a commit with an allow list. The name and
// the email are not empty, are graphic text, and have no "<" or ">". The email
// has one "@" and no space.
func validPerson(p forge.Person) error {
	if !plainText(p.Name) || strings.TrimSpace(p.Name) != p.Name || !plainText(p.Email) || !validEmail(p.Email) {
		return fmt.Errorf("%w: the author needs a name and an email of plain text", forge.ErrInvalid)
	}

	return nil
}

// plainText reports whether s is not empty, is not long, and has graphic
// characters only, and none of "<" and ">".
func plainText(s string) bool {
	if s == "" || len(s) > maxPersonBytes || !utf8.ValidString(s) {
		return false
	}

	for _, r := range s {
		if !unicode.IsGraphic(r) || r == '<' || r == '>' {
			return false
		}
	}

	return true
}

// validEmail reports whether email has one "@", a part before it and a part
// after it, and no space.
func validEmail(email string) bool {
	local, domain, found := strings.Cut(email, "@")

	return found && local != "" && domain != "" && !strings.Contains(domain, "@") &&
		!strings.ContainsFunc(email, unicode.IsSpace)
}

// gqlRequest is the body of the GraphQL call.
type gqlRequest struct {
	Query     string       `json:"query"`
	Variables gqlVariables `json:"variables"`
}

type gqlVariables struct {
	Input gqlInput `json:"input"`
}

type gqlInput struct {
	Branch          gqlBranch  `json:"branch"`
	Message         gqlMessage `json:"message"`
	FileChanges     gqlChanges `json:"fileChanges"`
	ExpectedHeadOid string     `json:"expectedHeadOid"`
}

type gqlBranch struct {
	Repository string `json:"repositoryNameWithOwner"`
	Name       string `json:"branchName"`
}

type gqlMessage struct {
	Headline string `json:"headline"`
	Body     string `json:"body"`
}

type gqlChanges struct {
	Additions []gqlAddition `json:"additions"`
	Deletions []gqlDeletion `json:"deletions"`
}

type gqlAddition struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

type gqlDeletion struct {
	Path string `json:"path"`
}

// gqlAnswer is the part of the answer to the mutation that the adapter reads.
type gqlAnswer struct {
	Data   gqlData    `json:"data"`
	Errors []gqlError `json:"errors"`
}

type gqlData struct {
	Create *gqlPayload `json:"createCommitOnBranch"`
}

type gqlPayload struct {
	Commit gqlCommit `json:"commit"`
}

type gqlCommit struct {
	OID           string    `json:"oid"`
	CommittedDate time.Time `json:"committedDate"`
}

type gqlError struct {
	Type string `json:"type"`
}

// graphqlTarget returns the address of the GraphQL API. GitHub Enterprise
// Server has the REST API at /api/v3 and GraphQL at /api/graphql.
func (f *gitHub) graphqlTarget() string {
	if root, ok := strings.CutSuffix(f.base, "/api/v3"); ok {
		return root + "/api/graphql"
	}

	return f.base + "/graphql"
}

// createCommit sends the mutation for req and reads the answer.
func (f *gitHub) createCommit(
	ctx context.Context, r repoName, bearer string, req forge.CommitRequest, msg message,
) (forge.Commit, error) {
	changes := gqlChanges{Additions: []gqlAddition{}, Deletions: []gqlDeletion{}}

	for _, c := range req.Changes {
		if c.Delete {
			changes.Deletions = append(changes.Deletions, gqlDeletion{Path: c.Path})

			continue
		}

		changes.Additions = append(
			changes.Additions, gqlAddition{Path: c.Path, Contents: base64.StdEncoding.EncodeToString(c.Content)},
		)
	}

	body, err := json.Marshal(gqlRequest{Query: mutation, Variables: gqlVariables{Input: gqlInput{
		Branch:          gqlBranch{Repository: r.owner + "/" + r.name, Name: req.Branch},
		Message:         gqlMessage{Headline: msg.headline, Body: msg.body},
		FileChanges:     changes,
		ExpectedHeadOid: req.Base,
	}}})
	if err != nil {
		return forge.Commit{}, fmt.Errorf("%w: the GitHub commit request does not encode", forge.ErrInvalid)
	}

	resp, err := f.send(ctx, call{
		method: http.MethodPost, path: "", bearer: bearer, body: body, status: f.commitStatus, oversize: nil,
		target: f.graphqlTarget(),
	})
	if err != nil {
		return forge.Commit{}, err
	}

	var answer gqlAnswer
	if decodeErr := decode(resp.body, &answer); decodeErr != nil {
		return forge.Commit{}, decodeErr
	}

	return commitOf(answer)
}

// commitOf turns the answer to the mutation into a commit. An answer with an
// error is never a commit, also when it holds data.
func commitOf(answer gqlAnswer) (forge.Commit, error) {
	if len(answer.Errors) > 0 {
		return forge.Commit{}, gqlFailure(answer.Errors)
	}

	if answer.Data.Create == nil {
		return forge.Commit{}, forge.Unavailable(0, "GitHub gave an answer with no commit")
	}

	c := answer.Data.Create.Commit
	if forge.ValidateCommitID(c.OID) != nil || c.CommittedDate.IsZero() {
		return forge.Commit{}, forge.Unavailable(0, "GitHub gave an answer with no commit")
	}

	return forge.Commit{ID: c.OID, Time: c.CommittedDate.UTC()}, nil
}

// gqlFailure maps the errors of an answer to a sentinel of the port. It
// returns the error of the highest rank first, and ErrUnavailable for a type
// that it does not know, so that no unknown error counts as a commit. The
// text of the answer is not in the error.
func gqlFailure(errs []gqlError) error {
	has := func(types ...string) bool {
		for _, e := range errs {
			if slices.Contains(types, e.Type) {
				return true
			}
		}

		return false
	}

	switch {
	case has(gqlStale):
		return fmt.Errorf("%w: the branch moved after the base commit", forge.ErrConflict)
	case has(gqlNotFound):
		return fmt.Errorf("%w: GitHub has no such repository or branch", forge.ErrNotFound)
	case has(gqlForbidden, gqlScopes):
		return fmt.Errorf("%w: a branch rule or a missing permission stops the commit", forge.ErrRefused)
	case has(gqlUnprocessble):
		return fmt.Errorf("%w: GitHub does not accept the commit", forge.ErrInvalid)
	default:
		return forge.Unavailable(0, "GitHub gave an error for the commit")
	}
}

// commitStatus maps the status of a call that writes. A rate limit is
// forge.ErrUnavailable with the wait time, and a server error (for example
// 502 or 503) is forge.ErrUnavailable, which the caller may retry.
func (f *gitHub) commitStatus(r response) error {
	switch {
	case r.status == http.StatusTooManyRequests, r.status == http.StatusForbidden && limited(r.header):
		return f.statusError(r)
	case r.status == http.StatusForbidden:
		return fmt.Errorf("%w: a missing permission or a rule of GitHub stops the call", forge.ErrRefused)
	case r.status == http.StatusNotFound:
		return fmt.Errorf("%w: GitHub has no such repository, branch or commit", forge.ErrNotFound)
	case r.status == http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: GitHub does not accept the request", forge.ErrInvalid)
	}

	return f.statusError(r)
}

// refExistsError is the answer of GitHub for a branch that exists already.
type refExistsError struct{}

func (refExistsError) Error() string { return "the branch exists already" }

// refRequest is the body of the call that makes a branch.
type refRequest struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// refAnswer is the part of the answer for a branch that the adapter reads.
type refAnswer struct {
	Object refObject `json:"object"`
}

type refObject struct {
	SHA string `json:"sha"`
}

// refStatus maps the status of the call that makes a branch. GitHub answers
// 422 for a name that is taken and for a commit that it does not have.
func (f *gitHub) refStatus(r response) error {
	if r.status != http.StatusUnprocessableEntity {
		return f.commitStatus(r)
	}

	var e errorBody
	if decode(r.body, &e) == nil {
		switch {
		case strings.Contains(e.Message, "already exists"):
			return refExistsError{}
		case strings.Contains(e.Message, "does not exist"):
			return fmt.Errorf("%w: GitHub has no commit with the ID of the base", forge.ErrNotFound)
		}
	}

	return f.commitStatus(r)
}

// startBranch makes the branch req.Branch at req.Base. A branch that exists
// at req.Base is the branch that a first try made, so the commit goes on. A
// branch that exists at another commit is forge.ErrConflict.
func (f *gitHub) startBranch(ctx context.Context, r repoName, bearer string, req forge.CommitRequest) error {
	body, err := json.Marshal(refRequest{Ref: "refs/heads/" + req.Branch, SHA: req.Base})
	if err != nil {
		return fmt.Errorf("%w: the GitHub branch request does not encode", forge.ErrInvalid)
	}

	_, err = f.send(ctx, call{
		method: http.MethodPost, path: r.path() + "/git/refs", bearer: bearer, body: body, status: f.refStatus,
		oversize: nil, target: "",
	})

	if !errors.Is(err, refExistsError{}) {
		return err
	}

	head, err := f.branchHead(ctx, r, bearer, req.Branch)
	if err != nil {
		return err
	}

	if head != req.Base {
		return fmt.Errorf("%w: the new branch exists with another head", forge.ErrConflict)
	}

	return nil
}

// branchHead returns the commit at the head of the branch.
func (f *gitHub) branchHead(ctx context.Context, r repoName, bearer, branch string) (string, error) {
	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/git/ref/heads/" + escapePath(branch), bearer: bearer, body: nil,
		status: f.commitStatus, oversize: nil, target: "",
	})
	if err != nil {
		return "", err
	}

	var head refAnswer
	if decodeErr := decode(resp.body, &head); decodeErr != nil {
		return "", decodeErr
	}

	if forge.ValidateCommitID(head.Object.SHA) != nil {
		return "", forge.Unavailable(0, "GitHub gave an answer with no commit")
	}

	return head.Object.SHA, nil
}

// FindCommit returns the newest commit on ref, at or after since, whose
// message has the trailer line trailer in its last paragraph, so that a
// retried write-back finds its own commit. A line of the text of a message
// that is not in the last paragraph is not a trailer. It returns
// forge.ErrNotFound when no such commit exists, and for a missing repository
// or ref. A commit that has the trailer counts only when the connection made
// it (see trustedCommit). The newest commit with the trailer that fails a
// check gives forge.ErrUntrustedCommit, not a commit and not ErrNotFound. It
// returns forge.ErrInvalid for a trailer, a base commit, a ref or a repository
// path that fails the checks, before any call, and forge.ErrUnavailable when
// there are more commits since the time than the adapter reads, so that it
// never answers "not found" for a commit that it did not look at.
//
// d is the delivery of the request that made the commit, and ref must be its
// branch. The commit must also have a proof that opens for the repository,
// the trailer, the base commit, the branch and the digest of the changes (see
// checkProof).
func (f *gitHub) FindCommit(
	ctx context.Context, repo, ref string, d forge.Delivery, since time.Time,
) (forge.Commit, error) {
	if err := f.commitAllowed("FindCommit"); err != nil {
		return forge.Commit{}, err
	}

	r, err := checkFind(repo, ref, d)
	if err != nil {
		return forge.Commit{}, err
	}

	bearer, err := f.readBearer(ctx, r)
	if err != nil {
		return forge.Commit{}, f.forget(r, err)
	}

	want := delivery{repo: r, bearer: bearer, Delivery: d}

	for page := 1; page <= maxCommitPages; page++ {
		list, full, listErr := f.listCommits(ctx, search{repo: r, bearer: bearer, ref: ref, since: since}, page)
		if listErr != nil {
			return forge.Commit{}, f.forget(r, listErr)
		}

		found, delivered, deliveryErr := f.deliveryIn(ctx, list, want)
		if deliveryErr != nil {
			return forge.Commit{}, f.forget(r, deliveryErr)
		}

		if delivered {
			return found, nil
		}

		if !full {
			return forge.Commit{}, fmt.Errorf(
				"%w: no commit on the ref has the trailer since that time",
				forge.ErrNotFound,
			)
		}
	}

	return forge.Commit{}, forge.Unavailable(0, "more commits since the time than the adapter reads")
}

// checkFind checks the arguments of FindCommit with allow lists, before any
// call, and returns the repository.
func checkFind(repo, ref string, d forge.Delivery) (repoName, error) {
	r, err := splitRepo(repo)
	if err != nil {
		return repoName{}, err
	}

	if refErr := validateRef(ref); refErr != nil {
		return repoName{}, refErr
	}

	if trailerErr := validTrailer(d.Trailer); trailerErr != nil {
		return repoName{}, trailerErr
	}

	if hasProofKey(d.Trailer) {
		return repoName{}, fmt.Errorf("%w: the %s line is for the adapter only", forge.ErrInvalid, proofKey)
	}

	// Defense in depth: proofBinding puts a length before each part. This
	// check stays so that a delivery that is not valid makes no call.
	if deliveryErr := forge.ValidateDelivery(d); deliveryErr != nil {
		return repoName{}, deliveryErr
	}

	// The proof binds the branch of the request. A search of another ref
	// could find the commit after a merge, and the branch in the proof would
	// then be a branch that the caller claims, not the branch that holds the
	// commit.
	if ref != d.Branch {
		return repoName{}, fmt.Errorf("%w: the ref of the search is not the branch of the delivery", forge.ErrInvalid)
	}

	return r, nil
}

// deliveryIn returns the first commit of list that has the trailer, and true.
// A commit with the trailer is the delivery only when the connection made it
// (see trustedCommit). Any other commit with the trailer stops the search with
// forge.ErrUntrustedCommit, so that the caller never records it and never
// makes a second commit on top of it. It returns false when no commit of the
// list has the trailer.
func (f *gitHub) deliveryIn(ctx context.Context, list []listedCommit, d delivery) (forge.Commit, bool, error) {
	for _, c := range list {
		if !hasTrailer(c.Commit.Message, d.Trailer) {
			continue
		}

		identity, err := f.commitIdentity(ctx)
		if err != nil {
			return forge.Commit{}, false, err
		}

		if trustErr := trustedCommit(c, identity); trustErr != nil {
			return forge.Commit{}, false, trustErr
		}

		if proofErr := f.checkProof(ctx, d, c); proofErr != nil {
			return forge.Commit{}, false, proofErr
		}

		return forge.Commit{ID: c.SHA, Time: c.Commit.Committer.Date.UTC()}, true, nil
	}

	return forge.Commit{ID: "", Time: time.Time{}}, false, nil
}

// listedCommit is a commit of the list that FindCommit reads.
type listedCommit struct {
	SHA    string          `json:"sha"`
	Commit listedCommitRef `json:"commit"`
	// Author and Committer are the accounts of GitHub for the author and the
	// committer. They are nil when no account has the email of the commit.
	Author    *listedAccount `json:"author"`
	Committer *listedAccount `json:"committer"`
}

type listedCommitRef struct {
	Message   string       `json:"message"`
	Committer committerRef `json:"committer"`
	// Verification is nil when the host reports no verification.
	Verification *listedVerification `json:"verification"`
}

// listedAccount is the account of an author or a committer in the list.
type listedAccount struct {
	Login string `json:"login"`
}

// listedVerification is the verification of a commit in the list.
type listedVerification struct {
	Verified *bool `json:"verified"`
}

// search is a search of FindCommit: the commits of ref in repo since a time.
type search struct {
	repo   repoName
	bearer string
	ref    string
	since  time.Time
}

// listCommits reads one page of the commits of the search and returns the
// commits that are at or after its time, and whether the page was full, which
// means that more commits may follow. The list is host data that is not
// trusted: a commit with no valid ID or time makes the call fail.
func (f *gitHub) listCommits(ctx context.Context, q search, page int) ([]listedCommit, bool, error) {
	query := "?sha=" + url.QueryEscape(
		q.ref,
	) + "&per_page=" + strconv.Itoa(
		commitsPerPage,
	) + "&page=" + strconv.Itoa(
		page,
	)
	// GitHub gives the time to the second, so a time with a fraction counts as
	// the second before it.
	floor := q.since.UTC().Truncate(time.Second)

	if !q.since.IsZero() {
		query += "&since=" + url.QueryEscape(floor.Format(time.RFC3339))
	}

	resp, err := f.send(ctx, call{
		method:   http.MethodGet,
		path:     q.repo.path() + "/commits" + query,
		bearer:   q.bearer,
		body:     nil,
		status:   f.notFound,
		oversize: nil,
		target:   "",
	})
	if err != nil {
		return nil, false, err
	}

	var list []listedCommit
	if decodeErr := decode(resp.body, &list); decodeErr != nil {
		return nil, false, decodeErr
	}

	unreadable := forge.Unavailable(0, "GitHub gave a list of commits that cannot be read")

	if len(list) > commitsPerPage {
		return nil, false, unreadable
	}

	// Defense in depth: the query asks for the commits since the time. This
	// check stays so that a commit before it never counts, whatever the list
	// holds.
	kept := make([]listedCommit, 0, len(list))

	for _, c := range list {
		if forge.ValidateCommitID(c.SHA) != nil || c.Commit.Committer.Date.IsZero() {
			return nil, false, unreadable
		}

		if !q.since.IsZero() && c.Commit.Committer.Date.UTC().Before(floor) {
			continue
		}

		kept = append(kept, c)
	}

	return kept, len(list) == commitsPerPage, nil
}

// hasTrailer reports whether the last paragraph of message has the line
// trailer. The line must equal trailer: a line with more or fewer characters
// is another trailer, so the job "j-1" never matches the job "j-10". The
// message is host data that is not trusted. A message with one paragraph has
// no trailer, because that paragraph is its first line.
func hasTrailer(message, trailer string) bool {
	message = strings.TrimRight(strings.ReplaceAll(message, "\r\n", "\n"), " \t\n")

	_, last, found := strings.CutLast(message, "\n\n")
	if !found {
		return false
	}

	return slices.Contains(strings.Split(last, "\n"), trailer)
}
