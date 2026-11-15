package settings_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

var errDiskFull = errors.New("disk is full")

const (
	replayedHeader = "Idempotent-Replayed"
	// connectionBody names a Git host connection, so the write needs the use
	// action.
	connectionBody = `{"codeLocation":{"connection":"` + connRW + `","repository":"acme/infra","ref":"main"}}`
)

// keyedHeader returns the headers of a write with the key and the version.
func keyedHeader(key, version string) map[string]string {
	return map[string]string{"Idempotency-Key": key, "If-Match": version}
}

func TestKeyedPutRunsOnce(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := keyedHeader("put-1", `"0"`)

	first := r.call(t, http.MethodPut, settingsPath, validBody, header)
	wantStatus(t, first, http.StatusOK, "")

	again := r.call(t, http.MethodPut, settingsPath, validBody, header)
	wantStatus(t, again, http.StatusOK, "")

	if again.Body.String() != first.Body.String() || again.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Errorf("the replay differs from the first answer:\n%s\n%s", first.Body.String(), again.Body.String())
	}

	if again.Header().Get(replayedHeader) != "true" || first.Header().Get(replayedHeader) != "" {
		t.Errorf("replay header: first = %q, again = %q, want empty and true", first.Header().Get(replayedHeader),
			again.Header().Get(replayedHeader))
	}

	if n := r.store.written(); n != 1 {
		t.Errorf("the store wrote %d times, want 1", n)
	}

	if a := decode(t, r.call(t, http.MethodGet, settingsPath, "", nil)); a.Version != 1 {
		t.Errorf("version after the replay = %d, want 1", a.Version)
	}
}

// TestKeyedPutAnswersTheDefaultsOfTheWrite checks that the answer of a keyed
// write holds the unit defaults that the store read in the transaction of the
// write, not the defaults of the earlier read, and that the replay of the
// stored answer holds them too.
func TestKeyedPutAnswersTheDefaultsOfTheWrite(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	before := unitsettings.NamespaceDefaults{
		Namespace: "acme", Pool: "rp-" + "0123456789abcdef", Version: 1, UpdatedAt: time.Time{},
	}
	after := unitsettings.NamespaceDefaults{
		Namespace: "acme", Pool: "rp-" + "fedcba9876543210", Version: 2, UpdatedAt: time.Time{},
	}
	r.store.defaults = before
	r.store.defaultsAtPut = &after

	// The body sets no pool, so the effective pool comes from the defaults.
	body := `{"codeLocation":{"repository":"acme/infra","ref":"main"}}`
	header := keyedHeader("defaults-1", `"0"`)

	first := r.call(t, http.MethodPut, settingsPath, body, header)
	wantStatus(t, first, http.StatusOK, "")
	checkEffectivePool(t, "first answer", decode(t, first), after.Pool)

	again := r.call(t, http.MethodPut, settingsPath, body, header)
	wantStatus(t, again, http.StatusOK, "")

	if again.Header().Get(replayedHeader) != "true" {
		t.Errorf("the replay header = %q, want true", again.Header().Get(replayedHeader))
	}

	checkEffectivePool(t, "replay", decode(t, again), after.Pool)
}

// checkEffectivePool fails the test unless the effective pool of the answer
// is pool and comes from the namespace defaults.
func checkEffectivePool(t *testing.T, what string, a answerOf, pool string) {
	t.Helper()

	if a.Effective["pool"].Value != pool || a.Effective["pool"].Origin != "namespace-default" {
		t.Errorf("%s: effective pool = %+v, want %s from the namespace defaults", what, a.Effective["pool"], pool)
	}
}

func TestKeyedPutWithAnotherBodyIsRefused(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := keyedHeader("put-2", `"0"`)

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, header), http.StatusOK, "")
	wantStatus(t, r.call(t, http.MethodPut, settingsPath, `{"pool":""}`, header), http.StatusUnprocessableEntity,
		errcode.RequestIdempotencyKeyReused)

	if n := r.store.written(); n != 1 {
		t.Errorf("the store wrote %d times, want 1", n)
	}
}

func TestKeyedPutChecksTheActionsOfTheBodyAgainBeforeAReplay(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := keyedHeader("put-3", `"0"`)

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, connectionBody, header), http.StatusOK, "")

	r.authz.revoke("namespace:use-git-host-connection acme")

	rec := r.call(t, http.MethodPut, settingsPath, connectionBody, header)
	wantStatus(t, rec, http.StatusForbidden, "")

	if rec.Header().Get(replayedHeader) != "" {
		t.Error("the refused replay sent a stored answer")
	}

	if n := r.store.written(); n != 1 {
		t.Errorf("the refused replay wrote again: %d writes, want 1", n)
	}
}

func TestKeyedPutChecksTheStepsActionAgainBeforeAReplay(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := keyedHeader("put-4", `"0"`)
	body := `{"steps":[{"name":"plan","role":"tool","phase":"plan","args":["tofu","plan"]}]}`

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, body, header), http.StatusOK, "")

	r.authz.revoke("unit:define-steps " + netObject)

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, body, header), http.StatusForbidden, "")
}

func TestKeyedWriteWithAnInvalidKeyIsRefused(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := keyedHeader(strings.Repeat("k", 256), `"0"`)

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, header), http.StatusBadRequest,
		errcode.RequestIdempotencyKeyInvalid)

	if n := r.store.written(); n != 0 {
		t.Errorf("the store wrote %d times, want 0", n)
	}
}

func TestKeyedFailedWriteDoesTheWriteAgainOnRetry(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := keyedHeader("put-5", `"0"`)

	r.store.failWith = errDiskFull
	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, header), http.StatusInternalServerError, "")

	r.store.failWith = nil
	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, header), http.StatusOK, "")

	if n := r.store.written(); n != 1 {
		t.Errorf("the store wrote %d times, want 1", n)
	}
}

func TestKeyedDeleteAndDefaultsRunOnce(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`)), http.StatusOK, "")

	del := keyedHeader("delete-1", `"1"`)
	wantStatus(t, r.call(t, http.MethodDelete, settingsPath, "", del), http.StatusNoContent, "")

	again := r.call(t, http.MethodDelete, settingsPath, "", del)
	wantStatus(t, again, http.StatusNoContent, "")

	if again.Header().Get(replayedHeader) != "true" {
		t.Errorf("replay header of the delete = %q, want true", again.Header().Get(replayedHeader))
	}

	defaults := keyedHeader("defaults-1", `"0"`)
	body := `{"pool":"` + poolJobs + `"}`
	first := r.call(t, http.MethodPut, defaultsPath, body, defaults)
	wantStatus(t, first, http.StatusOK, "")

	second := r.call(t, http.MethodPut, defaultsPath, body, defaults)
	wantStatus(t, second, http.StatusOK, "")

	if second.Body.String() != first.Body.String() || second.Header().Get(replayedHeader) != "true" {
		t.Errorf("the replay of the defaults differs: %q and %q", first.Body.String(), second.Body.String())
	}

	// The three writes (put, delete, defaults) each ran once.
	const writes = 3
	if n := r.store.written(); n != writes {
		t.Errorf("the store wrote %d times, want %d", n, writes)
	}
}

// bulkErrorAnswer is the error of one item of a bulk answer.
type bulkErrorAnswer struct {
	ErrorCode string `json:"errorCode"`
	Code      int    `json:"code"`
}

// bulkItemAnswer is one result of a bulk answer.
type bulkItemAnswer struct {
	Error   *bulkErrorAnswer `json:"error"`
	ID      string           `json:"id"`
	Status  string           `json:"status"`
	Version int64            `json:"version"`
}

// bulkAnswer is the answer of a bulk call.
type bulkAnswer struct {
	Results []bulkItemAnswer `json:"results"`
}

func decodeBulk(t *testing.T, rec *httptest.ResponseRecorder) bulkAnswer {
	t.Helper()

	var a bulkAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("bulk answer is not JSON: %v: %s", err, rec.Body.String())
	}

	return a
}

func bulkBody(settings string) string {
	return `{"units":[{"id":"` + netID + `","version":0},{"id":"` + appID + `","version":0}],"settings":` + settings + `}`
}

// wantItems fails the test unless every item of the answer is ok at version 1.
func wantItems(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	wantStatus(t, rec, http.StatusOK, "")

	for _, item := range decodeBulk(t, rec).Results {
		if item.Status != "ok" || item.Version != 1 {
			t.Fatalf("item %s = %+v, want ok at version 1", item.ID, item)
		}
	}
}

func TestKeyedBulkCallDoesFailedItemsAgain(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := map[string]string{"Idempotency-Key": "bulk-1"}
	body := bulkBody(validBody)

	// Every write fails with a server error: no item stores an answer.
	r.store.failWith = errDiskFull

	for _, item := range decodeBulk(t, r.call(t, http.MethodPost, bulkPath, body, header)).Results {
		if item.Status != "error" || item.Error == nil || item.Error.Code != http.StatusInternalServerError {
			t.Fatalf("item %s = %+v, want a 500 error", item.ID, item)
		}
	}

	// The retry does both items, because neither stored an answer.
	r.store.failWith = nil
	wantItems(t, r.call(t, http.MethodPost, bulkPath, body, header))

	if n := r.store.written(); n != 2 {
		t.Fatalf("the store wrote %d times, want 2", n)
	}
}

func TestKeyedBulkCallAnswersStoredItems(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := map[string]string{"Idempotency-Key": "bulk-4"}
	body := bulkBody(validBody)

	wantItems(t, r.call(t, http.MethodPost, bulkPath, body, header))

	again := r.call(t, http.MethodPost, bulkPath, body, header)
	wantItems(t, again)

	if again.Header().Get(replayedHeader) != "true" {
		t.Errorf("replay header of the bulk call = %q, want true", again.Header().Get(replayedHeader))
	}

	if n := r.store.written(); n != 2 {
		t.Errorf("the replay wrote again: %d writes, want 2", n)
	}
}

func TestKeyedBulkCallWithAnotherBodyIsRefused(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := map[string]string{"Idempotency-Key": "bulk-2"}

	wantStatus(t, r.call(t, http.MethodPost, bulkPath, bulkBody(validBody), header), http.StatusOK, "")
	wantStatus(t, r.call(t, http.MethodPost, bulkPath, bulkBody(`{"pool":""}`), header),
		http.StatusUnprocessableEntity, errcode.RequestIdempotencyKeyReused)
}

func TestKeyedBulkReplayChecksTheActionsOfEachItemAgain(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := map[string]string{"Idempotency-Key": "bulk-3"}
	body := bulkBody(validBody)

	wantStatus(t, r.call(t, http.MethodPost, bulkPath, body, header), http.StatusOK, "")

	r.authz.revoke("unit:manage " + netObject)

	for _, item := range decodeBulk(t, r.call(t, http.MethodPost, bulkPath, body, header)).Results {
		switch item.ID {
		case netID:
			if item.Status != "error" || item.Error == nil || item.Error.Code != http.StatusForbidden {
				t.Errorf("item %s = %+v, want a 403 error and no stored result", item.ID, item)
			}
		default:
			if item.Status != "ok" {
				t.Errorf("item %s = %+v, want ok", item.ID, item)
			}
		}
	}
}

func TestKeyedBulkCallWithAnInvalidKeyIsRefused(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := map[string]string{"Idempotency-Key": strings.Repeat("k", 256)}

	wantStatus(t, r.call(t, http.MethodPost, bulkPath, bulkBody(validBody), header),
		http.StatusBadRequest, errcode.RequestIdempotencyKeyInvalid)

	if n := r.store.written(); n != 0 {
		t.Errorf("the store wrote %d times, want 0", n)
	}
}

func TestKeyOnAServerWithNoReplayRecordsIsRefused(t *testing.T) {
	t.Parallel()

	r := newRigFull(t, suggester{err: nil}, rigOptions{withoutReplayRecords: true}, fullAccess()...)
	header := keyedHeader("none-1", `"0"`)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, settingsPath},
		{http.MethodDelete, settingsPath},
		{http.MethodPut, defaultsPath},
		{http.MethodPost, bulkPath},
	} {
		wantStatus(t, r.call(t, tc.method, tc.path, validBody, header), http.StatusBadRequest,
			errcode.RequestIdempotencyKeyUnsupported)
	}

	if n := r.store.written(); n != 0 {
		t.Errorf("the store wrote %d times, want 0", n)
	}
}

// TestPostToAReadOnlyRouteWithAKeyAnswersMethodNotAllowed shows what the mux
// does with a write to a route that only reads: the pattern names the method,
// so the request never reaches the route wrapper.
func TestPostToAReadOnlyRouteWithAKeyAnswersMethodNotAllowed(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	wantStatus(t, r.call(t, http.MethodPost, suggestionsPath, "", map[string]string{"Idempotency-Key": "x-1"}),
		http.StatusMethodNotAllowed, "")
}
