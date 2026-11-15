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

// drainWait is the drain period that the drain tests give the refusing
// source. The answer names it in the Retry-After header.
const drainWait = 5 * time.Second

// TestDurableStartsRefusedWhileDraining proves that the start of a job, which
// inserts a durable operation, answers 503 with the Retry-After time and the
// code instance.draining while the instance drains, and starts nothing.
func TestDurableStartsRefusedWhileDraining(t *testing.T) {
	t.Parallel()

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
	h := newWorkerHandler(t, newAuthorizer(readsNamespace()...), deps,
		&admittest.Source{Err: admit.Draining(drainWait)})

	rec := post(t, h, testToken, goodBody)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusServiceUnavailable)
	}

	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want %q, the drain period in whole seconds", got, "5")
	}

	if starter.calls() != 0 {
		t.Errorf("starts = %d, want 0", starter.calls())
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: []byte(goodBody)})
}
