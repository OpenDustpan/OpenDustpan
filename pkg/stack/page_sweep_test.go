package stack_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

// pageSpyBlob records, for each request that continues a listing, how many
// blobs were deleted before the request. A sweep that acts on a page before it
// asks for the next one has deleted a full page by then. A sweep that lists
// the whole stack first has deleted none.
type pageSpyBlob struct {
	*memBlob

	mu         sync.Mutex
	deletes    int
	atRequest  []int
	onContinue func()
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
		hook := s.onContinue
		s.mu.Unlock()

		if hook != nil {
			hook()
		}
	}

	return s.memBlob.ListPage(ctx, req)
}

// continueWith runs hook when the listing asks for the page after the first
// one, so a test changes the stack between two listings.
func (s *pageSpyBlob) continueWith(hook func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.onContinue = hook
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
	spy := &pageSpyBlob{
		memBlob: mem, mu: sync.Mutex{}, deletes: 0, atRequest: nil, onContinue: nil,
	}
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
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
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

// nameBlob records a checkpoint row that names key, as a save does, so that a
// later sweep keeps the blob.
func (m *memUpdates) nameBlob(ref stack.Ref, id stack.UpdateID, key blob.Key) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.checkpoints[id] = stack.Checkpoint{
		NamespaceID: "", StackID: "",
		SavedAt:       time.Time{},
		Features:      nil,
		Deployment:    nil,
		Update:        id,
		Ref:           ref,
		Lease:         token.Digest{},
		SchemaVersion: 0,
		Sequence:      0,
		Digest:        [32]byte{},
		Invalid:       false,
		BlobKey:       key,
		BlobSize:      0,
		BlobMD5:       [16]byte{},

		AttemptStartedAt: time.Time{}, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// pageOf returns the one-based page of key in the listing under dir.
func pageOf(t *testing.T, mem *memBlob, dir string, key blob.Key) int {
	t.Helper()

	all, err := blobtest.ListAll(t.Context(), mem, dir)
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}

	at := slices.IndexFunc(all, func(obj blob.Object) bool { return obj.Key == key })
	if at < 0 {
		t.Fatalf("key %s is not in the listing of %s", key, dir)
	}

	return at/blob.MaxPageLimit + 1
}

// requireKept fails t unless key is stored.
func requireKept(t *testing.T, mem *memBlob, key blob.Key) {
	t.Helper()

	body, err := mem.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("blob %s: %v, want it kept", key, err)
	}

	if closeErr := body.Close(); closeErr != nil {
		t.Fatalf("close blob %s: %v", key, closeErr)
	}
}

// requireUnreferenced fails t when a row of the stack names key.
func requireUnreferenced(t *testing.T, store *memUpdates, stackID stack.ID, key blob.Key) {
	t.Helper()

	refs, err := store.ReferencedBlobKeys(t.Context(), stackID)
	if err != nil {
		t.Fatalf("referenced keys: %v", err)
	}

	if slices.Contains(refs, key) {
		t.Fatalf("blob %s is referenced: %v", key, refs)
	}
}

// TestStartSweepRefreshesReferencesBetweenPages proves that the sweep reads
// the referenced keys again for each page. A blob on the second page is not
// referenced when the sweep starts, and a row names it after the sweep lists
// and sweeps the first page. The blob stays.
func TestStartSweepRefreshesReferencesBetweenPages(t *testing.T) {
	t.Parallel()

	clk := newClock()
	mem := newMemBlob(clk)
	spy := &pageSpyBlob{
		memBlob: mem, mu: sync.Mutex{}, deletes: 0, atRequest: nil, onContinue: nil,
	}
	f := newUpdatesWith(t, clk, spy, nil)
	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)

	err := f.svc.SaveCheckpoint(
		ctx, f.ref, first.Update.ID, first.Token, checkpoint(1, envelope(`{"a":1}`)),
	)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	dir := fmt.Sprintf("%sstk-test/%s/", f.prefix, first.Update.ID)

	saved, err := blobtest.ListAll(ctx, mem, dir)
	if err != nil || len(saved) != 1 {
		t.Fatalf("blobs of the update after the save = %v, %v; want the one checkpoint blob", saved, err)
	}

	named := saved[0].Key
	orphans := putOrphans(t, mem, dir, blob.MaxPageLimit+100)

	late, err := blob.ParseKey(dir + "2-late")
	if err != nil {
		t.Fatalf("late key: %v", err)
	}

	if putErr := mem.Put(ctx, late, strings.NewReader("late")); putErr != nil {
		t.Fatalf("write the late blob: %v", putErr)
	}

	if page := pageOf(t, mem, dir, late); page != 2 {
		t.Fatalf("the late blob is on page %d, want page two", page)
	}

	requireUnreferenced(t, f.store, "stk-test", late)

	if _, err = f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete first update: %v", err)
	}

	mem.ageAll(2 * time.Hour)

	next, err := f.svc.Create(ctx, f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create next update: %v", err)
	}

	spy.continueWith(func() { f.store.nameBlob(f.ref, next.ID, late) })

	if _, err = f.svc.Start(ctx, f.ref, next.ID); err != nil {
		t.Fatalf("start next update: %v", err)
	}

	requireSwept(t, mem, orphans, late)
	requireKept(t, mem, named)
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
