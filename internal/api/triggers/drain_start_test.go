package triggers_test

import (
	"log/slog"
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

// TestDurableStartsRefusedWhileDraining proves that a fire, which starts a job
// and so inserts a durable operation, answers 503 with the Retry-After time
// and the code instance.draining while the instance drains, and fires nothing.
func TestDurableStartsRefusedWhileDraining(t *testing.T) {
	t.Parallel()

	r := newRigWorkers(t, newAuthorizer(holdsAll()...), slog.New(slog.DiscardHandler),
		&admittest.Source{Err: admit.Draining(drainWait)})
	seed(r)

	rec := serve(t, r.h, request{
		method: http.MethodPost, path: basePath + "/trg-seed/fire", token: testToken, body: "", header: nil,
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fire = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusServiceUnavailable)
	}

	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want %q, the drain period in whole seconds", got, "5")
	}

	if got := r.disp.fires(); got != 0 {
		t.Errorf("fires = %d, want 0", got)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: nil})
}
