package proxy_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

const (
	// testHost is the host of the proxy rules in the tests.
	testHost = "api.example.com"
	// probeValue is the credential value in the tests. No record, body or
	// error text may hold it.
	probeValue = "ZQ9-probe-value-ZQ9"
	// ioWait is the longest wait of a test for something to happen.
	ioWait = 5 * time.Second
)

// Static errors of the test doubles.
var (
	errSinkDown   = errors.New("the sink is down")
	errNoSuchName = errors.New("no such name")
	errLookup     = errors.New("lookup failed at ns.internal.corp")
)

// closeAfter closes c when the test ends.
func closeAfter(t *testing.T, c io.Closer) {
	t.Helper()

	t.Cleanup(func() {
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Logf("close: %v", err)
		}
	})
}

// closeNow closes c at once.
func closeNow(t *testing.T, c io.Closer) {
	t.Helper()

	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Logf("close: %v", err)
	}
}

// send writes s to c and fails the test when the write fails.
func send(t *testing.T, c net.Conn, s string) {
	t.Helper()

	if _, err := c.Write([]byte(s)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// trySend writes s to c and reports whether the write worked. A peer may close
// a connection before the end of a hostile request.
func trySend(c net.Conn, s string) bool {
	_, err := c.Write([]byte(s))

	return err == nil
}

// pause waits for d, or ends when the test ends.
func pause(t *testing.T, d time.Duration) {
	t.Helper()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
	}
}

// sink is a record sink for the tests.
type sink struct {
	mu   sync.Mutex
	recs []proxy.Record
	fail atomic.Bool
}

func newSink() *sink {
	return &sink{mu: sync.Mutex{}, recs: nil, fail: atomic.Bool{}}
}

func (s *sink) Write(rec proxy.Record) error {
	if s.fail.Load() {
		return errSinkDown
	}

	s.mu.Lock()
	s.recs = append(s.recs, rec)
	s.mu.Unlock()

	return nil
}

func (s *sink) all() []proxy.Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]proxy.Record(nil), s.recs...)
}

// waitFor waits until the sink holds n records and returns them.
func (s *sink) waitFor(t *testing.T, n int) []proxy.Record {
	t.Helper()

	got := make([]proxy.Record, 0)

	if !poll(t, func() bool {
		got = s.all()

		return len(got) >= n
	}) {
		t.Fatalf("the sink holds %d records, want %d: %+v", len(got), n, got)
	}

	return got
}

// requestRecords returns the records of requests inside a connection: not the
// CONNECT and not the TLS event, which come when the connection ends.
func (s *sink) requestRecords() []proxy.Record {
	var reqs []proxy.Record

	all := s.all()
	for i := range all {
		if all[i].Method != http.MethodConnect && all[i].Method != "TLS" {
			reqs = append(reqs, all[i])
		}
	}

	return reqs
}

// poll calls cond every few milliseconds until it returns true. It returns
// false when ioWait passes first.
func poll(t *testing.T, cond func() bool) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), ioWait)
	defer cancel()

	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	for {
		if cond() {
			return true
		}

		select {
		case <-ctx.Done():
			return cond()
		case <-tick.C:
		}
	}
}

// bound makes every read and write on c fail after d, so a test never waits
// for a peer that stays silent.
func bound(t *testing.T, c net.Conn, d time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), d)
	stop := context.AfterFunc(ctx, func() {
		// A deadline in the past fails every pending and later I/O. An error
		// means the connection is closed already.
		if err := c.SetDeadline(time.Unix(1, 0)); err != nil {
			return
		}
	})

	t.Cleanup(func() {
		stop()
		cancel()
	})
}

// lastReq waits until the sink holds n records of requests inside a
// connection (not the CONNECT and not the TLS event, which come when the
// connection ends) and returns the n-th.
func (s *sink) lastReq(t *testing.T, n int) proxy.Record {
	t.Helper()

	reqs := make([]proxy.Record, 0)

	if !poll(t, func() bool {
		reqs = s.requestRecords()

		return len(reqs) >= n
	}) {
		t.Fatalf("the sink holds %d request records, want %d: %+v", len(reqs), n, s.all())
	}

	return reqs[n-1]
}

// creds is a credential source for the tests.
type creds map[string]proxy.Credential

func (c creds) Credential(name string) (proxy.Credential, error) {
	if v, ok := c[name]; ok {
		return v, nil
	}

	return proxy.Credential{}, fmt.Errorf("%w: %s", errNoSuchName, probeValue)
}

// hdr is a header credential.
func hdr(name, value string) proxy.Credential {
	return proxy.Credential{
		Kind: proxy.KindHeader, Name: name, Value: value, User: "", Password: "", Certificate: nil,
	}
}

// basicCred is a basic credential.
func basicCred(user, password string) proxy.Credential {
	return proxy.Credential{
		Kind: proxy.KindBasic, Name: "", Value: "", User: user, Password: password, Certificate: nil,
	}
}

// queryCred is a query credential.
func queryCred(name, value string) proxy.Credential {
	return proxy.Credential{
		Kind: proxy.KindQuery, Name: name, Value: value, User: "", Password: "", Certificate: nil,
	}
}

// resolver is a test resolver.
type resolver struct {
	answers map[string][]netip.Addr
	err     error
	calls   atomic.Int32
}

func (r *resolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.calls.Add(1)

	if r.err != nil {
		return nil, r.err
	}

	return r.answers[host], nil
}

// loopbackResolver resolves every name to 127.0.0.1.
func loopbackResolver() *resolver {
	lo := []netip.Addr{netip.MustParseAddr("127.0.0.1")}

	return &resolver{
		answers: map[string][]netip.Addr{testHost: lo, "other.example": lo},
		err:     nil,
		calls:   atomic.Int32{},
	}
}

// answering is a resolver with one answer for the test host.
func answering(addrs ...string) *resolver {
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, netip.MustParseAddr(a))
	}

	return &resolver{answers: map[string][]netip.Addr{testHost: out}, err: nil, calls: atomic.Int32{}}
}

// failing is a resolver that always fails.
func failing(err error) *resolver {
	return &resolver{answers: nil, err: err, calls: atomic.Int32{}}
}

// certFor makes a self-signed server certificate for host.
func certFor(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = big.NewInt(7)
	var subject pkix.Name

	subject.CommonName = host
	tmpl.Subject = subject
	tmpl.DNSNames = []string{host}
	// A fixed, wide window: the certificate is valid whenever the test runs.
	tmpl.NotBefore = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.NotAfter = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
	tmpl.IsCA = true
	tmpl.BasicConstraintsValid = true
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	var out tls.Certificate

	out.Certificate = [][]byte{der}
	out.PrivateKey = key

	return out, pool
}

// seen is one request that the upstream got.
type seen struct {
	Method string
	URI    string
	Proto  string
	Body   string
	Header http.Header
	// Truncated is true when the body ended with a read error.
	Truncated bool
}

// upstream is a fake TLS host.
type upstream struct {
	srv  *httptest.Server
	pool *x509.CertPool
	port int

	mu   sync.Mutex
	reqs []seen
}

func (u *upstream) requests() []seen {
	u.mu.Lock()
	defer u.mu.Unlock()

	return append([]seen(nil), u.reqs...)
}

// newUpstream starts a TLS host that answers with h.
func newUpstream(t *testing.T, h http.HandlerFunc) *upstream {
	t.Helper()

	cert, pool := certFor(t, testHost)
	u := &upstream{srv: nil, pool: pool, port: 0, mu: sync.Mutex{}, reqs: nil}

	u.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A read error ends the body: the record keeps what arrived.
		body, rerr := io.ReadAll(io.LimitReader(r.Body, 1<<20))

		u.mu.Lock()
		u.reqs = append(u.reqs, seen{
			Method: r.Method, URI: r.RequestURI, Proto: r.Proto, Body: string(body),
			Header: r.Header.Clone(), Truncated: rerr != nil,
		})
		u.mu.Unlock()

		if h != nil {
			h(w, r)

			return
		}

		if _, err := w.Write([]byte("ok")); err != nil {
			return
		}
	}))
	u.srv.EnableHTTP2 = true
	u.srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	u.srv.StartTLS()
	t.Cleanup(u.srv.Close)

	_, p, err := net.SplitHostPort(u.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	u.port, err = strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}

	return u
}

// env is a running job proxy with its fake host.
type env struct {
	p    *proxy.Proxy
	addr string
	up   *upstream
	sink *sink
}

// options changes the default configuration of newEnv.
type options struct {
	rules   func(port int) string
	creds   creds
	egress  egress.Rules
	res     *resolver
	limits  proxy.Limits
	buffer  int
	handler http.HandlerFunc
}

// option changes options.
type option func(*options)

func withRules(f func(port int) string) option { return func(o *options) { o.rules = f } }
func withCreds(c creds) option                 { return func(o *options) { o.creds = c } }
func withEgress(r egress.Rules) option         { return func(o *options) { o.egress = r } }
func withResolver(r *resolver) option          { return func(o *options) { o.res = r } }
func withLimits(l proxy.Limits) option         { return func(o *options) { o.limits = l } }
func withBuffer(n int) option                  { return func(o *options) { o.buffer = n } }
func withHandler(h http.HandlerFunc) option    { return func(o *options) { o.handler = h } }

// noLimits is the Limits value with every field at its default.
func noLimits() proxy.Limits {
	return proxy.Limits{
		HeaderTimeout: 0, HandshakeTimeout: 0, DialTimeout: 0, ResponseHeaderTimeout: 0,
		IdleTimeout: 0, MaxHeaderBytes: 0, MaxRequestBody: 0, MaxConnections: 0,
	}
}

// defaultRules is one inspect rule that allows GET and POST under /v1 and
// the credential "tok".
func defaultRules(port int) string {
	return fmt.Sprintf(`{"rules":[{"id":"api","operations":["plan"],"host":%q,"port":%d,`+
		`"mode":"inspect","methods":["GET","POST"],"paths":["/v1/**"],"credential":"tok"}]}`, testHost, port)
}

// tunnelRules is one tunnel rule for the test host.
func tunnelRules(port int) string {
	return fmt.Sprintf(`{"rules":[{"id":"t","operations":["plan"],"host":%q,"port":%d,"mode":"tunnel"}]}`,
		testHost, port)
}

// endpointRules is one endpoint rule for the test host.
func endpointRules(port int) string {
	return fmt.Sprintf(`{"rules":[{"id":"ep","operations":["plan"],"host":%q,"port":%d,`+
		`"mode":"endpoint","methods":["GET"],"paths":["/v1/**"],"credential":"tok"}]}`, testHost, port)
}

// render parses and renders a rule document for the operation plan.
func render(t *testing.T, doc string) jobproxy.Rendered {
	t.Helper()

	set, err := jobproxy.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	r, err := jobproxy.Render(set, jobproxy.Vars{
		Tool: "", Namespace: "", Name: "", Labels: nil, Operation: jobproxy.OperationPlan,
		Inputs: nil, Profiles: nil,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	return r
}

// baseConfig is a complete Config for the tests, with sink as the record sink.
func baseConfig(s proxy.Records) proxy.Config {
	return proxy.Config{
		Rendered: jobproxy.Rendered{}, Operation: jobproxy.OperationPlan,
		Job: "job-1", Phase: "plan", Unit: "acme/payments",
		Egress: egress.Rules{Allow: nil, LoopbackForTests: false}, Credentials: nil, Trust: nil,
		SystemRoots: nil, Records: s, RecordBuffer: 0, RecordRetry: 10 * time.Millisecond,
		Resolver: nil, CALifetime: time.Hour, Now: nil, Limits: noLimits(),
	}
}

// newEnv starts a job proxy on a loopback listener.
func newEnv(t *testing.T, opts ...option) *env {
	t.Helper()

	o := options{
		rules: defaultRules, creds: nil, egress: egress.Rules{Allow: nil, LoopbackForTests: true},
		res: loopbackResolver(), limits: noLimits(), buffer: 0, handler: nil,
	}
	for _, f := range opts {
		f(&o)
	}

	if o.creds == nil {
		o.creds = creds{"tok": hdr("X-Api-Key", probeValue)}
	}

	up := newUpstream(t, o.handler)
	e := &env{p: nil, addr: "", up: up, sink: newSink()}

	cfg := baseConfig(e.sink)
	cfg.Rendered = render(t, o.rules(up.port))
	cfg.Egress, cfg.Credentials, cfg.SystemRoots = o.egress, o.creds, up.pool
	cfg.RecordBuffer, cfg.Resolver, cfg.Limits = o.buffer, o.res, o.limits

	p, err := proxy.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	l := listenLoopback(t)
	e.p, e.addr = p, l.Addr().String()

	done := make(chan struct{})

	go func() {
		defer close(done)

		if serr := p.Serve(l); serr != nil {
			t.Errorf("Serve: %v", serr)
		}
	}()

	t.Cleanup(func() {
		if cerr := p.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}

		<-done
	})

	return e
}

// listenLoopback opens a loopback listener.
func listenLoopback(t *testing.T) net.Listener {
	t.Helper()

	var lc net.ListenConfig

	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	return l
}

// dialRaw opens a connection with a deadline.
func dialRaw(t *testing.T, addr string) net.Conn {
	t.Helper()

	var d net.Dialer

	d.Timeout = ioWait

	c, err := d.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}

	closeAfter(t, c)

	bound(t, c, 2*ioWait)

	return c
}

// caPool returns the pool with the CA of the proxy.
func (e *env) caPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(e.p.CACertificatePEM())

	return pool
}

// protocol is the HTTP version of the client leg.
type protocol int

const (
	http1 protocol = iota + 1
	http2
)

// client returns an HTTP client that goes through the proxy and trusts its CA.
func (e *env) client(t *testing.T, proto protocol) *http.Client {
	t.Helper()

	pu, err := url.Parse("http://" + e.addr)
	if err != nil {
		t.Fatal(err)
	}

	tr := &http.Transport{
		Proxy:             http.ProxyURL(pu),
		TLSClientConfig:   &tls.Config{RootCAs: e.caPool(), MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: proto == http2,
	}
	if proto == http1 {
		tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}

	t.Cleanup(tr.CloseIdleConnections)

	return &http.Client{
		Transport: tr,
		Timeout:   2 * ioWait,
		Jar:       nil,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// base is the base URL of the fake host.
func (e *env) base() string { return "https://" + testHost + ":" + portOf(e) }

// portOf returns the port of the fake host as text.
func portOf(e *env) string { return strconv.Itoa(e.up.port) }

// hostHeader is the Host header line for the fake host.
func hostHeader(e *env) string { return "Host: " + testHost + ":" + portOf(e) + "\r\n" }

// reply is one response, read to its end.
type reply struct {
	status int
	header http.Header
	body   string
}

// getVia sends a GET with c and returns the reply. A transport error fails
// the test. A non-empty host replaces the Host header.
func getVia(t *testing.T, c *http.Client, target, host string) reply {
	t.Helper()

	rep, err := tryGet(t, c, target, host)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}

	return rep
}

// tryGet is getVia with the error returned.
func tryGet(t *testing.T, c *http.Client, target, host string) (reply, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	if host != "" {
		req.Host = host
	}

	resp, err := c.Do(req)
	if err != nil {
		return reply{status: 0, header: nil, body: ""}, fmt.Errorf("do: %w", err)
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("close: %v", cerr)
		}
	}()

	body, rerr := io.ReadAll(resp.Body)
	rep := reply{status: resp.StatusCode, header: resp.Header, body: string(body)}

	if rerr != nil {
		return rep, fmt.Errorf("read: %w", rerr)
	}

	return rep, nil
}

// get sends a GET through the proxy.
func (e *env) get(t *testing.T, proto protocol, path string) reply {
	t.Helper()

	return getVia(t, e.client(t, proto), e.base()+path, "")
}

// connectRaw opens a connection to the proxy and sends a CONNECT for target.
// It returns the connection, a reader and the status of the answer.
func (e *env) connectRaw(t *testing.T, target string) (net.Conn, *bufio.Reader, int) {
	t.Helper()

	c := dialRaw(t, e.addr)
	send(t, c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")

	br := bufio.NewReader(c)

	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("CONNECT %q: %v", target, err)
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("close: %v", cerr)
		}
	}()

	return c, br, resp.StatusCode
}

// rawTLS opens a CONNECT to the fake host and ends TLS with the CA of the
// proxy, so a test can write request bytes of its own.
func (e *env) rawTLS(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()

	return e.rawTLSWith(t, e.caPool(), testHost)
}

// rawTLSWith is rawTLS with the roots and the server name of the client.
func (e *env) rawTLSWith(t *testing.T, roots *x509.CertPool, name string) (net.Conn, *bufio.Reader) {
	t.Helper()

	c, br, status := e.connectRaw(t, testHost+":"+portOf(e))
	if status != http.StatusOK {
		t.Fatalf("CONNECT status %d", status)
	}

	tc := tls.Client(&bufferedConn{Conn: c, br: br}, &tls.Config{
		ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
	})
	if err := tc.HandshakeContext(t.Context()); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	return tc, bufio.NewReader(tc)
}

// newReader returns a buffered reader for c.
func newReader(c net.Conn) *bufio.Reader { return bufio.NewReader(c) }

// handshake runs a TLS client handshake on c and returns its error.
func handshake(t *testing.T, c net.Conn, roots *x509.CertPool, name string) error {
	t.Helper()

	tc := tls.Client(c, &tls.Config{
		ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
	})

	if err := tc.HandshakeContext(t.Context()); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	return nil
}

// bufferedConn reads first from a reader that holds bytes already read.
type bufferedConn struct {
	net.Conn

	br *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return keep(b.br.Read(p)) }

// keep returns its arguments. The errors of a connection keep their identity,
// because crypto/tls compares them with io.EOF.
func keep(n int, err error) (int, error) { return n, err }

// readReply reads one response from br.
func readReply(br *bufio.Reader) (reply, bool) {
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return reply{status: 0, header: nil, body: ""}, false
	}

	body, rerr := io.ReadAll(resp.Body)
	cerr := resp.Body.Close()

	return reply{status: resp.StatusCode, header: resp.Header, body: string(body)}, rerr == nil && cerr == nil
}

// rawRequest writes the bytes of a request and reads one response.
func rawRequest(t *testing.T, c net.Conn, br *bufio.Reader, req string) reply {
	t.Helper()

	send(t, c, req)

	rep, ok := readReply(br)
	if !ok && rep.status == 0 {
		t.Fatal("no response")
	}

	return rep
}

// plainStatus sends a plain proxy request for target and returns the status,
// or 0 when the connection ended.
func plainStatus(t *testing.T, e *env, target string) int {
	t.Helper()

	c := dialRaw(t, e.addr)
	send(t, c, "GET "+target+" HTTP/1.1\r\nHost: x.example\r\n\r\n")

	rep, _ := readReply(bufio.NewReader(c))

	return rep.status
}

// assertNoProbe fails when s holds the credential value.
func assertNoProbe(t *testing.T, what, s string) {
	t.Helper()

	if strings.Contains(s, probeValue) {
		t.Errorf("%s holds the credential", what)
	}
}
