package settings_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
)

const (
	// holdTimeout bounds the wait for a write that a test keeps in flight.
	holdTimeout = 10 * time.Second
	// stepsBody sets one custom step, so the write needs the steps action.
	stepsBody = `{"steps":[{"name":"plan","role":"tool","phase":"plan","args":["tofu","plan"]}]}`
)

// callWithHeaders sends an authenticated request that carries every value of
// header, so a test can send one header two times.
func (r *rig) callWithHeaders(
	t *testing.T, method, path, body string, header http.Header,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "token "+testToken)

	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}

	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)

	return rec
}

// TestKeyedBulkReplayChecksTheActionsOfTheBodyAgain shows that a replay of a bulk
// item checks the actions that the body decides, and not only the actions on
// the unit. Without the check, a caller who lost an action reads the stored
// result of the item.
func TestKeyedBulkReplayChecksTheActionsOfTheBodyAgain(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		body      string
		revoke    string
		forbidden map[string]bool
	}{
		{
			name: "connection", body: bulkBody(connectionBody), revoke: "namespace:use-git-host-connection acme",
			forbidden: map[string]bool{netID: true, appID: true},
		},
		{
			name: "steps", body: bulkBody(stepsBody), revoke: "unit:define-steps " + netObject,
			forbidden: map[string]bool{netID: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, fullAccess()...)
			header := map[string]string{"Idempotency-Key": "bulk-body-" + tc.name}

			wantItems(t, r.call(t, http.MethodPost, bulkPath, tc.body, header))

			r.authz.revoke(tc.revoke)

			again := r.call(t, http.MethodPost, bulkPath, tc.body, header)
			wantStatus(t, again, http.StatusOK, "")

			wantForbiddenItems(t, again, tc.forbidden)

			if n := r.store.written(); n != 2 {
				t.Errorf("the store wrote %d times, want 2", n)
			}
		})
	}
}

// wantForbiddenItems fails the test unless the items in forbidden are 403
// errors and every other item is ok.
func wantForbiddenItems(t *testing.T, rec *httptest.ResponseRecorder, forbidden map[string]bool) {
	t.Helper()

	for _, item := range decodeBulk(t, rec).Results {
		if forbidden[item.ID] {
			if item.Status != "error" || item.Error == nil || item.Error.Code != http.StatusForbidden {
				t.Errorf("item %s = %+v, want a 403 error and no stored result", item.ID, item)
			}

			continue
		}

		if item.Status != "ok" {
			t.Errorf("item %s = %+v, want ok", item.ID, item)
		}
	}
}

// TestKeyedBulkCallWhileTheFirstRequestRunsIsInProgress shows the answer to a
// second request with a key whose first request has not ended.
func TestKeyedBulkCallWhileTheFirstRequestRunsIsInProgress(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := map[string]string{"Idempotency-Key": "bulk-busy"}
	body := bulkBody(validBody)

	var enterOnce, releaseOnce sync.Once

	entered, release := make(chan struct{}), make(chan struct{})
	free := func() { releaseOnce.Do(func() { close(release) }) }

	t.Cleanup(free)

	r.store.hold = func() {
		enterOnce.Do(func() { close(entered) })
		<-release
	}

	done := make(chan *httptest.ResponseRecorder, 1)

	go func() { done <- r.call(t, http.MethodPost, bulkPath, body, header) }()

	select {
	case <-entered:
	case <-time.After(holdTimeout):
		t.Fatal("the first request did not reach the store")
	}

	wantStatus(t, r.call(t, http.MethodPost, bulkPath, body, header), http.StatusConflict,
		errcode.RequestIdempotencyKeyInProgress)

	free()

	select {
	case first := <-done:
		wantItems(t, first)
	case <-time.After(holdTimeout):
		t.Fatal("the first request did not end")
	}

	if n := r.store.written(); n != 2 {
		t.Errorf("the store wrote %d times, want 2", n)
	}
}

// TestKeyedWriteWithTwoKeyHeadersIsRefused shows that a request that sends the
// Idempotency-Key header two times is refused on every write route, and that
// nothing is written.
func TestKeyedWriteWithTwoKeyHeadersIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, method, path, body string }{
		{"put settings", http.MethodPut, settingsPath, validBody},
		{"delete settings", http.MethodDelete, settingsPath, ""},
		{"put defaults", http.MethodPut, defaultsPath, `{"pool":"` + poolJobs + `"}`},
		{"bulk", http.MethodPost, bulkPath, bulkBody(validBody)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, fullAccess()...)
			header := http.Header{
				"Idempotency-Key": {"twice-1", "twice-2"},
				"If-Match":        {`"0"`},
			}

			wantStatus(t, r.callWithHeaders(t, tc.method, tc.path, tc.body, header), http.StatusBadRequest,
				errcode.RequestIdempotencyKeyInvalid)

			if n := r.store.written(); n != 0 {
				t.Errorf("the store wrote %d times, want 0", n)
			}
		})
	}
}

// TestKeyedReplayAfterALostActionIsRefused shows that a replay of a delete of
// the settings and of a write of the unit defaults checks the action of the
// route again. The route wrapper does it before the replay, so this test is a
// guard against a change that moves the check behind the replay.
func TestKeyedReplayAfterALostActionIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, method, path, body, version, revoke string
		setup                                     bool
	}{
		{"delete settings", http.MethodDelete, settingsPath, "", `"1"`, "unit:manage " + netObject, true},
		{
			"put defaults", http.MethodPut, defaultsPath, `{"pool":"` + poolJobs + `"}`, `"0"`,
			"namespace:admin acme", false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, fullAccess()...)

			if tc.setup {
				wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`)), http.StatusOK, "")
			}

			header := keyedHeader("lost-"+tc.name, tc.version)

			first := r.call(t, tc.method, tc.path, tc.body, header)
			if first.Code >= http.StatusMultipleChoices {
				t.Fatalf("first request = %d, want a success: %s", first.Code, first.Body.String())
			}

			writes := r.store.written()

			r.authz.revoke(tc.revoke)

			again := r.call(t, tc.method, tc.path, tc.body, header)
			wantStatus(t, again, http.StatusForbidden, "")

			if again.Header().Get(replayedHeader) != "" {
				t.Error("the refused replay sent a stored answer")
			}

			if n := r.store.written(); n != writes {
				t.Errorf("the refused replay wrote again: %d writes, want %d", n, writes)
			}
		})
	}
}

// TestKeyedPutWithAnotherIfMatchIsRefused shows that the fingerprint of a write
// holds the If-Match header, so the same key and body with another version is
// another request.
func TestKeyedPutWithAnotherIfMatchIsRefused(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, keyedHeader("ifm-1", `"0"`)), http.StatusOK, "")
	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, keyedHeader("ifm-1", `"1"`)),
		http.StatusUnprocessableEntity, errcode.RequestIdempotencyKeyReused)

	if n := r.store.written(); n != 1 {
		t.Errorf("the store wrote %d times, want 1", n)
	}
}
