package problem_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
)

// textError is an error with a given text.
type textError string

func (r textError) Error() string { return string(r) }

// answerOf writes p with WriteProblem and returns the recorder.
func answerOf(t *testing.T, p problem.Problem) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer

	wr := problem.Writer{Logger: slog.New(slog.NewTextHandler(&buf, nil)), KeepDenied: nil, Classify: nil}
	rec := httptest.NewRecorder()

	wr.WriteProblem(t.Context(), rec, p)

	return rec
}

// TestMaxMessageBytes pins the one length of a repeated refusal text.
func TestMaxMessageBytes(t *testing.T) {
	t.Parallel()

	if problem.MaxMessageBytes != 1280 {
		t.Fatalf("MaxMessageBytes = %d, want 1280", problem.MaxMessageBytes)
	}
}

// TestRefused checks that Refused answers the cleaned text of the error, cut at
// MaxMessageBytes, with the status and the code that the caller gives.
func TestRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		message string
	}{
		{"short text", textError("name is empty"), "name is empty"},
		{"control character", textError("bad\x07name\r\nnext"), `bad\u0007name\u000d\u000anext`},
		{"invalid UTF-8", textError("bad\xffname"), "bad�name"},
		{
			"cut at the limit",
			textError(strings.Repeat("a", 2*problem.MaxMessageBytes)),
			strings.Repeat("a", problem.MaxMessageBytes),
		},
		{
			"cut on a rune boundary",
			textError(strings.Repeat("é", problem.MaxMessageBytes)),
			strings.Repeat("é", problem.MaxMessageBytes/2),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := problem.Refused(tc.err, http.StatusConflict, errcode.SinkConflict)

			if p.Status != http.StatusConflict || p.Code != errcode.SinkConflict || p.Values != nil {
				t.Errorf("problem = %+v, want 409 %s with no values", p, errcode.SinkConflict)
			}

			if p.Message != tc.message {
				t.Errorf("message = %q, want %q", p.Message, tc.message)
			}

			if len(p.Message) > problem.MaxMessageBytes {
				t.Errorf("message has %d bytes, want at most %d", len(p.Message), problem.MaxMessageBytes)
			}
		})
	}
}

// TestNew checks the order of the arguments of New (status, code, message) and
// that it carries the values of the code. A code where the message goes is a
// code that the catalog refuses, so the writer answers server.internal.
func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		p      problem.Problem
		status int
		code   string
		args   map[string]any
		text   string
	}{
		{
			"no value", problem.New(http.StatusConflict, errcode.SinkConflict, "changed"),
			http.StatusConflict, errcode.SinkConflict, nil, "changed",
		},
		{
			"with a value",
			problem.New(http.StatusTooManyRequests, errcode.SinkTestRateLimited, "wait",
				errcode.Quantity("retryAfterSeconds", 7)),
			http.StatusTooManyRequests, errcode.SinkTestRateLimited,
			map[string]any{"retryAfterSeconds": float64(7)},
			"wait",
		},
		{
			"message where the code goes",
			problem.New(http.StatusConflict, "the destination changed", errcode.SinkConflict),
			http.StatusInternalServerError, errcode.ServerInternal, nil, "internal error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := answerOf(t, tc.p)
			got := body(t, rec)

			if rec.Code != tc.status || got.ErrorCode != tc.code || got.Message != tc.text {
				t.Errorf("answer = %d %q %q, want %d %q %q",
					rec.Code, got.ErrorCode, got.Message, tc.status, tc.code, tc.text)
			}

			if len(got.ErrorArgs) != len(tc.args) {
				t.Fatalf("args = %v, want %v", got.ErrorArgs, tc.args)
			}

			for k, want := range tc.args {
				if got.ErrorArgs[k] != want {
					t.Errorf("arg %s = %v, want %v", k, got.ErrorArgs[k], want)
				}
			}
		})
	}
}

// TestUnitsInTheBody checks that the units of a Problem reach the body in
// order, and that a Problem with no units has no units field.
func TestUnitsInTheBody(t *testing.T) {
	t.Parallel()

	with := problem.New(http.StatusConflict, errcode.SinkConflict, "loop")
	with.Units = []problem.Unit{{ID: "ws-a", Tool: "Pulumi", Name: "app"}, {ID: "ws-b", Tool: "OpenTofu", Name: "net"}}

	tests := []struct {
		name string
		p    problem.Problem
		want string
	}{
		{
			"units", with,
			`{"message":"loop","code":409,"errorCode":"sink.conflict","units":[` +
				`{"id":"ws-a","tool":"Pulumi","name":"app"},{"id":"ws-b","tool":"OpenTofu","name":"net"}]}` + "\n",
		},
		{
			"no units", problem.New(http.StatusConflict, errcode.SinkConflict, "loop"),
			`{"message":"loop","code":409,"errorCode":"sink.conflict"}` + "\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := answerOf(t, tc.p)

			if got := rec.Body.String(); got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}

			if !json.Valid(rec.Body.Bytes()) {
				t.Errorf("body is not JSON: %q", rec.Body)
			}
		})
	}
}
