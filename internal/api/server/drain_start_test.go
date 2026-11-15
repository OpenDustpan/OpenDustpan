package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
)

// drainWait is the drain period that the drain tests give the refusing
// source. The answer names it in the Retry-After header.
const drainWait = 5 * time.Second

// drainRefusal is the worker source of the drain tests: every start is
// refused with the drain, as an instance does from the stop signal on.
func drainRefusal() workerSource {
	return workerSource{&admittest.Source{Err: admit.Draining(drainWait)}}
}

// TestDurableStartsRefusedWhileDraining proves that each installation API
// route that starts a durable operation answers 503 with the Retry-After time
// and the code instance.draining while the instance drains, and stores
// nothing.
func TestDurableStartsRefusedWhileDraining(t *testing.T) {
	t.Parallel()

	t.Run("backup", func(t *testing.T) {
		t.Parallel()

		stores := openBackupDeps(t)
		fix := newBackupFixtureWorkers(t, stores, stores.registry, time.Now, drainRefusal())

		checkDrainingAnswer(t, serveDrainRequest(t, fix.handler, http.MethodPost, "/api/server/backups", ""))

		records, err := stores.registry.ListBackups(t.Context())
		if err != nil {
			t.Fatalf("list backups: %v", err)
		}

		if len(records) != 0 {
			t.Fatalf("backups = %d, want 0", len(records))
		}
	})

	t.Run("restore", func(t *testing.T) {
		t.Parallel()

		stores := openBackupDeps(t)
		fix := newRestoreFixtureWorkers(t, stores, stores.target, nil, drainRefusal())

		checkDrainingAnswer(t, serveDrainRequest(t, fix.handler, http.MethodPost, "/api/server/restores",
			`{"backupID":"bkp-missing000000000001"}`))

		rows, err := fix.restores.List(t.Context())
		if err != nil {
			t.Fatalf("list restores: %v", err)
		}

		if len(rows) != 0 {
			t.Fatalf("restores = %d, want 0", len(rows))
		}
	})

	t.Run("audit chain", func(t *testing.T) {
		t.Parallel()

		handler := newAuditChainHandler(t, newTestNamespaces(t), drainRefusal(), newFakeAuditChain(audit.ChainOff))

		// The approve route needs a fresh console session.
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/audit/chain", http.NoBody)
		startSession(t, handler, req, 0)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		checkDrainingAnswer(t, rec)
	})

	t.Run("store check", func(t *testing.T) {
		t.Parallel()

		f := newStoreCheckFixture(t, drainRefusal())

		checkDrainingAnswer(t, serveDrainRequest(t, f.handler, http.MethodPost, "/api/dustpan/v1/store-checks",
			`{"target":"blobs"}`))

		if rows := f.rows(t); len(rows) != 0 {
			t.Fatalf("store checks = %d, want 0", len(rows))
		}
	})
}

// serveDrainRequest sends one installation API request with the test token and
// returns the recorder, so the test reads the Retry-After header.
func serveDrainRequest(t *testing.T, handler testHandler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// checkDrainingAnswer fails unless rec is the 503 answer of a start while the
// instance drains: the code of the catalog and the Retry-After time.
func checkDrainingAnswer(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusServiceUnavailable)
	}

	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want %q, the drain period in whole seconds", got, "5")
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: nil})
}
