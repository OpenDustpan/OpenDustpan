package s3_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

	return resp, nil
}

func (f *faultTransport) attempts(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.counts[method]
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
	wantMinTries int // minimum DELETE tries
}

// runTransientCase opens a store through a faulty transport and checks the
// outcome against tc.
func runTransientCase(t *testing.T, tc transientCase) {
	t.Helper()

	fake := newProbeServer(t, true, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	ft := &faultTransport{
		inner:  server.Client().Transport,
		fail:   tc.fail,
		mu:     sync.Mutex{},
		counts: map[string]int{},
	}

	_, err := s3.Open(
		t.Context(),
		testSigner(server.URL, nil),
		"pfx",
		&http.Client{Transport: ft},
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

	got := ft.attempts(http.MethodDelete)
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
			name: "reset then success", fail: failMethodUntil(http.MethodDelete, 1, reset),
			deadline: 5 * time.Second, wantErr: false, wantErrText: "", wantDeletes: 2, wantMinTries: 0,
		},
		{
			name: "EOF then success", fail: failMethodUntil(http.MethodDelete, 2, io.EOF),
			deadline: 5 * time.Second, wantErr: false, wantErrText: "", wantDeletes: 3, wantMinTries: 0,
		},
		{
			name: "timeout on first write then success", fail: failMethodUntil(http.MethodPut, 1, timeoutError{}),
			deadline: 5 * time.Second, wantErr: false, wantErrText: "", wantDeletes: 0, wantMinTries: 0,
		},
		{
			name: "transient errors until the deadline", fail: failMethodUntil(http.MethodDelete, 1<<30, reset),
			deadline: 150 * time.Millisecond, wantErr: true, wantErrText: "connection reset by peer",
			wantDeletes: 0, wantMinTries: 3,
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

	ft := &faultTransport{
		inner: server.Client().Transport,
		fail: func(m string, _ int) error {
			if m == http.MethodPut {
				cancel()

				return io.EOF
			}

			return nil
		},
		mu:     sync.Mutex{},
		counts: map[string]int{},
	}

	_, err := s3.Open(
		ctx,
		testSigner(server.URL, nil),
		"pfx",
		&http.Client{Transport: ft},
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
