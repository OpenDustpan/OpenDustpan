package githubfake

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BotAccountID is the ID of the account that opens a pull request when the
// call carries an installation token: the bot of the app.
const BotAccountID = 4242001

// The states of a pull request and the values of its mergeable state.
const (
	pullOpen   = "open"
	pullClosed = "closed"

	stateClean   = "clean"
	stateDirty   = "dirty"
	stateBlocked = "blocked"
	stateUnknown = "unknown"
)

// levelNamePulls is the name of the permission to read and to write pull
// requests.
const levelNamePulls = "pull_requests"

// pull is a pull request of a repository.
type pull struct {
	number int
	title  string
	body   string
	// headRepo holds the head branch. It is not the base repository for a
	// pull request from a fork.
	headRepo *repo
	headRef  string
	// headSHA is the last head commit that the fake saw. It stays when the head
	// branch goes away.
	headSHA     string
	baseRef     string
	author      Account
	state       string
	merged      bool
	mergedAt    time.Time
	mergeCommit string
	// mergeOverride is the mergeable state that a test sets. Empty means that
	// the fake computes it.
	mergeOverride string
}

// PullSpec describes a pull request that a test opens as a person does.
type PullSpec struct {
	// Base is the repository of the base, for example "acme/infra".
	Base string
	// BaseRef is the base branch.
	BaseRef string
	// Head is the repository that holds the head branch. It is Base for a
	// pull request from the same repository, and a fork otherwise.
	Head    string
	HeadRef string
	Title   string
	Body    string
	Author  Account
}

// nextRepoID returns the ID for a new repository. The caller holds the lock.
func (f *Fake) nextRepoID() int64 {
	f.repoSeq++

	return repoIDBase + f.repoSeq
}

// AddFork makes the repository fork as a copy of parent, with its own
// repository ID, and returns it. The fork holds the branches, the tags and the
// commits of the parent at this time.
func (f *Fake) AddFork(parent, fork string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	src, found := f.repos[parent]
	if !found {
		return fmt.Errorf("%w: %s", ErrNoRepo, parent)
	}

	if _, found = f.repos[fork]; found {
		return fmt.Errorf("%w: %s", ErrExists, fork)
	}

	f.repos[fork] = &repo{
		statuses:      map[string][]CommitStatus{},
		comments:      nil,
		name:          fork,
		id:            f.nextRepoID(),
		defaultBranch: src.defaultBranch,
		pulls:         nil,
		commits:       maps.Clone(src.commits),
		blobs:         maps.Clone(src.blobs),
		branches:      maps.Clone(src.branches),
		tags:          maps.Clone(src.tags),
		protected:     map[string]bool{},
	}

	return nil
}

// SetDefaultBranch sets the default branch that the repository call answers.
// It does not check that the branch exists. A delete of the default branch is
// not refused by the fake, so that a test shows that the adapter refuses it.
func (f *Fake) SetDefaultBranch(fullName, branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	r.defaultBranch = branch

	return nil
}

// Branch makes the branch name at the head of the default branch, with no
// new commit.
func (f *Fake) Branch(fullName, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	if _, found = r.branches[name]; found {
		return fmt.Errorf("%w: branch %s", ErrExists, name)
	}

	r.branches[name] = r.branches[DefaultBranch]

	return nil
}

// OpenPull opens a pull request as a person does, with no check of the
// permissions, and returns its number.
func (f *Fake) OpenPull(spec PullSpec) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	base, found := f.repos[spec.Base]
	if !found {
		return 0, fmt.Errorf("%w: %s", ErrNoRepo, spec.Base)
	}

	head, found := f.repos[spec.Head]
	if !found {
		return 0, fmt.Errorf("%w: %s", ErrNoRepo, spec.Head)
	}

	if _, found = head.branches[spec.HeadRef]; !found {
		return 0, fmt.Errorf("%w: %s", ErrNoRef, spec.HeadRef)
	}

	if _, found = base.branches[spec.BaseRef]; !found {
		return 0, fmt.Errorf("%w: %s", ErrNoRef, spec.BaseRef)
	}

	return base.addPull(spec, head).number, nil
}

// addPull adds a pull request to r. The caller holds the lock.
func (r *repo) addPull(spec PullSpec, head *repo) *pull {
	p := &pull{
		number: len(r.pulls) + 1, title: spec.Title, body: spec.Body, headRepo: head, headRef: spec.HeadRef,
		headSHA: head.branches[spec.HeadRef], baseRef: spec.BaseRef, author: spec.Author, state: pullOpen,
		merged: false, mergedAt: time.Time{}, mergeCommit: "", mergeOverride: "",
	}
	r.pulls = append(r.pulls, p)

	return p
}

// pullOf finds a pull request of a repository.
func (r *repo) pullOf(number int) *pull {
	if number < 1 || number > len(r.pulls) {
		return nil
	}

	return r.pulls[number-1]
}

// ClosePull closes a pull request with no merge, as a person does.
func (f *Fake) ClosePull(fullName string, number int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	p, err := f.pullByNumber(fullName, number)
	if err != nil {
		return err
	}

	p.state = pullClosed

	return nil
}

// MergePull merges a pull request, as a person does: a merge commit on the
// base branch, and the pull request is closed and merged.
func (f *Fake) MergePull(fullName string, number int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	p, err := f.pullByNumber(fullName, number)
	if err != nil {
		return err
	}

	if p.state != pullOpen {
		return fmt.Errorf("%w: pull request %d is not open", ErrExists, number)
	}

	r, found := f.repos[fullName]
	if !found || r == nil {
		return fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	p.refreshHead()

	c := r.mergeCommit(p, "Merge pull request #"+strconv.Itoa(number), f.now())
	r.branches[p.baseRef] = c.meta.ID
	p.state, p.merged, p.mergedAt, p.mergeCommit = pullClosed, true, c.meta.Time, c.meta.ID

	return nil
}

// SetMergeState sets the mergeable state that the fake answers for a pull
// request, as a text. The text "unknown" makes the fake answer a pull request
// that GitHub has not computed yet. The empty text restores the computed
// state.
func (f *Fake) SetMergeState(fullName string, number int, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	p, err := f.pullByNumber(fullName, number)
	if err != nil {
		return err
	}

	p.mergeOverride = state

	return nil
}

// pullByNumber finds a pull request. The caller holds the lock.
func (f *Fake) pullByNumber(fullName string, number int) (*pull, error) {
	r, found := f.repos[fullName]
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	p := r.pullOf(number)
	if p == nil {
		return nil, fmt.Errorf("%w: pull request %d", ErrNoRef, number)
	}

	return p, nil
}

// refreshHead copies the head commit of the head branch, while the branch
// exists.
func (p *pull) refreshHead() {
	if sha, found := p.headRepo.branches[p.headRef]; found {
		p.headSHA = sha
	}
}

// mergeCommit adds a commit on top of the base branch of p whose tree holds
// the files of both sides. The head wins a file that both sides have. The
// caller holds the lock.
func (r *repo) mergeCommit(p *pull, message string, at time.Time) *commit {
	tree, modes := map[string]string{}, map[string]string{}

	if tip := r.commits[r.branches[p.baseRef]]; tip != nil {
		maps.Copy(tree, tip.tree)
		maps.Copy(modes, tip.modes)
	}

	if head := p.headRepo.commits[p.headSHA]; head != nil {
		for path, blob := range head.tree {
			tree[path] = blob
			r.blobs[blob] = p.headRepo.blobs[blob]

			if mode, found := head.modes[path]; found {
				modes[path] = mode
			} else {
				delete(modes, path)
			}
		}
	}

	parent := r.branches[p.baseRef]
	c := &commit{
		meta: Commit{
			ID: commitIDWithModes(parent, tree, modes, message, at), Parent: parent, Message: message, Time: at,
			Author: "", Committer: WebFlow, Verified: true,
		},
		tree:  tree,
		modes: modes,
	}
	r.commits[c.meta.ID] = c

	return c
}

// testMerge returns the ID of the commit that GitHub makes to test the merge
// of an open pull request. It depends on the base and the head, and it is
// not on a branch. The caller holds the lock.
func (r *repo) testMerge(p *pull) string {
	id := digest("test merge ", r.branches[p.baseRef], " ", p.headSHA)

	if r.commits[id] == nil {
		tip := r.commits[r.branches[p.baseRef]]
		head := p.headRepo.commits[p.headSHA]

		if tip != nil && head != nil {
			r.commits[id] = &commit{
				meta: Commit{
					ID: id, Parent: tip.meta.ID, Message: "Test merge", Time: tip.meta.Time,
					Author: "", Committer: WebFlow, Verified: true,
				},
				tree:  maps.Clone(head.tree),
				modes: maps.Clone(head.modes),
			}
		}
	}

	return id
}

// mergeable returns the mergeable state of an open pull request, and whether
// GitHub has computed it. The caller holds the lock.
func (r *repo) mergeable(p *pull) (string, *bool) {
	state := p.mergeOverride

	switch {
	case state != "":
	case r.protected[p.baseRef]:
		state = stateBlocked
	default:
		state = stateClean
	}

	switch state {
	case stateUnknown:
		return state, nil
	case stateDirty:
		no := false

		return state, &no
	default:
		yes := true

		return state, &yes
	}
}

// The fields of the JSON bodies of the pull request calls. GitHub names them in
// snake case.
const (
	fieldHTMLURL        = "html_url"
	fieldMergedAt       = "merged_at"
	fieldMergeCommitSHA = "merge_commit_sha"
	fieldMergeableState = "mergeable_state"
	fieldFullName       = "full_name"
	fieldDefaultBranch  = "default_branch"
	fieldNumber         = "number"
)

// pullEnd is the answer for the base or the head of a pull request. A nil repo
// is a repository that does not exist any more.
func pullEnd(ref, sha string, r *repo) map[string]any {
	var repoBody any
	if r != nil {
		repoBody = map[string]any{"id": r.id, fieldFullName: r.name}
	}

	return map[string]any{"ref": ref, "sha": sha, "repo": repoBody}
}

// wirePullOf is the answer for one pull request of r. The caller holds the
// lock.
func (f *Fake) wirePullOf(r *repo, p *pull) map[string]any {
	p.refreshHead()

	body := p.body
	if f.faults.oversize > 0 {
		body = strings.Repeat("x", f.faults.oversize)
	}

	out := map[string]any{
		fieldNumber: p.number, "state": p.state, "title": p.title, "body": body,
		fieldHTMLURL: f.srv.URL + "/" + r.name + "/pull/" + strconv.Itoa(p.number),
		"user":       map[string]any{"id": p.author.ID, "login": p.author.Login},
		"merged":     p.merged, fieldMergedAt: nil, fieldMergeCommitSHA: nil, "mergeable": nil,
		fieldMergeableState: stateUnknown,
		"base":              pullEnd(p.baseRef, r.branches[p.baseRef], r),
		"head":              pullEnd(p.headRef, p.headSHA, p.headRepo),
	}

	switch {
	case p.merged:
		out[fieldMergedAt], out[fieldMergeCommitSHA] = p.mergedAt.UTC().Format(time.RFC3339), p.mergeCommit
	case p.state == pullOpen:
		state, mergeable := r.mergeable(p)
		out[fieldMergeableState], out["mergeable"] = state, mergeable

		if mergeable != nil {
			out[fieldMergeCommitSHA] = r.testMerge(p)
		}
	default:
		// A pull request that closed with no merge has no merge state.
	}

	return out
}

// pullsOf finds the repository of the path and checks that the credential
// has the permission level on pull requests, besides the read of the contents.
// The caller holds the lock.
func (f *Fake) pullsOf(w http.ResponseWriter, r *http.Request, level string) (*repo, bool) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return nil, false
	}

	if !f.permitted(r, rp.name, levelNamePulls, level) {
		f.message(w, http.StatusForbidden, "Resource not accessible by integration")

		return nil, false
	}

	return rp, true
}

// actor returns the account that a call acts as: the bot of the app for an
// installation token, and the user otherwise. The caller holds the lock.
func (f *Fake) actor(r *http.Request) Account {
	if f.bearerToken(r) != nil {
		return Account{ID: BotAccountID, Login: f.AppLogin()}
	}

	return f.cfg.User
}

// getRepo answers the repository call.
func (f *Fake) getRepo(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	f.writeJSON(w, http.StatusOK, map[string]any{
		"id": rp.id, fieldFullName: rp.name, fieldDefaultBranch: rp.defaultBranch, "fork": false,
	})
}

// getBranch answers one branch with its protection flag.
func (f *Fake) getBranch(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	name := r.PathValue("branch")

	sha, found := rp.branches[name]
	if !found {
		f.message(w, http.StatusNotFound, "Branch not found")

		return
	}

	f.writeJSON(w, http.StatusOK, wireBranch{Name: name, Protected: rp.protected[name], Commit: wireParent{SHA: sha}})
}

// pullRequest is the body of the call that opens a pull request.
type pullRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}

// validationFailed is the message of GitHub for a request that it does not
// accept.
const validationFailed = "Validation Failed"

// invalidField answers 422 for a field of a pull request.
func (f *Fake) invalidField(w http.ResponseWriter, field string) {
	f.writeJSON(w, http.StatusUnprocessableEntity, wireValidation{
		Message: validationFailed,
		Errors:  []wireValidationError{{Resource: "PullRequest", Field: field, Code: "invalid", Message: ""}},
	})
}

// customProblem answers 422 with a text, as GitHub does for a rule of pull
// requests.
func (f *Fake) customProblem(w http.ResponseWriter, text string) {
	f.writeJSON(w, http.StatusUnprocessableEntity, wireValidation{
		Message: validationFailed,
		Errors:  []wireValidationError{{Resource: "PullRequest", Field: "", Code: "custom", Message: text}},
	})
}

// createPull opens a pull request from a branch of the same repository. It
// answers 422 for a head or a base that is not a branch, for a head with no
// commit that the base lacks, and for a pull request that exists.
func (f *Fake) createPull(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.pullsOf(w, r, levelNameWrite)
	if !ok {
		return
	}

	var req pullRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.message(w, http.StatusBadRequest, "Problems parsing JSON")

		return
	}

	headSHA, headFound := rp.branches[req.Head]
	_, baseFound := rp.branches[req.Base]

	switch {
	case req.Title == "":
		f.invalidField(w, "title")

		return
	case !headFound:
		f.invalidField(w, "head")

		return
	case !baseFound:
		f.invalidField(w, "base")

		return
	}

	for _, p := range rp.pulls {
		if p.state == pullOpen && p.headRepo == rp && p.headRef == req.Head && p.baseRef == req.Base {
			f.customProblem(w, "A pull request already exists for "+rp.name+":"+req.Head+".")

			return
		}
	}

	// A head that is the base, or one of its ancestors, has no commit that the
	// base lacks.
	if _, inBase := rp.depthOf(rp.commits[rp.branches[req.Base]], headSHA); inBase {
		f.customProblem(w, "No commits between "+req.Base+" and "+req.Head)

		return
	}

	p := rp.addPull(PullSpec{
		Base: rp.name, BaseRef: req.Base, Head: rp.name, HeadRef: req.Head, Title: req.Title, Body: req.Body,
		Author: f.actor(r),
	}, rp)

	f.writeJSON(w, http.StatusCreated, f.wirePullOf(rp, p))
}

// listPulls answers the pull requests of a repository, filtered by state,
// head ("owner:branch") and base.
func (f *Fake) listPulls(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.pullsOf(w, r, levelNameRead)
	if !ok {
		return
	}

	q := r.URL.Query()
	state, head, base := q.Get("state"), q.Get("head"), q.Get("base")

	if state == "" {
		state = pullOpen
	}

	out := []map[string]any{}

	for _, p := range rp.pulls {
		owner, _, _ := strings.Cut(p.headRepo.name, "/")

		switch {
		case state != "all" && p.state != state:
		case head != "" && head != owner+":"+p.headRef:
		case base != "" && base != p.baseRef:
		default:
			out = append(out, f.wirePullOf(rp, p))
		}
	}

	perPage := pageOf(q.Get("per_page"), defaultPerPage)
	f.writeJSON(w, http.StatusOK, out[:min(perPage, len(out))])
}

// numberOf reads the number of a pull request from the path.
func numberOf(r *http.Request) int {
	n, err := strconv.Atoi(r.PathValue(fieldNumber))
	if err != nil {
		return 0
	}

	return n
}

func (f *Fake) getPull(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.pullsOf(w, r, levelNameRead)
	if !ok {
		return
	}

	p := rp.pullOf(numberOf(r))
	if p == nil {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	f.writeJSON(w, http.StatusOK, f.wirePullOf(rp, p))
}

// pullPatch is the body of the call that changes a pull request.
type pullPatch struct {
	State string `json:"state"`
}

// patchPull closes a pull request. It answers 422 for a pull request that
// merged, and for a state other than "closed".
func (f *Fake) patchPull(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.pullsOf(w, r, levelNameWrite)
	if !ok {
		return
	}

	p := rp.pullOf(numberOf(r))
	if p == nil {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	var req pullPatch

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.message(w, http.StatusBadRequest, "Problems parsing JSON")

		return
	}

	switch {
	case req.State != pullClosed:
		f.invalidField(w, "state")
	case p.merged:
		f.customProblem(w, "The pull request merged.")
	default:
		p.state = pullClosed

		f.writeJSON(w, http.StatusOK, f.wirePullOf(rp, p))
	}
}

// deleteRef deletes a branch. It answers 422 for a branch that does not exist
// and for a protected branch. It does not refuse the default branch, so that
// a test shows that the adapter does.
func (f *Fake) deleteRef(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameWrite)
	if !ok {
		return
	}

	name, isBranch := strings.CutPrefix(r.PathValue("ref"), "heads/")

	switch _, found := rp.branches[name]; {
	case !isBranch || !found:
		f.message(w, http.StatusUnprocessableEntity, "Reference does not exist")
	case rp.protected[name]:
		f.message(w, http.StatusUnprocessableEntity, "Cannot delete this protected branch")
	default:
		f.closePullsOf(rp, name)
		delete(rp.branches, name)
		w.WriteHeader(http.StatusNoContent)
	}
}

// closePullsOf closes the open pull requests whose head is the branch name of
// rp, as GitHub does when it deletes the branch. The caller holds the lock.
func (f *Fake) closePullsOf(rp *repo, name string) {
	for _, other := range f.repos {
		for _, p := range other.pulls {
			if p.headRepo != rp || p.headRef != name {
				continue
			}

			p.refreshHead()

			if p.state == pullOpen {
				p.state = pullClosed
			}
		}
	}
}

// Oversize makes the fake answer each pull request with a description of n
// bytes more, until the returned function restores it.
func (f *Fake) Oversize(n int) func() {
	return f.setFault(func(x *faults) { x.oversize = n }, func(x *faults) { x.oversize = 0 })
}
