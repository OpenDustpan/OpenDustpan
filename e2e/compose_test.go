//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// composeStack is what a CLI run needs to reach the stack.
type composeStack struct {
	host  string
	token string
	cert  string
}

// command is one process that a step runs.
type command struct {
	dir  string
	env  []string
	name string
	args []string
	// clean runs the process with env as its whole environment, so nothing
	// of the developer's own CLI setup leaks into a CLI run.
	clean bool
}

const (
	composeStepLimit = 10 * time.Minute
	composeDirMode   = 0o750
	composeFileMode  = 0o600
	composeNamespace = "acme"
)

// TestComposeQuickstart brings up the stack of deploy/compose, as the
// quickstart guide says, and drives the real Pulumi CLI and OpenTofu against
// it. It builds the server image, runs init.sh, starts the stack and waits
// for every healthcheck. It makes a namespace, then runs pulumi login and
// pulumi stack init, and tofu init and tofu apply. It removes the stack and
// its volumes at the end. It needs Docker, OpenSSL and the Compose plugin, so
// it runs only with E2E_COMPOSE=1 (make compose-smoke).
//
// E2E_COMPOSE_IMAGE names an image to use instead of a build.
func TestComposeQuickstart(t *testing.T) {
	t.Parallel()

	if os.Getenv("E2E_COMPOSE") != "1" {
		t.Skip("set E2E_COMPOSE=1 (make compose-smoke) to run the compose quickstart test")
	}

	root := repoRoot(t)
	ctx := t.Context()

	image := os.Getenv("E2E_COMPOSE_IMAGE")
	if image == "" {
		image = "opendustpan:local"

		runIn(ctx, t, command{dir: root, env: nil, name: "./scripts/build-image-local.sh", args: nil, clean: false})
	}

	pulumi := installCLI(t, e2e.Pulumi(), "3.250.0")
	tofu := installCLI(t, e2e.OpenTofu(), "1.12.12")

	dir := t.TempDir()
	copyComposeFiles(t, filepath.Join(root, "deploy", "compose"), dir)
	runIn(ctx, t, command{dir: dir, env: nil, name: "./init.sh", args: nil, clean: false})

	port := freePort(t)
	project := "dustpan-smoke-" + strconv.Itoa(os.Getpid())
	composeEnv := []string{
		"OPENDUSTPAN_PORT=" + port,
		"OPENDUSTPAN_IMAGE=" + image,
	}

	compose := func(ctx context.Context, args ...string) string {
		t.Helper()

		return runIn(ctx, t, command{
			dir:   dir,
			env:   composeEnv,
			name:  "docker",
			args:  append([]string{"compose", "-p", project}, args...),
			clean: false,
		})
	}

	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), composeStepLimit)
		defer cancel()

		if t.Failed() {
			logs, logErr := composeCommand(cleanCtx, dir, composeEnv, "docker",
				"compose", "-p", project, "logs", "--no-color", "--tail", "100")
			t.Logf("stack logs (error %v):\n%s", logErr, logs)
		}

		out, err := composeCommand(cleanCtx, dir, composeEnv, "docker",
			"compose", "-p", project, "down", "--volumes", "--remove-orphans")
		if err != nil {
			t.Errorf("remove the stack: %v\n%s", err, out)
		}
	})

	compose(ctx, "up", "--detach", "--wait", "--wait-timeout", "300")

	token := envFileValue(t, filepath.Join(dir, ".env"), "OPENDUSTPAN_ADMIN_TOKEN")
	cert := filepath.Join(dir, "tls", "cert.pem")
	host := "localhost:" + port

	// The server reads the token from its own environment in the container.
	out := compose(ctx, "exec", "-T", "opendustpan", "/opendustpan", "namespace", "create",
		"--server", "https://localhost:8080", "--ca-file", "/tls/cert.pem", composeNamespace)
	if !strings.Contains(out, "created namespace "+composeNamespace) {
		t.Fatalf("namespace create did not report the namespace:\n%s", out)
	}

	stack := composeStack{host: host, token: token, cert: cert}

	// The subtests end before the restart check below. Their directories
	// come from this test, so that they still exist when the check runs.
	pulumiDirs := [2]string{t.TempDir(), t.TempDir()}
	tofuDirs := [2]string{t.TempDir(), t.TempDir()}

	var (
		listStacks   func(ctx context.Context, t *testing.T) string
		tofuGreeting func(ctx context.Context, t *testing.T) string
	)

	t.Run("Pulumi CLI", func(t *testing.T) {
		t.Parallel()
		listStacks = composePulumi(ctx, t, pulumi, stack, pulumiDirs)
	})

	t.Run("OpenTofu", func(t *testing.T) {
		t.Parallel()
		tofuGreeting = composeTofu(ctx, t, tofu, stack, tofuDirs)
	})

	// This cleanup runs after both subtests end and before the cleanup that
	// removes the stack. The data must survive a restart of the stack. Go
	// cancels the context of the test before it runs the cleanups.
	t.Cleanup(func() {
		if t.Failed() || listStacks == nil || tofuGreeting == nil {
			return
		}

		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), composeStepLimit)
		defer cancel()

		compose(restartCtx, "down")
		compose(restartCtx, "up", "--detach", "--wait", "--wait-timeout", "300")

		if out := listStacks(restartCtx, t); !strings.Contains(out, "dev") {
			t.Errorf("pulumi stack ls does not list the stack after a restart:\n%s", out)
		}

		if out := tofuGreeting(restartCtx, t); !strings.Contains(out, "hello") {
			t.Errorf("tofu output greeting does not show the value after a restart:\n%s", out)
		}
	})
}

// composePulumi makes a stack and answers a function that lists the stacks.
// dirs hold the project and the home directory.
func composePulumi(
	ctx context.Context, t *testing.T, cli e2e.Install, stack composeStack, dirs [2]string,
) func(ctx context.Context, t *testing.T) string {
	t.Helper()

	project, home := dirs[0], dirs[1]

	writeFile(t, filepath.Join(project, "Pulumi.yaml"), "name: demo\nruntime: yaml\n")

	env := []string{
		"PATH=" + cli.Dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home,
		"PULUMI_HOME=" + filepath.Join(home, ".pulumi"),
		"PULUMI_ACCESS_TOKEN=" + stack.token,
		"PULUMI_SKIP_UPDATE_CHECK=true",
		"PULUMI_CONFIG_PASSPHRASE=",
		"SSL_CERT_FILE=" + stack.cert,
		"NO_COLOR=1",
	}

	runCLI := func(args ...string) string {
		t.Helper()

		return runIn(ctx, t, command{dir: project, env: env, name: cli.Binary(), args: args, clean: true})
	}

	runCLI("login", "https://"+stack.host)
	runCLI("stack", "init", composeNamespace+"/demo/dev")

	if out := runCLI("stack", "ls"); !strings.Contains(out, "dev") {
		t.Fatalf("pulumi stack ls does not list the stack:\n%s", out)
	}

	return func(ctx context.Context, t *testing.T) string {
		t.Helper()

		return runIn(ctx, t, command{
			dir: project, env: env, name: cli.Binary(), args: []string{"stack", "ls"}, clean: true,
		})
	}
}

// composeTofu applies a configuration and answers a function that reads its
// output. dirs hold the project and the home directory.
func composeTofu(
	ctx context.Context, t *testing.T, cli e2e.Install, stack composeStack, dirs [2]string,
) func(ctx context.Context, t *testing.T) string {
	t.Helper()

	project, home := dirs[0], dirs[1]

	writeFile(t, filepath.Join(project, "main.tf"), fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = "web"
    }
  }
}

output "greeting" {
  value = "hello"
}
`, stack.host, composeNamespace))

	rc := filepath.Join(home, "dev.tfrc")
	writeFile(t, rc, fmt.Sprintf("credentials %q {\n  token = %q\n}\n", stack.host, stack.token))

	env := []string{
		"PATH=" + cli.Dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home,
		"TF_CLI_CONFIG_FILE=" + rc,
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"SSL_CERT_FILE=" + stack.cert,
		"NO_COLOR=1",
	}

	runCLI := func(args ...string) string {
		t.Helper()

		return runIn(ctx, t, command{dir: project, env: env, name: cli.Binary(), args: args, clean: true})
	}

	runCLI("init")

	if out := runCLI("apply", "-auto-approve"); !strings.Contains(out, "Apply complete") {
		t.Fatalf("tofu apply did not complete:\n%s", out)
	}

	return func(ctx context.Context, t *testing.T) string {
		t.Helper()

		return runIn(ctx, t, command{
			dir: project, env: env, name: cli.Binary(), args: []string{"output", "-raw", "greeting"}, clean: true,
		})
	}
}

func installCLI(t *testing.T, tool e2e.Tool, version string) e2e.Install {
	t.Helper()

	fetcher := sharedFetcher(t)

	// The download keeps an idle connection, which goleak reports at the end.
	t.Cleanup(http.DefaultClient.CloseIdleConnections)

	ctx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	cli, err := fetcher.Install(ctx, tool, version)
	if err != nil {
		t.Fatalf("install %s %s: %v", tool.Name, version, err)
	}

	return cli
}

// repoRoot finds the directory with go.mod above the test.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}

		dir = parent
	}
}

// copyComposeFiles copies the files of deploy/compose into dst, so the test
// writes .env and tls/ in a temporary directory and not in the checkout.
func copyComposeFiles(t *testing.T, src, dst string) {
	t.Helper()

	from, err := os.OpenRoot(src)
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}

	defer closeRoot(t, from)

	to, err := os.OpenRoot(dst)
	if err != nil {
		t.Fatalf("open %s: %v", dst, err)
	}

	defer closeRoot(t, to)

	for _, name := range []string{"compose.yaml", "init.sh", ".env.example"} {
		body, err := from.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		mode := os.FileMode(composeFileMode)
		if name == "init.sh" {
			mode = composeDirMode
		}

		if err := to.WriteFile(name, body, mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func envFileValue(t *testing.T, path, key string) string {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatalf("open %s: %v", filepath.Dir(path), err)
	}

	defer closeRoot(t, root)

	body, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	for line := range strings.SplitSeq(string(body), "\n") {
		if value, ok := strings.CutPrefix(line, key+"="); ok {
			return value
		}
	}

	t.Fatalf("%s has no %s", path, key)

	return ""
}

func closeRoot(t *testing.T, root *os.Root) {
	t.Helper()

	if err := root.Close(); err != nil {
		t.Errorf("close %s: %v", root.Name(), err)
	}
}

func freePort(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}

	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("split %q: %v", l.Addr(), err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("close the port probe: %v", err)
	}

	return port
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), composeFileMode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// composeCommand runs name in dir with extra added to the environment. It
// answers the combined output.
func composeCommand(ctx context.Context, dir string, extra []string, name string, args ...string) (string, error) {
	stepCtx, cancel := context.WithTimeout(ctx, composeStepLimit)
	defer cancel()

	//nolint:gosec // The suite runs fixed tools (docker, the CLIs under test) with its own arguments.
	cmd := exec.CommandContext(stepCtx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(withoutStackVariables(os.Environ()), extra...)

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()

	return out.String(), err
}

// runIn runs a command and fails the test on a non-zero exit.
func runIn(ctx context.Context, t *testing.T, c command) string {
	t.Helper()

	run := composeCommand
	if c.clean {
		run = cleanCommand
	}

	out, err := run(ctx, c.dir, c.env, c.name, c.args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", c.name, strings.Join(c.args, " "), err, out)
	}

	return out
}

// cleanCommand runs name with exactly env as its environment, except for the
// proxy variables that a download may need.
func cleanCommand(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	stepCtx, cancel := context.WithTimeout(ctx, composeStepLimit)
	defer cancel()

	//nolint:gosec // The suite runs the CLIs under test with its own arguments.
	cmd := exec.CommandContext(stepCtx, name, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"NO_PROXY=127.0.0.1,localhost"}, env...)

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()

	return out.String(), err
}

// withoutStackVariables drops the variables that a shell may hold and that
// the compose stack reads, so that they cannot override the .env file.
func withoutStackVariables(env []string) []string {
	prefixes := []string{"DUSTPAN_", "OPENDUSTPAN_", "POSTGRES_", "S3_", "AWS_"}
	kept := make([]string, 0, len(env))

	for _, entry := range env {
		drop := false

		for _, prefix := range prefixes {
			if strings.HasPrefix(entry, prefix) {
				drop = true

				break
			}
		}

		if !drop {
			kept = append(kept, entry)
		}
	}

	return kept
}
