package drift_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestStartCheckNeedsAWorker proves that the start of a drift check, which
// starts a job and so inserts a durable operation, answers 503 with the
// command that starts a worker when no worker runs, and starts nothing.
func TestStartCheckNeedsAWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		workers     admit.Source
		wantStatus  int
		wantChecked int
	}{
		{"no worker", admittest.NoWorker(), http.StatusServiceUnavailable, 0},
		{"a worker runs", admittest.Worker(), http.StatusAccepted, 1},
		{"no worker check is wired", nil, http.StatusAccepted, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer(holdsAll()...), func(cfg *driftapi.Config) { cfg.Workers = tc.workers })

			rec := r.serve(t, startRequest(""))
			if rec.Code != tc.wantStatus {
				t.Fatalf("POST drift-checks = %d %s, want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}

			if got := r.checker.calls(); got != tc.wantChecked {
				t.Fatalf("checks = %d, want %d", got, tc.wantChecked)
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
