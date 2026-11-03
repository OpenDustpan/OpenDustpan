package problem_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
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
			"known cause", http.StatusInternalServerError, problem.CauseNetwork,
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
			problem.LogServer(ctx, slog.New(slog.NewTextHandler(&buf, nil)), problem.ServerLog{
				Message: "failed", Status: tc.status, Cause: tc.cause, Attrs: nil,
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
