package stack_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/stack"
)

// pageSpyBlob records, for each request that continues a listing, how many
// blobs were deleted before the request. A sweep that acts on a page before it
// asks for the next one has deleted a full page by then. A sweep that lists
// the whole stack first has deleted none.
type pageSpyBlob struct {
	*memBlob

	mu        sync.Mutex
	deletes   int
	atRequest []int
}

func (s *pageSpyBlob) Delete(ctx context.Context, key blob.Key) error {
	err := s.memBlob.Delete(ctx, key)

	s.mu.Lock()
	s.deletes++
	s.mu.Unlock()

	return err
}

func (s *pageSpyBlob) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if req.After != "" {
		s.mu.Lock()
		s.atRequest = append(s.atRequest, s.deletes)
		s.mu.Unlock()
	}

	return s.memBlob.ListPage(ctx, req)
}

// TestStartSweepDeletesPageByPage proves the sweep that a start runs deletes
// the old orphans of a page before it asks for the next page, and still
// deletes all of them: it never holds the blobs of the whole stack.
func TestStartSweepDeletesPageByPage(t *testing.T) {
	t.Parallel()

	clk := newClock()
	mem := newMemBlob(clk)
	spy := &pageSpyBlob{memBlob: mem, mu: sync.Mutex{}, deletes: 0, atRequest: nil}
	f := newUpdatesWith(t, clk, spy, nil)
	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)

	orphans := make([]blob.Key, 0, blob.MaxPageLimit+100)

	for i := range blob.MaxPageLimit + 100 {
		key, err := blob.ParseKey(fmt.Sprintf("%sstk-test/%s/%d", f.prefix, first.Update.ID, 1000+i))
		if err != nil {
			t.Fatalf("orphan key: %v", err)
		}

		if putErr := mem.Put(ctx, key, strings.NewReader("orphan")); putErr != nil {
			t.Fatalf("write orphan blob: %v", putErr)
		}

		orphans = append(orphans, key)
	}

	if _, err := f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete first update: %v", err)
	}

	mem.ageAll(2 * time.Hour)

	spy.mu.Lock()
	spy.atRequest = nil
	spy.mu.Unlock()

	next, err := f.svc.Create(ctx, f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0,
	})
	if err != nil {
		t.Fatalf("create next update: %v", err)
	}

	if _, err := f.svc.Start(ctx, f.ref, next.ID); err != nil {
		t.Fatalf("start next update: %v", err)
	}

	spy.mu.Lock()
	requests := append([]int(nil), spy.atRequest...)
	spy.mu.Unlock()

	if len(requests) == 0 || requests[0] < blob.MaxPageLimit {
		t.Fatalf("deletes before each continuing request = %v, want the first at least %d", requests, blob.MaxPageLimit)
	}

	for _, key := range orphans {
		if _, err := mem.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("orphan %s: %v, want it deleted", key, err)
		}
	}
}
