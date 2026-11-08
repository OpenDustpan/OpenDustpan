package blobguard_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
)

// fakeLeases answers a Leases with functions.
type fakeLeases struct {
	take    func(ctx context.Context, holder string, ttl time.Duration) (blobguard.Work, bool, error)
	release func(ctx context.Context, holder string) error
}

func (f fakeLeases) TakeLease(
	ctx context.Context,
	holder string,
	ttl time.Duration,
) (blobguard.Work, bool, error) {
	return f.take(ctx, holder, ttl)
}

func (f fakeLeases) ReleaseLease(ctx context.Context, holder string) error {
	return f.release(ctx, holder)
}

func (f fakeLeases) LiveLeases(context.Context) (int, error) {
	return 0, nil
}

// leaseAnswer returns a fakeLeases that takes a lease with a fixed answer and
// releases it without an error.
func leaseAnswer(work blobguard.Work, ok bool, err error) fakeLeases {
	return fakeLeases{
		take: func(context.Context, string, time.Duration) (blobguard.Work, bool, error) {
			return work, ok, err
		},
		release: func(context.Context, string) error { return nil },
	}
}

var errWrite = errors.New("write failed")

// errOther is an error that names no reason, for the test of ReasonOf.
var errOther = errors.New("another failure")

// leaseStart returns the time that the clock of the cases answers.
func leaseStart() time.Time {
	return time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
}

// readClock returns the fixture start time on its first read, and that time
// plus step on each read after it. It counts its reads, so a test sees when
// the clock was read.
type readClock struct {
	step  time.Duration
	reads int
}

// Now returns the time of the next read.
func (c *readClock) Now() time.Time {
	out := leaseStart().Add(time.Duration(c.reads) * c.step)
	c.reads++

	return out
}

// holdLeaseCase is one case of TestHoldLease: the answer of the fake Leases,
// the holder of the call, the movement of the clock between two reads, and
// the result that the call must give.
type holdLeaseCase struct {
	name      string
	holder    string
	work      blobguard.Work
	ok        bool
	err       error
	want      error
	noLeases  bool
	noClock   bool
	step      time.Duration
	wantEnded bool
	wantReads int
}

func TestHoldLease(t *testing.T) {
	t.Parallel()

	tests := []holdLeaseCase{
		{
			name:      "nothing runs",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: "", ID: ""},
			ok:        false,
			err:       nil,
			want:      nil,
			noLeases:  false,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 2,
		},
		{
			name:      "the clock moves by half the lease time",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: "", ID: ""},
			ok:        false,
			err:       nil,
			want:      nil,
			noLeases:  false,
			noClock:   false,
			step:      blobguard.LeaseTTL / 2,
			wantEnded: true,
			wantReads: 2,
		},
		{
			name:      "the clock moves by less than half the lease time",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: "", ID: ""},
			ok:        false,
			err:       nil,
			want:      nil,
			noLeases:  false,
			noClock:   false,
			step:      blobguard.LeaseTTL / 4,
			wantEnded: false,
			wantReads: 2,
		},
		{
			name:      "a backup runs",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"},
			ok:        true,
			err:       nil,
			want:      blobguard.ErrWait,
			noLeases:  false,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 1,
		},
		{
			name:      "a restore runs",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"},
			ok:        true,
			err:       nil,
			want:      blobguard.ErrWait,
			noLeases:  false,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 1,
		},
		{
			name:      "a failed write",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: "", ID: ""},
			ok:        false,
			err:       errWrite,
			want:      errWrite,
			noLeases:  false,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 1,
		},
		{
			name:      "a failed write hides an answer",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"},
			ok:        true,
			err:       errWrite,
			want:      errWrite,
			noLeases:  false,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 1,
		},
		{
			name:      "an unknown kind",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: "other", ID: "x-1"},
			ok:        true,
			err:       nil,
			want:      blobguard.ErrUnknown,
			noLeases:  false,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 1,
		},
		{
			name:      "an empty ID",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: blobguard.Backup, ID: ""},
			ok:        true,
			err:       nil,
			want:      blobguard.ErrUnknown,
			noLeases:  false,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 1,
		},
		{
			name:      "an empty holder",
			holder:    "",
			work:      blobguard.Work{Kind: "", ID: ""},
			ok:        false,
			err:       nil,
			want:      blobguard.ErrNoHolder,
			noLeases:  false,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 0,
		},
		{
			name:      "no guard",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: "", ID: ""},
			ok:        false,
			err:       nil,
			want:      blobguard.ErrNoWatch,
			noLeases:  true,
			noClock:   false,
			step:      0,
			wantEnded: false,
			wantReads: 0,
		},
		{
			name:      "no clock",
			holder:    "dlt-1",
			work:      blobguard.Work{Kind: "", ID: ""},
			ok:        false,
			err:       nil,
			want:      blobguard.ErrNoClock,
			noLeases:  false,
			noClock:   true,
			step:      0,
			wantEnded: false,
			wantReads: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkHoldLease(t, tc)
		})
	}
}

// holdLeaseCalls records what a fake Leases was asked to do.
type holdLeaseCalls struct {
	took     bool
	released string
}

// leaseFake returns a fake Leases that answers one case of TestHoldLease and
// records its calls in calls.
func leaseFake(t *testing.T, tc holdLeaseCase, calls *holdLeaseCalls) fakeLeases {
	t.Helper()

	return fakeLeases{
		take: func(_ context.Context, holder string, ttl time.Duration) (blobguard.Work, bool, error) {
			calls.took = true

			if holder != tc.holder || ttl != blobguard.LeaseTTL {
				t.Errorf("TakeLease(%q, %s), want (%q, %s)", holder, ttl, tc.holder, blobguard.LeaseTTL)
			}

			return tc.work, tc.ok, tc.err
		},
		release: func(_ context.Context, holder string) error {
			calls.released = holder

			return nil
		},
	}
}

// checkHoldLease runs one case of TestHoldLease against a fake Leases that
// answers the case and records its calls.
func checkHoldLease(t *testing.T, tc holdLeaseCase) {
	t.Helper()

	calls := &holdLeaseCalls{took: false, released: ""}

	var leases blobguard.Leases

	if !tc.noLeases {
		leases = leaseFake(t, tc, calls)
	}

	clk := &readClock{step: tc.step, reads: 0}

	var now func() time.Time
	if !tc.noClock {
		now = clk.Now
	}

	leaseCtx, release, err := blobguard.HoldLease(t.Context(), leases, tc.holder, now)
	if !errors.Is(err, tc.want) {
		t.Fatalf("HoldLease err = %v, want %v", err, tc.want)
	}

	if clk.reads != tc.wantReads {
		t.Errorf("clock reads = %d, want %d", clk.reads, tc.wantReads)
	}

	if wantTake := !tc.noLeases && !tc.noClock && tc.holder != ""; calls.took != wantTake {
		t.Errorf("TakeLease called = %v, want %v", calls.took, wantTake)
	}

	wantWait := errors.Is(tc.want, blobguard.ErrWait)

	if _, isWait := errors.AsType[*blobguard.WaitError](err); isWait != wantWait {
		t.Fatalf("HoldLease err = %v, want a WaitError: %v", err, wantWait)
	}

	if tc.want != nil {
		if leaseCtx != nil {
			t.Fatal("HoldLease returned a context with an error")
		}

		if release != nil {
			t.Fatal("HoldLease returned a release function with an error")
		}

		return
	}

	checkHoldLeaseGrant(leaseCtx, t, tc, release, calls)
}

// checkHoldLeaseGrant checks the result of a case with a granted lease: the
// context of the delete is live or already ended, as the case says, and the
// release function cancels the context and releases the lease of the holder.
// The fake Leases ignores the context of the release, so the check releases
// under the context of the delete.
func checkHoldLeaseGrant(
	leaseCtx context.Context,
	t *testing.T,
	tc holdLeaseCase,
	release func(context.Context) error,
	calls *holdLeaseCalls,
) {
	t.Helper()

	if leaseCtx == nil {
		t.Fatal("HoldLease returned no context and no error")
	}

	if tc.wantEnded {
		checkEndedLease(leaseCtx, t)
	} else {
		checkLiveLease(leaseCtx, t)
	}

	if release == nil {
		t.Fatal("HoldLease returned no release function and no error")
	}

	if err := release(leaseCtx); err != nil {
		t.Fatalf("release the blob delete lease: %v", err)
	}

	if calls.released != tc.holder {
		t.Fatalf("ReleaseLease(%q), want %q", calls.released, tc.holder)
	}

	if !tc.wantEnded && !errors.Is(leaseCtx.Err(), context.Canceled) {
		t.Fatalf("lease context after the release = %v, want context.Canceled", leaseCtx.Err())
	}
}

// checkEndedLease fails the test unless the context of the delete already
// ended with context.DeadlineExceeded.
func checkEndedLease(leaseCtx context.Context, t *testing.T) {
	t.Helper()

	if !errors.Is(leaseCtx.Err(), context.DeadlineExceeded) {
		t.Fatalf("lease context = %v, want context.DeadlineExceeded", leaseCtx.Err())
	}
}

// checkLiveLease fails the test unless the context of the delete is live and
// has a deadline.
func checkLiveLease(leaseCtx context.Context, t *testing.T) {
	t.Helper()

	if leaseCtx.Err() != nil {
		t.Fatalf("lease context = %v, want a live context", leaseCtx.Err())
	}

	if _, hasDeadline := leaseCtx.Deadline(); !hasDeadline {
		t.Fatal("lease context has no deadline")
	}
}

// TestHoldLeaseReadsTheStartBeforeTheTake proves the order of the clock
// reads: the read of the start comes before the take, and the read of the
// elapsed time comes after it. A clock that counts its reads makes the order
// visible.
func TestHoldLeaseReadsTheStartBeforeTheTake(t *testing.T) {
	t.Parallel()

	clk := &readClock{step: time.Second, reads: 0}

	readsAtTake := -1

	leases := fakeLeases{
		take: func(context.Context, string, time.Duration) (blobguard.Work, bool, error) {
			readsAtTake = clk.reads

			return blobguard.Work{Kind: "", ID: ""}, false, nil
		},
		release: func(context.Context, string) error { return nil },
	}

	leaseCtx, release, err := blobguard.HoldLease(t.Context(), leases, "dlt-1", clk.Now)
	if err != nil {
		t.Fatalf("HoldLease: %v", err)
	}

	if readsAtTake != 1 {
		t.Fatalf("clock reads at the take = %d, want 1: the start comes before the take", readsAtTake)
	}

	if clk.reads != 2 {
		t.Fatalf("clock reads after the call = %d, want 2: the elapsed time comes after the take", clk.reads)
	}

	if leaseCtx.Err() != nil {
		t.Fatalf("lease context = %v, want a live context", leaseCtx.Err())
	}

	if err := release(t.Context()); err != nil {
		t.Fatalf("release the blob delete lease: %v", err)
	}
}

// TestHoldLeaseReleaseCancelsTheDeleteContext proves that the release
// function cancels the context of the delete.
func TestHoldLeaseReleaseCancelsTheDeleteContext(t *testing.T) {
	t.Parallel()

	leases := leaseAnswer(blobguard.Work{Kind: "", ID: ""}, false, nil)

	// The clock does not move, so the context of the delete stays live until
	// the release.
	leaseCtx, release, err := blobguard.HoldLease(t.Context(), leases, "dlt-7", leaseStart)
	if err != nil {
		t.Fatalf("HoldLease: %v", err)
	}

	if leaseCtx.Err() != nil {
		t.Fatalf("lease context before the release = %v, want an active context", leaseCtx.Err())
	}

	if err := release(t.Context()); err != nil {
		t.Fatalf("release the blob delete lease: %v", err)
	}

	if !errors.Is(leaseCtx.Err(), context.Canceled) {
		t.Fatalf("lease context after the release = %v, want context.Canceled", leaseCtx.Err())
	}
}

// TestHoldLeaseReleasesUnderTheCallersContext proves that the release uses the
// context of its own call, not the context of the delete and not the context
// of the take: a release after the context of the delete ended still reaches
// the lease, and the lease sees the context of the release.
func TestHoldLeaseReleasesUnderTheCallersContext(t *testing.T) {
	t.Parallel()

	var releaseErr error

	leases := fakeLeases{
		take: func(context.Context, string, time.Duration) (blobguard.Work, bool, error) {
			return blobguard.Work{Kind: "", ID: ""}, false, nil
		},
		release: func(ctx context.Context, _ string) error {
			releaseErr = ctx.Err()

			return nil
		},
	}

	// The clock moves by a lease time on each read, so the context of the
	// delete ends at once.
	clk := &readClock{step: blobguard.LeaseTTL, reads: 0}

	leaseCtx, release, err := blobguard.HoldLease(t.Context(), leases, "dlt-7", clk.Now)
	if err != nil {
		t.Fatalf("HoldLease: %v", err)
	}

	if !errors.Is(leaseCtx.Err(), context.DeadlineExceeded) {
		t.Fatalf("lease context = %v, want context.DeadlineExceeded", leaseCtx.Err())
	}

	releaseCtx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := release(releaseCtx); err != nil {
		t.Fatalf("release the blob delete lease: %v", err)
	}

	if !errors.Is(releaseErr, context.Canceled) {
		t.Fatalf("ReleaseLease got %v, want context.Canceled: the release uses the context of its own call", releaseErr)
	}
}

func TestHoldLeaseTakesAndReleases(t *testing.T) {
	t.Parallel()

	var (
		tookHolder string
		tookTTL    time.Duration
		released   string
	)

	leases := fakeLeases{
		take: func(_ context.Context, holder string, ttl time.Duration) (blobguard.Work, bool, error) {
			tookHolder, tookTTL = holder, ttl

			return blobguard.Work{Kind: "", ID: ""}, false, nil
		},
		release: func(_ context.Context, holder string) error {
			released = holder

			return nil
		},
	}

	leaseCtx, release, err := blobguard.HoldLease(t.Context(), leases, "dlt-7", leaseStart)
	if err != nil {
		t.Fatalf("HoldLease: %v", err)
	}

	if tookHolder != "dlt-7" || tookTTL != blobguard.LeaseTTL {
		t.Fatalf("TakeLease(%q, %s), want (%q, %s)", tookHolder, tookTTL, "dlt-7", blobguard.LeaseTTL)
	}

	if leaseCtx == nil {
		t.Fatal("HoldLease returned no context")
	}

	if release == nil {
		t.Fatal("HoldLease returned no release function")
	}

	if err := release(t.Context()); err != nil {
		t.Fatalf("release the blob delete lease: %v", err)
	}

	if released != "dlt-7" {
		t.Fatalf("ReleaseLease(%q), want %q", released, "dlt-7")
	}
}

func TestHoldLeaseReportsAFailedRelease(t *testing.T) {
	t.Parallel()

	leases := fakeLeases{
		take: func(context.Context, string, time.Duration) (blobguard.Work, bool, error) {
			return blobguard.Work{Kind: "", ID: ""}, false, nil
		},
		release: func(context.Context, string) error { return errWrite },
	}

	_, release, err := blobguard.HoldLease(t.Context(), leases, "dlt-7", leaseStart)
	if err != nil {
		t.Fatalf("HoldLease: %v", err)
	}

	if err := release(t.Context()); !errors.Is(err, errWrite) {
		t.Fatalf("release err = %v, want %v", err, errWrite)
	}
}

func TestHoldLeaseNamesTheWork(t *testing.T) {
	t.Parallel()

	work := blobguard.Work{Kind: blobguard.Backup, ID: "bkp-9"}

	leaseCtx, release, err := blobguard.HoldLease(t.Context(), leaseAnswer(work, true, nil), "dlt-1", leaseStart)

	wait, isWait := errors.AsType[*blobguard.WaitError](err)
	if !isWait || wait.Work != work {
		t.Fatalf("HoldLease: want a WaitError for %v, got %v", work, wait)
	}

	if leaseCtx != nil {
		t.Fatal("HoldLease returned a context with a WaitError")
	}

	if release != nil {
		t.Fatal("HoldLease returned a release function with a WaitError")
	}
}

// TestReasonOf maps each refused take to its reason.
func TestReasonOf(t *testing.T) {
	t.Parallel()

	backup := &blobguard.WaitError{Work: blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"}}
	restore := &blobguard.WaitError{Work: blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"}}

	tests := []struct {
		name string
		err  error
		want blobguard.WaitReason
	}{
		{name: "a backup", err: backup, want: blobguard.WaitBackup},
		{name: "a restore", err: restore, want: blobguard.WaitRestore},
		{name: "a wrapped backup", err: fmt.Errorf("hold the lease: %w", backup), want: blobguard.WaitBackup},
		{name: "a wrapped restore", err: fmt.Errorf("hold the lease: %w", restore), want: blobguard.WaitRestore},
		{name: "no guard", err: blobguard.ErrNoWatch, want: blobguard.WaitNoGuard},
		{
			name: "a wrapped no guard",
			err:  fmt.Errorf("hold the lease: %w", blobguard.ErrNoWatch),
			want: blobguard.WaitNoGuard,
		},
		{
			name: "an unknown kind",
			err:  &blobguard.WaitError{Work: blobguard.Work{Kind: "other", ID: "x-1"}},
			want: blobguard.WaitGuardError,
		},
		{name: "no holder", err: blobguard.ErrNoHolder, want: blobguard.WaitGuardError},
		{name: "an unknown answer", err: blobguard.ErrUnknown, want: blobguard.WaitGuardError},
		{name: "a failed take", err: errWrite, want: blobguard.WaitGuardError},
		{name: "an unknown error", err: errOther, want: blobguard.WaitGuardError},
		{name: "no error", err: nil, want: blobguard.WaitGuardError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := blobguard.ReasonOf(tc.err); got != tc.want {
				t.Errorf("ReasonOf(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
