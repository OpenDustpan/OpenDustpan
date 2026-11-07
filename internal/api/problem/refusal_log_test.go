package problem_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/runner"
)

// secretSession is the text of a session detail that the wrap of these tests
// holds. No log line and no answer may hold it.
const secretSession = "s3cr3t-session-value"

// runnerRefusal answers a refusal of the runner management API the way that
// API's classifier does: with the sentence of the sentinel, never the wrap
// text.
func runnerRefusal(err error) (problem.Problem, bool) {
	if errors.Is(err, runner.ErrInvalid) {
		return problem.New(http.StatusBadRequest, errcode.RunnerInvalid, runner.ErrInvalid.Error()), true
	}

	return problem.Pass()
}

// refusalLogCase is one row of TestRefusalLogHoldsNoWrapText: the error, the
// classifier of its API, and the answer that the refusal keeps.
type refusalLogCase struct {
	name     string
	classify problem.Classifier
	err      error
	status   int
	message  string
	code     string
}

// TestRefusalLogHoldsNoWrapText checks that the request refused line names the
// status and the code only. The wrap of err can hold a session detail, and the
// log never holds it. The answer keeps the status and the body that it had.
func TestRefusalLogHoldsNoWrapText(t *testing.T) {
	t.Parallel()

	tests := []refusalLogCase{
		{
			name:     "domain refusal with a session detail in the wrap",
			classify: domain,
			err:      fmt.Errorf("save session: %s: %w", secretSession, errDomain),
			status:   http.StatusConflict,
			message:  "refused",
			code:     errcode.SinkConflict,
		},
		{
			name:     "runner management refusal with a session detail in the wrap",
			classify: runnerRefusal,
			err:      fmt.Errorf("check runner claim: %s: %w", secretSession, runner.ErrInvalid),
			status:   http.StatusBadRequest,
			message:  runner.ErrInvalid.Error(),
			code:     errcode.RunnerInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			wr := problem.Writer{
				Logger:     slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
				KeepDenied: nil,
				Classify:   tc.classify,
			}
			rec := httptest.NewRecorder()

			wr.Write(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}

			checkRefusalLogCase(t, tc, buf.String(), rec)
		})
	}
}

// checkRefusalLogCase fails unless the log and the answer follow the rule of
// TestRefusalLogHoldsNoWrapText: the line names the status and the code, and
// the wrap text, which holds the session detail, reaches neither of them.
func checkRefusalLogCase(t *testing.T, tc refusalLogCase, logs string, rec *httptest.ResponseRecorder) {
	t.Helper()

	for _, want := range []string{"level=DEBUG", `msg="request refused"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("log %q does not hold %q", logs, want)
		}
	}

	if got := body(t, rec); got.Message != tc.message || got.ErrorCode != tc.code {
		t.Errorf("body = %+v, want message %q and code %q", got, tc.message, tc.code)
	}

	if !refusedLineHolds(logs, fmt.Sprintf("status=%d", tc.status)) {
		t.Errorf("request refused line does not hold status=%d: log %q", tc.status, logs)
	}

	if !refusedLineHolds(logs, "code="+tc.code) {
		t.Errorf("request refused line does not hold code=%s: log %q", tc.code, logs)
	}

	if strings.Contains(logs, secretSession) {
		t.Errorf("log %q holds the session detail", logs)
	}

	if strings.Contains(rec.Body.String(), secretSession) {
		t.Errorf("body %q holds the session detail", rec.Body.String())
	}
}
