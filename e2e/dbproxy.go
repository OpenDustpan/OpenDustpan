//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/store/postgres"
)

const (
	// proxyDialTimeout bounds the dial of one forwarded database connection.
	proxyDialTimeout = 5 * time.Second
	// postgresDefaultPort is used when the database URL names no port.
	postgresDefaultPort = "5432"
)

// DBProxy is a small TCP proxy between the server and PostgreSQL. The server
// connects through it, so pausing the proxy stops the database for that
// server only. The shared PostgreSQL server keeps running. Resuming the proxy
// lets the pool connect again. The listener stays bound from the start to the
// end of the test: the server's database URL names its port, and a port that
// the proxy released could be taken by another process (issue 391).
type DBProxy struct {
	mu       sync.Mutex
	listener net.Listener
	target   string
	port     string
	url      string
	conns    map[net.Conn]struct{}
	// paused makes the proxy close each accepted connection at once.
	paused bool
	// shut marks the end of the test: the listener is closed.
	shut bool
}

// URL returns the database URL that reaches PostgreSQL through the proxy.
func (p *DBProxy) URL() string {
	return p.url
}

// startDBProxy listens on loopback, forwards to the host and port of
// directURL, and returns the proxy with the URL that reaches the database
// through it. It registers a cleanup that shuts the proxy down.
func startDBProxy(ctx context.Context, tb testing.TB, directURL string) (*DBProxy, string) {
	tb.Helper()

	target := postgresTarget(tb, directURL)

	//nolint:exhaustruct_v5 // The probe listens with defaults; only the network and address matter.
	lc := &net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen database proxy: %v", err)
	}

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close() //nolint:errcheck // The listen failed; only the port is missing.
		tb.Fatalf("split proxy address: %v", err)
	}

	//nolint:exhaustruct_v5 // The mutex starts zero; only the proxy fields are set.
	proxy := &DBProxy{
		listener: listener,
		target:   target,
		port:     port,
		url:      proxiedPostgresURL(tb, directURL, port),
		conns:    map[net.Conn]struct{}{},
		paused:   false,
		shut:     false,
	}

	go proxy.serve(ctx, listener)

	tb.Cleanup(proxy.shutdown)

	return proxy, proxy.url
}

// openProxiedPostgresStore opens the PostgreSQL server at base in a fresh
// schema through a TCP proxy, so closing the proxy stops the database for
// this environment only. It returns the store that serves through the proxy
// with the proxy. The schema is dropped when the test ends.
func openProxiedPostgresStore(
	ctx context.Context,
	tb testing.TB,
	base string,
) (*postgres.Store, *DBProxy) {
	tb.Helper()

	if base == "" {
		tb.Fatalf("set %s to run the PostgreSQL suite", postgresURLEnv)
	}

	schema := randomSchema(tb)

	createSchema(ctx, tb, base, schema)

	directURI := schemaURL(tb, base, schema)
	proxy, proxiedURI := startDBProxy(ctx, tb, directURI)

	st, err := postgres.Open(ctx, proxiedURI)
	if err != nil {
		dropSchema(ctx, tb, base, schema)
		tb.Fatalf("open postgres store through proxy: %v", err)
	}

	tb.Cleanup(func() {
		st.CloseContext(ctx)
		dropSchema(context.WithoutCancel(ctx), tb, base, schema)
	})

	return st, proxy
}

// postgresTarget returns the host and port of directURL, with the default
// PostgreSQL port when the URL names none.
func postgresTarget(tb testing.TB, directURL string) string {
	tb.Helper()

	parsed, err := url.Parse(directURL)
	if err != nil {
		tb.Fatalf("parse database URL: %v", err)
	}

	host := parsed.Hostname()
	if host == "" {
		tb.Fatalf("database URL has no host: %q", directURL)
	}

	port := parsed.Port()
	if port == "" {
		port = postgresDefaultPort
	}

	return net.JoinHostPort(host, port)
}

// proxiedPostgresURL returns directURL with the host replaced by the proxy
// address on port. The user, password, database and query stay unchanged, so
// the schema (search_path) is the same.
func proxiedPostgresURL(tb testing.TB, directURL, port string) string {
	tb.Helper()

	parsed, err := url.Parse(directURL)
	if err != nil {
		tb.Fatalf("parse database URL: %v", err)
	}

	parsed.Host = net.JoinHostPort("127.0.0.1", port)

	return parsed.String()
}

// dialDatabase dials the database with ctx, bounded by the dial timeout.
func dialDatabase(ctx context.Context, target string) (net.Conn, error) {
	//nolint:exhaustruct_v5 // The dialer needs only a timeout; the rest stays zero.
	dialer := &net.Dialer{Timeout: proxyDialTimeout}

	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("dial database through proxy: %w", err)
	}

	return conn, nil
}

// Pause stops the database for the server: it drops every forwarded
// connection, so pooled connections break, and it closes each new connection
// at once. The listener stays bound, so no other process can take the port.
// It is idempotent.
func (p *DBProxy) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.paused = true
	p.dropConnsLocked()
}

// Resume forwards new connections again on the same listener, so the pool can
// connect. It is idempotent.
func (p *DBProxy) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.paused = false
}

// dropConnsLocked closes every forwarded connection. The caller holds p.mu.
func (p *DBProxy) dropConnsLocked() {
	for conn := range p.conns {
		_ = conn.Close() //nolint:errcheck // The connection is dropped; the result is not checked.
	}

	p.conns = map[net.Conn]struct{}{}
}

// serve accepts connections until the listener closes.
func (p *DBProxy) serve(ctx context.Context, listener net.Listener) {
	for {
		client, err := listener.Accept()
		if err != nil {
			return
		}

		p.mu.Lock()

		if p.paused || p.shut {
			p.mu.Unlock()
			_ = client.Close() //nolint:errcheck // The proxy is paused; the client cannot proceed.

			continue
		}

		p.conns[client] = struct{}{}
		p.mu.Unlock()

		go p.forward(ctx, client)
	}
}

// forward dials the database and copies both directions until one side ends.
// It removes both connections from the tracked set when done.
func (p *DBProxy) forward(ctx context.Context, client net.Conn) {
	defer func() {
		p.mu.Lock()
		delete(p.conns, client)
		p.mu.Unlock()
		_ = client.Close() //nolint:errcheck // The forwarding ends; the result is not checked.
	}()

	upstream, err := dialDatabase(ctx, p.target)
	if err != nil {
		return
	}

	p.mu.Lock()

	_, live := p.conns[client]
	if !live {
		// A pause dropped the client while the dial ran.
		p.mu.Unlock()
		_ = upstream.Close() //nolint:errcheck // The client is gone; the result is not checked.

		return
	}

	p.conns[upstream] = struct{}{}
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.conns, upstream)
		p.mu.Unlock()
		_ = upstream.Close() //nolint:errcheck // The forwarding ends; the result is not checked.
	}()

	var wg sync.WaitGroup

	wg.Go(func() {
		_, _ = io.Copy(upstream, client) //nolint:errcheck // A copy error only ends forwarding.
		_ = upstream.Close()             //nolint:errcheck // The copy ended; the result is not checked.
		_ = client.Close()               //nolint:errcheck // The copy ended; the result is not checked.
	})

	_, _ = io.Copy(client, upstream) //nolint:errcheck // A copy error only ends forwarding.
	_ = upstream.Close()             //nolint:errcheck // The copy ended; the result is not checked.
	_ = client.Close()               //nolint:errcheck // The copy ended; the result is not checked.

	wg.Wait()
}

// shutdown permanently closes the proxy at the end of the test.
func (p *DBProxy) shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.shut = true

	if p.listener != nil {
		_ = p.listener.Close() //nolint:errcheck // The test ends; the result is not checked.
		p.listener = nil
	}

	p.dropConnsLocked()
}

// StopDatabase pauses the database proxy, so the database stops for this
// environment only. The proxy keeps its port. It needs a PostgreSQL
// environment with a proxy.
func (e *Env) StopDatabase(tb testing.TB) {
	tb.Helper()

	if e.dbProxy == nil {
		tb.Fatalf("StopDatabase needs a PostgreSQL environment with a proxy")
	}

	e.dbProxy.Pause()
}

// StartDatabase resumes the database proxy on the same port, so the pool can
// connect again. It needs a PostgreSQL environment with a proxy.
func (e *Env) StartDatabase(tb testing.TB) {
	tb.Helper()

	if e.dbProxy == nil {
		tb.Fatalf("StartDatabase needs a PostgreSQL environment with a proxy")
	}

	e.dbProxy.Resume()
}

// AddStep records one custom scenario step for the report, so helpers
// outside this package can report checks such as the readiness probe.
func (e *Env) AddStep(step StepResult) {
	e.steps = append(e.steps, step)
}

// SkipStep records a skipped step with the reason, so a PostgreSQL-only step
// stays visible on SQLite runs.
func (e *Env) SkipStep(name, reason string) {
	e.steps = append(e.steps, StepResult{
		Name:     name,
		Command:  name,
		Failure:  reason,
		Stderr:   "",
		ExitCode: 0,
		Passed:   false,
		Skipped:  true,
	})
}
