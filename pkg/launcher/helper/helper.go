// Package helper is the client side of the credentials helpers of the
// launcher: the credentials helper of OpenTofu and the git credential
// helper. A tool starts the launcher binary under the name of a helper. The
// helper asks the launcher of the step over SocketHelper, with one line of
// JSON, and writes the answer in the form that the tool reads. It never
// holds a credential beyond one answer, and never reads one from a
// variable.
package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher"
)

var (
	// ErrUsage reports a helper call that the helper does not accept.
	ErrUsage = errors.New("helper call is not valid")
	// ErrNoLauncher reports a helper that cannot reach the launcher of the
	// step: the launcher answers only during a step of the role tool.
	ErrNoLauncher = errors.New("the launcher of the step does not answer")
)

// Kinds of a Request.
const (
	KindOpenTofu = "opentofu"
	KindGit      = "git"
)

// Request is the one line of JSON that a helper sends on the socket.
type Request struct {
	Kind string `json:"kind"`
	Host string `json:"host"`
}

// Answer is the one line of JSON that the launcher answers. An empty token
// means no credential for the host.
type Answer struct {
	Token string `json:"token,omitempty"`
}

// Limits of a helper call.
const (
	// MaxHost is the longest host, with its port.
	MaxHost = 261
	// MaxInput is the most bytes that the git helper reads from git.
	MaxInput = 64 << 10
	// MaxAnswer is the most bytes of an answer of the launcher.
	MaxAnswer = 16 << 10
	// Timeout is the longest wait for the launcher.
	Timeout = 30 * time.Second
	// gitUser is the user name of a token of a forge for git over HTTPS.
	gitUser = "x-access-token"
)

// Dialer opens the connection to the socket at path.
type Dialer func(ctx context.Context, path string) (net.Conn, error)

// UnixDialer is the Dialer of a Unix socket.
func UnixDialer(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer

	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("dial the helper socket: %w", err)
	}

	return conn, nil
}

// Call is one start of a helper.
type Call struct {
	Mode launcher.Mode
	// Args are the arguments after the program name.
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	// Socket is the path of SocketHelper, from launcher.EnvHelperSocket.
	Socket string
	Dial   Dialer
}

// Run runs one helper call. It returns an error that wraps ErrUsage for a
// call that it does not accept, and ErrNoLauncher when the launcher does
// not answer. The error never holds a token.
func Run(ctx context.Context, c *Call) error {
	if err := checkSocket(c.Socket); err != nil {
		return err
	}

	if c.Mode == launcher.ModeOpenTofuHelper {
		return openTofu(ctx, c)
	}

	if c.Mode == launcher.ModeGitHelper {
		return git(ctx, c)
	}

	return fmt.Errorf("%w: mode %q is not a helper", ErrUsage, c.Mode)
}

// checkSocket refuses a socket path that is not absolute or that does not
// name SocketHelper.
func checkSocket(p string) error {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || filepath.Base(p) != launcher.SocketHelper {
		return fmt.Errorf("%w: %s names no helper socket", ErrNoLauncher, launcher.EnvHelperSocket)
	}

	return nil
}

// openTofu answers "get <host>" of the credentials helper protocol of
// OpenTofu. It refuses "store" and "forget": the launcher keeps no
// credential that a tool gives.
func openTofu(ctx context.Context, c *Call) error {
	if len(c.Args) != 2 || c.Args[0] != "get" {
		return fmt.Errorf("%w: the OpenTofu helper takes only \"get <host>\"", ErrUsage)
	}

	host := c.Args[1]
	if !ValidHost(host) {
		return fmt.Errorf("%w: the host is not a host name", ErrUsage)
	}

	token, err := ask(ctx, c, Request{Kind: KindOpenTofu, Host: host})
	if err != nil {
		return err
	}

	out := []byte("{}\n")

	if token != "" {
		b, encErr := json.Marshal(map[string]string{"token": token})
		if encErr != nil {
			return fmt.Errorf("encode the answer of the helper: %w", encErr)
		}

		b = append(b, '\n')
		out = b
	}

	if _, err = c.Stdout.Write(out); err != nil {
		return fmt.Errorf("write the answer of the helper: %w", err)
	}

	return nil
}

// git answers "get" of the git credential protocol. It reads "store" and
// "erase" and does nothing: the launcher keeps no credential that git
// gives.
func git(ctx context.Context, c *Call) error {
	if len(c.Args) != 1 {
		return fmt.Errorf("%w: the git helper takes one operation", ErrUsage)
	}

	attrs, err := readAttrs(c.Stdin)
	if err != nil {
		return err
	}

	switch c.Args[0] {
	case "store", "erase":
		return nil
	case "get":
	default:
		return fmt.Errorf("%w: unknown git helper operation", ErrUsage)
	}

	if attrs["protocol"] != "https" {
		// No token leaves the launcher for a connection without TLS.
		return nil
	}

	host := attrs["host"]
	if !ValidHost(host) {
		return fmt.Errorf("%w: the host is not a host name", ErrUsage)
	}

	token, err := ask(ctx, c, Request{Kind: KindGit, Host: host})
	if err != nil || token == "" {
		return err
	}

	if _, err = fmt.Fprintf(c.Stdout, "username=%s\npassword=%s\n", gitUser, token); err != nil {
		return fmt.Errorf("write the answer of the helper: %w", err)
	}

	return nil
}

// readAttrs reads the "key=value" lines that git sends, up to a blank line
// or the end.
func readAttrs(r io.Reader) (map[string]string, error) {
	attrs := make(map[string]string)
	sc := bufio.NewScanner(io.LimitReader(r, MaxInput))

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}

		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%w: git sent a line that is not key=value", ErrUsage)
		}

		attrs[k] = v
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: read the input of git: %w", ErrUsage, err)
	}

	return attrs, nil
}

// ValidHost reports whether h is a host name or an IP address, with an
// optional port: 1 to MaxHost ASCII letters, digits, '.', '-', ':', '[' and
// ']'.
func ValidHost(h string) bool {
	if h == "" || len(h) > MaxHost {
		return false
	}

	return !strings.ContainsFunc(h, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
			!strings.ContainsRune(".-:[]", r)
	})
}

// ask sends req to the launcher and returns the token of its answer.
func ask(ctx context.Context, c *Call, req Request) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	conn, err := c.Dial(ctx, c.Socket)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoLauncher, err)
	}

	token, err := exchange(ctx, conn, req)
	if closeErr := conn.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("%w: close the connection: %w", ErrNoLauncher, closeErr)
	}

	if err != nil {
		return "", err
	}

	return token, nil
}

// exchange sends req on conn and reads the answer.
func exchange(ctx context.Context, conn net.Conn, req Request) (string, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return "", fmt.Errorf("%w: set the deadline: %w", ErrNoLauncher, err)
		}
	}

	line, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("encode the helper request: %w", err)
	}

	if _, err = conn.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("%w: send the request: %w", ErrNoLauncher, err)
	}

	raw, err := io.ReadAll(io.LimitReader(conn, MaxAnswer+1))
	if err != nil || len(raw) > MaxAnswer {
		return "", fmt.Errorf("%w: the answer is not readable", ErrNoLauncher)
	}

	var ans Answer
	if err = json.Unmarshal(raw, &ans); err != nil {
		return "", fmt.Errorf("%w: the answer is not valid", ErrNoLauncher)
	}

	if strings.ContainsAny(ans.Token, "\r\n\x00") {
		// Defense in depth: the launcher answers only tokens that it got
		// from the server; this check stays so that an answer never adds a
		// line to the output that a tool reads.
		return "", fmt.Errorf("%w: the answer holds a line break", ErrNoLauncher)
	}

	return ans.Token, nil
}
