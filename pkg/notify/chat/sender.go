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

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/notify"
)

// SendTimeout is the longest time of one send, as a deadline on the context
// of the request. The timeout of the client is a second layer above it.
const SendTimeout = 10 * time.Second

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

// Send posts m.Notification to the webhook URL m.To. A 2xx answer that the
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

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	resp, err := s.client.Do(httpReq.WithContext(ctx))
	if err != nil {
		return transportError(ctx, err)
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
	// The text of these errors can repeat the address of the URL, so the sender
	// keeps the class and drops the text.
	if egress.CheckURL(m.To, egress.HTTPSOnly) != nil {
		return nil, fmt.Errorf("post chat message: %w: the URL is not allowed", egress.ErrRefused)
	}

	if egress.CheckPort(m.To, s.ports) != nil {
		return nil, fmt.Errorf("post chat message: %w: the port is not allowed", egress.ErrRefused)
	}

	host, err := notify.ChatHost(m.To)
	if err != nil {
		return nil, fmt.Errorf("post chat message: %w", egress.ErrRefused)
	}

	if host != m.Sink.Target {
		return nil, fmt.Errorf("post chat message: %w: the host of the URL is not the target of the sink",
			egress.ErrRefused)
	}

	if err = r.CheckHost(host); err != nil {
		return nil, fmt.Errorf("post chat message: %w: the host is not a host of the platform", egress.ErrRefused)
	}

	u, err := url.Parse(m.To)
	if err != nil || u == nil {
		return nil, fmt.Errorf("post chat message: %w", egress.ErrRefused)
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
		u.Path = strings.TrimSuffix(u.Path, "/") + req.PathSuffix
		u.RawPath = ""
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("%w: the address does not parse", egress.ErrRefused)
	}

	httpReq.Header.Set("Content-Type", req.ContentType)

	return httpReq, nil
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
			strings.ContainsFunc(suffix, func(r rune) bool { return r < 0x21 || r == 0x7f })):
		return fmt.Errorf("%w: the renderer made a request that breaks the contract", notify.ErrPermanent)
	default:
		return nil
	}
}

// transportError maps the error of a request that got no answer. It drops the
// text of err, which can repeat the URL and the resolved address, and keeps
// the class.
func transportError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, egresshttp.ErrDialRefused):
		// A refusal of the dial guard is a failed connection, so that an
		// answer does not tell a name that resolves to a private address from
		// a name that does not resolve.
		return fmt.Errorf("post chat message: %w: the request failed", notify.ErrTemporary)
	case errors.Is(err, egress.ErrRefused):
		return fmt.Errorf("post chat message: %w", egress.ErrRefused)
	case ctx.Err() != nil:
		return fmt.Errorf("post chat message: %w: %w", notify.ErrTemporary, ctx.Err())
	default:
		return fmt.Errorf("post chat message: %w: the request failed", notify.ErrTemporary)
	}
}

// answer maps the answer of the platform.
func (s *Sender) answer(r Renderer, resp *http.Response) error {
	code := resp.StatusCode

	body, readErr := egresshttp.ReadBody(resp)

	switch {
	case code >= http.StatusOK && code < http.StatusMultipleChoices:
		if readErr != nil {
			// An answer that is too large, or that breaks off, is not an answer
			// of the platform. Another attempt could post the message twice.
			return fmt.Errorf("post chat message: status %d: %w: the answer cannot be read", code, notify.ErrPermanent)
		}

		return checkAnswer(r, code, body)
	case code == http.StatusRequestTimeout, code == http.StatusTooManyRequests, code >= http.StatusInternalServerError:
		if wait, ok := notify.ParseRetryAfter(resp.Header.Get("Retry-After"), s.now()); ok {
			return fmt.Errorf("post chat message: status %d: %w", code, &notify.RetryAfterError{Wait: wait})
		}

		return fmt.Errorf("post chat message: status %d: %w", code, notify.ErrTemporary)
	case code >= http.StatusMultipleChoices:
		return fmt.Errorf("post chat message: status %d: %w", code, notify.ErrPermanent)
	default:
		return fmt.Errorf("post chat message: status %d: %w", code, notify.ErrTemporary)
	}
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
		return fmt.Errorf("post chat message: status %d: %w: the platform refused the message", code,
			notify.ErrTemporary)
	default:
		return fmt.Errorf("post chat message: status %d: %w: the platform refused the message", code,
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
