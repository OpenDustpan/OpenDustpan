//go:build e2e && upgrade

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// tagTimeout bounds the git lookup of the previous release tag.
	tagTimeout = 30 * time.Second
	// upgradeSecret is the Pulumi config secret the test writes before the
	// restart and reads back after it.
	upgradeSecret = "s3cr3t"
)

// TestUpgrade restarts the current binary on the same database and checks
// that the data, the secrets and the held lock survive. It runs the
// empty-database case of ADR 21: the binary starts on a new database, the
// real Pulumi CLI and OpenTofu write state, an OpenTofu lock is held, the
// server stops, and the current binary starts again on the same database.
// Run it with `make upgrade-test`; it is not part of `make check`.
func TestUpgrade(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux.
		t.Skipf("the upgrade test needs Linux, not %s", runtime.GOOS)
	}

	if !e2e.UseBinaryServer() {
		t.Fatal("the upgrade test needs E2E_SERVER=binary (run make upgrade-test)")
	}

	t.Logf("upgrade case: empty database (newest v* tag ancestor of HEAD: %s)", previousTag(t))

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersions := e2e.Pulumi().Versions()
	tofuVersions := e2e.OpenTofu().Versions()

	if len(pulumiVersions) == 0 || len(tofuVersions) == 0 {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersions[0])
	if err != nil {
		t.Fatalf("install pulumi %s: %v", pulumiVersions[0], err)
	}

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersions[0])
	if err != nil {
		t.Fatalf("install tofu %s: %v", tofuVersions[0], err)
	}

	t.Logf("pulumi %s with tofu %s", pulumiCLI.Version, tofuCLI.Version)

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			upgradeOnStore(t, pulumiCLI, tofuCLI, db)
		})
	}
}

// previousTag returns the newest v* tag that is an ancestor of HEAD, or
// "none" when the repository has no such tag.
func previousTag(t *testing.T) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), tagTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "describe", "--tags", "--abbrev=0", "--match", "v*")

	out, err := cmd.Output()
	if err != nil {
		return "none"
	}

	if tag := strings.TrimSpace(string(out)); tag != "" {
		return tag
	}

	return "none"
}

// upgradeLockedDoc decodes a workspace document in tests.
type upgradeLockedDoc struct {
	Data upgradeLockedData `json:"data"`
}

// upgradeLockedData holds the data object of a workspace document.
type upgradeLockedData struct {
	Attributes upgradeLockedAttributes `json:"attributes"`
}

// upgradeLockedAttributes holds the locked flag of a workspace document.
type upgradeLockedAttributes struct {
	Locked bool `json:"locked"`
}

// upgradeOnStore writes state with both CLIs on a new database, holds an
// OpenTofu lock, restarts the binary on the same database and checks that
// the data, the secrets and the held lock survive.
func upgradeOnStore(t *testing.T, pulumiCLI, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, pulumiCLI, store)
	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	env.Step(ctx, t, step("login", contains(env.URL), "login"))
	env.Step(ctx, t, step("stack init", nil, "stack", "init", qualified))
	// The scratch program reads stack other, so up needs it to exist.
	env.Step(ctx, t, step("stack init other", nil, "stack", "init", "other"))
	env.Step(ctx, t, step("config set secret", nil, "config", "set", "--secret", "apiKey", upgradeSecret))
	env.Step(ctx, t, step("up", nil, "up", "--yes", "--non-interactive"))
	env.Step(ctx, t, step("stack export", exportHoldsReference, "stack", "export"))
	env.Step(ctx, t, step("config get", contains(upgradeSecret), "config", "get", "apiKey"))

	env.SetCLI(tofuCLI)

	env.Step(ctx, t, step("init", nil, "init", "-input=false", "-no-color"))
	env.Step(ctx, t, step("apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.Step(ctx, t, step("state pull", contains(`"greeting"`), "state", "pull"))

	wsID := env.WorkspaceID(ctx, t)
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"

	status, body, err := env.TFEStatusA(ctx, http.MethodPost, lockPath, `{"data":{}}`)
	if err != nil || status != http.StatusOK {
		t.Fatalf("hold the lock before the restart: status %d, body %s, err %v", status, body, err)
	}

	t.Logf("restarting the current binary on the same %s database", store)
	env.StopServer(ctx, t)
	env.StartServer(ctx, t)

	env.SetCLI(pulumiCLI)

	env.Step(ctx, t, step("config get after restart", contains(upgradeSecret), "config", "get", "apiKey"))
	env.Step(ctx, t, step("stack export after restart", exportHidesSecrets, "stack", "export"))

	env.SetCLI(tofuCLI)

	env.Step(ctx, t, step("state pull after restart", contains(`"greeting"`), "state", "pull"))
	checkUpgradeLocked(ctx, t, env, wsID)

	status, body, err = env.TFEStatusB(ctx, http.MethodPost, lockPath, `{"data":{}}`)
	if err != nil || status != http.StatusConflict {
		t.Fatalf("a lock by another principal after the restart answers %d, want %d: %s (%v)",
			status, http.StatusConflict, body, err)
	}
}

// checkUpgradeLocked proves the workspace is still locked after the restart:
// the held lock survived.
func checkUpgradeLocked(ctx context.Context, t *testing.T, env *e2e.Env, wsID string) {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/workspaces/"+wsID, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read the workspace after the restart: status %d, body %s, err %v", status, body, err)
	}

	var doc upgradeLockedDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the workspace after the restart: %v (%s)", err, body)
	}

	if !doc.Data.Attributes.Locked {
		t.Fatal("the workspace is unlocked after the restart, want the held lock to survive")
	}
}
