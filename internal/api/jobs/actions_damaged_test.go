package jobs_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
