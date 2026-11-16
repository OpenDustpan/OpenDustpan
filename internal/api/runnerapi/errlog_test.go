package runnerapi_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestProtocolErrorsUnchanged pins the runner protocol, which keeps its own
// body, its own codes and its own Retry-After, and gets no Cache-Control
// header. The protocol is a contract that runners depend on.
func TestProtocolErrorsUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{
			name: "claim lost", err: runner.ErrClaimLost,
			status: http.StatusConflict, code: protocol.ErrorClaimLost, retryAfter: "",
		},
		{
			name: "rate limited", err: runner.ErrLimit,
			status: http.StatusTooManyRequests, code: protocol.ErrorRateLimited, retryAfter: "5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			runnerapi.WriteError(t.Context(), rec, slog.New(slog.DiscardHandler), tc.err)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}

			var body protocol.Error

			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}

			if body.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Code, tc.code)
			}

			checkProtocolHeaders(t, rec.Header(), tc.retryAfter)
		})
	}
}

// checkProtocolHeaders fails unless the protocol answer has no Cache-Control
// header and the Retry-After header want.
func checkProtocolHeaders(t *testing.T, header http.Header, retryAfter string) {
	t.Helper()

	if got := header.Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want none", got)
	}

	if got := header.Get("Retry-After"); got != retryAfter {
		t.Errorf("Retry-After = %q, want %q", got, retryAfter)
	}
}
