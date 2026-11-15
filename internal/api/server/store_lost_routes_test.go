package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// lostInstances is an instance store whose database does not answer.
type lostInstances struct{ err error }

func (l lostInstances) RegisterInstance(context.Context, installation.Instance) error { return l.err }

func (l lostInstances) UnregisterInstance(context.Context, string) error { return l.err }

func (l lostInstances) DeleteInstance(context.Context, string) error { return l.err }

func (l lostInstances) ListInstances(context.Context) ([]installation.Instance, error) {
	return nil, l.err
}

// storeLostRoute is a route and the way to make its store fail.
type storeLostRoute struct {
	name   string
	method string
	path   string
	// fail makes the store of the route return err.
	fail func(f *fakeMaintenance, err error)
}

// serve sends the request of the route and returns the answer.
func (r storeLostRoute) serve(t *testing.T, handler testHandler) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), r.method, r.path, strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// TestMaintenanceAndInstanceRoutesAnswer503WhenTheStoreIsLost proves that the
// maintenance routes and the instance routes answer 503 with Retry-After 5,
// not 500, when their store reports a database that does not answer. The store
// error wraps the sentinel of its port and the error of the lost database, as
// both built-in stores return it.
func TestMaintenanceAndInstanceRoutesAnswer503WhenTheStoreIsLost(t *testing.T) {
	t.Parallel()

	lost := fmt.Errorf("%w: connection failed: %w", namespace.ErrUnavailable, io.ErrUnexpectedEOF)
	maintenanceErr := fmt.Errorf("store installation state: %w: %w", maintenance.ErrUnavailable, lost)
	instanceErr := fmt.Errorf("select instance versions: %w: %w", installation.ErrUnavailable, lost)

	maintenanceRoutes := []storeLostRoute{
		{
			"state",
			http.MethodGet,
			"/api/dustpan/v1/maintenance",
			func(f *fakeMaintenance, err error) { f.getErr = err },
		},
		{
			"enter",
			http.MethodPost,
			"/api/dustpan/v1/maintenance/enter",
			func(f *fakeMaintenance, err error) { f.enterErr = err },
		},
		{
			"leave",
			http.MethodPost,
			"/api/dustpan/v1/maintenance/leave",
			func(f *fakeMaintenance, err error) { f.leaveErr = err },
		},
	}

	for _, route := range maintenanceRoutes {
		t.Run("maintenance "+route.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeMaintenance(maintenance.StateNormal)
			route.fail(fake, maintenanceErr)

			checkStoreLost(t, route.serve(t, newMaintenanceHandler(t, fake)))
		})
	}

	instanceRoutes := []storeLostRoute{
		{"list", http.MethodGet, "/api/server/instances", nil},
		{"delete", http.MethodDelete, "/api/server/instances/ins-old", nil},
	}

	for _, route := range instanceRoutes {
		t.Run("instances "+route.name, func(t *testing.T) {
			t.Parallel()

			st := openEpochStore(t)
			handler := newInstanceHandler(t, st, lostInstances{err: instanceErr})

			checkStoreLost(t, route.serve(t, handler))
		})
	}
}

// checkStoreLost checks the answer to a route whose store is lost.
func checkStoreLost(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (%s)", rec.Code, rec.Body.String())
	}

	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After = %q, want 5", got)
	}
}
