package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// defaultHTTPPort is the port of a plain proxy request with no port.
const defaultHTTPPort = 80

// directions is the number of copy directions of a tunnel.
const directions = 2

// idleConn sets a deadline on each read and write, so a peer that moves no
// byte for the idle time loses the connection.
type idleConn struct {
	net.Conn

	idle time.Duration
}

func (c *idleConn) Read(b []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, fmt.Errorf("set the read deadline: %w", err)
	}

	return same(c.Conn.Read(b))
}

func (c *idleConn) Write(b []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, fmt.Errorf("set the write deadline: %w", err)
	}

	return same(c.Conn.Write(b))
}

// same returns its arguments. The errors of a connection pass to the caller
// unchanged, because net/http and crypto/tls compare them with io.EOF and
// net.ErrClosed: a wrapped error would change how they end a connection.
func same(n int, err error) (int, error) { return n, err }

// sameErr returns err unchanged, for the same reason as same.
func sameErr(err error) error { return err }

// CloseWrite closes the write side when the connection can.
func (c *idleConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return sameErr(cw.CloseWrite())
	}

	return sameErr(c.Close())
}

// bufConn reads first from the buffer that a hijack returned.
type bufConn struct {
	net.Conn

	r io.Reader
}

func (c *bufConn) Read(b []byte) (int, error) { return same(c.r.Read(b)) }

// notifyConn tells when it is closed.
type notifyConn struct {
	net.Conn

	once   *sync.Once
	closed chan struct{}
}

func (c *notifyConn) Close() error {
	c.once.Do(func() { close(c.closed) })

	return sameErr(c.Conn.Close())
}

// oneConnListener gives one connection to http.Server.Serve and then waits
// until that connection is closed.
type oneConnListener struct {
	ch     chan net.Conn
	closed <-chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *oneConnListener) Close() error { return nil }

func (l *oneConnListener) Addr() net.Addr { return &net.TCPAddr{IP: nil, Port: 0, Zone: ""} }

// connect serves a CONNECT.
func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	if !p.begin(w) {
		return
	}

	rec := p.newRecord(http.MethodConnect, "", "")

	defer p.finish(&rec)

	// Layer 1: the target. The raw request target is parsed, and the Host
	// that the server derived from it must be the same text.
	host, port, err := ParseConnectTarget(r.RequestURI)
	if err != nil || r.Host != r.RequestURI {
		rec.Reason, rec.Status = ReasonBadTarget, http.StatusBadRequest
		refuse(w, rec.Status, "blocked by the job proxy: the CONNECT target is not valid")

		return
	}

	rec.Host = host

	mode, ok := p.byHost[hostKey{host: host, port: port}]
	if !ok {
		rec.Reason, rec.Status = ReasonNoRuleHost, http.StatusForbidden
		refuse(w, rec.Status, "blocked by the job proxy: CONNECT "+hostPort(host, port)+": no rule names the host")

		return
	}

	rec.Mode = mode

	switch mode {
	case jobproxy.ModeTunnel:
		p.tunnel(w, r, &rec, hostKey{host: host, port: port})
	case jobproxy.ModeInspect:
		p.inspect(w, r, &rec, hostKey{host: host, port: port})
	case jobproxy.ModeEndpoint:
		rec.Reason, rec.Status = ReasonModeNotAllowed, http.StatusForbidden
		refuse(w, rec.Status, "blocked by the job proxy: CONNECT "+hostPort(host, port)+": the host is an endpoint")
	default:
		rec.Reason, rec.Status = ReasonModeNotAllowed, http.StatusForbidden
		refuse(w, rec.Status, "blocked by the job proxy: CONNECT "+hostPort(host, port)+": the mode is not known")
	}
}

// dialReason maps the error of a dial to a fixed reason and a status.
func dialReason(err error) (Reason, int) {
	switch {
	case errors.Is(err, ErrGuardRefused):
		return ReasonGuardRefused, http.StatusForbidden
	case errors.Is(err, ErrResolveFailed):
		return ReasonResolveFailed, http.StatusBadGateway
	case errors.Is(err, context.DeadlineExceeded):
		return ReasonTimeout, http.StatusGatewayTimeout
	default:
		return ReasonDialFailed, http.StatusBadGateway
	}
}

// hijack takes the connection of a CONNECT and answers 200.
func (p *Proxy) hijack(w http.ResponseWriter) (net.Conn, bool) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		refuse(w, http.StatusInternalServerError, "blocked by the job proxy: the connection cannot be taken")

		return nil, false
	}

	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, false
	}

	var c net.Conn = &idleConn{Conn: conn, idle: p.cfg.Limits.IdleTimeout}
	if brw != nil && brw.Reader.Buffered() > 0 {
		c = &bufConn{Conn: c, r: brw.Reader}
	}

	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		p.shut(conn)

		return nil, false
	}

	return c, true
}

// tunnel serves a CONNECT of mode tunnel.
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request, rec *Record, key hostKey) {
	p.wg.Add(1)
	defer p.wg.Done()

	dec := jobproxy.Match(p.cfg.Rendered, jobproxy.Request{
		Operation: p.cfg.Operation, Host: key.host, Port: key.port, Method: "", Path: "", PlainHTTP: false,
	})
	if !dec.Allowed {
		rec.Reason, rec.Status = Reason(dec.Reason), http.StatusForbidden
		refuse(w, rec.Status, "blocked by the job proxy: CONNECT "+hostPort(key.host, key.port)+": no rule matched")

		return
	}

	rec.RuleID = dec.RuleID

	if !p.acquire() {
		rec.Reason, rec.Status = ReasonTooBusy, http.StatusServiceUnavailable
		refuse(w, rec.Status, "blocked by the job proxy: too many connections")

		return
	}
	defer p.release()

	// Layer: the outbound guard, before the 200.
	up, err := p.guard.DialContext(r.Context(), key.host, key.port)
	if err != nil {
		rec.Reason, rec.Status = dialReason(err)
		refuse(w, rec.Status, "blocked by the job proxy: CONNECT "+hostPort(key.host, key.port)+": "+string(rec.Reason))

		return
	}

	upc := &idleConn{Conn: up, idle: p.cfg.Limits.IdleTimeout}

	client, ok := p.hijack(w)
	if !ok {
		p.shut(up)
		rec.Status = http.StatusInternalServerError

		return
	}

	if !p.track(client) || !p.track(upc) {
		p.shut(client)
		p.shut(up)

		return
	}

	defer p.untrack(client)
	defer p.untrack(upc)

	rec.Allowed, rec.Status = true, http.StatusOK
	rec.Bytes = p.pipe(client, upc)
}

// pipe copies in both directions until one side ends or fails, and returns
// the number of bytes that it moved. It closes both connections.
func (p *Proxy) pipe(a, b net.Conn) int64 {
	type result struct {
		n   int64
		err error
	}

	done := make(chan result, directions)

	copyDir := func(dst, src net.Conn) {
		n, err := io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok && err == nil {
			if cerr := cw.CloseWrite(); cerr != nil {
				p.closeErrors.Add(1)
			}
		}

		done <- result{n: n, err: err}
	}

	go copyDir(a, b)

	go copyDir(b, a)

	var total int64

	for range directions {
		res := <-done

		total += res.n

		if res.err != nil {
			// A reset or a timeout on one side ends both.
			p.shut(a)
			p.shut(b)
		}
	}

	p.shut(a)
	p.shut(b)

	return total
}

// inspect serves a CONNECT of mode inspect: it answers 200, ends the TLS of
// the client with a leaf of the CA and serves the requests inside.
func (p *Proxy) inspect(w http.ResponseWriter, r *http.Request, rec *Record, key hostKey) {
	if !p.acquire() {
		rec.Reason, rec.Status = ReasonTooBusy, http.StatusServiceUnavailable
		refuse(w, rec.Status, "blocked by the job proxy: too many connections")

		return
	}
	defer p.release()

	conn, ok := p.hijack(w)
	if !ok {
		rec.Status = http.StatusInternalServerError

		return
	}

	rec.Allowed, rec.Status = true, http.StatusOK

	p.wg.Add(1)
	defer p.wg.Done()

	closed := make(chan struct{})
	nc := &notifyConn{Conn: conn, once: &sync.Once{}, closed: closed}

	if !p.track(nc) {
		p.shut(nc)

		return
	}
	defer p.untrack(nc)

	tc, ok := p.endTLS(r.Context(), nc, key)
	if !ok {
		p.shut(nc)

		return
	}

	srv := p.newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.forward(w, r, target{host: key.host, port: key.port, kind: kindInspect})
	}))
	ln := &oneConnListener{ch: make(chan net.Conn, 1), closed: closed}
	ln.ch <- tc

	// Serve returns when the connection closes: its error is the close.
	if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
		p.closeErrors.Add(1)
	}

	p.shut(srv)
}

// endTLS ends the TLS of the client. Layer: the SNI must be the host of the
// CONNECT. The leaf is for that host only, so a certificate for another name
// is never made.
func (p *Proxy) endTLS(ctx context.Context, nc net.Conn, key hostKey) (*tls.Conn, bool) {
	mismatch := false

	tc := tls.Server(nc, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.ServerName != key.host {
				mismatch = true

				return nil, ErrBadTarget
			}

			return p.ca.leaf(key.host)
		},
	})

	hctx, cancel := context.WithTimeout(ctx, p.cfg.Limits.HandshakeTimeout)
	defer cancel()

	if err := tc.HandshakeContext(hctx); err != nil {
		extra := p.newRecord("TLS", key.host, jobproxy.ModeInspect)

		extra.Reason = ReasonBadRequest
		if mismatch {
			extra.Reason = ReasonHostMismatch
		}

		p.note(&extra)

		return nil, false
	}

	return tc, true
}

// plain serves a plain proxy request: an absolute-form request to http.
func (p *Proxy) plain(w http.ResponseWriter, r *http.Request) {
	if !p.begin(w) {
		return
	}

	form, ok := splitAbsoluteForm(r.RequestURI)
	if !ok {
		rec := p.newRecord(r.Method, "", "")
		rec.Reason, rec.Status = ReasonBadTarget, http.StatusBadRequest

		if !strings.HasPrefix(r.RequestURI, "http://") {
			// An https absolute form, an origin form or an asterisk form is not
			// a plain proxy request.
			rec.Reason = ReasonBadRequest
		}

		refuse(w, rec.Status, "blocked by the job proxy: the request target is not valid")
		p.finish(&rec)

		return
	}

	// The reserved slot is passed on: forwardReserved must not reserve a second one.
	p.forwardReserved(w, r, target{host: form.host, port: form.port, kind: kindPlain}, form.pathQuery)
}

// splitAbsoluteForm splits "http://host[:port]/path?query" into its parts. It
// accepts the http scheme in lower case only, a valid host name, and a path
// that starts with "/".
func splitAbsoluteForm(uri string) (absoluteForm, bool) {
	none := absoluteForm{host: "", port: 0, pathQuery: ""}

	rest, ok := strings.CutPrefix(uri, "http://")
	if !ok {
		return none, false
	}

	auth, pathQuery := rest, ""
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		auth, pathQuery = rest[:i], rest[i:]
	}

	host, port := auth, defaultHTTPPort

	if h, portText, found := strings.Cut(auth, ":"); found {
		host = h

		if port, ok = parsePort(portText); !ok {
			return none, false
		}
	}

	if !validHostName(host) || !strings.HasPrefix(pathQuery, "/") {
		return none, false
	}

	return absoluteForm{host: host, port: port, pathQuery: pathQuery}, true
}

// absoluteForm is the parts of a plain proxy request target.
type absoluteForm struct {
	host      string
	port      int
	pathQuery string
}
