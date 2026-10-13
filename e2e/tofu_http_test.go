//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

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

	env := e2e.NewEnv(t.Context(), t, cli, store)
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

	runTofuHTTPForceUnlock(t, env, backendEnv)
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
