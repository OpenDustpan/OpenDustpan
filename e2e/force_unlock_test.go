//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// forceUnlockReason is the reason that the API force-unlock of the scenario
// gives.
const forceUnlockReason = "the run crashed; the lock is stale"

// forceUnlockPath is the TFE-compatible force-unlock route of a workspace.
func forceUnlockPath(id string) string {
	return "/api/v2/workspaces/" + id + "/actions/force-unlock"
}

// TestForceUnlockNeedsTheForcedWriteAction proves, with each OpenTofu version
// of the matrix and on each store, that a force-unlock needs unit:write-forced:
// a maintainer is refused, and an admin releases the lock with the CLI (which
// sends no body) and with the API (which can send a reason and the lock that
// the caller saw). A lock that differs answers 409 and changes nothing, and
// each release writes one change entry in the audit log.
func TestForceUnlockNeedsTheForcedWriteAction(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	for _, version := range e2e.OpenTofu().Versions() {
		cli, installErr := fetcher.Install(installCtx, e2e.OpenTofu(), version)
		if installErr != nil {
			t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, version, installErr)
		}

		for _, db := range databases {
			t.Run(version+"/"+string(db), func(t *testing.T) {
				t.Parallel()

				if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
					t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
				}

				runForceUnlockScenario(t, cli, db)
			})
		}
	}
}

// forceUnlockRun holds what the steps of the force-unlock scenario share.
type forceUnlockRun struct {
	env        *e2e.Env
	cli        e2e.Install
	id         string
	maintainer *refusalPerson
	admin      *refusalPerson
}

// lockView is the lock that the workspace document shows.
type lockView struct {
	id     string
	holder string
}

// lockAttributes holds the lock attributes of a workspace document.
type lockAttributes struct {
	LockID     string `json:"lock-id"`     //nolint:tagliatelle // The TFE wire name; it must not change.
	LockHolder string `json:"lock-holder"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// lockData is the data object of a workspace document.
type lockData struct {
	ID         string         `json:"id"`
	Attributes lockAttributes `json:"attributes"`
}

// lockDocument reads the ID and the lock attributes of a workspace document.
type lockDocument struct {
	Data lockData `json:"data"`
}

// errorObject is one error of a JSON:API error document.
type errorObject struct {
	Code string `json:"code"`
}

// errorDocument reads the machine code of the errors of a response.
type errorDocument struct {
	Errors []errorObject `json:"errors"`
}

// runForceUnlockScenario runs the force-unlock scenario on one store.
func runForceUnlockScenario(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, refusalNamespace)

	run := forceUnlockRun{
		env:        env,
		cli:        cli,
		id:         "",
		maintainer: issueRefusalPerson(ctx, t, env, "ann", "maintainer"),
		admin:      issueRefusalPerson(ctx, t, env, "cy", "admin"),
	}

	writeRefusalTofuProject(t, env)

	for _, p := range []*refusalPerson{run.maintainer, run.admin} {
		env.WriteFile(t, filepath.Join(env.Home(), "tofurc-refuse-"+p.name), fmt.Sprintf(
			"credentials %q {\n  token = %q\n}\n", env.Host(), p.token.Secret,
		))
	}

	// The maintainer applies, so the workspace exists and holds a state.
	for _, args := range [][]string{{"init", "-input=false"}, {"apply", "-input=false", "-auto-approve"}} {
		if res := runRefusalCLI(t, env, cli, run.maintainer, args...); res.ExitCode != 0 {
			t.Fatalf("tofu %s as maintainer exits %d:\n%s", args[0], res.ExitCode, res.Stderr)
		}
	}

	run.id = run.workspaceID(t)

	run.checkRefusals(t)
	run.checkAllowed(t)
	run.checkAudit(t)
	run.checkCustomRole(t)
}

// call sends one TFE API request as person and returns the status and the body.
func (r *forceUnlockRun) call(t *testing.T, person *refusalPerson, method, path, body string) (int, string) {
	t.Helper()

	status, respBody, err := r.env.TFEStatus(t.Context(), person.token.Secret, method, path, body)
	if err != nil {
		t.Fatalf("%s %s as %s: %v", method, path, person.name, err)
	}

	return status, respBody
}

// workspaceID reads the ID of the workspace web of the namespace.
func (r *forceUnlockRun) workspaceID(t *testing.T) string {
	t.Helper()

	status, body := r.call(
		t, r.maintainer, http.MethodGet, "/api/v2/organizations/"+refusalNamespace+"/workspaces/web", "",
	)
	if status != http.StatusOK {
		t.Fatalf("read the workspace answers %d: %s", status, body)
	}

	var doc lockDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Data.ID == "" {
		t.Fatalf("workspace document %q has no ID: %v", body, err)
	}

	return doc.Data.ID
}

// lock locks the workspace as person through the API and returns the lock that
// the workspace document shows.
func (r *forceUnlockRun) lock(t *testing.T, person *refusalPerson) lockView {
	t.Helper()

	status, body := r.call(
		t, person, http.MethodPost, "/api/v2/workspaces/"+r.id+"/actions/lock", `{"data":{}}`,
	)
	if status != http.StatusOK {
		t.Fatalf("lock as %s answers %d: %s", person.name, status, body)
	}

	return r.current(t)
}

// current reads the lock that the workspace document shows.
func (r *forceUnlockRun) current(t *testing.T) lockView {
	t.Helper()

	status, body := r.call(t, r.admin, http.MethodGet, "/api/v2/workspaces/"+r.id, "")
	if status != http.StatusOK {
		t.Fatalf("read the workspace answers %d: %s", status, body)
	}

	var doc lockDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the workspace document %q: %v", body, err)
	}

	return lockView{id: doc.Data.Attributes.LockID, holder: doc.Data.Attributes.LockHolder}
}

// errorCode reads the machine code of the first error of an error document.
func errorCode(t *testing.T, body string) string {
	t.Helper()

	var doc errorDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil || len(doc.Errors) == 0 {
		t.Fatalf("error document %q has no error: %v", body, err)
	}

	return doc.Errors[0].Code
}

// forceUnlock sends a force-unlock request as person.
func (r *forceUnlockRun) forceUnlock(t *testing.T, person *refusalPerson, body string) (int, string) {
	t.Helper()

	return r.call(t, person, http.MethodPost, forceUnlockPath(r.id), body)
}

// checkRefusals proves that a maintainer is refused, on the CLI and on the
// API, with a message that names the action; that a lock that differs from the
// lock of the body answers 409 with its code; and that a bad reason is refused.
// No refusal releases the lock.
func (r *forceUnlockRun) checkRefusals(t *testing.T) {
	t.Helper()

	held := r.lock(t, r.maintainer)
	if held.id == "" || held.holder == "" {
		t.Fatalf("workspace document shows lock ID %q and holder %q, want both", held.id, held.holder)
	}

	res := runRefusalCLI(t, r.env, r.cli, r.maintainer, "force-unlock", "-force", refusalNamespace+"/web")
	if res.ExitCode == 0 {
		t.Fatalf("tofu force-unlock as maintainer succeeds, want a refusal:\n%s", res.Stdout)
	}

	if out := res.Stdout + res.Stderr; !strings.Contains(out, "unit:write-forced") {
		t.Fatalf("tofu force-unlock as maintainer prints %q, want it to name unit:write-forced", out)
	}

	status, body := r.forceUnlock(t, r.maintainer, "")
	if status != http.StatusForbidden || !strings.Contains(body, "unit:write-forced") {
		t.Fatalf("API force-unlock as maintainer answers %d %q, want 403 that names unit:write-forced", status, body)
	}

	r.checkMismatch(t, `{"data":{"attributes":{"lock-id":"lock-other","lock-holder":"`+held.holder+`"}}}`)
	r.checkMismatch(t, `{"data":{"attributes":{"lock-id":"`+held.id+`","lock-holder":"someone-else"}}}`)

	tooLong := `{"data":{"attributes":{"reason":"` + strings.Repeat("a", 501) + `"}}}`

	if status, body = r.forceUnlock(t, r.admin, tooLong); status != http.StatusUnprocessableEntity {
		t.Fatalf("force-unlock with a reason of 501 characters answers %d %q, want 422", status, body)
	}

	if still := r.current(t); still != held {
		t.Fatalf("lock after the refusals is %+v, want %+v", still, held)
	}
}

// checkMismatch proves that an admin who names a lock that differs from the
// current lock gets 409 with the code workspace.lock-mismatch.
func (r *forceUnlockRun) checkMismatch(t *testing.T, body string) {
	t.Helper()

	status, respBody := r.forceUnlock(t, r.admin, body)
	if status != http.StatusConflict || errorCode(t, respBody) != "workspace.lock-mismatch" {
		t.Fatalf("force-unlock with %s answers %d %q, want 409 workspace.lock-mismatch", body, status, respBody)
	}
}

// checkAllowed proves that an admin releases the lock with the CLI, which sends
// no body, and with the API, with a reason and the lock that the caller saw; and
// that a repeat by the releaser succeeds with no change, and that a force-unlock
// of an unlocked workspace that names another lock answers 409 with its code.
func (r *forceUnlockRun) checkAllowed(t *testing.T) {
	t.Helper()

	res := runRefusalCLI(t, r.env, r.cli, r.admin, "force-unlock", "-force", refusalNamespace+"/web")
	if res.ExitCode != 0 {
		t.Fatalf("tofu force-unlock as admin exits %d:\n%s", res.ExitCode, res.Stderr)
	}

	if still := r.current(t); still != (lockView{id: "", holder: ""}) {
		t.Fatalf("lock after the CLI force-unlock is %+v, want none", still)
	}

	held := r.lock(t, r.maintainer)
	body := `{"data":{"attributes":{"reason":"` + forceUnlockReason + `","lock-id":"` + held.id +
		`","lock-holder":"` + held.holder + `"}}}`

	status, respBody := r.forceUnlock(t, r.admin, body)
	if status != http.StatusOK {
		t.Fatalf("API force-unlock as admin answers %d: %s", status, respBody)
	}

	if still := r.current(t); still.id != "" {
		t.Fatalf("lock after the API force-unlock is %+v, want none", still)
	}

	// The admin repeats the force-unlock, as OpenTofu does when it loses the
	// answer: the repeat succeeds and changes nothing.
	status, respBody = r.forceUnlock(t, r.admin, "")
	if status != http.StatusOK {
		t.Fatalf("repeat of the force-unlock by the releaser answers %d %q, want 200", status, respBody)
	}

	if still := r.current(t); still.id != "" {
		t.Fatalf("lock after the repeated force-unlock is %+v, want none", still)
	}

	// The unit view of the tool-neutral API names the record that made the
	// repeat succeed. The TFE workspace document keeps its shape and shows none.
	r.checkLastRelease(t, held.id, held.holder)

	// A force-unlock that names another lock is not a repeat: it answers 409.
	other := `{"data":{"attributes":{"lock-id":"lock-other","lock-holder":"` + held.holder + `"}}}`

	status, respBody = r.forceUnlock(t, r.admin, other)
	if status != http.StatusConflict || errorCode(t, respBody) != "workspace.not-locked" {
		t.Fatalf("force-unlock that names another lock answers %d %q, want 409 workspace.not-locked", status, respBody)
	}
}

// lastReleaseBody is the lastRelease object of one unit of the unit list.
type lastReleaseBody struct {
	LockID     string `json:"id"`
	Holder     string `json:"holder"`
	ReleasedBy string `json:"releasedBy"`
	ReleasedAt string `json:"releasedAt"`
	Kind       string `json:"kind"`
}

// lastReleaseUnit is the part of one unit of the unit list that the last
// release check reads.
type lastReleaseUnit struct {
	LastRelease *lastReleaseBody `json:"lastRelease"`
	Tool        string           `json:"tool"`
	Name        string           `json:"name"`
}

// workspaceLastRelease reads the unit list of the namespace and returns the
// last release of the OpenTofu workspace web, or nil when the unit shows none.
func (r *forceUnlockRun) workspaceLastRelease(t *testing.T) *lastReleaseBody {
	t.Helper()

	status, body, err := r.env.APIStatus(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/"+refusalNamespace+"/units", "",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("list units answers %d %q: %v", status, body, err)
	}

	var list struct {
		Units []lastReleaseUnit `json:"units"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode the unit list %q: %v", body, err)
	}

	for _, u := range list.Units {
		if u.Tool == "OpenTofu" && u.Name == "web" {
			return u.LastRelease
		}
	}

	t.Fatalf("the unit list has no workspace web: %s", body)

	return nil
}

// checkLastRelease proves that the unit list of the tool-neutral API shows the
// last released lock of the workspace web: the lock ID, the holder, a releaser,
// the time and the forced kind. It also proves that the TFE workspace document
// shows no such record.
func (r *forceUnlockRun) checkLastRelease(t *testing.T, lockID, holder string) {
	t.Helper()

	got := r.workspaceLastRelease(t)
	if got == nil {
		t.Fatal("the unit list shows no last release for the workspace web")
	}

	if got.LockID != lockID || got.Holder != holder || got.Kind != "forced" ||
		got.ReleasedBy == "" || got.ReleasedAt == "" {
		t.Fatalf("last release = %+v, want lock %q, holder %q, kind forced, a releaser and a time", got, lockID, holder)
	}

	_, doc := r.call(t, r.admin, http.MethodGet, "/api/v2/workspaces/"+r.id, "")
	if strings.Contains(strings.ToLower(doc), "release") {
		t.Fatalf("the TFE workspace document shows the last release: %s", doc)
	}
}

// checkAudit proves that the namespace log holds one change entry for each
// release: one with no reason from the CLI, and one with the reason of the API,
// both by the admin.
func (r *forceUnlockRun) checkAudit(t *testing.T) {
	t.Helper()

	entries, err := fetchAuditEntries(t.Context(), r.env, refusalNamespace)
	if err != nil {
		t.Fatalf("list %s audit entries: %v", refusalNamespace, err)
	}

	subject := e2e.SignInSubject(r.admin.name + "@example.com")
	reasons := map[string]int{}

	for i := range entries {
		e := &entries[i]
		if e.Kind != "change" || e.Action != "unit:write-forced" || e.Object != "opentofu:"+refusalNamespace+"/web" {
			continue
		}

		if e.Subject != subject || e.Outcome != "success" || e.ObjectID == "" {
			t.Fatalf("force-unlock entry %+v, want a success by %s with the ID of the unit", e, subject)
		}

		reasons[e.Reason]++
	}

	if len(reasons) != 2 || reasons[""] != 1 || reasons[forceUnlockReason] != 1 {
		t.Fatalf(
			"force-unlock entries by reason = %v, want one with no reason and one with %q",
			reasons,
			forceUnlockReason,
		)
	}
}

// unlockerRole is the custom role of the scenario. It holds the read action
// that the route needs to show the workspace to its caller and the action of
// the force-unlock, and no other write action.
const unlockerRole = "lock-breaker"

// checkCustomRole proves that a custom role that holds unit:write-forced is
// enough: a person with that role releases a lock that the maintainer holds,
// and the log names that person.
func (r *forceUnlockRun) checkCustomRole(t *testing.T) {
	t.Helper()

	ctx := t.Context()

	var role e2eRole

	principalCall(ctx, t, r.env, principalStep{
		http.MethodPost, "/api/server/roles",
		`{"name":"` + unlockerRole + `","actions":["unit:read","unit:write-forced"]}`, http.StatusCreated,
	}, &role)

	// A grant names a custom role by its ID.
	dee := issueRefusalPerson(ctx, t, r.env, "dee", role.ID)

	held := r.lock(t, r.maintainer)
	if held.id == "" {
		t.Fatal("workspace document shows no lock ID after the maintainer locks")
	}

	if status, body := r.forceUnlock(t, dee, ""); status != http.StatusOK {
		t.Fatalf("API force-unlock as %s with the custom role answers %d: %s", dee.name, status, body)
	}

	if still := r.current(t); still.id != "" {
		t.Fatalf("lock after the force-unlock by the custom role is %+v, want none", still)
	}

	entries, err := fetchAuditEntries(ctx, r.env, refusalNamespace)
	if err != nil {
		t.Fatalf("list %s audit entries: %v", refusalNamespace, err)
	}

	subject := e2e.SignInSubject(dee.name + "@example.com")
	found := 0

	for i := range entries {
		e := &entries[i]
		if e.Action == "unit:write-forced" && e.Subject == subject {
			found++

			if e.Kind != "change" || e.Outcome != "success" || e.Reason != "" {
				t.Fatalf("force-unlock entry %+v of the custom role, want a successful change with no reason", e)
			}
		}
	}

	if found != 1 {
		t.Fatalf("force-unlock entries by %s = %d, want 1", subject, found)
	}
}
