package pulumi_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
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

// TestJournalEntriesReservesBodyBytesForSingleEntry checks that the route
// reserves the bytes it reads, not the batch bound: a single-entry batch
// over the batch bound but within the checkpoint bound answers 503 while the
// gate holds room for the batch bound but not for the body. Reserving only
// the batch bound would answer 409 or 413 instead of 503.
func TestJournalEntriesReservesBodyBytesForSingleEntry(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	if err := ctx.Err(); err != nil {
		t.Fatalf("test context: %v", err)
	}

	batchBound := stack.MaxJournalBatchBytes
	checkpointBound := stack.DefaultMaxCheckpointBytes
	bodyLen := batchBound + (1 << 20)
	gateBytes := batchBound + (1 << 19)

	f := newUpdateFixtureWithGate(t, allowAll(), checkpointBound, int64(gateBytes))
	id, lease := mintForLease(t, f, journalPatterns()[0])
	target := journalTarget(id)

	prefix := `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`
	body := prefix + strings.Repeat(" ", bodyLen-len(prefix))
	if len(body) <= batchBound {
		t.Fatal("test batch fits the batch bound, want a batch over it")
	}
	if len(body) <= gateBytes {
		t.Fatal("test batch fits the gate, want a batch over it")
	}
	if len(body) >= checkpointBound {
		t.Fatal("test batch reaches the checkpoint bound, want a batch within it")
	}

	status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, target, lease, body))
	if status != http.StatusServiceUnavailable {
		t.Fatalf(
			"single-entry batch over the gate answers %d, want %d (%s)",
			status, http.StatusServiceUnavailable, resp,
		)
	}
}

// TestJournalEntriesAnswersBatchBoundWithSingleEntryException checks the batch
// bound at the handler: a multi-entry batch over the batch bound answers 413,
// while a single-entry batch over the batch bound but within the checkpoint
// bound is accepted. Removing the single-entry check answers 413 for both.
func TestJournalEntriesAnswersBatchBoundWithSingleEntryException(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	if err := ctx.Err(); err != nil {
		t.Fatalf("test context: %v", err)
	}

	bodyLen := stack.MaxJournalBatchBytes + (1 << 20)

	f := newUpdateFixture(t, allowAll())
	id, lease := mintReplayBound(t, f)
	target := journalTarget(id)

	singlePrefix := `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`
	single := singlePrefix + strings.Repeat(" ", bodyLen-len(singlePrefix))
	if len(single) <= stack.MaxJournalBatchBytes {
		t.Fatal("single-entry batch fits the batch bound, want a batch over it")
	}
	if len(single) >= stack.DefaultMaxCheckpointBytes {
		t.Fatal("single-entry batch reaches the checkpoint bound, want a batch within it")
	}

	multiPrefix := `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":0,"sequenceID":2,"operationID":2,"removeOld":null,"removeNew":null}]}`
	multi := multiPrefix + strings.Repeat(" ", bodyLen-len(multiPrefix))
	if len(multi) <= stack.MaxJournalBatchBytes {
		t.Fatal("multi-entry batch fits the batch bound, want a batch over it")
	}
	if len(multi) >= stack.DefaultMaxCheckpointBytes {
		t.Fatal("multi-entry batch reaches the checkpoint bound, want a batch within it")
	}

	status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, target, lease, multi))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"multi-entry batch over the bound answers %d, want %d (%s)",
			status, http.StatusRequestEntityTooLarge, resp,
		)
	}

	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(resp), &body); err != nil {
		t.Fatalf("decode 413 body: %v (%s)", err, resp)
	}

	batchMessage := regexp.MustCompile(
		`^This journal batch has \d+ bytes, more than the limit of ` +
			strconv.Itoa(stack.MaxJournalBatchBytes) +
			" bytes: run `pulumi up` with fewer resources\\.$",
	)
	if !batchMessage.MatchString(body.Message) {
		t.Fatalf("413 message = %q, want the one-sentence batch message", body.Message)
	}

	status, resp = f.doUpdate(t, leaseRequest(http.MethodPatch, target, lease, single))
	if status != http.StatusOK {
		t.Fatalf(
			"single-entry batch over the bound answers %d, want %d (%s)",
			status, http.StatusOK, resp,
		)
	}
}
