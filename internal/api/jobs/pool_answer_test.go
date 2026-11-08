package jobs_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// poolReader reads pools from a map. A missing ID is runner.ErrNotFound. A set
// err fails every read.
type poolReader struct {
	pools map[string]runner.Pool
	err   error
}

func (p poolReader) Pool(_ context.Context, id string) (runner.Pool, error) {
	if p.err != nil {
		return runner.Pool{}, p.err
	}

	pool, ok := p.pools[id]
	if !ok {
		return runner.Pool{}, runner.ErrNotFound
	}

	return pool, nil
}

// newPoolHandler is the handler of a job store with a reader of pools.
func newPoolHandler(
	t *testing.T, jobs job.Store, starter job.Starter, pools runner.PoolReader, logger *slog.Logger,
) http.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces:     namespaces,
		Jobs:           jobs,
		Units:          nil,
		Artifacts:      nil,
		Logs:           nil,
		Cursors:        nil,
		Starter:        starter,
		Workers:        nil,
		Pools:          pools,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     newAuthorizer(holdsBoth()...),
		Audit:          newTestAudit(),
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         logger,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return apidesctest.CheckStatus(t, handler)
}

// TestJobDocumentNamesThePool proves that the document of a job answers the
// name of the pool in pool, which a start request takes, and the ID of the
// pool in poolID.
func TestJobDocumentNamesThePool(t *testing.T) {
	t.Parallel()

	pool := func(ns namespace.Name) runner.Pool {
		return runner.Pool{
			ID: "pool-1", Namespace: ns, Name: "main", Purpose: runner.PurposeJobs, Lease: 0, Concurrency: 0,
			Isolation: nil, GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: time.Time{},
			UpdatedAt: time.Time{},
		}
	}

	tests := []struct {
		name     string
		stored   string
		reader   runner.PoolReader
		wantCode int
		pool     string
		poolID   string
	}{
		{
			"a pool of the namespace", "pool-1",
			poolReader{pools: map[string]runner.Pool{"pool-1": pool("acme")}, err: nil},
			http.StatusOK, "main", "pool-1",
		},
		{
			"a deleted pool", "pool-1",
			poolReader{pools: nil, err: nil},
			http.StatusOK, "", "pool-1",
		},
		{
			"a pool of another namespace", "pool-1",
			poolReader{pools: map[string]runner.Pool{"pool-1": pool("other")}, err: nil},
			http.StatusOK, "", "pool-1",
		},
		{
			"a failed read of the pool", "pool-1",
			poolReader{pools: nil, err: errStore},
			http.StatusInternalServerError, "", "",
		},
		{
			"a job with no pool", "",
			poolReader{pools: nil, err: errStore},
			http.StatusOK, "", "",
		},
		{"no reader of pools", "pool-1", nil, http.StatusOK, "", "pool-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := runningJob(t, testJob)
			j.Spec.Pool = tt.stored

			handler := newPoolHandler(t, newFakeJobs(j), nil, tt.reader, slog.New(slog.DiscardHandler))
			rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}

			if tt.wantCode != http.StatusOK {
				return
			}

			checkPoolMembers(t, rec.Body.Bytes(), tt.pool, tt.poolID)
		})
	}
}

// checkPoolMembers checks the pool and poolID members of a job document. An
// empty want means the member is not there.
func checkPoolMembers(t *testing.T, body []byte, pool, poolID string) {
	t.Helper()

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for member, want := range map[string]string{"pool": pool, "poolID": poolID} {
		value, present := got[member]
		if present != (want != "") || (present && value != want) {
			t.Errorf("%s = %v (present %t), want %q", member, value, present, want)
		}
	}
}

// TestStartAnswersTheCommittedJobWhenThePoolReadFails proves that a start whose
// job is stored answers 201 with poolID and without pool when the read of the
// pool fails, makes exactly one start, and logs the failure at Warn with the
// job ID. It also proves the other rows of the name: a pool of another
// namespace has no name either.
func TestStartAnswersTheCommittedJobWhenThePoolReadFails(t *testing.T) {
	t.Parallel()

	other := runner.Pool{
		ID: "pool-1", Namespace: "other", Name: "main", Purpose: runner.PurposeJobs, Lease: 0, Concurrency: 0,
		Isolation: nil, GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: time.Time{},
		UpdatedAt: time.Time{},
	}

	tests := []struct {
		name    string
		reader  runner.PoolReader
		wantLog bool
	}{
		{"a failed read of the pool", poolReader{pools: nil, err: errStore}, true},
		{"a pool of another namespace", poolReader{pools: map[string]runner.Pool{"pool-1": other}, err: nil}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			starter.started.Spec.Pool = "pool-1"

			var logged strings.Builder

			logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
			handler := newPoolHandler(t, newFakeJobs(), starter, tt.reader, logger)

			rec := post(t, handler, testToken, goodBody)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d %s, want 201", rec.Code, rec.Body.String())
			}

			checkPoolMembers(t, rec.Body.Bytes(), "", "pool-1")

			if starter.calls() != 1 {
				t.Errorf("starts = %d, want 1", starter.calls())
			}

			got := strings.Contains(logged.String(), "level=WARN") && strings.Contains(logged.String(), string(testJob))
			if got != tt.wantLog {
				t.Errorf("warning with the job ID = %t, want %t; log %q", got, tt.wantLog, logged.String())
			}

			if strings.Contains(rec.Body.String(), "/var/lib/secret/path") {
				t.Errorf("body holds driver text: %s", rec.Body.String())
			}
		})
	}
}
