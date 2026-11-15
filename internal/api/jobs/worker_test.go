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
			deps := handlerDeps{
				jobs:      newFakeJobs(),
				units:     nil,
				starter:   starter,
				entries:   nil,
				artifacts: nil,
				logs:      nil,
				cursors:   nil,
			}
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

// TestReplayNeedsAWorker proves that the worker check runs before the
// Idempotency-Key replay: a start that sends the key of a job that exists
// still answers 503 when no worker runs, and does not replay the first
// answer. The starter sees one request.
func TestReplayNeedsAWorker(t *testing.T) {
	t.Parallel()

	starter := newFakeStarter(t)
	// A non-nil key set makes the starter answer a start with a key that it saw
	// before as the replay of that job.
	starter.keys = map[string]bool{}
	workers := admittest.Worker()
	deps := handlerDeps{
		jobs:      newFakeJobs(),
		units:     nil,
		starter:   starter,
		entries:   nil,
		artifacts: nil,
		logs:      nil,
		cursors:   nil,
	}
	h := newWorkerHandler(t, newAuthorizer(readsNamespace()...), deps, workers)

	tests := []struct {
		name       string
		workerErr  error
		wantStatus int
		wantStarts int
	}{
		{"the first start with a worker", nil, http.StatusCreated, 1},
		{"the replay of its key with no worker", admittest.NoWorker().Err, http.StatusServiceUnavailable, 1},
	}

	// The steps run in order on one handler and one starter: the replay needs
	// the job of the first start, so the cases never run in parallel.
	for _, tc := range tests {
		workers.Err = tc.workerErr

		rec := post(t, h, testToken, goodBody, "Idempotency-Key", "key-1")
		if rec.Code != tc.wantStatus {
			t.Fatalf("%s: status = %d %s, want %d", tc.name, rec.Code, rec.Body.String(), tc.wantStatus)
		}

		if starter.calls() != tc.wantStarts {
			t.Fatalf("%s: starts = %d, want %d", tc.name, starter.calls(), tc.wantStarts)
		}

		if tc.wantStatus != http.StatusServiceUnavailable {
			continue
		}

		if rec.Header().Get("Idempotent-Replayed") != "" {
			t.Errorf("%s: the refused replay is marked as a replay", tc.name)
		}

		errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.wantStatus, rec.Body.Bytes(),
			errcodetest.Want{Code: errcode.WorkerAbsent, Args: nil, RequestBody: nil})
	}
}
