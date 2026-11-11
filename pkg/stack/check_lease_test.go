package stack_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/stack"
)

// checkLeaseRight proves the right token passes after start.
func checkLeaseRight(t *testing.T, f updatesFixture, s stack.Started) {
	t.Helper()

	if checkErr := f.svc.CheckLease(t.Context(), f.ref, s.Update.ID, s.Token); checkErr != nil {
		t.Fatalf("check right token: %v", checkErr)
	}
}

// checkLeaseForeign proves a foreign token fails with ErrLeaseInvalid.
func checkLeaseForeign(t *testing.T, f updatesFixture, s stack.Started) {
	t.Helper()

	foreign, newErr := token.New()
	if newErr != nil {
		t.Fatalf("new token: %v", newErr)
	}

	if checkErr := f.svc.CheckLease(t.Context(), f.ref, s.Update.ID, foreign); !errors.Is(
		checkErr,
		stack.ErrLeaseInvalid,
	) {
		t.Errorf("check foreign token: %v, want ErrLeaseInvalid", checkErr)
	}
}

// checkLeaseNotStarted proves a not-started update fails with ErrLeaseInvalid:
// the zero digest matches no token.
func checkLeaseNotStarted(t *testing.T, f updatesFixture, s stack.Started) {
	t.Helper()

	notStarted, createErr := f.svc.Create(t.Context(), f.ref, stack.Plan{
		Kind:         stack.KindUpdate,
		DryRun:       false,
		CreatedBy:    "",
		RestoredFrom: 0, GitHead: "",
	})
	if createErr != nil {
		t.Fatalf("create: %v", createErr)
	}

	if checkErr := f.svc.CheckLease(t.Context(), f.ref, notStarted.ID, s.Token); !errors.Is(
		checkErr,
		stack.ErrLeaseInvalid,
	) {
		t.Errorf("check not-started update: %v, want ErrLeaseInvalid", checkErr)
	}
}

// checkLeaseUnknown proves an unknown update fails as Get does.
func checkLeaseUnknown(t *testing.T, f updatesFixture, s stack.Started) {
	t.Helper()

	unknown := stack.UpdateID("no-such-update")

	_, getErr := f.svc.Get(t.Context(), f.ref, unknown)
	if getErr == nil {
		t.Fatal("get unknown update succeeded, want an error")

		return
	}

	if !errors.Is(getErr, stack.ErrUpdateNotFound) {
		t.Fatalf("get unknown update: %v, want ErrUpdateNotFound", getErr)
	}

	if checkErr := f.svc.CheckLease(t.Context(), f.ref, unknown, s.Token); !errors.Is(
		checkErr,
		stack.ErrUpdateNotFound,
	) {
		t.Errorf("check unknown update: %v, want ErrUpdateNotFound", checkErr)
	}
}

// checkLeaseExpired proves an expired lease with the right token still passes
// and the check writes nothing.
func checkLeaseExpired(t *testing.T, f updatesFixture, s stack.Started) {
	t.Helper()

	before, getErr := f.svc.Get(t.Context(), f.ref, s.Update.ID)
	if getErr != nil {
		t.Fatalf("get before: %v", getErr)
	}

	f.clock.Advance(6 * time.Minute)

	if checkErr := f.svc.CheckLease(t.Context(), f.ref, s.Update.ID, s.Token); checkErr != nil {
		t.Errorf("check expired lease with right token: %v, want nil", checkErr)
	}

	after, afterErr := f.svc.Get(t.Context(), f.ref, s.Update.ID)
	if afterErr != nil {
		t.Fatalf("get after: %v", afterErr)
	}

	if after.Revision != before.Revision {
		t.Errorf("revision after check = %d, want %d", after.Revision, before.Revision)
	}
}

// TestCheckLease proves the lease check matches the token of the update and
// nothing else: the right token passes after start, a foreign token fails, a
// not-started update fails, an unknown update fails as Get does, an expired
// lease with the right token still passes, and the check writes nothing.
func TestCheckLease(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)

	checkLeaseRight(t, f, s)
	checkLeaseForeign(t, f, s)
	checkLeaseNotStarted(t, f, s)
	checkLeaseUnknown(t, f, s)
	checkLeaseExpired(t, f, s)
}
