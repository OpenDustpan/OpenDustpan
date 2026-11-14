package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// ErrNotLoopback means that a URL does not name a loopback address. The
// harness talks to loopback addresses only.
var ErrNotLoopback = errors.New("conformance: the address is not a loopback address")

// ErrPointer means that a fault names a JSON pointer that does not fit the
// answer of the server.
var ErrPointer = errors.New("conformance: the JSON pointer does not fit the answer")

// ErrBodyTooLarge means that a request or an answer body is longer than
// maxProxyBodyBytes. The harness refuses the body and never cuts it.
var ErrBodyTooLarge = errors.New("conformance: the body is longer than the limit")

// maxProxyBodyBytes is the largest request or answer body that the harness reads.
const maxProxyBodyBytes = 16 << 20

// readLimited reads a body up to maxProxyBodyBytes. It reads one byte more, so
// that a longer body is an error and not a body that is cut in silence.
func readLimited(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxProxyBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the body: %w", err)
	}

	if len(body) > maxProxyBodyBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrBodyTooLarge, maxProxyBodyBytes)
	}

	return body, nil
}

// loopbackClient returns an HTTP client that connects to a loopback address
// only. The check is on the address of each connection, when it opens, so a
// host name that resolves to another address is refused. The proxy settings of
// the environment do not apply.
func loopbackClient(timeout time.Duration) *http.Client {
	dialer := new(net.Dialer)
	dialer.Timeout = timeout
	// Defense in depth: loopbackURL checks the name of the host too. This
	// check stays so that a name that resolves to another address, or a
	// redirect, cannot reach a host that is not on this machine.
	dialer.ControlContext = func(_ context.Context, _, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: %q", ErrNotLoopback, address)
		}

		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("%w: %q", ErrNotLoopback, address)
		}

		return nil
	}

	return &http.Client{
		Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext},
		Timeout:   timeout,
	}
}

// authorizationHeader is the request header that holds the token.
const authorizationHeader = "Authorization"

// recordedHeaders returns the request headers that an Exchange keeps.
func recordedHeaders() []string {
	return []string{authorizationHeader, "Idempotency-Key", "If-Match", "Content-Type", "User-Agent"}
}

// ProxyOptions are the settings of NewProxy.
type ProxyOptions struct {
	// Upstream is the base URL of the server, on a loopback address. The proxy
	// forwards to it and to no other host.
	Upstream string
	// Operations name the routes, so that a Fault finds its exchange by the
	// operation ID.
	Operations []model.Operation
}

// RecordingProxy implements Proxy. Start it with NewProxy.
type RecordingProxy struct {
	upstream   *url.URL
	operations []model.Operation
	client     *http.Client
	main       *listener
	other      *listener

	mu        sync.Mutex
	exchanges []Exchange
	others    []Exchange
	faults    []Fault
}

// NewProxy starts the proxy on two loopback ports. The context bounds the
// opening of the ports only.
func NewProxy(ctx context.Context, opts ProxyOptions) (*RecordingProxy, error) {
	up, err := loopbackURL(opts.Upstream)
	if err != nil {
		return nil, err
	}

	p := new(RecordingProxy)
	p.upstream, p.operations = up, opts.Operations
	p.client = loopbackClient(DefaultCallTimeout)
	// The proxy gives a redirect to the client as it is.
	p.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	if p.main, err = serve(ctx, "127.0.0.1", http.HandlerFunc(p.handle)); err != nil {
		return nil, err
	}

	if p.other, err = serve(ctx, "localhost", http.HandlerFunc(p.handleOther)); err != nil {
		if closeErr := p.main.close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}

		return nil, err
	}

	return p, nil
}

// Close stops both listeners.
func (p *RecordingProxy) Close() error {
	p.client.CloseIdleConnections()

	return errors.Join(p.main.close(), p.other.close())
}

// URL implements Proxy.
func (p *RecordingProxy) URL() string { return p.main.url }

// OtherURL implements Proxy.
func (p *RecordingProxy) OtherURL() string { return p.other.url }

// Inject implements Proxy.
func (p *RecordingProxy) Inject(f Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.faults = append(p.faults, f)
}

// Exchanges implements Proxy.
func (p *RecordingProxy) Exchanges() []Exchange {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.exchanges)
}

// OtherExchanges implements Proxy.
func (p *RecordingProxy) OtherExchanges() []Exchange {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.others)
}

// Reset implements Proxy.
func (p *RecordingProxy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.exchanges, p.others, p.faults = nil, nil, nil
}

// handleOther records a request on the second listener and answers 204.
func (p *RecordingProxy) handleOther(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "proxy: "+err.Error(), refusalStatus(err))

		return
	}

	w.WriteHeader(http.StatusNoContent)

	p.mu.Lock()
	p.others = append(p.others, exchangeOf(r, body, http.StatusNoContent))
	p.mu.Unlock()
}

// handle forwards one request to the server, or applies a fault.
func (p *RecordingProxy) handle(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "proxy: "+err.Error(), refusalStatus(err))

		return
	}

	status := p.respond(w, r, body, p.takeFault(r))

	p.mu.Lock()
	p.exchanges = append(p.exchanges, exchangeOf(r, body, status))
	p.mu.Unlock()
}

// refusalStatus is the status for a request that the proxy cannot read: 413
// for a body over the limit, and 400 for another fault.
func refusalStatus(err error) int {
	if errors.Is(err, ErrBodyTooLarge) {
		return http.StatusRequestEntityTooLarge
	}

	return http.StatusBadRequest
}

// readBody reads the body of a request up to the limit.
func readBody(r *http.Request) ([]byte, error) {
	body, err := readLimited(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read the request: %w", err)
	}

	return body, nil
}

// operationOf finds the operation ID of a request, or "" when no route fits.
func (p *RecordingProxy) operationOf(r *http.Request) string {
	segments := strings.Split(r.URL.EscapedPath(), "/")

	for i := range p.operations {
		if routeMatches(&p.operations[i], r.Method, segments) {
			return p.operations[i].ID
		}
	}

	return ""
}

// routeMatches says that an operation has the method and the path segments. A
// wildcard of the route matches one segment that is not empty.
func routeMatches(op *model.Operation, method string, segments []string) bool {
	if string(op.Method) != method {
		return false
	}

	tmpl := strings.Split(op.Path, "/")
	if len(tmpl) != len(segments) {
		return false
	}

	for i, t := range tmpl {
		wildcard := strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}")
		if wildcard && segments[i] == "" {
			return false
		}

		if !wildcard && t != segments[i] {
			return false
		}
	}

	return true
}

// takeFault removes the first queued fault for the operation of the request.
// It returns nil when there is none.
func (p *RecordingProxy) takeFault(r *http.Request) *Fault {
	op := p.operationOf(r)

	p.mu.Lock()
	defer p.mu.Unlock()

	i := slices.IndexFunc(p.faults, func(f Fault) bool { return f.Operation == op })
	if i < 0 {
		return nil
	}

	f := p.faults[i]
	p.faults = slices.Delete(p.faults, i, i+1)

	return &f
}

// respond writes the answer and returns its status.
func (p *RecordingProxy) respond(w http.ResponseWriter, r *http.Request, body []byte, f *Fault) int {
	if f != nil {
		if status, done := p.answerWithoutServer(w, r, f); done {
			return status
		}
	}

	up, err := p.forward(r, body)
	if err != nil {
		http.Error(w, "proxy: "+err.Error(), http.StatusBadGateway)

		return http.StatusBadGateway
	}

	if f != nil && f.Kind == FaultDropAnswer {
		dropConnection(w)

		return up.status
	}

	if f != nil && (f.Kind == FaultAddField || f.Kind == FaultEnumValue) {
		edited, editErr := editJSON(up.body, *f)
		if editErr != nil {
			http.Error(w, "proxy: "+editErr.Error(), http.StatusBadGateway)

			return http.StatusBadGateway
		}

		up.body = edited
	}

	return writeUpstream(w, up)
}

// answerWithoutServer answers the faults that do not ask the server. It
// returns false for a fault that needs the answer of the server.
func (p *RecordingProxy) answerWithoutServer(w http.ResponseWriter, r *http.Request, f *Fault) (int, bool) {
	switch f.Kind {
	case FaultStatus:
		return writeFaultStatus(w, *f), true
	case FaultRedirect:
		w.Header().Set("Location", p.other.url+r.URL.RequestURI())
		w.WriteHeader(http.StatusTemporaryRedirect)

		return http.StatusTemporaryRedirect, true
	case FaultDropAnswer, FaultAddField, FaultEnumValue:
		return 0, false
	default:
		http.Error(w, "proxy: unknown fault kind "+strconv.Itoa(int(f.Kind)), http.StatusInternalServerError)

		return http.StatusInternalServerError, true
	}
}

// writeFaultStatus answers the status of a FaultStatus.
func writeFaultStatus(w http.ResponseWriter, f Fault) int {
	if f.ContentType != "" {
		w.Header().Set("Content-Type", f.ContentType)
	}

	if f.RetryAfter != "" {
		w.Header().Set("Retry-After", f.RetryAfter)
	}

	w.WriteHeader(f.Status)

	if _, err := w.Write(f.Body); err != nil {
		return f.Status
	}

	return f.Status
}

// dropConnection closes the connection with no answer.
func dropConnection(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}

	if conn, _, err := hj.Hijack(); err == nil {
		if closeErr := conn.Close(); closeErr != nil {
			return
		}
	}
}

// upstreamAnswer is the answer of the server.
type upstreamAnswer struct {
	status int
	header http.Header
	body   []byte
}

// forward sends the request to the fixed upstream. It never uses a host from
// the request.
func (p *RecordingProxy) forward(r *http.Request, body []byte) (upstreamAnswer, error) {
	var up upstreamAnswer

	target, err := p.target(r)
	if err != nil {
		return up, err
	}

	//nolint:gosec // G704: the target is on the fixed upstream.
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		return up, fmt.Errorf("new request: %w", err)
	}

	nominated := connectionNominated(r.Header)

	for name, values := range r.Header {
		if !hopByHop(name, nominated) {
			req.Header[name] = slices.Clone(values)
		}
	}

	resp, err := p.client.Do(req) //nolint:gosec // G704: the target is on the fixed upstream.
	if err != nil {
		return up, fmt.Errorf("forward: %w", err)
	}

	defer resp.Body.Close() //nolint:errcheck // The answer is read below.

	up.status, up.header = resp.StatusCode, resp.Header

	if up.body, err = readLimited(resp.Body); err != nil {
		return up, fmt.Errorf("read the answer of the server: %w", err)
	}

	return up, nil
}

// target joins the fixed upstream and the path and query of a request. The
// path keeps the escapes that the client sent.
func (p *RecordingProxy) target(r *http.Request) (string, error) {
	rest, err := url.ParseRequestURI(r.URL.RequestURI())
	if err != nil {
		return "", fmt.Errorf("request URI: %w", err)
	}

	target := *p.upstream
	target.Path = strings.TrimRight(p.upstream.Path, "/") + rest.Path
	target.RawPath = strings.TrimRight(p.upstream.EscapedPath(), "/") + rest.EscapedPath()
	target.RawQuery = rest.RawQuery

	return target.String(), nil
}

// connectionNominated returns the header names that the Connection header of h
// nominates, in lower case. A message can hold the header more than one time,
// and each value can hold a comma-separated list.
func connectionNominated(h http.Header) map[string]bool {
	nominated := make(map[string]bool)

	for name, values := range h {
		if strings.EqualFold(name, "Connection") {
			for _, value := range values {
				for _, field := range connectionNames(value) {
					nominated[field] = true
				}
			}
		}
	}

	return nominated
}

// connectionNames returns the header names of one Connection value, in lower
// case.
func connectionNames(value string) []string {
	var names []string

	for field := range strings.SplitSeq(value, ",") {
		if field = strings.TrimSpace(field); field != "" {
			names = append(names, strings.ToLower(field))
		}
	}

	return names
}

// hopByHop says that a header belongs to one connection: it is a fixed header,
// or a header that the Connection header of the message nominated.
func hopByHop(name string, nominated map[string]bool) bool {
	if nominated[strings.ToLower(name)] {
		return true
	}

	switch http.CanonicalHeaderKey(name) {
	case "Connection",
		"Keep-Alive",
		"Proxy-Connection",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
		"Content-Length":
		return true
	default:
		return false
	}
}

// writeUpstream copies the answer of the server to the client.
func writeUpstream(w http.ResponseWriter, up upstreamAnswer) int {
	nominated := connectionNominated(up.header)

	for name, values := range up.header {
		if !hopByHop(name, nominated) {
			w.Header()[name] = slices.Clone(values)
		}
	}

	w.Header().Set("Content-Length", strconv.Itoa(len(up.body)))
	w.WriteHeader(up.status)

	if _, err := w.Write(up.body); err != nil {
		return up.status
	}

	return up.status
}

// exchangeOf records a request.
func exchangeOf(r *http.Request, body []byte, status int) Exchange {
	headers := make(map[string]string)

	for _, name := range recordedHeaders() {
		v := r.Header.Get(name)
		if v == "" {
			continue
		}

		if name == authorizationHeader {
			// The token never goes into a record.
			scheme, _, _ := strings.Cut(v, " ")
			v = scheme + " <redacted>"
		}

		headers[name] = v
	}

	return Exchange{
		Method: r.Method, Path: r.URL.EscapedPath(), RawQuery: r.URL.RawQuery,
		Headers: headers, Body: body, Status: status, At: time.Now(),
	}
}

// loopbackURL parses a base URL and checks that its host is a loopback address.
func loopbackURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%w: %q", ErrNotLoopback, raw)
	}

	host := u.Hostname()
	if ip := net.ParseIP(host); (ip == nil || !ip.IsLoopback()) && host != "localhost" {
		return nil, fmt.Errorf("%w: %q", ErrNotLoopback, raw)
	}

	return u, nil
}

// listener is one HTTP server of the proxy.
type listener struct {
	srv  *http.Server
	url  string
	done chan struct{}
}

// serve listens on a free loopback port. The URL uses name as the host name.
func serve(ctx context.Context, name string, h http.Handler) (*listener, error) {
	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("proxy listen: %w", err)
	}

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("proxy address: %w", err), ln.Close())
	}

	l := &listener{
		srv:  &http.Server{Handler: h, ReadHeaderTimeout: DefaultCallTimeout},
		url:  "http://" + net.JoinHostPort(name, port),
		done: make(chan struct{}),
	}

	go func() {
		defer close(l.done)

		// Serve ends with ErrServerClosed at close, and with the error of the
		// listener otherwise. Either way the listener is done.
		if serveErr := l.srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return
		}
	}()

	return l, nil
}

// close stops the listener, drops its connections, and waits for its
// goroutine. Nothing is in flight when the run ends.
func (l *listener) close() error {
	err := l.srv.Close()

	<-l.done

	if err != nil {
		return fmt.Errorf("stop the listener: %w", err)
	}

	return nil
}

// editJSON writes Fault.Value at the JSON pointer Fault.Field of a JSON body.
func editJSON(body []byte, f Fault) ([]byte, error) {
	doc, err := decodeNumbers(body)
	if err != nil {
		return nil, fmt.Errorf("the answer is not JSON: %w", err)
	}

	value, err := decodeNumbers(f.Value)
	if err != nil {
		return nil, fmt.Errorf("the fault value is not JSON: %w", err)
	}

	mode := modeAdd
	if f.Kind == FaultEnumValue {
		mode = modeReplace
	}

	doc, err = setPointer(doc, f.Field, value, mode)
	if err != nil {
		return nil, err
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode the edited answer: %w", err)
	}

	return out, nil
}

// decodeNumbers decodes one JSON value and keeps the numbers exact.
func decodeNumbers(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	return v, nil
}

// editMode says whether an edit adds a member or replaces one.
type editMode int

const (
	_ editMode = iota // Zero means that nobody chose.
	// modeAdd sets a member, and makes it when it is not there.
	modeAdd
	// modeReplace sets a member that is there.
	modeReplace
)

// setPointer sets the value at a JSON pointer (RFC 6901).
func setPointer(doc any, pointer string, value any, mode editMode) (any, error) {
	if pointer == "" {
		return value, nil
	}

	rest, ok := strings.CutPrefix(pointer, "/")
	if !ok {
		return nil, fmt.Errorf("%w: %q does not start with a slash", ErrPointer, pointer)
	}

	tokens := strings.Split(rest, "/")
	for i, t := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}

	cur := doc

	for _, t := range tokens[:len(tokens)-1] {
		var err error
		if cur, err = child(cur, t); err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrPointer, pointer, err)
		}
	}

	if err := assign(cur, tokens[len(tokens)-1], value, mode); err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrPointer, pointer, err)
	}

	return doc, nil
}

// errStep is the reason that a step of a pointer fails.
var errStep = errors.New("the step does not fit the value")

// child goes one token into a container.
func child(cur any, token string) (any, error) {
	switch node := cur.(type) {
	case map[string]any:
		v, present := node[token]
		if node == nil || !present {
			return nil, fmt.Errorf("%w: no member %q", errStep, token)
		}

		return v, nil
	case []any:
		idx, err := itemIndex(token, len(node))
		if err != nil || node == nil {
			return nil, fmt.Errorf("%w: no item %q", errStep, token)
		}

		return node[idx], nil
	default:
		return nil, fmt.Errorf("%w: %q is not in a container", errStep, token)
	}
}

// assign sets the value at the last token of a pointer.
func assign(cur any, token string, value any, mode editMode) error {
	switch node := cur.(type) {
	case map[string]any:
		if node == nil {
			return fmt.Errorf("%w: the object is null", errStep)
		}

		if _, present := node[token]; !present && mode == modeReplace {
			return fmt.Errorf("%w: no member %q", errStep, token)
		}

		node[token] = value

		return nil
	case []any:
		idx, err := itemIndex(token, len(node))
		if err != nil || node == nil {
			return fmt.Errorf("%w: no item %q", errStep, token)
		}

		node[idx] = value

		return nil
	default:
		return fmt.Errorf("%w: %q is not in a container", errStep, token)
	}
}

// itemIndex reads the index of an item of a list of a given length.
func itemIndex(token string, length int) (int, error) {
	idx, err := strconv.Atoi(token)
	if err != nil || idx < 0 || idx >= length {
		return 0, fmt.Errorf("%w: no item %q", errStep, token)
	}

	return idx, nil
}
