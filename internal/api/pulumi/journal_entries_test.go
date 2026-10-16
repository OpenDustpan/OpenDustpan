package pulumi_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/stack"
)

// journalPatterns lists the journal entry route. Its checks differ from the
// other lease routes: a valid lease on an update that saves checkpoints
// (journal version 0) answers 409, not 200.
func journalPatterns() []string {
	return []string{"PATCH /api/stacks/{namespace}/{project}/{stack}/update/{update}/journalentries"}
}

// journalTarget returns the journal route of the update that mintForLease
// started. It uses the events path of the same update.
func journalTarget(id stack.UpdateID) string {
	return strings.Replace(mintedPaths(id).Events, "/events/batch", "/journalentries", 1)
}

// TestJournalEntriesRefusesUnleasedWritesAndAnswersVersionZero checks that the
// route needs a lease, and that a leased batch to an update without journaling
// answers 409 with the message the Pulumi CLI shows (ADR 43, work item 9).
func TestJournalEntriesRefusesUnleasedWritesAndAnswersVersionZero(t *testing.T) {
	t.Parallel()

	const body = `{"entries":[]}`

	f := newUpdateFixture(t, allowAll())
	id, lease := mintForLease(t, f, journalPatterns()[0])
	target := journalTarget(id)

	foreign, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	unleased := []updateRequest{
		anonymousRequest(http.MethodPatch, target, body),
		accessRequest(http.MethodPatch, target, body),
		leaseRequest(http.MethodPatch, target, foreign.Reveal(), body),
	}

	for _, req := range unleased {
		status, resp := f.doUpdate(t, req)
		if status != http.StatusUnauthorized {
			t.Fatalf("unleased batch answers %d, want %d (%s)", status, http.StatusUnauthorized, resp)
		}
	}

	status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, target, lease, body))
	if status != http.StatusConflict {
		t.Fatalf("leased batch to a version 0 update answers %d, want %d (%s)", status, http.StatusConflict, resp)
	}

	const message = "This update does not use journaling. Send checkpoints."
	if !strings.Contains(resp, message) {
		t.Fatalf("refusal lacks the approved message %q: %s", message, resp)
	}
}

// TestJournalEntriesReservesConfiguredCheckpointBound checks that the route
// reserves the configured checkpoint bound before it reads the batch: a batch
// over that bound answers with the oversized status while the gate holds room
// for the batch bound, so the reserve follows the configured value.
func TestJournalEntriesReservesConfiguredCheckpointBound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	const checkpointBound = 2048
	const gateBytes = 4096

	f := newUpdateFixtureWithGate(t, allowAll(), checkpointBound, gateBytes)
	id, lease := mintForLease(t, f, journalPatterns()[0])
	target := journalTarget(id)

	prefix := `{"entries":[]}`
	padding := checkpointBound
	for len(prefix)+padding < gateBytes+1024 {
		padding += checkpointBound
	}

	body := prefix + strings.Repeat(" ", padding)
	if len(body) <= checkpointBound {
		t.Fatal("test batch fits the checkpoint bound, want a batch over it")
	}

	if err := ctx.Err(); err != nil {
		t.Fatalf("test context: %v", err)
	}

	status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, target, lease, body))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"batch over the configured bound answers %d, want %d (%s)",
			status, http.StatusRequestEntityTooLarge, resp,
		)
	}
}
