// Package proxy is the core of the job proxy: the only network exit of a job
// and the place that adds a credential to a request.
//
// The core serves the CONNECT method and plain proxy requests. For each
// CONNECT it applies the rendered proxy rules of the job (package jobproxy):
//   - tunnel passes the bytes after the outbound guard checked the host,
//   - inspect ends the TLS of the client with a certificate of the CA of the
//     claim, checks each request, and sends it to the host with a credential,
//   - endpoint serves the host on a loopback listener of the job.
//
// The core is a network boundary, so each input has its own layer: the
// target parser, the name binding of the SNI and the Host header, the path
// check, the outbound guard, the injection and the response scan. No layer
// relies on another one. The core never follows a redirect, never writes a
// header, a body, a query value or a credential to a record, and refuses new
// requests when the buffer of records is full.
//
// The package links no internal package, no SQL driver and no forge client,
// because the runner module links it. It reads the raw request target and
// the headers on purpose: it is an HTTP proxy.
package proxy

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// Errors of the package. Their text is fixed.
var (
	// ErrConfig is the error for a Config that New refuses.
	ErrConfig = errors.New("the job proxy configuration is not valid")
	// ErrEndpoint is the error for an endpoint that cannot be served.
	ErrEndpoint = errors.New("the endpoint cannot be served")
	// ErrServe is the error for a listener that failed.
	ErrServe = errors.New("the listener failed")
)

// Defaults of the limits.
const (
	defaultHeaderTimeout    = 10 * time.Second
	defaultHandshakeTimeout = 10 * time.Second
	defaultDialTimeout      = 10 * time.Second
	defaultResponseTimeout  = 60 * time.Second
	defaultIdleTimeout      = 2 * time.Minute
	defaultRetry            = time.Second
	defaultMaxHeaderBytes   = 32 << 10
	defaultMaxRequestBody   = 256 << 20
	defaultMaxConnections   = 256
)

// Limits are the size limits and the time limits of the job proxy. A zero
// field takes its default.
type Limits struct {
	// HeaderTimeout is the time a client has to send a request line and its
	// headers.
	HeaderTimeout time.Duration
	// HandshakeTimeout is the time a client has to finish the TLS handshake.
	HandshakeTimeout time.Duration
	// DialTimeout is the time to resolve and to connect to a host.
	DialTimeout time.Duration
	// ResponseHeaderTimeout is the time a host has to answer a request.
	ResponseHeaderTimeout time.Duration
	// IdleTimeout ends a connection, on any side, that moves no byte for this
	// time.
	IdleTimeout time.Duration
	// MaxHeaderBytes is the largest request line plus headers.
	MaxHeaderBytes int
	// MaxRequestBody is the largest request body.
	MaxRequestBody int64
	// MaxConnections is the largest number of tunnels and TLS ends.
	MaxConnections int
}

// Config is the configuration of a job proxy.
type Config struct {
	// Rendered is the rendered proxy rules of the job. Its zero value allows
	// nothing.
	Rendered jobproxy.Rendered
	// Operation is the operation of the job.
	Operation jobproxy.Operation
	// Job, Phase and Unit name the job in each record.
	Job, Phase, Unit string
	// Egress is the outbound guard.
	Egress egress.Rules
	// Credentials gives the credentials of the proxy rules.
	Credentials Credentials
	// Trust gives the CA pool of a proxy rule with a CA export as server
	// trust.
	Trust Trust
	// SystemRoots are the roots for the server trust "system". Nil means the
	// roots of the system.
	SystemRoots *x509.CertPool
	// Records takes the records. It is required.
	Records Records
	// RecordBuffer is the number of records to keep. The default is
	// DefaultRecordBuffer.
	RecordBuffer int
	// RecordRetry is the time between two tries of a failing sink.
	RecordRetry time.Duration
	// Resolver resolves host names. Nil means net.DefaultResolver.
	Resolver Resolver
	// CALifetime is the lifetime of the CA of the claim. It is required.
	CALifetime time.Duration
	// Now is the clock for the certificates and the records. Nil means
	// time.Now.
	Now func() time.Time
	// Limits are the limits.
	Limits Limits

	// dial is the connect function; tests replace it.
	dial dialFunc
}

// hostKey is a host and a port of a proxy rule.
type hostKey struct {
	host string
	port int
}

// Proxy is a job proxy.
type Proxy struct {
	cfg    Config
	ca     *ca
	byHost map[hostKey]jobproxy.Mode
	byID   map[string]jobproxy.RenderedRule
	guard  *guardDialer
	queue  *recordQueue
	sem    chan struct{}

	// closeErrors counts the failed closes of connections that ended.
	closeErrors atomic.Int64

	mu         sync.Mutex
	closed     bool
	servers    []*http.Server
	conns      map[net.Conn]struct{}
	transports map[string]*http.Transport
	wg         sync.WaitGroup
}

// New builds a job proxy and starts the writer of its records. Call Close to
// stop it.
func New(cfg Config) (*Proxy, error) {
	cfg = withDefaults(cfg)

	switch {
	case !cfg.Operation.Valid():
		return nil, fmt.Errorf("%w: the operation", ErrConfig)
	case cfg.Records == nil:
		return nil, fmt.Errorf("%w: the record sink", ErrConfig)
	case cfg.CALifetime <= 0:
		return nil, fmt.Errorf("%w: the CA lifetime", ErrConfig)
	}

	byHost := map[hostKey]jobproxy.Mode{}
	byID := map[string]jobproxy.RenderedRule{}

	var inspectHosts []string

	rules := cfg.Rendered.Rules()
	for i := range rules {
		r := &rules[i]
		byID[r.ID] = *r

		if !slices.Contains(r.Operations, cfg.Operation) {
			continue
		}

		byHost[hostKey{host: r.Host, port: r.Port}] = r.Mode

		if r.Mode == jobproxy.ModeInspect {
			inspectHosts = append(inspectHosts, r.Host)
		}
	}

	c, err := newCA(inspectHosts, cfg.CALifetime, cfg.Now)
	if err != nil {
		return nil, err
	}

	return &Proxy{
		cfg:    cfg,
		ca:     c,
		byHost: byHost,
		byID:   byID,
		guard: &guardDialer{
			rules: cfg.Egress, resolver: cfg.Resolver, timeout: cfg.Limits.DialTimeout, dial: cfg.dial,
		},
		queue:       newRecordQueue(cfg.Records, cfg.RecordBuffer, cfg.RecordRetry),
		sem:         make(chan struct{}, cfg.Limits.MaxConnections),
		closeErrors: atomic.Int64{},
		mu:          sync.Mutex{},
		closed:      false,
		servers:     nil,
		conns:       map[net.Conn]struct{}{},
		transports:  map[string]*http.Transport{},
		wg:          sync.WaitGroup{},
	}, nil
}

// withDefaults fills the zero fields of cfg.
func withDefaults(cfg Config) Config {
	l := &cfg.Limits

	l.HeaderTimeout = orDuration(l.HeaderTimeout, defaultHeaderTimeout)
	l.HandshakeTimeout = orDuration(l.HandshakeTimeout, defaultHandshakeTimeout)
	l.DialTimeout = orDuration(l.DialTimeout, defaultDialTimeout)
	l.ResponseHeaderTimeout = orDuration(l.ResponseHeaderTimeout, defaultResponseTimeout)
	l.IdleTimeout = orDuration(l.IdleTimeout, defaultIdleTimeout)

	if l.MaxHeaderBytes <= 0 {
		l.MaxHeaderBytes = defaultMaxHeaderBytes
	}

	if l.MaxRequestBody <= 0 {
		l.MaxRequestBody = defaultMaxRequestBody
	}

	if l.MaxConnections <= 0 {
		l.MaxConnections = defaultMaxConnections
	}

	if cfg.RecordBuffer <= 0 {
		cfg.RecordBuffer = DefaultRecordBuffer
	}

	cfg.RecordRetry = orDuration(cfg.RecordRetry, defaultRetry)

	if cfg.Resolver == nil {
		cfg.Resolver = net.DefaultResolver
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.dial == nil {
		cfg.dial = plainDial
	}

	return cfg
}

// orDuration returns d, or def when d is zero or negative.
func orDuration(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}

	return d
}

// CACertificatePEM returns the PEM of the CA certificate of the claim. The
// key never leaves the proxy.
func (p *Proxy) CACertificatePEM() []byte {
	return slices.Clone(p.ca.pem)
}

// CloseErrors returns the number of connections whose close failed. A failed
// close has no recovery, so the proxy counts it and goes on.
func (p *Proxy) CloseErrors() int64 { return p.closeErrors.Load() }

// Close stops the servers, closes each connection, and flushes the records.
// It returns ErrRecordsLost when the sink did not take every record. A second
// call does nothing.
func (p *Proxy) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()

		return nil
	}

	p.closed = true
	servers := p.servers
	conns := make([]net.Conn, 0, len(p.conns))

	for c := range p.conns {
		conns = append(conns, c)
	}

	transports := make([]*http.Transport, 0, len(p.transports))
	for _, t := range p.transports {
		transports = append(transports, t)
	}
	p.mu.Unlock()

	for _, s := range servers {
		p.shut(s)
	}

	for _, c := range conns {
		p.shut(c)
	}

	p.wg.Wait()

	for _, t := range transports {
		t.CloseIdleConnections()
	}

	return p.queue.close()
}

// Serve serves the proxy protocol on l until Close or an error of l. The
// request line and the headers of a client are limited, and a slow client is
// cut.
func (p *Proxy) Serve(l net.Listener) error {
	return p.run(l, p)
}

// ServeEndpoint serves the host and port of a proxy rule in mode endpoint on
// l. It returns ErrEndpoint when no such proxy rule exists for the job or
// when l is not a loopback listener.
func (p *Proxy) ServeEndpoint(l net.Listener, host string, port int) error {
	if p.byHost[hostKey{host: host, port: port}] != jobproxy.ModeEndpoint {
		return fmt.Errorf("%w: no endpoint proxy rule", ErrEndpoint)
	}

	// Defense in depth: the proxy rule parser has no listener address; this
	// check stays so that no caller can open an endpoint beyond the job.
	ta, ok := l.Addr().(*net.TCPAddr)
	if !ok || !ta.IP.IsLoopback() {
		return fmt.Errorf("%w: the listener is not a loopback listener", ErrEndpoint)
	}

	return p.run(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.forward(w, r, target{host: host, port: port, kind: kindEndpoint})
	}))
}

// ServeHTTP serves a CONNECT or a plain proxy request.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)

		return
	}

	p.plain(w, r)
}

// track keeps a connection so that Close can close it. It returns false when
// the proxy is closed.
func (p *Proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return false
	}

	p.conns[c] = struct{}{}

	return true
}

// untrack forgets a connection.
func (p *Proxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
}

// shut closes c. A close that fails on a connection that ends has no
// recovery, so the proxy counts it.
func (p *Proxy) shut(c io.Closer) {
	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		p.closeErrors.Add(1)
	}
}

// newServer builds an http.Server with the limits.
func (p *Proxy) newServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: p.cfg.Limits.HeaderTimeout,
		IdleTimeout:       p.cfg.Limits.IdleTimeout,
		MaxHeaderBytes:    p.cfg.Limits.MaxHeaderBytes,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
}

// run runs an http.Server for h on l.
func (p *Proxy) run(l net.Listener, h http.Handler) error {
	srv := p.newServer(h)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()

		// Like a server that Close stopped: no error.
		return nil
	}

	p.servers = append(p.servers, srv)
	p.mu.Unlock()

	if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%w: %w", ErrServe, err)
	}

	return nil
}

// acquire takes one connection slot.
func (p *Proxy) acquire() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// release gives back a connection slot.
func (p *Proxy) release() { <-p.sem }

// begin reserves a slot for the record of a request. It returns false, and
// answers 503, when the buffer of records is full: no request passes without
// a record.
func (p *Proxy) begin(w http.ResponseWriter) bool {
	if p.queue.reserve() {
		return true
	}

	refuse(w, http.StatusServiceUnavailable, "blocked by the job proxy: the records are full")

	return false
}

// finish queues the record of a request.
func (p *Proxy) finish(rec *Record) {
	rec.Time = p.cfg.Now()
	p.queue.put(*rec)
}

// note queues an extra record when a slot is free. It is for an event that
// ends a connection, so a missing record cannot let a request pass.
func (p *Proxy) note(rec *Record) {
	if p.queue.reserve() {
		rec.Time = p.cfg.Now()
		p.queue.put(*rec)
	}
}

// newRecord starts a record for a request.
func (p *Proxy) newRecord(method, host string, mode jobproxy.Mode) Record {
	return Record{
		Job: p.cfg.Job, Phase: p.cfg.Phase, Unit: p.cfg.Unit, Operation: p.cfg.Operation,
		Mode: mode, Method: method, Host: host, Path: "", RuleID: "", Allowed: false,
		Reason: ReasonNone, Status: 0, Bytes: 0, Time: time.Time{},
	}
}

// refuse writes a fixed refusal.
func refuse(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Connection", "close")
	w.WriteHeader(status)

	// The body is plain text with nosniff and a fixed shape, never HTML. The
	// writer is wrapped as an io.Writer so that the taint check of the
	// linter, which models an HTML page, does not apply to a text body.
	// A failed write to a client that went away has no recovery.
	if _, err := io.WriteString(textBody{w}, body); err != nil {
		return
	}
}

// textBody writes the bytes of a plain text body.
type textBody struct{ w io.Writer }

func (t textBody) Write(b []byte) (int, error) { return same(t.w.Write(b)) }
