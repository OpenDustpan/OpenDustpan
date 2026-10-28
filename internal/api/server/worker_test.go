package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
)

// workerSource is a WorkerSource whose start check is the embedded Source.
type workerSource struct {
	*admittest.Source
}

func (workerSource) Components() []string { return []string{"api"} }

func (workerSource) WorkerReport(context.Context) (apiserver.WorkerReport, error) {
	return apiserver.WorkerReport{
		LeaseSeconds: 0, SilentSeconds: 0, OldestUnclaimedSeconds: 0, Stuck: false, Operations: nil, Schedules: nil,
	}, nil
}

// TestStartBackupNeedsAWorker proves that the start of a backup, which
// inserts a durable operation, answers 503 with the command that starts a
// worker when no worker runs, and starts no backup.
func TestStartBackupNeedsAWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		workers     func() apiserver.WorkerSource
		wantStatus  int
		wantBackups int
	}{
		{
			"no worker", func() apiserver.WorkerSource { return workerSource{admittest.NoWorker()} },
			http.StatusServiceUnavailable, 0,
		},
		{
			"a worker runs", func() apiserver.WorkerSource { return workerSource{admittest.Worker()} },
			http.StatusAccepted, 1,
		},
		{"no worker check is wired", func() apiserver.WorkerSource { return nil }, http.StatusAccepted, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stores := openBackupDeps(t)
			fix := newBackupFixtureWorkers(t, stores, stores.registry, time.Now, tc.workers())

			status, body := serveBackup(t, fix.handler, http.MethodPost, "/api/server/backups")
			if status != tc.wantStatus {
				t.Fatalf("POST /api/server/backups = %d %s, want %d", status, body, tc.wantStatus)
			}

			records, err := stores.registry.ListBackups(t.Context())
			if err != nil {
				t.Fatalf("list backups: %v", err)
			}

			if len(records) != tc.wantBackups {
				t.Fatalf("backups = %d, want %d", len(records), tc.wantBackups)
			}

			if tc.wantStatus == http.StatusServiceUnavailable {
				checkNoWorkerAnswer(t, body)
			}
		})
	}
}

// checkNoWorkerAnswer fails unless body is the 503 answer of a start with no
// worker: the status, the code of the catalog and the command that helps.
func checkNoWorkerAnswer(t *testing.T, body string) {
	t.Helper()

	var doc struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	if doc.Code != http.StatusServiceUnavailable || !strings.Contains(doc.Message, "--components worker") {
		t.Errorf("body = %s, want 503 with the command that starts a worker", body)
	}

	if doc.ErrorCode != errcode.WorkerAbsent {
		t.Errorf("code = %q, want %q", doc.ErrorCode, errcode.WorkerAbsent)
	}
}

// TestStartRestoreNeedsAWorker proves that the start of a restore, which
// inserts a durable operation, answers 503 worker.absent when no worker runs,
// and stores no restore.
func TestStartRestoreNeedsAWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workers    func() apiserver.WorkerSource
		wantStatus int
		wantRows   int
	}{
		{
			"no worker", func() apiserver.WorkerSource { return workerSource{admittest.NoWorker()} },
			http.StatusServiceUnavailable, 0,
		},
		{
			"a worker runs", func() apiserver.WorkerSource { return workerSource{admittest.Worker()} },
			http.StatusAccepted, 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stores := openBackupDeps(t)
			fix := newRestoreFixtureWorkers(t, stores, stores.target, nil, tc.workers())

			status, body := serveRestore(
				t, fix.handler, http.MethodPost, "/api/server/restores", `{"backupID":"bkp-missing000000000001"}`,
			)
			if status != tc.wantStatus {
				t.Fatalf("POST /api/server/restores = %d %s, want %d", status, body, tc.wantStatus)
			}

			rows, err := fix.restores.List(t.Context())
			if err != nil {
				t.Fatalf("list restores: %v", err)
			}

			if len(rows) != tc.wantRows {
				t.Fatalf("restores = %d, want %d", len(rows), tc.wantRows)
			}

			if tc.wantStatus == http.StatusServiceUnavailable {
				checkNoWorkerAnswer(t, body)
			}
		})
	}
}
