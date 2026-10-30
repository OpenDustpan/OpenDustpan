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
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
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

// putOrphans writes n blobs that no row names under dir, with names that sort
// before the name of a checkpoint blob.
func putOrphans(t *testing.T, mem *memBlob, dir string, n int) []blob.Key {
	t.Helper()

	orphans := make([]blob.Key, 0, n)

	for i := range n {
		key, err := blob.ParseKey(fmt.Sprintf("%s-orphan-%05d", dir, i))
		if err != nil {
			t.Fatalf("orphan key: %v", err)
		}

		if putErr := mem.Put(t.Context(), key, strings.NewReader("orphan")); putErr != nil {
			t.Fatalf("write orphan blob: %v", putErr)
		}

		orphans = append(orphans, key)
	}

	return orphans
}

// requireSwept fails t unless each orphan is gone and the named blob is kept.
func requireSwept(t *testing.T, mem *memBlob, orphans []blob.Key, named blob.Key) {
	t.Helper()

	for _, key := range orphans {
		if _, err := mem.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("orphan %s: %v, want it deleted", key, err)
		}
	}

	body, err := mem.Get(t.Context(), named)
	if err != nil {
		t.Fatalf("the blob that a row names, on the second page: %v, want it kept", err)
	}

	if closeErr := body.Close(); closeErr != nil {
		t.Fatalf("close the kept blob: %v", closeErr)
	}
}

// TestStartSweepDeletesPageByPage proves the sweep that a start runs deletes
// the old orphans of a page before it asks for the next page, and still
// deletes all of them: it never holds the blobs of the whole stack. A blob
// that a row names comes on the second page, after more than a page of
// orphans, and the sweep keeps it.
func TestStartSweepDeletesPageByPage(t *testing.T) {
	t.Parallel()

	clk := newClock()
	mem := newMemBlob(clk)
	spy := &pageSpyBlob{memBlob: mem, mu: sync.Mutex{}, deletes: 0, atRequest: nil}
	f := newUpdatesWith(t, clk, spy, nil)
	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)
	input := checkpoint(1, envelope(`{"a":1}`))

	if err := f.svc.SaveCheckpoint(ctx, f.ref, first.Update.ID, first.Token, input); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	dir := fmt.Sprintf("%sstk-test/%s/", f.prefix, first.Update.ID)

	saved, err := blobtest.ListAll(ctx, mem, dir)
	if err != nil || len(saved) != 1 {
		t.Fatalf("blobs of the update after the save = %v, %v; want the one checkpoint blob", saved, err)
	}

	named := saved[0].Key
	orphans := putOrphans(t, mem, dir, blob.MaxPageLimit+100)

	if last := orphans[len(orphans)-1]; named <= last {
		t.Fatalf("the named blob %s sorts before the last orphan %s: not after a full page", named, last)
	}

	if _, err = f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete first update: %v", err)
	}

	mem.ageAll(2 * time.Hour)
	spy.reset()

	next, err := f.svc.Create(ctx, f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0,
	})
	if err != nil {
		t.Fatalf("create next update: %v", err)
	}

	if _, err = f.svc.Start(ctx, f.ref, next.ID); err != nil {
		t.Fatalf("start next update: %v", err)
	}

	if got := spy.firstRequest(); got < blob.MaxPageLimit {
		t.Fatalf("deletes before the first continuing request = %d, want at least %d", got, blob.MaxPageLimit)
	}

	requireSwept(t, mem, orphans, named)
}

// reset forgets the requests so far.
func (s *pageSpyBlob) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.atRequest = nil
}

// firstRequest returns the deletes that had run when the first continuing
// request came, or -1 when the listing had one page only.
func (s *pageSpyBlob) firstRequest() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.atRequest) == 0 {
		return -1
	}

	return s.atRequest[0]
}
