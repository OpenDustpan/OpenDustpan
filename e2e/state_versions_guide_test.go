//go:build e2e

package e2e_test

import (
	"net/http"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// restoreMessage is the message of the answer of the restore route.
const restoreMessage = "Restore writes old state as a new version. It changes no infrastructure."

// TestStateVersionsGuideCommands runs each command that
// docs/guides/state-versions.md shows against the e2e test server. The read
// of the state of a stack, the read of the current state version of a
// workspace and the restore of a state version run with the personal access
// token of a person. The doctor command runs with the token of the
// installation.
//
// It writes no report: its run would overwrite the report of TestCLIs for the
// same version and store.
func TestStateVersionsGuideCommands(t *testing.T) {
	t.Parallel()

	t.Run("Pulumi stack export", func(t *testing.T) {
		t.Parallel()

		cli := installOneVersion(t, e2e.Pulumi())

		for _, store := range databasesUnderTest(t) {
			t.Run(string(store), func(t *testing.T) {
				t.Parallel()
				runPulumiExportAfterUp(t, cli, store)
			})
		}
	})

	t.Run("OpenTofu current version, restore and doctor", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS != "linux" {
			// OpenTofu must trust the test server's certificate, and Go
			// honors SSL_CERT_FILE only on Linux.
			t.Skipf("the OpenTofu suite needs Linux, not %s", runtime.GOOS)
		}

		cli := installOneVersion(t, e2e.OpenTofu())

		for _, store := range databasesUnderTest(t) {
			t.Run(string(store), func(t *testing.T) {
				t.Parallel()
				runTofuGuideCommands(t, cli, store)
			})
		}
	})
}

// runTofuGuideCommands runs the commands of the page that need a workspace
// with state: the read of the current state version, the restore of an older
// state version, and the doctor check of the install.
func runTofuGuideCommands(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	skipWithoutPostgres(t, store)

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	env.Step(ctx, t, step("init", nil, "init", "-input=false", "-no-color"))
	env.Step(ctx, t, step("apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	wsID := env.WorkspaceID(ctx, t)
	token := stateVersionReader(ctx, t, env, "state-version-reader")
	path := "/api/v2/workspaces/" + wsID + "/current-state-version"

	// The command of the page "Read the current version of a workspace".
	older := readCurrentStateVersion(t, env, token, path)
	checkCurrentStateVersion(t, "after the first apply", older)

	// A second apply writes a second state version, so the first one is an
	// older version to restore.
	env.WriteTofuConfig(t, tofuSecondConfig(env.Host()))
	env.Step(ctx, t, step("apply again", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	current := readCurrentStateVersion(t, env, token, path)
	checkCurrentStateVersion(t, "after the second apply", current)

	if current.Data.ID == older.Data.ID {
		t.Fatalf("the second apply left the current state version %s unchanged", current.Data.ID)
	}

	// The command of the page "Restore an older version".
	restorePath := "/api/v2/state-versions/" + older.Data.ID + "/actions/restore"

	status, body, err := env.TFEStatus(ctx, token, http.MethodPost, restorePath, "")
	if err != nil {
		t.Fatalf("POST %s: %v", restorePath, err)
	}

	if status != http.StatusOK {
		t.Fatalf("POST %s answers %d, want %d (%s)", restorePath, status, http.StatusOK, body)
	}

	checkStateVersionRestoreAnswer(t, body, older.Data.ID)

	restored := readCurrentStateVersion(t, env, token, path)
	checkCurrentStateVersion(t, "after the restore", restored)

	if restored.Data.ID == current.Data.ID || restored.Data.ID == older.Data.ID {
		t.Fatalf("the restore left the current state version %s, want a new version", restored.Data.ID)
	}

	// The command of the page "Retry after a rollback".
	runDoctorCommand(t, env)
}

// checkStateVersionRestoreAnswer proves that the answer of the restore route
// holds a new state version that names the restored version as its source,
// and the message that no infrastructure changed.
func checkStateVersionRestoreAnswer(t *testing.T, body, from string) {
	t.Helper()

	if !strings.Contains(body, `"type":"state-versions"`) {
		t.Fatalf("the restore answer holds no state version: %s", body)
	}

	if !strings.Contains(body, `"restored-from":"`+from+`"`) {
		t.Fatalf("the restore answer does not name the restored version %s: %s", from, body)
	}

	if !strings.Contains(body, `"message":"`+restoreMessage+`"`) {
		t.Fatalf("the restore answer holds no message %q: %s", restoreMessage, body)
	}
}

// runDoctorCommand runs the `opendustpan doctor` command of the page against
// the e2e test server. The install of the test keeps warnings by design, so
// the step proves that the command runs and prints its report, and it does
// not require exit status 0. The command reads the token of the installation
// and the database value from the environment.
func runDoctorCommand(t *testing.T, env *e2e.Env) {
	t.Helper()

	res := env.RunBinaryWithEnv(
		t.Context(), t,
		[]string{"DUSTPAN_ADMIN_TOKEN=" + env.TokenA(), "DUSTPAN_DATABASE=" + env.DatabaseURL(t)},
		"doctor", "--server", env.URL, "--ca-file", env.CABundle(),
	)

	if !hasDoctorReport(res.Stdout) {
		t.Fatalf("opendustpan doctor prints no check line (exit %d):\n%s%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}
}

// hasDoctorReport reports whether out holds a line of the report of doctor: a
// line that starts with pass, warn or fail.
func hasDoctorReport(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)

		if len(fields) > 0 && (fields[0] == "pass" || fields[0] == "warn" || fields[0] == "fail") {
			return true
		}
	}

	return false
}
