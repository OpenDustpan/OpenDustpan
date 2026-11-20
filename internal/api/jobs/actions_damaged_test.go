package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// damagedCancelRig is a handler whose store holds one damaged row, with a fake
// service that records each cancel.
func damagedCancelRig(
	t *testing.T, ns namespace.Name, units coreinventory.Reader, holds ...string,
) (testHandler, *fakeCanceler, *recordingAudit) {
	t.Helper()

	svc := &fakeCanceler{Starter: nil, mu: sync.Mutex{}, calls: nil, job: runningJob(t, testJob), err: nil}
	store := newFakeJobs()
	store.damaged = map[job.ID]fakeDamaged{
		testJob: {
			row: job.DamagedRow{ID: testJob, UnitID: testUnit, Reason: "the body is not JSON", State: "", Version: 0},
			ns:  ns,
		},
	}

	entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
	h := newWorkerHandler(t, newAuthorizer(holds...), handlerDeps{
		jobs: store, units: units, starter: svc, entries: entries,
		artifacts: nil, logs: nil, cursors: nil,
	}, nil)

	return h, svc, entries
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
		wantDeny  bool
	}{
		{"an allowed cancel", "acme", unitsOf(testUnit), holds, path, http.StatusOK, 1, false},
		{
			"no cancel action", "acme", unitsOf(testUnit), readsUnits(testUnit),
			path, http.StatusForbidden, 0, true,
		},
		{"the row is of another namespace", "other", unitsOf(testUnit), holds, path, http.StatusNotFound, 0, false},
		{"the unit of the row is unknown", "acme", unitsOf(), holds, path, http.StatusNotFound, 0, false},
		{"no inventory", "acme", nil, holds, path, http.StatusNotFound, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, svc, entries := damagedCancelRig(t, tc.ns, tc.units, tc.holds...)

			if got := cancelDamaged(t, h, tc.path); got != tc.wantCode {
				t.Errorf("status = %d, want %d", got, tc.wantCode)
			}

			if n := len(svc.called()); n != tc.wantCalls {
				t.Errorf("the service got %d cancels, want %d", n, tc.wantCalls)
			}

			// A denied cancel of a damaged job writes one refusal entry that
			// names job:cancel.
			wantDenials := 0
			if tc.wantDeny {
				wantDenials = 1
			}

			if got := cancelDenials(entries); got != wantDenials {
				t.Errorf("refusal entries for job:cancel = %d, want %d", got, wantDenials)
			}
		})
	}
}

// TestJobRoutesRefuseADamagedJobExceptCancel proves that only the cancel route
// serves a damaged job: any other route answers 409 with the code job.damaged
// (the cases of each route are in damaged_read_test.go).
func TestJobRoutesRefuseADamagedJobExceptCancel(t *testing.T) {
	t.Parallel()

	holds := append(readsUnits(testUnit), "job:cancel opentofu:acme/net-"+testUnit)
	h, svc, _ := damagedCancelRig(t, "acme", unitsOf(testUnit), holds...)

	rec := serve(t, h, request{
		method: http.MethodGet, path: jobPath("acme", string(testJob)), token: testToken,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("GET of a damaged job = %d, want %d", rec.Code, http.StatusConflict)
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

// TestJobActionRoutesCancelADamagedJobFailsClosed is the hostile table of the
// resolver of a damaged job: each case stops before the service, even when the
// authorizer would allow every object.
func TestJobActionRoutesCancelADamagedJobFailsClosed(t *testing.T) {
	t.Parallel()

	path := cancelURL(string(testJob))
	repaired := fmt.Errorf("%w: the stored job was repaired; send the request again", job.ErrConflict)
	repairedInStore := fmt.Errorf("%w: password=hunter2 now parses", job.ErrInvalid)

	tests := []struct {
		name     string
		units    coreinventory.Reader
		storeErr error
		svcErr   error
		wantCode int
	}{
		{"the store has no such row", unitsOf(testUnit), job.ErrNotFound, nil, http.StatusNotFound},
		{"the store fails on the row", unitsOf(testUnit), errInventoryDown, nil, http.StatusInternalServerError},
		{"the row was repaired before the store read it", unitsOf(testUnit), repairedInStore, nil, http.StatusConflict},
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
					row: job.DamagedRow{
						ID:      testJob,
						UnitID:  testUnit,
						Reason:  "the body is not JSON",
						State:   "",
						Version: 0,
					},
					ns: "acme",
				},
			}
			store.getDamagedErr = tc.storeErr

			var logBuf syncBuffer

			h := newLoggedWorkerHandler(t, allowAll{}, handlerDeps{
				jobs: store, units: tc.units, starter: svc,
				entries:   &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil},
				artifacts: nil, logs: nil, cursors: nil,
			}, nil, slog.New(slog.NewTextHandler(&logBuf, nil)))

			rec := serveCancel(t, h, path)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}

			noText(t, rec.Body.String(), "the answer")
			noText(t, logBuf.String(), "the log")

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

// cancelDenials counts the entries that refuse job:cancel.
func cancelDenials(a *recordingAudit) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	n := 0

	for i := range a.entries {
		if e := &a.entries[i]; e.Action == string(identity.ActionJobCancel) && e.Outcome == audit.OutcomeDenied {
			n++
		}
	}

	return n
}

// noText fails the test when the log holds the secret text of a failure.
func noText(t *testing.T, got, what string) {
	t.Helper()

	if strings.Contains(got, "hunter2") {
		t.Errorf("%s holds the text of the failure: %s", what, got)
	}
}

// syncBuffer is a log buffer that many goroutines may write.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
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
