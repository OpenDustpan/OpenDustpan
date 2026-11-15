package server_test

import (
	"net/http"
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
		fix := newBackupFixtureWorkers(t, stores, stores.registry, nil, workers())

		status, body := serveBackup(t, fix.handler, http.MethodPost, "/api/dustpan/v1/backups")
		checkDrainingAnswer(t, status, body)
	})

	t.Run("restore", func(t *testing.T) {
		t.Parallel()

		stores := openBackupDeps(t)
		fix := newRestoreFixtureWorkers(t, stores, stores.target, nil, workers())

		status, body := serveRestore(
			t, fix.handler, http.MethodPost, "/api/dustpan/v1/restores", `{"backupID":"bkp-missing000000000001"}`,
		)
		checkDrainingAnswer(t, status, body)
	})

	t.Run("audit chain", func(t *testing.T) {
		t.Parallel()

		namespaces := newTestNamespaces(t)
		handler := newAuditChainHandlerDraining(t, namespaces, workers())

		status, body := approveAuditChain(t, handler)
		checkDrainingAnswer(t, status, body)
	})

	t.Run("store check", func(t *testing.T) {
		t.Parallel()

		f := newStoreCheckFixture(t, workers())

		status, body := f.post(t, `{"target":"blobs"}`)
		checkDrainingAnswer(t, status, body)
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

// checkDrainingAnswer fails unless status and body are the 503 answer of a
// start that draining refused: the code instance.draining.
func checkDrainingAnswer(t *testing.T, status int, body string) {
	t.Helper()

	if status != http.StatusServiceUnavailable {
		t.Fatalf("start while draining = %d %s, want 503", status, body)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, []byte(body),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: nil})
}
