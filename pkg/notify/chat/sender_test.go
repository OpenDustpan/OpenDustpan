package chat_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/slack"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
)

const (
	// canaryToken stands for the token in the path of the webhook URL.
	canaryToken = "canary-url-token-91ce05" //nolint:gosec // A fake token used as test data.
	// canaryBody stands for text in the answer of a platform.
	canaryBody = "canary-answer-body-5d2e"
	hookPath   = "/services/T000/B000/" + canaryToken
	hookURL    = "https://hooks.slack.com" + hookPath
)

//nolint:gochecknoglobals // A fixed clock shared by the cases.
var fixedNow = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func now() time.Time { return fixedNow }

type request struct {
	method string
	path   string
	// uri is the request target as it came over the wire, with its encoded
	// bytes.
	uri    string
	header http.Header
	body   []byte
}

// receiver is a TLS test server that answers as the case says.
type receiver struct {
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []request
	handler atomic.Pointer[http.HandlerFunc]
}

func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)

		_, _ = io.WriteString(w, body) //nolint:errcheck // A test server.
	}
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()

	r := new(receiver)
	r.set(reply(http.StatusOK, "ok"))

	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}

		r.mu.Lock()
		r.reqs = append(r.reqs, request{
			method: req.Method, path: req.URL.Path, uri: req.RequestURI, header: req.Header.Clone(), body: b,
		})
		r.mu.Unlock()

		(*r.handler.Load())(w, req)
	}))
	t.Cleanup(r.srv.Close)

	return r
}

func (r *receiver) set(h http.HandlerFunc) { r.handler.Store(&h) }

func (r *receiver) requests() []request {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]request(nil), r.reqs...)
}

func (r *receiver) sender(t *testing.T, opts ...chat.Option) *chat.Sender {
	t.Helper()

	s, err := chat.NewForTest(r.srv, now, slack.New())
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}

	for _, o := range opts {
		o(s)
	}

	t.Cleanup(s.Close)

	return s
}

func notification() notify.Notification {
	return notify.Notification{
		ID:       "ntf-1",
		Type:     notify.JobFailed,
		Scope:    "shop",
		Object:   "opentofu:shop/network",
		ObjectID: "u-1",
		Subject:  "alice",
		Agent:    "dusty",
		Severity: notify.SeverityWarning,
		Summary:  "The apply of network failed.",
		Links: []notify.Link{
			{Title: "Job", URL: "https://console.example.com/jobs/j-1", Kind: notify.LinkConsole},
		},
		Attributes: map[string]string{"job": "j-1", "attempt": "2"},
		Recipients: nil,
		DedupeKey:  "j-1/failed",
		At:         fixedNow,
		Sequence:   42,
	}
}

func message() notify.Message {
	return notify.Message{
		Notification: notification(),
		Sink: notify.Sink{
			ID: "snk-1", Scope: "shop", Kind: notify.SinkChat, Name: "Ops", Platform: "slack",
			Target: "hooks.slack.com", SecretRefs: []string{"itm-1"}, CreatedBy: "alice", Version: 1,
		},
		Secrets: nil,
		To:      hookURL,
		Actions: nil,
	}
}

func TestSenderConformance(t *testing.T) {
	t.Parallel()

	notifytest.RunSender(t, func(t *testing.T) notifytest.SenderHarness {
		t.Helper()

		r := newReceiver(t)

		return notifytest.SenderHarness{
			Sender:  r.sender(t),
			Message: message(),
			Behave: func(b notifytest.Behavior) {
				switch b {
				case notifytest.ReceiverAccepts:
					r.set(reply(http.StatusOK, "ok"))
				case notifytest.ReceiverRefusesForGood:
					r.set(reply(http.StatusNotFound, "no_service"))
				case notifytest.ReceiverFailsForNow:
					r.set(reply(http.StatusBadGateway, ""))
				case notifytest.ReceiverAsksToWait:
					r.set(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Retry-After", strconv.Itoa(int(notifytest.AskedWait.Seconds())))
						w.WriteHeader(http.StatusTooManyRequests)
					})
				default:
					t.Errorf("unknown behavior %d", b)
				}
			},
			Received:     func() int { return len(r.requests()) },
			NoRetryAfter: false,
		}
	})
}

func TestSendPostsOneReadableMessage(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)

	if err := r.sender(t, chat.WithProduct("Acme Platform")).Send(t.Context(), message()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	reqs := r.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}

	got := reqs[0]
	if got.method != http.MethodPost || got.path != hookPath || got.header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %s %s %q, want POST of the webhook path as JSON", got.method, got.path,
			got.header.Get("Content-Type"))
	}

	if got.header.Get("Authorization") != "" || got.header.Get("Cookie") != "" {
		t.Error("the request carries a credential header")
	}

	var msg struct {
		Text string `json:"text"`
	}

	if err := json.Unmarshal(got.body, &msg); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}

	for _, want := range []string{
		"Acme Platform", "Job failed", "The apply of network failed.", "Namespace", "shop",
		"opentofu:shop/network", "alice", "attempt", "console.example.com/jobs/j-1",
	} {
		if !strings.Contains(string(got.body), want) {
			t.Errorf("the body does not hold %q", want)
		}
	}

	if !strings.Contains(msg.Text, "Warning") {
		t.Errorf("fallback text = %q, want the severity", msg.Text)
	}
}

func TestSendTestMessageSaysItIsATest(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)
	m := message()
	m.Notification = notify.SinkTestNotification(m.Sink, "ntf-t", fixedNow)

	if err := r.sender(t).Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	body := string(r.requests()[0].body)
	if !strings.Contains(body, "Test message") ||
		!strings.Contains(body, "This is a test message. No event happened.") {
		t.Errorf("the body does not say that it is a test: %s", body)
	}
}

func TestSendRefusesBeforeTheNetwork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(m *notify.Message)
		want   error
	}{
		{"another host that the sink names", func(m *notify.Message) {
			m.To, m.Sink.Target = "https://evil.example.com"+hookPath, "evil.example.com"
		}, egress.ErrRefused},
		{"a host that is not the target of the sink", func(m *notify.Message) {
			m.To = "https://hooks.slack-gov.com" + hookPath
		}, egress.ErrRefused},
		{"a host that ends with the host of Slack", func(m *notify.Message) {
			m.To, m.Sink.Target = "https://hooks.slack.com.evil.example"+hookPath, "hooks.slack.com.evil.example"
		}, egress.ErrRefused},
		{"a host of Slack with a port", func(m *notify.Message) {
			m.To, m.Sink.Target = "https://hooks.slack.com:8443"+hookPath, "hooks.slack.com:8443"
		}, egress.ErrRefused},
		{"user information", func(m *notify.Message) {
			m.To = "https://user:pw@hooks.slack.com" + hookPath
		}, egress.ErrRefused},
		{"http", func(m *notify.Message) { m.To = "http://hooks.slack.com" + hookPath }, egress.ErrRefused},
		{"a fragment", func(m *notify.Message) { m.To = hookURL + "#x" }, egress.ErrRefused},
		{"a loopback address", func(m *notify.Message) {
			m.To, m.Sink.Target = "https://127.0.0.1"+hookPath, "127.0.0.1"
		}, egress.ErrRefused},
		{"the metadata address", func(m *notify.Message) {
			m.To, m.Sink.Target = "https://169.254.169.254"+hookPath, "169.254.169.254"
		}, egress.ErrRefused},
		{
			"localhost",
			func(m *notify.Message) { m.To, m.Sink.Target = "https://localhost"+hookPath, "localhost" },
			egress.ErrRefused,
		},
		{"an empty URL", func(m *notify.Message) { m.To = "" }, egress.ErrRefused},
		{"a port that the operator did not allow", func(m *notify.Message) {
			m.To, m.Sink.Target = "https://hooks.slack.com:9443"+hookPath, "hooks.slack.com:9443"
		}, egress.ErrRefused},
		{"a platform with no renderer", func(m *notify.Message) { m.Sink.Platform = "teams" }, notify.ErrPermanent},
		{"no platform", func(m *notify.Message) { m.Sink.Platform = "" }, notify.ErrPermanent},
		{"a sink of another kind", func(m *notify.Message) { m.Sink.Kind = notify.SinkWebhook }, notify.ErrPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t)
			m := message()
			tt.mutate(&m)

			err := r.sender(t).Send(t.Context(), m)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Send = %v, want %v", err, tt.want)
			}

			if n := len(r.requests()); n != 0 {
				t.Errorf("requests = %d, want 0", n)
			}

			assertNoLeak(t, err)
		})
	}
}

func assertNoLeak(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		return
	}

	for _, secret := range []string{canaryToken, "/services/", canaryBody, "127.0.0.1"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error text holds %q: %v", secret, err)
		}
	}
}

func TestSendAllowedPortStillNeedsTheHostOfSlack(t *testing.T) {
	t.Parallel()

	ports, err := egress.NewPorts(8443)
	if err != nil {
		t.Fatal(err)
	}

	r := newReceiver(t)
	m := message()
	m.To, m.Sink.Target = "https://hooks.slack.com:8443"+hookPath, "hooks.slack.com:8443"

	// The operator allows the port, and the renderer still refuses the host with a port.
	if err = r.sender(t, chat.WithPorts(ports)).Send(t.Context(), m); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Send = %v, want ErrRefused", err)
	}

	if n := len(r.requests()); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestSendRedirectIsNotFollowed(t *testing.T) {
	t.Parallel()

	var elsewhere atomic.Int32

	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	t.Cleanup(other.Close)

	for _, code := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		r := newReceiver(t)
		r.set(func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, other.URL+"/capture", code)
		})

		err := r.sender(t).Send(t.Context(), message())
		if !errors.Is(err, egress.ErrRefused) || errors.Is(err, notify.ErrTemporary) {
			t.Errorf("status %d: Send = %v, want ErrRefused and not temporary", code, err)
		}

		if n := len(r.requests()); n != 1 {
			t.Errorf("status %d: requests = %d, want 1 and no second request", code, n)
		}

		assertNoLeak(t, err)
	}

	if n := elsewhere.Load(); n != 0 {
		t.Errorf("the redirect target got %d requests, want 0", n)
	}
}

func TestSendAnswers(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", egress.MaxBodyBytes+10)

	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    error
		not     error
	}{
		{"a 200 that is not ok", reply(http.StatusOK, "no_text "+canaryBody), notify.ErrPermanent, notify.ErrTemporary},
		{"a 200 with a body above the limit", reply(http.StatusOK, huge), notify.ErrPermanent, notify.ErrTemporary},
		{
			"a 404 with a body above the limit",
			reply(http.StatusNotFound, huge),
			notify.ErrPermanent,
			notify.ErrTemporary,
		},
		{
			"a 400",
			reply(http.StatusBadRequest, "invalid_payload "+canaryBody),
			notify.ErrPermanent,
			notify.ErrTemporary,
		},
		{"a 403", reply(http.StatusForbidden, "action_prohibited"), notify.ErrPermanent, notify.ErrTemporary},
		{"a 404", reply(http.StatusNotFound, "no_service"), notify.ErrPermanent, notify.ErrTemporary},
		{"a 408", reply(http.StatusRequestTimeout, ""), notify.ErrTemporary, notify.ErrPermanent},
		{"a 429", reply(http.StatusTooManyRequests, ""), notify.ErrTemporary, notify.ErrPermanent},
		{"a 500", reply(http.StatusInternalServerError, canaryBody), notify.ErrTemporary, notify.ErrPermanent},
		{"a 503", reply(http.StatusServiceUnavailable, ""), notify.ErrTemporary, notify.ErrPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t)
			r.set(tt.handler)

			err := r.sender(t).Send(t.Context(), message())
			if !errors.Is(err, tt.want) || errors.Is(err, tt.not) {
				t.Fatalf("Send = %v, want %v and not %v", err, tt.want, tt.not)
			}

			assertNoLeak(t, err)

			if n := len(r.requests()); n != 1 {
				t.Errorf("requests = %d, want one attempt", n)
			}
		})
	}
}

func TestSendRetryAfterOnTemporaryStatus(t *testing.T) {
	t.Parallel()

	for _, code := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway} {
		r := newReceiver(t)
		r.set(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(code)
		})

		var ra *notify.RetryAfterError

		err := r.sender(t).Send(t.Context(), message())
		if !errors.As(err, &ra) || ra.Wait != 30*time.Second {
			t.Errorf("status %d: Send = %v, want a RetryAfterError of 30 s", code, err)
		}
	}
}

func TestSendSlowServer(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)
	release := make(chan struct{})

	r.set(func(http.ResponseWriter, *http.Request) { <-release })
	t.Cleanup(func() { close(release) })

	s := r.sender(t)
	s.SetTimeoutForTest(100 * time.Millisecond)

	start := time.Now()

	err := s.Send(t.Context(), message())
	if !errors.Is(err, notify.ErrTemporary) || errors.Is(err, notify.ErrPermanent) {
		t.Fatalf("Send = %v, want ErrTemporary", err)
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Send took %s, want about the deadline of 100 ms", took)
	}

	assertNoLeak(t, err)
}

func TestSendSlowAnswerBody(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)
	release := make(chan struct{})

	r.set(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-release
	})
	t.Cleanup(func() { close(release) })

	s := r.sender(t)
	s.SetTimeoutForTest(slowBodyDeadline)

	// The platform sent the status and holds the body: the sender cannot say that
	// Slack took the message, and a second attempt could post it twice.
	if err := s.Send(t.Context(), message()); !errors.Is(err, notify.ErrPermanent) {
		t.Fatalf("Send = %v, want ErrPermanent: the status must arrive before the deadline of %s", err,
			slowBodyDeadline)
	}
}

// slowBodyDeadline is the deadline of the send in TestSendSlowAnswerBody. The
// handshake, the request and the status must arrive before it, also on a slow
// machine. The body never arrives, so the send always ends at the deadline.
const slowBodyDeadline = 2 * time.Second

func TestSendHostileTextIsEncoded(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)
	m := message()
	n := &m.Notification
	n.Summary = "<!channel> *x* <@U123ABC> <https://evil.example|click>"
	n.Object = "stack:<!here>/shop/<b>net</b>"
	n.Subject = "<!subteam^S1|@ops>"
	n.Attributes = map[string]string{"<!channel>": "<@U1>", "k\x00ey": "va\rl\u202eue"}
	n.Links = append(
		n.Links,
		notify.Link{Title: "<!channel>", URL: "https://console.example/x|<!channel>", Kind: notify.LinkOther},
	)

	if err := r.sender(t).Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	body := string(r.requests()[0].body)

	if strings.ContainsAny(strings.ReplaceAll(body, "console.example/x|<!channel>", ""), "<>") {
		t.Errorf("the body holds < or > outside the URL of a button: %s", body)
	}

	for _, want := range []string{"&lt;!channel&gt;", "&lt;@U123ABC&gt;", "&lt;!here&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body does not hold the encoded text %q", want)
		}
	}
}

func TestSendDoesNotRenderActions(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)
	m := message()
	m.Actions = []notify.ActionLink{{Title: "Approve", URL: "https://dustpan.example/act/SIGNED-ACTION"}}

	if err := r.sender(t).Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	body := string(r.requests()[0].body)
	if strings.Contains(body, "SIGNED-ACTION") || strings.Contains(body, "Approve") {
		t.Errorf("a webhook message holds an action: %s", body)
	}
}

func TestSendCleansAndCutsText(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)
	m := message()
	m.Notification.Summary = "a\x00b\rc" + strings.Repeat("é", notify.MaxSummaryLen)
	m.Notification.Attributes = map[string]string{"a\xffb": "v"}

	if err := r.sender(t, chat.WithProduct("")).Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var msg map[string]any
	if err := json.Unmarshal(r.requests()[0].body, &msg); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(r.requests()[0].body), chat.DefaultProduct) {
		t.Error("an empty product name does not fall back to the default")
	}
}

func TestSendManyFieldsAndLinks(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)
	m := message()
	m.Notification.Attributes = map[string]string{}

	for i := range notify.MaxAttributes {
		m.Notification.Attributes["k"+strconv.Itoa(i)] = strings.Repeat("<", notify.MaxAttributeLen)
	}

	m.Notification.Links = nil
	for range notify.MaxLinks {
		m.Notification.Links = append(
			m.Notification.Links,
			notify.Link{
				Title: strings.Repeat("<", notify.MaxLinkTitleLen),
				URL:   "https://console.example/a",
				Kind:  notify.LinkConsole,
			},
		)
	}

	if err := r.sender(t).Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if n := len(r.requests()[0].body); n > chat.MaxRequestBytes {
		t.Errorf("body = %d bytes, want at most %d", n, chat.MaxRequestBytes)
	}
}

func TestSendDialGuardRefusesLoopback(t *testing.T) {
	t.Parallel()

	// The production dial guard runs on every connection. A name that resolves
	// to a refused address is a failed connection, the same class as a name
	// that does not resolve.
	s := chat.NewDialGuardedForTest(now, slack.New())
	t.Cleanup(s.Close)

	err := s.Send(t.Context(), message())
	if !errors.Is(err, notify.ErrTemporary) || errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Send = %v, want ErrTemporary only", err)
	}

	assertNoLeak(t, err)
}

func TestProductionSenderRefusesTheMachineOfTheServer(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)

	s := chat.NewSender([]chat.Renderer{slack.New()})
	t.Cleanup(s.Close)

	m := message()
	m.To, m.Sink.Target = srv.URL+hookPath, strings.TrimPrefix(srv.URL, "https://")

	if err := s.Send(t.Context(), m); !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Send = %v, want ErrRefused", err)
	}

	if hits.Load() != 0 {
		t.Error("the production sender reached a loopback server")
	}
}

func TestServes(t *testing.T) {
	t.Parallel()

	s := chat.NewSender([]chat.Renderer{slack.New()})
	t.Cleanup(s.Close)

	for _, tt := range []struct {
		kind     notify.SinkKind
		platform string
		want     bool
	}{
		{notify.SinkChat, "slack", true},
		{notify.SinkChat, "teams", false},
		{notify.SinkChat, "", false},
		{notify.SinkWebhook, "slack", false},
	} {
		sink := notify.Sink{
			ID: "snk-1", Scope: "shop", Kind: tt.kind, Name: "Ops", Platform: tt.platform, Target: "hooks.slack.com",
			SecretRefs: nil, CreatedBy: "alice", Version: 1,
		}

		if got := s.Serves(sink); got != tt.want {
			t.Errorf("Serves(%s, %q) = %v, want %v", tt.kind, tt.platform, got, tt.want)
		}
	}

	if s.Kind() != notify.SinkChat {
		t.Errorf("Kind = %q", s.Kind())
	}
}

func TestSenderCheckHost(t *testing.T) {
	t.Parallel()

	s := chat.NewSender([]chat.Renderer{slack.New()})
	t.Cleanup(s.Close)

	for _, tt := range []struct {
		platform, host string
		ok             bool
	}{
		{"slack", "hooks.slack.com", true},
		{"slack", "hooks.slack-gov.com", true},
		{"slack", "hooks.example.com", false},
		{"slack", "hooks.slack.com:8443", false},
		{"teams", "anything.example.com", true},
	} {
		err := s.CheckHost(tt.platform, tt.host)
		if tt.ok != (err == nil) || (!tt.ok && !errors.Is(err, notify.ErrInvalid)) {
			t.Errorf("CheckHost(%q, %q) = %v, want ok %v", tt.platform, tt.host, err, tt.ok)
		}
	}
}
