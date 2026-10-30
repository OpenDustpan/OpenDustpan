package workspace_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// strayCount is the count of blobs of the page walk tests. It is more than
// one page, so a caller that lists the whole unit before it acts differs from
// one that acts page by page.
const strayCount = blob.MaxPageLimit + 100

// pageSpy records, for each request that continues a listing, how many blobs
// were deleted before the request. A caller that acts on a page before it
// asks for the next one has deleted a full page by then. A caller that lists
// the whole unit first has deleted none.
type pageSpy struct {
	blob.Store

	mu        sync.Mutex
	deletes   int
	atRequest []int
}

func (s *pageSpy) Delete(ctx context.Context, key blob.Key) error {
	err := s.Store.Delete(ctx, key)

	s.mu.Lock()
	s.deletes++
	s.mu.Unlock()

	if err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}

	return nil
}

func (s *pageSpy) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if req.After != "" {
		s.mu.Lock()
		s.atRequest = append(s.atRequest, s.deletes)
		s.mu.Unlock()
	}

	page, err := s.Store.ListPage(ctx, req)
	if err != nil {
		return blob.Page{}, fmt.Errorf("list %s: %w", req.Prefix, err)
	}

	return page, nil
}

// deletesBeforeNextPage returns the deletes that had run when the first
// continuing request came, or -1 when the listing had one page only.
func (s *pageSpy) deletesBeforeNextPage() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.atRequest) == 0 {
		return -1
	}

	return s.atRequest[0]
}

// TestDeletePurgedBlobsDeletesPageByPage proves the purge deletes the blobs of
// a page before it asks for the next page, so it never holds the keys of the
// whole workspace.
func TestDeletePurgedBlobsDeletesPageByPage(t *testing.T) {
	t.Parallel()

	spy := &pageSpy{Store: openFixtureBlobs(t), mu: sync.Mutex{}, deletes: 0, atRequest: nil}
	f := newFixtureWithBlobs(t, spy)
	f.blobs = spy

	prefix := purgedWorkspace(t, f)

	for i := range strayCount {
		key, err := blob.ParseKey(fmt.Sprintf("%ssv-stray/a%05d", prefix, i))
		if err != nil {
			t.Fatalf("stray key: %v", err)
		}

		if putErr := spy.Put(t.Context(), key, strings.NewReader("x")); putErr != nil {
			t.Fatalf("put %s: %v", key, putErr)
		}
	}

	if err := f.svc.DeletePurgedBlobs(t.Context(), f.ws.NamespaceID, f.ws.ID); err != nil {
		t.Fatalf("delete purged blobs: %v", err)
	}

	if got := spy.deletesBeforeNextPage(); got < blob.MaxPageLimit {
		t.Fatalf("deletes before the second page = %d, want at least %d", got, blob.MaxPageLimit)
	}

	if left, err := blobtest.ListAll(t.Context(), f.blobs, prefix); err != nil || len(left) != 0 {
		t.Fatalf("blobs after the delete = %d, %v, want none", len(left), err)
	}
}

// TestSweepDeletesPageByPage proves the sweep of unreferenced blobs deletes
// the old orphans of a page before it asks for the next page, and still
// deletes all of them.
func TestSweepDeletesPageByPage(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	spy := &pageSpy{Store: blobs, mu: sync.Mutex{}, deletes: 0, atRequest: nil}
	f := newFixtureWithBlobs(t, spy)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	// A finalized version names its blob. The orphans below sort before it, so
	// it comes on the second page of the listing, after more than a page of
	// blobs that no version names, and the sweep must keep it.
	pending, err := f.svc.BeginState(
		ctx, f.ws.ID, "alice", newState("l1", 1, "{\"v\":1}"), authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	current, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	old := f.now.Add(-2 * workspace.SweepGracePeriod)
	chtimesBlob(t, dir, current.Content, old)

	var last blob.Key

	for i := range strayCount {
		key, keyErr := blob.ParseKey(fmt.Sprintf("%ssv--stray/a%05d", f.stateDir(t, f.ws.ID), i))
		if keyErr != nil {
			t.Fatalf("stray key: %v", keyErr)
		}

		if putErr := blobs.Put(ctx, key, strings.NewReader("x")); putErr != nil {
			t.Fatalf("put %s: %v", key, putErr)
		}

		chtimesBlob(t, dir, key, old)

		last = key
	}

	if current.Content <= last {
		t.Fatalf("the named blob %s sorts before the last stray %s: not after a full page", current.Content, last)
	}

	_, err = f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 2, "{\"v\":2}"), authority(forcedOnly(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if got := spy.deletesBeforeNextPage(); got < blob.MaxPageLimit {
		t.Fatalf("deletes before the second page = %d, want at least %d", got, blob.MaxPageLimit)
	}

	if left := listBlobDir(t, dir); !slices.Equal(left, []string{current.Content.String()}) {
		t.Fatalf("blobs after the sweep = %d, want only the named blob %s", len(left), current.Content)
	}
}
