package faultlog_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/requestid"
)

// TestLogServer checks that a cause outside the closed list logs the
// unclassified class, never the value, and that the status is the one given.
func TestLogServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		cause   string
		wantLog []string
		noLog   []string
	}{
		{
			"known cause", http.StatusInternalServerError, faultlog.CauseNetwork,
			[]string{"status=500", "cause=network", "request_id=0123abcd"},
			nil,
		},
		{
			"free text cause", http.StatusInternalServerError, "password=hunter2",
			[]string{"cause=unclassified"},
			[]string{"hunter2"},
		},
		{
			"empty cause", http.StatusBadGateway, "",
			[]string{"status=502", "cause=unclassified"},
			nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			ctx := requestid.WithContext(t.Context(), "0123abcd")
			faultlog.LogServer(ctx, slog.New(slog.NewTextHandler(&buf, nil)), faultlog.ServerLog{
				Message: "failed", Status: tc.status, Cause: tc.cause, Code: "", Op: "",
			})

			for _, want := range tc.wantLog {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("log = %q, want it to hold %q", buf.String(), want)
				}
			}

			for _, no := range tc.noLog {
				if strings.Contains(buf.String(), no) {
					t.Errorf("log = %q, want it to hold no %q", buf.String(), no)
				}
			}
		})
	}
}

// TestLogServerFields checks the fields of a failure log line: the route, the
// op and the code are kept when they have the form of a constant, and are left
// out when they could carry input or a line break. A status of 0 logs no
// status.
func TestLogServerFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		route   string
		entry   faultlog.ServerLog
		wantLog []string
		noLog   []string
	}{
		{
			name:  "constant route, op and code",
			route: "POST /api/stacks/{org}/{stack}",
			entry: faultlog.ServerLog{
				Message: "m",
				Status:  500,
				Cause:   "",
				Code:    "server.internal",
				Op:      "audit.append",
			},
			wantLog: []string{
				`route="POST /api/stacks/{org}/{stack}"`, "op=audit.append", "error_code=server.internal", "status=500",
			},
			noLog: nil,
		},
		{
			name:    "no status",
			route:   "",
			entry:   faultlog.ServerLog{Message: "m", Status: 0, Cause: "", Code: "", Op: ""},
			wantLog: []string{"cause=unclassified"},
			noLog:   []string{"status", "route", "op=", "error_code"},
		},
		{
			name:    "route with CR and LF",
			route:   "GET /x\r\nlevel=ERROR forged",
			entry:   faultlog.ServerLog{Message: "m", Status: 500, Cause: "", Code: "", Op: ""},
			wantLog: []string{"cause=unclassified"},
			noLog:   []string{"route", "forged"},
		},
		{
			name:    "op and code with a space and a quote",
			route:   "",
			entry:   faultlog.ServerLog{Message: "m", Status: 500, Cause: "", Code: "a b", Op: `x"y`},
			wantLog: []string{"cause=unclassified"},
			noLog:   []string{"op=", "error_code"},
		},
		{
			name:    "route of 201 bytes",
			route:   "GET /" + strings.Repeat("a", 200),
			entry:   faultlog.ServerLog{Message: "m", Status: 500, Cause: "", Code: "", Op: ""},
			wantLog: []string{"cause=unclassified"},
			noLog:   []string{"route"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			ctx := routeContext(t, tc.route)
			faultlog.LogServer(ctx, slog.New(slog.NewTextHandler(&buf, nil)), tc.entry)
			checkLogLine(t, buf.String(), tc.wantLog, tc.noLog)
		})
	}
}

// routeContext returns a context that a mux of route made, or the context of
// the test for an empty route.
func routeContext(t *testing.T, route string) context.Context {
	t.Helper()

	if route == "" {
		return t.Context()
	}

	var seen []*http.Request

	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = append(seen, r) })
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	requestid.WithRoute(route, next).ServeHTTP(httptest.NewRecorder(), req)

	if len(seen) != 1 {
		t.Fatalf("handler ran %d times, want 1", len(seen))
	}

	return seen[0].Context()
}

// checkLogLine checks that log is one line that holds each text of want and
// none of the texts of no.
func checkLogLine(t *testing.T, log string, want, no []string) {
	t.Helper()

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
