package pulumi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// manualLockHolder answers one manual lock for every unit.
type manualLockHolder struct {
	lock manuallock.Lock
}

func (m manualLockHolder) CoveringFor(context.Context, manuallock.Unit, string) (manuallock.Lock, error) {
	return m.lock, nil
}

func (m manualLockHolder) NamespaceLock(context.Context, namespace.Name) (manuallock.Lock, error) {
	if m.lock.Target.Kind != manuallock.KindNamespace {
		return manuallock.Lock{}, manuallock.ErrNotLocked
	}

	return m.lock, nil
}

// heldCollectionLock is a manual lock of alice on the collection acme/payments.
func heldCollectionLock() manuallock.Lock {
	return manuallock.Lock{
		Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
		Target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
		Holder: "alice",
		Reason: "change window closed",
	}
}

// startUpdate creates an update at createPath and starts it. It returns the
// answer of the start.
func startUpdate(t *testing.T, f updateFixture, createPath string) (int, string) {
	t.Helper()

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, createPath, `{}`))
	if status != http.StatusOK {
		t.Fatalf("create answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var created struct {
		UpdateID string `json:"updateID"`
	}

	if err := json.Unmarshal([]byte(body), &created); err != nil || created.UpdateID == "" {
		t.Fatalf("create answers %q, want an update ID: %v", body, err)
	}

	return f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/update/"+created.UpdateID, `{}`))
}

// checkManualLockBody checks the error body of a refused start: the code of the
// Pulumi Cloud-compatible API error shape, and a message that is the text of
// the lock and names the object, the holder and the reason.
func checkManualLockBody(t *testing.T, body string, lock manuallock.Lock) {
	t.Helper()

	var failure struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &failure); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}

	if failure.Code != http.StatusConflict {
		t.Errorf("code = %d, want %d", failure.Code, http.StatusConflict)
	}

	want := "stack acme/web/prod is in collection acme/payments, which has a manual lock by alice since " +
		lock.Since.UTC().Format(time.RFC3339) + ": change window closed. To remove the lock, the holder sends " +
		"DELETE /api/dustpan/v1/namespaces/acme/collections/payments/lock."
	if failure.Message != want {
		t.Errorf("message = %q, want %q", failure.Message, want)
	}

	if strings.Contains(failure.Message, "unit") {
		t.Errorf("message = %q: the CLI shows the word stack, not unit", failure.Message)
	}

	for _, want := range []string{"alice", "change window closed", "acme/payments"} {
		if !strings.Contains(failure.Message, want) {
			t.Errorf("message = %q, want it to name %q", failure.Message, want)
		}
	}
}

// TestUpdateStartMeetsAManualLock proves the start of a mutating update answers
// 409 in the error shape of the Pulumi Cloud-compatible API, with a message that
// names the object, the holder and the reason of the manual lock, and that the
// start of a preview still works.
func TestUpdateStartMeetsAManualLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		createPath string
		wantStatus int
	}{
		{name: "update", createPath: "/api/stacks/acme/web/prod/update", wantStatus: http.StatusConflict},
		{name: "preview", createPath: "/api/stacks/acme/web/prod/preview", wantStatus: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lock := heldCollectionLock()
			f := newUpdateFixtureWithAuthenticator(t, allowAll(), tokenAuthenticator{}, fixtureOptions{
				manual: manualLockHolder{lock: lock}, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: nil,
			})

			status, body := startUpdate(t, f, tc.createPath)
			if status != tc.wantStatus {
				t.Fatalf("start answers %d, want %d (%s)", status, tc.wantStatus, body)
			}

			if tc.wantStatus == http.StatusConflict {
				checkManualLockBody(t, body, lock)
			}
		})
	}
}
