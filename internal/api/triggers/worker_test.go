package triggers_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestFireNeedsAWorker proves that a fire, which starts a job and so inserts
// a durable operation, answers 503 with the command that starts a worker when
// no worker runs, and fires nothing.
func TestFireNeedsAWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workers    admit.Source
		wantStatus int
		wantFires  int
	}{
		{"no worker", admittest.NoWorker(), http.StatusServiceUnavailable, 0},
		{"a worker runs", admittest.Worker(), http.StatusCreated, 1},
		{"no worker check is wired", nil, http.StatusCreated, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRigWorkers(t, newAuthorizer(holdsAll()...), slog.New(slog.DiscardHandler), tc.workers)
			seed(r)

			rec := serve(t, r.h, request{
				method: http.MethodPost, path: basePath + "/trg-seed/fire", token: testToken, body: "", header: nil,
			})
			if rec.Code != tc.wantStatus {
				t.Fatalf("fire = %d %s, want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}

			if got := r.disp.fires(); got != tc.wantFires {
				t.Fatalf("fires = %d, want %d", got, tc.wantFires)
			}

			if tc.wantStatus != http.StatusServiceUnavailable {
				return
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.wantStatus, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.WorkerAbsent, Args: nil, RequestBody: nil})

			if !strings.Contains(rec.Body.String(), "--components worker") {
				t.Errorf("body = %s, want the command that starts a worker", rec.Body.String())
			}
		})
	}
}
