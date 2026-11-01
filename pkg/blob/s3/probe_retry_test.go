package s3_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// timeoutError is a network timeout error.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

// faultTransport fails requests before they reach the server. fail gets the
// method and the number of this method's request, counted from 1. A nil
// result sends the request on.
type faultTransport struct {
	inner http.RoundTripper
	fail  func(method string, n int) error
	// lose, when set, fails the request after the server handled it: the
	// server holds the effect and the client sees only the error.
	lose func(method string, n int) error

	mu     sync.Mutex
	counts map[string]int
}

func (f *faultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.counts[req.Method]++
	n := f.counts[req.Method]
	f.mu.Unlock()

	if err := f.fail(req.Method, n); err != nil {
		return nil, err
	}

	resp, err := f.inner.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("inner transport: %w", err)
	}

	if f.lose != nil {
		if loseErr := f.lose(req.Method, n); loseErr != nil {
			drainErr := drainAndClose(resp.Body)
			if drainErr != nil {
				return nil, errors.Join(loseErr, drainErr)
			}

			return nil, loseErr
		}
	}

	return resp, nil
}

func (f *faultTransport) attempts(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.counts[method]
}

// drainAndClose reads body to the end and closes it.
func drainAndClose(body io.ReadCloser) error {
	_, copyErr := io.Copy(io.Discard, body)
	closeErr := body.Close()

	return errors.Join(copyErr, closeErr)
}

// realSleep waits for d or until ctx ends.
func realSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return fmt.Errorf("sleep: %w", ctx.Err())
	}
}

// transientCase is one scripted run of Open against a faulty transport.
type transientCase struct {
	name         string
	fail         func(method string, n int) error
	deadline     time.Duration
	wantErr      bool
	wantErrText  string
	wantDeletes  int // exact number of DELETE tries; 0 means not checked
	wantMinTries int // minimum tries of countMethod
	countMethod  string
}

// runTransientCase opens a store through a faulty transport and checks the
// outcome against tc.
func runTransientCase(t *testing.T, tc transientCase) {
	t.Helper()

	fake := newProbeServer(t, true, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	client, ft := newFaultClient(server, tc.fail, noFault)

	_, err := s3.Open(
		t.Context(),
		testSigner(server.URL, nil),
		"pfx",
		client,
		s3.WithSleep(realSleep),
		s3.WithBackoff(func(int) time.Duration { return 5 * time.Millisecond }),
		s3.WithMaxAttempts(1),
		s3.WithProbeDeadline(tc.deadline),
	)

	if tc.wantErr != (err != nil) {
		t.Fatalf("err = %v, want error: %v", err, tc.wantErr)
	}

	if err != nil && !strings.Contains(err.Error(), tc.wantErrText) {
		t.Errorf("error %q lacks the last cause %q", err, tc.wantErrText)
	}

	got := ft.attempts(tc.countMethod)
	if tc.wantDeletes != 0 && got != tc.wantDeletes {
		t.Errorf("DELETE tries = %d, want %d", got, tc.wantDeletes)
	}

	if got < tc.wantMinTries {
		t.Errorf("DELETE tries = %d, want at least %d", got, tc.wantMinTries)
	}
}

// failMethodUntil fails the first n requests of method with err.
func failMethodUntil(method string, n int, err error) func(string, int) error {
	return func(m string, i int) error {
		if m == method && i <= n {
			return err
		}

		return nil
	}
}

func TestOpenProbeTransientErrors(t *testing.T) {
	t.Parallel()

	reset := &net.OpError{
		Op: "read", Net: "tcp", Source: nil, Addr: nil, Err: syscall.ECONNRESET,
	}

	tests := []transientCase{
		{
			name:         "reset then success",
			fail:         failMethodUntil(http.MethodDelete, 1, reset),
			deadline:     5 * time.Second,
			wantErr:      false,
			wantErrText:  "",
			wantDeletes:  2,
			wantMinTries: 0,
			countMethod:  http.MethodDelete,
		},
		{
			name:         "EOF then success",
			fail:         failMethodUntil(http.MethodDelete, 2, io.EOF),
			deadline:     5 * time.Second,
			wantErr:      false,
			wantErrText:  "",
			wantDeletes:  3,
			wantMinTries: 0,
			countMethod:  http.MethodDelete,
		},
		{
			name:         "timeout on first write then success",
			fail:         failMethodUntil(http.MethodPut, 1, timeoutError{}),
			deadline:     5 * time.Second,
			wantErr:      false,
			wantErrText:  "",
			wantDeletes:  0,
			wantMinTries: 0,
			countMethod:  http.MethodDelete,
		},
		{
			name: "transient errors until the deadline", fail: failMethodUntil(http.MethodPut, 1<<30, reset),
			deadline: 150 * time.Millisecond, wantErr: true, wantErrText: "connection reset by peer",
			wantDeletes: 0, wantMinTries: 3, countMethod: http.MethodPut,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runTransientCase(t, tc)
		})
	}
}

func TestOpenProbeNonTransientFailsAtOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		code   string
	}{
		{name: "access denied", status: http.StatusForbidden, code: "AccessDenied"},
		{name: "no bucket", status: http.StatusNotFound, code: "NoSuchBucket"},
		{name: "bad signature", status: http.StatusForbidden, code: "SignatureDoesNotMatch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeServer(t, func(*http.Request, []byte, int) (int, string) {
				return tt.status, errorDoc(tt.code)
			})
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)

			_, err := s3.Open(
				t.Context(),
				testSigner(server.URL, nil),
				"pfx",
				server.Client(),
				s3.WithSleep(realSleep),
				s3.WithProbeDeadline(5*time.Second),
			)
			if err == nil {
				t.Fatal("open succeeded, want an error")
			}

			if !strings.Contains(err.Error(), tt.code) {
				t.Errorf("error %q lacks code %q", err, tt.code)
			}

			// One PUT, then the cleanup DELETE. No retry of either.
			if got := fake.calls(); got != 2 {
				t.Errorf("server saw %d requests, want 2 (one PUT, one DELETE)", got)
			}
		})
	}
}

func TestOpenProbeCanceledContext(t *testing.T) {
	t.Parallel()

	fake := newProbeServer(t, true, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := s3.Open(ctx, testSigner(server.URL, nil), "pfx", server.Client(), s3.WithSleep(realSleep))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	if got := len(fake.putPaths()); got != 0 {
		t.Errorf("server saw %d PUTs, want none", got)
	}
}

func TestOpenProbeCanceledDuringRetry(t *testing.T) {
	t.Parallel()

	fake := newProbeServer(t, true, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())

	client, ft := newFaultClient(server, func(m string, _ int) error {
		if m == http.MethodPut {
			cancel()

			return io.EOF
		}

		return nil
	}, noFault)

	_, err := s3.Open(
		ctx,
		testSigner(server.URL, nil),
		"pfx",
		client,
		s3.WithSleep(realSleep),
		s3.WithBackoff(func(int) time.Duration { return time.Hour }),
		s3.WithMaxAttempts(1),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	if !strings.Contains(err.Error(), "EOF") {
		t.Errorf("error %q lacks the last cause", err)
	}

	if got := ft.attempts(http.MethodPut); got != 1 {
		t.Errorf("PUT tries = %d, want 1", got)
	}
}

// newFaultClient returns a client that sends through a faultTransport.
func newFaultClient(server *httptest.Server, fail, lose func(string, int) error) (*http.Client, *faultTransport) {
	ft := &faultTransport{
		inner:  server.Client().Transport,
		fail:   fail,
		lose:   lose,
		mu:     sync.Mutex{},
		counts: map[string]int{},
	}

	return &http.Client{Transport: ft}, ft
}

func noFault(string, int) error { return nil }

// fastProbeOptions returns options for a quick probe run with one try for
// each store call, so that the probe loop makes every retry.
func fastProbeOptions(extra ...s3.Option) []s3.Option {
	opts := make([]s3.Option, 0, 4+len(extra))
	opts = append(opts,
		s3.WithSleep(realSleep),
		s3.WithBackoff(func(int) time.Duration { return time.Millisecond }),
		s3.WithMaxAttempts(1),
		s3.WithProbeDeadline(5*time.Second),
	)

	return append(opts, extra...)
}

func TestOpenProbeLostAnswerDeletesOrphan(t *testing.T) {
	t.Parallel()

	fake := newProbeServer(t, true, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	// The first PUT reaches the server and the answer is lost.
	lose := func(m string, n int) error {
		if m == http.MethodPut && n == 1 {
			return io.EOF
		}

		return nil
	}

	client, _ := newFaultClient(server, noFault, lose)

	if _, err := s3.Open(t.Context(), testSigner(server.URL, nil), "pfx", client, fastProbeOptions()...); err != nil {
		t.Fatalf("open: %v", err)
	}

	if got := fake.objectCount(); got != 0 {
		t.Errorf("server holds %d objects, want none", got)
	}

	puts := fake.putPaths()
	if len(puts) < 2 || puts[0] != puts[1] {
		t.Fatalf("PUT paths = %v, want the first key sent twice", puts)
	}

	if !slices.Contains(fake.deletePaths(), puts[0]) {
		t.Errorf("DELETE paths = %v, want the first key %s deleted", fake.deletePaths(), puts[0])
	}
}

// onceFailServer answers the first PUT with an error answer, then passes
// requests to the probe server.
func onceFailServer(t *testing.T, status int, code string) (*httptest.Server, *probeServer) {
	t.Helper()

	fake := newProbeServer(t, true, false)

	var failed atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && failed.CompareAndSwap(false, true) {
			w.WriteHeader(status)

			if _, err := io.WriteString(w, errorDoc(code)); err != nil {
				t.Errorf("write answer: %v", err)
			}

			return
		}

		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	return server, fake
}

// answerCase is one error answer of the store to the first probe write.
type answerCase struct {
	name      string
	status    int
	code      string
	wantRetry bool
}

// checkAnswerCase opens a store whose first PUT gets the answer of tc.
func checkAnswerCase(t *testing.T, tc answerCase) {
	t.Helper()

	server, fake := onceFailServer(t, tc.status, tc.code)

	_, err := s3.Open(t.Context(), testSigner(server.URL, nil), "pfx", server.Client(), fastProbeOptions()...)

	if tc.wantRetry {
		if err != nil {
			t.Fatalf("open: %v, want success after a retry", err)
		}

		// The retry of the first write and the second write.
		if got := len(fake.putPaths()); got != 2 {
			t.Errorf("server handled %d PUTs, want 2", got)
		}

		return
	}

	if err == nil {
		t.Fatal("open succeeded, want an error")
	}

	if !strings.Contains(err.Error(), tc.code) {
		t.Errorf("error %q lacks code %q", err, tc.code)
	}

	if got := len(fake.putPaths()); got != 0 {
		t.Errorf("server handled %d PUTs, want none: the error answer must not be retried", got)
	}
}

func TestOpenProbeAnswerClasses(t *testing.T) {
	t.Parallel()

	tests := []answerCase{
		{name: "500", status: http.StatusInternalServerError, code: "InternalError", wantRetry: true},
		{name: "502", status: http.StatusBadGateway, code: "BadGateway", wantRetry: true},
		{name: "503", status: http.StatusServiceUnavailable, code: "ServiceUnavailable", wantRetry: true},
		{name: "504", status: http.StatusGatewayTimeout, code: "GatewayTimeout", wantRetry: true},
		{name: "429", status: http.StatusTooManyRequests, code: "TooManyRequests", wantRetry: true},
		{name: "SlowDown", status: http.StatusServiceUnavailable, code: "SlowDown", wantRetry: true},
		{name: "400", status: http.StatusBadRequest, code: "BadRequest", wantRetry: false},
		{name: "501", status: http.StatusNotImplemented, code: "NotImplemented", wantRetry: false},
		{name: "505", status: http.StatusHTTPVersionNotSupported, code: "HTTPVersionNotSupported", wantRetry: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkAnswerCase(t, tt)
		})
	}
}

func TestOpenProbeWrongResultIsNotRetried(t *testing.T) {
	t.Parallel()

	// The store ignores If-None-Match: the second write succeeds.
	fake := newProbeServer(t, false, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	_, err := s3.Open(t.Context(), testSigner(server.URL, nil), "pfx", server.Client(), fastProbeOptions()...)
	if !errors.Is(err, s3.ErrNoConditionalWrites) {
		t.Fatalf("err = %v, want ErrNoConditionalWrites", err)
	}

	if got := len(fake.putPaths()); got != 2 {
		t.Errorf("server handled %d PUTs, want 2", got)
	}

	if got := fake.objectCount(); got != 0 {
		t.Errorf("server holds %d objects, want none", got)
	}
}

func TestOpenProbeNetworkErrorClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{
			name: "connection refused",
			err: &net.OpError{
				Op: "dial", Net: "tcp", Source: nil, Addr: nil, Err: syscall.ECONNREFUSED,
			},
			wantRetry: true,
		},
		{
			name: "temporary DNS error",
			err: &net.DNSError{
				Err: "server misbehaving", Name: "s3.test", Server: "", IsTimeout: false,
				IsTemporary: true, IsNotFound: false, UnwrapErr: nil,
			},
			wantRetry: true,
		},
		{
			name: "DNS name not found",
			err: &net.DNSError{
				Err: "no such host", Name: "s3.test", Server: "", IsTimeout: false,
				IsTemporary: false, IsNotFound: true, UnwrapErr: nil,
			},
			wantRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newProbeServer(t, true, false)
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)

			client, ft := newFaultClient(server, failMethodUntil(http.MethodPut, 1, tt.err), noFault)

			_, err := s3.Open(t.Context(), testSigner(server.URL, nil), "pfx", client, fastProbeOptions()...)

			if tt.wantRetry {
				if err != nil {
					t.Fatalf("open: %v, want success after a retry", err)
				}

				return
			}

			if err == nil {
				t.Fatal("open succeeded, want an error")
			}

			if got := ft.attempts(http.MethodPut); got != 1 {
				t.Errorf("PUT tries = %d, want 1: a name that does not exist must not be retried", got)
			}
		})
	}
}

// recordHandler keeps the log records it gets.
type recordHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r)

	return nil
}

func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordHandler) WithGroup(string) slog.Handler      { return h }

// loggedErrorText runs Open against a store whose first PUT gets status 503
// with code, and returns the error text of the one retry record.
func loggedErrorText(t *testing.T, code string) string {
	t.Helper()

	server, _ := onceFailServer(t, http.StatusServiceUnavailable, code)
	handler := &recordHandler{mu: sync.Mutex{}, records: nil}

	_, err := s3.Open(
		t.Context(), testSigner(server.URL, nil), "pfx", server.Client(),
		fastProbeOptions(s3.WithLogger(slog.New(handler)))...,
	)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()

	if len(handler.records) != 1 {
		t.Fatalf("got %d log records, want 1", len(handler.records))
	}

	var text string

	handler.records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "error" {
			text = a.Value.String()
		}

		return true
	})

	return text
}

func TestOpenProbeRetryLogIsSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
	}{
		{name: "CR and LF", code: "InternalError&#13;&#10;level=ERROR msg=forged"},
		{name: "long text", code: strings.Repeat("A", 4000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text := loggedErrorText(t, tt.code)

			if text == "" {
				t.Fatal("log record has no error text")
			}

			if strings.ContainsAny(text, "\r\n") {
				t.Errorf("error text %q holds CR or LF", text)
			}

			if len(text) > 512 {
				t.Errorf("error text has %d bytes, want at most 512", len(text))
			}
		})
	}
}
