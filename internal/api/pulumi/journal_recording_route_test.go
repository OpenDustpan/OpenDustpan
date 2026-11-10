//go:build journalrecording

package pulumi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// mintJournaled creates a mutating update and starts it with journal version 1,
// as the recording build answers. It returns the update ID and its lease token.
func mintJournaled(t *testing.T, f updateFixture) (stack.UpdateID, string) {
	t.Helper()

	u, err := f.updates.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.StartWithJournalVersion(context.Background(), f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start with journal version 1: %v", err)
	}

	return u.ID, started.Token.Reveal()
}

// TestJournalEntriesAcceptsAValidBatch checks that a leased, valid batch to a
// journaled update answers 200 once the writer has stored it (ADR 43, decision
// 6.2).
func TestJournalEntriesAcceptsAValidBatch(t *testing.T) {
	t.Parallel()

	const body = `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":5,"removeOld":null,"removeNew":null}]}`

	f := newUpdateFixture(t, allowAll())
	id, lease := mintJournaled(t, f)

	status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, journalTarget(id), lease, body))
	if status != http.StatusOK {
		t.Fatalf("valid batch answers %d, want %d (%s)", status, http.StatusOK, resp)
	}
}

// TestJournalEntriesRefusesAnUnknownEntry checks that a batch with an entry of
// an unknown kind answers 400 and says which entry it refuses (ADR 43, decision
// 5.5).
func TestJournalEntriesRefusesAnUnknownEntry(t *testing.T) {
	t.Parallel()

	const body = `{"entries":[{"version":1,"kind":42,"sequenceID":1,"operationID":5,"removeOld":null,"removeNew":null}]}`

	f := newUpdateFixture(t, allowAll())
	id, lease := mintJournaled(t, f)

	status, resp := f.doUpdate(t, leaseRequest(http.MethodPatch, journalTarget(id), lease, body))
	if status != http.StatusBadRequest {
		t.Fatalf("unknown kind answers %d, want %d (%s)", status, http.StatusBadRequest, resp)
	}

	if !strings.Contains(resp, "entry with sequence ID 1") {
		t.Fatalf("refusal does not name the entry: %s", resp)
	}
}
