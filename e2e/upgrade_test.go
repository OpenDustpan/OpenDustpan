//go:build e2e && upgrade

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// tagTimeout bounds the git lookup of the release tags.
	tagTimeout = 30 * time.Second
	// upgradeSecret is the Pulumi config secret the test writes before the
	// restart and reads back after it.
	upgradeSecret = "s3cr3t"
)

// upgradeRecentTags holds how many of the newest release tags the upgrade
// test covers, next to the first tag. Upgrades can skip any number of
// releases, so the test covers the oldest schema and the newest ones: the
// first v* tag plus the last upgradeRecentTags tags. With four or fewer tags
// it covers every tag.
const upgradeRecentTags = 3

// TestUpgrade checks that the data, the secrets and the held lock survive a
// restart on the same database. With no v* tag reachable from HEAD it runs
// the empty-database case: the current binary starts on a new database, the
// real Pulumi CLI and OpenTofu write state, an OpenTofu lock is held, the
// server stops, and the current binary starts again. With tags it runs one
// upgrade per supported tag, each straight to the current binary: the tag
// binary is built with `git archive` in a temporary directory, so the
// working tree and the index never change, it writes the same state, the
// current binary upgrades the database, and the old binary must then refuse
// the upgraded database when it has the schema check. The supported tags are
// the first tag plus the last few, as supportedUpgradeTags lists them. With
// no tag the test logs that it ran over 0 tags and runs the empty-database
// case, so it never skips silently.
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

	tags := supportedUpgradeTags(listReleaseTags(t))

	t.Logf("upgrade test ran over %d tags: %s", len(tags), strings.Join(tags, ", "))

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tools := installUpgradeTools(t, fetcher)

	if len(tags) == 0 {
		runEmptyUpgrades(t, databases, tools)

		return
	}

	runTagUpgrades(t, tags, databases, tools)
}

// installUpgradeTools installs one CLI of each tool and groups them.
func installUpgradeTools(t *testing.T, fetcher *e2e.Fetcher) upgradeTools {
	t.Helper()

	pulumiVersion := e2e.Pulumi().Single()
	tofuVersion := e2e.OpenTofu().Single()

	if pulumiVersion == "" || tofuVersion == "" {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install pulumi %s: %v", pulumiVersion, err)
	}

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install tofu %s: %v", tofuVersion, err)
	}

	t.Logf("pulumi %s with tofu %s", pulumiCLI.Version, tofuCLI.Version)

	return upgradeTools{pulumi: pulumiCLI, tofu: tofuCLI}
}

// runEmptyUpgrades writes state with the current binary and restarts it.
func runEmptyUpgrades(t *testing.T, databases []e2e.Store, tools upgradeTools) {
	t.Helper()

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			upgradeOnStore(t, tools.pulumi, tools.tofu, db)
		})
	}
}

// runTagUpgrades upgrades each supported tag straight to the current binary.
func runTagUpgrades(t *testing.T, tags []string, databases []e2e.Store, tools upgradeTools) {
	t.Helper()

	newBin := e2e.BuildCurrentBinary(t.Context(), t, t.TempDir())

	oldBins := make(map[string]string, len(tags))
	for _, tag := range tags {
		oldBins[tag] = e2e.BuildTagBinary(t.Context(), t, tag, t.TempDir())
		t.Logf("built tag %s at %s and the current binary at %s", tag, oldBins[tag], newBin)
	}

	for _, tag := range tags {
		for _, db := range databases {
			t.Run(tag+"/"+string(db), func(t *testing.T) {
				t.Parallel()
				upgradeFromTagOnStore(t, upgradeRun{
					tag:    tag,
					oldBin: oldBins[tag],
					newBin: newBin,
					tools:  tools,
					store:  db,
				})
			})
		}
	}
}

// upgradeTools holds the installed CLIs one upgrade run drives.
type upgradeTools struct {
	pulumi e2e.Install
	tofu   e2e.Install
}

// upgradeRun groups what one previous-tag upgrade on one store needs.
type upgradeRun struct {
	tag    string
	oldBin string
	newBin string
	tools  upgradeTools
	store  e2e.Store
}

// listReleaseTags returns every v* tag reachable from HEAD, ordered by
// version. It returns nothing when the repository has no such tag. When git
// finds none and the clone is shallow, it fails with an error that says to
// fetch tags, because the default CI checkout fetches no tags.
func listReleaseTags(t *testing.T) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), tagTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "tag", "--merged", "HEAD", "--list", "v*", "--sort=version:refname")
	cmd.Dir = e2e.GitTopLevel(ctx, t)

	out, err := cmd.Output()
	if err != nil {
		if isShallowRepository(t) {
			t.Fatalf("no v* tag found and the clone is shallow: fetch tags with " +
				"`git fetch --tags` (or unshallow the clone first)")
		}

		return []string{}
	}

	tags := []string{}

	for line := range strings.Lines(strings.TrimSpace(string(out))) {
		if tag := strings.TrimSpace(line); tag != "" {
			tags = append(tags, tag)
		}
	}

	if len(tags) == 0 && isShallowRepository(t) {
		t.Fatalf("no v* tag found and the clone is shallow: fetch tags with " +
			"`git fetch --tags` (or unshallow the clone first)")
	}

	return tags
}

// supportedUpgradeTags narrows all release tags to the supported set: the
// first tag plus the last upgradeRecentTags tags. With four or fewer tags it
// returns every tag in version order.
func supportedUpgradeTags(all []string) []string {
	if len(all) <= upgradeRecentTags+1 {
		return slices.Clone(all)
	}

	out := make([]string, 0, upgradeRecentTags+1)
	out = append(out, all[0])
	out = append(out, all[len(all)-upgradeRecentTags:]...)

	return out
}

// isShallowRepository reports whether the repository is a shallow clone.
func isShallowRepository(t *testing.T) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), tagTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-shallow-repository")

	out, err := cmd.Output()
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(out)) == "true"
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

	wsID := writeUpgradeState(ctx, t, env, tofuCLI)

	t.Logf("restarting the current binary on the same %s database", store)
	env.StopServer(ctx, t)
	env.StartServer(ctx, t)

	checkUpgradeSurvived(ctx, t, env, upgradeTools{pulumi: pulumiCLI, tofu: tofuCLI}, wsID)
}

// upgradeFromTagOnStore writes state with the tag binary on a new database,
// upgrades to the current binary on the same database, checks that the data,
// the secrets and the held lock survive, and checks that the old binary
// refuses the upgraded database when it has the schema check.
func upgradeFromTagOnStore(t *testing.T, in upgradeRun) {
	t.Helper()

	if in.store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithBinary(ctx, t, in.tools.pulumi, in.store, in.oldBin)

	wsID := writeUpgradeState(ctx, t, env, in.tools.tofu)

	t.Logf("upgrading tag %s to the current binary on the same %s database", in.tag, in.store)
	env.StopServer(ctx, t)
	env.SetServerBinary(in.newBin)
	env.StartServer(ctx, t)

	checkUpgradeSurvived(ctx, t, env, in.tools, wsID)

	env.StopServer(ctx, t)
	checkOldBinaryRefuses(ctx, t, env, in.tag, in.oldBin)
}

// writeUpgradeState writes Pulumi state with a secret and OpenTofu state
// with both CLIs, holds an OpenTofu lock, and returns the workspace ID.
func writeUpgradeState(ctx context.Context, t *testing.T, env *e2e.Env, tofuCLI e2e.Install) string {
	t.Helper()

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

	return wsID
}

// checkUpgradeSurvived proves the Pulumi secret and state and the OpenTofu
// state and held lock survived the restart onto the same database.
func checkUpgradeSurvived(ctx context.Context, t *testing.T, env *e2e.Env, tools upgradeTools, wsID string) {
	t.Helper()

	env.SetCLI(tools.pulumi)

	env.Step(ctx, t, step("config get after restart", contains(upgradeSecret), "config", "get", "apiKey"))
	env.Step(ctx, t, step("stack export after restart", exportHidesSecrets, "stack", "export"))

	env.SetCLI(tools.tofu)

	env.Step(ctx, t, step("state pull after restart", contains(`"greeting"`), "state", "pull"))
	checkUpgradeLocked(ctx, t, env, wsID)

	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"

	status, body, err := env.TFEStatusB(ctx, http.MethodPost, lockPath, `{"data":{}}`)
	if err != nil || status != http.StatusConflict {
		t.Fatalf("a lock by another principal after the restart answers %d, want %d: %s (%v)",
			status, http.StatusConflict, body, err)
	}
}

// checkOldBinaryRefuses starts the old binary on the upgraded database. A
// binary with a schema check refuses with a non-zero exit and a message that
// names the versions. A binary with the minimum binary version rule starts
// when its version is at or above the minimum of the upgraded schema, and a
// binary with the older newer-schema rule starts when the schema is
// unchanged: the test says so and skips only this assertion. A binary
// without any check also starts, and the test says that too.
func checkOldBinaryRefuses(ctx context.Context, t *testing.T, env *e2e.Env, tag, oldBin string) {
	t.Helper()

	res := env.TryServe(ctx, t, oldBin)
	output := res.Stdout + res.Stderr

	if res.ExitCode == 0 {
		if oldHasSchemaCheck(ctx, t, tag) {
			t.Logf("old binary at %s has a schema check and starts: "+
				"it is at or above the schema minimum, skipping the refusal assertion", tag)
		} else {
			t.Logf("old binary at %s does not have a schema check, "+
				"skipping the refusal assertion", tag)
		}

		return
	}

	if !e2e.HasSchemaRefusal(output) {
		t.Fatalf("old binary at %s exits with %d but names no schema versions: %s%s",
			tag, res.ExitCode, res.Stdout, res.Stderr)
	}

	t.Logf("old binary at %s refuses the upgraded database with exit %d", tag, res.ExitCode)
}

// oldHasSchemaCheck reports whether the source at tag holds a schema
// refusal: the minimum binary version rule or the older newer-schema rule.
// It reads with `git grep`, so the working tree and the index never change.
func oldHasSchemaCheck(ctx context.Context, t *testing.T, tag string) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, tagTimeout)
	defer cancel()

	//nolint:gosec // The tag comes from git describe of this repository.
	cmd := exec.CommandContext(ctx, "git", "grep", "-q", "-e", "ErrSchemaNewer", "-e", "ErrBinaryBelowMinimum",
		tag, "--", "store", "pkg/store")
	cmd.Dir = e2e.GitTopLevel(ctx, t)

	return cmd.Run() == nil
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
