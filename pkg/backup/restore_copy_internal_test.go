package backup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/durable"
)

var errCopyFailed = errors.New("copy failed")

func manyBlobs(n int) []ManifestBlob {
	out := make([]ManifestBlob, 0, n)

	for i := range n {
		out = append(out, ManifestBlob{
			Key: "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws/sv/a" + string(
				rune('a'+i),
			),
			Size:   1,
			MD5:    "",
			Sealed: false,
		})
	}

	return out
}

// TestRunBoundedKeepsTheBound proves that no more than limit runs are in
// flight, and that the limit is reached when there are enough items.
func TestRunBoundedKeepsTheBound(t *testing.T) {
	t.Parallel()

	const limit, items = 8, 20

	var (
		inflight, most atomic.Int64
		mu             sync.Mutex
		release        = make(chan struct{})
		started        = make(chan struct{}, items)
	)

	done := make(chan error, 1)

	go func() {
		done <- runBounded(t.Context(), manyBlobs(items), limit, func(_ context.Context, _ ManifestBlob) error {
			n := inflight.Add(1)

			mu.Lock()
			most.Store(max(most.Load(), n))
			mu.Unlock()

			started <- struct{}{}

			<-release
			inflight.Add(-1)

			return nil
		})
	}()

	// The first limit runs start and wait. No other run can start now.
	for range limit {
		<-started
	}

	close(release)

	if err := <-done; err != nil {
		t.Fatalf("runBounded: %v", err)
	}

	if got := most.Load(); got != limit {
		t.Errorf("most runs in flight = %d, want %d", got, limit)
	}
}

// TestRunBoundedStopsAtTheFirstError proves that an error cancels the runs that
// go on, starts none of the items that wait, and is the result.
func TestRunBoundedStopsAtTheFirstError(t *testing.T) {
	t.Parallel()

	const limit = 4

	var (
		count    atomic.Int64
		canceled atomic.Int64
		running  sync.WaitGroup
	)

	running.Add(limit - 1)

	err := runBounded(t.Context(), manyBlobs(20), limit, func(ctx context.Context, _ ManifestBlob) error {
		if count.Add(1) == 1 {
			// The first run fails after the others started.
			running.Wait()

			return errCopyFailed
		}

		running.Done()
		<-ctx.Done()
		canceled.Add(1)

		return nil
	})
	if !errors.Is(err, errCopyFailed) {
		t.Fatalf("runBounded: err = %v, want the first error", err)
	}

	if got := canceled.Load(); got != limit-1 {
		t.Errorf("canceled runs = %d, want %d", got, limit-1)
	}

	if got := count.Load(); got != limit {
		t.Errorf("started runs = %d, want %d: the items that wait must not start", got, limit)
	}
}

// TestRunBoundedWithACanceledContext proves that a canceled parent context
// starts no run and leaves no goroutine (the goleak check of the package).
func TestRunBoundedWithACanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var count atomic.Int64

	err := runBounded(ctx, manyBlobs(10), 4, func(context.Context, ManifestBlob) error {
		count.Add(1)

		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runBounded: err = %v, want context.Canceled", err)
	}

	if got := count.Load(); got != 0 {
		t.Errorf("started runs = %d, want 0", got)
	}
}

// TestUniqueBlobsKeepsEachKeyOnce sends duplicate keys that agree straight to
// the check. An equal duplicate is dropped.
func TestUniqueBlobsKeepsEachKeyOnce(t *testing.T) {
	t.Parallel()

	const key = "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws/sv/a1"

	base := ManifestBlob{Key: key, Size: 5, MD5: "abc", Sealed: false}

	for _, tc := range []struct {
		name string
		refs []ManifestBlob
		want int
	}{
		{"no duplicate", []ManifestBlob{base, {Key: key + "b", Size: 5, MD5: "abc", Sealed: false}}, 2},
		{"equal duplicate", []ManifestBlob{base, base}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			live := &eventLive{RestoreLive: nil, events: nil, err: nil}
			s := keyService(live)

			got, err := s.uniqueBlobs(t.Context(), noFence(), keyInput(), tc.refs)
			if err != nil || len(got) != tc.want || len(live.events) != 0 {
				t.Errorf("uniqueBlobs = %d blobs, err %v, events %v; want %d blobs, no error, no event",
					len(got), err, live.events, tc.want)
			}
		})
	}
}

// TestUniqueBlobsRefusesAConflict sends duplicate keys that disagree straight
// to the check, as if the export and the pin of the manifest had not refused
// them. The restore fails closed, and the audit event names the refusal.
func TestUniqueBlobsRefusesAConflict(t *testing.T) {
	t.Parallel()

	const key = "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws/sv/a1"

	base := ManifestBlob{Key: key, Size: 5, MD5: "abc", Sealed: false}

	for _, tc := range []struct {
		name  string
		other ManifestBlob
	}{
		{"other size", ManifestBlob{Key: key, Size: 6, MD5: "abc", Sealed: false}},
		{"other checksum", ManifestBlob{Key: key, Size: 5, MD5: "abd", Sealed: false}},
		{"other seal flag", ManifestBlob{Key: key, Size: 5, MD5: "abc", Sealed: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			live := &eventLive{RestoreLive: nil, events: nil, err: nil}
			s := keyService(live)

			_, err := s.uniqueBlobs(t.Context(), noFence(), keyInput(), []ManifestBlob{base, tc.other})
			if !errors.Is(err, ErrConflictingBlob) || !errors.Is(err, ErrCorrupt) {
				t.Fatalf("uniqueBlobs: err = %v, want ErrConflictingBlob and ErrCorrupt", err)
			}

			if len(live.events) != 1 || !strings.Contains(live.events[0], "twice with different bytes") {
				t.Errorf("events = %v, want one refusal", live.events)
			}
		})
	}
}

// TestUniqueBlobsFailsClosedWhenTheEventIsLost proves that a conflict stands
// when its audit event cannot be stored, and that a lost claim stops the step.
func TestUniqueBlobsFailsClosedWhenTheEventIsLost(t *testing.T) {
	t.Parallel()

	refs := []ManifestBlob{{Key: "k", Size: 1, MD5: "a", Sealed: false}, {Key: "k", Size: 2, MD5: "a", Sealed: false}}
	live := &eventLive{RestoreLive: nil, events: nil, err: errEventStore}
	s := keyService(live)

	_, err := s.uniqueBlobs(t.Context(), noFence(), keyInput(), refs)
	if !errors.Is(err, ErrConflictingBlob) || !errors.Is(err, errEventStore) {
		t.Fatalf("lost event: err = %v, want the conflict joined to the store error", err)
	}

	live.err = durable.ErrClaimLost

	_, err = s.uniqueBlobs(t.Context(), noFence(), keyInput(), refs)
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("lost claim: err = %v, want ErrClaimLost", err)
	}
}

// TestCopyBlobsCopiesNothingForAConflictingManifest sends a manifest with a
// good key and a conflicting duplicate of it straight to the parallel copy. The
// service has no blob store, so a copy of the good key would panic.
func TestCopyBlobsCopiesNothingForAConflictingManifest(t *testing.T) {
	t.Parallel()

	const key = "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws/sv/a1"

	live := &eventLive{RestoreLive: nil, events: nil, err: nil}
	s := keyService(live)

	err := s.copyBlobs(t.Context(), keyInput(), noFence(), []ManifestBlob{
		{Key: key, Size: 5, MD5: "abc", Sealed: false},
		{Key: key, Size: 6, MD5: "abc", Sealed: false},
	})
	if !errors.Is(err, ErrConflictingBlob) {
		t.Fatalf("copyBlobs: err = %v, want ErrConflictingBlob", err)
	}
}

// TestCopyBlobsRefusesAProtectedKeyInTheParallelPath sends a hostile key to
// the parallel copy. The copy refuses it with its own check, before any store
// is read: the service has no blob store.
func TestCopyBlobsRefusesAProtectedKeyInTheParallelPath(t *testing.T) {
	t.Parallel()

	live := &eventLive{RestoreLive: nil, events: nil, err: nil}
	s := keyService(live)

	err := s.copyBlobs(t.Context(), keyInput(), noFence(), []ManifestBlob{
		{Key: "audit-checkpoints/epochs/0001/6163/abc", Size: 1, MD5: "", Sealed: false},
	})
	if !errors.Is(err, ErrProtectedKey) {
		t.Fatalf("copyBlobs: err = %v, want ErrProtectedKey", err)
	}

	if len(live.events) != 1 || !strings.Contains(live.events[0], "refused blob key") {
		t.Errorf("events = %v, want one refusal", live.events)
	}
}
