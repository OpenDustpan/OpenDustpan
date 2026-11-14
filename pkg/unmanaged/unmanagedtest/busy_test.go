package unmanagedtest_test

import (
	"errors"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/unmanaged"
	"opendustpan.org/opendustpan/pkg/unmanaged/unmanagedtest"
)

// TestSweepStoreBusy proves that one target holds at most one open sweep,
// and that concurrent starts leave exactly one sweep.
func TestSweepStoreBusy(t *testing.T) {
	t.Parallel()

	t.Run("second start is busy", func(t *testing.T) {
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

		first, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{Namespace: "acme", Target: "aws"}, f)
		if err != nil {
			t.Fatalf("start: %v", err)
		}

		if _, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{Namespace: "acme", Target: "aws"}, f); !errors.Is(
			err, unmanaged.ErrScanBusy,
		) {
			t.Errorf("second start = %v, want ErrScanBusy", err)
		}

		if _, err := h.Store.AdvanceSweep(t.Context(), unmanaged.SweepAdvance{
			Namespace: "acme", ID: first.ID, Event: unmanaged.EventSucceed,
		}, f); err != nil {
			t.Fatalf("succeed: %v", err)
		}

		if _, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{Namespace: "acme", Target: "aws"}, f); err != nil {
			t.Errorf("start after the end = %v, want nil", err)
		}
	})

	t.Run("concurrent starts leave one sweep", func(t *testing.T) {
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

		const callers = 8

		var wg sync.WaitGroup

		start := make(chan struct{})

		errs := make([]error, callers)

		for i := range callers {
			wg.Add(1)

			go func(i int) {
				defer wg.Done()

				<-start

				_, errs[i] = h.Store.StartSweep(t.Context(), unmanaged.Sweep{
					Namespace: "acme", Target: "aws",
				}, f)
			}(i)
		}

		close(start)
		wg.Wait()

		succeeded := 0

		for _, err := range errs {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, unmanaged.ErrScanBusy):
			default:
				t.Errorf("start = %v, want nil or ErrScanBusy", err)
			}
		}

		if succeeded != 1 {
			t.Errorf("%d starts succeeded, want exactly 1", succeeded)
		}

		list, err := h.Store.ListSweeps(t.Context(), "acme", "aws", "", unmanaged.MaxPage)
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		if len(list) != 1 {
			t.Errorf("sweeps = %d, want exactly 1 after the race", len(list))
		}
	})

	t.Run("each end status frees the target", func(t *testing.T) {
		t.Parallel()

		for _, ev := range []unmanaged.SweepEvent{
			unmanaged.EventSucceed, unmanaged.EventFail, unmanaged.EventCancel, unmanaged.EventExpire,
		} {
			t.Run(string(ev), func(t *testing.T) {
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

				s, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{Namespace: "acme", Target: "aws"}, f)
				if err != nil {
					t.Fatalf("start: %v", err)
				}

				if _, err := h.Store.AdvanceSweep(t.Context(), unmanaged.SweepAdvance{
					Namespace: "acme", ID: s.ID, Event: ev,
				}, f); err != nil {
					t.Fatalf("advance with %q: %v", ev, err)
				}

				if _, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{
					Namespace: "acme", Target: "aws",
				}, f); err != nil {
					t.Errorf("start after %q = %v, want nil", ev, err)
				}
			})
		}
	})
}

// TestSweepStoreTransitions proves that AdvanceSweep follows the transition
// table.
func TestSweepStoreTransitions(t *testing.T) {
	t.Parallel()

	t.Run("illegal event is refused", func(t *testing.T) {
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

		s, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{Namespace: "acme", Target: "aws"}, f)
		if err != nil {
			t.Fatalf("start: %v", err)
		}

		s, err = h.Store.AdvanceSweep(t.Context(), unmanaged.SweepAdvance{
			Namespace: "acme", ID: s.ID, Event: unmanaged.EventSucceed,
		}, f)
		if err != nil {
			t.Fatalf("succeed: %v", err)
		}

		if !s.Status.Terminal() {
			t.Fatalf("status = %q, want a terminal status", s.Status)
		}

		if _, err := h.Store.AdvanceSweep(t.Context(), unmanaged.SweepAdvance{
			Namespace: "acme", ID: s.ID, Event: unmanaged.EventStart,
		}, f); !errors.Is(err, unmanaged.ErrTransition) {
			t.Errorf("advance after the end = %v, want ErrTransition", err)
		}

		got, err := h.Store.GetSweep(t.Context(), "acme", s.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}

		if got.Status != s.Status {
			t.Errorf("status = %q, want the refused advance to keep %q", got.Status, s.Status)
		}
	})

	t.Run("missing sweep is not found", func(t *testing.T) {
		t.Parallel()

		h := unmanagedtest.NewHarness()(t)
		f := h.Namespace(t, "acme")

		if _, err := h.Store.AdvanceSweep(t.Context(), unmanaged.SweepAdvance{
			Namespace: "acme", ID: "swp-none", Event: unmanaged.EventCancel,
		}, f); !errors.Is(err, unmanaged.ErrNotFound) {
			t.Errorf("advance a missing sweep = %v, want ErrNotFound", err)
		}
	})
}

// TestSweepStoreDeleteTarget proves the delete rules of a target.
func TestSweepStoreDeleteTarget(t *testing.T) {
	t.Parallel()

	t.Run("open sweep refuses the delete", func(t *testing.T) {
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

		if _, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{Namespace: "acme", Target: "aws"}, f); err != nil {
			t.Fatalf("start: %v", err)
		}

		if err := h.Store.DeleteTarget(t.Context(), "acme", "aws", f); !errors.Is(err, unmanaged.ErrScanBusy) {
			t.Errorf("delete with an open sweep = %v, want ErrScanBusy", err)
		}

		got, err := h.Store.Targets(t.Context(), "acme")
		if err != nil {
			t.Fatalf("targets: %v", err)
		}

		if len(got) != 1 {
			t.Errorf("targets = %d, want the target to stay", len(got))
		}
	})

	t.Run("delete after the end and duplicate name", func(t *testing.T) {
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

		if err := h.Store.CreateTarget(t.Context(), target, f); !errors.Is(err, unmanaged.ErrAlreadyExists) {
			t.Errorf("create again = %v, want ErrAlreadyExists", err)
		}

		s, err := h.Store.StartSweep(t.Context(), unmanaged.Sweep{Namespace: "acme", Target: "aws"}, f)
		if err != nil {
			t.Fatalf("start: %v", err)
		}

		if _, err := h.Store.AdvanceSweep(t.Context(), unmanaged.SweepAdvance{
			Namespace: "acme", ID: s.ID, Event: unmanaged.EventCancel,
		}, f); err != nil {
			t.Fatalf("cancel: %v", err)
		}

		if err := h.Store.DeleteTarget(t.Context(), "acme", "aws", f); err != nil {
			t.Fatalf("delete: %v", err)
		}

		if err := h.Store.DeleteTarget(t.Context(), "acme", "aws", f); !errors.Is(err, unmanaged.ErrNotFound) {
			t.Errorf("delete again = %v, want ErrNotFound", err)
		}
	})
}
