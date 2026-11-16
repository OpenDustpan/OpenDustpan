package backup_test

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// staleDeleteReachesTarget runs the clear of a holder that lost its claim with
// the claim check check, and reports whether a file of the target was
// deleted. The error of the clear is not the subject: only the target state is.
func staleDeleteReachesTarget(t *testing.T, check func(context.Context) error) bool {
	t.Helper()

	store := newMemStore()
	name := scopeKey("1", "tables/a.ndjson")
	store.storeObject(t, name, []byte("a"))

	svc := backup.NewFencedServiceForTest(store, check)
	if err := svc.ClearPartialForTest(t.Context(), scopeID, "1"); err != nil && store.has(name) {
		return false
	}

	return !store.has(name)
}

// stalePointerPutReachesTarget runs the pointer write of a holder that lost
// its claim with the claim check check, and reports whether the pointer
// reached the target. The call always succeeds on a healthy store: only the
// target state is the subject. It fails the test on an unexpected store
// error, so a broken store cannot read as a passing guard.
func stalePointerPutReachesTarget(t *testing.T, check func(context.Context) error) bool {
	t.Helper()

	store := newMemStore()

	if err := backup.WriteLivePointerForTest(t.Context(), store, scopeID, "5", check); err != nil {
		t.Fatalf("write the live pointer: %v", err)
	}

	return store.has(livePointerKeyOf(scopeID))
}

// TestFenceMutationIsDetected removes the check of the claim before the target
// call, as a mutation of the attempt writer does (its claimCheck field set to
// a function that returns nil), and proves that the delete test and the
// pointer test tell the two services apart: with the check neither the delete
// nor the pointer put lands, with the check removed both land.
func TestFenceMutationIsDetected(t *testing.T) {
	t.Parallel()

	if staleDeleteReachesTarget(t, claimGone) {
		t.Fatal("a delete of a holder without a claim reached the target")
	}

	if !staleDeleteReachesTarget(t, claimHeld) {
		t.Fatal("with no claim check the delete did not land: the delete test cannot see the mutation")
	}

	if stalePointerPutReachesTarget(t, claimGone) {
		t.Fatal("a pointer put of a holder without a claim reached the target")
	}

	if !stalePointerPutReachesTarget(t, claimHeld) {
		t.Fatal("with no claim check the pointer put did not land: the pointer test cannot see the mutation")
	}
}
