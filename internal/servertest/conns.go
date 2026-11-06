package servertest

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
)

// connSet holds open connections, so a kill closes them all at once. After
// closeAll, add closes each new connection at once.
type connSet struct {
	conns  map[net.Conn]struct{}
	mu     sync.Mutex
	closed bool
}

func newConnSet() *connSet {
	return &connSet{conns: make(map[net.Conn]struct{}), mu: sync.Mutex{}, closed: false}
}

// add holds conn and reports true, or closes conn and reports false after
// closeAll.
func (s *connSet) add(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		_ = conn.Close() //nolint:errcheck // The connection is refused; nothing reads it.

		return false
	}

	s.conns[conn] = struct{}{}

	return true
}

// remove forgets conn after its own close.
func (s *connSet) remove(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.conns, conn)
}

// reopen lets add hold connections again after closeAll.
func (s *connSet) reopen() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = false
}

// closeAll closes every held connection and every later one.
func (s *connSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	for conn := range s.conns {
		_ = conn.Close() //nolint:errcheck // A kill drops the connection; its peer sees the close.
		delete(s.conns, conn)
	}
}

// trackedListener is a net.Listener that holds every accepted connection,
// so a kill closes the connections of requests in flight too.
type trackedListener struct {
	net.Listener

	conns *connSet
	once  sync.Once
}

// listen binds loopback on an ephemeral port.
func listen(t *testing.T) (*trackedListener, error) {
	t.Helper()

	//nolint:exhaustruct_v5 // The test listens with defaults; only the address matters.
	lc := &net.ListenConfig{}

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	return &trackedListener{Listener: ln, conns: newConnSet(), once: sync.Once{}}, nil
}

// Accept implements net.Listener.
func (l *trackedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, fmt.Errorf("accept: %w", err)
		}

		if l.conns.add(conn) {
			return &trackedConn{Conn: conn, set: l.conns}, nil
		}
	}
}

// Close implements net.Listener. A second close answers nil.
func (l *trackedListener) Close() error {
	var err error

	l.once.Do(func() { err = l.Listener.Close() })

	if err != nil {
		return fmt.Errorf("close listener: %w", err)
	}

	return nil
}

// closeAll closes the listener and every accepted connection.
func (l *trackedListener) closeAll() {
	_ = l.Close() //nolint:errcheck // A kill drops the listener; a second close is nil.

	l.conns.closeAll()
}

// trackedConn is a held connection that leaves its set on close.
type trackedConn struct {
	net.Conn

	set *connSet
}

// Close implements net.Conn.
func (c *trackedConn) Close() error {
	c.set.remove(c.Conn)

	if err := c.Conn.Close(); err != nil {
		return fmt.Errorf("close connection: %w", err)
	}

	return nil
}

// connProxy forwards TCP connections from a loopback port to the database
// host. A server connects through its own proxy, so a kill cuts every
// database connection of that server and refuses its new ones, and the
// database sees the server as gone.
type connProxy struct {
	ctx    context.Context //nolint:containedctx // The proxy dials until close cancels it.
	cancel context.CancelFunc
	ln     net.Listener
	conns  *connSet
	target string
	url    string
	wg     sync.WaitGroup
	once   sync.Once
}

// startProxy listens on loopback and forwards to the host of dbURL. Its url
// is dbURL with the host of the proxy.
func startProxy(t *testing.T, dbURL string) *connProxy {
	t.Helper()

	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}

	if u.Host == "" {
		t.Fatalf("%s must name a TCP host", EnvURL)
	}

	target := u.Host
	if u.Port() == "" {
		target = net.JoinHostPort(u.Hostname(), "5432")
	}

	//nolint:exhaustruct_v5 // The proxy listens with defaults; only the address matters.
	lc := &net.ListenConfig{}

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the database proxy: %v", err)
	}

	u.Host = ln.Addr().String()

	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))

	p := &connProxy{
		ctx:    ctx,
		cancel: cancel,
		ln:     ln,
		conns:  newConnSet(),
		target: target,
		url:    u.String(),
		wg:     sync.WaitGroup{},
		once:   sync.Once{},
	}

	p.wg.Go(p.serve)

	return p
}

// serve accepts connections until the listener closes.
func (p *connProxy) serve() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}

		if !p.conns.add(conn) {
			continue
		}

		p.wg.Go(func() { p.forward(conn) })
	}
}

// forward copies bytes both ways between conn and a new connection to the
// target, until either side closes.
func (p *connProxy) forward(conn net.Conn) {
	defer func() {
		p.conns.remove(conn)
		_ = conn.Close() //nolint:errcheck // The peer already saw the end of the copy.
	}()

	//nolint:exhaustruct_v5 // The proxy dials with defaults; only the target matters.
	dialer := &net.Dialer{}

	upstream, err := dialer.DialContext(p.ctx, "tcp", p.target)
	if err != nil {
		return
	}

	if !p.conns.add(upstream) {
		return
	}

	defer func() {
		p.conns.remove(upstream)
		_ = upstream.Close() //nolint:errcheck // The peer already saw the end of the copy.
	}()

	var copies sync.WaitGroup

	copies.Go(func() { copyThenClose(upstream, conn) })
	copies.Go(func() { copyThenClose(conn, upstream) })
	copies.Wait()
}

// copyThenClose copies src to dst, then closes both, so the other copy of
// the pair ends too.
func copyThenClose(dst, src net.Conn) {
	_, _ = io.Copy(dst, src) //nolint:errcheck // A cut connection ends the copy; the peers see the close.

	_ = dst.Close() //nolint:errcheck // Both ends close; the peers see the close.
	_ = src.Close() //nolint:errcheck // Both ends close; the peers see the close.
}

// cut closes every connection of the proxy and refuses each new one until
// resume, as a database does that fails over: the listener stays, and the
// peers see a closed connection.
func (p *connProxy) cut() {
	p.conns.closeAll()
}

// resume lets the proxy forward new connections again after cut.
func (p *connProxy) resume() {
	p.conns.reopen()
}

// close stops the proxy: it closes the listener and every connection, and
// waits for the copies to end. A second close does nothing.
func (p *connProxy) close() {
	p.once.Do(func() {
		p.cancel()
		_ = p.ln.Close() //nolint:errcheck // A kill drops the listener.

		p.conns.closeAll()
		p.wg.Wait()
	})
}
