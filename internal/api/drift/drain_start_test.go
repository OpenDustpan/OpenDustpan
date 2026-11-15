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

// drainWait is the drain period that the drain tests give the refusing
// source. The answer names it in the Retry-After header.
const drainWait = 5 * time.Second

// TestDurableStartsRefusedWhileDraining proves that the start of a drift
// check, which starts a job and so inserts a durable operation, answers 503
// with the Retry-After time and the code instance.draining while the instance
// drains, and starts nothing.
func TestDurableStartsRefusedWhileDraining(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(holdsAll()...),
		func(cfg *driftapi.Config) { cfg.Workers = &admittest.Source{Err: admit.Draining(drainWait)} })

	rec := r.serve(t, startRequest(""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST drift-checks = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusServiceUnavailable)
	}

	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want %q, the drain period in whole seconds", got, "5")
	}

	if got := r.checker.calls(); got != 0 {
		t.Errorf("checks = %d, want 0", got)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: nil})
}
