package notifications_test

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

const (
	read   = identity.ActionNamespaceRead
	manage = identity.ActionNamespaceManageNotifications

	// canary stands for the token in a webhook URL. It must never appear in an
	// answer, an audit entry or a log line.
	canary = "CANARY-9f3a2c"

	// webhookURL is the URL of a webhook sink. Its path and query are secret: no
	// answer, audit entry or log line may show them.
	webhookURL  = "https://hooks.example.com/ops/" + canary + "?token=" + canary
	webhookHost = "hooks.example.com"

	base = "/api/dustpan/v1/namespaces/acme/sinks"
)

// grantsOf returns the actions of a subject. The token of a subject is its
// name.
func grantsOf(subject string) []identity.Action {
	switch subject {
	case "admin":
		return []identity.Action{read, manage}
	case "reader":
		return []identity.Action{read}
	case "manageonly":
		return []identity.Action{manage}
	default:
		return nil
	}
}

type testAuthn struct{}

func (testAuthn) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if grantsOf(token) == nil {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: token, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson, Credential: "",
		JobID: "",
	}, nil
}

// testAuthz allows the actions of the subject, minus the denied ones.
type testAuthz struct {
	deny map[identity.Action]bool
}

func (a testAuthz) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if slices.Contains(grantsOf(p.Subject), action) && !a.deny[action] {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (testAuthz) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// recordingSender records the messages it gets and answers err.
type recordingSender struct {
	kind notify.SinkKind

	mu   sync.Mutex
	err  error
	sent []notify.Message
}

func (s *recordingSender) Kind() notify.SinkKind { return s.kind }

func (s *recordingSender) Send(_ context.Context, m notify.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The service clears its plaintext after the send, so the recorder keeps
	// its own copy of each secret.
	copies := make([][]byte, len(m.Secrets))
	for i := range m.Secrets {
		copies[i] = bytes.Clone(m.Secrets[i])
	}

	m.Secrets = copies
	s.sent = append(s.sent, m)

	return s.err
}

func (s *recordingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.sent)
}

func (s *recordingSender) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.err = err
}

// logBuffer is a log destination that tests read.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	n, err := l.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

type fixture struct {
	handler *apinotifications.Handler
	// served is the handler that the tests call: it fails a test when a
	// handler answers a success status that the description does not give.
	served  http.Handler
	config  apinotifications.Config
	store   *sqlite.Store
	secrets *secret.Service
	hook    *recordingSender
	logs    *logBuffer
	// denied holds the actions that testAuthz refuses. A test can add one
	// while no request runs.
	denied map[identity.Action]bool
	clock  *testClock
}

// testClock is the clock of the Idempotency-Key middleware.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// newFixture returns a handler over a real SQLite store, with the signed
// webhook kind served by a recording sender.
func newFixture(t *testing.T, deny ...identity.Action) fixture {
	t.Helper()

	return buildFixture(t, keyMiddleware, deny...)
}

// newFixtureWithoutKeys returns a handler with no store of replay records.
func newFixtureWithoutKeys(t *testing.T) fixture {
	t.Helper()

	return buildFixture(t, noMiddleware)
}

// keyMiddleware returns the Idempotency-Key middleware over the store.
func keyMiddleware(t *testing.T, store *sqlite.Store, clock *testClock) *idempotent.Middleware {
	t.Helper()

	idem, err := idempotent.New(idempotent.Config{
		Store:  store.Idempotency(idempotencytest.TestWrapper(t, 1)),
		Logger: slog.New(slog.DiscardHandler), Now: clock.Now, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	return idem
}

// noMiddleware returns no middleware: the handler refuses the header.
func noMiddleware(*testing.T, *sqlite.Store, *testClock) *idempotent.Middleware { return nil }

// openStore returns a SQLite store with the namespaces acme and other, and the
// secret service over it.
func openStore(t *testing.T) (*sqlite.Store, *secret.Service, *namespace.Service) {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	for _, name := range []namespace.Name{"acme", "other"} {
		if _, err = namespaces.Create(t.Context(), name); err != nil {
			t.Fatalf("create namespace %s: %v", name, err)
		}
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: store, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	return store, secrets, namespaces
}

func buildFixture(
	t *testing.T, middleware func(*testing.T, *sqlite.Store, *testClock) *idempotent.Middleware,
	deny ...identity.Action,
) fixture {
	t.Helper()

	return buildFixtureWith(t, middleware, nil, deny...)
}

// buildFixtureWith is buildFixture with wrap, which a test uses to put a
// double in front of the sink store. A nil wrap keeps the store.
func buildFixtureWith(
	t *testing.T, middleware func(*testing.T, *sqlite.Store, *testClock) *idempotent.Middleware,
	wrap func(*sqlite.Store, notify.SinkStore) notify.SinkStore, deny ...identity.Action,
) fixture {
	t.Helper()

	store, secrets, namespaces := openStore(t)
	hook := &recordingSender{kind: notify.SinkWebhook, mu: sync.Mutex{}, err: nil, sent: nil}

	sinkStore := store.NotificationSinks()
	if wrap != nil {
		sinkStore = wrap(store, sinkStore)
	}

	svc, err := sinks.NewService(sinks.Config{
		Store: sinkStore, Sealer: notify.SinkSealer{Namespaces: secrets, Installation: nil},
		Namespaces: namespaces, Senders: []notify.Sender{hook}, Ports: egress.Ports{}, Now: nil,
	})
	if err != nil {
		t.Fatalf("sink service: %v", err)
	}

	denied := map[identity.Action]bool{}
	for _, a := range deny {
		denied[a] = true
	}

	clock := &testClock{mu: sync.Mutex{}, now: time.Now()}
	logs := &logBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}

	cfg := apinotifications.Config{
		Sinks: svc, Authenticator: testAuthn{}, Authorizer: testAuthz{deny: denied}, Audit: store, Namespaces: nil,
		OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, Idempotency: middleware(t, store, clock),
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	}

	handler, err := apinotifications.NewHandler(cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return fixture{
		handler: handler,
		served:  apidesctest.CheckStatus(t, handler),
		config:  cfg,
		store:   store,
		secrets: secrets,
		hook:    hook,
		logs:    logs,
		denied:  denied,
		clock:   clock,
	}
}

// request is one call of a test.
type request struct {
	token   string
	method  string
	target  string
	body    string
	headers map[string]string
}

// response is what a call answers.
type response struct {
	code   int
	header http.Header
	body   string
}

func get(token, target string) request {
	return request{token: token, method: http.MethodGet, target: target, body: "", headers: nil}
}

func send(token, method, target, body string) request {
	return request{token: token, method: method, target: target, body: body, headers: nil}
}

// withHeader returns r with one more header.
func (r request) withHeader(key, value string) request {
	headers := map[string]string{key: value}
	maps.Copy(headers, r.headers)

	r.headers = headers

	return r
}

func keyed(r request, key string) request { return r.withHeader("Idempotency-Key", key) }

// do sends one request.
func (f fixture) do(t *testing.T, r request) response {
	t.Helper()

	var reader io.Reader = http.NoBody
	if r.body != "" {
		reader = strings.NewReader(r.body)
	}

	req := httptest.NewRequestWithContext(t.Context(), r.method, r.target, reader)
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}

	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	f.served.ServeHTTP(rec, req)

	return response{code: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

// wantCode fails the test when the answer has another status.
func wantCode(t *testing.T, what string, got response, want int) {
	t.Helper()

	if got.code != want {
		t.Errorf("%s: %d %s, want %d", what, got.code, got.body, want)
	}
}

func jsonString(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return string(b)
}

// sinkWire is the part of a sink answer that the tests read.
type sinkWire struct {
	ID         string `json:"id"`
	Namespace  string `json:"namespace"`
	Kind       string `json:"kind"`
	Platform   string `json:"platform"`
	Name       string `json:"name"`
	Target     string `json:"target"`
	URLSet     bool   `json:"urlSet"`
	CreatedBy  string `json:"createdBy"`
	Version    int64  `json:"version"`
	SigningKey string `json:"signingKey"`
}

type listWire struct {
	Sinks []sinkWire `json:"sinks"`
}

func decode[T any](t *testing.T, got response) T {
	t.Helper()

	var v T
	if err := json.Unmarshal([]byte(got.body), &v); err != nil {
		t.Fatalf("decode %q: %v", got.body, err)
	}

	return v
}

func webhookBody(t *testing.T, name string) string {
	t.Helper()

	return jsonString(t, map[string]string{"kind": "webhook", "name": name, "target": webhookURL})
}

func chatBody(t *testing.T, name string) string {
	t.Helper()

	return jsonString(t, map[string]string{
		"kind": "chat", "platform": "slack", "name": name, "url": "https://hooks.slack.com/services/T0/B0/" + canary,
	})
}

// makeWebhook creates a webhook sink as admin.
func (f fixture) makeWebhook(t *testing.T) sinkWire {
	t.Helper()

	got := f.do(t, send("admin", http.MethodPost, base, webhookBody(t, "Ops")))
	wantCode(t, "create webhook", got, http.StatusCreated)

	return decode[sinkWire](t, got)
}

// makeChat creates a chat sink as admin.
func (f fixture) makeChat(t *testing.T, name string) sinkWire {
	t.Helper()

	got := f.do(t, send("admin", http.MethodPost, base, chatBody(t, name)))
	wantCode(t, "create chat", got, http.StatusCreated)

	return decode[sinkWire](t, got)
}

// entries returns the audit entries of the namespace acme, oldest first.
func (f fixture) entries(t *testing.T) []audit.Entry {
	t.Helper()

	listing, err := f.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	out := slices.Clone(listing.Entries)
	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out
}

// sinkEntries returns the audit entries of the sink actions, oldest first.
func (f fixture) sinkEntries(t *testing.T) []audit.Entry {
	t.Helper()

	return slices.DeleteFunc(f.entries(t), func(e audit.Entry) bool { return !strings.HasPrefix(e.Action, "sink:") })
}
