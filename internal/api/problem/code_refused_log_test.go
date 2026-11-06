package problem_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/requestid"
)

// TestRefusedCodeLogsThroughFaultlog checks that a code that the catalog
// refuses logs the code and the request ID in the fields of a 500 line, that a
// hostile code is left out, and that the answer is the internal error.
func TestRefusedCodeLogsThroughFaultlog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		code    string
		wantLog []string
		noLog   []string
	}{
		{
			"unknown code",
			"no.such.code",
			[]string{"error code refused", "error_code=no.such.code", "request_id=0123abcd"},
			nil,
		},
		{
			"code with CR and LF",
			"x\r\nlevel=ERROR forged",
			[]string{"error code refused"},
			[]string{"forged", "error_code=x"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			wr := problem.Writer{Logger: slog.New(slog.NewTextHandler(&buf, nil)), KeepDenied: nil, Classify: nil}
			rec := httptest.NewRecorder()
			wr.WriteProblem(
				requestid.WithContext(t.Context(), "0123abcd"),
				rec,
				problem.New(http.StatusBadRequest, tc.code, "m"),
			)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rec.Code)
			}

			checkLine(t, buf.String(), tc.wantLog, tc.noLog)
		})
	}
}

// checkLine checks that log holds each text of want and none of the texts of
// no.
func checkLine(t *testing.T, log string, want, no []string) {
	t.Helper()

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
