// Conformance suite of the sweep store.
package unmanagedtest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

// RunConformance runs the general cases of the sweep store.
func RunConformance(t *testing.T, newHarness Factory) {
	t.Helper()

	t.Run("create and list targets", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		if err := h.Store.CreateTarget(t.Context(), targetOf("acme", "aws", pool), f); err != nil {
			t.Fatalf("create: %v", err)
		}

		got, err := h.Store.Targets(t.Context(), "acme")
		if err != nil {
			t.Fatalf("targets: %v", err)
		}

		if len(got) != 1 || got[0].Name != "aws" || got[0].PoolID != pool {
			t.Errorf("targets = %+v, want the one target aws", got)
		}
	})

	t.Run("duplicate name is refused", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		if err := h.Store.CreateTarget(t.Context(), targetOf("acme", "aws", pool), f); err != nil {
			t.Fatalf("create: %v", err)
		}

		if err := h.Store.CreateTarget(t.Context(), targetOf("acme", "aws", pool), f); err == nil {
			t.Error("create again succeeded, want ErrAlreadyExists")
		} else if !isErr(err, unmanaged.ErrAlreadyExists) {
			t.Errorf("create again = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("get and list sweeps", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		f := h.Namespace(t, "acme")
		pool := h.PoolID(t, "acme")

		if err := h.Store.CreateTarget(t.Context(), targetOf("acme", "aws", pool), f); err != nil {
			t.Fatalf("create: %v", err)
		}

		s, err := h.Store.StartSweep(t.Context(), sweepOf("acme", "aws"), f)
		if err != nil {
			t.Fatalf("start: %v", err)
		}

		if s.ID == "" || s.Status != unmanaged.SweepQueued {
			t.Errorf("sweep = %+v, want an ID and the queued status", s)
		}

		got, err := h.Store.GetSweep(t.Context(), "acme", s.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}

		if got.ID != s.ID {
			t.Errorf("get = %+v, want ID %q", got, s.ID)
		}

		list, err := h.Store.ListSweeps(t.Context(), "acme", "", "", unmanaged.MaxPage)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		if len(list) != 1 || list[0].ID != s.ID {
			t.Errorf("list = %+v, want the one sweep", list)
		}
	})

	t.Run("missing target and sweep", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		f := h.Namespace(t, "acme")

		if _, err := h.Store.StartSweep(t.Context(), sweepOf("acme", "none"), f); !isErr(
			err, unmanaged.ErrNotFound,
		) {
			t.Errorf("start on a missing target = %v, want ErrNotFound", err)
		}

		if _, err := h.Store.GetSweep(t.Context(), "acme", "swp-none"); !isErr(err, unmanaged.ErrNotFound) {
			t.Errorf("get a missing sweep = %v, want ErrNotFound", err)
		}
	})

	t.Run("namespace isolation", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		f := h.Namespace(t, "acme")
		h.Namespace(t, "other")
		pool := h.PoolID(t, "acme")

		if err := h.Store.CreateTarget(t.Context(), targetOf("acme", "aws", pool), f); err != nil {
			t.Fatalf("create: %v", err)
		}

		got, err := h.Store.Targets(t.Context(), "other")
		if err != nil {
			t.Fatalf("targets of other: %v", err)
		}

		if len(got) != 0 {
			t.Errorf("targets of other = %+v, want none", got)
		}

		stale := namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
		if err := h.Store.CreateTarget(t.Context(), targetOf("acme", "second", pool), stale); !isErr(
			err, namespace.ErrFenced,
		) {
			t.Errorf("create with a stale fence = %v, want ErrFenced", err)
		}
	})
}

// TestSweepStoreConformance runs the conformance cases on the reference
// adapter. The runnable copy lives in conformance_test.go; this copy keeps
// the test name in the NEW file the plan names.
func TestSweepStoreConformance(t *testing.T) {
	t.Helper()

	RunConformance(t, NewHarness())
}

func isErr(err, want error) bool {
	return errors.Is(err, want)
}
