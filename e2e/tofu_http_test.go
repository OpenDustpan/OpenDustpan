//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// tofuHTTPConfig is the OpenTofu configuration of the http backend test: one
// address for the workspace, which serves GET, POST, LOCK and UNLOCK (ADR 49,
// decision 2). The password comes from TF_HTTP_PASSWORD, never from this file
// (ADR 49, decision 4).
const tofuHTTPConfig = `terraform {
  backend "http" {
    address        = %q
    lock_address   = %q
    unlock_address = %q
  }
}

output "greeting" {
  value = "hello"
}
`

// TestTofuHTTPBackend proves the OpenTofu http backend through the real CLI
// (ADR 49, decision 10): init configures the backend, apply writes state
// through the lock, state pull reads it back, a lock held by another client
// makes apply fail with a lock error, and the release lets apply succeed. It
// runs on every store for every OpenTofu version in the matrix.
func TestTofuHTTPBackend(t *testing.T) {
	t.Parallel()

	tool := e2e.OpenTofu()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux; elsewhere it uses the system store.
		t.Skipf("the OpenTofu suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, err := fetcher.Install(installCtx, tool, label)
			if err != nil {
				t.Fatalf("install %s %s: %v", tool.Name, label, err)
			}

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					runTofuHTTPOnStore(t, cli, db)
				})
			}
		})
	}
}

// runTofuHTTPOnStore runs the http backend scenario on one store.
func runTofuHTTPOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	// The gate steps write gates as a signed-in administrator.
	env := e2e.NewEnvWithSignIn(t.Context(), t, cli, store)
	ctx := t.Context()

	address := env.URL + "/tofu/state/" + e2e.Namespace + "/" + e2e.Workspace
	writeTofuHTTPConfig(t, env.ProjectDir(), address)

	backendEnv := []string{"TF_HTTP_USERNAME=dustpan", "TF_HTTP_PASSWORD=" + env.TokenA()}

	env.Step(ctx, t, e2e.StepSpec{
		Check: checkTofuHTTPInit(), Name: "init", Args: []string{"init", "-input=false", "-no-color"},
		Env: backendEnv, Stdin: "", Fail: false, Redact: false,
	})

	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkTofuHTTPApply(),
		Name:   "apply",
		Args:   []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:    backendEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	env.Step(ctx, t, e2e.StepSpec{
		Check: contains(`"greeting"`), Name: "state pull", Args: []string{"state", "pull"},
		Env: backendEnv, Stdin: "", Fail: false, Redact: false,
	})

	lock := tofuHTTPLockInfo()

	status, body, err := env.TFEStatusA(ctx, "LOCK", "/tofu/state/"+e2e.Namespace+"/"+e2e.Workspace, lock)
	if err != nil || status != http.StatusOK {
		t.Fatalf("LOCK for the conflict case: status %d, body %q, err %v", status, body, err)
	}

	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkTofuHTTPLockConflict(),
		Name:   "apply while locked",
		Args:   []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:    backendEnv,
		Stdin:  "",
		Fail:   true,
		Redact: false,
	})

	status, body, err = env.TFEStatusA(ctx, "UNLOCK", "/tofu/state/"+e2e.Namespace+"/"+e2e.Workspace, lock)
	if err != nil || status != http.StatusOK {
		t.Fatalf("UNLOCK after the conflict case: status %d, body %q, err %v", status, body, err)
	}

	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkTofuHTTPApplied(),
		Name:   "apply after unlock",
		Args:   []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:    backendEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	runTofuHTTPManualLock(t, env, backendEnv)

	runTofuHTTPMigration(t, env, address, backendEnv)

	runTofuHTTPForceUnlock(t, env, backendEnv)

	runTofuHTTPStateWriteGate(t, env, backendEnv)

	tofuNotHomeScenario(t, env, backendEnv)

	runTofuHTTPRefusedCreatesNothing(t, env, backendEnv)
}

// runTofuHTTPRefusedCreatesNothing proves that a POST or a LOCK that the server
// refuses leaves no workspace: the unit list of the namespace does not name
// the workspace that the requests used. The refusals are a POST with no lock, a
// POST that names a lock nobody holds, and a LOCK under a manual lock of the
// namespace. The LOCK under the manual lock answers the lock info of that lock,
// and apply on the new workspace prints it.
func runTofuHTTPRefusedCreatesNothing(t *testing.T, env *e2e.Env, backendEnv []string) {
	t.Helper()

	ctx := t.Context()
	path := "/tofu/state/" + e2e.Namespace + "/refused-first"
	state := `{"version":4,"serial":1,"lineage":"Q","terraform_version":"1.12.7","outputs":{},"resources":[]}`

	for _, step := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, path, state, http.StatusForbidden},
		{http.MethodPost, path + "?ID=x", state, http.StatusConflict},
	} {
		status, body, err := env.TFEStatusA(ctx, step.method, step.path, step.body)
		if err != nil || status != step.want {
			t.Fatalf("%s %s: status %d, body %q, err %v, want %d", step.method, step.path, status, body, err, step.want)
		}
	}

	nsLock := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/lock"
	bootstrapCall(t, env, http.MethodPut, nsLock, `{"reason":"freeze"}`)

	status, body, err := env.TFEStatusA(ctx, "LOCK", path, tofuHTTPLockInfo())
	if err != nil || status != http.StatusLocked {
		t.Fatalf("LOCK under the manual lock: status %d, body %q, err %v, want 423", status, body, err)
	}

	// The backend reads the body of a 423 as lock info: string fields with
	// PascalCase keys.
	var info map[string]string
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("LOCK under the manual lock: body %q is not lock info: %v", body, err)
	}

	if !strings.HasPrefix(info["ID"], "A manual lock blocks this run.") {
		t.Fatalf("LOCK under the manual lock: lock ID %q, want the lock info of the manual lock", info["ID"])
	}

	runTofuHTTPNewWorkspaceUnderNamespaceLock(t, env, backendEnv, env.URL+path)

	bootstrapCall(t, env, http.MethodDelete, nsLock, "")

	units := bootstrapCall(
		t,
		env,
		http.MethodGet,
		"/api/dustpan/v1/namespaces/"+e2e.Namespace+"/units?pageSize=500",
		"",
	)
	if strings.Contains(units, "refused-first") {
		t.Fatalf("the unit list names the workspace of the refused requests: %s", units)
	}
}

// runTofuHTTPNewWorkspaceUnderNamespaceLock proves with the real CLI that
// apply on a workspace that does not exist yet stops on the manual lock of the
// namespace and prints its lock info: the plain words, the route that removes
// the lock, and the reason. The caller holds the namespace lock.
func runTofuHTTPNewWorkspaceUnderNamespaceLock(t *testing.T, env *e2e.Env, backendEnv []string, address string) {
	t.Helper()

	ctx := t.Context()
	dir := t.TempDir()
	writeTofuHTTPConfig(t, dir, address)

	env.StepIn(ctx, t, dir, e2e.StepSpec{
		Check: nil, Name: "init http backend for a new workspace under the namespace lock",
		Args: []string{"init", "-input=false", "-no-color"},
		Env:  backendEnv, Stdin: "", Fail: false, Redact: false,
	})

	env.StepIn(ctx, t, dir, e2e.StepSpec{
		Check: func(r e2e.Result) error {
			out := r.Stdout + r.Stderr
			for _, want := range []string{
				"Error acquiring the state lock",
				"A manual lock blocks this run.",
				"`DELETE /api/dustpan/v1/namespaces/" + e2e.Namespace + "/lock`",
				"freeze",
			} {
				if !strings.Contains(out, want) {
					return fmt.Errorf("%w: apply output lacks %q: %s", errUnexpected, want, out)
				}
			}

			return nil
		},
		Name: "apply on a new workspace under the namespace lock",
		Args: []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:  backendEnv, Stdin: "", Fail: true, Redact: false,
	})
}

// bootstrapCall sends one installation API request with the bootstrap token and
// fails the test unless it answers a 2xx status. It
// returns the body.
func bootstrapCall(t *testing.T, env *e2e.Env, method, path, body string) string {
	t.Helper()

	status, got, err := env.ServerAPIStatus(t.Context(), method, path, body)
	if err != nil || status/100 != 2 {
		t.Fatalf("%s %s: status %d, body %q, err %v", method, path, status, got, err)
	}

	return got
}

// runTofuHTTPManualLock proves a manual lock on the workspace stops the http
// backend: the LOCK request answers 423, and the CLI prints the lock info. The
// info says that a manual lock blocks the run and names the API route that
// removes it. After the holder removes the lock, apply works again.
func runTofuHTTPManualLock(t *testing.T, env *e2e.Env, backendEnv []string) {
	t.Helper()

	ctx := t.Context()
	path := unitLockPath(tofuUnitRef())

	// The push document for the unlocked write below, taken before the lock.
	wsID := env.WorkspaceID(ctx, t)
	pulled := env.Step(ctx, t, e2e.StepSpec{
		Check: nil, Name: "http backend: state pull for the unlocked push",
		Args: []string{"state", "pull"}, Env: backendEnv, Stdin: "", Fail: false, Redact: false,
	}).Stdout
	lineage, serial := overrideCurrent(t, env, "http backend: current state for the unlocked push",
		"/api/v2/workspaces/"+wsID+"/current-state-version")
	push := overridePushDocument(t, pulled, lineage, serial+1)

	lockCall(t, env, actorA, collectionCall{
		name: "http backend: lock the workspace", method: http.MethodPut, path: path,
		body: lockBody(lockReasonText), want: http.StatusOK, check: nil,
	})

	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkTofuHTTPManualLock(env),
		Name:   "apply while a manual lock holds the workspace",
		Args:   []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:    backendEnv,
		Stdin:  "",
		Fail:   true,
		Redact: false,
	})

	runTofuHTTPUnlockedPushUnderManualLock(t, env, backendEnv, push)

	lockCall(t, env, actorA, collectionCall{
		name: "http backend: remove the manual lock", method: http.MethodDelete, path: path,
		body: "", want: http.StatusNoContent, check: nil,
	})

	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkTofuHTTPApplied(),
		Name:   "apply after the manual lock goes",
		Args:   []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:    backendEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
}

// checkTofuHTTPManualLock proves the CLI stopped on the manual lock and printed
// the lock info: the plain words that a manual lock blocks the run, the route
// that removes it, the holder and the reason. The server answered the LOCK
// request with 423.
func checkTofuHTTPManualLock(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		out := r.Stdout + r.Stderr

		for _, want := range []string{
			"Error acquiring the state lock",
			"A manual lock blocks this run.",
			"`DELETE /api/inventory/unit-lock?unit=" + tofuUnitRef() + "`",
			e2e.Subject,
			lockReasonText,
		} {
			if !strings.Contains(out, want) {
				return fmt.Errorf("%w: apply output lacks %q: %s", errUnexpected, want, out)
			}
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			if entries[i].Method == "LOCK" && entries[i].Status == http.StatusLocked {
				return nil
			}
		}

		return fmt.Errorf("%w: no LOCK request answered %d", errUnexpected, http.StatusLocked)
	}
}

// runTofuHTTPMigration moves the state of the workspace from the http backend
// to the cloud block with the CLI migration, and then reads it back
// (ADR 49, decision 3). A cloud block takes the copy of the state from an
// interactive prompt of init, so the scenario answers yes on stdin. The workspace is one unit for both backends, so
// each
// direction reads the state the other wrote. The scenario ends on the http
// backend, which the force-unlock scenario needs.
func runTofuHTTPMigration(t *testing.T, env *e2e.Env, address string, backendEnv []string) {
	t.Helper()

	ctx := t.Context()

	env.WriteTofuConfig(t, tofuAuditSecretConfig(env.Host()))

	env.Step(ctx, t, e2e.StepSpec{
		Check:  contains("Cloud backend has been successfully initialized!"),
		Name:   "migrate from http to cloud",
		Args:   []string{"init", "-no-color"},
		Env:    append(slices.Clone(backendEnv), "TF_INPUT=1"),
		Stdin:  "yes\n",
		Fail:   false,
		Redact: false,
	})

	cloudSide := env.Step(ctx, t, e2e.StepSpec{
		Check:  contains(`"greeting"`),
		Name:   "state pull through cloud",
		Args:   []string{"state", "pull"},
		Env:    nil,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	writeTofuHTTPConfig(t, env.ProjectDir(), address)

	// The http backend has no workspaces, so the cloud workspace name must not
	// stay selected in .terraform; the default workspace is the only one.
	envFile := filepath.Join(env.ProjectDir(), ".terraform", "environment")
	if err := os.Remove(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("clear the selected workspace: %v", err)
	}

	// OpenTofu does not migrate state away from a cloud block ("not yet
	// implemented"), so the way back is a reconfigure. The unit is one
	// workspace, so its state stays where the cloud block wrote it.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  contains(`Successfully configured the backend "http"`),
		Name:   "reconfigure cloud to http",
		Args:   []string{"init", "-reconfigure", "-no-color"},
		Env:    backendEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	httpSide := env.Step(ctx, t, e2e.StepSpec{
		Check:  contains(`"greeting"`),
		Name:   "state pull through http",
		Args:   []string{"state", "pull"},
		Env:    backendEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	assertSameStateHead(t, cloudSide.Stdout, httpSide.Stdout)
}

// tofuStateHead is the part of a state document that identifies its version.
type tofuStateHead struct {
	Serial  int64  `json:"serial"`
	Lineage string `json:"lineage"`
}

// assertSameStateHead proves the state the http backend reads after the
// reconfigure has the serial and lineage that the cloud block wrote last.
func assertSameStateHead(t *testing.T, cloudJSON, httpJSON string) {
	t.Helper()

	var want, got tofuStateHead

	if err := json.Unmarshal([]byte(cloudJSON), &want); err != nil {
		t.Fatalf("decode the state pulled through cloud: %v", err)
	}

	if err := json.Unmarshal([]byte(httpJSON), &got); err != nil {
		t.Fatalf("decode the state pulled through http: %v", err)
	}

	if got != want {
		t.Fatalf("state after the reconfigure = serial %d lineage %q, want serial %d lineage %q",
			got.Serial, got.Lineage, want.Serial, want.Lineage)
	}
}

// tofuHTTPForceLock is the lock that principal B holds in the force-unlock
// scenario, so principal A is not its holder.
const tofuHTTPForceLock = `{"ID":"e2e-lock-2","Operation":"OperationTypeApply","Info":"","Who":"e2e-b@host","Version":"1.13.1","Created":"2026-10-13T00:00:00Z","Path":""}`

// runTofuHTTPForceUnlock proves a force-unlock by a principal that does not hold
// the lock (ADR 49, decision 6, and ADR 44). Principal B takes the lock. Principal
// A force-unlocks it with the CLI, which needs both switches on. A write that
// names the released lock then answers 409, so a stale writer cannot overwrite
// the state. The switches are off again when the scenario ends.
func runTofuHTTPForceUnlock(t *testing.T, env *e2e.Env, backendEnv []string) {
	t.Helper()

	ctx := t.Context()
	path := "/tofu/state/" + e2e.Namespace + "/" + e2e.Workspace
	wsID := env.WorkspaceID(ctx, t)
	nsSwitches := "/api/server/namespaces/" + e2e.Namespace + "/override-switches"
	unitSwitches := "/api/v2/workspaces/" + wsID + "/override-switches"
	on := `{"allowForcedWrites":true,"allowUnlockedWrites":false}`
	off := `{"allowForcedWrites":false,"allowUnlockedWrites":false}`

	// The unit route names its switches in kebab case; the namespace route in
	// camel case, as the override tests do.
	unitOn := `{"allow-forced-writes":true,"allow-unlocked-writes":false}`
	unitOff := `{"allow-forced-writes":false,"allow-unlocked-writes":false}`

	setSwitches(t, env, nsSwitches, on)
	setSwitches(t, env, unitSwitches, unitOn)

	defer func() {
		setSwitches(t, env, nsSwitches, off)
		setSwitches(t, env, unitSwitches, unitOff)
	}()

	status, body, err := env.TFEStatusB(ctx, "LOCK", path, tofuHTTPForceLock)
	if err != nil || status != http.StatusOK {
		t.Fatalf("LOCK by principal B: status %d, body %q, err %v", status, body, err)
	}

	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "force-unlock by another principal",
		Args:   []string{"force-unlock", "-force", "e2e-lock-2"},
		Env:    backendEnv,
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	stale := `{"version":4,"serial":99,"lineage":"e2e-stale","resources":[]}`

	status, body, err = env.TFEStatusB(ctx, "POST", path+"?ID=e2e-lock-2", stale)
	if err != nil || status != http.StatusConflict {
		t.Fatalf("stale write after the force-unlock: status %d, want 409; body %q, err %v", status, body, err)
	}

	checkTofuHTTPForceUnlockAudit(t, env)
}

// checkTofuHTTPForceUnlockAudit proves that the force-unlock of the http
// backend wrote one change entry with the action unit:write-forced and no
// reason: the lock info of the http backend has no field for a reason.
func checkTofuHTTPForceUnlockAudit(t *testing.T, env *e2e.Env) {
	t.Helper()

	entries, err := fetchAuditEntries(t.Context(), env, e2e.Namespace)
	if err != nil {
		t.Fatalf("list %s audit entries: %v", e2e.Namespace, err)
	}

	object := "opentofu:" + e2e.Namespace + "/" + e2e.Workspace

	var forced []auditListItem

	for i := range entries {
		if entries[i].Action == "unit:write-forced" && entries[i].Object == object {
			forced = append(forced, entries[i])
		}
	}

	if len(forced) != 1 {
		t.Fatalf("entries of unit:write-forced on %s = %+v, want exactly one", object, forced)
	}

	if e := forced[0]; e.Kind != "change" || e.Outcome != "success" || e.Reason != "" || e.ObjectID == "" {
		t.Fatalf("force-unlock entry %+v, want a successful change with the ID of the unit and no reason", e)
	}
}

// setSwitches sets the override switches at path to body, as principal A.
func setSwitches(t *testing.T, env *e2e.Env, path, body string) {
	t.Helper()

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: overrideAgent,
		Name:      "set override switches",
		Method:    http.MethodPut,
		Path:      path,
		Body:      body,
		Want:      http.StatusOK,
		Check:     nil,
		Scheme:    "",
	})
}

// writeTofuHTTPConfig replaces the main configuration of the project with the
// http backend configuration at address.
func writeTofuHTTPConfig(t *testing.T, project, address string) {
	t.Helper()

	content := fmt.Sprintf(tofuHTTPConfig, address, address, address)

	if err := os.WriteFile(filepath.Join(project, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatalf("write the http backend configuration: %v", err)
	}
}

// tofuHTTPLockInfo returns the lock info that another client holds the lock
// with. The fields are the ones OpenTofu sends in a LOCK request.
func tofuHTTPLockInfo() string {
	return `{"ID":"e2e-lock-1","Operation":"OperationTypeApply","Info":"","Who":"e2e@host","Version":"1.13.1","Created":"2026-10-13T00:00:00Z","Path":""}`
}

// checkTofuHTTPInit proves init configured the http backend.
func checkTofuHTTPInit() func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, `Successfully configured the backend "http"`) {
			return fmt.Errorf("%w: init did not configure the http backend", errUnexpected)
		}

		return nil
	}
}

// checkTofuHTTPApply proves apply wrote the state and printed the summary.
func checkTofuHTTPApply() func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, "Apply complete!") {
			return fmt.Errorf("%w: apply did not complete", errUnexpected)
		}

		return nil
	}
}

// checkTofuHTTPLockConflict proves a lock held by another client stops apply
// with the lock error, before any state write.
func checkTofuHTTPLockConflict() func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stderr, "Error acquiring the state lock") {
			return fmt.Errorf("%w: apply did not report the state lock", errUnexpected)
		}

		return nil
	}
}

// checkTofuHTTPApplied proves apply succeeds once the lock is released.
func checkTofuHTTPApplied() func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, "Apply complete!") && !strings.Contains(r.Stdout, "No changes") {
			return fmt.Errorf("%w: apply did not succeed after the unlock", errUnexpected)
		}

		return nil
	}
}

// runTofuHTTPUnlockedPushUnderManualLock proves a state push without a lock ID
// meets the manual lock, also when both unlocked-write switches are on: the
// CLI writes to the http backend with no ?ID= and the lock refuses it. The
// switches go off again after the step, so later scenarios see the defaults.
// push is the state document to push, taken before the lock.
func runTofuHTTPUnlockedPushUnderManualLock(t *testing.T, env *e2e.Env, backendEnv []string, push string) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)
	unitSwitches := "/api/v2/workspaces/" + wsID + "/override-switches"
	nsSwitches := "/api/server/namespaces/" + e2e.Namespace + "/override-switches"
	const unlockedPushStep = "http backend: state push without a lock under the manual lock"

	on, off := `{"allow-forced-writes":false,"allow-unlocked-writes":true}`,
		`{"allow-forced-writes":false,"allow-unlocked-writes":false}`

	overrideSwitches(t, env, "http backend: turn on the namespace switches", nsSwitches,
		`{"allowForcedWrites":false,"allowUnlockedWrites":true}`)
	overrideSwitches(t, env, "http backend: turn on the unit switches", unitSwitches, on)

	env.Step(ctx, t, e2e.StepSpec{
		Check:  checkTofuHTTPUnlockedPushRefused(env, unlockedPushStep),
		Name:   unlockedPushStep,
		Args:   []string{"state", "push", "-lock=false", "-"},
		Env:    backendEnv,
		Stdin:  push,
		Fail:   true,
		Redact: false,
	})

	overrideSwitches(t, env, "http backend: turn off the unit switches", unitSwitches, off)
	overrideSwitches(t, env, "http backend: turn off the namespace switches", nsSwitches,
		`{"allowForcedWrites":false,"allowUnlockedWrites":false}`)
}

// checkTofuHTTPUnlockedPushRefused checks a state push without a lock ID that a
// manual lock stopped. The CLI prints the status only, so the answer of the
// server is the proof: the POST answered 423 with lock info that names the
// holder and the reason. It reads only the requests of the step stepName.
func checkTofuHTTPUnlockedPushRefused(env *e2e.Env, stepName string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if out := r.Stdout + r.Stderr; !strings.Contains(out, "423") {
			return fmt.Errorf("%w: push output lacks the status 423: %s", errUnexpected, out)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			e := &entries[i]
			if e.Step != stepName || e.Method != http.MethodPost || e.Status != http.StatusLocked {
				continue
			}

			for _, want := range []string{e2e.Subject, lockReasonText} {
				if !strings.Contains(e.ResponseBody, want) {
					return fmt.Errorf("%w: 423 body lacks %q: %s", errUnexpected, want, e.ResponseBody)
				}
			}

			return nil
		}

		return fmt.Errorf("%w: no POST answered %d", errUnexpected, http.StatusLocked)
	}
}
