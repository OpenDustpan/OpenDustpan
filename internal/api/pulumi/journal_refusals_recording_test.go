//go:build journalrecording

package pulumi_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestCompleteRefusedJournalAnswersConflict checks that a complete with
// succeeded answers 409 with a message that says a journal batch was not
// stored and names pulumi cancel, while the update holds a recorded refusal.
// Cancel still ends the update.
func TestCompleteRefusedJournalAnswersConflict(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id, lease := mintJournaled(t, f)
	paths := mintedPaths(id)

	const refused = `{"entries":[{"version":1,"kind":42,"sequenceID":1,"operationID":5,` +
		`"removeOld":null,"removeNew":null}]}`

	status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, journalTarget(id), lease, refused))
	if status != http.StatusBadRequest {
		t.Fatalf("refused batch answers %d, want %d (%s)", status, http.StatusBadRequest, resp)
	}

	status, resp = f.doUpdate(
		t, leaseRequest(http.MethodPost, paths.Complete, lease, `{"status":"succeeded"}`),
	)
	if status != http.StatusConflict {
		t.Fatalf("complete with a refusal answers %d, want %d (%s)", status, http.StatusConflict, resp)
	}

	if !strings.Contains(resp, "was not stored") || !strings.Contains(resp, "pulumi cancel") {
		t.Fatalf("complete message lacks the unstored batch or pulumi cancel: %s", resp)
	}

	status, resp = f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusOK {
		t.Fatalf("cancel after refused complete answers %d, want %d (%s)", status, http.StatusOK, resp)
	}
}
