package jobs_test

import (
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestDurableStartsRefusedWhileDraining proves that while the source of the
// route admits no start because the instance drains, the start of a job
// answers 503 with Retry-After and the code instance.draining, and starts
// nothing.
func TestDurableStartsRefusedWhileDraining(t *testing.T) {
	t.Parallel()

	workers := &admittest.Source{Err: admit.Draining(10 * time.Second)}

	starter := newFakeStarter(t)
	deps := handlerDeps{
		jobs:      newFakeJobs(),
		units:     nil,
		starter:   starter,
		entries:   nil,
		artifacts: nil,
		logs:      nil,
		cursors:   nil,
	}
	h := newWorkerHandler(t, newAuthorizer(readsNamespace()...), deps, workers)

	rec := post(t, h, testToken, goodBody, "Idempotency-Key", "key-drain-1")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %s, want 503", rec.Code, rec.Body.String())
	}

	if starter.calls() != 0 {
		t.Fatalf("starts = %d, want 0", starter.calls())
	}

	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After is empty, want the drain period in seconds")
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: nil})

	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Error("the refused start is marked as a replay")
	}
}
