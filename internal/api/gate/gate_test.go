package gate_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	api "opendustpan.org/opendustpan/internal/api/gate"
	"opendustpan.org/opendustpan/pkg/gate"
)

func TestHandlerHidesRefusedAndMissing(t *testing.T) {
	t.Parallel()

	store := gate.NewRecordStore()
	store.Put(gate.Record{ID: "dec_1", Result: gate.ResultAllow})

	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	mux.Handle(api.RoutePattern, api.Handler(log, store, func(*http.Request, gate.Record) bool { return false }))

	for _, id := range []string{"dec_1", "dec_none"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/spike/v1/decisions/"+id, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", id, rr.Code)
		}
	}
}

func TestConfigRoutes(t *testing.T) {
	t.Parallel()

	reg := gate.NewRegistry()
	mux := http.NewServeMux()
	api.Register(mux, slog.New(slog.DiscardHandler), reg, func(_ *http.Request, s gate.Scope) bool {
		return s.Level != gate.LevelInstallation
	})

	do := func(method, path, body string) int {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		return rr.Code
	}

	good := `{"id":"acme/g","scope":{"level":"namespace","name":"acme"},"decisionPoint":"change.planned",` +
		`"condition":"true","outcome":"warn","message":"m","mode":"enforce"}`

	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/spike/v1/gates/acme/g", good, http.StatusNoContent},
		{"/spike/v1/gates/acme/other", good, http.StatusBadRequest},
		{"/spike/v1/gates/acme/g", strings.Replace(good, `"true"`, `"true }; forbid (principal, action, resource) when { true"`, 1), http.StatusUnprocessableEntity},
		{"/spike/v1/gates/acme/g", strings.Replace(good, "namespace", "installation", 1), http.StatusForbidden},
		{"/spike/v1/gates/acme/g", `{"unknown":1}`, http.StatusBadRequest},
		{"/spike/v1/approval-defaults/d", `{"id":"d","scope":{"level":"collection","name":"acme/prod"},"count":2}`, http.StatusNoContent},
		{"/spike/v1/owner-rules/o", `{"id":"o","scope":{"level":"collection","name":"acme/prod"},"paths":["a/**"],"groups":["x"],"count":1}`, http.StatusNoContent},
	} {
		if got := do(http.MethodPut, tc.path, tc.body); got != tc.want {
			t.Errorf("PUT %s: status = %d, want %d", tc.path, got, tc.want)
		}
	}
}
