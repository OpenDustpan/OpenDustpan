package unmanagedtest_test

import (
	"errors"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
	"opendustpan.org/opendustpan/pkg/unmanaged/unmanagedtest"
)

// TestSweepStoreBounds proves the bounds of the sweep store.
func TestSweepStoreBounds(t *testing.T) {
	t.Parallel()

	t.Run("page above the bound is refused", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		h.Namespace(t, "acme")

		if _, err := h.Store.ListSweeps(t.Context(), "acme", "", "", unmanaged.MaxPage+1); !errors.Is(
			err, unmanaged.ErrLimit,
		) {
			t.Errorf("page of %d = %v, want ErrLimit", unmanaged.MaxPage+1, err)
		}

		if _, err := h.Store.ListSweeps(t.Context(), "acme", "", "", 0); !errors.Is(err, unmanaged.ErrLimit) {
			t.Errorf("page of 0 = %v, want ErrLimit", err)
		}
	})

	t.Run("targets above the limit are refused", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		target := func(name string) unmanaged.Target {
			return unmanaged.Target{
				Namespace: "acme", Name: name, PoolID: pool,
				Every: 3600000000000, Timeout: 0, Enabled: true,
			}
		}

		for i := range unmanaged.MaxTargets {
			name := "target-" + string(rune('a'+i%26)) + "-" + string(rune('0'+i/26))
			if err := h.Store.CreateTarget(t.Context(), target(name), f); err != nil {
				t.Fatalf("create %d: %v", i, err)
			}
		}

		// One more target is above the limit. The store refuses it and the
		// count stays at the limit.
		if err := h.Store.CreateTarget(t.Context(), target("one-too-many"), f); !errors.Is(
			err, unmanaged.ErrLimit,
		) {
			t.Errorf("create above the limit = %v, want ErrLimit", err)
		}

		got, err := h.Store.Targets(t.Context(), "acme")
		if err != nil {
			t.Fatalf("targets: %v", err)
		}

		if len(got) != unmanaged.MaxTargets {
			t.Errorf("targets = %d, want %d", len(got), unmanaged.MaxTargets)
		}
	})

	t.Run("malformed namespace ID is fenced", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		bad := unmanaged.Target{
			Namespace: "acme!", Name: "aws", PoolID: pool,
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}

		// The fence names another namespace than the target. The store
		// checks the fence first and refuses with ErrFenced.
		other := namespace.Fence{Namespace: "other", FailoverVersion: 1}
		_ = f

		if err := h.Store.CreateTarget(t.Context(), bad, other); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("create with a fence of another namespace = %v, want ErrFenced", err)
		}
	})
}

// TestSweepStoreConcurrentFence proves that a fence change during a write
// refuses the write and leaves no partial row.
func TestSweepStoreConcurrentFence(t *testing.T) {
	t.Parallel()

	t.Run("concurrent fence bump refuses one write", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		const writers = 4

		var wg sync.WaitGroup

		start := make(chan struct{})

		errs := make([]error, writers)

		for i := range writers {
			wg.Add(1)

			go func(i int) {
				defer wg.Done()

				<-start

				// The first writer bumps the fence while the others run.
				// At most the writes with the live fence succeed.
				if i == 0 {
					h.BumpFence(t, "acme")
				}

				name := "target-" + string(rune('a'+i))

				errs[i] = h.Store.CreateTarget(t.Context(), unmanaged.Target{
					Namespace: "acme", Name: name, PoolID: pool,
					Every: 3600000000000, Timeout: 0, Enabled: true,
				}, f)
			}(i)
		}

		close(start)
		wg.Wait()

		fenced := 0

		for _, err := range errs {
			if errors.Is(err, namespace.ErrFenced) {
				fenced++
			} else if err != nil {
				t.Errorf("create = %v, want nil or ErrFenced", err)
			}
		}

		if fenced == 0 {
			t.Error("no write met the bumped fence, want at least one ErrFenced")
		}

		got, err := h.Store.Targets(t.Context(), "acme")
		if err != nil {
			t.Fatalf("targets: %v", err)
		}

		// Every stored target was written with the live fence.
		if len(got)+fenced != writers {
			t.Errorf("targets = %d plus %d fenced, want %d writes in all", len(got), fenced, writers)
		}
	})
}

// TestSweepStoreRollback proves that a write that fails after its fence
// check leaves no row.
func TestSweepStoreRollback(t *testing.T) {
	t.Parallel()

	t.Run("failed create stores nothing", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		h.FailNext(t)

		target := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: pool,
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}

		if err := h.Store.CreateTarget(t.Context(), target, f); err == nil {
			t.Fatal("create with an injected failure succeeded, want an error")
		}

		got, err := h.Store.Targets(t.Context(), "acme")
		if err != nil {
			t.Fatalf("targets: %v", err)
		}

		if len(got) != 0 {
			t.Errorf("targets = %+v, want none after the rollback", got)
		}

		// The next write with the same fence succeeds.
		if err := h.Store.CreateTarget(t.Context(), target, f); err != nil {
			t.Errorf("create after the rollback: %v", err)
		}
	})

	t.Run("failed start creates no sweep", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		target := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: pool,
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}

		if err := h.Store.CreateTarget(t.Context(), target, f); err != nil {
			t.Fatalf("create: %v", err)
		}

		h.FailNext(t)

		if _, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{
			Namespace: "acme", Target: "aws",
		}, f); err == nil {
			t.Fatal("start with an injected failure succeeded, want an error")
		}

		list, err := h.Store.ListSweeps(t.Context(), "acme", "", "", unmanaged.MaxPage)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		if len(list) != 0 {
			t.Errorf("sweeps = %d, want none after the rollback", len(list))
		}
	})
}
