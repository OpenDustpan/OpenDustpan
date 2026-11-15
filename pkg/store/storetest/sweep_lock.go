package storetest

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
)

// testSweepLock proves that the sweep lock of a store lets one holder in at a
// time and gives the lock back at the release. It uses no sleep: the holders
// start together, and the lock decides who runs.
func testSweepLock(t *testing.T, store Store) {
	t.Helper()

	s, ok := store.(inventory.SweepLocker)
	if !ok {
		t.Fatalf("store %T does not implement inventory.SweepLocker", store)
	}

	release, err := s.TryLockSweep(t.Context())
	if err != nil {
		t.Fatalf("first TryLockSweep: %v", err)
	}

	release = SweepReleaseOnce(release)
	t.Cleanup(release)

	if _, heldErr := s.TryLockSweep(t.Context()); !errors.Is(heldErr, inventory.ErrSweepRunning) {
		t.Fatalf("TryLockSweep while held: err = %v, want ErrSweepRunning", heldErr)
	}

	release()

	release, err = s.TryLockSweep(t.Context())
	if err != nil {
		t.Fatalf("TryLockSweep after the release: %v", err)
	}

	release = SweepReleaseOnce(release)
	t.Cleanup(release)

	release()

	testSweepLockRace(t, s)

	release, err = s.TryLockSweep(t.Context())
	if err != nil {
		t.Fatalf("TryLockSweep after all releases: %v", err)
	}

	release = SweepReleaseOnce(release)
	t.Cleanup(release)

	release()
}

// testSweepLockRace starts many sweeps at once. Exactly one gets the lock
// while the winner holds it.
func testSweepLockRace(t *testing.T, s inventory.SweepLocker) {
	t.Helper()

	const sweeps = 8

	var (
		wg      sync.WaitGroup
		won     atomic.Int32
		refused atomic.Int32
		hold    = make(chan struct{})
		holding = make(chan struct{}, sweeps)
	)

	for range sweeps {
		wg.Go(func() {
			rel, lockErr := s.TryLockSweep(t.Context())
			if lockErr != nil {
				if errors.Is(lockErr, inventory.ErrSweepRunning) {
					refused.Add(1)
				}

				holding <- struct{}{}

				return
			}

			won.Add(1)

			rel = SweepReleaseOnce(rel)
			t.Cleanup(rel)

			holding <- struct{}{}

			<-hold
			rel()
		})
	}

	for range sweeps {
		<-holding
	}

	close(hold)
	wg.Wait()

	if won.Load() != 1 || refused.Load() != sweeps-1 {
		t.Fatalf("%d sweeps at once: %d got the lock, %d got ErrSweepRunning, want 1 and %d",
			sweeps, won.Load(), refused.Load(), sweeps-1)
	}
}

// SweepReleaseOnce makes a sweep release idempotent: the first call gives the
// lock back, and a later call from a t.Cleanup does nothing.
func SweepReleaseOnce(release func()) func() {
	var once sync.Once

	return func() { once.Do(release) }
}
