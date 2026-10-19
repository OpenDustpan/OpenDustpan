package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/installation"
)

// errDevTestTimeout reports a dev command that neither started nor ended in
// the time that a test allows.
var errDevTestTimeout = errors.New("dev did not start in time")

// syncBuffer is a buffer that the server and the test write and read at the
// same time.
type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func newSyncBuffer() *syncBuffer {
	return &syncBuffer{buf: bytes.Buffer{}, mu: sync.Mutex{}}
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // A transparent wrapper: callers see the original error.
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// devTestProcess holds the environment and the terminal of a test run of dev.
type devTestProcess struct {
	env      map[string]string
	unset    []string
	mu       sync.Mutex
	terminal bool
}

func newDevTestProcess(terminal bool) *devTestProcess {
	return &devTestProcess{env: map[string]string{}, unset: nil, mu: sync.Mutex{}, terminal: terminal}
}

// testRoots is the text of the roots that the bundle of a test starts with.
const testRoots = "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"

func (p *devTestProcess) process(home string, started chan<- string) devProcess {
	return devProcess{
		lookup: func(key string) (string, bool) {
			p.mu.Lock()
			defer p.mu.Unlock()

			v, ok := p.env[key]

			return v, ok
		},
		unsetenv: func(key string) error {
			p.mu.Lock()
			defer p.mu.Unlock()

			p.unset = append(p.unset, key)
			delete(p.env, key)

			return nil
		},
		hostname: func() (string, error) { return "testhost", nil },
		now:      time.Now,
		dirs: func(flagDir, envParent string) devDirInput {
			return devDirInput{
				getenv: func(string) string { return "" }, homeDir: func() (string, error) { return home, nil },
				flagDir: flagDir, envParent: envParent, goos: "linux",
			}
		},
		started:      func(url string) { started <- url },
		roots:        func(func(string) (string, bool)) []byte { return []byte(testRoots) },
		processStart: processStartTime,
		goos:         "linux",
		terminal:     p.terminal,
	}
}

// unsetNames answers the variables that dev cleared.
func (p *devTestProcess) unsetNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.unset)
}

// runningDev is a dev command that serves in the background.
type runningDev struct {
	cancel context.CancelFunc
	done   chan error
	stderr *syncBuffer
	err    error
	url    string
	once   sync.Once
}

// stop ends dev as a signal does and answers its result. A second call
// answers the same result.
func (r *runningDev) stop(t *testing.T) error {
	t.Helper()

	r.once.Do(func() {
		r.cancel()

		select {
		case r.err = <-r.done:
		case <-time.After(30 * time.Second):
			t.Error("dev did not stop")
		}
	})

	return r.err
}

// startDev starts dev in the background and waits until it is about to
// serve. It answers the error of a run that ended before that.
func startDev(t *testing.T, proc *devTestProcess, home string, args ...string) (*runningDev, error) {
	t.Helper()

	started := make(chan string, 1)
	stderr := newSyncBuffer()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		done <- devWithProcess(ctx, args, stderr, proc.process(home, started))
	}()

	select {
	case url := <-started:
		return &runningDev{
			cancel: cancel, done: done, stderr: stderr, err: nil, url: url, once: sync.Once{},
		}, nil
	case err := <-done:
		cancel()

		return nil, err
	case <-time.After(60 * time.Second):
		cancel()

		return nil, errDevTestTimeout
	}
}

// mustStartDev starts dev and fails the test when it does not start. The
// test stops it at its end.
func mustStartDev(t *testing.T, proc *devTestProcess, home string, args ...string) *runningDev {
	t.Helper()

	r, err := startDev(t, proc, home, args...)
	if err != nil {
		t.Fatalf("start dev: %v", err)
	}

	t.Cleanup(func() {
		if stopErr := r.stop(t); stopErr != nil {
			t.Errorf("stop dev: %v", stopErr)
		}
	})

	return r
}

// devGet sends a GET request with an optional bearer token.
func devGet(t *testing.T, client *http.Client, url, token string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
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

	return resp.StatusCode, string(body)
}

// devPost sends the one-time code to the dev sign-in.
func devPost(t *testing.T, url, code string) (int, []*http.Cookie) {
	t.Helper()

	body := strings.NewReader(`{"code":"` + code + `"}`)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+"/api/dustpan/v1/sign-in/dev-code", body)
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

var codePattern = regexp.MustCompile(`#code=([A-Za-z0-9_-]+)`)

// consoleDir makes a directory with a console page, so dev has a console.
func consoleDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatalf("write console page: %v", err)
	}

	return dir
}

func readToken(t *testing.T, dir string) string {
	t.Helper()

	return strings.TrimSpace(string(readFileOf(t, filepath.Join(dir, devTokenName))))
}

// trialArgs are the flags of a test run with the data directory dir, a
// console and a port that the system chooses.
func trialArgs(t *testing.T, dir string) []string {
	t.Helper()

	return []string{
		"--data-dir",
		dir,
		"--listen",
		"127.0.0.1:0",
		"--console-dir",
		consoleDir(t),
		"--log-format",
		"text",
	}
}

// TestDevFirstStartPrintsALinkThatWorksOnce proves the banner of the first
// start of a data directory holds the sign-in link, never the token, that the
// code of the link signs a browser in one time, and that the banner is the
// first text on stderr.
func TestDevFirstStartPrintsALinkThatWorksOnce(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	r := mustStartDev(t, newDevTestProcess(true), t.TempDir(), trialArgs(t, dir)...)
	banner := r.stderr.String()

	match := codePattern.FindStringSubmatch(banner)
	if match == nil {
		t.Fatalf("the first start prints no sign-in link:\n%s", banner)
	}

	wantLine := "Sign in:  " + strings.Replace(r.url, "127.0.0.1", "localhost", 1) + "/sign-in#code=" + match[1] +
		"   (first start on a terminal only, works one time, for 10 minutes)"
	if !strings.Contains(banner, wantLine) {
		t.Fatalf("banner does not hold the sign-in line %q:\n%s", wantLine, banner)
	}

	if strings.Contains(banner, readToken(t, dir)) {
		t.Fatal("the banner holds the administrator token")
	}

	if !strings.HasPrefix(banner, "opendustpan dev: this server is for a trial or for CI.") {
		t.Fatalf("the banner is not the first text on stderr:\n%s", banner)
	}

	status, cookies := devPost(t, r.url, match[1])
	if status != http.StatusNoContent || len(cookies) != 1 {
		t.Fatalf("the code: status = %d, cookies = %d, want 204 and the session cookie", status, len(cookies))
	}

	if again, _ := devPost(t, r.url, match[1]); again != http.StatusForbidden {
		t.Fatalf("the code a second time: status = %d, want 403", again)
	}
}

// TestDevFirstStartMakesTheInstallation proves what a first start makes: the
// files with their modes, the namespace dev, the installation kind in the API,
// and dev.addr while it runs and not after.
func TestDevFirstStartMakesTheInstallation(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	r := mustStartDev(t, newDevTestProcess(false), t.TempDir(), trialArgs(t, dir)...)
	token := readToken(t, dir)

	status, body := devGet(t, http.DefaultClient, r.url+"/api/server/namespaces", token)
	if status != http.StatusOK || !strings.Contains(body, `"dev"`) {
		t.Fatalf("namespaces = %d %s, want 200 with the namespace dev", status, body)
	}

	status, body = devGet(t, http.DefaultClient, r.url+"/api/dustpan/v1/capabilities", token)
	if status != http.StatusOK || !strings.Contains(body, `"installationKind":"dev"`) {
		t.Fatalf("capabilities with the token = %d %s, want the kind dev", status, body)
	}

	status, body = devGet(t, http.DefaultClient, r.url+"/api/dustpan/v1/capabilities", "")
	if status != http.StatusOK || strings.Contains(body, "installationKind") {
		t.Fatalf("capabilities with no sign-in = %d %s, want no kind", status, body)
	}

	checkDevFileModes(t, dir)

	if _, err := os.Stat(filepath.Join(dir, devAddrName)); err != nil {
		t.Fatalf("dev.addr does not exist while dev runs: %v", err)
	}

	if err := r.stop(t); err != nil {
		t.Fatalf("stop: %v", err)
	}

	checkDevFileModes(t, dir)

	if _, err := os.Stat(filepath.Join(dir, devAddrName)); err == nil {
		t.Fatal("dev.addr stays after the stop")
	}
}

// checkDevFileModes proves the files in the data directory and their modes.
func checkDevFileModes(t *testing.T, dir string) {
	t.Helper()

	for _, name := range []string{devKeyName, devTokenName, devEnvName, devTFRCName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}

		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %04o, want 0600", name, info.Mode().Perm())
		}
	}

	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("data directory = %v, %v, want mode 0700", info, err)
	}
}

// TestDevLaterStartPrintsNoLink proves a later start of the data directory
// prints no link, mounts no route for the code, and keeps the token.
func TestDevLaterStartPrintsNoLink(t *testing.T) {
	t.Parallel()

	proc := newDevTestProcess(true)
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "trial")
	args := trialArgs(t, dir)

	first := mustStartDev(t, proc, home, args...)

	match := codePattern.FindStringSubmatch(first.stderr.String())
	if match == nil {
		t.Fatalf("the first start prints no link:\n%s", first.stderr.String())
	}

	token := readToken(t, dir)

	if err := first.stop(t); err != nil {
		t.Fatalf("stop: %v", err)
	}

	second := mustStartDev(t, proc, home, args...)

	if strings.Contains(second.stderr.String(), "#code=") {
		t.Fatalf("a later start prints a link:\n%s", second.stderr.String())
	}

	if readToken(t, dir) != token {
		t.Fatal("a later start changed the token")
	}

	if status, _ := devPost(t, second.url, match[1]); status != http.StatusNotFound {
		t.Fatalf("the dev sign-in of a later start: status = %d, want 404", status)
	}
}

// TestDevSignInLinkNeedsTerminalAndConsole proves the banner prints no link,
// and the server mounts no route for the code, without a terminal, and that it
// names --console-dir without a console.
func TestDevSignInLinkNeedsTerminalAndConsole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal bool
		console  bool
		// want is text that the banner holds.
		want string
	}{
		{name: "no terminal", terminal: false, console: true, want: "Token:    in the file "},
		{name: "no console", terminal: true, console: false, want: "--console-dir"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "trial")
			args := []string{"--data-dir", dir, "--listen", "127.0.0.1:0"}

			if tt.console {
				args = append(args, "--console-dir", consoleDir(t))
			}

			r := mustStartDev(t, newDevTestProcess(tt.terminal), t.TempDir(), args...)

			banner := r.stderr.String()
			if strings.Contains(banner, "#code=") || !strings.Contains(banner, tt.want) {
				t.Fatalf("banner = %s, want no link and %q", banner, tt.want)
			}

			if status, _ := devPost(t, r.url, "anything"); status != http.StatusNotFound {
				t.Fatalf("dev sign-in with no link: status = %d, want 404", status)
			}
		})
	}
}

// TestDevEphemeralLeavesNoFile proves --ephemeral writes only to a temporary
// directory, says so, and leaves no file after a stop.
func TestDevEphemeralLeavesNoFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	proc := newDevTestProcess(true)
	proc.env[cli.DevDataDirEnv] = "/data-that-dev-must-ignore"

	r := mustStartDev(t, proc, t.TempDir(), "--ephemeral", "--listen", "127.0.0.1:0", "--log-format", "text")

	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the temporary directory holds %v, %v, want one directory", entries, err)
	}

	dir := filepath.Join(tmp, entries[0].Name())
	banner := r.stderr.String()

	checkText(t, banner, []string{
		"Data:     " + dir + " (dev deletes this directory at stop",
		"level=WARN", "deletes it at stop", "--ephemeral ignores the variable of the data directory",
	}, nil)

	if _, statErr := os.Stat("/data-that-dev-must-ignore"); statErr == nil {
		t.Fatal("dev wrote to the directory of the variable")
	}

	if stopErr := r.stop(t); stopErr != nil {
		t.Fatalf("stop: %v", stopErr)
	}

	if left, readErr := os.ReadDir(tmp); readErr != nil || len(left) != 0 {
		t.Fatalf("after the stop, the temporary directory holds %v, %v, want nothing", left, readErr)
	}

	checkText(t, r.stderr.String(), []string{"dev deletes its temporary data directory"}, nil)
}

// TestDevEphemeralLeavesNoFileAfterAFailedStart proves a start that fails
// after the directory exists still deletes it.
func TestDevEphemeralLeavesNoFileAfterAFailedStart(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	proc := newDevTestProcess(false)
	// A token of 3 bytes fails the token check after the directory exists.
	proc.env[cli.DevAdminTokenEnv] = "abc"

	if _, err := startDev(t, proc, t.TempDir(), "--ephemeral", "--listen", "127.0.0.1:0"); err == nil {
		t.Fatal("dev started with a token that is too short")
	}

	if left, readErr := os.ReadDir(tmp); readErr != nil || len(left) != 0 {
		t.Fatalf("after the failed start, the temporary directory holds %v, %v, want nothing", left, readErr)
	}
}

const (
	testProductionToken = "0123456789abcdef0123456789abcdef-production"
	testDevJobToken     = "0123456789abcdef0123456789abcdef-dev-job"
)

// TestDevReadsNoVariableOfServe proves that dev clears the secret variables
// of serve and never listens or authenticates with what they set.
func TestDevReadsNoVariableOfServe(t *testing.T) {
	t.Parallel()

	proc := newDevTestProcess(false)
	proc.env["DUSTPAN_ADMIN_TOKEN"] = testProductionToken
	proc.env["DUSTPAN_ADMIN_TOKEN_FILE"] = "/nonexistent/tokens"
	proc.env["DUSTPAN_KEY_ENCRYPTION_KEY"] = "AAAA"
	proc.env["DUSTPAN_DATABASE"] = "postgres://db.invalid/prod"
	proc.env["DUSTPAN_LISTEN"] = "0.0.0.0:80"
	proc.env["DUSTPAN_LOG_FORMAT"] = "not-a-format"
	proc.env[cli.DevAdminTokenEnv] = testDevJobToken

	dir := filepath.Join(t.TempDir(), "trial")

	r := mustStartDev(t, proc, t.TempDir(), "--data-dir", dir, "--listen", "127.0.0.1:0")

	cleared := proc.unsetNames()

	for _, name := range []string{
		"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE", "DUSTPAN_KEY_ENCRYPTION_KEY", "DUSTPAN_DATABASE",
		cli.DevAdminTokenEnv,
	} {
		if !slices.Contains(cleared, name) {
			t.Errorf("dev did not clear %s", name)
		}
	}

	if !strings.HasPrefix(r.url, "http://127.0.0.1:") || strings.Contains(r.url, ":80/") {
		t.Fatalf("dev listens on %s, so it read DUSTPAN_LISTEN", r.url)
	}

	namespacesURL := r.url + "/api/server/namespaces"

	status, _ := devGet(t, http.DefaultClient, namespacesURL, testProductionToken)
	if status != http.StatusUnauthorized {
		t.Fatalf("the production token: status = %d, want 401", status)
	}

	status, _ = devGet(t, http.DefaultClient, namespacesURL, testDevJobToken)
	if status != http.StatusOK {
		t.Fatalf("the token of the variable: status = %d, want 200", status)
	}

	checkTokenOfTheVariable(t, dir)
}

// checkTokenOfTheVariable proves the files of a start with the token in
// DUSTPAN_DEV_ADMIN_TOKEN: no token file, a dev.env that reads the variable
// and holds no token, and a dev.tfrc that holds the token.
func checkTokenOfTheVariable(t *testing.T, dir string) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(dir, devTokenName)); err == nil {
		t.Fatal("dev made a token file although the variable gave the token")
	}

	env := string(readFileOf(t, filepath.Join(dir, devEnvName)))
	if strings.Contains(env, testDevJobToken) || !strings.Contains(env, "${DUSTPAN_DEV_ADMIN_TOKEN:?}") {
		t.Fatalf("dev.env holds the token of the variable, or does not read it from the variable:\n%s", env)
	}

	if tfrc := string(readFileOf(t, filepath.Join(dir, devTFRCName))); !strings.Contains(tfrc, testDevJobToken) {
		t.Fatalf("dev.tfrc = %q, want the token in a credentials block", tfrc)
	}
}

// TestDevRefusesAForeignDirectory proves dev refuses a directory with files
// and no key file, and opens no file in it.
func TestDevRefusesAForeignDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dustpan.db"), 0o600)

	_, err := startDev(t, newDevTestProcess(false), t.TempDir(), "--data-dir", dir, "--listen", "127.0.0.1:0")

	want := dir + ": the directory holds files but no key file of opendustpan dev: dev refuses it. Set a new --data-dir."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("the directory holds %v, %v, want the file that was there", entries, readErr)
	}
}

// TestDevRefusesTheDatabaseOfServe proves dev refuses a database that serve
// made, with the decided text, and that the error wraps ErrKindMismatch.
func TestDevRefusesTheDatabaseOfServe(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	if _, err := claimDevDir(dir); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if _, err := makeDevKey(filepath.Join(dir, devKeyName)); err != nil {
		t.Fatalf("make key: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)

	st, err := openStore(
		t.Context(),
		"sqlite:"+filepath.Join(dir, devDBName),
		logger,
		openMigrate,
		installation.KindProduction,
	)
	if err != nil {
		t.Fatalf("open as serve: %v", err)
	}

	if _, claimErr := st.ClaimKind(t.Context(), installation.KindProduction); claimErr != nil {
		t.Fatalf("claim: %v", claimErr)
	}

	st.close()

	_, startErr := startDev(t, newDevTestProcess(false), t.TempDir(), "--data-dir", dir, "--listen", "127.0.0.1:0")
	if startErr == nil ||
		startErr.Error() != "opendustpan serve made this database: dev refuses it. Set a new --data-dir." {
		t.Fatalf("err = %v", startErr)
	}

	if !errors.Is(startErr, installation.ErrKindMismatch) {
		t.Fatalf("err = %v, want it to wrap ErrKindMismatch", startErr)
	}
}

// TestServeRefusesTheDatabaseOfDev proves the reverse: after a start of dev,
// the open of serve refuses the database with the decided text.
func TestServeRefusesTheDatabaseOfDev(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")

	r := mustStartDev(t, newDevTestProcess(false), t.TempDir(), "--data-dir", dir, "--listen", "127.0.0.1:0")
	if err := r.stop(t); err != nil {
		t.Fatalf("stop: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)

	_, err := openStore(
		t.Context(),
		"sqlite:"+filepath.Join(dir, devDBName),
		logger,
		openMigrate,
		installation.KindProduction,
	)
	refused := kindRefusal(err, commandServe)

	if refused == nil ||
		refused.Error() != "opendustpan dev made this database: serve refuses it. Set --database to a new database." {
		t.Fatalf("serve opens the database of dev: err = %v", refused)
	}
}

// TestDevTLS proves --tls: HTTPS with a self-signed leaf that holds the names,
// a certificate bundle for SSL_CERT_FILE and the line in the banner.
func TestDevTLS(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	r := mustStartDev(t, newDevTestProcess(false), t.TempDir(),
		"--data-dir", dir, "--listen", "127.0.0.1:0", "--tls", "--hostname", "dustpan")

	if !strings.HasPrefix(r.url, "https://127.0.0.1:") {
		t.Fatalf("url = %s, want https", r.url)
	}

	leaf := parseLeaf(t, filepath.Join(dir, devCertName))
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}
	defer client.CloseIdleConnections()

	status, body := devGet(t, client, r.url+"/api/dustpan/v1/capabilities", readToken(t, dir))
	if status != http.StatusOK || !strings.Contains(body, `"installationKind":"dev"`) {
		t.Fatalf("capabilities over TLS = %d %s", status, body)
	}

	for _, want := range []string{"dustpan", "testhost", "localhost"} {
		if err := leaf.VerifyHostname(want); err != nil {
			t.Errorf("the leaf does not hold %s: %v", want, err)
		}
	}

	checkTLSFiles(t, dir, leaf, r.stderr.String())
}

// checkTLSFiles proves the files and the banner of a start with --tls.
func checkTLSFiles(t *testing.T, dir string, leaf *x509.Certificate, banner string) {
	t.Helper()

	env := string(readFileOf(t, filepath.Join(dir, devEnvName)))
	if !strings.Contains(env, "export SSL_CERT_FILE="+shellQuote(filepath.Join(dir, devBundleName))) ||
		!strings.Contains(env, "unset SSL_CERT_DIR") {
		t.Fatalf("dev.env = %q, want SSL_CERT_FILE and the clear of SSL_CERT_DIR", env)
	}

	if bundle := string(readFileOf(t, filepath.Join(dir, devBundleName))); !strings.HasPrefix(bundle, testRoots) {
		t.Fatalf("bundle = %q, want the roots first", bundle)
	}

	checkText(t, banner, []string{
		"TLS:      self-signed certificate, valid until " + leaf.NotAfter.UTC().Format(time.DateOnly),
	}, nil)

	addr := string(readFileOf(t, filepath.Join(dir, devAddrName)))
	if !strings.Contains(addr, filepath.Join(dir, devCertName)) {
		t.Fatalf("dev.addr = %q, want the path of the certificate", addr)
	}
}

// TestDevNetworkLine proves the line for an address that is not loopback.
func TestDevNetworkLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tls       bool
		wantPlain bool
	}{
		{name: "plain HTTP", tls: false, wantPlain: true},
		{name: "TLS", tls: true, wantPlain: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := []string{
				"--data-dir",
				filepath.Join(t.TempDir(), "d"),
				"--log-format",
				"text",
				"--listen",
				"0.0.0.0:0",
			}
			if tt.tls {
				args = append(args, "--tls")
			}

			r := mustStartDev(t, newDevTestProcess(false), t.TempDir(), args...)

			banner := r.stderr.String()
			checkText(t, banner, []string{"Network:  ", "is not a loopback address", "level=WARN"}, nil)

			if plain := strings.Contains(banner, "crosses the network in plain text"); plain != tt.wantPlain {
				t.Fatalf("plain text warning = %v, want %v:\n%s", plain, tt.wantPlain, banner)
			}

			if strings.Contains(r.url, "0.0.0.0") {
				t.Fatalf("the URL %s names the wildcard address", r.url)
			}
		})
	}
}

func TestDevRefusesBadFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "ephemeral and data-dir",
			args: []string{"--ephemeral", "--data-dir", "/x"},
			want: "--ephemeral and --data-dir exclude each other",
		},
		{name: "a log level that is not known", args: []string{"--log-level", "loud"}, want: "log level"},
		{name: "a log format that is not known", args: []string{"--log-format", "xml"}, want: "log format"},
		{name: "a negative size limit", args: []string{"--max-state-bytes", "-1"}, want: "size limits"},
		{
			name: "a listen address with no port",
			args: []string{"--listen", "localhost"},
			want: "needs a host and a port",
		},
		{name: "an argument", args: []string{"extra"}, want: "dev takes no argument"},
		{name: "a flag of serve", args: []string{"--database", "sqlite:x.db"}, want: "flag provided but not defined"},
		{
			name: "the key flag of serve",
			args: []string{"--key-encryption-key-file", "k"},
			want: "flag provided but not defined",
		},
		{name: "the config flag of serve", args: []string{"--config", "c.yaml"}, want: "flag provided but not defined"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proc := newDevTestProcess(false).process(t.TempDir(), make(chan string, 1))

			err := devWithProcess(t.Context(), tt.args, io.Discard, proc)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to hold %q", err, tt.want)
			}
		})
	}
}

// TestDevSignInLinkNeedsLoopbackOrTLS proves the rule of the Secure cookie:
// on plain HTTP the link works only from a loopback origin, so dev on an
// address that is not a loopback address makes no code and no link, and says
// to set --tls. With --tls, dev makes the link for any address.
func TestDevSignInLinkNeedsLoopbackOrTLS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		listen   string
		tls      bool
		wantLink bool
	}{
		{name: "loopback address, plain HTTP", listen: "127.0.0.1:0", tls: false, wantLink: true},
		{name: "wildcard address, plain HTTP", listen: "0.0.0.0:0", tls: false, wantLink: false},
		{name: "wildcard address, TLS", listen: "0.0.0.0:0", tls: true, wantLink: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := []string{
				"--data-dir", filepath.Join(t.TempDir(), "trial"), "--listen", tt.listen,
				"--console-dir", consoleDir(t), "--log-format", "text",
			}
			if tt.tls {
				args = append(args, "--tls")
			}

			r := mustStartDev(t, newDevTestProcess(true), t.TempDir(), args...)
			banner := r.stderr.String()
			match := codePattern.FindStringSubmatch(banner)

			if (match != nil) != tt.wantLink {
				t.Fatalf("link printed = %v, want %v:\n%s", match != nil, tt.wantLink, banner)
			}

			switch {
			case !tt.wantLink:
				checkText(t, banner, []string{"set --tls", "administrator token in the console"}, []string{"#code="})

				if status, _ := devPost(t, r.url, "anything"); status != http.StatusNotFound {
					t.Fatalf("dev sign-in with no link: status = %d, want 404", status)
				}
			case tt.tls:
				checkText(t, banner, []string{"Sign in:  https://"}, nil)
			default:
				checkText(t, banner, []string{"Sign in:  http://localhost:"}, []string{"Sign in:  http://127.0.0.1"})
			}
		})
	}
}

// TestDevDeletesStaleAddrBeforeListening proves that dev deletes dev.addr of
// the data directory at start, before it listens: a start that fails to listen
// still leaves no file that tells `health` a server runs.
func TestDevDeletesStaleAddrBeforeListening(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	home := t.TempDir()
	proc := newDevTestProcess(false)

	first := mustStartDev(t, proc, home, "--data-dir", dir, "--listen", "127.0.0.1:0", "--log-format", "text")
	if err := first.stop(t); err != nil {
		t.Fatalf("stop: %v", err)
	}

	stale := filepath.Join(dir, devAddrName)
	if err := os.WriteFile(stale, []byte("{\"addr\":\"127.0.0.1:1\"}\n"), 0o600); err != nil {
		t.Fatalf("write a stale dev.addr: %v", err)
	}

	busy, err := (&net.ListenConfig{}).Listen( //nolint:exhaustruct_v5 // Defaults only.
		t.Context(), "tcp", "127.0.0.1:0",
	)
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}

	t.Cleanup(func() { _ = busy.Close() }) //nolint:errcheck // The test ends.

	if _, startErr := startDev(t, proc, home, "--data-dir", dir, "--listen", busy.Addr().String()); startErr == nil {
		t.Fatal("dev started on a port that is in use")
	}

	if _, statErr := os.Stat(stale); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("dev.addr after a start that fails to listen: stat err = %v, want not exist", statErr)
	}
}

// busyAddr holds a port until the test ends and answers its address, for a
// start that must fail to listen.
func busyAddr(t *testing.T) string {
	t.Helper()

	busy, err := (&net.ListenConfig{}).Listen( //nolint:exhaustruct_v5 // Defaults only.
		t.Context(), "tcp", "127.0.0.1:0",
	)
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}

	t.Cleanup(func() { _ = busy.Close() }) //nolint:errcheck // The test ends.

	return busy.Addr().String()
}

// TestDevSecondStartKeepsLiveAddr proves that a second `dev` on the data
// directory of a running server leaves the live dev.addr in place, also when
// the second start fails to listen.
func TestDevSecondStartKeepsLiveAddr(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	home := t.TempDir()
	proc := newDevTestProcess(false)

	_ = mustStartDev(t, proc, home, "--data-dir", dir, "--listen", "127.0.0.1:0", "--log-format", "text")

	before, err := os.ReadFile(filepath.Join(dir, devAddrName)) //nolint:gosec // A path of the test.
	if err != nil {
		t.Fatalf("read the live dev.addr: %v", err)
	}

	if _, startErr := startDev(t, proc, home, "--data-dir", dir, "--listen", busyAddr(t)); startErr == nil {
		t.Fatal("the second dev started on a port that is in use")
	}

	after, err := os.ReadFile(filepath.Join(dir, devAddrName)) //nolint:gosec // A path of the test.
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("dev.addr after the second start = %q, %v; want the live file %q", after, err, before)
	}
}

// TestDevKeepsAddrOfForeignDirectory proves that dev deletes no dev.addr in a
// directory that holds files and no key file of dev: the start refuses the
// directory and the file stays.
func TestDevKeepsAddrOfForeignDirectory(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "foreign")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("make the directory: %v", err)
	}

	addr := filepath.Join(dir, devAddrName)
	for path, text := range map[string]string{addr: "{\"addr\":\"127.0.0.1:1\"}\n", filepath.Join(dir, "notes.txt"): "mine"} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	_, err := startDev(t, newDevTestProcess(false), t.TempDir(), "--data-dir", dir, "--listen", "127.0.0.1:0")
	if !errors.Is(err, errDevForeign) {
		t.Fatalf("start in a foreign directory: err = %v, want errDevForeign", err)
	}

	if _, statErr := os.Stat(addr); statErr != nil {
		t.Fatalf("dev.addr of the foreign directory: %v, want it kept", statErr)
	}
}

// TestDevKeepsAddrOfOpenDirectory proves that dev deletes no dev.addr in a
// data directory that other users can open: the start refuses it and the file
// stays.
func TestDevKeepsAddrOfOpenDirectory(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows has no mode bits to check")
	}

	dir := filepath.Join(t.TempDir(), "trial")
	home := t.TempDir()
	proc := newDevTestProcess(false)

	first := mustStartDev(t, proc, home, "--data-dir", dir, "--listen", "127.0.0.1:0", "--log-format", "text")
	if err := first.stop(t); err != nil {
		t.Fatalf("stop: %v", err)
	}

	addr := filepath.Join(dir, devAddrName)
	if err := os.WriteFile(addr, []byte("{\"addr\":\"127.0.0.1:1\"}\n"), 0o600); err != nil {
		t.Fatalf("write dev.addr: %v", err)
	}

	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // The test makes the directory open on purpose.
		t.Fatalf("open the directory: %v", err)
	}

	if _, err := startDev(t, proc, home, "--data-dir", dir, "--listen", "127.0.0.1:0"); !errors.Is(err, errDevPrivate) {
		t.Fatalf("start in an open directory: err = %v, want errDevPrivate", err)
	}

	if _, err := os.Stat(addr); err != nil {
		t.Fatalf("dev.addr of the open directory: %v, want it kept", err)
	}
}

// TestDevDeletesAddrOfDeadProcess proves that dev deletes a dev.addr that
// names no running process: an ID that no process has, a file that does not
// parse, and (where the platform gives start times) the ID of a process that
// started after the server that wrote the file.
func TestDevDeletesAddrOfDeadProcess(t *testing.T) {
	t.Parallel()

	_, startErr := processStartTime(os.Getpid())
	hasStartTimes := startErr == nil

	tests := []struct {
		name string
		text string
		skip bool
	}{
		{
			name: "ID that no process has", skip: false,
			text: `{"started":"2026-10-19T09:00:00Z","addr":"127.0.0.1:1","pid":2147483646}`,
		},
		{name: "file that does not parse", text: "not json", skip: false},
		{name: "no ID", text: `{"addr":"127.0.0.1:1"}`, skip: false},
		{
			name: "ID of a later process", skip: !hasStartTimes,
			text: fmt.Sprintf(`{"started":"2001-01-01T00:00:00Z","addr":"127.0.0.1:1","pid":%d}`, os.Getpid()),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.skip {
				t.Skip("this platform gives no start time")
			}

			checkStaleAddrDeleted(t, tt.text)
		})
	}
}

// checkStaleAddrDeleted proves that a start in a dev directory with a
// dev.addr of the text deletes the file, also when the start fails to listen.
func checkStaleAddrDeleted(t *testing.T, text string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "trial")
	home := t.TempDir()
	proc := newDevTestProcess(false)

	first := mustStartDev(t, proc, home, "--data-dir", dir, "--listen", "127.0.0.1:0", "--log-format", "text")
	if err := first.stop(t); err != nil {
		t.Fatalf("stop: %v", err)
	}

	addr := filepath.Join(dir, devAddrName)
	if err := os.WriteFile(addr, []byte(text), 0o600); err != nil {
		t.Fatalf("write dev.addr: %v", err)
	}

	if _, err := startDev(t, proc, home, "--data-dir", dir, "--listen", busyAddr(t)); err == nil {
		t.Fatal("dev started on a port that is in use")
	}

	if _, err := os.Stat(addr); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dev.addr after the start: stat err = %v, want not exist", err)
	}
}
