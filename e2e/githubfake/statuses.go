package githubfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// maxStatusesPerContext is the most statuses that GitHub keeps for one commit
// and one context. A call above it answers 422.
const maxStatusesPerContext = 1000

// defaultStatusContext is the context that GitHub gives to a status with no
// context.
const defaultStatusContext = "default"

// CommitStatus is a status of a commit as the fake shows it.
type CommitStatus struct {
	State       string
	Context     string
	Description string
	TargetURL   string
	// Creator is the login of the account that set the status.
	Creator string
}

// Fields of the body of the call that sets a status.
const (
	fieldState       = "state"
	fieldContext     = "context"
	fieldDescription = "description"
	fieldTargetURL   = "target_url"
)

// statusStates are the states that GitHub accepts.
func statusStates() []string { return []string{"error", "failure", "pending", "success"} }

// permissionOf checks the credential of a call that needs the metadata read
// and the permission perm at level, and no permission for the contents, as
// GitHub does for the commit statuses and the issues. It returns the repository
// and true, or answers and returns false. The caller holds the lock.
func (f *Fake) permissionOf(w http.ResponseWriter, r *http.Request, perm, level string) (*repo, bool) {
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

// createStatus sets a status on a commit. It answers 422 for a state that is
// not known, for a commit that the repository does not have and for a commit
// and a context that have the most statuses.
func (f *Fake) createStatus(w http.ResponseWriter, r *http.Request) {
	rp, ok := f.permissionOf(w, r, "statuses", levelNameWrite)
	if !ok {
		return
	}

	var req map[string]string

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.message(w, http.StatusBadRequest, "Problems parsing JSON")

		return
	}

	sha := r.PathValue("sha")

	switch {
	case rp.commits[sha] == nil:
		f.message(w, http.StatusUnprocessableEntity, "No commit found for SHA: "+sha)

		return
	case !slices.Contains(statusStates(), req[fieldState]):
		f.writeJSON(w, http.StatusUnprocessableEntity, wireValidation{
			Message: validationFailed,
			Errors:  []wireValidationError{{Resource: "Status", Field: fieldState, Code: "custom", Message: ""}},
		})

		return
	}

	name := req[fieldContext]
	if name == "" {
		name = defaultStatusContext
	}

	count := 0

	for _, s := range rp.statuses[sha] {
		if s.Context == name {
			count++
		}
	}

	if count >= maxStatusesPerContext {
		f.message(w, http.StatusUnprocessableEntity, "This SHA and context has reached the maximum number of statuses.")

		return
	}

	creator := f.actor(r)

	rp.statuses[sha] = append(rp.statuses[sha], CommitStatus{
		State: req[fieldState], Context: name, Description: req[fieldDescription], TargetURL: req[fieldTargetURL],
		Creator: creator.Login,
	})

	f.writeJSON(w, http.StatusCreated, map[string]any{
		"id": len(rp.statuses[sha]), fieldState: req[fieldState], fieldContext: name,
		fieldDescription: req[fieldDescription], fieldTargetURL: req[fieldTargetURL],
		"creator": wireAccount(creator),
	})
}

// Statuses returns the current status of each context of the commit sha of the
// repository fullName, as GitHub shows it: the newest status of a context
// replaces the older ones. The list is in the order of the contexts.
func (f *Fake) Statuses(fullName, sha string) ([]CommitStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	latest := map[string]CommitStatus{}
	for _, s := range r.statuses[sha] {
		latest[s.Context] = s
	}

	out := make([]CommitStatus, 0, len(latest))
	for _, c := range sortedKeys(latest) {
		out = append(out, latest[c])
	}

	return out, nil
}

// StatusCount returns the number of statuses that the commit sha has for the
// context, with the replaced ones.
func (f *Fake) StatusCount(fullName, sha, statusContext string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, found := f.repos[fullName]
	if !found {
		return 0, fmt.Errorf("%w: %s", ErrNoRepo, fullName)
	}

	n := 0

	for _, s := range r.statuses[sha] {
		if strings.EqualFold(s.Context, statusContext) {
			n++
		}
	}

	return n, nil
}
