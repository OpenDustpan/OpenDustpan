package pulumi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/requestid"
)

// TestWriteErrorLogs500Route checks that the log line of a 500 holds the route
// pattern, and that the method appears only as part of that pattern. A route
// with CR and LF is left out.
func TestWriteErrorLogs500Route(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		wantLog []string
		noLog   []string
	}{
		{
			name:    "route with a method",
			pattern: "GET /api/stacks/{org}",
			wantLog: []string{`route="GET /api/stacks/{org}"`},
			noLog:   []string{"hunter2"},
		},
		{
			name:    "route with a method and a longer path",
			pattern: "POST /api/stacks/{org}/updates",
			wantLog: []string{`route="POST /api/stacks/{org}/updates"`},
			noLog:   []string{"hunter2"},
		},
		{
			name:    "no route",
			pattern: "",
			wantLog: []string{"status=500", "cause=unclassified"},
			noLog:   []string{"hunter2", "route="},
		},
		{
			name:    "route with CR and LF",
			pattern: "GET /x\r\nlevel=ERROR forged",
			wantLog: []string{"status=500", "cause=unclassified"},
			noLog:   []string{"hunter2", "forged", "route="},
		},
		{
			name:    "route ends with a line feed",
			pattern: "GET /x\n",
			wantLog: []string{"status=500", "cause=unclassified"},
			noLog:   []string{"hunter2", "route="},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			h := new(Handler)
			h.logger = slog.New(slog.NewTextHandler(&buf, nil))

			handler := requestid.WithRoute(tc.pattern, http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					h.writeError(r.Context(), w, errDriver)
				},
			))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			check500Log(t, rec, buf.String(), tc.wantLog, tc.noLog)
		})
	}
}
