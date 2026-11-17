package runnerproxysvc_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/runnerproxysvc"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/launcher/proxy"
	"opendustpan.org/opendustpan/pkg/runner"
)

const (
	testHost   = "api.example.test"
	serverHost = "server.example.test"
)

// serverOptions is what newServer needs. defaultOptions fills every field.
type serverOptions struct {
	rules      string
	operation  jobproxy.Operation
	isolation  runner.Isolation
	hasRules   bool
	serverHost string
	serverPort int
	egress     egress.Rules
	resolver   proxy.Resolver
	listen     string
	roots      *x509.CertPool
	lifetime   time.Duration
}

// defaultOptions returns the options of a proxied claim with no proxy rules.
func defaultOptions() serverOptions {
	return serverOptions{
		rules: "", operation: jobproxy.OperationPlan, isolation: runner.IsolationGVisor,
		hasRules: false, serverHost: serverHost, serverPort: jobproxy.DefaultPort,
		egress: egress.Rules{Allow: nil, LoopbackForTests: false}, resolver: nil,
		listen: "", roots: nil, lifetime: time.Hour,
	}
}

// testClaim returns a claim of a proxied gvisor sandbox with rules proxy
// rules.
func testClaim(id string, isolation runner.Isolation, rules int) runnerproxysvc.Claim {
	return runnerproxysvc.Claim{
		ID: id, Job: "job-1", Phase: "plan", Unit: "acme/web",
		Operation: jobproxy.OperationPlan, Isolation: isolation, Rules: rules,
	}
}

// testRecord returns a denied record with a host and a canonical path.
func testRecord() proxy.Record {
	return proxy.Record{
		Job: "job-1", Phase: "plan", Unit: "acme/web", Operation: jobproxy.OperationPlan,
		Mode: jobproxy.ModeInspect, Method: http.MethodGet, Host: testHost, Path: "/v1/team",
		RuleID: "api", Allowed: false, Reason: proxy.ReasonNoRuleMatched, Status: http.StatusForbidden,
		Bytes: 0, Time: time.Time{},
	}
}

// noLimits is the Limits value with every field at its default.
func noLimits() proxy.Limits {
	return proxy.Limits{
		HeaderTimeout: 0, HandshakeTimeout: 0, DialTimeout: 0, ResponseHeaderTimeout: 0,
		IdleTimeout: 0, MaxHeaderBytes: 0, MaxRequestBody: 0, MaxConnections: 0,
	}
}

// renderRules parses and renders a proxy rule document for the operation.
func renderRules(t *testing.T, doc string, operation jobproxy.Operation) jobproxy.Rendered {
	t.Helper()

	set, err := jobproxy.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse the proxy rules: %v", err)
	}

	rendered, err := jobproxy.Render(set, jobproxy.Vars{
		Tool: "pulumi", Namespace: "acme", Name: "web/prod", Labels: nil,
		Operation: operation, Inputs: nil, Profiles: nil,
	})
	if err != nil {
		t.Fatalf("render the proxy rules: %v", err)
	}

	return rendered
}

// inspectRules returns one inspect rule for testHost and the port.
func inspectRules(port int, methods ...string) string {
	quoted := make([]string, 0, len(methods))
	for _, method := range methods {
		quoted = append(quoted, `"`+method+`"`)
	}

	return fmt.Sprintf(
		`{"rules":[{"id":"api","operations":["plan"],"host":%q,"port":%d,"mode":"inspect",`+
			`"methods":[%s],"paths":["/v1/**"]}]}`,
		testHost, port, strings.Join(quoted, ","),
	)
}

// tunnelRules returns one tunnel rule for host and the port.
func tunnelRules(host string, port int) string {
	return fmt.Sprintf(
		`{"rules":[{"id":"t","operations":["plan"],"host":%q,"port":%d,"mode":"tunnel"}]}`, host, port,
	)
}

// newServer starts a job proxy on a loopback listener and returns the server
// and the job log buffer.
func newServer(t *testing.T, opts serverOptions) (*runnerproxysvc.Server, *bytes.Buffer) {
	t.Helper()

	var buf bytes.Buffer

	var rendered jobproxy.Rendered
	if opts.rules != "" {
		rendered = renderRules(t, opts.rules, opts.operation)
	}

	rules := 0
	if opts.hasRules {
		rules = 1
	}

	srv, err := runnerproxysvc.NewServer(runnerproxysvc.ServerConfig{
		Claim: runnerproxysvc.Claim{
			ID: "claim", Job: "job-1", Phase: "plan", Unit: "acme/web",
			Operation: opts.operation, Isolation: opts.isolation, Rules: rules,
		},
		Rendered: rendered, ServerHost: opts.serverHost, ServerPort: opts.serverPort,
		Egress: opts.egress, Records: runnerproxysvc.NewLogSink(&buf), Resolver: opts.resolver,
		SystemRoots: opts.roots, CALifetime: opts.lifetime, Limits: noLimits(), Listen: opts.listen,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() {
		if cerr := srv.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})

	return srv, &buf
}

// testResolver answers with the addresses of each host.
type testResolver struct {
	answers map[string][]netip.Addr
	calls   atomic.Int32
}

// LookupNetIP returns the answer of host.
func (r *testResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.calls.Add(1)

	return r.answers[host], nil
}

// connectResult is the answer to a CONNECT and the connection after it.
type connectResult struct {
	conn   net.Conn
	reader *bufio.Reader
	status int
}

// connectTo opens a connection to the proxy at addr and sends a CONNECT for
// target.
func connectTo(t *testing.T, addr, target string) connectResult {
	t.Helper()

	return connectRaw(t, addr, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
}

// connectRaw opens a connection to the proxy at addr and sends request.
func connectRaw(t *testing.T, addr, request string) connectResult {
	t.Helper()

	var dialer net.Dialer

	conn, err := dialer.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}

	if _, werr := io.WriteString(conn, request); werr != nil {
		t.Fatalf("send the CONNECT: %v", werr)
	}

	reader := bufio.NewReader(conn)

	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		closeConn(t, conn)

		t.Fatalf("read the CONNECT answer: %v", err)
	}

	if cerr := resp.Body.Close(); cerr != nil {
		t.Logf("close the CONNECT body: %v", cerr)
	}

	return connectResult{conn: conn, reader: reader, status: resp.StatusCode}
}

// bufferedConn reads first from the reader and then from the connection.
type bufferedConn struct {
	net.Conn

	reader *bufio.Reader
}

// Read reads the bytes that the reader holds first.
func (c *bufferedConn) Read(p []byte) (int, error) { return keep(c.reader.Read(p)) }

// keep returns its arguments, so a read error keeps its identity.
func keep(n int, err error) (int, error) { return n, err }

// closeConn closes conn and logs a close that fails on a live connection.
func closeConn(t *testing.T, conn net.Conn) {
	t.Helper()

	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Logf("close the connection: %v", err)
	}
}

// startTLS ends TLS with the proxy after a CONNECT, with the roots and the
// server name of the caller.
func startTLS(t *testing.T, result connectResult, name string, roots *x509.CertPool) (*tls.Conn, *bufio.Reader, error) {
	t.Helper()

	conn := tls.Client(&bufferedConn{Conn: result.conn, reader: result.reader}, &tls.Config{
		ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
	})

	if err := conn.HandshakeContext(t.Context()); err != nil {
		return nil, nil, fmt.Errorf("handshake: %w", err)
	}

	return conn, bufio.NewReader(conn), nil
}

// rootsFor returns the CA of the proxy as a client root pool.
func rootsFor(t *testing.T, srv *runnerproxysvc.Server) *x509.CertPool {
	t.Helper()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(srv.CACertificatePEM()) {
		t.Fatal("the CA certificate of the proxy is not valid PEM")
	}

	return pool
}

// rawReply is the status and the body of a raw request.
type rawReply struct {
	status int
	body   string
}

// rawRequest sends one raw request on conn and reads its answer.
func rawRequest(t *testing.T, conn net.Conn, reader *bufio.Reader, request string) rawReply {
	t.Helper()

	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("send the request: %v", err)
	}

	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read the answer: %v", err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}

	if cerr := resp.Body.Close(); cerr != nil {
		t.Logf("close the body: %v", cerr)
	}

	return rawReply{status: resp.StatusCode, body: string(body)}
}

// echoListener starts a TCP listener that echoes each byte and returns its
// port.
func echoListener(t *testing.T) int {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	t.Cleanup(func() {
		if cerr := ln.Close(); cerr != nil {
			t.Logf("close the listener: %v", cerr)
		}
	})

	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}

			go func() {
				defer closeConn(t, conn)

				if _, cerr := io.Copy(conn, conn); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
					return
				}
			}()
		}
	}()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("the listener address is not a TCP address")
	}

	return addr.Port
}
