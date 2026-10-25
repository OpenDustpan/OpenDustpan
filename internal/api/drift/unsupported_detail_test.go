package drift_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// TestStartUnsupportedAnswersTheDetailOnly checks that a drift check refused
// as unsupported answers its detail sentence, and that neither the wrap prefix
// nor the sentinel text reaches the answer.
func TestStartUnsupportedAnswersTheDetailOnly(t *testing.T) {
	t.Parallel()

	const detail = "the unit has no code location; name one in the request"

	r := newRig(t, newAuthorizer(holdsAll()...))
	r.checker.err = fmt.Errorf("start drift check: plan job: %w: %s", job.ErrUnsupported, detail)

	rec := r.serve(t, startRequest(""))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusUnprocessableEntity)
	}

	if !strings.Contains(rec.Body.String(), detail) {
		t.Errorf("answer = %s, want the detail %q", rec.Body.String(), detail)
	}

	for _, banned := range []string{"plan job", "start drift check", job.ErrUnsupported.Error()} {
		if strings.Contains(rec.Body.String(), banned) {
			t.Errorf("answer = %s, must not repeat %q", rec.Body.String(), banned)
		}
	}
}
