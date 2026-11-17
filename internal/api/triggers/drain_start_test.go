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

// TestDurableStartsRefusedWhileDraining proves that while the source of the
// route admits no start because the instance drains, a fire answers 503 with
// Retry-After and the code instance.draining, and fires nothing.
func TestDurableStartsRefusedWhileDraining(t *testing.T) {
	t.Parallel()

	r := newRigWorkers(t, newAuthorizer(holdsAll()...), slog.New(slog.DiscardHandler),
		&admittest.Source{Err: admit.Draining(10 * time.Second)})
	seed(r)

	rec := serve(t, r.h, request{
		method: http.MethodPost, path: basePath + "/trg-seed/fire", token: testToken, body: "", header: nil,
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("fire = %d %s, want 503", rec.Code, rec.Body.String())
	}

	if got := r.disp.fires(); got != 0 {
		t.Fatalf("fires = %d, want 0", got)
	}

	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After is empty, want the drain period in seconds")
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.InstanceDraining, Args: nil, RequestBody: nil})
}
