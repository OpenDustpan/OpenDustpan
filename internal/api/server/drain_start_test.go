package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestDurableStartsRefusedWhileDraining proves that while the source of the
// route admits no start because the instance drains, each durable start
// answers 503 with Retry-After and the code instance.draining: the backup
// start, the restore start, the audit chain start and the store check start.
func TestDurableStartsRefusedWhileDraining(t *testing.T) {
	t.Parallel()

	workers := func() apiserver.WorkerSource {
		return workerSource{Source: &admittest.Source{Err: admit.Draining(10 * time.Second)}}
	}

	t.Run("backup", func(t *testing.T) {
		t.Parallel()

		stores := openBackupDeps(t)
		fix := newBackupFixtureWorkers(
			t, stores, stores.registry, nil, backupFixtureOptions{workers: workers(), authz: nil},
		)

		rec := serveRecorded(t, fix.handler, http.MethodPost, "/api/dustpan/v1/backups", "")
		checkDrainingAnswer(t, rec)
	})

	t.Run("restore", func(t *testing.T) {
		t.Parallel()

		stores := openBackupDeps(t)
		fix := newRestoreFixtureWorkers(
			t, stores, stores.target, nil, restoreFixtureOptions{workers: workers(), authz: nil},
		)

		rec := serveRecorded(
			t, fix.handler, http.MethodPost, "/api/dustpan/v1/restores", `{"backupID":"bkp-missing000000000001"}`,
		)
		checkDrainingAnswer(t, rec)
	})

	t.Run("audit chain", func(t *testing.T) {
		t.Parallel()

		namespaces := newTestNamespaces(t)
		handler := newAuditChainHandlerDraining(t, namespaces, workers())

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/audit/chain", http.NoBody)
		startSession(t, handler, req, 0)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		checkDrainingAnswer(t, rec)
	})

	t.Run("store check", func(t *testing.T) {
		t.Parallel()

		f := newStoreCheckFixture(t, workers())

		rec := serveRecorded(t, f.handler, http.MethodPost, "/api/dustpan/v1/store-checks", `{"target":"blobs"}`)
		checkDrainingAnswer(t, rec)
	})
}

// newAuditChainHandlerDraining builds a Handler with chain, namespaces and a
// worker source that refuses each start because the instance drains. It is
// the audit chain handler of the drain test: the approve call carries a
// fresh console session and a waiting change.
func newAuditChainHandlerDraining(
	t *testing.T, namespaces *namespace.Service, workers apiserver.WorkerSource,
) testHandler {
	t.Helper()

	return newAuditChainHandlerWorkers(t, namespaces, newFakeAuditChain(audit.ChainOff), workers)
}

// serveRecorded sends one request and returns the recorded answer, with its
// headers. It sends the bearer token of the installation API.
func serveRecorded(
	t *testing.T, handler testHandler, method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// checkDrainingAnswer fails unless rec is the 503 answer of a start that
// draining refused: the code instance.draining and the Retry-After header of
// the drain period of 10 seconds.
func checkDrainingAnswer(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("start while draining = %d %s, want 503", rec.Code, body)
	}

	if got := rec.Header().Get("Retry-After"); got != "10" {
		t.Errorf("Retry-After = %q, want %q", got, "10")
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, []byte(body),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: nil})
}
