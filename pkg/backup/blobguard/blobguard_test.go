package blobguard_test

import (
	"context"
	"errors"
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

// holdLeaseCase is one case of TestHoldLease: the answer of the fake Leases,
// the holder of the call, and the result that the call must give.
type holdLeaseCase struct {
	name     string
	holder   string
	work     blobguard.Work
	ok       bool
	err      error
	want     error
	noLeases bool
}

func TestHoldLease(t *testing.T) {
	t.Parallel()

	tests := []holdLeaseCase{
		{
			name:     "nothing runs",
			holder:   "dlt-1",
			work:     blobguard.Work{Kind: "", ID: ""},
			ok:       false,
			err:      nil,
			want:     nil,
			noLeases: false,
		},
		{
			name:     "a backup runs",
			holder:   "dlt-1",
			work:     blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"},
			ok:       true,
			err:      nil,
			want:     blobguard.ErrWait,
			noLeases: false,
		},
		{
			name:     "a restore runs",
			holder:   "dlt-1",
			work:     blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"},
			ok:       true,
			err:      nil,
			want:     blobguard.ErrWait,
			noLeases: false,
		},
		{
			name:     "a failed write",
			holder:   "dlt-1",
			work:     blobguard.Work{Kind: "", ID: ""},
			ok:       false,
			err:      errWrite,
			want:     errWrite,
			noLeases: false,
		},
		{
			name:     "a failed write hides an answer",
			holder:   "dlt-1",
			work:     blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"},
			ok:       true,
			err:      errWrite,
			want:     errWrite,
			noLeases: false,
		},
		{
			name:     "an unknown kind",
			holder:   "dlt-1",
			work:     blobguard.Work{Kind: "other", ID: "x-1"},
			ok:       true,
			err:      nil,
			want:     blobguard.ErrUnknown,
			noLeases: false,
		},
		{
			name:     "an empty ID",
			holder:   "dlt-1",
			work:     blobguard.Work{Kind: blobguard.Backup, ID: ""},
			ok:       true,
			err:      nil,
			want:     blobguard.ErrUnknown,
			noLeases: false,
		},
		{
			name:     "an empty holder",
			holder:   "",
			work:     blobguard.Work{Kind: "", ID: ""},
			ok:       false,
			err:      nil,
			want:     blobguard.ErrNoHolder,
			noLeases: false,
		},
		{
			name:     "no guard",
			holder:   "dlt-1",
			work:     blobguard.Work{Kind: "", ID: ""},
			ok:       false,
			err:      nil,
			want:     blobguard.ErrNoWatch,
			noLeases: true,
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

	release, err := blobguard.HoldLease(t.Context(), leases, tc.holder)
	if !errors.Is(err, tc.want) {
		t.Fatalf("HoldLease err = %v, want %v", err, tc.want)
	}

	if wantTake := !tc.noLeases && tc.holder != ""; calls.took != wantTake {
		t.Errorf("TakeLease called = %v, want %v", calls.took, wantTake)
	}

	wantWait := errors.Is(tc.want, blobguard.ErrWait)

	if _, isWait := errors.AsType[*blobguard.WaitError](err); isWait != wantWait {
		t.Fatalf("HoldLease err = %v, want a WaitError: %v", err, wantWait)
	}

	if tc.want != nil {
		if release != nil {
			t.Fatal("HoldLease returned a release function with an error")
		}

		return
	}

	if release == nil {
		t.Fatal("HoldLease returned no release function and no error")
	}

	if err := release(t.Context()); err != nil {
		t.Fatalf("release the blob delete lease: %v", err)
	}

	if calls.released != tc.holder {
		t.Fatalf("ReleaseLease(%q), want %q", calls.released, tc.holder)
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

	release, err := blobguard.HoldLease(t.Context(), leases, "dlt-7")
	if err != nil {
		t.Fatalf("HoldLease: %v", err)
	}

	if tookHolder != "dlt-7" || tookTTL != blobguard.LeaseTTL {
		t.Fatalf("TakeLease(%q, %s), want (%q, %s)", tookHolder, tookTTL, "dlt-7", blobguard.LeaseTTL)
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

	release, err := blobguard.HoldLease(t.Context(), leases, "dlt-7")
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

	release, err := blobguard.HoldLease(t.Context(), leaseAnswer(work, true, nil), "dlt-1")

	wait, isWait := errors.AsType[*blobguard.WaitError](err)
	if !isWait || wait.Work != work {
		t.Fatalf("HoldLease: want a WaitError for %v, got %v", work, wait)
	}

	if release != nil {
		t.Fatal("HoldLease returned a release function with a WaitError")
	}
}
