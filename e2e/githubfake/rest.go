package githubfake

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
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
	mux.HandleFunc("GET /user", f.getUser)
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{ref...}", f.getCommit)
	mux.HandleFunc("GET /repos/{owner}/{repo}/contents/{path...}", f.getContents)
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/blobs/{sha}", f.getBlob)
	mux.HandleFunc("POST /graphql", f.graphql)

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

	w.Header().Set("X-Oauth-Scopes", "repo, read:org")
	f.writeJSON(w, http.StatusOK, wireAccount{ID: f.cfg.User.ID, Login: f.cfg.User.Login})
}

// repoOf finds the repository of the path and checks that the credential can
// read its contents. It answers 404 for a repository that does not exist and
// for one that the credential cannot see. The caller holds the lock.
func (f *Fake) repoOf(w http.ResponseWriter, r *http.Request, perm, level string) (*repo, bool) {
	if !f.authorize(w, r, BearerInstallation, BearerUser) {
		return nil, false
	}

	full := r.PathValue("owner") + "/" + r.PathValue("repo")

	rp, found := f.repos[full]
	if !found || !f.permitted(r, full, "metadata", levelNameRead) {
		f.message(w, http.StatusNotFound, "Not Found")

		return nil, false
	}

	if !f.permitted(r, full, perm, level) {
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
	rp, ok := f.repoOf(w, r, "contents", levelNameRead)
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

	parents := []wireParent{}
	if c.meta.Parent != "" {
		parents = append(parents, wireParent{SHA: c.meta.Parent})
	}

	f.writeJSON(w, http.StatusOK, wireCommit{
		SHA:     c.meta.ID,
		Commit:  wireCommitBody{Message: c.meta.Message, Author: person(c.meta.Time), Committer: person(c.meta.Time)},
		Parents: parents,
	})
}

func (f *Fake) getContents(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, "contents", levelNameRead)
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
		f.writeJSON(w, http.StatusOK, fileEntry(rp, p, blob, true))

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

		out = append(out, fileEntry(rp, prefix+name, kinds[name], false))
	}

	return out
}

func (f *Fake) getBlob(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.repoOf(w, r, "contents", levelNameRead)
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
