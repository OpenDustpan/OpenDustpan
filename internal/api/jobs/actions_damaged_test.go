package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// damagedCancelRig is a handler whose store holds one damaged row, with a fake
// service that records each cancel.
func damagedCancelRig(
	t *testing.T, ns namespace.Name, units coreinventory.Reader, holds ...string,
) (testHandler, *fakeCanceler) {
	t.Helper()

	svc := &fakeCanceler{Starter: nil, mu: sync.Mutex{}, calls: nil, job: runningJob(t, testJob), err: nil}
	store := newFakeJobs()
	store.damaged = map[job.ID]fakeDamaged{
		testJob: {row: job.DamagedRow{ID: testJob, UnitID: testUnit, Reason: "the body is not JSON"}, ns: ns},
	}

	h := newWorkerHandler(t, newAuthorizer(holds...), handlerDeps{
		jobs: store, units: units, starter: svc, entries: &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil},
		artifacts: nil, logs: nil, cursors: nil,
	}, nil)

	return h, svc
}

func cancelDamaged(t *testing.T, h testHandler, path string) int {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path,
		strings.NewReader(`{"reason":"the row is damaged"}`))
	req.Header.Set("Authorization", "token "+testToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code
}

func TestJobActionRoutesCancelADamagedJob(t *testing.T) {
	t.Parallel()

	holds := append(readsUnits(testUnit), "job:cancel opentofu:acme/net-"+testUnit)
	path := cancelURL(string(testJob))

	tests := []struct {
		name      string
		ns        namespace.Name
		units     coreinventory.Reader
		holds     []string
		path      string
		wantCode  int
		wantCalls int
	}{
		{"an allowed cancel", "acme", unitsOf(testUnit), holds, path, http.StatusOK, 1},
		{
			"no cancel action", "acme", unitsOf(testUnit), readsUnits(testUnit),
			path, http.StatusForbidden, 0,
		},
		{"the row is of another namespace", "other", unitsOf(testUnit), holds, path, http.StatusNotFound, 0},
		{"the unit of the row is unknown", "acme", unitsOf(), holds, path, http.StatusNotFound, 0},
		{"no inventory", "acme", nil, holds, path, http.StatusNotFound, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, svc := damagedCancelRig(t, tc.ns, tc.units, tc.holds...)

			if got := cancelDamaged(t, h, tc.path); got != tc.wantCode {
				t.Errorf("status = %d, want %d", got, tc.wantCode)
			}

			if n := len(svc.called()); n != tc.wantCalls {
				t.Errorf("the service got %d cancels, want %d", n, tc.wantCalls)
			}
		})
	}
}

// TestJobRoutesRefuseADamagedJobExceptCancel proves that only the cancel route
// resolves a damaged job: any other route answers with the error of the store.
func TestJobRoutesRefuseADamagedJobExceptCancel(t *testing.T) {
	t.Parallel()

	holds := append(readsUnits(testUnit), "job:cancel opentofu:acme/net-"+testUnit)
	h, svc := damagedCancelRig(t, "acme", unitsOf(testUnit), holds...)

	rec := serve(t, h, request{
		method: http.MethodGet, path: jobPath("acme", string(testJob)), token: testToken,
	})
	if rec.Code == http.StatusOK {
		t.Fatalf("GET of a damaged job = %d, want an error", rec.Code)
	}

	if n := len(svc.called()); n != 0 {
		t.Errorf("the service got %d cancels, want 0", n)
	}
}

var errInventoryDown = errors.New("inventory: password=hunter2 refused")

// downUnits is an inventory whose unit lookup fails.
type downUnits struct{ fakeUnits }

func (downUnits) GetUnit(context.Context, string) (coreinventory.Entry, error) {
	return coreinventory.Entry{}, errInventoryDown
}

// allowAll is a route authorizer that allows every action on every object,
// the zero object too.
type allowAll struct{ identity.Authorizer }

func (allowAll) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

// TestJobActionRoutesCancelADamagedJobFailsClosed is the hostile table of the
// resolver of a damaged job: each case stops before the service, even when the
// authorizer would allow every object.
func TestJobActionRoutesCancelADamagedJobFailsClosed(t *testing.T) {
	t.Parallel()

	path := cancelURL(string(testJob))
	repaired := fmt.Errorf("%w: the stored job was repaired; send the request again", job.ErrConflict)

	tests := []struct {
		name     string
		units    coreinventory.Reader
		storeErr error
		svcErr   error
		wantCode int
	}{
		{"the store has no such row", unitsOf(testUnit), job.ErrNotFound, nil, http.StatusNotFound},
		{"the store fails on the row", unitsOf(testUnit), errInventoryDown, nil, http.StatusInternalServerError},
		{"the inventory fails", downUnits{unitsOf(testUnit)}, nil, nil, http.StatusInternalServerError},
		{"the row was repaired before the service read it", unitsOf(testUnit), nil, repaired, http.StatusConflict},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := &fakeCanceler{
				Starter: nil,
				mu:      sync.Mutex{},
				calls:   nil,
				job:     runningJob(t, testJob),
				err:     tc.svcErr,
			}
			store := newFakeJobs()
			store.damaged = map[job.ID]fakeDamaged{
				testJob: {
					row: job.DamagedRow{ID: testJob, UnitID: testUnit, Reason: "the body is not JSON"},
					ns:  "acme",
				},
			}
			store.getDamagedErr = tc.storeErr

			h := newWorkerHandler(t, allowAll{nil}, handlerDeps{
				jobs: store, units: tc.units, starter: svc,
				entries:   &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil},
				artifacts: nil, logs: nil, cursors: nil,
			}, nil)

			rec := serveCancel(t, h, path)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}

			if strings.Contains(rec.Body.String(), "hunter2") {
				t.Errorf("the answer leaks the text of the failure: %s", rec.Body.String())
			}

			wantCalls := 0
			if tc.svcErr != nil {
				wantCalls = 1
			}

			if n := len(svc.called()); n != wantCalls {
				t.Errorf("the service got %d cancels, want %d", n, wantCalls)
			}
		})
	}
}

func serveCancel(t *testing.T, h testHandler, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path,
		strings.NewReader(`{"reason":"the row is damaged"}`))
	req.Header.Set("Authorization", "token "+testToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}
