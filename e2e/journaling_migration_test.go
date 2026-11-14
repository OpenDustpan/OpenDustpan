//go:build e2e && journalrecording

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestPulumiJournalStateMigration measures a state migration in three modes:
// checkpoints, journal version 1 and journal version 2. The program registers
// a component with a state migration callback. Version 1 must refuse the
// registration, while version 2 must give the same normalized stack export as
// checkpoints. If version 1 does not refuse, or the exports differ, the step
// fails.
func TestPulumiJournalStateMigration(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersions := e2e.Pulumi().Versions()
	if len(pulumiVersions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runPulumiJournalStateMigration(t, fetcher, pulumiVersions, db)
		})
	}
}

// migrationStack is the short stack name every mode of the migration step
// uses. Each mode runs on its own server, so the names never meet.
const migrationStack = "mig"

// migrationNeedsCLI is the oldest CLI that negotiates journal version 2. An
// older CLI never sends a state migration entry.
//
//nolint:gochecknoglobals // The step runs one CLI, so its minimum stays a shared value.
var migrationNeedsCLI = [3]int{3, 264, 0}

// runPulumiJournalStateMigration installs the newest CLI of the matrix that
// negotiates version 2 and runs the migration program in the three modes.
func runPulumiJournalStateMigration(
	t *testing.T, fetcher *e2e.Fetcher, pulumiLabels []string, store e2e.Store,
) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH, so the migration program cannot run")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	pulumiCLI, ok := installMigrationCLI(installCtx, t, fetcher, pulumiLabels)
	if !ok {
		t.Skipf(
			"no Pulumi CLI of v%d.%d.%d or later in the matrix",
			migrationNeedsCLI[0],
			migrationNeedsCLI[1],
			migrationNeedsCLI[2],
		)
	}

	t.Logf("pulumi %s on %s drives a state migration in three modes", pulumiCLI.Version, store)

	venvDir := t.TempDir()
	migrationPipInstall(t, venvDir, pulumiCLI.Version)

	checkpoints := runMigrationMode(t, migrationRun{
		cli:        pulumiCLI,
		store:      store,
		mode:       migrationCheckpoints,
		stack:      migrationStack,
		venvDir:    venvDir,
		versionCap: nil,
	})
	version1 := runMigrationMode(t, migrationRun{
		cli:        pulumiCLI,
		store:      store,
		mode:       migrationJournalV1,
		stack:      migrationStack,
		venvDir:    venvDir,
		versionCap: e2e.NewJournalVersionCap(1),
	})
	version2 := runMigrationMode(t, migrationRun{
		cli:        pulumiCLI,
		store:      store,
		mode:       migrationJournalV2,
		stack:      migrationStack,
		venvDir:    venvDir,
		versionCap: nil,
	})

	checkMigrationV1Refused(t, version1)
	checkMigrationExportsMatch(t, checkpoints.exported, version2.exported)
	checkMigrationJournaled(t, version2.env)
}

// installMigrationCLI installs every CLI of the matrix and returns the newest
// one that negotiates journal version 2, or false when none does.
func installMigrationCLI(
	ctx context.Context, t *testing.T, fetcher *e2e.Fetcher, labels []string,
) (e2e.Install, bool) {
	t.Helper()

	var best e2e.Install

	bestVersion := [3]int{}

	for _, label := range labels {
		installed, err := fetcher.Install(ctx, e2e.Pulumi(), label)
		if err != nil {
			t.Fatalf("install pulumi %s: %v", label, err)
		}

		version, ok := parseRelease(installed.Version)
		if !ok || slices.Compare(version[:], migrationNeedsCLI[:]) < 0 {
			continue
		}

		if slices.Compare(version[:], bestVersion[:]) > 0 {
			best, bestVersion = installed, version
		}
	}

	return best, bestVersion != [3]int{}
}

// parseRelease parses a plain "x.y.z" release into its parts.
func parseRelease(s string) ([3]int, bool) {
	var version [3]int

	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != len(version) {
		return version, false
	}

	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version, false
		}

		version[i] = n
	}

	return version, true
}

// migrationPipInstall creates a virtual environment in venvDir and installs
// the Pulumi SDK of the CLI version into it, so the migration program runs
// against the SDK that matches the CLI.
func migrationPipInstall(t *testing.T, venvDir, cliVersion string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	venv := func(args ...string) {
		t.Helper()

		//nolint:gosec // The arguments are fixed below; only the directory comes from the test.
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Env = os.Environ()

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	venv("python3", "-m", "venv", filepath.Join(venvDir, "venv"))
	venv(
		filepath.Join(venvDir, "venv", "bin", "pip"),
		"install",
		"pulumi=="+strings.TrimPrefix(cliVersion, "v"),
	)
}

// migrationMode names the update mode of one migration run: checkpoints,
// journal version 1, or journal version 2.
type migrationMode string

const (
	migrationCheckpoints migrationMode = "checkpoints"
	migrationJournalV1   migrationMode = "journal-v1"
	migrationJournalV2   migrationMode = "journal-v2"
)

// migrationRun groups what one mode runs with.
type migrationRun struct {
	cli        e2e.Install
	store      e2e.Store
	mode       migrationMode
	stack      string
	venvDir    string
	versionCap *e2e.JournalVersionCap
}

// migrationOutcome is the recorded result of one mode: the CLI result of the
// migrating update and the stack export after it.
type migrationOutcome struct {
	up         e2e.Result
	exported   string
	env        *e2e.Env
	versionCap *e2e.JournalVersionCap
}

// runMigrationMode runs the migration program on a fresh server: an update
// without the migration, then an update that migrates the component. The
// version cap lowers the negotiated journal version to 1 for the version 1
// mode. The checkpoints mode answers version 0 through journaling off, so it
// takes no cap.
func runMigrationMode(t *testing.T, run migrationRun) migrationOutcome {
	t.Helper()

	var env *e2e.Env

	switch run.mode {
	case migrationCheckpoints:
		env = e2e.NewEnvWithJournalingOff(t.Context(), t, run.cli, run.store)
	case migrationJournalV1:
		env = e2e.NewEnvWithJournalVersionCap(t.Context(), t, run.cli, run.store, run.versionCap)
	case migrationJournalV2:
		env = e2e.NewEnv(t.Context(), t, run.cli, run.store)
	default:
		t.Fatalf("unknown migration mode %q", run.mode)
	}

	ctx := t.Context()
	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, run.stack)

	for _, s := range migrationSetupSteps(env, qualified) {
		env.Step(ctx, t, s)
	}

	writeMigrationProgram(t, env, migrationProgramV1)

	venvEnv := []string{"PATH=" + filepath.Join(run.venvDir, "venv", "bin") +
		string(os.PathListSeparator) + run.cli.Dir +
		string(os.PathListSeparator) + os.Getenv("PATH")}

	env.Step(ctx, t, migrationStep("up without migration", false, venvEnv,
		"up", "--yes", "--non-interactive"))

	writeMigrationProgram(t, env, migrationProgramV2)

	var up e2e.Result

	if run.mode == migrationJournalV1 {
		up = env.Step(ctx, t, migrationStep("up with migration at version 1", true, venvEnv,
			"up", "--yes", "--non-interactive"))
	} else {
		up = env.Step(ctx, t, migrationStep("up with migration", false, venvEnv,
			"up", "--yes", "--non-interactive"))
	}

	exported := env.Step(ctx, t, migrationStep("stack export", false, venvEnv,
		"stack", "export"))

	return migrationOutcome{up: up, exported: exported.Stdout, env: env, versionCap: run.versionCap}
}

// migrationStep is a scenario step of the migration test with extra
// environment entries for the virtual environment.
func migrationStep(name string, fail bool, extra []string, args ...string) e2e.StepSpec {
	return e2e.StepSpec{
		Check:  nil,
		Name:   name,
		Args:   args,
		Env:    extra,
		Stdin:  "",
		Fail:   fail,
		Redact: false,
	}
}

// migrationSetupSteps returns the login and stack steps before the migration
// updates.
func migrationSetupSteps(env *e2e.Env, qualified string) []e2e.StepSpec {
	return []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("stack init", nil, "stack", "init", qualified),
		step("stack select", nil, "stack", "select", qualified),
	}
}

// writeMigrationProgram replaces the Pulumi program of env with the Python
// program content and its Pulumi.yaml.
func writeMigrationProgram(t *testing.T, env *e2e.Env, program string) {
	t.Helper()

	env.WritePulumiProject(t, "name: "+e2e.Project+"\nruntime: python\n")

	if err := os.WriteFile(filepath.Join(env.ProjectDir(), "__main__.py"), []byte(program), 0o600); err != nil {
		t.Fatalf("write migration program: %v", err)
	}
}

// migrationNotSupportedEngine is the error the engine shows for a resource
// registration that carries a state migration at journal version 1.
// migrationNotSupportedSDK is the error the Python SDK shows for the same
// registration: it checks the monitor feature before the registration reaches
// the engine.
const (
	migrationNotSupportedEngine = "state migrations are not supported"
	migrationNotSupportedSDK    = "does not support state migrations"
)

// migrationRefused reports whether the CLI output shows the version 1
// refusal of a state migration, either from the engine or from the SDK.
func migrationRefused(output string) bool {
	return strings.Contains(output, migrationNotSupportedEngine) ||
		strings.Contains(output, migrationNotSupportedSDK)
}

// checkMigrationV1Refused proves the version 1 mode refused the migration:
// the update failed with the migration error, the cap lowered at least one
// start answer, and the export still holds the unmigrated component.
func checkMigrationV1Refused(t *testing.T, mode migrationOutcome) {
	t.Helper()

	if mode.up.ExitCode == 0 {
		t.Fatal("up with migration at version 1 succeeded, want the refusal of the migration")
	}

	if !migrationRefused(mode.up.Stdout) && !migrationRefused(mode.up.Stderr) {
		t.Fatalf(
			"failed up lacks %q or %q:\nstdout:\n%s\nstderr:\n%s",
			migrationNotSupportedEngine,
			migrationNotSupportedSDK,
			mode.up.Stdout,
			mode.up.Stderr,
		)
	}

	if got := mode.versionCap.Capped(); got == 0 {
		t.Fatal("the version cap lowered no start answer, want at least one version 1 update")
	}

	exported := normalizeMigrationExport(t, mode.exported)

	if !strings.Contains(exported, `"type":"my:mod:OldComp"`) {
		t.Fatalf("export after the refused migration holds no OldComp:\n%s", exported)
	}

	if strings.Contains(exported, `"type":"my:mod:NewComp"`) {
		t.Fatalf("export after the refused migration holds NewComp:\n%s", exported)
	}
}

// checkMigrationExportsMatch proves the version 2 mode built the same state
// as checkpoints: the normalized exports are equal, both hold the migrated
// component, and the version 2 run journaled without checkpoint saves.
func checkMigrationExportsMatch(t *testing.T, checkpoints, version2 string) {
	t.Helper()

	got := normalizeMigrationExport(t, version2)
	want := normalizeMigrationExport(t, checkpoints)

	if got != want {
		t.Fatalf("export at version 2 differs from the checkpoints export:\ngot\n%s\nwant\n%s", got, want)
	}

	if !strings.Contains(got, `"type":"my:mod:NewComp"`) {
		t.Fatalf("export at version 2 holds no NewComp:\n%s", got)
	}

	if strings.Contains(got, `"type":"my:mod:OldComp"`) {
		t.Fatalf("export at version 2 still holds OldComp:\n%s", got)
	}
}

// checkMigrationJournaled proves the version 2 run negotiated version 2 and
// built its checkpoint from the journal: a start answered version 2, at least
// one journal batch was accepted, and no checkpoint save ran.
func checkMigrationJournaled(t *testing.T, env *e2e.Env) {
	t.Helper()

	entries := env.Recorder.Entries()
	negotiated, accepted, checkpoints := false, false, false

	for i := range entries {
		entry := entries[i]

		if strings.Contains(entry.ResponseBody, `"journalVersion":2`) {
			negotiated = true
		}

		if entry.Method == http.MethodPatch && strings.HasSuffix(entry.Path, "/journalentries") &&
			entry.Status == http.StatusOK {
			accepted = true
		}

		if entry.Method == http.MethodPatch && strings.Contains(entry.Path, "/checkpoint") {
			checkpoints = true
		}
	}

	if !negotiated {
		t.Fatal("no start answered journal version 2, want the version 2 update")
	}

	if !accepted {
		t.Fatal("no journal batch was accepted, want the journaled migration")
	}

	if checkpoints {
		t.Fatal("a checkpoint save ran during the version 2 update, want the journal build only")
	}
}

// normalizeMigrationExport returns the canonical form of a stack export for
// the migration comparison: without the manifest, the metadata, the server
// address in the secrets provider state and the resource times, with the
// resources sorted by URN. Only the url key of the state goes: the other keys
// stay in the comparison. Each mode runs on its own server, so the address
// differs by design.
func normalizeMigrationExport(t *testing.T, raw string) string {
	t.Helper()

	var doc struct {
		Version    int            `json:"version"`
		Deployment map[string]any `json:"deployment"`
	}

	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode stack export: %v", err)
	}

	delete(doc.Deployment, "manifest")
	delete(doc.Deployment, "metadata")

	if providers, ok := doc.Deployment["secrets_providers"].(map[string]any); ok {
		if state, ok := providers["state"].(map[string]any); ok {
			delete(state, "url")
		}
	}

	resources, ok := doc.Deployment["resources"].([]any)
	if !ok {
		t.Fatalf("stack export holds no resources: %s", raw)
	}

	for _, res := range resources {
		if obj, ok := res.(map[string]any); ok {
			delete(obj, "created")
			delete(obj, "modified")
		}
	}

	slices.SortFunc(resources, func(a, b any) int {
		return strings.Compare(migrationResourceURN(a), migrationResourceURN(b))
	})

	canonical, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode stack export: %v", err)
	}

	return string(canonical)
}

// migrationResourceURN returns the URN of a decoded resource, or "" when the
// value has none.
func migrationResourceURN(v any) string {
	obj, ok := v.(map[string]any)
	if !ok {
		return ""
	}

	urn, ok := obj["urn"].(string)
	if !ok {
		return ""
	}

	return urn
}

// migrationProgramV1 is the Python program before the migration: a component
// with one child component and no state migration.
const migrationProgramV1 = `import pulumi


class OldComp(pulumi.ComponentResource):
    def __init__(self, name):
        super().__init__("my:mod:OldComp", name, None, None)
        self.child = pulumi.ComponentResource(
            "my:mod:OldChild",
            name + "-child",
            None,
            pulumi.ResourceOptions(parent=self),
        )
        self.register_outputs({})


OldComp("mig")
`

// migrationProgramV2 is the Python program after the migration: the component
// keeps its name under a new type, with an alias for the old type and a state
// migration callback that renames the component and its child.
const migrationProgramV2 = `import pulumi
from typing import Optional

OLD_COMP = "my:mod:OldComp"
NEW_COMP = "my:mod:NewComp"
OLD_CHILD = "my:mod:OldChild"
NEW_CHILD = "my:mod:NewChild"


def migrate(args: pulumi.StateMigrationArgs) -> Optional[pulumi.StateMigrationResult]:
    old_state = args.old_state
    if not old_state:
        return None
    if old_state[0].get("type") != OLD_COMP:
        return None
    old_root_urn = old_state[0].get("urn", "")
    new_root_urn = old_root_urn.replace(OLD_COMP, NEW_COMP)
    new_state = []
    successors = {}
    for res in old_state:
        urn = res.get("urn", "")
        typ = res.get("type", "")
        if typ == OLD_COMP:
            new_res = dict(res)
            new_res["urn"] = new_root_urn
            new_res["type"] = NEW_COMP
            new_state.append(new_res)
            successors[urn] = new_root_urn
        elif typ == OLD_CHILD:
            new_urn = urn.replace(OLD_COMP + "$", NEW_COMP + "$").replace(OLD_CHILD, NEW_CHILD)
            new_res = dict(res)
            new_res["urn"] = new_urn
            new_res["type"] = NEW_CHILD
            if new_res.get("parent") == old_root_urn:
                new_res["parent"] = new_root_urn
            new_state.append(new_res)
            successors[urn] = new_urn
        else:
            raise Exception("Unexpected resource type in migration input: " + typ)
    return pulumi.StateMigrationResult(new_state=new_state, successors=successors)


class NewComp(pulumi.ComponentResource):
    def __init__(self, name):
        super().__init__(
            "my:mod:NewComp",
            name,
            None,
            pulumi.ResourceOptions(
                aliases=[pulumi.Alias(type_=OLD_COMP)],
                state_migrations=[migrate],
            ),
        )
        self.child = pulumi.ComponentResource(
            "my:mod:NewChild",
            name + "-child",
            None,
            pulumi.ResourceOptions(parent=self),
        )
        self.register_outputs({})


NewComp("mig")
`
