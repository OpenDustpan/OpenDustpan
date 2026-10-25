package triggers_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// TestBrokenFrequencyAnswersAFailure proves that a stored schedule trigger whose
// frequency has no expression answers 500 on the list route, with no text of
// the record, and logs one error that names the trigger. The store refuses such
// a row before the handler reads it, so the row is planted past that layer, as a
// row written outside the store would be. The handler must still fail closed.
func TestBrokenFrequencyAnswersAFailure(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	r := newRigLogged(t, newAuthorizer(holdsAll()...), slog.New(slog.NewTextHandler(&logs, nil)))
	r.store.plant(trigger.Trigger{
		ID: "trg-broken", Unit: unitNet(), Kind: trigger.KindSchedule,
		Frequency: trigger.Frequency{Preset: "", Cron: ""}, Source: noUnit(), Enabled: true,
		LastSaver: ada, LastSaverID: "prn-" + ada,
	})

	code, body, _ := do(t, r, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/ws-1/triggers", "")

	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: %s", code, http.StatusInternalServerError, body)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusInternalServerError, []byte(body),
		errcodetest.Want{Code: errcode.ServerInternal, Args: nil, RequestBody: nil})

	if !strings.Contains(body, `"message":"internal error"`) {
		t.Errorf("message is not the internal error: %s", body)
	}

	if strings.Contains(body, "stored schedule frequency") {
		t.Errorf("body holds the text of the stored record: %s", body)
	}

	logged := logs.String()
	if n := strings.Count(logged, "msg=\"stored frequency has no expression\""); n != 1 {
		t.Errorf("record lines = %d, want 1: %s", n, logged)
	}

	if !strings.Contains(logged, "trigger_id=trg-broken") {
		t.Errorf("log does not name the trigger: %s", logged)
	}

	if strings.Contains(logged, "stored schedule frequency") {
		t.Errorf("log holds the text of the error: %s", logged)
	}
}
