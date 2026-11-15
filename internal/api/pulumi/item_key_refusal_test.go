package pulumi_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// refusedSecretLine is a line that the program printed. No answer and no
// audit entry may hold it.
const refusedSecretLine = "printed-s3cr3t"

// startedBy creates and starts a preview of the fixture with the creator
// subject, as the route records the caller, and returns its ID and lease.
func startedBy(t *testing.T, f updateFixture, subject string) (stack.UpdateID, string) {
	t.Helper()

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindPreview, DryRun: false, CreatedBy: subject, RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return u.ID, started.Token.Reveal()
}

// deniedSecurityEntries returns the security entries with the outcome denied of the
// audit log of the fixture.
func deniedSecurityEntries(t *testing.T, f updateFixture) []audit.Entry {
	t.Helper()

	listing, err := f.audit.List(t.Context(), namespace.Name("acme"), audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}

	var denied []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Kind == audit.KindSecurity && listing.Entries[i].Outcome == audit.OutcomeDenied {
			denied = append(denied, listing.Entries[i])
		}
	}

	return denied
}

// checkItemKeyAnswer fails t unless the answer is 409 with the code
// update.item-key-unavailable and the fixed text, with no part of the batch.
func checkItemKeyAnswer(t *testing.T, status int, answer string) {
	t.Helper()

	if status != http.StatusConflict || !strings.Contains(answer, `"errorCode":"update.item-key-unavailable"`) {
		t.Fatalf("POST events = %d %s, want 409 and update.item-key-unavailable", status, answer)
	}

	if strings.Contains(answer, refusedSecretLine) || !strings.Contains(answer, "Run the update again.") {
		t.Fatalf("answer = %s, want the fixed text with no part of the batch", answer)
	}
}

// TestEventBatchWithShreddedKeyIsRefused proves that an event batch of an
// update whose item key is shredded answers 409 with the code
// update.item-key-unavailable, stores no event, and writes a security entry
// with the outcome denied.
func TestEventBatchWithShreddedKeyIsRefused(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())
	id, lease := startedBy(t, f, "alice")

	item, err := stack.UpdateItem(f.ref.Namespace, "stk-test", id)
	if err != nil {
		t.Fatalf("item: %v", err)
	}

	f.sealer.Shred(item)

	body := `{"events":[{"sequence":0,"timestamp":1,"diagnosticEvent":{"message":"` + refusedSecretLine + `"}}]}`

	status, answer := f.doUpdate(t, leaseRequest(http.MethodPost, mintedPaths(id).Events, lease, body))
	checkItemKeyAnswer(t, status, answer)

	if stored, listErr := f.mem.ListEvents(t.Context(), f.ref, id, -1, 10); listErr != nil || len(stored) != 0 {
		t.Fatalf("stored events = %d, %v; want none", len(stored), listErr)
	}

	denied := deniedSecurityEntries(t, f)
	if len(denied) != 1 {
		t.Fatalf("denied security entries = %d, want 1", len(denied))
	}

	e := denied[0]
	if e.Version != id.String() || e.Subject != "alice" || e.Reason == "" ||
		strings.Contains(e.Reason, refusedSecretLine) {
		t.Fatalf("denied entry = %+v, want one for update %s by alice with a reason", e, id)
	}
}
