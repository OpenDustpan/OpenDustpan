package githubfake

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// contentsInlineLimit is the largest file whose content the contents call
// answers. For a larger file, GitHub answers the encoding "none" and an
// empty content, and the caller reads the blob.
const contentsInlineLimit = 1 << 20

// base64LineBytes is the number of characters in a line of a base64 content
// that GitHub answers.
const base64LineBytes = 60

func (f *Fake) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /app/installations/{id}", f.getInstallation)
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", f.createInstallationToken)
	mux.HandleFunc("DELETE /installation/token", f.revokeInstallationToken)
	mux.HandleFunc("GET /app", f.getApp)
	mux.HandleFunc("GET /user", f.getUser)
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/trees/{sha}", f.getTree)
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits", f.listCommits)
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{ref...}", f.getCommit)
	mux.HandleFunc("GET /repos/{owner}/{repo}/compare/{spec...}", f.compare)
	mux.HandleFunc("POST /repos/{owner}/{repo}/statuses/{sha}", f.createStatus)
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/ref/{ref...}", f.getRef)
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/refs", f.createRef)
	mux.HandleFunc("GET /repos/{owner}/{repo}/contents/{path...}", f.getContents)
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/blobs/{sha}", f.getBlob)
	mux.HandleFunc("GET /repos/{owner}/{repo}", f.getRepo)
	mux.HandleFunc("GET /repos/{owner}/{repo}/branches/{branch...}", f.getBranch)
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls", f.createPull)
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls", f.listPulls)
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{number}", f.getPull)
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/pulls/{number}", f.patchPull)
	mux.HandleFunc("DELETE /repos/{owner}/{repo}/git/refs/{ref...}", f.deleteRef)
	mux.HandleFunc("POST /graphql", f.graphql)
	// GitHub answers a path that it does not have, and a method that a path does
	// not have, with the same JSON 404. Without this pattern, the mux answers
	// plain text 404 and 405.
	mux.HandleFunc("/", f.notFound)

	return f.record(mux)
}

// writeJSON writes an answer. With the fault garble, a success answer holds
// a body that does not parse. The caller holds the lock.
func (f *Fake) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if f.faults.garble && status < http.StatusMultipleChoices {
		writeBody(w, []byte("not json {"))

		return
	}

	data, err := json.Marshal(v)
	if err != nil {
		return
	}

	writeBody(w, data)
}

// writeBody writes the body of an answer. A write that fails means that the
// client closed the connection, and there is nobody to tell.
func writeBody(w io.Writer, data []byte) {
	if _, err := w.Write(data); err != nil {
		return
	}
}

// message answers an error in the form of GitHub.
func (f *Fake) message(w http.ResponseWriter, status int, text string) {
	f.writeJSON(w, status, wireMessage{Message: text})
}

// notFound answers a request that no other pattern serves.
func (f *Fake) notFound(w http.ResponseWriter, _ *http.Request) {
	f.message(w, http.StatusNotFound, "Not Found")
}

func (f *Fake) getInstallation(w http.ResponseWriter, r *http.Request) {
	if !f.authorize(w, r, BearerApp) {
		return
	}

	if r.PathValue("id") != f.cfg.InstallationID {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	f.writeJSON(w, http.StatusOK, wireInstallation{
		ID:          json.Number(f.cfg.InstallationID),
		Account:     wireAccount{ID: f.cfg.Owner.ID, Login: f.cfg.Owner.Login},
		Permissions: f.cfg.Permissions,
	})
}

// tokenRequest is the body of the call that makes an installation token.
type tokenRequest struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

func (f *Fake) createInstallationToken(w http.ResponseWriter, r *http.Request) {
	if !f.authorize(w, r, BearerApp) {
		return
	}

	if r.PathValue("id") != f.cfg.InstallationID {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	var req tokenRequest

	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.message(w, http.StatusBadRequest, "Problems parsing JSON")

			return
		}
	}

	perms, problem := f.grantable(req)
	if problem != "" {
		f.message(w, http.StatusUnprocessableEntity, problem)

		return
	}

	t := f.newToken(perms, req.Repositories)

	f.writeJSON(w, http.StatusCreated, map[string]any{
		"token":                t.value,
		"expires_at":           t.expires.UTC().Format(time.RFC3339),
		"permissions":          t.permissions,
		"repository_selection": selection(req.Repositories),
		"repositories":         f.repositoryList(req.Repositories),
	})
}

// grantable returns the permissions of the token that req asks for, or the
// text of the problem. The caller holds the lock.
func (f *Fake) grantable(req tokenRequest) (map[string]string, string) {
	perms := f.cfg.Permissions

	if req.Permissions != nil {
		perms = req.Permissions

		for name, level := range perms {
			if levelRank(level) == 0 || levelRank(level) > levelRank(f.cfg.Permissions[name]) {
				return nil, "The permissions requested are not granted to this installation."
			}
		}
	}

	for _, name := range req.Repositories {
		if !f.hasRepoNamed(name) {
			return nil, "There is at least one repository that does not exist or is not accessible to the " +
				"parent installation."
		}
	}

	// GitHub gives every installation token the permission to read the
	// metadata, also when the request does not name it.
	perms = maps.Clone(perms)
	if perms["metadata"] == "" {
		perms["metadata"] = levelNameRead
	}

	return perms, ""
}

// repositoryList is the list of repositories in the answer of a token
// request: the selected repositories, and none for a token of all of them.
func (f *Fake) repositoryList(names []string) []map[string]string {
	if names == nil {
		return nil
	}

	out := make([]map[string]string, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]string{"name": n, "full_name": f.cfg.Owner.Login + "/" + n})
	}

	return out
}

func selection(repos []string) string {
	if repos == nil {
		return "all"
	}

	return "selected"
}

func (f *Fake) revokeInstallationToken(w http.ResponseWriter, r *http.Request) {
	if !f.authorize(w, r, BearerInstallation) {
		return
	}

	if t := f.bearerToken(r); t != nil {
		t.revoked = true
	}

	w.WriteHeader(http.StatusNoContent)
}

func (f *Fake) getUser(w http.ResponseWriter, r *http.Request) {
	if !f.authorize(w, r, BearerUser) {
		return
	}

	if !f.faults.expiry.IsZero() {
		w.Header().
			Set("Github-Authentication-Token-Expiration", f.faults.expiry.UTC().Format("2006-01-02 15:04:05 UTC"))
	}

	// Only a classic token has scopes. A fine-grained token has none.
	if f.cfg.ClassicToken {
		w.Header().Set("X-Oauth-Scopes", "repo, read:org")
	}

	f.writeJSON(w, http.StatusOK, wireAccount{ID: f.cfg.User.ID, Login: f.cfg.User.Login})
}

// repoOf finds the repository of the path and checks that the credential has the
// permission level (read or write) on its contents. It answers 404 for a repository that does not exist and
// for one that the credential cannot see. The caller holds the lock.
func (f *Fake) repoOf(w http.ResponseWriter, r *http.Request, level string) (*repo, bool) {
	if !f.authorize(w, r, BearerInstallation, BearerUser) {
		return nil, false
	}

	full := r.PathValue("owner") + "/" + r.PathValue("repo")

	rp, found := f.repos[full]
	if to, renamed := f.moved[full]; !found && renamed {
		// GitHub answers a renamed repository with a redirect to the new name.
		// The new location is the repository. The caller adds the rest of the path.
		http.Redirect(w, r, "/repos/"+to, http.StatusMovedPermanently)

		return nil, false
	}

	if !found || !f.permitted(r, full, "metadata", levelNameRead) {
		f.message(w, http.StatusNotFound, "Not Found")

		return nil, false
	}

	if !f.permitted(r, full, "contents", level) {
		f.message(w, http.StatusForbidden, "Resource not accessible by integration")

		return nil, false
	}

	return rp, true
}

// person is an author or a committer in a commit answer.
func person(at time.Time) wirePerson {
	return wirePerson{Name: "Dustpan Fake", Email: "fake@example.com", Date: at.UTC().Format(time.RFC3339)}
}

func (f *Fake) getCommit(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	ref := r.PathValue("ref")

	c, found := rp.resolve(ref)
	if !found {
		// GitHub answers 422 for a name that is no commit of the repository.
		f.message(w, http.StatusUnprocessableEntity, "No commit found for SHA: "+ref)

		return
	}

	f.writeJSON(w, http.StatusOK, wireCommitOf(c))
}

// wireCommitOf is the answer for one commit.
func wireCommitOf(c *commit) wireCommit {
	parents := []wireParent{}
	if c.meta.Parent != "" {
		parents = append(parents, wireParent{SHA: c.meta.Parent})
	}

	reason := "unsigned"
	if c.meta.Verified {
		reason = "valid"
	}

	return wireCommit{
		SHA: c.meta.ID,
		Commit: wireCommitBody{
			Message: c.meta.Message, Author: person(c.meta.Time), Committer: person(c.meta.Time),
			Verification: wireVerification{Verified: c.meta.Verified, Reason: reason},
		},
		Parents:   parents,
		Author:    loginOf(c.meta.Author),
		Committer: loginOf(c.meta.Committer),
	}
}

// loginOf returns the account for a login, or nil for no account.
func loginOf(login string) *wireLogin {
	if login == "" {
		return nil
	}

	return &wireLogin{Login: login}
}

// Limits of the list of commits, as GitHub has them.
const (
	defaultPerPage = 30
	maxPerPage     = 100
)

// listCommits answers the list of the commits that lead to the commit of the
// query sha (a branch, a tag or a commit ID; the default branch when it is
// not set), newest first. The query since keeps the commits at or after that
// time, page and per_page cut the list. The list holds the first parent
// chain only.
func (f *Fake) listCommits(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	q := r.URL.Query()

	ref := q.Get("sha")
	if ref == "" {
		ref = DefaultBranch
	}

	head, found := rp.resolve(ref)
	if !found {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	var since time.Time

	if raw := q.Get("since"); raw != "" {
		var err error
		if since, err = time.Parse(time.RFC3339, raw); err != nil {
			f.message(w, http.StatusUnprocessableEntity, "Invalid since: "+raw)

			return
		}
	}

	perPage, page := pageOf(q.Get("per_page"), defaultPerPage), pageOf(q.Get("page"), 1)

	all := []wireCommit{}

	for c := head; c != nil; c = rp.commits[c.meta.Parent] {
		if !c.meta.Time.Before(since) {
			all = append(all, wireCommitOf(c))
		}
	}

	from := min((page-1)*perPage, len(all))
	to := min(from+perPage, len(all))

	f.writeJSON(w, http.StatusOK, all[from:to])
}

// pageOf reads a positive number of a query, or fallback. The most for a
// page size is maxPerPage.
func pageOf(raw string, fallback int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return fallback
	}

	return min(n, maxPerPage)
}

// depthOf returns the number of steps from c back to the commit with the ID
// ancestor, and false when ancestor is not on the chain of parents of c.
func (r *repo) depthOf(c *commit, ancestor string) (int, bool) {
	steps := 0

	for cur := c; cur != nil; cur = r.commits[cur.meta.Parent] {
		if cur.meta.ID == ancestor {
			return steps, true
		}

		steps++
	}

	return 0, false
}

// compare answers the call "compare base...head": whether head is ahead of
// base, behind it, the same, or on another line of history, and the commit
// where the two meet. The chain of first parents is the history.
func (f *Fake) compare(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	baseRef, headRef, found := strings.Cut(r.PathValue("spec"), "...")
	base, baseFound := rp.resolve(baseRef)
	head, headFound := rp.resolve(headRef)

	if !found || !baseFound || !headFound {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	status, mergeBase, aheadBy, behindBy := "diverged", "", 0, 0

	// The merge base is the first commit on the chain of head that is also on
	// the chain of base.
	for cur := head; cur != nil; cur = rp.commits[cur.meta.Parent] {
		behind, onBase := rp.depthOf(base, cur.meta.ID)
		if !onBase {
			aheadBy++

			continue
		}

		behindBy, mergeBase = behind, cur.meta.ID

		switch {
		case aheadBy == 0 && behind == 0:
			status = "identical"
		case behind == 0:
			status = "ahead"
		case aheadBy == 0:
			status = "behind"
		default:
			status = "diverged"
		}

		break
	}

	f.writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "ahead_by": aheadBy, "behind_by": behindBy,
		"merge_base_commit": map[string]string{"sha": mergeBase}, "files": rp.changedBetween(mergeBase, head, r),
	})
}

// changedBetween returns the page of the files that changed from the commit
// with the ID mergeBase to head. A merge base that the repository does not
// have, for two lines of history that never meet, gives no file.
func (r *repo) changedBetween(mergeBase string, head *commit, req *http.Request) []map[string]string {
	mb := r.commits[mergeBase]
	if mb == nil {
		return []map[string]string{}
	}

	return pagedFiles(diffFiles(r, mb, head), req)
}

// refPrefix starts the full name of a branch.
const refPrefix = "refs/heads/"

// getRef answers a branch, by the path "heads/<name>".
func (f *Fake) getRef(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	name, isBranch := strings.CutPrefix(r.PathValue("ref"), "heads/")

	head, found := rp.branches[name]
	if !isBranch || !found {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	f.writeJSON(w, http.StatusOK, wireRef{Ref: refPrefix + name, Object: wireRefObject{SHA: head, Type: "commit"}})
}

// collides reports whether a branch is on the path of name: GitHub cannot
// have the branches "a" and "a/b" at the same time.
func (r *repo) collides(name string) bool {
	for other := range r.branches {
		if strings.HasPrefix(name, other+"/") || strings.HasPrefix(other, name+"/") {
			return true
		}
	}

	return false
}

// createRefRequest is the body of the call that makes a branch.
type createRefRequest struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// createRef makes a branch at a commit. It answers 422 for a name that is
// taken and for a commit that the repository does not have.
func (f *Fake) createRef(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameWrite)
	if !ok {
		return
	}

	var req createRefRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.message(w, http.StatusBadRequest, "Problems parsing JSON")

		return
	}

	name, isBranch := strings.CutPrefix(req.Ref, refPrefix)

	switch _, taken := rp.branches[name]; {
	case !isBranch || name == "" || rp.collides(name):
		f.message(w, http.StatusUnprocessableEntity, "Reference update failed")
	case taken:
		f.message(w, http.StatusUnprocessableEntity, "Reference already exists")
	case rp.commits[req.SHA] == nil:
		f.message(w, http.StatusUnprocessableEntity, "Object does not exist")
	default:
		rp.branches[name] = req.SHA

		f.writeJSON(w, http.StatusCreated, wireRef{Ref: req.Ref, Object: wireRefObject{SHA: req.SHA, Type: "commit"}})
	}
}

func (f *Fake) getContents(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = DefaultBranch
	}

	c, found := rp.resolve(ref)
	if !found {
		f.message(w, http.StatusNotFound, "No commit found for the ref "+ref)

		return
	}

	p := strings.Trim(r.PathValue("path"), "/")

	if blob, found := c.tree[p]; found && p != "" {
		if len(rp.blobs[blob]) > f.cfg.ContentsLimit {
			f.writeJSON(w, http.StatusForbidden, wireTooLarge{
				Message: "This API returns blobs up to 1 MB in size. The requested blob is too large to fetch via the API.",
				Errors:  []wireErrorCode{{Code: "too_large"}},
			})

			return
		}

		entry := fileEntry(rp, p, blob, true)
		entry.Type = contentsType(c.modeOf(p))

		f.writeJSON(w, http.StatusOK, entry)

		return
	}

	if entries := dirEntries(rp, c, p); len(entries) > 0 {
		f.writeJSON(w, http.StatusOK, entries)

		return
	}

	f.message(w, http.StatusNotFound, "Not Found")
}

// fileEntry is the answer of the contents call for one file. With inline
// set, it holds the content.
func fileEntry(rp *repo, p, blob string, inline bool) wireEntry {
	content := rp.blobs[blob]
	entry := wireEntry{
		Type: "file", Name: p[strings.LastIndex(p, "/")+1:], Path: p, SHA: blob, Size: len(content),
		Encoding: nil, Content: nil,
	}

	switch {
	case !inline:
	case len(content) > contentsInlineLimit:
		none, empty := "none", ""
		entry.Encoding, entry.Content = &none, &empty
	default:
		enc, text := "base64", wrapBase64(base64.StdEncoding.EncodeToString(content))
		entry.Encoding, entry.Content = &enc, &text
	}

	return entry
}

// wrapBase64 breaks s in lines, as GitHub does, with a newline at the end.
func wrapBase64(s string) string {
	var b strings.Builder

	for len(s) > base64LineBytes {
		b.WriteString(s[:base64LineBytes])
		b.WriteByte('\n')

		s = s[base64LineBytes:]
	}

	b.WriteString(s)
	b.WriteByte('\n')

	return b.String()
}

// dirEntries lists the direct children of the directory dir ("" is the
// root), in the order of their names.
func dirEntries(rp *repo, c *commit, dir string) []wireEntry {
	prefix := dir
	if prefix != "" {
		prefix += "/"
	}

	// A name maps to its blob ID, or to "" for a directory.
	kinds := map[string]string{}

	for p, blob := range c.tree {
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok {
			continue
		}

		if name, _, nested := strings.Cut(rest, "/"); nested {
			kinds[name] = ""
		} else {
			kinds[rest] = blob
		}
	}

	names := slices.Sorted(maps.Keys(kinds))
	out := make([]wireEntry, 0, len(names))

	for _, name := range names {
		if kinds[name] == "" {
			out = append(out, wireEntry{
				Type: "dir", Name: name, Path: prefix + name, SHA: "", Size: 0, Encoding: nil, Content: nil,
			})

			continue
		}

		entry := fileEntry(rp, prefix+name, kinds[name], false)
		entry.Type = contentsType(c.modeOf(prefix + name))
		out = append(out, entry)
	}

	return out
}

// contentsType returns the type that the contents call gives for an entry
// with the mode.
func contentsType(mode string) string {
	switch mode {
	case modeSymlink:
		return "symlink"
	case modeSubmodule:
		return "submodule"
	default:
		return "file"
	}
}

func (f *Fake) getBlob(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, levelNameRead)
	if !ok {
		return
	}

	sha := r.PathValue("sha")

	content, found := rp.blobs[sha]
	if !found {
		f.message(w, http.StatusNotFound, "Not Found")

		return
	}

	f.writeJSON(w, http.StatusOK, wireBlob{
		SHA: sha, Size: len(content), Encoding: "base64",
		Content: wrapBase64(base64.StdEncoding.EncodeToString(content)),
	})
}
