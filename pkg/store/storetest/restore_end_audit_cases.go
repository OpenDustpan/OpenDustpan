package storetest

import (
	"context"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
)

// restoreEndAuditCases are the cases that prove the audit entry of the end of
// a restore says what happened: its outcome and its text, for a restore that
// finishes, one that a cancel stops before the swap, one that a cancel stops
// in the window of the swap, one that a takeover finishes, and one that fails
// after the swap.
func restoreEndAuditCases() []restoreCase {
	return []restoreCase{
		{
			name: "the audit entry of a restore that ends before the swap says what ended it and claims no replaced tables",
			test: testRestoreEndAuditEntries,
		},
	}
}

// endAuditWant is what the audit entry of one restore end must hold.
type endAuditWant struct {
	outcome audit.Outcome
	has     string
	hasNot  string
}

// restoreEntryOf returns the one audit entry in the scope @server whose
// object starts with the prefix of restore id, or fails the test.
func (r *workerRig) restoreEntryOf(t *testing.T, id backup.RestoreID) audit.Entry {
	t.Helper()

	listing, err := r.st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list the audit log: %v", err)
	}

	var found []audit.Entry

	for i := range listing.Entries {
		if strings.HasPrefix(listing.Entries[i].Object, "restore "+id.String()+" of backup ") {
			found = append(found, listing.Entries[i])
		}
	}

	if len(found) != 1 {
		t.Fatalf("audit entries of restore %s = %d, want 1", id, len(found))
	}

	return found[0]
}

func (w endAuditWant) check(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Outcome != w.outcome {
		t.Errorf("audit outcome = %q (%s), want %q", e.Outcome, e.Object, w.outcome)
	}

	if !strings.Contains(e.Object, w.has) {
		t.Errorf("audit object = %q, want it to hold %q", e.Object, w.has)
	}

	if w.hasNot != "" && strings.Contains(e.Object, w.hasNot) {
		t.Errorf("audit object = %q, want it not to hold %q", e.Object, w.hasNot)
	}
}

func testRestoreEndAuditEntries(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	t.Run("a restore that finishes", func(t *testing.T) {
		r := newWorkerRig(t, newStore)
		a := r.service(t, workerService{
			owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil,
		})
		id := r.start(t, a, false, false)
		r.driveToEnd(t, a, id)

		endAuditWant{outcome: audit.OutcomeSuccess, has: "(access tables replaced", hasNot: ""}.check(
			t, r.restoreEntryOf(t, id),
		)
	})

	for _, tc := range []struct {
		name string
		step string
	}{
		{name: "a restore canceled before the swap", step: backup.RestoreStepCopyBlobs},
		{name: "a restore canceled in the window of the swap", step: backup.RestoreStepSwap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, id, _ := cancelBefore(t, newStore, cancelSpec{step: tc.step, checkOnly: false, maintOf: nil})

			endAuditWant{
				outcome: audit.OutcomeFailure, has: "canceled before the swap", hasNot: "replaced",
			}.check(t, r.restoreEntryOf(t, id))
		})
	}

	t.Run("a restore that a takeover finishes", func(t *testing.T) {
		r := newWorkerRig(t, newStore)
		gate := newStepGate(t, r, backup.RestoreStepSwap, gateCrash)
		a := r.service(t, workerService{
			owner: "instance-a", ops: gate, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil,
		})
		b := r.service(t, workerService{
			owner: "instance-b", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil,
		})

		id := r.start(t, a, false, false)
		aDone := runAsync(t, a, gate)
		<-aDone
		waitReached(t, gate, aDone)
		r.driveToEnd(t, b, id)

		endAuditWant{outcome: audit.OutcomeSuccess, has: "(access tables replaced", hasNot: ""}.check(
			t, r.restoreEntryOf(t, id),
		)
	})

	t.Run("a restore that fails after the swap", func(t *testing.T) {
		r := newWorkerRig(t, newStore)
		a := r.service(t, workerService{
			owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0,
			afterApply: func(context.Context) error { return errHookFailed },
		})
		id := r.start(t, a, false, false)
		r.driveToEnd(t, a, id)

		want := endAuditWant{
			outcome: audit.OutcomeFailure, has: "failed after the swap (access tables replaced", hasNot: "",
		}
		want.check(t, r.restoreEntryOf(t, id))
	})
}
