package post_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/internal/notify/post"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
)

// markedURL is a URL with a marker in its path. No error may repeat it.
const markedURL = "https://hooks.example.com/T0K3N-in-the-path"

// Static errors of the tests.
var (
	errNotHTTPS  = errors.New("not https")
	errNoHost    = errors.New("no host")
	errRead      = errors.New("read failed")
	errConnReset = errors.New("connection reset")
)

// answer returns an answer with code and the Retry-After header retryAfter,
// when it is not empty.
func answer(code int, retryAfter string) *http.Response {
	var resp http.Response

	resp.StatusCode = code
	resp.Header = http.Header{}
	resp.Body = http.NoBody

	if retryAfter != "" {
		resp.Header.Set("Retry-After", retryAfter)
	}

	return &resp
}

// hostOf returns the host of a URL like the host readers of notify do, for
// URLs that the tests below build.
func hostOf(raw string) (string, error) {
	rest, ok := strings.CutPrefix(raw, "https://")
	if !ok {
		return "", errNotHTTPS
	}

	host, _, _ := strings.Cut(rest, "/")

	return host, nil
}

// TestCheckTargetRefusesHostileURLs sends each hostile URL straight to
// CheckTarget, as if the sink service had not checked it.
func TestCheckTargetRefusesHostileURLs(t *testing.T) {
	t.Parallel()

	failingHost := func(string) (string, error) { return "", errNoHost }

	cases := []struct {
		name   string
		to     string
		target string
		hostOf func(string) (string, error)
	}{
		{"http URL", "http://hooks.example.com/T0K3N", "hooks.example.com", hostOf},
		{"loopback", "https://127.0.0.1/T0K3N", "127.0.0.1", hostOf},
		{"port not allowed", "https://hooks.example.com:8443/T0K3N", "hooks.example.com:8443", hostOf},
		{"host reader fails", markedURL, "hooks.example.com", failingHost},
		{"host is not the target", markedURL, "other.example.com", hostOf},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := post.CheckTarget("post test", tc.to, tc.target, egress.Ports{}, tc.hostOf)
			if !errors.Is(err, egress.ErrRefused) {
				t.Fatalf("CheckTarget(%q) = %v, want egress.ErrRefused", tc.to, err)
			}

			if strings.Contains(err.Error(), "T0K3N") {
				t.Fatalf("error %q repeats the URL", err)
			}
		})
	}
}

// TestCheckTargetAllowsTheTarget checks that an https URL on port 443 whose
// host is the target of the sink passes.
func TestCheckTargetAllowsTheTarget(t *testing.T) {
	t.Parallel()

	if err := post.CheckTarget("post test", markedURL, "hooks.example.com", egress.Ports{}, hostOf); err != nil {
		t.Fatalf("CheckTarget = %v, want nil", err)
	}
}

// TestTransportErrorKeepsTheClassOnly checks the class of each request
// error, and that no error repeats the text of the request error.
func TestTransportErrorKeepsTheClassOnly(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	leak := func(err error) error { return fmt.Errorf("dial %s: %w", markedURL, err) }

	cases := []struct {
		name string
		ctx  context.Context //nolint:containedctx // Each case names the context of its request.
		err  error
		want error
	}{
		{"dial guard", t.Context(), leak(egresshttp.ErrDialRefused), notify.ErrTemporary},
		{"egress refusal", t.Context(), leak(egress.ErrRefused), egress.ErrRefused},
		{"context ended", canceled, leak(errRead), context.Canceled},
		{"other", t.Context(), leak(errConnReset), notify.ErrTemporary},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := post.TransportError(tc.ctx, "post test", tc.err)
			if !errors.Is(got, tc.want) {
				t.Fatalf("TransportError = %v, want %v", got, tc.want)
			}

			if errors.Is(got, egresshttp.ErrDialRefused) {
				// A dial refusal must read as a failed connection.
				t.Fatalf("TransportError = %v, want no dial refusal class", got)
			}

			if strings.Contains(got.Error(), "T0K3N") {
				t.Fatalf("error %q repeats the URL", got)
			}
		})
	}
}

// TestStatusErrorClasses checks the class of each answer status.
func TestStatusErrorClasses(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0)

	cases := []struct {
		code       int
		retryAfter string
		want       error
	}{
		{http.StatusOK, "", nil},
		{http.StatusNoContent, "", nil},
		{http.StatusMovedPermanently, "", notify.ErrPermanent},
		{http.StatusBadRequest, "", notify.ErrPermanent},
		{http.StatusNotFound, "", notify.ErrPermanent},
		{http.StatusRequestTimeout, "", notify.ErrTemporary},
		{http.StatusTooManyRequests, "", notify.ErrTemporary},
		{http.StatusInternalServerError, "", notify.ErrTemporary},
		{http.StatusBadGateway, "not a time", notify.ErrTemporary},
		{http.StatusContinue, "", notify.ErrTemporary},
	}

	for _, tc := range cases {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			t.Parallel()

			resp := answer(tc.code, tc.retryAfter)
			got := post.StatusError("post test", resp, now)

			if err := resp.Body.Close(); err != nil {
				t.Fatalf("close body: %v", err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("StatusError(%d) = %v, want nil", tc.code, got)
				}

				return
			}

			if !errors.Is(got, tc.want) {
				t.Fatalf("StatusError(%d) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestStatusErrorRetryAfter checks that a temporary answer with a
// Retry-After header gives the wait.
func TestStatusErrorRetryAfter(t *testing.T) {
	t.Parallel()

	for _, code := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		resp := answer(code, "30")
		got := post.StatusError("post test", resp, time.Unix(1700000000, 0))

		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close body: %v", err)
		}

		wait, ok := errors.AsType[*notify.RetryAfterError](got)
		if !ok || wait.Wait != 30*time.Second {
			t.Fatalf("StatusError(%d) = %v, want a wait of 30s", code, got)
		}
	}
}
