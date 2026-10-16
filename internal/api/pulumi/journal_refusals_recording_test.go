//go:build journalrecording

package pulumi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// checkConflictBody checks that a refused complete answers 409 with the exact
// error shape: {"code":409,"message":"complete update <id> of <ref>: ..."}.
func checkConflictBody(t *testing.T, ref stack.Ref, id stack.UpdateID, status int, resp string) {
	t.Helper()

	if status != http.StatusConflict {
		t.Fatalf("complete answers %d, want %d (%s)", status, http.StatusConflict, resp)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(resp), &decoded); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, resp)
	}

	want := fmt.Sprintf("complete update %s of %s: %s", id, ref, stack.ErrJournalIncomplete)

	if len(decoded) != 2 || decoded["code"] != float64(http.StatusConflict) || decoded["message"] != want {
		t.Fatalf("error body = %s, want {\"code\":409,\"message\":%q}", resp, want)
	}
}

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
	checkConflictBody(t, f.ref, id, status, resp)

	status, resp = f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusOK {
		t.Fatalf("cancel after refused complete answers %d, want %d (%s)", status, http.StatusOK, resp)
	}
}

// TestCompleteJournalGapAnswersConflict checks that a complete with succeeded
// answers 409 with the unstored-batch message when the stored sequence IDs
// hold a gap, even with no recorded refusal. The update stays in progress.
func TestCompleteJournalGapAnswersConflict(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id, lease := mintJournaled(t, f)
	paths := mintedPaths(id)

	const second = `{"entries":[{"version":1,"kind":0,"sequenceID":2,"operationID":6,` +
		`"removeOld":null,"removeNew":null}]}`

	status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, journalTarget(id), lease, second))
	if status != http.StatusOK {
		t.Fatalf("batch of sequence 2 answers %d, want %d (%s)", status, http.StatusOK, resp)
	}

	status, resp = f.doUpdate(
		t, leaseRequest(http.MethodPost, paths.Complete, lease, `{"status":"succeeded"}`),
	)
	checkConflictBody(t, f.ref, id, status, resp)

	u, err := f.updates.Get(t.Context(), f.ref, id)
	if err != nil {
		t.Fatalf("get update after refused complete: %v", err)
	}

	if u.Status != stack.StatusRunning {
		t.Fatalf("update status = %q, want running", u.Status)
	}
}
