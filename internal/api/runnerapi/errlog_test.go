package runnerapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// errUnknown is an error that no case of a classifier knows. Its text names a
// secret, so a test can see whether a log line holds it.
var errUnknown = errors.New("driver: password=hunter2")

// TestManagementErrorHeaders fails for an error answer of the management API
// that a cache may keep: every such answer carries Cache-Control: no-store.
func TestManagementErrorHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"not found", runner.ErrNotFound, http.StatusNotFound},
		{"unknown error", errUnknown, http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			runnerapi.WriteError(
				t.Context(), rec, slog.New(slog.DiscardHandler), runnerapi.ScopeAuthorized, tc.err,
			)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want %q", got, "no-store")
			}
		})
	}
}

// logCase is one error of the management API with the log lines it must and
// must not write, and the message of its answer when that is checked.
type logCase struct {
	name        string
	err         error
	status      int
	wantLog     []string
	wantNoLog   []string
	wantMessage string
}

// TestManagementErrorLog checks the log lines of the management API. A 500
// logs its status and never the error text. A refused code logs its key
// error_code and never the message. A fenced write logs one WARN line with the
// wrap text, and the answer holds the sentence of the sentinel only.
func TestManagementErrorLog(t *testing.T) {
	t.Parallel()

	tests := []logCase{
		{
			name:        "unknown error",
			err:         errUnknown,
			status:      http.StatusInternalServerError,
			wantLog:     []string{`msg="request failed"`, "status=500"},
			wantNoLog:   []string{"hunter2", "password"},
			wantMessage: "",
		},
		{
			name:        "refused code",
			err:         problem.BadRequest("runner.no-such-code", "secret detail"),
			status:      http.StatusInternalServerError,
			wantLog:     []string{`msg="error code refused"`, "error_code=runner.no-such-code"},
			wantNoLog:   []string{"secret detail"},
			wantMessage: "",
		},
		{
			name:        "fenced write",
			err:         fmt.Errorf("commit pool: %w", namespace.ErrFenced),
			status:      http.StatusConflict,
			wantLog:     []string{`msg="write fenced"`, "commit pool:"},
			wantNoLog:   nil,
			wantMessage: namespace.ErrFenced.Error(),
		},
		{
			name:        "refusal with its wrap text",
			err:         fmt.Errorf("body: %w", runner.ErrInvalid),
			status:      http.StatusBadRequest,
			wantLog:     []string{`msg="runner request refused"`, "body:"},
			wantNoLog:   nil,
			wantMessage: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer

			rec := httptest.NewRecorder()
			runnerapi.WriteError(
				t.Context(), rec, slog.New(slog.NewTextHandler(&logs, nil)), runnerapi.ScopeAuthorized, tc.err,
			)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}

			checkLog(t, logs.String(), tc)
			checkMessage(t, rec.Body.Bytes(), tc.wantMessage)
		})
	}
}

// checkLog fails unless log holds each of tc.wantLog and none of tc.wantNoLog.
func checkLog(t *testing.T, log string, tc logCase) {
	t.Helper()

	for _, want := range tc.wantLog {
		if !strings.Contains(log, want) {
			t.Errorf("log = %q, want it to hold %q", log, want)
		}
	}

	for _, no := range tc.wantNoLog {
		if strings.Contains(log, no) {
			t.Errorf("log = %q, want it not to hold %q", log, no)
		}
	}
}

// checkMessage fails unless the message of the answer body is want. An empty
// want is not checked.
func checkMessage(t *testing.T, body []byte, want string) {
	t.Helper()

	if want == "" {
		return
	}

	var got struct {
		Message string `json:"message"`
	}

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if got.Message != want {
		t.Errorf("message = %q, want %q", got.Message, want)
	}
}

// TestManagementSharedErrors proves that an error of a class that every
// tool-neutral API shares gets the shared answer, not the 500 of the classifier
// of this API.
func TestManagementSharedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		status   int
		wantCode string
	}{
		{"invalid namespace name", namespace.ErrInvalidName, http.StatusBadRequest, errcode.NamespaceInvalidName},
		{"lost idempotency key", idempotency.ErrLost, http.StatusConflict, idempotent.CodeLost},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			runnerapi.WriteError(
				t.Context(), rec, slog.New(slog.DiscardHandler), runnerapi.ScopeAuthorized, tc.err,
			)

			var body struct {
				Code      int    `json:"code"`
				ErrorCode string `json:"errorCode"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}

			if rec.Code != tc.status || body.ErrorCode != tc.wantCode {
				t.Errorf("status = %d, code = %q; want %d, %q", rec.Code, body.ErrorCode, tc.status, tc.wantCode)
			}
		})
	}
}

// TestProtocolErrorsUnchanged pins the runner protocol, which keeps its own
// body, its own codes and its own Retry-After, and gets no Cache-Control
// header. The protocol is a contract that runners depend on.
func TestProtocolErrorsUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scope      runnerapi.Scope
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{
			name: "claim lost", scope: runnerapi.ScopeRunnerKey, err: runner.ErrClaimLost,
			status: http.StatusConflict, code: protocol.ErrorClaimLost, retryAfter: "",
		},
		{
			name: "rate limited", scope: runnerapi.ScopeJoinToken, err: runner.ErrLimit,
			status: http.StatusTooManyRequests, code: protocol.ErrorRateLimited, retryAfter: "5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			runnerapi.WriteError(t.Context(), rec, slog.New(slog.DiscardHandler), tc.scope, tc.err)

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
