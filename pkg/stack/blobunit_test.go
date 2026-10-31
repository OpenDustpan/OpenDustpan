package stack_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// blobExists reports whether the store holds a blob under key.
func blobExists(t *testing.T, store blob.Store, key string) bool {
	t.Helper()

	return slices.Contains(blobKeysOf(t, store), key)
}

// unitForeignKeys are the keys that a row of the stack stk-test must never
// name: the keys of foreignKeys, which include a key of another update of the
// same stack. Each one is a blob that the call must leave alone.
func unitForeignKeys() []foreignKey {
	return foreignKeys()
}

// TestSupersedeDeleteStaysInTheUpdate is the hostile test of the delete after
// a save. The checkpoint row of the running update names a blob outside the
// directory of the update: in another namespace, or in the same namespace in
// another stack, in another update, or of another kind. The next save would
// delete the blob that the row names as superseded. It must delete nothing,
// still store its own checkpoint, and write one security refusal.
func TestSupersedeDeleteStaysInTheUpdate(t *testing.T) {
	t.Parallel()

	for _, tc := range unitForeignKeys() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			s := f.started(t, stack.KindUpdate)
			victim := tc.build(f.nsID.String(), s.Update.ID.String()+"/5")

			putAll(t, f.blobs, []string{victim})
			putForeignCheckpoint(t, f, s, victim, 5)

			saveErr := f.svc.SaveCheckpoint(
				t.Context(),
				f.ref,
				s.Update.ID,
				s.Token,
				checkpoint(6, envelope(`{"n":6}`)),
			)
			if saveErr != nil {
				t.Fatalf("save: %v", saveErr)
			}

			if !blobExists(t, f.blobs, victim) {
				t.Fatalf("the save deleted the blob %q that the row named", victim)
			}

			recs := f.refusals.Records()
			if len(recs) != 1 || recs[0].Op != "delete" || recs[0].Where != tc.where || recs[0].Namespace != f.nsID {
				t.Fatalf("refusals = %+v, want one delete in %s with the place %q", recs, f.nsID, tc.where)
			}
		})
	}
}

// listingFault is a blob store that lists keys it was told to add, whatever
// the prefix says, as an adapter with a listing fault would.
type listingFault struct {
	blob.Store

	extra []blob.Key
}

func (l *listingFault) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	page, err := l.Store.ListPage(ctx, req)
	if err != nil {
		return blob.Page{}, err
	}

	for _, key := range l.extra {
		page.Objects = append(page.Objects, blob.Object{Key: key, ModifiedAt: time.Time{}})
	}

	return page, nil
}

// listingRun is a call that lists the blobs of a stack and deletes what the
// listing returns. refused says that the call returns the refusal itself.
type listingRun struct {
	run     func(t *testing.T, f updatesFixture) error
	refused bool
}

// listingRuns are the calls that list the blobs of a stack and delete what
// the listing returns: the purge, the sweep at the start of an update and the
// sweep after a save.
func listingRuns() map[string]listingRun {
	return map[string]listingRun{
		"purge": {refused: true, run: func(t *testing.T, f updatesFixture) error {
			t.Helper()

			return f.svc.DeleteStackBlobs(t.Context(), f.nsID, "stk-test")
		}},
		"sweep at the start of an update": {refused: false, run: func(t *testing.T, f updatesFixture) error {
			t.Helper()

			f.started(t, stack.KindUpdate)

			return nil
		}},
		"sweep after a save": {refused: false, run: func(t *testing.T, f updatesFixture) error {
			t.Helper()

			s := f.started(t, stack.KindUpdate)
			f.clock.Advance(2 * time.Hour)

			return f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"n":1}`)))
		}},
	}
}

// TestListingFaultDeletesNothing is the hostile test of the purge and of the
// two sweeps. The blob store lists a blob of another stack of the same
// namespace, and a blob of another namespace, in the listing of this stack.
// The purge, the sweep at the start of an update and the sweep after a save
// must delete no blob, and write a refusal.
func TestListingFaultDeletesNothing(t *testing.T) {
	t.Parallel()

	for name, run := range listingRuns() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkListingFault(t, run)
		})
	}
}

// checkListingFault runs run on a fixture whose blob store lists two foreign
// blobs, and checks that both blobs stay and that the audit log got a list
// refusal. A purge also returns the refusal.
func checkListingFault(t *testing.T, tc listingRun) {
	t.Helper()

	clk := newClock()
	faulty := &listingFault{Store: newMemBlob(clk), extra: nil}
	f := newUpdatesWith(t, clk, faulty, nil)

	victims := []string{
		f.prefix + "stk-other/upd-1/1",
		"namespaces/ns-other/pulumi/stk-test/upd-1/1",
	}

	putAll(t, faulty.Store, victims)

	for _, v := range victims {
		faulty.extra = append(faulty.extra, blob.Key(v))
	}

	err := tc.run(t, f)

	if tc.refused && !errors.Is(err, blob.ErrOutOfScope) {
		t.Fatalf("purge: err = %v, want blob.ErrOutOfScope", err)
	}

	for _, v := range victims {
		if !blobExists(t, faulty.Store, v) {
			t.Fatalf("the call deleted the blob %q that the listing named", v)
		}
	}

	recs := f.refusals.Records()
	if len(recs) == 0 || recs[0].Op != "list" {
		t.Fatalf("refusals = %+v, want a list refusal", recs)
	}
}

// foreignRemoved is a store whose build returns more blob keys than the rows
// of the journal named, as a store with a fault would.
type foreignRemoved struct {
	*journalMemStore

	extra []blob.Key
}

func (s foreignRemoved) PutBuiltCheckpoint(
	ctx context.Context, cp stack.Checkpoint, fence namespace.Fence,
) ([]blob.Key, error) {
	keys, err := s.journalMemStore.PutBuiltCheckpoint(ctx, cp, fence)

	return append(keys, s.extra...), err
}

// TestBuildDeleteStaysInTheJournalOfTheUpdate is the hostile test of the
// delete after a build. The store returns, with the keys of the journal rows,
// the key of a journal batch of another stack of the same namespace, of
// another update of the same stack, and of another namespace. The build must
// delete the batches of the update, delete no other blob, and write one
// refusal for each foreign key.
func TestBuildDeleteStaysInTheJournalOfTheUpdate(t *testing.T) {
	t.Parallel()

	clk := newClock()

	var foreign foreignRemoved

	f := newBuildFixtureWith(t, clk, pulumijournal.NewReplayer(clk.Now, ""),
		func(s *journalMemStore) stack.CheckpointStore {
			foreign = foreignRemoved{journalMemStore: s, extra: nil}

			return &foreign
		})

	prefix := "namespaces/" + f.nsID.String() + "/pulumi/"
	victims := []string{
		prefix + "stk-other/upd-1/journal/1",
		prefix + f.stackID.String() + "/upd-other/journal/1",
		prefix + f.stackID.String() + "/upd-other/1",
		"namespaces/ns-other/pulumi/" + f.stackID.String() + "/upd-1/journal/1",
	}

	putAll(t, f.blobs, victims)

	for _, v := range victims {
		foreign.extra = append(foreign.extra, blob.Key(v))
	}

	s := f.startJournaled(t)
	own := f.journalKeys(t, s.Update.ID)

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete journaled update: %v", err)
	}

	for _, v := range victims {
		if !blobExists(t, f.blobs, v) {
			t.Errorf("the build deleted the blob %q that the store returned", v)
		}
	}

	for _, key := range own {
		if blobExists(t, f.blobs, key.String()) {
			t.Errorf("the build kept the journal blob %q of the update", key)
		}
	}

	recs := f.refusals.Records()
	if len(recs) != len(victims) {
		t.Fatalf("refusals = %+v, want %d", recs, len(victims))
	}

	for _, rec := range recs {
		if rec.Op != "delete" || rec.Namespace != f.nsID {
			t.Errorf("refusal = %+v, want a delete in %s", rec, f.nsID)
		}
	}
}

// TestBlobCallsMakeNoExtraNamespaceReads checks that the read paths take the
// namespace ID from the row that the store returned. An export, an export of a
// version and a purge make no read of the namespace. A read of the state of a
// running journaled update makes none either. A save makes the one read that
// the fence needs.
func TestBlobCallsMakeNoExtraNamespaceReads(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	before := f.nsStore.reads.Load()

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"a":1}`))); err != nil {
		t.Fatalf("save: %v", err)
	}

	if got := f.nsStore.reads.Load() - before; got != 1 {
		t.Errorf("save: %d namespace reads, want 1 (the fence)", got)
	}

	before = f.nsStore.reads.Load()

	if _, err := f.svc.Export(ctx, f.ref); err != nil {
		t.Fatalf("export: %v", err)
	}

	if _, err := f.svc.ExportVersion(ctx, f.ref, s.Update.Version); err != nil {
		t.Fatalf("export of a version: %v", err)
	}

	if err := f.svc.DeleteStackBlobs(ctx, f.nsID, "stk-test"); err != nil {
		t.Fatalf("delete stack blobs: %v", err)
	}

	if got := f.nsStore.reads.Load() - before; got != 0 {
		t.Errorf("export, export of a version and purge: %d namespace reads, want 0", got)
	}

	clk := newClock()
	b := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))
	b.startJournaled(t)

	before = b.nsStore.reads.Load()

	if _, err := b.svc.Export(ctx, b.ref); err != nil {
		t.Fatalf("export during the journaled update: %v", err)
	}

	if got := b.nsStore.reads.Load() - before; got != 0 {
		t.Errorf("export of a journaled state: %d namespace reads, want 0", got)
	}
}
