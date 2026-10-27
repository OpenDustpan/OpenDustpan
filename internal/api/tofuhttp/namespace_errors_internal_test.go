package tofuhttp

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriteErrorNamespaceSentinels proves each namespace sentinel answers its
// own status, never a 500: a fenced write answers 409, a namespace homed in
// another installation 421, and a database that does not answer 503 with a
// Retry-After time.
func TestWriteErrorNamespaceSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		want       int
		message    string
		retryAfter bool
	}{
		{
			name: "fenced", err: namespace.ErrFenced, want: http.StatusConflict,
			message: namespace.ErrFenced.Error(), retryAfter: false,
		},
		{
			name: "not home", err: namespace.ErrNotHome, want: http.StatusMisdirectedRequest,
			message: namespace.ErrNotHome.Error(), retryAfter: false,
		},
		{
			name: "unavailable", err: namespace.ErrUnavailable, want: http.StatusServiceUnavailable,
			message: namespace.ErrUnavailable.Error(), retryAfter: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h Handler

			h.logger = slog.New(slog.DiscardHandler)

			rec := httptest.NewRecorder()
			h.writeError(t.Context(), rec, fmt.Errorf("write state of acme/prod: %w", tc.err))

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}

			if got := rec.Header().Get("Retry-After") != ""; got != tc.retryAfter {
				t.Fatalf("Retry-After set = %t, want %t", got, tc.retryAfter)
			}

			if body := rec.Body.String(); !strings.Contains(body, tc.message) {
				t.Fatalf("body = %q, want it to hold %q", body, tc.message)
			}

			if body := rec.Body.String(); strings.Contains(body, "acme/prod") {
				t.Fatalf("body = %q, want no request detail", body)
			}
		})
	}
}
