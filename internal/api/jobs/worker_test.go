package jobs_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestStartNeedsAWorker proves that the start of a job, which inserts a
// durable operation, answers 503 with the command that starts a worker when no
// worker runs, and starts nothing.
func TestStartNeedsAWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workers    admit.Source
		wantStatus int
		wantStarts int
	}{
		{"no worker", admittest.NoWorker(), http.StatusServiceUnavailable, 0},
		{"a worker runs", admittest.Worker(), http.StatusCreated, 1},
		{"no worker check is wired", nil, http.StatusCreated, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			deps := handlerDeps{jobs: newFakeJobs(), starter: starter, entries: nil}
			h := newWorkerHandler(t, newAuthorizer(readsNamespace()...), deps, tc.workers)

			rec := post(t, h, testToken, goodBody)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.wantStatus)
			}

			if starter.calls() != tc.wantStarts {
				t.Fatalf("starts = %d, want %d", starter.calls(), tc.wantStarts)
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
