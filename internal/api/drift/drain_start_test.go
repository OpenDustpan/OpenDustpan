package drift_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestDurableStartsRefusedWhileDraining proves that while the source of the
// route admits no start because the instance drains, the start of a drift
// check answers 503 with Retry-After and the code instance.draining, and
// starts nothing.
func TestDurableStartsRefusedWhileDraining(t *testing.T) {
	t.Parallel()

	workers := &admittest.Source{Err: admit.Draining(10 * time.Second)}
	r := newRig(t, newAuthorizer(holdsAll()...), func(cfg *driftapi.Config) {
		cfg.Workers = workers
	})

	rec := r.serve(t, startRequest(""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST drift-checks = %d %s, want 503", rec.Code, rec.Body.String())
	}

	if got := r.checker.calls(); got != 0 {
		t.Fatalf("checks = %d, want 0", got)
	}

	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After is empty, want the drain period in seconds")
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: nil})
}
