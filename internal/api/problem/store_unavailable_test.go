package problem_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriterStoreLost proves each way that a store reports a database that
// does not answer gives the problem shape 503 with Retry-After 5 and the
// catalog code, also when the classifier of the API has another answer for the
// same error.
func TestWriterStoreLost(t *testing.T) {
	t.Parallel()

	errDomain := errors.New("domain error")

	other := func(error) (problem.Problem, bool) {
		return problem.New(http.StatusConflict, errcode.ServerInternal, "conflict"), true
	}

	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", namespace.ErrUnavailable},
		{"wrapped sentinel", fmt.Errorf("list units: %w", namespace.ErrUnavailable)},
		{"connection failed", fmt.Errorf("%w: connection failed: %w", namespace.ErrUnavailable, syscall.ECONNREFUSED)},
		{"connection lost", errors.Join(namespace.ErrUnavailable, io.ErrUnexpectedEOF)},
		{"server shutdown", fmt.Errorf("%w: server is shutting down", namespace.ErrUnavailable)},
		{"read only", fmt.Errorf("%w: database is read-only", namespace.ErrUnavailable)},
		{"busy", fmt.Errorf("%w: database is busy", namespace.ErrUnavailable)},
		{"joined with a domain error", errors.Join(errDomain, namespace.ErrUnavailable)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := writeWith(t, problem.Writer{Logger: slog.New(slog.DiscardHandler), Classify: other, KeepDenied: nil}, tc.err)

			assertStoreLost(t, rec)
		})
	}
}

func assertStoreLost(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After = %q, want 5", got)
	}

	var body struct {
		Code      int    `json:"code"`
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}

	if body.Code != http.StatusServiceUnavailable || body.ErrorCode != errcode.NamespaceUnavailable {
		t.Fatalf("body = %+v, want code 503 and %s", body, errcode.NamespaceUnavailable)
	}
}
