package stack_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestAppendJournalRefusesAnUpdateWithoutJournaling checks that a batch for an
// update that saves checkpoints is refused with ErrNotJournaled. The refusal is
// an ErrWrongUpdateState, and its text is the one the Pulumi CLI shows.
func TestAppendJournalRefusesAnUpdateWithoutJournaling(t *testing.T) {
	t.Parallel()

	f := newUpdatesWith(t, newClock(), nil, nil)
	s := f.started(t, stack.KindUpdate)

	err := f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, []byte(`{"entries":[]}`))
	if err == nil {
		t.Fatal("batch to a version 0 update stored, want ErrNotJournaled")
	}

	if !errors.Is(err, stack.ErrNotJournaled) {
		t.Fatalf("batch to a version 0 update: err = %v, want ErrNotJournaled", err)
	}

	if !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Errorf("refusal is not an ErrWrongUpdateState: %v", err)
	}

	const want = "This update does not use journaling. Send checkpoints."
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// TestAppendJournalNeedsALeaseBeforeTheVersionCheck checks that a batch with a
// token that never held the lease is refused as a lease error, before the
// journal version is read.
func TestAppendJournalNeedsALeaseBeforeTheVersionCheck(t *testing.T) {
	t.Parallel()

	f := newUpdatesWith(t, newClock(), nil, nil)
	s := f.started(t, stack.KindUpdate)

	foreign, err := token.New()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}

	err = f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, foreign, []byte(`{"entries":[]}`))
	if !errors.Is(err, stack.ErrLeaseInvalid) {
		t.Fatalf("foreign token: err = %v, want ErrLeaseInvalid", err)
	}
}

// TestAppendJournalNeedsAWriterForAJournaledUpdate checks that an update with
// journal version 1 and no journal writer is refused with ErrMissingDependency,
// and that nothing is stored.
func TestAppendJournalNeedsAWriterForAJournaledUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdatesWith(t, newClock(), nil, nil)

	u, err := f.svc.Create(
		t.Context(),
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s, err := f.svc.StartWithJournalVersion(t.Context(), f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start with journal version 1: %v", err)
	}

	err = f.svc.AppendJournal(t.Context(), f.ref, s.Update.ID, s.Token, []byte(`{"entries":[]}`))
	if !errors.Is(err, stack.ErrMissingDependency) {
		t.Fatalf("journaled update without a writer: err = %v, want ErrMissingDependency", err)
	}
}
