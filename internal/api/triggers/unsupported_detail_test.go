package triggers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// TestFireUnsupportedAnswersTheDetailOnly checks that a manual fire refused as
// unsupported answers its detail sentence, and that neither the wrap chain nor
// the sentinel text reaches the answer.
func TestFireUnsupportedAnswersTheDetailOnly(t *testing.T) {
	t.Parallel()

	const detail = "the unit has no code location; name one in the request"

	r := fullRig(t)
	seed(r)
	r.disp.err = fmt.Errorf("start the plan of trigger trg-seed: start plan of opentofu:qa/ws-1: plan job: %w: %s",
		job.ErrUnsupported, detail)

	code, body, _ := do(t, r, http.MethodPost, basePath+"/trg-seed/fire", "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s, want %d", code, body, http.StatusUnprocessableEntity)
	}

	if !strings.Contains(body, detail) {
		t.Errorf("answer = %s, want the detail %q", body, detail)
	}

	for _, banned := range []string{"plan job", "start plan of", job.ErrUnsupported.Error()} {
		if strings.Contains(body, banned) {
			t.Errorf("answer = %s, must not repeat %q", body, banned)
		}
	}
}
