package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

var errUnsetFailed = errors.New("unset failed")

// childMarker tells the serve-level test that it runs as the child process,
// which starts with the canary variables already set.
const childMarker = "DUSTPAN_TEST_ENVCLEAR_CHILD"

// notServedReason answers why the serve-level test leaves a variable out, and
// whether it does. The injected-unset tests still cover each of them.
func notServedReason(name string) (string, bool) {
	switch name {
	case "DUSTPAN_AUDIT_SINK", "DUSTPAN_LOG_SINK":
		return "a sink address: a canary makes the start refuse the sink", true
	case "DUSTPAN_OIDC_ISSUER":
		return "an issuer address: a canary makes the start fetch the issuer", true
	case "DUSTPAN_DEV_ADMIN_TOKEN":
		return "a variable of dev: serve refuses to start when it is set", true
	default:
		return "", false
	}
}

// servedNames answers the variables that the serve-level test sets.
func servedNames() []string {
	var names []string

	for _, name := range clearedNames() {
		if _, skip := notServedReason(name); !skip {
			names = append(names, name)
		}
	}

	return names
}

// clearedNames answers every variable that the serve hook must clear.
func clearedNames() []string {
	return append(cli.SensitiveEnvs(), cli.ExternalSecretEnvs()...)
}

// TestClearSensitiveEnvUnsetsEveryListedVariable proves that clearSensitiveEnv
// asks for the unset of each sensitive setting variable and of each external
// secret variable. The unset is injected, so the test never touches the
// process environment.
func TestClearSensitiveEnvUnsetsEveryListedVariable(t *testing.T) {
	t.Parallel()

	var unset []string

	err := clearSensitiveEnv(func(name string) error {
		unset = append(unset, name)

		return nil
	})
	if err != nil {
		t.Fatalf("clearSensitiveEnv: %v", err)
	}

	for _, want := range []string{
		"DUSTPAN_OIDC_CLIENT_SECRET", "DUSTPAN_AUDIT_WEBHOOK_SECRET", "DUSTPAN_LOG_WEBHOOK_SECRET",
		"DUSTPAN_AUDIT_OTLP_HEADERS", "DUSTPAN_LOG_OTLP_HEADERS", "DUSTPAN_ADMIN_TOKEN", "DUSTPAN_KEY_ENCRYPTION_KEY",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "PGPASSWORD", "PGSSLPASSWORD",
	} {
		if !slices.Contains(unset, want) {
			t.Errorf("clearSensitiveEnv does not unset %s", want)
		}
	}

	for _, name := range cli.SensitiveEnvs() {
		if !slices.Contains(unset, name) {
			t.Errorf("clearSensitiveEnv does not unset the sensitive setting variable %s", name)
		}
	}
}

// TestClearSensitiveEnvFailsClosed proves a failing unset stops the start,
// names the variable, and does not stop the clear of the other variables.
func TestClearSensitiveEnvFailsClosed(t *testing.T) {
	t.Parallel()

	var cleared []string

	err := clearSensitiveEnv(func(name string) error {
		if name == "DUSTPAN_OIDC_CLIENT_SECRET" {
			return errUnsetFailed
		}

		cleared = append(cleared, name)

		return nil
	})
	if !errors.Is(err, errUnsetFailed) || !strings.Contains(err.Error(), "DUSTPAN_OIDC_CLIENT_SECRET") {
		t.Fatalf("err = %v, want a failure that names DUSTPAN_OIDC_CLIENT_SECRET", err)
	}

	if len(cleared) != len(clearedNames())-1 {
		t.Errorf("cleared %d variables, want %d", len(cleared), len(clearedNames())-1)
	}
}

// TestServeClearsSensitiveEnvironment starts the test binary as a child
// process. The child has a canary value in each variable that the hook must
// clear, runs serve until it listens, and requires each variable to be unset.
// The parent never sets process environment, so it runs in parallel.
func TestServeClearsSensitiveEnvironment(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}

	dir := t.TempDir()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + dir,
		"HOME=" + dir,
		childMarker + "=1",
		"DUSTPAN_DATABASE=sqlite:" + filepath.Join(dir, "dustpan.db"),
		"DUSTPAN_ADMIN_TOKEN=" + adminTestToken,
		keyEnvVar + "=" + testKeyBase64(),
	}

	for _, name := range servedNames() {
		if !slices.ContainsFunc(env, func(entry string) bool { return strings.HasPrefix(entry, name+"=") }) {
			env = append(env, name+"=canary-"+strings.ToLower(name))
		}
	}

	out, state := runChild(t, exe, dir, env)

	if !state.Success() {
		t.Fatalf("child failed: %s\n%s", state, out)
	}

	if !strings.Contains(out, "--- PASS: TestServeClearsSensitiveEnvironmentChild") {
		t.Fatalf("the child did not run to a pass:\n%s", out)
	}
}

// runChild runs the child test of the binary exe in dir with the environment
// env, and answers its output and its exit state.
func runChild(t *testing.T, exe, dir string, env []string) (string, *os.ProcessState) {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}

	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close root: %v", closeErr)
		}
	})

	outFile, err := root.Create("child.log")
	if err != nil {
		t.Fatalf("create child log: %v", err)
	}

	proc, err := os.StartProcess(exe, []string{exe, "-test.v", "-test.run=^TestServeClearsSensitiveEnvironmentChild$"},
		&os.ProcAttr{Dir: dir, Env: env, Files: []*os.File{nil, outFile, outFile}, Sys: nil})
	if err != nil {
		t.Fatalf("start child: %v", err)
	}

	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("wait for child: %v", err)
	}

	if closeErr := outFile.Close(); closeErr != nil {
		t.Fatalf("close child log: %v", closeErr)
	}

	out, err := root.ReadFile("child.log")
	if err != nil {
		t.Fatalf("read child log: %v", err)
	}

	return string(out), state
}

// TestServeClearsSensitiveEnvironmentChild is the child of
// TestServeClearsSensitiveEnvironment. Without the marker it does nothing.
func TestServeClearsSensitiveEnvironmentChild(t *testing.T) {
	t.Parallel()

	if _, ok := os.LookupEnv(childMarker); !ok {
		t.Skip("runs only as the child of TestServeClearsSensitiveEnvironment")
	}

	names := servedNames()
	for _, name := range names {
		if _, set := os.LookupEnv(name); !set {
			t.Fatalf("%s is not set before serve: the canary did not reach the child", name)
		}
	}

	if logs, startErr := startServerLogs(t, "--listen", "127.0.0.1:0"); startErr != nil {
		t.Fatalf("serve: %v\n%s", startErr, logs)
	}

	for _, name := range names {
		if _, set := os.LookupEnv(name); set {
			t.Errorf("%s is still set after serve started", name)
		}
	}
}
