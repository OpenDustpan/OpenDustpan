package runnerapi_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/requestid"
)

// TestWriteErrorLogs500CauseByScope checks the log line of a 500: it holds the
// status, a cause from the closed list and a request ID that is safe, and it
// holds no part of the error text. The answer holds no error text either.
func TestWriteErrorLogs500CauseByScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		scope     runnerapi.Scope
		err       error
		requestID string
		wantLog   []string
		noLog     []string
	}{
		{
			name:      "management: deadline with a valid request ID",
			scope:     runnerapi.ScopeAuthorized,
			err:       fmt.Errorf("write: %w: password=hunter2", context.DeadlineExceeded),
			requestID: "0123abcd",
			wantLog:   []string{"request failed", "status=500", "cause=deadline_exceeded", "request_id=0123abcd"},
			noLog:     []string{"hunter2"},
		},
		{
			name:      "management: unknown error",
			scope:     runnerapi.ScopeAuthorized,
			err:       errUnknown,
			requestID: "0123abcd",
			wantLog:   []string{"status=500", "cause=unclassified", "request_id=0123abcd"},
			noLog:     []string{"hunter2"},
		},
		{
			name:      "management: no request ID",
			scope:     runnerapi.ScopeAuthorized,
			err:       errUnknown,
			requestID: "",
			wantLog:   []string{"status=500", "cause=unclassified"},
			noLog:     []string{"hunter2", "request_id"},
		},
		{
			name:      "management: request ID with CR and LF",
			scope:     runnerapi.ScopeAuthorized,
			err:       errUnknown,
			requestID: "ab\r\nlevel=ERROR forged",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"hunter2", "request_id", "forged"},
		},
		{
			name:      "management: request ID with upper case",
			scope:     runnerapi.ScopeAuthorized,
			err:       errUnknown,
			requestID: "ABCD",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "management: request ID of 65 bytes",
			scope:     runnerapi.ScopeAuthorized,
			err:       errUnknown,
			requestID: strings.Repeat("a", 65),
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "protocol: deadline with a valid request ID",
			scope:     runnerapi.ScopeRunnerKey,
			err:       fmt.Errorf("write: %w: password=hunter2", context.DeadlineExceeded),
			requestID: "0123abcd",
			wantLog:   []string{"request failed", "status=500", "cause=deadline_exceeded", "request_id=0123abcd"},
			noLog:     []string{"hunter2"},
		},
		{
			name:      "protocol: unknown error",
			scope:     runnerapi.ScopeRunnerKey,
			err:       errUnknown,
			requestID: "0123abcd",
			wantLog:   []string{"status=500", "cause=unclassified", "request_id=0123abcd"},
			noLog:     []string{"hunter2"},
		},
		{
			name:      "protocol: no request ID",
			scope:     runnerapi.ScopeRunnerKey,
			err:       errUnknown,
			requestID: "",
			wantLog:   []string{"status=500", "cause=unclassified"},
			noLog:     []string{"hunter2", "request_id"},
		},
		{
			name:      "protocol: request ID with CR and LF",
			scope:     runnerapi.ScopeRunnerKey,
			err:       errUnknown,
			requestID: "ab\r\nlevel=ERROR forged",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"hunter2", "request_id", "forged"},
		},
		{
			name:      "protocol: request ID with upper case",
			scope:     runnerapi.ScopeRunnerKey,
			err:       errUnknown,
			requestID: "ABCD",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "protocol: request ID of 65 bytes",
			scope:     runnerapi.ScopeRunnerKey,
			err:       errUnknown,
			requestID: strings.Repeat("a", 65),
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			ctx := t.Context()
			if tc.requestID != "" {
				ctx = requestid.WithContext(ctx, tc.requestID)
			}

			rec := httptest.NewRecorder()
			runnerapi.WriteError(ctx, rec, slog.New(slog.NewTextHandler(&buf, nil)), tc.scope, tc.err)

			check500Log(t, rec, buf.String(), tc.wantLog, tc.noLog)
		})
	}
}

// check500Log checks the answer and the log of a 500: the answer holds no
// error text, the log is one line, and it holds each text of want and none of
// the texts of no.
func check500Log(t *testing.T, rec *httptest.ResponseRecorder, log string, want, no []string) {
	t.Helper()

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("body = %q, want no error text", rec.Body.String())
	}

	if n := strings.Count(strings.TrimSuffix(log, "\n"), "\n"); n != 0 {
		t.Errorf("log has %d extra lines: %q", n, log)
	}

	for _, w := range want {
		if !strings.Contains(log, w) {
			t.Errorf("log = %q, want it to hold %q", log, w)
		}
	}

	for _, n := range no {
		if strings.Contains(log, n) {
			t.Errorf("log = %q, want it to hold no %q", log, n)
		}
	}
}
