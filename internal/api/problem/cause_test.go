package problem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
)

var (
	errSecretNetwork = errors.New("password=hunter2")
	errHostileText   = errors.New("cause=network\r\nlevel=ERROR forged=1")
)

func TestCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "deadline wrapped",
			err:  fmt.Errorf("store: %w", context.DeadlineExceeded),
			want: problem.CauseDeadlineExceeded,
		},
		{
			name: "canceled",
			err:  fmt.Errorf("store: %w", context.Canceled),
			want: problem.CauseCanceled,
		},
		{
			name: "unexpected EOF",
			err:  io.ErrUnexpectedEOF,
			want: problem.CauseUnexpectedEOF,
		},
		{
			name: "EOF",
			err:  io.EOF,
			want: problem.CauseUnexpectedEOF,
		},
		{
			name: "EOF wrapped",
			err:  fmt.Errorf("read: %w", io.EOF),
			want: problem.CauseUnexpectedEOF,
		},
		{
			name: "missing file",
			err:  fmt.Errorf("open: %w", fs.ErrNotExist),
			want: problem.CauseFileMissing,
		},
		{
			name: "permission",
			err:  fmt.Errorf("open: %w", fs.ErrPermission),
			want: problem.CauseFilePermission,
		},
		{
			name: "network",
			err: &net.OpError{
				Op: "dial", Net: "tcp", Source: nil, Addr: nil, Err: errSecretNetwork,
			},
			want: problem.CauseNetwork,
		},
		{
			name: "driver text",
			err:  errDriver,
			want: problem.CauseUnclassified,
		},
		{
			name: "hostile text",
			err:  errHostileText,
			want: problem.CauseUnclassified,
		},
		{
			name: "nil",
			err:  nil,
			want: problem.CauseUnclassified,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := problem.Cause(tc.err); got != tc.want {
				t.Errorf("Cause() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWriteLogsSafeCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		requestID string
		wantLog   []string
		noLog     []string
	}{
		{
			name:      "deadline with secret",
			err:       fmt.Errorf("pq: password=hunter2: %w", context.DeadlineExceeded),
			requestID: "0123abcd",
			wantLog: []string{
				"request failed", "status=500", "cause=deadline_exceeded", "request_id=0123abcd",
			},
			noLog: []string{"hunter2", "password"},
		},
		{
			name:      "unknown error",
			err:       errDriver,
			requestID: "",
			wantLog:   []string{"status=500", "cause=unclassified"},
			noLog:     []string{"hunter2", "request_id"},
		},
		{
			name:      "request ID with CR and LF",
			err:       errDriver,
			requestID: "abcd" + "\r\n" + "level=ERROR forged=1",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"forged", "request_id"},
		},
		{
			name:      "request ID too long",
			err:       errDriver,
			requestID: strings.Repeat("a", 65),
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with lower case text",
			err:       errDriver,
			requestID: "hunter2",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with g",
			err:       errDriver,
			requestID: "g",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with xyz0",
			err:       errDriver,
			requestID: "xyz0",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID one letter above f",
			err:       errDriver,
			requestID: "abcdg",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with mixed case",
			err:       errDriver,
			requestID: "abCDef0",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with a slash",
			err:       errDriver,
			requestID: "/",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with a colon",
			err:       errDriver,
			requestID: ":",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with a backtick",
			err:       errDriver,
			requestID: "`",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with a space",
			err:       errDriver,
			requestID: "ab cd",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
		{
			name:      "request ID with upper case",
			err:       errDriver,
			requestID: "ABCD",
			wantLog:   []string{"cause=unclassified"},
			noLog:     []string{"request_id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			wr := problem.Writer{
				Logger:     slog.New(slog.NewTextHandler(&buf, nil)),
				KeepDenied: nil,
				Classify:   nil,
			}

			ctx := t.Context()
			if tc.requestID != "" {
				ctx = requestid.WithContext(ctx, tc.requestID)
			}

			rec := httptest.NewRecorder()
			wr.Write(ctx, rec, tc.err)

			checkInternalBody(t, rec)
			checkSingleLog(t, buf.String(), tc.wantLog, tc.noLog)
		})
	}
}

// checkInternalBody checks that rec holds the 500 answer of server.internal.
func checkInternalBody(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var body problem.Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if body.ErrorCode != errcode.ServerInternal {
		t.Errorf("errorCode = %q, want %q", body.ErrorCode, errcode.ServerInternal)
	}
}

// checkSingleLog checks that logs is one line that holds each of wantLog and
// none of noLog.
func checkSingleLog(t *testing.T, logs string, wantLog, noLog []string) {
	t.Helper()

	if n := strings.Count(logs, "\n"); n != 1 {
		t.Errorf("log lines = %d, want 1: %q", n, logs)
	}

	for _, want := range wantLog {
		if !strings.Contains(logs, want) {
			t.Errorf("log %q does not contain %q", logs, want)
		}
	}

	for _, no := range noLog {
		if strings.Contains(logs, no) {
			t.Errorf("log %q contains %q", logs, no)
		}
	}
}

func TestWriteProblemLogsSafeCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		p       problem.Problem
		wantLog []string
	}{
		{
			name:    "500 logs the unclassified cause",
			p:       problem.New(http.StatusInternalServerError, errcode.ServerInternal, "internal error"),
			wantLog: []string{"request failed", "status=500", "cause=unclassified"},
		},
		{
			name:    "404 logs nothing",
			p:       problem.New(http.StatusNotFound, errcode.SinkNotFound, "not found"),
			wantLog: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			wr := problem.Writer{
				Logger:     slog.New(slog.NewTextHandler(&buf, nil)),
				KeepDenied: nil,
				Classify:   nil,
			}

			rec := httptest.NewRecorder()
			wr.WriteProblem(t.Context(), rec, tc.p)

			logs := buf.String()

			if len(tc.wantLog) == 0 {
				if logs != "" {
					t.Errorf("log = %q, want empty", logs)
				}

				return
			}

			for _, want := range tc.wantLog {
				if !strings.Contains(logs, want) {
					t.Errorf("log %q does not contain %q", logs, want)
				}
			}
		})
	}
}
