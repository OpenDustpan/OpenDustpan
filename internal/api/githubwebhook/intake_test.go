package githubwebhook_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/githubwebhook"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	validHook   = "0123456789abcdef0123456789abcdef"
	unknownHook = "ffffffffffffffffffffffffffffffff"
	signature   = "sha256=5f2a4cd8b0c9d9d1e4a7c0f1b2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f708192a3b"
)

// errStore is the error of a store that fails.
var (
	errStore      = errors.New("the store failed with hook 0123456789abcdef0123456789abcdef")
	errNoNsAppend = errors.New("the handler writes the installation scope only")
	errReset      = errors.New("connection reset")
)

// allReasons lists the reasons that the service refuses a delivery for.
func allReasons() []string {
	return []string{
		forge.EventReasonUnknownHook, forge.EventReasonNoRegistration, forge.EventReasonSignature,
		forge.EventReasonTooLarge, forge.EventReasonMalformed, forge.EventReasonUnsupported,
		forge.EventReasonDuplicate, forge.EventReasonNotAssigned, forge.EventReasonNotPrimary,
		forge.EventReasonSecretNotOpened, forge.EventReasonReceiptsFull, forge.EventReasonOther,
	}
}

// fakeReceiver is the service that the handler calls.
type fakeReceiver struct {
	err   error
	mu    sync.Mutex
	calls []receiverCall
}

type receiverCall struct {
	hookID string
	req    forge.EventRequest
}

func (f *fakeReceiver) ReceiveEvent(
	_ context.Context, hookID string, r forge.EventRequest,
) (forge.EventReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, receiverCall{hookID: hookID, req: r})

	return forge.EventReceipt{}, f.err
}

func (f *fakeReceiver) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.err = err
}

func (f *fakeReceiver) callList() []receiverCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

// fakeAudit is an audit store with the installation scope.
type fakeAudit struct {
	err     error
	mu      sync.Mutex
	entries []audit.Entry
}

func (f *fakeAudit) Append(context.Context, namespace.Fence, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errNoNsAppend
}

func (f *fakeAudit) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	return audit.Listing{Entries: nil, More: false}, nil
}

func (f *fakeAudit) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

func (f *fakeAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := e.Validate(); err != nil {
		return audit.Entry{}, fmt.Errorf("validate: %w", err)
	}

	if f.err != nil {
		return audit.Entry{}, f.err
	}

	f.entries = append(f.entries, e)

	return e, nil
}

func (f *fakeAudit) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.err = err
}

func (f *fakeAudit) written() []audit.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.entries)
}

// plainAudit is an audit store that cannot write the installation scope.
type plainAudit struct{}

func (plainAudit) Append(context.Context, namespace.Fence, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errNoNsAppend
}

func (plainAudit) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	return audit.Listing{Entries: nil, More: false}, nil
}

func (plainAudit) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// clock is a clock that a test moves.
type clock struct {
	now atomic.Int64
}

func newClock() *clock {
	c := &clock{now: atomic.Int64{}}
	c.now.Store(time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC).UnixNano())

	return c
}

func (c *clock) Now() time.Time { return time.Unix(0, c.now.Load()).UTC() }

func (c *clock) advance(d time.Duration) { c.now.Add(int64(d)) }

type env struct {
	handler  *githubwebhook.Handler
	receiver *fakeReceiver
	audit    *fakeAudit
	clock    *clock
	mu       sync.Mutex
	rejected []string
	failures atomic.Int64
}

func (e *env) rejects() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return slices.Clone(e.rejected)
}

func newEnv(t *testing.T) *env {
	t.Helper()

	e := &env{
		handler: nil, receiver: &fakeReceiver{err: nil, mu: sync.Mutex{}, calls: nil},
		audit: &fakeAudit{err: nil, mu: sync.Mutex{}, entries: nil}, clock: newClock(),
		mu: sync.Mutex{}, rejected: nil, failures: atomic.Int64{},
	}

	limiter := denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: e.clock.Now,
	})

	h, err := githubwebhook.NewHandler(githubwebhook.Config{
		Receiver: e.receiver,
		Audit:    e.audit,
		Limiter:  limiter,
		OnRejected: func(reason string) {
			e.mu.Lock()
			defer e.mu.Unlock()

			e.rejected = append(e.rejected, reason)
		},
		OnAuditFailure: func() { e.failures.Add(1) },
		Logger:         slog.New(slog.DiscardHandler),
		Now:            e.clock.Now,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	e.handler = h

	return e
}

// answer is what a sender sees.
type answer struct {
	header http.Header
	body   string
	status int
}

func (e *env) serve(t *testing.T, method, target string, body io.Reader, headers map[string]string) answer {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return answer{header: rec.Header().Clone(), body: rec.Body.String(), status: rec.Code}
}

func (e *env) deliver(t *testing.T, hook string, body []byte) answer {
	t.Helper()

	return e.serve(t, http.MethodPost, "/webhooks/"+hook, bytes.NewReader(body), map[string]string{
		"X-Hub-Signature-256": signature, "X-GitHub-Event": "ping",
		"X-GitHub-Delivery": "11111111-2222-3333-4444-555555555555",
	})
}

func refusalOf(reason string) error {
	return fmt.Errorf("receive: %w", &forge.EventRefusalError{Reason: reason})
}

func sameAnswer(t *testing.T, name string, got, want answer) {
	t.Helper()

	if got.status != want.status || got.body != want.body || !headersEqual(got.header, want.header) {
		t.Errorf("%s: answer = %d %v %q, want %d %v %q",
			name, got.status, got.header, got.body, want.status, want.header, want.body)
	}
}

func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}

	for k, av := range a {
		if !slices.Equal(av, b[k]) {
			return false
		}
	}

	return true
}

// TestIntakeAnswersAreUniform proves that the answer is the same for an
// accepted delivery, for each reason of a refusal, and for an error that is no
// refusal: no status, header or body tells them apart.
func TestIntakeAnswersAreUniform(t *testing.T) {
	t.Parallel()

	accepted := newEnv(t)
	want := accepted.deliver(t, validHook, []byte(`{"zen":"x"}`))

	if want.status != http.StatusNoContent || want.body != "" {
		t.Fatalf("accepted answer = %d %q, want 204 and no body", want.status, want.body)
	}

	if len(accepted.rejects()) != 0 || len(accepted.audit.written()) != 0 {
		t.Fatal("an accepted delivery counted a reject or wrote an entry")
	}

	cases := map[string]error{"not a refusal": errStore, "a refusal of an unknown reason": refusalOf("made\r\nup")}
	for _, reason := range allReasons() {
		cases[reason] = refusalOf(reason)
	}

	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			e.receiver.setErr(cause)

			sameAnswer(t, name, e.deliver(t, validHook, []byte(`{"zen":"x"}`)), want)
			sameAnswer(t, name+" (unknown hook)", e.deliver(t, unknownHook, []byte(`{}`)), want)

			if got := len(e.rejects()); got != 2 {
				t.Errorf("rejects = %d, want 2", got)
			}
		})
	}

	// The body that is too large has the same answer.
	big := newEnv(t)
	sameAnswer(t, "too large", big.deliver(t, validHook, bytes.Repeat([]byte("a"), githubwebhook.MaxBodyBytes+1)), want)

	if got := big.rejects(); !slices.Equal(got, []string{forge.EventReasonTooLarge}) {
		t.Errorf("rejects = %v, want [too-large]", got)
	}
}

// TestIntakeReasonsAreCountedAndAudited proves the reason that reaches the
// counter and the entry: the reason of the refusal, or other.
func TestIntakeReasonsAreCountedAndAudited(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		cause error
		want  string
	}{
		"signature": {cause: refusalOf(forge.EventReasonSignature), want: "signature"},
		"wrapped twice": {
			cause: fmt.Errorf("outer: %w", refusalOf(forge.EventReasonNotAssigned)),
			want:  "not-assigned",
		},
		"other error":      {cause: errStore, want: "other"},
		"unknown reason":   {cause: refusalOf("0123456789abcdef0123456789abcdef"), want: "other"},
		"empty reason":     {cause: refusalOf(""), want: "other"},
		"header in reason": {cause: refusalOf("a\r\nX-Evil: 1"), want: "other"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			e.receiver.setErr(tc.cause)
			e.deliver(t, validHook, []byte(`{}`))

			if got := e.rejects(); !slices.Equal(got, []string{tc.want}) {
				t.Fatalf("rejects = %v, want [%s]", got, tc.want)
			}

			entries := e.audit.written()
			if len(entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(entries))
			}

			checkEntry(t, entries[0], tc.want)
		})
	}
}

// checkEntry checks the fields of the entry of one reject.
func checkEntry(t *testing.T, e audit.Entry, reason string) {
	t.Helper()

	if e.Namespace != audit.ServerScope || e.Subject != "webhook" || e.Action != "git-host-event:receive" ||
		e.Object != "git-host-event" || e.Kind != audit.KindSecurity || e.Outcome != audit.OutcomeDenied {
		t.Errorf("entry = %+v", e)
	}

	if !strings.Contains(e.Reason, reason) {
		t.Errorf("reason text %q does not name %q", e.Reason, reason)
	}

	checkEntryHoldsNoRequestData(t, e)
}

// checkEntryHoldsNoRequestData checks that nothing of the request is in e.
func checkEntryHoldsNoRequestData(t *testing.T, e audit.Entry) {
	t.Helper()

	for _, leak := range []string{validHook, unknownHook, signature, "sha256", "X-Evil", "\r", "\n", "ping"} {
		if strings.Contains(fmt.Sprintf("%+v", e), leak) {
			t.Errorf("entry holds %q: %+v", leak, e)
		}
	}

	if e.SourceIP != "" || e.UserAgent != "" || e.TokenID != "" || e.RequestID != "" || e.ObjectID != "" {
		t.Errorf("entry holds request data: %+v", e)
	}
}

// bodyOf returns a body of n bytes.
func bodyOf(n int) []byte { return bytes.Repeat([]byte("a"), n) }

// TestIntakeBodyAtTheBound proves that a body of exactly the bound reaches the
// service whole.
func TestIntakeBodyAtTheBound(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.deliver(t, validHook, bodyOf(githubwebhook.MaxBodyBytes))

	calls := e.receiver.callList()
	if len(calls) != 1 || len(calls[0].req.Body) != githubwebhook.MaxBodyBytes {
		t.Fatalf("calls = %d, want 1 with the whole body", len(calls))
	}

	if len(e.rejects()) != 0 {
		t.Fatalf("rejects = %v, want none", e.rejects())
	}
}

// wantDropped checks that a delivery did not reach the service, got the one
// answer, and counted the reason.
func wantDropped(t *testing.T, e *env, status int, reason string) {
	t.Helper()

	if status != http.StatusNoContent || len(e.receiver.callList()) != 0 {
		t.Fatalf("status = %d, calls = %d, want 204 and no call", status, len(e.receiver.callList()))
	}

	if got := e.rejects(); !slices.Equal(got, []string{reason}) {
		t.Fatalf("rejects = %v, want [%s]", got, reason)
	}
}

// TestIntakeBodyOverTheBound proves that one byte over the bound stops the
// read before the service.
func TestIntakeBodyOverTheBound(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	a := e.deliver(t, validHook, bodyOf(githubwebhook.MaxBodyBytes+1))

	wantDropped(t, e, a.status, forge.EventReasonTooLarge)
}

// TestIntakeChunkedBodyStopsAtTheBound proves that a body with no length stops
// at the bound.
func TestIntakeChunkedBodyStopsAtTheBound(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	src := &countingReader{n: 10 << 20, read: atomic.Int64{}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/"+validHook, src)
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	wantDropped(t, e, rec.Code, forge.EventReasonTooLarge)

	if read := src.read.Load(); read > githubwebhook.MaxBodyBytes+64<<10 {
		t.Fatalf("read %d bytes of the chunked body, the bound is %d", read, githubwebhook.MaxBodyBytes)
	}
}

// TestIntakeBodyThatFailsToRead proves that a broken read is a reject of the
// class malformed.
func TestIntakeBodyThatFailsToRead(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/"+validHook, failingReader{})
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	wantDropped(t, e, rec.Code, forge.EventReasonMalformed)
}

type countingReader struct {
	n    int64
	read atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, io.EOF
	}

	n := min(int64(len(p)), c.n)
	for i := range n {
		p[i] = 'a'
	}

	c.n -= n
	c.read.Add(n)

	return int(n), nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errReset }

// TestIntakeRejectsAreCountedAndLimited proves that a burst of rejects counts
// every one, writes one entry for the reason, and carries the count of the
// others in the next window.
func TestIntakeRejectsAreCountedAndLimited(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.receiver.setErr(refusalOf(forge.EventReasonSignature))

	const burst = 10000

	for range burst {
		e.deliver(t, validHook, []byte(`{}`))
	}

	if got := len(e.rejects()); got != burst {
		t.Fatalf("counted %d rejects, want %d", got, burst)
	}

	if got := len(e.audit.written()); got != 1 {
		t.Fatalf("entries = %d, want 1 for the burst", got)
	}

	// Another reason has its own key.
	e.receiver.setErr(refusalOf(forge.EventReasonUnknownHook))
	e.deliver(t, validHook, []byte(`{}`))

	if got := len(e.audit.written()); got != 2 {
		t.Fatalf("entries = %d, want 2 after a second reason", got)
	}

	// After the window, the next reject writes its entry with the count.
	e.clock.advance(2 * time.Minute)
	e.receiver.setErr(refusalOf(forge.EventReasonSignature))
	e.deliver(t, validHook, []byte(`{}`))

	entries := e.audit.written()
	if len(entries) < 3 {
		t.Fatalf("entries = %d, want an entry in the next window", len(entries))
	}

	counted := false

	for _, entry := range entries[2:] {
		if strings.Contains(entry.Reason, strconv.Itoa(burst-1)) {
			counted = true
		}
	}

	if !counted {
		t.Fatalf("no entry carries the count %d: %+v", burst-1, entries[2:])
	}

	for _, entry := range entries {
		if err := entry.Validate(); err != nil {
			t.Errorf("entry does not validate: %v", err)
		}
	}
}

// TestIntakeKeysAreBoundedByTheReasons proves that a stranger cannot grow the
// table of the limiter: hostile reason texts all land on the key other.
func TestIntakeKeysAreBoundedByTheReasons(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	for i := range 500 {
		e.receiver.setErr(refusalOf(fmt.Sprintf("hostile-%d", i)))
		e.deliver(t, validHook, []byte(`{}`))
	}

	if got := len(e.audit.written()); got != 1 {
		t.Fatalf("entries = %d, want 1: every hostile reason is the reason other", got)
	}

	if got := e.rejects(); got[0] != "other" || len(slices.Compact(slices.Clone(got))) != 1 {
		t.Fatalf("rejects = %v, want other only", got[:3])
	}
}

// TestIntakeAuditFailureKeepsTheAnswer proves that a failed append changes
// nothing for the sender, and counts on the failure counter.
func TestIntakeAuditFailureKeepsTheAnswer(t *testing.T) {
	t.Parallel()

	good := newEnv(t)
	want := good.deliver(t, validHook, []byte(`{}`))

	e := newEnv(t)
	e.audit.setErr(errStore)
	e.receiver.setErr(refusalOf(forge.EventReasonSignature))

	sameAnswer(t, "audit failure", e.deliver(t, validHook, []byte(`{}`)), want)

	if got := e.failures.Load(); got != 1 {
		t.Fatalf("audit failures = %d, want 1", got)
	}

	if got := e.rejects(); !slices.Equal(got, []string{"signature"}) {
		t.Fatalf("rejects = %v, want the reject counted", got)
	}

	// The failed write gives its slot back: the next reject tries again.
	e.audit.setErr(nil)
	e.deliver(t, validHook, []byte(`{}`))

	if got := len(e.audit.written()); got != 1 {
		t.Fatalf("entries = %d, want 1 after the store recovered", got)
	}
}

// TestIntakeHookIDForms proves what the handler gives the service for each
// form of the hook ID, and that no form changes the answer.
func TestIntakeHookIDForms(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	want := e.deliver(t, validHook, []byte(`{}`))

	forms := map[string]string{
		"valid":         validHook,
		"unknown":       unknownHook,
		"upper case":    strings.ToUpper(validHook),
		"escaped slash": "ab%2fcd",
		"escaped NUL":   "ab%00cd",
		"escaped CRLF":  "ab%0d%0acd",
		"long":          strings.Repeat("a", 4096),
		"unicode":       "%e2%80%ae",
		"percent":       "%25",
	}

	for name, form := range forms {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := newEnv(t)
			env.receiver.setErr(refusalOf(forge.EventReasonUnknownHook))

			sameAnswer(t, name, env.deliver(t, form, []byte(`{}`)), want)

			calls := env.receiver.callList()
			if len(calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(calls))
			}

			// The service gets the decoded segment and decides itself.
			if name == "valid" && calls[0].hookID != validHook {
				t.Errorf("hook ID = %q", calls[0].hookID)
			}

			if name == "escaped slash" && calls[0].hookID != "ab/cd" {
				t.Errorf("hook ID = %q, want the decoded segment", calls[0].hookID)
			}
		})
	}
}

// TestIntakeOtherRequestsAnswerTheSameForEveryHookID proves that a method other
// than POST, and a path with another shape, never reaches the service and
// answers the same for a valid and for an unknown hook ID.
func TestIntakeOtherRequestsAnswerTheSameForEveryHookID(t *testing.T) {
	t.Parallel()

	for _, method := range []string{
		http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions,
	} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			a := e.serve(t, method, "/webhooks/"+validHook, nil, nil)
			b := e.serve(t, method, "/webhooks/"+unknownHook, nil, nil)

			sameAnswer(t, method, a, b)

			if a.status == http.StatusNoContent || a.status == http.StatusOK {
				t.Errorf("status = %d, want a refusal", a.status)
			}

			if len(e.receiver.callList()) != 0 || len(e.rejects()) != 0 || len(e.audit.written()) != 0 {
				t.Errorf("a %s request reached the service or the audit", method)
			}
		})
	}

	for _, target := range []string{"/webhooks/", "/webhooks/a/b", "/webhooks", "/webhooks/" + validHook + "/x"} {
		e := newEnv(t)
		a := e.serve(t, http.MethodPost, target, strings.NewReader("{}"), nil)

		if a.status == http.StatusNoContent || len(e.receiver.callList()) != 0 {
			t.Errorf("POST %s: status = %d, calls = %d", target, a.status, len(e.receiver.callList()))
		}
	}
}

// TestIntakePassesTheHeaderAndTheBody proves what the service gets.
func TestIntakePassesTheHeaderAndTheBody(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/webhooks/"+validHook,
		strings.NewReader("body"),
	)
	req.Header.Add("X-Hub-Signature-256", "one")
	req.Header.Add("X-Hub-Signature-256", "two")
	req.Header.Set("X-Github-Event", "ping")

	e.handler.ServeHTTP(httptest.NewRecorder(), req)

	calls := e.receiver.callList()
	if len(calls) != 1 || string(calls[0].req.Body) != "body" {
		t.Fatalf("calls = %+v", calls)
	}

	// Both values reach the verifier, which refuses a request with two.
	if got := calls[0].req.Header["X-Hub-Signature-256"]; !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("signature header = %v, want both values", got)
	}
}

func TestNewHandlerNeedsItsParts(t *testing.T) {
	t.Parallel()

	ok := githubwebhook.Config{
		Receiver: &fakeReceiver{err: nil, mu: sync.Mutex{}, calls: nil},
		Audit:    &fakeAudit{err: nil, mu: sync.Mutex{}, entries: nil},
		Limiter:  nil, OnRejected: nil, OnAuditFailure: nil, Logger: slog.New(slog.DiscardHandler), Now: nil,
	}

	if _, err := githubwebhook.NewHandler(ok); err != nil {
		t.Fatalf("complete config: %v", err)
	}

	noReceiver, noAudit, noLogger, plain := ok, ok, ok, ok
	noReceiver.Receiver = nil
	noAudit.Audit = nil
	noLogger.Logger = nil
	plain.Audit = plainAudit{}

	for name, cfg := range map[string]githubwebhook.Config{
		"no receiver": noReceiver, "no audit": noAudit, "no logger": noLogger, "no installation scope": plain,
	} {
		if _, err := githubwebhook.NewHandler(cfg); !errors.Is(err, githubwebhook.ErrMissingDependency) {
			t.Errorf("%s: error = %v, want ErrMissingDependency", name, err)
		}
	}
}

// TestIntakeRoutes proves the route table: one public row, with no action.
func TestIntakeRoutes(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	rows := e.handler.Routes()
	if len(rows) != 1 || rows[0].Method != http.MethodPost || rows[0].Path != "/webhooks/{hookID}" ||
		len(rows[0].Actions) != 0 {
		t.Fatalf("rows = %+v", rows)
	}

	if got := e.handler.RegisteredPatterns(); !slices.Equal(got, []string{"POST /webhooks/{hookID}"}) {
		t.Fatalf("patterns = %v", got)
	}
}
