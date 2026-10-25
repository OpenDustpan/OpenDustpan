package jobs_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestErrorAnswerHeaders fails for an error answer that a cache may keep: every
// error answer of the jobs API carries Cache-Control: no-store.
func TestErrorAnswerHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"invalid job", job.ErrInvalid, http.StatusBadRequest},
		{"unknown error", errUnknown, http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			jobsapi.WriteError(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want %q", got, "no-store")
			}
		})
	}
}

// refusedCode answers every error with a code that the catalog refuses.
func refusedCode(error) (problem.Problem, bool) {
	return problem.Problem{
		Status: http.StatusBadRequest, Code: "jobs.no-such-code", Values: nil, Message: "secret detail",
		RetryAfter: 0, Reauthenticate: "",
	}, true
}

// errDriverDetail is an error whose text must never reach a log.
var errDriverDetail = errors.New("driver detail")

// TestErrorLog fails for an error answer whose log line is not the one of the
// shared error module: a refused code logs error_code, a fenced write logs its
// wrap, and a 500 logs its status and never the error text.
func TestErrorLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		classify problem.Classifier
		err      error
		wantLog  []string
		noLog    []string
		wantCode string
	}{
		{
			name: "unknown error", classify: nil, err: errUnknown,
			wantLog: []string{"request failed", "status=500"}, noLog: []string{errUnknown.Error()},
			wantCode: errcode.ServerInternal,
		},
		{
			name: "refused code", classify: refusedCode, err: errDriverDetail,
			wantLog: []string{"error code refused", "error_code=jobs.no-such-code"},
			noLog:   []string{"secret detail", "driver detail"}, wantCode: errcode.ServerInternal,
		},
		{
			name: "fenced", classify: nil, err: fmt.Errorf("start the check: %w", namespace.ErrFenced),
			wantLog: []string{"write fenced", "start the check"}, noLog: nil, wantCode: errcode.NamespaceFenced,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&logs, nil))
			rec := httptest.NewRecorder()

			jobsapi.WriteErrorTo(t.Context(), rec, logger, tc.classify, tc.err)

			log := logs.String()
			for _, want := range tc.wantLog {
				if !strings.Contains(log, want) {
					t.Errorf("log = %q, want it to hold %q", log, want)
				}
			}

			for _, no := range tc.noLog {
				if strings.Contains(log, no) {
					t.Errorf("log = %q, want it not to hold %q", log, no)
				}
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.wantCode, Args: nil, RequestBody: nil})
		})
	}
}

// TestSharedErrorsKeepTheirAnswer fails for an error of a class that every
// tool-neutral API shares, when the jobs API does not answer it in the shared
// words: the answer of the shared class is the one that the other APIs give.
func TestSharedErrorsKeepTheirAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid namespace name", namespace.ErrInvalidName, http.StatusBadRequest, errcode.NamespaceInvalidName},
		{"lost idempotency key", idempotent.Lost(), http.StatusConflict, idempotent.CodeLost},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			jobsapi.WriteError(t.Context(), rec, tc.err)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}
