package unmanagedtest_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/unmanaged"
	"opendustpan.org/opendustpan/pkg/unmanaged/unmanagedtest"
)

// TestSweepStorePool proves that a target names an operator pool with the
// sweep purpose, and that the start re-reads the pool.
func TestSweepStorePool(t *testing.T) {
	t.Parallel()

	t.Run("jobs pool is refused", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		jobs := h.JobPoolID(t, "acme")

		target := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: jobs,
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}

		if err := h.Store.CreateTarget(t.Context(), target, f); !errors.Is(err, unmanaged.ErrInvalidTarget) {
			t.Errorf("create on a jobs pool = %v, want ErrInvalidTarget", err)
		}
	})

	t.Run("namespace sweep pool is refused", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")

		target := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: "pool-acme-sweep",
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}
		// The harness pool IDs are stable IDs. A pool ID that no harness
		// made is unknown, which the store also refuses.
		if err := h.Store.CreateTarget(t.Context(), target, f); err != nil {
			t.Fatalf("create on the operator sweep pool: %v", err)
		}

		h.SetPoolPurpose(t, "pool-acme-sweep", unmanaged.PurposeJobs)

		moved := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: "pool-acme-sweep",
			Every: 0, Timeout: 0, Enabled: true,
		}

		// The purpose changed after the save, so an update with the same
		// pool is now refused.
		if err := h.Store.UpdateTarget(t.Context(), moved, f); !errors.Is(err, unmanaged.ErrInvalidTarget) {
			t.Errorf("update after the purpose changed = %v, want ErrInvalidTarget", err)
		}
	})

	t.Run("pool of another namespace is refused", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		h.Namespace(t, "other")
		foreign := h.PoolID(t, "other")

		target := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: foreign,
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}

		if err := h.Store.CreateTarget(t.Context(), target, f); !errors.Is(err, unmanaged.ErrInvalidTarget) {
			t.Errorf("create with a pool of another namespace = %v, want ErrInvalidTarget", err)
		}
	})

	t.Run("pool that changes after the save", func(t *testing.T) {
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

		// The pool changes purpose after the save. The start re-reads the
		// pool by its stable ID and refuses with the pool-purpose error.
		h.SetPoolPurpose(t, pool, unmanaged.PurposeJobs)

		if _, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{
			Namespace: "acme", Target: "aws",
		}, f); !errors.Is(err, unmanaged.ErrInvalidTarget) {
			t.Errorf("start after the purpose changed = %v, want ErrInvalidTarget", err)
		}

		// A deleted pool is refused the same way and creates no sweep.
		h.SetPoolPurpose(t, pool, unmanaged.PurposeSweeps)
		h.DeletePool(t, pool)

		if _, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{
			Namespace: "acme", Target: "aws",
		}, f); !errors.Is(err, unmanaged.ErrInvalidTarget) {
			t.Errorf("start on a deleted pool = %v, want ErrInvalidTarget", err)
		}

		list, err := h.Store.ListSweeps(t.Context(), "acme", "", "", unmanaged.MaxPage)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		if len(list) != 0 {
			t.Errorf("sweeps = %d, want none after the refused starts", len(list))
		}
	})

	t.Run("malformed pool ID is refused", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")

		target := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: "",
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}

		if err := h.Store.CreateTarget(t.Context(), target, f); !errors.Is(err, unmanaged.ErrInvalidTarget) {
			t.Errorf("create with an empty pool = %v, want ErrInvalidTarget", err)
		}
	})

	t.Run("rename keeps the pool by ID", func(t *testing.T) {
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

		// The pool keeps its ID while its purpose stays sweeps. A rename
		// of the pool name outside the store does not change the check,
		// because the store reads the pool by ID.
		got, err := h.Store.Targets(t.Context(), "acme")
		if err != nil {
			t.Fatalf("targets: %v", err)
		}

		if len(got) != 1 || got[0].PoolID != pool {
			t.Errorf("targets = %+v, want the target with pool ID %q", got, pool)
		}
	})
}
