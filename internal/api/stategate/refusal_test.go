package stategate_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestApprovalRefusalNamesRecordRead proves that an approval refusal at
// state.write, which a CLI cannot wait for, names the ID of its decision
// record and the read route of the record, and names no command of the
// change order.
func TestApprovalRefusalNamesRecordRead(t *testing.T) {
	t.Parallel()

	const recordID = "gdr-j7k2m4n6p8q0r1s3t5u7"

	// The approval refusal of a gate at state.write: a CLI cannot wait, so
	// the write is refused with the record of the evaluation.
	refused := &gate.RefusalError{
		Err:    gate.ErrDenied,
		Record: recordID,
		Messages: []string{
			"The approval rule 'netops' needs 2 approvals of the unit before a write of state",
		},
	}

	guard := &fakeGuard{err: refused}
	rec := &recorder{}
	w := write(t, gate.OperationLock, principal(identity.CredentialAccessToken, ""))

	out := newAdmitter(t, guard).Admit(t.Context(), w, rec.record)

	if out.Refusal == nil {
		t.Fatal("the write was not refused")
	}

	if got := string(out.Refusal.Record); got != recordID {
		t.Fatalf("record = %q, want %q", got, recordID)
	}

	for _, want := range []string{
		"decision record " + recordID,
		"GET /api/dustpan/v1/namespaces/acme/decision-records/" + recordID,
	} {
		if !strings.Contains(out.Detail, want) {
			t.Errorf("detail = %q, want it to name %q", out.Detail, want)
		}
	}

	// The change order slice ships no command yet: the refusal must name no
	// route or command of it.
	for _, forbidden := range []string{"change-order", "change-orders"} {
		if strings.Contains(out.Detail, forbidden) {
			t.Errorf("detail = %q, want it to name no command of the change order (%q)", out.Detail, forbidden)
		}
	}
}
