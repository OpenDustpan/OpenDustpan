package gate_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
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
