package jobs_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// TestStartUnsupportedAnswersTheDetailOnly checks that a wrapped unsupported
// error answers its detail sentence, and that neither the wrap prefix nor the
// sentinel text reaches the answer.
func TestStartUnsupportedAnswersTheDetailOnly(t *testing.T) {
	t.Parallel()

	const detail = "the unit has no code location; name one in the request"

	starter := newFakeStarter(t)
	starter.err = fmt.Errorf("start plan of opentofu:qa/ws-1: plan job: %w: %s", job.ErrUnsupported, detail)
	h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

	rec := post(t, h, testToken, goodBody)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusUnprocessableEntity)
	}

	checkErrorBody(t, rec.Body.String(), "job.unsupported", detail)

	for _, banned := range []string{"plan job", "start plan of", job.ErrUnsupported.Error()} {
		if strings.Contains(rec.Body.String(), banned) {
			t.Errorf("answer = %s, must not repeat %q", rec.Body.String(), banned)
		}
	}
}
