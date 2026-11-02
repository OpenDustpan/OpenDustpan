package localusersvc_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
)

// countingHasher counts the compares that reach the hasher of a sign-in.
type countingHasher struct {
	localuser.PasswordHasher

	compares *atomic.Int64
}

// Reserve takes the reservation of the embedded hasher and counts each
// Verify that the reservation runs.
func (h countingHasher) Reserve(ctx context.Context, stored localuser.PasswordHash) (*localuser.Reservation, error) {
	res, err := h.PasswordHasher.Reserve(ctx, stored)
	if err != nil {
		return nil, err
	}

	verify := func(password string, current localuser.HashParams) (localuser.Verdict, error) {
		h.compares.Add(1)

		return res.Verify(password, current)
	}

	return localuser.NewReservation(verify, res.Release), nil
}

// countingService returns a service over the store of f whose compares are
// counted in compares.
func countingService(t *testing.T, f *fixture, compares *atomic.Int64) *localusersvc.Service {
	t.Helper()

	svc, err := localusersvc.New(localusersvc.Config{
		Log:        nil,
		Store:      f.store,
		Principals: f.principals,
		Hasher:     countingHasher{PasswordHasher: f.hasher, compares: compares},
		Blocklist:  blocklist.Embedded(),
		Audit:      f.store,
		Locks:      f.store,
		Keys:       f.store,
		Settings:   f.settings,
		SSO:        func(context.Context) (bool, error) { return f.sso, nil },
		Reach:      noReach(),
		Subjects:   f.store,
		Kind:       installation.KindProduction,
		Now:        nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// lockedCase is one row of TestLockedNameStillRunsTheCompare.
type lockedCase struct {
	name             string
	makeUser         bool
	lock             bool
	sendName         string
	lastPassword     string
	wantCompares     int64
	wantLockRefusals int
}

// failUntilLocked sends the wrong password the limit of times for name, so
// that the name holds a lock. Each attempt must fail with ErrSignInFailed.
func failUntilLocked(t *testing.T, svc *localusersvc.Service, name string) {
	t.Helper()

	for range authgate.DefaultNameLimit {
		if _, err := svc.SignIn(t.Context(), from(lockedSource), name, otherPassword); !errors.Is(
			err, localuser.ErrSignInFailed,
		) {
			t.Fatalf("wrong password before the lock = %v, want ErrSignInFailed", err)
		}
	}
}

// runLockedCase runs one row: it makes the user and the lock of the row, sends
// the last attempt, and checks its compares and its lock refusals.
func runLockedCase(t *testing.T, tc lockedCase) {
	t.Helper()

	f := newFixture(t)
	if tc.makeUser {
		f.mustCreate(t, "alice", goodPassword)
	}

	var compares atomic.Int64

	svc := countingService(t, f, &compares)

	if tc.lock {
		failUntilLocked(t, svc, tc.sendName)
	}

	before := compares.Load()

	if _, err := svc.SignIn(t.Context(), from(lockedSource), tc.sendName, tc.lastPassword); !errors.Is(
		err, localuser.ErrSignInFailed,
	) {
		t.Fatalf("last attempt = %v, want ErrSignInFailed", err)
	}

	if got := compares.Load() - before; got != tc.wantCompares {
		t.Fatalf("compares in the last attempt = %d, want %d", got, tc.wantCompares)
	}

	if got := f.reasons(t)[localusersvc.ReasonNameLocked]; got != tc.wantLockRefusals {
		t.Fatalf("lock refusals = %d, want %d", got, tc.wantLockRefusals)
	}
}

// TestLockedNameStillRunsTheCompare proves that a sign-in on a locked name
// runs the compare of the password before it refuses, so that the time of the
// answer does not tell that the name is locked.
func TestLockedNameStillRunsTheCompare(t *testing.T) {
	t.Parallel()

	tests := []lockedCase{
		{
			name: "locked known name, good password", makeUser: true, lock: true, sendName: "alice",
			lastPassword: goodPassword, wantCompares: 1, wantLockRefusals: 1,
		},
		{
			name: "locked known name, wrong password", makeUser: true, lock: true, sendName: "alice",
			lastPassword: otherPassword, wantCompares: 1, wantLockRefusals: 1,
		},
		{
			name: "locked unknown name", makeUser: false, lock: true, sendName: "nobody",
			lastPassword: otherPassword, wantCompares: 1, wantLockRefusals: 1,
		},
		{
			name: "name not locked, wrong password", makeUser: true, lock: false, sendName: "alice",
			lastPassword: otherPassword, wantCompares: 1, wantLockRefusals: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runLockedCase(t, tc)
		})
	}
}
