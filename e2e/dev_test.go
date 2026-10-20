//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	// devReadyLimit bounds the wait for a dev server to write dev.addr.
	devReadyLimit = 60 * time.Second
	// devStopLimit bounds the wait for a dev server to stop after SIGTERM.
	devStopLimit = 30 * time.Second
	// devPollInterval sets how often the wait checks for dev.addr.
	devPollInterval = 50 * time.Millisecond
	// devNamespace is the namespace that dev makes, and devProject the
	// project and workspace that the scenarios use in it.
	devNamespace = "dev"
	devProject   = "demo"
	// devCommandTimeout bounds one CLI command.
	devCommandTimeout = 2 * time.Minute
)

// devPulumiProject is a project with no resource and one output, so up needs
// no provider.
const devPulumiProject = "name: " + devProject + "\nruntime: yaml\noutputs:\n  greeting: hello\n"

// devTofuHTTPConfig is the configuration of a workspace in the namespace dev
// through the http backend. The password comes from TF_HTTP_PASSWORD in
// dev.env, never from this file.
const devTofuHTTPConfig = `terraform {
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

// devTofuCloudConfig is the configuration of a workspace in the namespace dev
// through the cloud block, which needs HTTPS.
const devTofuCloudConfig = `terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

output "greeting" {
  value = "hello"
}
`

var (
	errDevNotReady = errors.New("dev did not write dev.addr in time")
	devCodePattern = regexp.MustCompile(`#code=([A-Za-z0-9_-]+)`)
)

// ptyBuffer holds the output of a process on a pseudo terminal.
type ptyBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *ptyBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // A transparent wrapper: callers see the original error.
}

func (b *ptyBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// devAddrFile is the content of dev.addr.
type devAddrFile struct {
	Addr string `json:"addr"`
	Cert string `json:"cert"`
	PID  int    `json:"pid"`
}

// devServer is a dev command that runs on a pseudo terminal, so it prints the
// sign-in link as it does for a person.
type devServer struct {
	cmd  *exec.Cmd
	out  *ptyBuffer
	done chan error
	dir  string
	// pid is the process of dev, from dev.addr: the process that script runs.
	pid int
	// url is the URL of the server.
	url string
}

// shellQuote quotes s for a POSIX sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// startDevServer starts the dev command of bin with args under script, which
// gives it a pseudo terminal, and waits until it has written dev.addr. dir is
// the data directory. env holds the extra variables of the process, and the
// process has no DUSTPAN_ variable and no other setting.
func startDevServer(t *testing.T, bin, dir string, env []string, args ...string) *devServer {
	t.Helper()

	words := make([]string, 0, 2+len(args))
	words = append(words, shellQuote(bin), "dev")

	for _, arg := range args {
		words = append(words, shellQuote(arg))
	}

	out := &ptyBuffer{buf: bytes.Buffer{}, mu: sync.Mutex{}}

	//nolint:gosec // The test runs the binary that it built, with arguments that it chose.
	cmd := exec.CommandContext(
		context.WithoutCancel(t.Context()),
		"script",
		"-qefc",
		strings.Join(words, " "),
		"/dev/null",
	)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, env...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = devStopLimit

	if err := cmd.Start(); err != nil {
		t.Fatalf("start dev: %v", err)
	}

	server := &devServer{cmd: cmd, out: out, done: make(chan error, 1), dir: dir, pid: 0, url: ""}

	go func() { server.done <- cmd.Wait() }()

	t.Cleanup(func() { server.stopAtEnd(t) })

	addr, err := server.waitAddr(t)
	if err != nil {
		t.Fatalf("%v\noutput of dev:\n%s", err, out.String())
	}

	server.pid = addr.PID
	server.url = "http://" + addr.Addr

	if addr.Cert != "" {
		server.url = "https://" + addr.Addr
	}

	return server
}

// waitAddr waits until dev writes dev.addr and answers its content.
func (d *devServer) waitAddr(t *testing.T) (devAddrFile, error) {
	t.Helper()

	deadline := time.Now().Add(devReadyLimit)

	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(d.dir, "dev.addr"))
		if err == nil {
			var addr devAddrFile
			if jsonErr := json.Unmarshal(raw, &addr); jsonErr == nil && addr.PID != 0 {
				return addr, nil
			}
		}

		select {
		case err := <-d.done:
			d.done <- err

			return devAddrFile{}, fmt.Errorf("dev ended before dev.addr: %w", err)
		case <-time.After(devPollInterval):
		}
	}

	return devAddrFile{}, errDevNotReady
}

// stop ends dev as a signal does and answers its exit error. A second call
// answers the same result.
func (d *devServer) stop(t *testing.T) error {
	t.Helper()

	if d.pid != 0 {
		if err := syscall.Kill(d.pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("signal dev: %v", err)
		}
	} else if d.cmd.Process != nil {
		// dev never wrote dev.addr: stop the terminal wrapper.
		if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("signal the terminal wrapper: %v", err)
		}
	}

	select {
	case err := <-d.done:
		d.done <- err

		return err
	case <-time.After(devStopLimit):
		_ = d.cmd.Process.Kill() //nolint:errcheck // The test fails below.

		t.Errorf("dev did not stop after SIGTERM:\n%s", d.out.String())

		return errDevNotReady
	}
}

// stopAtEnd stops dev at the end of a test, and fails the test when dev does
// not stop clean.
func (d *devServer) stopAtEnd(t *testing.T) {
	t.Helper()

	if err := d.stop(t); err != nil {
		t.Errorf("dev did not stop clean: %v\n%s", err, d.out.String())
	}
}

// devPost sends the one-time code to the dev sign-in and answers the status
// and the cookies of the answer.
func devPost(ctx context.Context, t *testing.T, url, code string) (int, []*http.Cookie) {
	t.Helper()

	body := strings.NewReader(`{"code":"` + code + `"}`)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/api/dustpan/v1/sign-in/dev-code", body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sign in: %v", err)
	}

	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		t.Fatalf("read body: %v", copyErr)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	return resp.StatusCode, resp.Cookies()
}

// consoleStub makes a directory with one page, so that dev serves a console
// and prints the sign-in link.
func consoleStub(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatalf("write console page: %v", err)
	}

	return dir
}

// devCLI runs the CLI of one install with the settings of dev.env and nothing
// else: the variables of the Pulumi CLI, of OpenTofu and of the SSL roots all
// come from the file that dev wrote.
type devCLI struct {
	cli     e2e.Install
	home    string
	envFile string
	work    string
}

func newDevCLI(t *testing.T, cli e2e.Install, dir string) devCLI {
	t.Helper()

	return devCLI{cli: cli, home: t.TempDir(), envFile: filepath.Join(dir, "dev.env"), work: t.TempDir()}
}

// run runs the command of the CLI after it sources dev.env, and answers its
// output.
func (c devCLI) run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), devCommandTimeout)
	defer cancel()

	script := make([]string, 0, 5+len(args))
	script = append(script, "-c", `. "$1" && shift && exec "$@"`, "sh", c.envFile, c.cli.Binary())
	script = append(script, args...)

	//nolint:gosec // The test runs the CLI that it installed, with arguments that it chose.
	cmd := exec.CommandContext(ctx, "sh", script...)
	cmd.Dir = c.work
	cmd.Env = []string{
		"PATH=" + c.cli.Dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + c.home, "NO_COLOR=1", "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost",
		"PULUMI_HOME=" + filepath.Join(c.home, ".pulumi"), "PULUMI_SKIP_UPDATE_CHECK=true",
		"TF_IN_AUTOMATION=1", "TF_INPUT=0",
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w", c.cli.Tool, strings.Join(args, " "), err)
	}

	return string(out), nil
}

// mustRun runs the command and fails the test when it fails. It answers the
// output.
func (c devCLI) mustRun(t *testing.T, args ...string) string {
	t.Helper()

	out, err := c.run(t, args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	return out
}

// writeWork writes a file in the working directory of the CLI.
func (c devCLI) writeWork(t *testing.T, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(c.work, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestDevCommand proves opendustpan dev with the real binary and the real
// CLIs: the first run prints a sign-in link that works one time, --ephemeral
// leaves no file, dev and serve refuse each other's database, the Pulumi CLI
// logs in, makes a stack and runs up with only dev.env, and OpenTofu runs init
// and apply through the http backend, and through the cloud block with --tls,
// with only dev.env and dev.tfrc. The server has no setting but its flags.
func TestDevCommand(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the dev e2e step needs Linux (a pseudo terminal and SSL_CERT_FILE), not %s", runtime.GOOS)
	}

	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("the dev e2e step needs the script utility for a pseudo terminal")
	}

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")
	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	// The downloads and the requests to the servers keep connections open
	// in the default client. The check for goroutines at the end of the
	// package fails for each one that stays.
	t.Cleanup(http.DefaultClient.CloseIdleConnections)

	t.Run("installation", func(t *testing.T) {
		t.Parallel()
		devInstallation(t, bin)
	})

	for _, tool := range []e2e.Tool{e2e.Pulumi(), e2e.OpenTofu()} {
		t.Run(tool.Name, func(t *testing.T) {
			t.Parallel()

			for _, label := range tool.Versions() {
				t.Run(label, func(t *testing.T) {
					t.Parallel()
					devWithCLI(t, bin, fetcher, tool, label)
				})
			}
		})
	}
}

// devWithCLI installs one CLI version and runs its scenario against dev.
func devWithCLI(t *testing.T, bin string, fetcher *e2e.Fetcher, tool e2e.Tool, label string) {
	t.Helper()

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	cli, err := fetcher.Install(installCtx, tool, label)
	if err != nil {
		t.Fatalf("install %s %s: %v", tool.Name, label, err)
	}

	if tool.Name == e2e.Pulumi().Name {
		t.Run("plain HTTP", func(t *testing.T) { t.Parallel(); devPulumi(t, startPlainDev(t, bin), cli) })
		t.Run("TLS", func(t *testing.T) { t.Parallel(); devPulumi(t, startPlainDev(t, bin, "--tls"), cli) })

		return
	}

	t.Run("http backend", func(t *testing.T) { t.Parallel(); devTofuHTTP(t, bin, cli) })
	t.Run("cloud block with TLS", func(t *testing.T) { t.Parallel(); devTofuCloud(t, bin, cli) })
}

// startPlainDev starts dev with no setting but a data directory and a port
// that the system chooses.
func startPlainDev(t *testing.T, bin string, extra ...string) *devServer {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "trial")
	args := append([]string{"--data-dir", dir, "--listen", "127.0.0.1:0", "--console-dir", consoleStub(t)}, extra...)

	return startDevServer(t, bin, dir, nil, args...)
}

// devPulumi drives the Pulumi CLI against dev with only dev.env: login, whoami,
// stack init in the namespace dev, up, and export.
func devPulumi(t *testing.T, server *devServer, cli e2e.Install) {
	t.Helper()

	pulumi := newDevCLI(t, cli, server.dir)
	pulumi.writeWork(t, "Pulumi.yaml", devPulumiProject)

	qualified := devNamespace + "/" + devProject + "/dev"

	if out := pulumi.mustRun(t, "login"); !strings.Contains(out, server.url) {
		t.Fatalf("login printed %q, want it to name %s", out, server.url)
	}

	pulumi.mustRun(t, "stack", "init", qualified)

	if out := pulumi.mustRun(t, "up", "--yes", "--non-interactive"); !strings.Contains(out, "greeting") {
		t.Fatalf("up printed %q, want the output greeting", out)
	}

	if out := pulumi.mustRun(t, "stack", "export"); !strings.Contains(out, "greeting") {
		t.Fatalf("stack export printed %q, want the output greeting", out)
	}
}

// devTofuHTTP drives OpenTofu through the http backend with only dev.env and
// dev.tfrc: init, apply, and state pull.
func devTofuHTTP(t *testing.T, bin string, cli e2e.Install) {
	t.Helper()

	server := startPlainDev(t, bin)
	tofu := newDevCLI(t, cli, server.dir)

	address := server.url + "/tofu/state/" + devNamespace + "/" + devProject
	tofu.writeWork(t, "main.tf", fmt.Sprintf(devTofuHTTPConfig, address, address, address))

	tofu.mustRun(t, "init", "-input=false", "-no-color")

	out := tofu.mustRun(t, "apply", "-input=false", "-auto-approve", "-no-color")
	if !strings.Contains(out, "greeting") {
		t.Fatalf("apply printed %q, want the output greeting", out)
	}

	if out := tofu.mustRun(t, "state", "pull"); !strings.Contains(out, "greeting") {
		t.Fatalf("state pull printed %q, want the output greeting", out)
	}
}

// devTofuCloud drives OpenTofu through the cloud block against a dev server
// with --tls, with only dev.env and dev.tfrc. The credentials block of
// dev.tfrc names the host with its port, and the bundle of dev.env makes the
// self-signed certificate trusted with no trust store change.
func devTofuCloud(t *testing.T, bin string, cli e2e.Install) {
	t.Helper()

	server := startPlainDev(t, bin, "--tls")
	tofu := newDevCLI(t, cli, server.dir)

	host := strings.TrimPrefix(server.url, "https://")
	tofu.writeWork(t, "main.tf", fmt.Sprintf(devTofuCloudConfig, host, devNamespace, devProject))

	tofu.mustRun(t, "init", "-input=false", "-no-color")

	out := tofu.mustRun(t, "apply", "-input=false", "-auto-approve", "-no-color")
	if !strings.Contains(out, "greeting") {
		t.Fatalf("apply printed %q, want the output greeting", out)
	}
}

// devInstallation proves what needs no CLI: the sign-in link works one time,
// the capabilities document tells the kind to the administrator only,
// --ephemeral leaves no file, and dev and serve refuse each other's database.
func devInstallation(t *testing.T, bin string) {
	t.Helper()

	t.Run("sign-in link", func(t *testing.T) { t.Parallel(); devSignInLink(t, bin) })
	t.Run("ephemeral", func(t *testing.T) { t.Parallel(); devEphemeral(t, bin) })
	t.Run("serve and dev refuse each other", func(t *testing.T) { t.Parallel(); devKindRefusals(t, bin) })
}

// devSignInLink proves the link of the first start: it holds a code that
// signs a browser in one time, the banner never holds the token, and the
// capabilities document names the kind dev to the administrator.
func devSignInLink(t *testing.T, bin string) {
	t.Helper()

	server := startPlainDev(t, bin)
	banner := server.waitBanner(t, "#code=")

	match := devCodePattern.FindStringSubmatch(banner)
	if match == nil {
		t.Fatalf("the first start on a terminal prints no sign-in link:\n%s", banner)
	}

	token := strings.TrimSpace(string(mustReadFile(t, filepath.Join(server.dir, "dev.token"))))
	if strings.Contains(banner, token) {
		t.Fatalf("the banner holds the administrator token:\n%s", banner)
	}

	status, cookies := devPost(t.Context(), t, server.url, match[1])
	if status != http.StatusNoContent || len(cookies) != 1 {
		t.Fatalf(
			"the code the first time: status %d with %d cookies, want 204 and the session cookie",
			status,
			len(cookies),
		)
	}

	if again, _ := devPost(t.Context(), t, server.url, match[1]); again != http.StatusForbidden {
		t.Fatalf("the code the second time: status %d, want 403", again)
	}

	body := getWithToken(t, server.url+"/api/dustpan/v1/capabilities", token)
	if !strings.Contains(body, `"installationKind":"dev"`) {
		t.Fatalf("capabilities with the token = %s, want the kind dev", body)
	}

	body = getWithToken(t, server.url+"/api/dustpan/v1/capabilities", "")
	if strings.Contains(body, "installationKind") {
		t.Fatalf("capabilities with no sign-in = %s, want no kind", body)
	}
}

// getWithToken answers the body of a GET request with an optional bearer
// token.
func getWithToken(t *testing.T, url, token string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(body)
}

// mustReadFile reads a file that dev wrote.
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // The test reads a file that dev wrote.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return raw
}

// devEphemeral proves --ephemeral: dev writes to a temporary directory only,
// says so, and leaves no file after a clean stop.
func devEphemeral(t *testing.T, bin string) {
	t.Helper()

	tmp := t.TempDir()

	// The temporary directory of dev is a new directory in tmp. dev.addr holds
	// its process, so the test finds the directory in tmp after the start.
	cmd := startEphemeral(t, bin, tmp)

	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the temporary directory holds %v, %v, want one directory\n%s", entries, err, cmd.out.String())
	}

	banner := cmd.waitBanner(t, "dev deletes this directory at stop")
	if !strings.Contains(banner, "dev deletes this directory at stop") {
		t.Fatalf("the banner does not say that a stop deletes the data:\n%s", banner)
	}

	if stopErr := cmd.stop(t); stopErr != nil {
		t.Fatalf("dev did not stop clean: %v\n%s", stopErr, cmd.out.String())
	}

	left, readErr := os.ReadDir(tmp)
	if readErr != nil || len(left) != 0 {
		t.Fatalf("after the stop, the temporary directory holds %v, %v, want nothing", left, readErr)
	}
}

// startEphemeral starts dev with --ephemeral and a temporary directory in tmp,
// and waits until it listens.
func startEphemeral(t *testing.T, bin, tmp string) *devServer {
	t.Helper()

	words := strings.Join([]string{shellQuote(bin), "dev", "--ephemeral", "--listen", "127.0.0.1:0"}, " ")
	out := &ptyBuffer{buf: bytes.Buffer{}, mu: sync.Mutex{}}

	//nolint:gosec // The test runs the binary that it built.
	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), "script", "-qefc", words, "/dev/null")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "TMPDIR=" + tmp}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = devStopLimit

	if err := cmd.Start(); err != nil {
		t.Fatalf("start dev: %v", err)
	}

	server := &devServer{cmd: cmd, out: out, done: make(chan error, 1), dir: "", pid: 0, url: ""}

	go func() { server.done <- cmd.Wait() }()

	t.Cleanup(func() { server.stopAtEnd(t) })

	server.dir = waitForTempDir(t, tmp, server)

	addr, err := server.waitAddr(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	server.pid = addr.PID
	server.url = "http://" + addr.Addr

	return server
}

// waitForTempDir waits until dev has made its temporary directory in tmp.
func waitForTempDir(t *testing.T, tmp string, server *devServer) string {
	t.Helper()

	deadline := time.Now().Add(devReadyLimit)

	for time.Now().Before(deadline) {
		if entries, err := os.ReadDir(tmp); err == nil && len(entries) == 1 {
			return filepath.Join(tmp, entries[0].Name())
		}

		time.Sleep(devPollInterval) //nolint:forbidigo // A bounded poll for a directory that another process makes.
	}

	t.Fatalf("dev made no temporary directory:\n%s", server.out.String())

	return ""
}

// devKindRefusals proves the marker of the installation kind through the real
// binary: serve refuses the database of dev, and dev refuses a database that
// serve made.
func devKindRefusals(t *testing.T, bin string) {
	t.Helper()

	server := startPlainDev(t, bin)
	if err := server.stop(t); err != nil {
		t.Fatalf("stop dev: %v", err)
	}

	database := "sqlite:" + filepath.Join(server.dir, "dev.db")

	out, err := runServe(t, bin, database, filepath.Join(t.TempDir(), "serve.key"))
	if err == nil ||
		!strings.Contains(
			out,
			"opendustpan dev made this database: serve refuses it. Set --database to a new database.",
		) {
		t.Fatalf("serve on the database of dev: err = %v, output:\n%s", err, out)
	}

	servePath := serveMadeDatabase(t, bin)

	if copyErr := replaceDatabase(server.dir, servePath); copyErr != nil {
		t.Fatalf("put the database of serve in the data directory of dev: %v", copyErr)
	}

	args := []string{"--data-dir", server.dir, "--listen", "127.0.0.1:0"}

	out, err = runBinary(t, bin, append([]string{"dev"}, args...)...)
	if err == nil ||
		!strings.Contains(out, "opendustpan serve made this database: dev refuses it. Set a new --data-dir.") {
		t.Fatalf("dev on the database of serve: err = %v, output:\n%s", err, out)
	}
}

// replaceDatabase replaces the database file of a data directory of dev with
// the file at source, and removes the files that SQLite keeps next to it.
func replaceDatabase(dir, source string) error {
	raw, err := os.ReadFile(source) //nolint:gosec // The test reads the database that serve made.
	if err != nil {
		return fmt.Errorf("read the database of serve: %w", err)
	}

	for _, suffix := range []string{"-wal", "-shm", "-lock"} {
		if rmErr := os.Remove(filepath.Join(dir, "dev.db"+suffix)); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return fmt.Errorf("remove dev.db%s: %w", suffix, rmErr)
		}
	}

	//nolint:gosec // The test writes in the data directory that dev made.
	if writeErr := os.WriteFile(filepath.Join(dir, "dev.db"), raw, 0o600); writeErr != nil {
		return fmt.Errorf("write dev.db: %w", writeErr)
	}

	return nil
}

// serveToken is the administrator token of the serve commands of this test.
// It holds more than 32 bytes, as serve requires.
const serveToken = "0123456789abcdef0123456789abcdef-serve-token"

// runBinary runs the binary with args and answers its output and its error.
// The process has no DUSTPAN_ variable but the ones that the test sets.
func runBinary(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), devCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // The test runs the binary that it built.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "DUSTPAN_ADMIN_TOKEN=" + serveToken}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("opendustpan %s: %w", strings.Join(args, " "), err)
	}

	return string(out), nil
}

// runServe runs serve on the database with a key file that serve makes, and
// answers its output and its error. It is meant for a start that stops.
func runServe(t *testing.T, bin, database, keyFile string) (string, error) {
	t.Helper()

	return runBinary(t, bin, "serve", "--database", database, "--key-encryption-key-file", keyFile,
		"--generate-key-encryption-key", "--listen", "127.0.0.1:0")
}

// serveMadeDatabase runs serve on a new database until it is ready, stops it
// with SIGTERM and answers the path of the database file.
func serveMadeDatabase(t *testing.T, bin string) string {
	t.Helper()

	dir := t.TempDir()
	database := filepath.Join(dir, "serve.db")
	addr := freeLoopbackAddr(t)

	//nolint:gosec // The test runs the binary that it built.
	cmd := exec.CommandContext(
		context.WithoutCancel(t.Context()),
		bin,
		"serve",
		"--database",
		"sqlite:"+database,
		"--key-encryption-key-file",
		filepath.Join(dir, "serve.key"),
		"--generate-key-encryption-key",
		"--listen",
		addr,
		"--blob-dir",
		filepath.Join(dir, "blobs"),
	)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "DUSTPAN_ADMIN_TOKEN=" + serveToken}

	var out ptyBuffer

	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = devStopLimit

	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}

	waitReady(t, "http://"+addr, &out)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("stop serve: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("serve did not stop clean: %v\n%s", err, out.String())
	}

	return database
}

// freeLoopbackAddr answers a loopback address with a port that no process
// listens on now.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()

	//nolint:exhaustruct_v5 // The test listens with defaults.
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	addr := listener.Addr().String()

	if closeErr := listener.Close(); closeErr != nil {
		t.Fatalf("close listener: %v", closeErr)
	}

	return addr
}

// waitReady waits until the server at base answers /health/ready with 200.
func waitReady(t *testing.T, base string, out *ptyBuffer) {
	t.Helper()

	deadline := time.Now().Add(devReadyLimit)

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/health/ready", http.NoBody)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		if resp, doErr := http.DefaultClient.Do(req); doErr == nil {
			status := resp.StatusCode

			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Fatalf("close body: %v", closeErr)
			}

			if status == http.StatusOK {
				return
			}
		}

		time.Sleep(devPollInterval) //nolint:forbidigo // A bounded poll for a server that another process starts.
	}

	t.Fatalf("the server at %s was not ready:\n%s", base, out.String())
}

// waitBanner waits until the output of dev holds want. The pseudo terminal
// copies the output on its own goroutine, so the output can lag behind the
// address file that the test waits for first.
func (d *devServer) waitBanner(t *testing.T, want string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()

	for !strings.Contains(d.out.String(), want) {
		select {
		case <-ctx.Done():
			return d.out.String()
		case <-tick.C:
		}
	}

	return d.out.String()
}
