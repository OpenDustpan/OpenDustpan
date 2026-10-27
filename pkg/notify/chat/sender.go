package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/internal/notify/post"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
)

// SendTimeout is the longest time of one send, as a deadline on the context
// of the request. The timeout of the client is a second layer above it.
const SendTimeout = 10 * time.Second

// op names a send in the error texts.
const op = "post chat message"

// DefaultProduct is the product name of a message when no option sets one.
const DefaultProduct = "OpenDustpan"

// Sender is the notify.Sender of the kind notify.SinkChat. It is the only code
// that opens a connection for a chat message. A Renderer of a platform builds
// the request and reads the answer; the Sender does the rest, so a platform
// cannot skip a layer of the outbound rules. It is safe for concurrent use.
// Call Close when done, to close the idle connections.
type Sender struct {
	client    *http.Client
	renderers map[Platform]Renderer
	ports     egress.Ports
	product   string
	now       func() time.Time
	timeout   time.Duration
}

var _ notify.Sender = (*Sender)(nil)

// Option changes a Sender.
type Option func(*Sender)

// WithPorts sets the ports that the sender sends to. Without it, the sender
// sends to the port 443 only.
func WithPorts(p egress.Ports) Option {
	return func(s *Sender) { s.ports = p }
}

// WithProduct sets the product name that a message shows. Without it, the
// name is DefaultProduct.
func WithProduct(name string) Option {
	return func(s *Sender) { s.product = name }
}

// NewSender returns a Sender for the platforms of renderers, with the egress
// rules of a chat webhook: https only, no redirect, the dial guard on every
// connection and no proxy. A later renderer of one platform replaces an
// earlier one.
func NewSender(renderers []Renderer, opts ...Option) *Sender {
	s := assemble(egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly), time.Now, renderers)

	for _, o := range opts {
		o(s)
	}

	return s
}

func assemble(c *http.Client, now func() time.Time, renderers []Renderer) *Sender {
	s := &Sender{
		client:    c,
		renderers: map[Platform]Renderer{},
		ports:     egress.Ports{},
		product:   DefaultProduct,
		now:       now,
		timeout:   SendTimeout,
	}

	for _, r := range renderers {
		s.renderers[r.Platform()] = r
	}

	return s
}

// Close closes the idle connections of the sender.
func (s *Sender) Close() { s.client.CloseIdleConnections() }

// Kind returns notify.SinkChat.
func (*Sender) Kind() notify.SinkKind { return notify.SinkChat }

// Serves reports whether the sender has a renderer for the platform of sink.
// The test route of a sink asks it, so a platform with no adapter yet answers
// that the server has no sender and not that the receiver refused.
func (s *Sender) Serves(sink notify.Sink) bool {
	_, ok := s.renderers[Platform(sink.Platform)]

	return sink.Kind == notify.SinkChat && ok
}

// TakesAuthSecret reports whether the renderer of platform needs an access
// token besides the URL (see AuthRenderer). It reports false for a platform
// with no renderer. The sink service asks it when a person saves a sink.
func (s *Sender) TakesAuthSecret(platform string) bool {
	r, ok := s.renderers[Platform(platform)]
	if !ok {
		return false
	}

	a, ok := r.(AuthRenderer)

	return ok && a.AuthSecret()
}

// CheckHost returns an error that matches notify.ErrInvalid when the renderer
// of platform does not take host as a webhook host. It returns nil for a
// platform with no renderer: the sender cannot judge it. The sink service calls
// it when a person saves a sink.
func (s *Sender) CheckHost(platform, host string) error {
	r, ok := s.renderers[Platform(platform)]
	if !ok {
		return nil
	}

	return r.CheckHost(host)
}

// Send posts m.Notification to the webhook URL m.To. For a platform with an
// AuthRenderer, the first value of m.Secrets is the access token. A 2xx answer that the
// renderer accepts means the platform holds the message. It makes one attempt.
// Send returns an error that matches:
//   - egress.ErrRefused for a URL, a port or a host that the rules refuse, for
//     a host that is not the target of the sink or not a host of the platform,
//     and for a redirect. A name that resolves to an address that the rules
//     refuse is a failed connection, so an answer does not tell such a name
//     from a name that does not resolve;
//   - notify.ErrPermanent for a message that is not for a chat sink or for a
//     platform with no renderer, for a 4xx answer other than 408 and 429, for
//     a 2xx answer that the renderer refuses or that is too large, and for any
//     other answer that will not change;
//   - a *notify.RetryAfterError for an answer 408, 429 or 5xx with a
//     Retry-After header that notify.ParseRetryAfter reads;
//   - notify.ErrTemporary for every other failure.
//
// An error never holds the URL, the network error, the body of an answer or a
// secret. A message has no action button: m.Actions is not used, because a
// webhook cannot prove that it belongs to the application of the installation.
func (s *Sender) Send(ctx context.Context, m notify.Message) error {
	r, err := s.renderer(m)
	if err != nil {
		return err
	}

	target, err := s.checkTarget(m, r)
	if err != nil {
		return err
	}

	req, err := r.Render(s.content(m.Notification))
	if err != nil {
		return fmt.Errorf("%w: the message does not render", notify.ErrPermanent)
	}

	httpReq, err := s.request(ctx, target, req)
	if err != nil {
		return err
	}

	if authErr := authorize(r, httpReq, m.Secrets); authErr != nil {
		return authErr
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	resp, err := s.client.Do(httpReq.WithContext(ctx))
	if err != nil {
		return post.TransportError(ctx, op, err)
	}

	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // The status decides the result.

	return s.answer(r, resp)
}

// renderer returns the renderer of the platform of m, or ErrPermanent for a
// message that is not for a chat sink or a platform with no renderer.
//
//nolint:ireturn // The renderer is the port of a platform.
func (s *Sender) renderer(
	m notify.Message,
) (Renderer, error) {
	if m.Sink.Kind != notify.SinkChat {
		return nil, fmt.Errorf("%w: the message is for a %q sink, not a chat sink", notify.ErrPermanent, m.Sink.Kind)
	}

	r, ok := s.renderers[Platform(m.Sink.Platform)]
	if !ok {
		return nil, fmt.Errorf("%w: this server has no adapter for the chat platform %q", notify.ErrPermanent,
			m.Sink.Platform)
	}

	return r, nil
}

// checkTarget parses and checks m.To and returns it. Defense in depth: the
// sink service checks the URL and the port when a person saves a sink and
// before a test, and the dial guard checks the address of each connection.
// These checks stay so that a caller of the sender that skips the service, or
// a sink that a restore left behind, cannot send to an address that the rules
// refuse, to a port that the operator did not allow, or to a host that is not
// the target of the sink.
func (s *Sender) checkTarget(m notify.Message, r Renderer) (*url.URL, error) {
	if err := post.CheckTarget(op, m.To, m.Sink.Target, s.ports, notify.ChatHost); err != nil {
		return nil, err
	}

	// CheckTarget read the host and found it is the target of the sink.
	if err := r.CheckHost(m.Sink.Target); err != nil {
		return nil, fmt.Errorf("%s: %w: the host is not a host of the platform", op, egress.ErrRefused)
	}

	u, err := url.Parse(m.To)
	if err != nil || u == nil {
		return nil, fmt.Errorf("%s: %w", op, egress.ErrRefused)
	}

	return u, nil
}

// request builds the HTTP request of req for the webhook URL base. A url.Error
// would repeat the URL, which holds a token, so no error here wraps one.
func (s *Sender) request(ctx context.Context, base *url.URL, req Request) (*http.Request, error) {
	if err := checkRequest(req); err != nil {
		return nil, err
	}

	u := *base
	if req.PathSuffix != "" {
		// Keep the encoded bytes of the path of the URL: a segment such as
		// %21room%3Aexample.org is sent as it is, and %2F stays one segment.
		// The suffix is a path in decoded form, so it is encoded here.
		suffix := (&url.URL{Path: req.PathSuffix}).EscapedPath()
		u.RawPath = strings.TrimSuffix(u.EscapedPath(), "/") + suffix
		u.Path = strings.TrimSuffix(u.Path, "/") + req.PathSuffix
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("%w: the address does not parse", egress.ErrRefused)
	}

	httpReq.Header.Set("Content-Type", req.ContentType)

	return httpReq, nil
}

// authorize sets the header Authorization of httpReq to the access token, when
// the renderer needs one (see AuthRenderer). It returns ErrPermanent when the
// renderer needs a token and the sink has none, or has one that is not a
// valid header value. For a renderer that needs none it sends no token, also
// when the message holds one. The error text is fixed and never holds the
// token.
//
// Defense in depth: the sink service checks the token when a person saves it.
// This check stays so that a sink that a restore left behind, or a caller
// that skips the service, cannot make the sender write a line break or a
// space into a header, or send a token to a platform that does not take one.
func authorize(r Renderer, httpReq *http.Request, secrets [][]byte) error {
	if a, ok := r.(AuthRenderer); !ok || !a.AuthSecret() {
		return nil
	}

	var token []byte
	if len(secrets) > 0 {
		token = secrets[0]
	}

	if !notify.ValidAccessToken(token) {
		return fmt.Errorf("%s: %w: the sink has no usable access token", op, notify.ErrPermanent)
	}

	httpReq.Header.Set("Authorization", "Bearer "+string(token))

	return nil
}

// checkRequest returns ErrPermanent for a request of a renderer that breaks
// the contract of Request. Defense in depth: chattest.RunRenderer checks the
// same rules for each renderer. This check stays so that a renderer that does
// not run the suite cannot make the sender send a body above the limit, send
// with another method, or leave the path of the webhook.
func checkRequest(req Request) error {
	suffix := req.PathSuffix

	switch {
	case req.Method != http.MethodPost && req.Method != http.MethodPut,
		req.ContentType == "", len(req.Body) == 0, len(req.Body) > MaxRequestBytes,
		len(suffix) > MaxPathSuffixLen,
		suffix != "" && (suffix[0] != '/' || strings.HasPrefix(suffix, "//") ||
			strings.ContainsAny(suffix, "?#\\:") || strings.Contains(suffix, "..") ||
			strings.HasSuffix(suffix, "/.") || strings.Contains(suffix, "/./") ||
			strings.ContainsFunc(suffix, func(r rune) bool { return r < 0x21 || r == 0x7f })):
		return fmt.Errorf("%w: the renderer made a request that breaks the contract", notify.ErrPermanent)
	default:
		return nil
	}
}

// answer maps the answer of the platform.
func (s *Sender) answer(r Renderer, resp *http.Response) error {
	code := resp.StatusCode

	body, readErr := egresshttp.ReadBody(resp)

	if code < http.StatusOK || code >= http.StatusMultipleChoices {
		return post.StatusError(op, resp, s.now())
	}

	if readErr != nil {
		// An answer that is too large, or that breaks off, is not an answer
		// of the platform. Another attempt could post the message twice.
		return fmt.Errorf("%s: status %d: %w: the answer cannot be read", op, code, notify.ErrPermanent)
	}

	return checkAnswer(r, code, body)
}

// checkAnswer asks the renderer about a 2xx answer, and keeps only the class
// of its error. Defense in depth: the contract of Renderer.Check says that the
// text of its error never holds the body. This function does not rely on that.
func checkAnswer(r Renderer, code int, body []byte) error {
	err := r.Check(code, body)

	switch {
	case err == nil:
		return nil
	case errors.Is(err, notify.ErrTemporary):
		return fmt.Errorf("%s: status %d: %w: the platform refused the message", op, code,
			notify.ErrTemporary)
	default:
		return fmt.Errorf("%s: status %d: %w: the platform refused the message", op, code,
			notify.ErrPermanent)
	}
}

// content builds the Content of n. It cuts and cleans each text, so that a
// notification with a text that breaks a bound of Content, for example a
// stray control character, still reaches the platform.
func (s *Sender) content(n notify.Notification) Content {
	product := clean(s.product, MaxProductLen)
	if strings.TrimSpace(product) == "" {
		product = DefaultProduct
	}

	c := Content{
		NotificationID: n.ID,
		Product:        product,
		Title:          clean(titleOf(n.Type), MaxTitleLen),
		Summary:        clean(n.Summary, notify.MaxSummaryLen),
		Severity:       n.Severity,
		Fields:         nil,
		Links:          nil,
	}

	add := func(label, value string) {
		if value != "" && len(c.Fields) < MaxFields {
			c.Fields = append(c.Fields, Field{Label: clean(label, MaxFieldLen), Value: clean(value, MaxFieldLen)})
		}
	}

	add("Namespace", n.Scope.String())

	if n.Type != notify.SinkTest {
		add("Object", n.Object)
	}

	add("By", n.Subject)

	for _, k := range slices.Sorted(maps.Keys(n.Attributes)) {
		add(k, n.Attributes[k])
	}

	for _, l := range n.Links {
		if len(c.Links) == notify.MaxLinks {
			break
		}

		l.Title = clean(l.Title, notify.MaxLinkTitleLen)
		if checkLink(l) == nil {
			c.Links = append(c.Links, l)
		}
	}

	return c
}

// titleOf returns the title of a notification type: the type with its dots and
// dashes as spaces, for example "Job awaiting approval".
func titleOf(t notify.Type) string {
	if t == notify.SinkTest {
		return "Test message"
	}

	s := strings.NewReplacer(".", " ", "-", " ").Replace(string(t))
	if s == "" {
		return "Notification"
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

// clean returns s with each invalid byte and each forbidden character
// replaced by a space, cut to at most limit bytes on a character boundary.
func clean(s string, limit int) string {
	s = strings.ToValidUTF8(s, " ")
	s = strings.Map(func(r rune) rune {
		if forbiddenRune(r) {
			return ' '
		}

		return r
	}, s)

	if len(s) <= limit {
		return s
	}

	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}

	return s[:limit]
}
