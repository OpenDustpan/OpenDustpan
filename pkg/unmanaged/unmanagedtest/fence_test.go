package unmanagedtest_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
	"opendustpan.org/opendustpan/pkg/unmanaged/unmanagedtest"
)

// TestSweepStoreFence proves that each write checks the fence in the same
// transaction as the write.
func TestSweepStoreFence(t *testing.T) {
	t.Parallel()

	t.Run("stale fence refuses each write", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")
		stale := namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}

		target := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: pool,
			Every: 3600000000000, Timeout: 60000000000, Enabled: true,
		}

		if err := h.Store.CreateTarget(t.Context(), target, stale); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("create with a stale fence = %v, want ErrFenced", err)
		}

		if err := h.Store.CreateTarget(t.Context(), target, f); err != nil {
			t.Fatalf("create: %v", err)
		}

		changed := target
		changed.Every = 0

		if err := h.Store.UpdateTarget(t.Context(), changed, stale); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("update with a stale fence = %v, want ErrFenced", err)
		}

		if _, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{
			Namespace: "acme", Target: "aws",
		}, stale); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("start with a stale fence = %v, want ErrFenced", err)
		}

		if err := h.Store.DeleteTarget(t.Context(), "acme", "aws", stale); !errors.Is(
			err, namespace.ErrFenced,
		) {
			t.Errorf("delete with a stale fence = %v, want ErrFenced", err)
		}

		got, err := h.Store.Targets(t.Context(), "acme")
		if err != nil {
			t.Fatalf("targets: %v", err)
		}

		if len(got) != 1 {
			t.Errorf("targets = %d, want the one target the live fence made", len(got))
		}
	})

	t.Run("missing and deleted namespace are fenced", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		missing := namespace.Fence{Namespace: "absent", FailoverVersion: 1}
		target := unmanaged.Target{
			Namespace: "absent", Name: "aws", PoolID: pool,
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}

		if err := h.Store.CreateTarget(t.Context(), target, missing); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("create in a missing namespace = %v, want ErrFenced", err)
		}

		h.DeleteNamespace(t, "acme")

		target.Namespace = "acme"

		if err := h.Store.CreateTarget(t.Context(), target, f); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("create in a deleted namespace = %v, want ErrFenced", err)
		}
	})

	t.Run("write to one namespace leaves the rows of another", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		h.Namespace(t, "other")

		pool := h.PoolID(t, "acme")

		target := unmanaged.Target{
			Namespace: "acme", Name: "aws", PoolID: pool,
			Every: 3600000000000, Timeout: 0, Enabled: true,
		}

		if err := h.Store.CreateTarget(t.Context(), target, f); err != nil {
			t.Fatalf("create: %v", err)
		}

		other, err := h.Store.Targets(t.Context(), "other")
		if err != nil {
			t.Fatalf("targets of other: %v", err)
		}

		if len(other) != 0 {
			t.Errorf("targets of other = %+v, want none", other)
		}
	})

	t.Run("large batch with a stale fence stores nothing", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")
		stale := namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 99}

		for i := range 3 {
			name := string(rune('a'+i)) + "-target"

			target := unmanaged.Target{
				Namespace: "acme", Name: name, PoolID: pool,
				Every: 3600000000000, Timeout: 0, Enabled: true,
			}

			if err := h.Store.CreateTarget(t.Context(), target, stale); !errors.Is(
				err, namespace.ErrFenced,
			) {
				t.Errorf("create %q with a stale fence = %v, want ErrFenced", name, err)
			}
		}

		got, err := h.Store.Targets(t.Context(), "acme")
		if err != nil {
			t.Fatalf("targets: %v", err)
		}

		if len(got) != 0 {
			t.Errorf("targets = %d, want none after the refused batch", len(got))
		}
	})
}
