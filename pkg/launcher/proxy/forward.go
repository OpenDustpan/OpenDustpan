package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// Bounds of the transport to a host.
const (
	maxResponseHeaderBytes = 64 << 10
	maxIdleHostConns       = 4
	hostIdleTimeout        = 30 * time.Second
	copyChunk              = 32 << 10
	defaultTLSPort         = 443
)

// kind is the way a request reached the proxy.
type kind int

const (
	// kindInspect: a request inside the TLS end of a CONNECT.
	kindInspect kind = iota + 1
	// kindEndpoint: a request on the loopback listener of a proxy rule.
	kindEndpoint
	// kindPlain: a plain HTTP proxy request.
	kindPlain
)

// target is the host that a request goes to, from the CONNECT, the endpoint
// or the plain request, and never from a header of the request.
type target struct {
	host string
	port int
	kind kind
}

// plain reports whether the proxy sends the request with no TLS.
func (t target) plain() bool { return t.kind == kindPlain }

// modeServes reports whether a proxy rule of mode m serves a request that
// reached the proxy as k.
func modeServes(k kind, m jobproxy.Mode) bool {
	switch k {
	case kindInspect:
		return m == jobproxy.ModeInspect
	case kindEndpoint:
		return m == jobproxy.ModeEndpoint
	case kindPlain:
		return m == jobproxy.ModeInspect || m == jobproxy.ModeTunnel
	default:
		return false
	}
}

// authority returns the Host header value for host and port.
func authority(t target) string {
	def := defaultTLSPort
	if t.plain() {
		def = defaultHTTPPort
	}

	if t.port == def {
		return t.host
	}

	return hostPort(t.host, t.port)
}

// statusWriter keeps the status and the byte count of a response for the
// record.
type statusWriter struct {
	http.ResponseWriter

	status int
	n      int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	n, err := w.ResponseWriter.Write(b)
	w.n += int64(n)

	return same(n, err)
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// refusal is the answer to a request that the proxy does not forward.
type refusal struct {
	status int
	reason Reason
	body   string
}

// newRefusal builds a refusal with the fixed body of its reason.
func newRefusal(status int, reason Reason) *refusal {
	return &refusal{status: status, reason: reason, body: "blocked by the job proxy: " + string(reason)}
}

// admitted is a request that the proxy rules allow.
type admitted struct {
	rule     jobproxy.RenderedRule
	path     string
	query    string
	hasQuery bool
}

// outbound is the request for the host with the scan of its response.
type outbound struct {
	req  *http.Request
	scan *scanner
}

// forward serves one request that reached the proxy as t.kind. It reserves
// the slot of the record first.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, t target) {
	if !p.begin(w) {
		return
	}

	p.forwardReserved(w, r, t, r.RequestURI)
}

// forwardReserved serves a request. The slot of its record is reserved.
func (p *Proxy) forwardReserved(rw http.ResponseWriter, r *http.Request, t target, pathQuery string) {
	w := &statusWriter{ResponseWriter: rw, status: 0, n: 0}
	rec := p.newRecord(r.Method, t.host, p.byHost[hostKey{host: t.host, port: t.port}])

	defer func() {
		rec.Status, rec.Bytes = w.status, w.n
		p.finish(&rec)
	}()

	if ref := p.exchange(w, r, t, pathQuery, &rec); ref != nil {
		rec.Reason = ref.reason
		refuse(w, ref.status, ref.body)
	}
}

// exchange checks the request, sends it to the host and relays the response.
// It returns a refusal when nothing was sent to the client yet.
func (p *Proxy) exchange(w *statusWriter, r *http.Request, t target, pathQuery string, rec *Record) *refusal {
	adm, ref := p.admit(r, t, pathQuery, rec)
	if ref != nil {
		return ref
	}

	rec.Allowed = true

	if r.ContentLength > p.cfg.Limits.MaxRequestBody {
		return newRefusal(http.StatusRequestEntityTooLarge, ReasonBodyTooLarge)
	}

	out := &outbound{req: &http.Request{}, scan: nil}
	if bref := p.buildRequest(r, t, adm, out); bref != nil {
		return bref
	}

	tr, err := p.transport(adm.rule, t.kind)
	if err != nil {
		return newRefusal(http.StatusBadGateway, ReasonAuthUnavailable)
	}

	out.req.Body = http.MaxBytesReader(w, r.Body, p.cfg.Limits.MaxRequestBody)

	resp, err := tr.RoundTrip(out.req.WithContext(r.Context()))
	if err != nil {
		reason, status := upstreamReason(err)

		return newRefusal(status, reason)
	}
	defer p.shut(resp.Body)

	if reason := checkResponse(resp, out.scan); reason != ReasonNone {
		return newRefusal(http.StatusBadGateway, reason)
	}

	relay(w, resp, out.scan, rec)

	return nil
}

// validQuery reports whether q has only printable ASCII bytes without space.
func validQuery(q string) bool {
	for i := range len(q) {
		if q[i] <= ' ' || q[i] >= 0x7f {
			return false
		}
	}

	return true
}

// hostHeaderOK reports whether the Host header (or :authority) is the host of
// the target, byte for byte, with or without the port.
func hostHeaderOK(got string, t target) bool {
	return got == t.host || got == hostPort(t.host, t.port)
}

// denyText returns the fixed text of a denial for the body.
func denyText(reason jobproxy.Reason) string {
	if reason == jobproxy.ReasonPlainHTTPInjection {
		return "credential over plain HTTP"
	}

	if reason == jobproxy.ReasonNoRuleHost {
		return "no rule names the host"
	}

	return "no rule matched"
}

// admit applies the layers that decide whether the request may go on: the
// shape of the request line, the name binding, the path and the proxy rules.
func (p *Proxy) admit(r *http.Request, t target, pathQuery string, rec *Record) (admitted, *refusal) {
	var none admitted

	if !strings.HasPrefix(pathQuery, "/") || r.Method == http.MethodConnect {
		return none, newRefusal(http.StatusBadRequest, ReasonBadRequest)
	}

	path, query, hasQuery := strings.Cut(pathQuery, "?")
	if !validQuery(query) {
		return none, newRefusal(http.StatusBadRequest, ReasonBadRequest)
	}

	// Layer: the name binding. The Host header (:authority in HTTP/2) must be
	// the host of the CONNECT. A plain proxy request has the host in its
	// target, and the proxy writes its own Host header.
	if !t.plain() && !hostHeaderOK(r.Host, t) {
		return none, newRefusal(http.StatusMisdirectedRequest, ReasonHostMismatch)
	}

	// Layer: the path. The proxy never normalizes: it refuses.
	if _, ok := jobproxy.CanonicalPath(path); !ok {
		return none, newRefusal(http.StatusBadRequest, ReasonPathNotCanonical)
	}

	rec.Path = path

	dec := jobproxy.Match(p.cfg.Rendered, jobproxy.Request{
		Operation: p.cfg.Operation, Host: t.host, Port: t.port,
		Method: r.Method, Path: path, PlainHTTP: t.plain(),
	})
	rec.RuleID = dec.RuleID

	if !dec.Allowed {
		ref := newRefusal(http.StatusForbidden, Reason(dec.Reason))
		ref.body = "blocked by the job proxy: " + r.Method + " " + t.host + path + ": " + denyText(dec.Reason)

		return none, ref
	}

	rule := p.byID[dec.RuleID]
	rec.Mode = rule.Mode

	if !modeServes(t.kind, rule.Mode) {
		return none, newRefusal(http.StatusForbidden, ReasonModeNotAllowed)
	}

	return admitted{rule: rule, path: path, query: query, hasQuery: hasQuery}, nil
}

// buildRequest builds the request for the host: no hop-by-hop header, the
// host and the path that the proxy checked, and the credential. It fills out
// and returns nil, or returns the refusal.
func (p *Proxy) buildRequest(r *http.Request, t target, adm admitted, out *outbound) *refusal {
	unescaped, err := url.PathUnescape(adm.path)
	if err != nil {
		return newRefusal(http.StatusBadRequest, ReasonPathNotCanonical)
	}

	h := r.Header.Clone()
	stripHopByHop(h)
	h.Del("Expect")
	// The proxy writes the Host header from the checked target.
	h.Del("Host")

	query, hasQuery := adm.query, adm.hasQuery

	var scan *scanner

	if adm.rule.Credential != "" {
		cred, cerr := p.credential(adm.rule.Credential)
		if cerr != nil || cred.Kind == KindClientCertificate {
			return newRefusal(http.StatusBadGateway, ReasonAuthUnavailable)
		}

		newQuery, aerr := cred.apply(h, query)
		if aerr != nil {
			return newRefusal(http.StatusBadGateway, ReasonAuthUnavailable)
		}

		query, hasQuery = newQuery, hasQuery || newQuery != ""
		scan = newScanner(cred.secrets())

		// A compressed response would hide the credential from the scan.
		h.Set("Accept-Encoding", "identity")
	}

	// No User-Agent from the transport: the client's own header or none.
	if _, ok := h["User-Agent"]; !ok {
		h["User-Agent"] = nil
	}

	scheme := "https"
	if t.plain() {
		scheme = "http"
	}

	auth := authority(t)
	u := &url.URL{
		Scheme: scheme, Host: auth, Path: unescaped, RawPath: adm.path,
		RawQuery: query, ForceQuery: hasQuery && query == "",
	}

	out.req = &http.Request{
		Method: r.Method, URL: u, Host: auth, Header: h, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		ContentLength: r.ContentLength,
	}
	out.scan = scan

	return nil
}

// credential gets a credential by name and checks its fields.
func (p *Proxy) credential(name string) (Credential, error) {
	if p.cfg.Credentials == nil {
		return noCredential(), ErrCredential
	}

	c, err := p.cfg.Credentials.Credential(name)
	if err != nil {
		// The error text of a credential source is never kept.
		return noCredential(), ErrCredential
	}

	if err := c.validate(); err != nil {
		return noCredential(), err
	}

	return c, nil
}

// errClientCertificate marks the failure to load a client certificate or a
// CA pool, and a transport request after Close.
var errClientCertificate = errors.New("the client certificate or the CA is not available")

// upstreamReason maps the error of a round trip to a fixed reason.
func upstreamReason(err error) (Reason, int) {
	var tooBig *http.MaxBytesError

	var ne net.Error

	switch {
	case errors.As(err, &tooBig):
		return ReasonBodyTooLarge, http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrGuardRefused), errors.Is(err, ErrResolveFailed), errors.Is(err, ErrDialFailed):
		return dialReason(err)
	case errors.Is(err, errClientCertificate):
		return ReasonAuthUnavailable, http.StatusBadGateway
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return ReasonTimeout, http.StatusGatewayTimeout
	default:
		return ReasonUpstreamFailed, http.StatusBadGateway
	}
}

// checkResponse refuses a response that the job proxy must not pass: an
// upgrade, a response of a rule with a credential that is encoded, and a
// response whose header holds a form of the credential.
func checkResponse(resp *http.Response, scan *scanner) Reason {
	if resp.StatusCode == http.StatusSwitchingProtocols {
		return ReasonUpstreamFailed
	}

	if scan == nil {
		return ReasonNone
	}

	if ce := resp.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		return ReasonEncodedResponse
	}

	for name, values := range resp.Header {
		if scan.holds([]byte(name)) {
			return ReasonAuthInResponse
		}

		for _, v := range values {
			if scan.holds([]byte(v)) {
				return ReasonAuthInResponse
			}
		}
	}

	return ReasonNone
}

// relay sends the response to the client. A redirect passes unchanged. When
// the scan finds a form of the credential, the stream breaks: the handler
// aborts, so the client sees a failed transfer and not a short success. The
// deferred call of forwardReserved writes the one record.
func relay(w *statusWriter, resp *http.Response, scan *scanner, rec *Record) {
	h := resp.Header.Clone()
	stripHopByHop(h)
	maps.Copy(w.Header(), h)

	w.WriteHeader(resp.StatusCode)

	var src io.Reader = resp.Body
	if scan != nil {
		src = scan.reader(resp.Body)
	}

	rc := http.NewResponseController(w)
	buf := make([]byte, copyChunk)

	for {
		n, err := src.Read(buf)
		if n > 0 && !emit(w, rc, buf[:n]) {
			rec.Reason = ReasonUpstreamFailed

			panic(http.ErrAbortHandler)
		}

		if err == nil {
			continue
		}

		if errors.Is(err, io.EOF) {
			return
		}

		rec.Reason = ReasonUpstreamFailed
		if errors.Is(err, ErrAuthInResponse) {
			rec.Reason = ReasonAuthInResponse
		}

		panic(http.ErrAbortHandler)
	}
}

// emit writes a chunk to the client and flushes it. It returns false when the
// client is gone.
func emit(w io.Writer, rc *http.ResponseController, b []byte) bool {
	if _, err := w.Write(b); err != nil {
		return false
	}

	err := rc.Flush()

	return err == nil || errors.Is(err, http.ErrNotSupported)
}

// transport returns the transport of a proxy rule. It dials only the host and
// port of the rule, with the outbound guard, whatever the request says.
func (p *Proxy) transport(rule jobproxy.RenderedRule, k kind) (*http.Transport, error) {
	plain := k == kindPlain
	key := rule.ID + "|" + strconv.FormatBool(plain)

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, errClientCertificate
	}

	if t, ok := p.transports[key]; ok {
		return t, nil
	}

	lim := p.cfg.Limits
	tr := &http.Transport{
		Proxy:                  nil,
		DisableCompression:     true,
		ForceAttemptHTTP2:      true,
		MaxIdleConnsPerHost:    maxIdleHostConns,
		IdleConnTimeout:        hostIdleTimeout,
		ResponseHeaderTimeout:  lim.ResponseHeaderTimeout,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
	}

	dialGuarded := func(ctx context.Context) (net.Conn, error) {
		c, err := p.guard.DialContext(ctx, rule.Host, rule.Port)
		if err != nil {
			return nil, err
		}

		return &idleConn{Conn: c, idle: lim.IdleTimeout}, nil
	}

	if plain {
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return dialGuarded(ctx) }
		p.transports[key] = tr

		return tr, nil
	}

	cfg, err := p.upstreamTLS(rule)
	if err != nil {
		return nil, err
	}

	tr.DialTLSContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		raw, derr := dialGuarded(ctx)
		if derr != nil {
			return nil, derr
		}

		tc := tls.Client(raw, cfg)

		hctx, cancel := context.WithTimeout(ctx, lim.DialTimeout)
		defer cancel()

		if herr := tc.HandshakeContext(hctx); herr != nil {
			p.shut(raw)

			return nil, ErrDialFailed
		}

		return tc, nil
	}
	p.transports[key] = tr

	return tr, nil
}

// upstreamTLS builds the TLS configuration for the host of a proxy rule: the
// name of the rule, the roots of its server trust and its client certificate.
func (p *Proxy) upstreamTLS(rule jobproxy.RenderedRule) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: rule.Host,
		NextProtos: []string{"h2", "http/1.1"},
	}

	if rule.ServerTrust == jobproxy.ServerTrustSystem {
		cfg.RootCAs = p.cfg.SystemRoots
	} else {
		if p.cfg.Trust == nil {
			return nil, errClientCertificate
		}

		pool, err := p.cfg.Trust.Pool(rule.ServerTrust)
		if err != nil || pool == nil {
			return nil, errClientCertificate
		}

		cfg.RootCAs = pool
	}

	if rule.ClientCertificate != "" {
		c, err := p.credential(rule.ClientCertificate)
		if err != nil || c.Kind != KindClientCertificate {
			return nil, errClientCertificate
		}

		cfg.Certificates = []tls.Certificate{*c.Certificate}
	}

	return cfg, nil
}
