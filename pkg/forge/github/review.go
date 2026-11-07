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
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Limits of the review request calls.
const (
	// maxReviewBytes is the largest answer about a review request that the
	// adapter reads. The egress rules limit every answer to a larger size; this
	// limit is lower, because the answer of a pull request has a few kilobytes
	// and a description of up to 64 KiB.
	maxReviewBytes = 1 << 20
	// maxReviewURLBytes is the longest address of a review request that the
	// adapter accepts from GitHub.
	maxReviewURLBytes = 2048
	// maxReviewIDDigits is the most digits in the number of a review request.
	maxReviewIDDigits = 10
	// reviewsPerPage is the size of the page of the list of open review
	// requests. A branch and a target have one open review request at most, so
	// a few more than one show a list that the adapter does not trust.
	reviewsPerPage = 5
)

// reviewAllowed returns forge.ErrUnsupported for a connection that
// Capabilities does not list forge.CapReview for.
func (f *gitHub) reviewAllowed(method string) error {
	if !f.Capabilities().Has(forge.CapReview) {
		return fmt.Errorf(
			"%w: GitHub %s needs a connection with push access and, for an app, the permission to write the "+
				"contents and the pull requests",
			forge.ErrUnsupported, method,
		)
	}

	return nil
}

// OpenReview opens a pull request from req.Branch into req.Target of the same
// repository. When an open pull request of the same branch and target of the
// repository exists, it returns that one. A pull request from a fork never
// counts, also when its branch has the same name.
//
// It returns forge.ErrInvalid or forge.ErrTooLarge for a request that fails
// the checks, before any call. It returns forge.ErrNotFound for a missing
// repository, branch or target, forge.ErrRefused when GitHub does not allow the
// pull request (for example a branch with no commit that the target lacks, or
// a missing permission), and forge.ErrUnsupported for a connection that
// Capabilities does not list the capability for.
func (f *gitHub) OpenReview(ctx context.Context, repo string, req forge.ReviewRequest) (forge.Review, error) {
	if err := f.reviewAllowed("OpenReview"); err != nil {
		return forge.Review{}, err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return forge.Review{}, err
	}

	if checkErr := checkReview(req); checkErr != nil {
		return forge.Review{}, checkErr
	}

	bearer, err := f.reviewBearer(ctx, r)
	if err != nil {
		return forge.Review{}, f.forget(r, err)
	}

	got, err := f.openOrFind(ctx, r, bearer, req)
	if err != nil {
		return forge.Review{}, f.forget(r, err)
	}

	return got, nil
}

// openOrFind returns the open review request of the branch and the target, or
// opens one.
func (f *gitHub) openOrFind(
	ctx context.Context,
	r repoName,
	bearer string,
	req forge.ReviewRequest,
) (forge.Review, error) {
	if id, found, err := f.findOpen(ctx, r, bearer, req); err != nil || found {
		if err != nil {
			return forge.Review{}, err
		}

		return f.readOwn(ctx, r, bearer, id, req)
	}

	body, err := json.Marshal(newPull{Title: req.Title, Body: req.Body, Head: req.Branch, Base: req.Target})
	if err != nil {
		return forge.Review{}, fmt.Errorf("%w: the GitHub pull request does not encode", forge.ErrInvalid)
	}

	resp, err := f.send(ctx, call{
		method: http.MethodPost, path: r.path() + "/pulls", bearer: bearer, body: body, status: f.reviewStatus,
		oversize: nil, target: "",
	})
	if errors.Is(err, reviewExistsError{}) {
		// A second call opened the review request first.
		id, found, findErr := f.findOpen(ctx, r, bearer, req)
		if findErr != nil {
			return forge.Review{}, findErr
		}

		if !found {
			// A pull request exists for the head, and it is not the one of the
			// write-back, for example a pull request of a fork with a branch of
			// the same name. A retry fails the same way, so this is a conflict.
			return forge.Review{}, fmt.Errorf(
				"%w: a pull request exists for the branch, and it is not a review request of the base repository",
				forge.ErrConflict,
			)
		}

		return f.readOwn(ctx, r, bearer, id, req)
	}

	if err != nil {
		return forge.Review{}, err
	}

	got, err := reviewFrom(resp.body, "")
	if err != nil {
		return forge.Review{}, err
	}

	return ownReview(got, req)
}

// readOwn reads the review request id and checks that it is the one that req
// asks for.
func (f *gitHub) readOwn(
	ctx context.Context, r repoName, bearer, id string, req forge.ReviewRequest,
) (forge.Review, error) {
	got, err := f.fetchReview(ctx, r, bearer, id)
	if err != nil {
		return forge.Review{}, err
	}

	return ownReview(got, req)
}

// ownReview returns got when it is an open review request of the branch and
// the target of req, with the head in the base repository.
//
// Defense in depth: the list and the new pull request name the branch, and
// GitHub applies the name to the repository. This check stays so that an
// answer for another branch, a closed review request or a pull request from a
// fork is never the review request of the write-back.
func ownReview(got forge.Review, req forge.ReviewRequest) (forge.Review, error) {
	if got.State != forge.ReviewOpen || got.Branch != req.Branch || got.Target != req.Target || got.FromFork() {
		return forge.Review{}, forge.Unavailable(0, "GitHub gave a pull request that is not the one that was asked for")
	}

	return got, nil
}

// newPull is the body of the call that opens a pull request.
type newPull struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}

// findOpen looks for the open pull request of the branch and the target in the
// base repository. A pull request from a fork whose branch has the same name
// is not in the list, because the list names the owner of the repository as
// the owner of the head, and the answer is checked for the repository ID.
func (f *gitHub) findOpen(
	ctx context.Context, r repoName, bearer string, req forge.ReviewRequest,
) (string, bool, error) {
	query := "?state=open&per_page=" + strconv.Itoa(reviewsPerPage) +
		"&head=" + url.QueryEscape(r.owner+":"+req.Branch) + "&base=" + url.QueryEscape(req.Target)

	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/pulls" + query, bearer: bearer, body: nil, status: f.reviewStatus,
		oversize: nil, target: "",
	})
	if err != nil {
		return "", false, err
	}

	if len(resp.body) > maxReviewBytes {
		return "", false, forge.Unavailable(0, "GitHub gave an answer above the size limit")
	}

	var list []json.RawMessage
	if decodeErr := decode(resp.body, &list); decodeErr != nil {
		return "", false, decodeErr
	}

	if len(list) > reviewsPerPage {
		return "", false, forge.Unavailable(0, "GitHub gave a list of pull requests that cannot be read")
	}

	for _, item := range list {
		var head pullListed
		if decodeErr := decode(item, &head); decodeErr != nil {
			return "", false, decodeErr
		}

		number, ok := positiveNumber(head.Number)
		if !ok {
			return "", false, forge.Unavailable(0, "GitHub gave a pull request with no number")
		}

		// Only a pull request of the same branch and target, from the base
		// repository, can be the one. Another one in the list is ignored.
		if head.Head.Ref == req.Branch && head.Base.Ref == req.Target && head.sameRepo() {
			return number, true, nil
		}
	}

	return "", false, nil
}

// pullListed is the part of an item of the list that findOpen reads.
type pullListed struct {
	Number wholeNumber `json:"number"`
	Head   pullEnd     `json:"head"`
	Base   pullEnd     `json:"base"`
}

// sameRepo reports whether the head and the base are in the same repository,
// by the repository ID.
func (p pullListed) sameRepo() bool {
	head, headOK := p.Head.repoID()
	base, baseOK := p.Base.repoID()

	return headOK && baseOK && head == base
}

// ReadReview returns the pull request id with its state, its base and head
// commits, its head repository, its author, whether GitHub lets it merge and,
// after a merge, the merge time and commit. A pull request from a fork has a
// head repository ID other than the base repository ID.
//
// It returns forge.ErrInvalid for an id that is not a pull request number,
// before any call, forge.ErrNotFound for a pull request that does not exist,
// and forge.ErrUnavailable for an answer that does not pass the checks of the
// adapter, so that no answer that it cannot check is a review request. The
// merge state is forge.MergeUnknown while GitHub computes the merge.
func (f *gitHub) ReadReview(ctx context.Context, repo, id string) (forge.Review, error) {
	if err := f.reviewAllowed("ReadReview"); err != nil {
		return forge.Review{}, err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return forge.Review{}, err
	}

	if idErr := validReviewID(id); idErr != nil {
		return forge.Review{}, idErr
	}

	bearer, err := f.reviewReadBearer(ctx, r)
	if err != nil {
		return forge.Review{}, f.forget(r, err)
	}

	got, err := f.fetchReview(ctx, r, bearer, id)
	if err != nil {
		return forge.Review{}, f.forget(r, err)
	}

	return got, nil
}

// fetchReview reads one pull request and checks the answer.
func (f *gitHub) fetchReview(ctx context.Context, r repoName, bearer, id string) (forge.Review, error) {
	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/pulls/" + id, bearer: bearer, body: nil, status: f.reviewStatus,
		oversize: nil, target: "",
	})
	if err != nil {
		return forge.Review{}, err
	}

	return reviewFrom(resp.body, id)
}

// CloseReview closes the pull request id with no merge. A pull request that is
// closed already returns nil.
//
// It returns forge.ErrInvalid for an id that is not a pull request number,
// before any call, forge.ErrNotFound for a pull request that does not exist,
// forge.ErrConflict for a pull request that merged, and forge.ErrRefused for a
// pull request that is not one of the write-back: its head branch is not a
// branch of the write-back, or its head is in another repository.
func (f *gitHub) CloseReview(ctx context.Context, repo, id string) error {
	if err := f.reviewAllowed("CloseReview"); err != nil {
		return err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return err
	}

	if idErr := validReviewID(id); idErr != nil {
		return idErr
	}

	bearer, err := f.reviewBearer(ctx, r)
	if err != nil {
		return f.forget(r, err)
	}

	if closeErr := f.closePull(ctx, r, bearer, id); closeErr != nil {
		return f.forget(r, closeErr)
	}

	return nil
}

func (f *gitHub) closePull(ctx context.Context, r repoName, bearer, id string) error {
	got, err := f.fetchReview(ctx, r, bearer, id)
	if err != nil {
		return err
	}

	// Defense in depth: the caller names the review request that it made. This
	// check stays so that a number of the pull request of a person, or of a
	// fork, is never closed through the write-back. It runs for each state, so
	// that the answer does not depend on the state of a pull request that is not
	// ours.
	if !forge.IsWriteBackBranch(got.Branch) || got.FromFork() {
		return fmt.Errorf("%w: the pull request is not one of the write-back", forge.ErrRefused)
	}

	switch got.State {
	case forge.ReviewMerged:
		return errMerged()
	case forge.ReviewClosed:
		return nil
	case forge.ReviewOpen:
		return f.patchClosed(ctx, r, bearer, got)
	default:
		return unreadableReview()
	}
}

// errMerged is the error for a pull request that merged.
func errMerged() error {
	return fmt.Errorf("%w: the pull request merged", forge.ErrConflict)
}

// patchClosed closes the open pull request got and reads the answer.
func (f *gitHub) patchClosed(ctx context.Context, r repoName, bearer string, got forge.Review) error {
	// Defense in depth: the caller names the review request that it made. This
	// check stays so that a number of the pull request of a person, or of a
	// fork, is never closed through the write-back.
	if !forge.IsWriteBackBranch(got.Branch) || got.FromFork() {
		return fmt.Errorf("%w: the pull request is not one of the write-back", forge.ErrRefused)
	}

	resp, err := f.send(ctx, call{
		method:   http.MethodPatch,
		path:     r.path() + "/pulls/" + got.ID,
		bearer:   bearer,
		body:     []byte(`{"state":"closed"}`),
		status:   f.reviewStatus,
		oversize: nil,
		target:   "",
	})
	if errors.Is(err, forge.ErrInvalid) {
		// GitHub refuses to close a pull request that merged after the read.
		again, readErr := f.fetchReview(ctx, r, bearer, got.ID)
		if readErr == nil && again.State == forge.ReviewMerged {
			return errMerged()
		}
	}

	if err != nil {
		return err
	}

	closed, err := reviewFrom(resp.body, got.ID)
	if err != nil {
		return err
	}

	switch closed.State {
	case forge.ReviewClosed:
		return nil
	case forge.ReviewMerged:
		return errMerged()
	case forge.ReviewOpen:
		return forge.Unavailable(0, "GitHub did not close the pull request")
	default:
		return unreadableReview()
	}
}

// DeleteBranch deletes a branch of the write-back. The branch name starts with
// forge.WriteBackBranchPrefix, and the branch is not the default branch of the
// repository, which the adapter reads before the delete. The adapter also reads
// the branch just before the delete, and refuses a protected branch and a branch
// that an open review request has as its head.
//
// It returns forge.ErrInvalid for a name that fails the checks, before any
// call. It returns forge.ErrRefused for the default branch, for a protected
// branch and for a missing permission, and forge.ErrNotFound for a missing
// repository or branch.
func (f *gitHub) DeleteBranch(ctx context.Context, repo, branch string) error {
	if err := f.reviewAllowed("DeleteBranch"); err != nil {
		return err
	}

	r, err := splitRepo(repo)
	if err != nil {
		return err
	}

	// Defense in depth: the caller passes the branch of a job. This check stays
	// so that a new caller, or a name from a stored record that changed, never
	// deletes a branch that the write-back did not make. It does not rely on the
	// check of the default branch below.
	if refErr := validateRef(branch); refErr != nil {
		return refErr
	}

	if !forge.IsWriteBackBranch(branch) {
		return fmt.Errorf(
			"%w: only a branch that starts with %q can be deleted", forge.ErrInvalid, forge.WriteBackBranchPrefix,
		)
	}

	bearer, err := f.reviewBearer(ctx, r)
	if err != nil {
		return f.forget(r, err)
	}

	if deleteErr := f.removeBranch(ctx, r, bearer, branch); deleteErr != nil {
		return f.forget(r, deleteErr)
	}

	return nil
}

func (f *gitHub) removeBranch(ctx context.Context, r repoName, bearer, branch string) error {
	def, err := f.defaultBranch(ctx, r, bearer)
	if err != nil {
		return err
	}

	// Defense in depth: the prefix check above refuses most default branches.
	// This check stays so that a repository whose default branch has the prefix
	// of the write-back never loses it. It does not rely on the prefix check.
	if branch == def {
		return fmt.Errorf("%w: the branch is the default branch of the repository", forge.ErrRefused)
	}

	if guardErr := f.guardBranch(ctx, r, bearer, branch); guardErr != nil {
		return guardErr
	}

	resp, err := f.send(ctx, call{
		method: http.MethodDelete, path: r.path() + "/git/refs/heads/" + escapePath(branch), bearer: bearer,
		body: nil, status: f.deleteStatus, oversize: nil, target: "",
	})
	if err != nil {
		return err
	}

	if resp.status != http.StatusNoContent {
		return forge.Unavailable(0, "GitHub gave an answer for the delete that is not the expected one")
	}

	return nil
}

// keyProtected is the field of the answer for a branch that says whether a rule
// protects it.
const keyProtected = "protected"

// guardBranch reads the state of the branch just before the delete, and refuses
// the delete when the host says that a rule protects the branch, or that an
// open review request has the branch as its head. A delete would close that
// review request. This narrows the race window of the default branch read: the
// answers are read in the same moment as the delete, and an answer that cannot
// be read refuses the delete.
//
// Defense in depth: GitHub refuses to delete a protected branch too. This check
// stays so that the adapter does not rely on that answer.
func (f *gitHub) guardBranch(ctx context.Context, r repoName, bearer, branch string) error {
	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/branches/" + escapePath(branch), bearer: bearer, body: nil,
		status: f.reviewStatus, oversize: nil, target: "",
	})
	if err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if decodeErr := decode(resp.body, &fields); decodeErr != nil {
		return decodeErr
	}

	var protected bool
	if json.Unmarshal(fields[keyProtected], &protected) != nil {
		return forge.Unavailable(0, "GitHub gave a branch with no protection flag")
	}

	if protected {
		return fmt.Errorf("%w: the branch is protected", forge.ErrRefused)
	}

	for _, side := range []string{"head=" + url.QueryEscape(r.owner+":"+branch), "base=" + url.QueryEscape(branch)} {
		if openErr := f.refuseOpenReview(ctx, r, bearer, side); openErr != nil {
			return openErr
		}
	}

	return nil
}

// refuseOpenReview returns forge.ErrRefused when an open pull request matches the
// query side ("head=owner:branch" or "base=branch"). A delete would close it.
func (f *gitHub) refuseOpenReview(ctx context.Context, r repoName, bearer, side string) error {
	open, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path() + "/pulls?state=open&per_page=1&" + side, bearer: bearer, body: nil,
		status: f.reviewStatus, oversize: nil, target: "",
	})
	if err != nil {
		return err
	}

	var list []json.RawMessage
	if decodeErr := decode(open.body, &list); decodeErr != nil {
		return decodeErr
	}

	if len(list) > 0 {
		return fmt.Errorf("%w: an open review request has the branch as its head or its base", forge.ErrRefused)
	}

	return nil
}

// keyDefaultBranch is the field of the answer for a repository that holds the
// default branch.
const keyDefaultBranch = "default_branch"

// defaultBranch returns the default branch of the repository.
func (f *gitHub) defaultBranch(ctx context.Context, r repoName, bearer string) (string, error) {
	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path(), bearer: bearer, body: nil, status: f.reviewStatus, oversize: nil,
		target: "",
	})
	if err != nil {
		return "", err
	}

	var fields map[string]json.RawMessage
	if decodeErr := decode(resp.body, &fields); decodeErr != nil {
		return "", decodeErr
	}

	var def string
	if json.Unmarshal(fields[keyDefaultBranch], &def) != nil || validateRef(def) != nil {
		return "", forge.Unavailable(0, "GitHub gave a repository with no default branch")
	}

	return def, nil
}

// reviewExistsError is the answer of GitHub for a pull request that exists.
type reviewExistsError struct{}

func (reviewExistsError) Error() string { return "the pull request exists already" }

// validationBody is the form of an error 422 of GitHub.
type validationBody struct {
	Message string            `json:"message"`
	Errors  []validationError `json:"errors"`
}

type validationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// reviewStatus maps the status of a call about pull requests. GitHub answers
// 422 for a head or a base that is not a branch (forge.ErrNotFound), for a
// pull request that exists (the answer of the open call handles it), for a
// branch with no commit that the target lacks (forge.ErrRefused), and for any
// other problem with the request (forge.ErrInvalid). The other statuses are
// the ones of the commit calls.
func (f *gitHub) reviewStatus(r response) error {
	if r.status != http.StatusUnprocessableEntity {
		return f.commitStatus(r)
	}

	var body validationBody
	if decode(r.body, &body) != nil {
		return f.commitStatus(r)
	}

	for _, e := range body.Errors {
		switch {
		case (e.Field == "head" || e.Field == "base") && e.Code == "invalid":
			return fmt.Errorf("%w: GitHub has no such branch for the pull request", forge.ErrNotFound)
		case strings.Contains(e.Message, "already exists"):
			return reviewExistsError{}
		case strings.Contains(e.Message, "No commits between"):
			return fmt.Errorf("%w: the branch has no commit that the target lacks", forge.ErrRefused)
		}
	}

	return f.commitStatus(r)
}

// deleteStatus maps the status of the call that deletes a branch. GitHub
// answers 422 for a branch that does not exist and for a protected branch.
func (f *gitHub) deleteStatus(r response) error {
	if r.status != http.StatusUnprocessableEntity {
		return f.commitStatus(r)
	}

	var body errorBody
	if decode(r.body, &body) != nil {
		return f.commitStatus(r)
	}

	switch {
	case strings.Contains(body.Message, "does not exist"):
		return fmt.Errorf("%w: GitHub has no such branch", forge.ErrNotFound)
	case strings.Contains(body.Message, "protected"):
		return fmt.Errorf("%w: the branch is protected", forge.ErrRefused)
	}

	return f.commitStatus(r)
}

// checkReview checks req with the checks of the port and the checks of the
// adapter.
//
// Defense in depth: forge.ValidateReviewRequest checks the request, the caller
// of the port may check it too, and GitHub checks it. The adapter checks stay
// so that a request that fails one of them never goes into a call, also when
// the check of the port changes. None relies on another.
func checkReview(req forge.ReviewRequest) error {
	if err := forge.ValidateReviewRequest(req); err != nil {
		return fmt.Errorf("the review request: %w", err)
	}

	return checkReviewLocal(req)
}

// checkReviewLocal is the part of checkReview that does not use the port.
func checkReviewLocal(req forge.ReviewRequest) error {
	if err := validateRef(req.Branch); err != nil {
		return err
	}

	if err := validateRef(req.Target); err != nil {
		return err
	}

	if req.Branch == req.Target {
		return fmt.Errorf("%w: the branch and the target of a review request are the same", forge.ErrInvalid)
	}

	if strings.TrimSpace(req.Title) == "" || !reviewTitle(req.Title) {
		return fmt.Errorf(
			"%w: the title of a review request is one line of 1 to %d bytes of text",
			forge.ErrInvalid, forge.MaxReviewTitleBytes,
		)
	}

	if !reviewBody(req.Body) {
		return fmt.Errorf(
			"%w: the description of a review request is text of %d bytes at most",
			forge.ErrInvalid, forge.MaxReviewBodyBytes,
		)
	}

	return nil
}

// reviewTitle reports whether s is valid UTF-8 of limit bytes at most, with no
// control character.
func reviewTitle(s string) bool {
	return len(s) <= forge.MaxReviewTitleBytes && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

// reviewBody reports whether s is valid UTF-8 of limit bytes at most, with no
// control character except the line break and the tab.
func reviewBody(s string) bool {
	if len(s) > forge.MaxReviewBodyBytes || !utf8.ValidString(s) {
		return false
	}

	return !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	})
}

// validReviewID checks the number of a review request with an allow list: 1 to
// maxReviewIDDigits ASCII digits, with no zero at the start.
//
// Defense in depth: the caller passes an ID that the adapter gave. This check
// stays so that an ID from a record, a comment or a request never changes the
// path of the call.
func validReviewID(id string) error {
	bad := id == "" || len(id) > maxReviewIDDigits || id[0] == '0'

	for i := 0; i < len(id) && !bad; i++ {
		bad = id[i] < '0' || id[i] > '9'
	}

	if bad {
		return fmt.Errorf("%w: the ID of a review request is a number", forge.ErrInvalid)
	}

	return nil
}

// pullAnswer is the part of the answer for a pull request that the adapter
// reads. Every field is host data that is not trusted. parsePull fills it.
type pullAnswer struct {
	Number         wholeNumber
	State          string
	HTMLURL        string
	User           *pullUser
	Merged         bool
	MergedAt       *time.Time
	MergeCommitSHA *string
	Mergeable      *bool
	MergeableState string
	Base           pullEnd
	Head           pullEnd
}

// The fields of the answer for a pull request.
const (
	keyNumber         = "number"
	keyState          = "state"
	keyHTMLURL        = "html_url"
	keyUser           = "user"
	keyMerged         = "merged"
	keyMergedAt       = "merged_at"
	keyMergeCommitSHA = "merge_commit_sha"
	keyMergeable      = "mergeable"
	keyMergeableState = "mergeable_state"
	keyBase           = "base"
	keyHead           = "head"
)

// parsePull decodes the answer for a pull request. A field of the wrong type
// makes it fail; a field that is missing keeps its zero value, which the checks
// of reviewFrom refuse.
func parsePull(body []byte) (pullAnswer, error) {
	var fields map[string]json.RawMessage
	if err := decode(body, &fields); err != nil {
		return pullAnswer{}, err
	}

	var a pullAnswer

	targets := map[string]any{
		keyNumber: &a.Number, keyState: &a.State, keyHTMLURL: &a.HTMLURL, keyUser: &a.User, keyMerged: &a.Merged,
		keyMergedAt: &a.MergedAt, keyMergeCommitSHA: &a.MergeCommitSHA, keyMergeable: &a.Mergeable,
		keyMergeableState: &a.MergeableState, keyBase: &a.Base, keyHead: &a.Head,
	}

	for key, target := range targets {
		if raw, found := fields[key]; found && json.Unmarshal(raw, target) != nil {
			return pullAnswer{}, unreadableReview()
		}
	}

	return a, nil
}

type pullUser struct {
	ID wholeNumber `json:"id"`
}

type pullEnd struct {
	Ref  string    `json:"ref"`
	SHA  string    `json:"sha"`
	Repo *pullRepo `json:"repo"`
}

type pullRepo struct {
	ID wholeNumber `json:"id"`
}

// repoID returns the ID of the repository of an end, and false when the end has
// no repository (a fork that was deleted) or an ID that is not a number.
func (e pullEnd) repoID() (string, bool) {
	if e.Repo == nil {
		return "", false
	}

	return positiveNumber(e.Repo.ID)
}

// wholeNumber is a JSON number as it was written. It is not a JSON string that
// holds digits, and not null: those make the decode fail.
type wholeNumber string

// UnmarshalJSON reads a JSON number. A value of another JSON type is an error.
func (n *wholeNumber) UnmarshalJSON(data []byte) error {
	var number json.Number
	if len(data) == 0 || data[0] == '"' || json.Unmarshal(data, &number) != nil {
		return errNotNumber
	}

	*n = wholeNumber(number.String())

	return nil
}

var errNotNumber = errors.New("the value is not a JSON number")

// positiveNumber returns n as a canonical decimal string when it is a whole
// number above zero.
func positiveNumber(n wholeNumber) (string, bool) {
	v, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || v < 1 || strconv.FormatInt(v, 10) != string(n) {
		return "", false
	}

	return string(n), true
}

// unreadableReview is the error for an answer that does not pass a check.
func unreadableReview() error {
	return forge.Unavailable(0, "GitHub gave a pull request that cannot be read")
}

// reviewFrom decodes and checks the answer for a pull request. When wantID is
// not empty, the answer must be for that number. Any field that fails its check
// makes the whole answer fail: the adapter never returns a part of an answer
// that it could not check.
func reviewFrom(body []byte, wantID string) (forge.Review, error) {
	// Defense in depth: the egress rules limit the size of every answer. This
	// limit is lower and stays so that a larger answer is never decoded.
	if len(body) > maxReviewBytes {
		return forge.Review{}, forge.Unavailable(0, "GitHub gave an answer above the size limit")
	}

	a, err := parsePull(body)
	if err != nil {
		return forge.Review{}, err
	}

	number, ok := positiveNumber(a.Number)
	if !ok || (wantID != "" && number != wantID) || validReviewID(number) != nil {
		return forge.Review{}, unreadableReview()
	}

	state, mergedAt, err := reviewState(a)
	if err != nil {
		return forge.Review{}, err
	}

	got, err := reviewEnds(a)
	if err != nil {
		return forge.Review{}, err
	}

	got.ID, got.State, got.MergedAt = number, state, mergedAt

	if finishErr := finishReview(&got, a); finishErr != nil {
		return forge.Review{}, finishErr
	}

	return got, nil
}

// finishReview sets the fields of got that depend on its state. Only an open
// review request has a merge state, and only a merged one has a merge commit.
func finishReview(got *forge.Review, a pullAnswer) error {
	got.Mergeable = forge.MergeUnknown

	switch got.State {
	case forge.ReviewOpen:
		got.Mergeable, got.TestMergeCommit = openMerge(a)

		return testMergeError(a)
	case forge.ReviewMerged:
		if a.MergeCommitSHA == nil || forge.ValidateCommitID(*a.MergeCommitSHA) != nil {
			return unreadableReview()
		}

		got.MergeCommit = *a.MergeCommitSHA

		return nil
	case forge.ReviewClosed:
		return nil
	default:
		return unreadableReview()
	}
}

// testMergeError returns an error for an open pull request whose test merge
// commit is set and is not a commit ID.
func testMergeError(a pullAnswer) error {
	if a.MergeCommitSHA != nil && *a.MergeCommitSHA != "" && forge.ValidateCommitID(*a.MergeCommitSHA) != nil {
		return unreadableReview()
	}

	return nil
}

// reviewState reads the state of the pull request and the merge time. GitHub
// has two states, open and closed, and a flag for a merge. A combination that
// has no meaning fails.
func reviewState(a pullAnswer) (forge.ReviewState, time.Time, error) {
	var mergedAt time.Time
	if a.MergedAt != nil {
		mergedAt = a.MergedAt.UTC()
	}

	switch {
	case a.State == "open" && !a.Merged && mergedAt.IsZero():
		return forge.ReviewOpen, time.Time{}, nil
	case a.State == "closed" && a.Merged && !mergedAt.IsZero():
		return forge.ReviewMerged, mergedAt, nil
	case a.State == "closed" && !a.Merged && mergedAt.IsZero():
		return forge.ReviewClosed, time.Time{}, nil
	}

	return "", time.Time{}, unreadableReview()
}

// reviewEnds reads the branches, the commits, the repositories, the author and
// the address of the pull request. A head with no repository (the fork was
// deleted) keeps an empty HeadRepoID, which forge.Review.FromFork reports as a
// fork.
func reviewEnds(a pullAnswer) (forge.Review, error) {
	baseRepo, baseOK := a.Base.repoID()
	headRepo, _ := a.Head.repoID()

	if a.Head.Repo != nil && headRepo == "" {
		// A head repository with an ID that is not a number is not a deleted fork.
		return forge.Review{}, unreadableReview()
	}

	author, authorOK := "", false
	if a.User != nil {
		author, authorOK = positiveNumber(a.User.ID)
	}

	bad := !baseOK || !authorOK || validateRef(a.Head.Ref) != nil || validateRef(a.Base.Ref) != nil ||
		forge.ValidateCommitID(a.Head.SHA) != nil || forge.ValidateCommitID(a.Base.SHA) != nil ||
		!reviewURL(a.HTMLURL)
	if bad {
		return forge.Review{}, unreadableReview()
	}

	return forge.Review{
		ID: "", URL: a.HTMLURL, State: "", Branch: a.Head.Ref, Target: a.Base.Ref, Head: a.Head.SHA,
		BaseCommit: a.Base.SHA, BaseRepoID: baseRepo, HeadRepoID: headRepo, AuthorID: author, TestMergeCommit: "",
		Mergeable: "", MergedAt: time.Time{}, MergeCommit: "",
	}, nil
}

// reviewURL reports whether s is an https address of a moderate length with a
// host and no user information.
func reviewURL(s string) bool {
	if s == "" || len(s) > maxReviewURLBytes {
		return false
	}

	u, err := url.Parse(s)
	if err != nil || u == nil {
		return false
	}

	return u.Scheme == schemeHTTPS && u.Host != "" && u.User == nil
}

// hostMergeStates maps the mergeable state of GitHub to the merge state of the
// port, for each state except "clean", which needs the flag too.
func hostMergeStates() map[string]forge.MergeState {
	return map[string]forge.MergeState{
		"dirty": forge.MergeConflict, "blocked": forge.MergeBlocked, "behind": forge.MergeBehind,
		"unstable": forge.MergeUnstable, "draft": forge.MergeDraft,
	}
}

// openMerge reads the merge state of an open pull request, and the commit of the
// test merge. Only the state "clean" with a mergeable flag of true is
// forge.MergeClean. The test merge commit is the commit that GitHub made, and
// it is set only when GitHub says that the pull request can merge. A flag that
// GitHub has not computed (null) and a state that the adapter does not know
// are forge.MergeUnknown, so that the caller never takes them for clean.
func openMerge(a pullAnswer) (forge.MergeState, string) {
	mergeable := a.Mergeable != nil && *a.Mergeable

	state, known := hostMergeStates()[a.MergeableState]
	if !known {
		state = forge.MergeUnknown
	}

	if a.MergeableState == "clean" && mergeable {
		state = forge.MergeClean
	}

	if state == forge.MergeUnknown || !mergeable || a.MergeCommitSHA == nil {
		return state, ""
	}

	return state, *a.MergeCommitSHA
}
