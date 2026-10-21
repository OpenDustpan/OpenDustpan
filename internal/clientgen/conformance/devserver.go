package conformance

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// ErrServer means that the dev server did not start, or did not answer.
var ErrServer = errors.New("conformance: the dev server failed")

// Limits of the dev server process.
const (
	// devStartTimeout bounds the start of the server.
	devStartTimeout = 60 * time.Second
	// pollEvery is the time between two asks of a public route during the start.
	pollEvery = 50 * time.Millisecond
	// bannerBufferBytes is the start size of the buffer for a line of standard
	// error.
	bannerBufferBytes = 64 << 10
)

// descriptionPath is the route that serves the API description.
const descriptionPath = "/api/dustpan/v1/openapi.json"

// capabilitiesPath is a public route that answers when the server is ready.
const capabilitiesPath = "/api/dustpan/v1/capabilities"

// devBanner finds the URL of the server in the banner of the dev command.
var devBanner = regexp.MustCompile(`^Server:\s+(http://\S+)$`)

// devEnv returns the variables of the harness that the server gets. It gets no
// other variable, so a token or a database URL of CI stays out of it.
func devEnv() []string { return []string{"PATH", "HOME", "TMPDIR", "SYSTEMROOT"} }

// DevOptions are the settings of StartDevServer.
type DevOptions struct {
	// Binary is the opendustpan program.
	Binary string
	// DataDir is the parent of the data directory of the server. It should be
	// empty and private, because the server writes its key and its token there.
	DataDir string
}

// DevServer is an opendustpan dev process. It implements Server.
type DevServer struct {
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	stderr  *tailBuffer
	url     string
	token   string
	release string
	client  *http.Client
	waitErr error
}

// StartDevServer runs `opendustpan dev` on a free loopback port with an empty
// database, and waits until it answers. Close stops it.
func StartDevServer(ctx context.Context, opts DevOptions) (*DevServer, error) {
	release, err := releaseOf(ctx, opts)
	if err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithCancel(ctx)

	args := []string{"dev", "--listen", "127.0.0.1:0", "--log-level", "warn"}
	cmd := exec.CommandContext(runCtx, opts.Binary, args...) //nolint:gosec // G204: the caller names the program.
	cmd.Env = Environ(devEnv(), "DUSTPAN_DATA_DIR="+opts.DataDir)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGINT) }
	cmd.WaitDelay = closeTimeout

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()

		return nil, fmt.Errorf("server stderr: %w", err)
	}

	s := newDevServer(cmd, cancel, release)

	if err = cmd.Start(); err != nil {
		cancel()

		return nil, fmt.Errorf("start the server: %w", err)
	}

	found := make(chan string, 1)

	go s.watch(stderr, found)

	if err = s.waitReady(ctx, found, opts.DataDir); err != nil {
		return nil, errors.Join(err, s.Close())
	}

	return s, nil
}

// newDevServer returns a DevServer with its client, which connects to
// loopback addresses only.
func newDevServer(cmd *exec.Cmd, cancel context.CancelFunc, release string) *DevServer {
	s := new(DevServer)
	s.cmd, s.cancel, s.done, s.release = cmd, cancel, make(chan struct{}), release
	s.stderr = new(tailBuffer)
	s.stderr.max = stderrKeepBytes
	s.client = loopbackClient(DefaultCallTimeout)

	return s
}

// releaseOf asks the binary for its version.
func releaseOf(ctx context.Context, opts DevOptions) (string, error) {
	cmd := exec.CommandContext(ctx, opts.Binary, "--version") //nolint:gosec // G204: the caller names the program.
	cmd.Env = Environ(devEnv())

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ask the server for its version: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// URL implements Server.
func (s *DevServer) URL() string { return s.url }

// Release implements Server.
func (s *DevServer) Release() string { return s.release }

// Token implements Server. It is the token of the dev data directory.
func (s *DevServer) Token() string { return s.token }

// Description implements Server.
func (s *DevServer) Description(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+descriptionPath, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get the description: %w", err)
	}

	defer resp.Body.Close() //nolint:errcheck // The body is read below.

	doc, err := readLimited(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read the description: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: the description route answered %d", ErrServer, resp.StatusCode)
	}

	return doc, nil
}

// Close stops the server with an interrupt, and kills it after five seconds.
func (s *DevServer) Close() error {
	s.cancel()
	<-s.done

	s.client.CloseIdleConnections()

	// Close ends the context of the command, so Wait may return that error for
	// a server that stopped on the interrupt. A non-zero exit after the
	// interrupt is not an error of the harness either.
	var exit *exec.ExitError
	if s.waitErr != nil && !errors.As(s.waitErr, &exit) && !errors.Is(s.waitErr, context.Canceled) {
		return fmt.Errorf("stop the server: %w", s.waitErr)
	}

	return nil
}

// watch reads the standard error of the server. It sends the URL from the
// banner, and then keeps the rest for a report until the server exits.
func (s *DevServer) watch(stderr io.Reader, found chan<- string) {
	defer close(s.done)

	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, bannerBufferBytes), MaxMessageBytes)

	sent := false

	for sc.Scan() {
		line := sc.Text()
		s.stderr.keep(line)

		if m := devBanner.FindStringSubmatch(line); m != nil && !sent {
			found <- m[1]
			sent = true
		}
	}

	// The server may still write; drain the pipe so that it can exit.
	if _, err := io.Copy(io.Discard, stderr); err != nil {
		s.stderr.note(err)
	}

	s.waitErr = s.cmd.Wait()
}

// waitReady waits for the banner, reads the token, and polls a public route.
func (s *DevServer) waitReady(ctx context.Context, found <-chan string, dataDir string) error {
	ctx, cancel := context.WithTimeout(ctx, devStartTimeout)
	defer cancel()

	select {
	case u := <-found:
		if _, err := loopbackURL(u); err != nil {
			return err
		}

		s.url = u
	case <-s.done:
		return fmt.Errorf("%w: it ended before the banner: %w; standard error: %q",
			ErrServer, s.waitErr, s.stderr.String())
	case <-ctx.Done():
		return fmt.Errorf("%w: wait for the banner: %w; standard error: %q", ErrServer, ctx.Err(), s.stderr.String())
	}

	tokenFile := filepath.Join(dataDir, "dev", "dev.token")

	raw, err := os.ReadFile(tokenFile) //nolint:gosec // G304: the directory is the harness's.
	if err != nil {
		return fmt.Errorf("read the token file: %w", err)
	}

	s.token = strings.TrimSpace(string(raw))

	return s.poll(ctx)
}

// poll asks a public route until the server answers 200.
func (s *DevServer) poll(ctx context.Context) error {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+capabilitiesPath, http.NoBody)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}

		if s.answers(req) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: wait for the server to answer: %w", ErrServer, ctx.Err())
		case <-time.After(pollEvery):
		}
	}
}

// answers says that the server answers the request with 200.
func (s *DevServer) answers(req *http.Request) bool {
	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}

	defer resp.Body.Close() //nolint:errcheck // Only the status counts.

	if _, err = io.Copy(io.Discard, resp.Body); err != nil {
		return false
	}

	return resp.StatusCode == http.StatusOK
}
