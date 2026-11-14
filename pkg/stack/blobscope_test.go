package stack_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// countingBlob counts the calls that reach the in-memory blob store under it.
type countingBlob struct {
	*memBlob

	n  int
	mu sync.Mutex
}

func newCountingBlob() *countingBlob {
	return &countingBlob{memBlob: newMemBlob(newClock()), n: 0, mu: sync.Mutex{}}
}

func (c *countingBlob) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	c.bump()

	return c.memBlob.Put(ctx, key, r)
}

func (c *countingBlob) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	c.bump()

	return c.memBlob.Get(ctx, key)
}

func (c *countingBlob) Delete(ctx context.Context, key blob.Key) error {
	c.bump()

	return c.memBlob.Delete(ctx, key)
}

func (c *countingBlob) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	c.bump()

	return c.memBlob.ListPage(ctx, req)
}

// bump counts one call.
func (c *countingBlob) bump() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.n++
}

// calls returns the number of calls so far.
func (c *countingBlob) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.n
}

// foreignKey is a checkpoint key that a row of the namespace of a fixture
// must never name: build returns it for the namespace ID ns and the tail.
type foreignKey struct {
	build func(ns, tail string) string
	name  string
	where scopeaudit.Where
}

// foreignKeys are the keys that the blob scope refuses, with the place that
// the refusal records.
func foreignKeys() []foreignKey {
	return []foreignKey{
		{
			name:  "another namespace",
			build: func(_, tail string) string { return "namespaces/ns-other/pulumi/stk-test/" + tail },
			where: scopeaudit.OutsideNamespace,
		},
		{
			name:  "old layout",
			build: func(_, tail string) string { return "pulumi/stk-test/" + tail },
			where: scopeaudit.OutsideNamespace,
		},
		{
			name:  "id as a prefix",
			build: func(ns, tail string) string { return "namespaces/" + ns + "x/pulumi/stk-test/" + tail },
			where: scopeaudit.OutsideNamespace,
		},
		{
			name:  "own tofu directory",
			build: func(ns, tail string) string { return "namespaces/" + ns + "/tofu/stk-test/" + tail },
			where: scopeaudit.OutsideDirectory,
		},
		{
			name:  "own item keys",
			build: func(ns, tail string) string { return "namespaces/" + ns + "/item-keys/stk-test/" + tail },
			where: scopeaudit.OutsideDirectory,
		},
		{
			name:  "same namespace, another stack",
			build: func(ns, tail string) string { return "namespaces/" + ns + "/pulumi/stk-other/" + tail },
			where: scopeaudit.OutsideUnitDirectory,
		},
		{
			name:  "same stack, another update",
			build: func(ns, _ string) string { return "namespaces/" + ns + "/pulumi/stk-test/upd-other/5" },
			where: scopeaudit.OutsideUnitDirectory,
		},
		{
			name:  "same namespace, stack ID as a prefix",
			build: func(ns, tail string) string { return "namespaces/" + ns + "/pulumi/stk-testx/" + tail },
			where: scopeaudit.OutsideUnitDirectory,
		},
	}
}

// putForeignCheckpoint stores a checkpoint row for the running update s whose
// blob key is key, as a row of a damaged or hostile database would be.
func putForeignCheckpoint(t *testing.T, f updatesFixture, s stack.Started, key string, seq int64) {
	t.Helper()

	row := stack.Checkpoint{
		NamespaceID: "", StackID: "",
		SavedAt:       f.clock.Now(),
		Features:      nil,
		Deployment:    nil,
		Update:        s.Update.ID,
		Ref:           f.ref,
		Lease:         s.Update.Lease.Digest,
		SchemaVersion: 3,
		Sequence:      seq,
		Digest:        sha256.Sum256([]byte("body")),
		Invalid:       false,
		BlobKey:       blob.Key(key),
		BlobSize:      1,
		BlobMD5:       [16]byte{},

		AttemptStartedAt: f.clock.Now(), Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

	if _, err := f.store.PutCheckpoint(t.Context(), row, fence); err != nil {
		t.Fatalf("store the checkpoint row: %v", err)
	}
}

// blobKeysOf returns the keys of all objects in store, in the order of the key.
func blobKeysOf(t *testing.T, store blob.Store) []string {
	t.Helper()

	all, err := blobtest.ListAll(t.Context(), store, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	keys := make([]string, 0, len(all))

	for _, obj := range all {
		keys = append(keys, obj.Key.String())
	}

	return keys
}

// TestStackBlobsLiveUnderTheNamespacePrefix saves each kind of checkpoint and
// checks the layout: every blob key is
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>-<attempt ID>,
// and the blob store holds no key outside the prefix of the namespace.
func TestStackBlobsLiveUnderTheNamespacePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tail  string
		saves []int64
	}{
		{"one sequenced save", "1", []int64{1}},
		{"a sequenced save replaces the one before", "2", []int64{1, 2}},
		{"an unsequenced save", "0", []int64{0}},
		{"unsequenced saves keep one blob", "0", []int64{0, 0}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			s := f.started(t, stack.KindUpdate)

			for i, seq := range tc.saves {
				in := checkpoint(seq, envelope(`{"n":`+strconv.Itoa(i)+`}`))
				if err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, in); err != nil {
					t.Fatalf("save %d: %v", i, err)
				}
			}

			wantAttemptKey(t, blobKeysOf(t, f.blobs), f.prefix+"stk-test/"+s.Update.ID.String()+"/"+tc.tail+"-")
		})
	}
}

// attemptIDLength is the length of the attempt ID that a save writes in its key.
const attemptIDLength = 20

// wantAttemptKey fails the test unless keys holds one key that is head
// followed by an attempt ID.
func wantAttemptKey(t *testing.T, keys []string, head string) {
	t.Helper()

	if len(keys) != 1 || !strings.HasPrefix(keys[0], head) ||
		len(strings.TrimPrefix(keys[0], head)) != attemptIDLength {
		t.Fatalf("blob keys = %v, want one key that starts with %s and ends in an attempt ID", keys, head)
	}
}

// checkpointRead is one read of a checkpoint blob through the update service.
type checkpointRead func(t *testing.T, f updatesFixture, s stack.Started) error

// checkpointReads are the reads of a checkpoint blob: the export of the
// latest state, the export of a version, the restore of a version, and the
// delta save that reads the previous checkpoint.
func checkpointReads() map[string]checkpointRead {
	return map[string]checkpointRead{
		"export": func(t *testing.T, f updatesFixture, _ stack.Started) error {
			t.Helper()

			_, err := f.svc.Export(t.Context(), f.ref)

			return err
		},
		"export of a version": func(t *testing.T, f updatesFixture, s stack.Started) error {
			t.Helper()

			_, err := f.svc.ExportVersion(t.Context(), f.ref, s.Update.Version)

			return err
		},
		"restore of a version": func(t *testing.T, f updatesFixture, s stack.Started) error {
			t.Helper()

			_, err := f.svc.Restore(t.Context(), f.ref, s.Update.Version, "admin")

			return err
		},
		"checkpoint delta": func(t *testing.T, f updatesFixture, s stack.Started) error {
			t.Helper()

			in := stack.DeltaInput{SchemaVersion: 3, Sequence: 6, Hash: strings.Repeat("0", 64), Delta: []byte(`[]`)}

			return f.svc.SaveCheckpointDelta(t.Context(), f.ref, s.Update.ID, s.Token, in)
		},
	}
}

// checkRefusedRead runs read on a fixture whose checkpoint row names the key
// that tc builds. It checks the refusal, that no call reached the blob store,
// and that the audit log got one refusal in the namespace of the call.
func checkRefusedRead(t *testing.T, read checkpointRead, tc foreignKey) {
	t.Helper()

	counted := newCountingBlob()
	f := newUpdatesWith(t, newClock(), counted, nil)
	s := f.started(t, stack.KindUpdate)

	putForeignCheckpoint(t, f, s, tc.build(f.nsID.String(), s.Update.ID.String()+"/5"), 5)

	before := counted.calls()

	err := read(t, f, s)
	if !errors.Is(err, blob.ErrOutOfScope) {
		t.Fatalf("err = %v, want blob.ErrOutOfScope", err)
	}

	if strings.Contains(err.Error(), "ns-other") {
		t.Fatalf("error holds the other namespace: %v", err)
	}

	if got := counted.calls() - before; got != 0 {
		t.Fatalf("the read reached the blob store %d times, want 0", got)
	}

	recs := f.refusals.Records()
	if len(recs) != 1 {
		t.Fatalf("refusals = %+v, want one", recs)
	}

	if recs[0].Namespace != f.nsID || recs[0].Op != "get" || recs[0].Where != tc.where {
		t.Fatalf("refusal = %+v, want a get in %s with the place %q", recs[0], f.nsID, tc.where)
	}
}

// TestCheckpointReadsRefuseKeysOutsideTheScope is the blob scope test: each
// read of a checkpoint blob, with a key of another namespace in the row,
// returns blob.ErrOutOfScope. No call reaches the blob store, and the audit
// log gets one refusal for the namespace of the call.
func TestCheckpointReadsRefuseKeysOutsideTheScope(t *testing.T) {
	t.Parallel()

	for readName, read := range checkpointReads() {
		for _, tc := range foreignKeys() {
			t.Run(readName+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkRefusedRead(t, read, tc)
			})
		}
	}
}

// journalForeignKey is a journal blob key that a row of the journal tests'
// namespace must never name.
type journalForeignKey struct {
	key   blob.Key
	where scopeaudit.Where
}

// journalForeignKeys are the journal keys that the blob scope refuses, with
// the place that the refusal records.
func journalForeignKeys() map[string]journalForeignKey {
	return map[string]journalForeignKey{
		"another namespace": {"namespaces/ns-other/pulumi/stack-1/upd-1/journal/1", scopeaudit.OutsideNamespace},
		"old layout":        {"pulumi/stack-1/upd-1/journal/1", scopeaudit.OutsideNamespace},
		"id as a prefix": {
			"namespaces/ns-journal-testx/pulumi/stack-1/upd-1/journal/1", scopeaudit.OutsideNamespace,
		},
		"own tofu directory": {
			"namespaces/ns-journal-test/tofu/stack-1/upd-1/journal/1", scopeaudit.OutsideDirectory,
		},
		"same namespace, another stack": {
			"namespaces/ns-journal-test/pulumi/stack-2/upd-1/journal/1", scopeaudit.OutsideUnitDirectory,
		},
		"same namespace, stack ID as a prefix": {
			"namespaces/ns-journal-test/pulumi/stack-10/upd-1/journal/1", scopeaudit.OutsideUnitDirectory,
		},
		"same stack, another update": {
			"namespaces/ns-journal-test/pulumi/stack-1/upd-2/journal/1", scopeaudit.OutsideUnitDirectory,
		},
		"same stack, checkpoint of the update": {
			"namespaces/ns-journal-test/pulumi/stack-1/upd-1/1", scopeaudit.OutsideUnitDirectory,
		},
	}
}

// journalBlobOp is one operation of the journal writer that takes a blob key
// from a row, with the name that the refusal records for it.
type journalBlobOp struct {
	run func(ctx context.Context, e *stack.JournalEntries, key blob.Key) error
	op  string
}

// journalBlobOps are the operations of the journal writer that touch a blob
// with a key from a row: the read of the batches and the delete after a build.
func journalBlobOps() map[string]journalBlobOp {
	return map[string]journalBlobOp{
		"batches": {op: "get", run: func(ctx context.Context, e *stack.JournalEntries, _ blob.Key) error {
			_, err := e.Batches(ctx, journalNS, "stack-1", "upd-1")

			return err
		}},
		"remove": {op: "delete", run: func(ctx context.Context, e *stack.JournalEntries, key blob.Key) error {
			return e.RemoveBlobs(ctx, journalNS, "stack-1", "upd-1", []blob.Key{key})
		}},
	}
}

// TestJournalBlobReadsRefuseKeysOutsideTheScope is the same test for the
// journal batches: the read of the batches, and the delete of the blobs after
// a build, refuse a key outside the scope of the namespace of the call.
func TestJournalBlobReadsRefuseKeysOutsideTheScope(t *testing.T) {
	t.Parallel()

	for opName, op := range journalBlobOps() {
		for keyName, tc := range journalForeignKeys() {
			t.Run(opName+"/"+keyName, func(t *testing.T) {
				t.Parallel()

				checkRefusedJournalOp(t, op, tc)
			})
		}
	}
}

// checkRefusedJournalOp runs op with the row key of tc and checks the refusal,
// that no call reached the blob store, and the one audit record.
func checkRefusedJournalOp(t *testing.T, op journalBlobOp, tc journalForeignKey) {
	t.Helper()

	blobs := newFakeBlobs()
	refusals := stacktest.NewRefusals()
	rows := newFakeRows()
	rows.keys = []blob.Key{tc.key}
	rows.rows = []stack.JournalRow{rowNaming(tc.key, [sha256.Size]byte{}, 0)}

	entries, err := stack.NewJournalEntries(blobs, rows, allowAll{}, 0, refusals)
	if err != nil {
		t.Fatalf("new journal writer: %v", err)
	}

	if runErr := op.run(t.Context(), entries, tc.key); !errors.Is(runErr, blob.ErrOutOfScope) {
		t.Fatalf("err = %v, want blob.ErrOutOfScope", runErr)
	}

	if len(blobs.calls) != 0 {
		t.Fatalf("blob store calls = %v, want none", blobs.calls)
	}

	recs := refusals.Records()
	if len(recs) != 1 || recs[0].Namespace != journalNS || recs[0].Op != op.op || recs[0].Where != tc.where {
		t.Fatalf("refusals = %+v, want one %s in %s with the place %q", recs, op.op, journalNS, tc.where)
	}
}

// TestJournalWriterRefusesAnInvalidNamespace checks the boundary of the
// writer: a batch, a read and a remove with a namespace ID that cannot be part
// of a key fail before they reach the blob store.
func TestJournalWriterRefusesAnInvalidNamespace(t *testing.T) {
	t.Parallel()

	body := journalBatch(beginEntry(1, 1))
	for _, id := range []namespace.ID{"", "acme", "ns-", "ns-A", "ns-a/b"} {
		blobs := newFakeBlobs()
		entries := newEntries(t, blobs, newFakeRows(), allowAll{})
		batch := journalBatchFor(body)
		batch.Namespace = id

		if err := entries.Append(t.Context(), batch, fence()); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("append with the namespace %q: err = %v, want blob.ErrInvalidKey", id, err)
		}

		if _, err := entries.Batches(t.Context(), id, "stack-1", "upd-1"); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("batches with the namespace %q: err = %v, want blob.ErrInvalidKey", id, err)
		}

		if err := entries.RemoveBlobs(t.Context(), id, "stack-1", "upd-1", nil); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("remove with the namespace %q: err = %v, want blob.ErrInvalidKey", id, err)
		}

		if len(blobs.calls) != 0 {
			t.Errorf("namespace %q: blob store calls = %v, want none", id, blobs.calls)
		}
	}
}

// TestJournalAppendWritesUnderTheNamespacePrefix checks the key of a batch and
// that the writer takes the namespace of the batch for the scope.
func TestJournalAppendWritesUnderTheNamespacePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ns   namespace.ID
		want string
	}{
		{"the namespace of the tests", journalNS, "namespaces/ns-journal-test/pulumi/stack-1/upd-1/journal/1-"},
		{"another namespace", "ns-second", "namespaces/ns-second/pulumi/stack-1/upd-1/journal/1-"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			blobs := newFakeBlobs()
			rows := newFakeRows()
			entries := newEntries(t, blobs, rows, allowAll{})
			batch := journalBatchFor(journalBatch(beginEntry(1, 1)))
			batch.Namespace = tc.ns

			if err := entries.Append(t.Context(), batch, fence()); err != nil {
				t.Fatalf("append: %v", err)
			}

			if len(rows.rows) != 1 || !hasAttemptKey(rows.rows[0].BlobKey, tc.want) {
				t.Fatalf("rows = %+v, want one row with a key that starts with %q", rows.rows, tc.want)
			}

			if _, ok := blobs.objects[rows.rows[0].BlobKey]; !ok {
				t.Fatalf("blob store holds %v, want the key of the row", blobs.objects)
			}
		})
	}
}

// updatesConfigFor returns the configuration of the update service over the
// stores of f with the given blob store and recorder.
func updatesConfigFor(f updatesFixture, blobs blob.Store, refusals scopeaudit.Recorder) stack.UpdatesConfig {
	return stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		Updates:            f.store,
		Checkpoints:        f.store,
		Events:             f.store,
		Namespaces:         f.namespaces,
		Tokens:             nil,
		Stacks:             f.store,
		Blobs:              blobs,
		Refusals:           refusals,
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
		ManualLocks:        nil,
	}
}

// TestNewUpdatesNeedsTheBlobDependencies checks that the update service and
// the journal writer refuse to start without a blob store or a recorder of
// refusals, so no blob access exists without the audit trail.
func TestNewUpdatesNeedsTheBlobDependencies(t *testing.T) {
	t.Parallel()

	refusals := stacktest.NewRefusals()

	if _, err := stack.NewJournalEntries(nil, newFakeRows(), allowAll{}, 0, refusals); !errors.Is(
		err, stack.ErrMissingDependency,
	) {
		t.Errorf("journal writer without a blob store: err = %v, want ErrMissingDependency", err)
	}

	if _, err := stack.NewJournalEntries(newFakeBlobs(), newFakeRows(), allowAll{}, 0, nil); !errors.Is(
		err, stack.ErrMissingDependency,
	) {
		t.Errorf("journal writer without a recorder: err = %v, want ErrMissingDependency", err)
	}

	f := newUpdates(t)

	for name, cfg := range map[string]stack.UpdatesConfig{
		"no blob store": updatesConfigFor(f, nil, refusals),
		"no recorder":   updatesConfigFor(f, f.blobs, nil),
	} {
		if _, err := stack.NewUpdates(cfg); !errors.Is(err, stack.ErrMissingDependency) {
			t.Errorf("update service with %s: err = %v, want ErrMissingDependency", name, err)
		}
	}
}

// putAll writes a small blob under each key.
func putAll(t *testing.T, store blob.Store, keys []string) {
	t.Helper()

	for _, key := range keys {
		if err := store.Put(t.Context(), blob.Key(key), strings.NewReader("x")); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
}

// TestDeleteStackBlobsStaysInTheNamespace checks the purge: it removes every
// blob of the stack in the namespace of the call, checkpoints and journal
// batches, and it leaves a blob of the same stack ID in another namespace,
// and a blob of another stack.
func TestDeleteStackBlobsStaysInTheNamespace(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"a":1}`))); err != nil {
		t.Fatalf("save: %v", err)
	}

	keep := []string{
		"namespaces/ns-other/pulumi/stk-test/" + s.Update.ID.String() + "/1",
		f.prefix + "stk-test-two/" + s.Update.ID.String() + "/1",
		"namespaces/" + f.nsID.String() + "/tofu/stk-test/ws/1",
	}

	putAll(t, f.blobs, append([]string{f.prefix + "stk-test/" + s.Update.ID.String() + "/journal/1"}, keep...))

	if err := f.svc.DeleteStackBlobs(ctx, f.nsID, "stk-test"); err != nil {
		t.Fatalf("delete stack blobs: %v", err)
	}

	if got := blobKeysOf(t, f.blobs); len(got) != len(keep) {
		t.Fatalf("blobs left = %v, want only %v", got, keep)
	}

	if len(f.refusals.Records()) != 0 {
		t.Fatalf("refusals = %+v, want none", f.refusals.Records())
	}
}

// TestDeleteStackBlobsRefusesAnInvalidScope checks that the purge with an
// empty namespace ID, as the join of a blob purge record with a missing
// namespace row gives, or with a stack ID that is not one segment, fails and
// deletes nothing.
func TestDeleteStackBlobsRefusesAnInvalidScope(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	putAll(t, f.blobs, []string{f.prefix + "stk-test/upd-1/1"})

	if err := f.svc.DeleteStackBlobs(t.Context(), "", "stk-test"); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("delete with an empty namespace ID: err = %v, want blob.ErrInvalidKey", err)
	}

	if err := f.svc.DeleteStackBlobs(t.Context(), f.nsID, ""); !errors.Is(err, stack.ErrInvalidScope) {
		t.Fatalf("delete with an empty stack ID: err = %v, want ErrInvalidScope", err)
	}

	if got := blobKeysOf(t, f.blobs); len(got) != 1 {
		t.Fatalf("blobs left = %v, want the one blob", got)
	}
}

// hostileBase is a checkpoint store whose rows for the base of a journaled
// update name the blob key *key, as a damaged or hostile database would.
type hostileBase struct {
	stack.CheckpointStore

	key *blob.Key
}

// CheckpointByVersion implements stack.CheckpointStore.
func (h hostileBase) CheckpointByVersion(ctx context.Context, ref stack.Ref, version int64) (stack.Checkpoint, error) {
	cp, err := h.CheckpointStore.CheckpointByVersion(ctx, ref, version)
	if err != nil {
		return cp, err
	}

	cp.BlobKey = *h.key

	return cp, nil
}

// LatestCheckpoint implements stack.CheckpointStore.
func (h hostileBase) LatestCheckpoint(ctx context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	cp, err := h.CheckpointStore.LatestCheckpoint(ctx, ref)
	if err != nil {
		return cp, err
	}

	cp.BlobKey = *h.key

	return cp, nil
}

// TestJournalBaseRefusesKeysOutsideTheScope is the blob scope test for the
// base read of a journaled update (journalBase, then resolveCheckpoint): a
// base checkpoint row that names a key outside the scope gives
// blob.ErrOutOfScope and one refusal in the namespace of the call, and the
// object at that key is never read.
func TestJournalBaseRefusesKeysOutsideTheScope(t *testing.T) {
	t.Parallel()

	for _, tc := range foreignKeys() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var key blob.Key

			clk := newClock()
			f := newBuildFixtureWith(t, clk, pulumijournal.NewReplayer(clk.Now, ""),
				func(s *journalMemStore) stack.CheckpointStore { return hostileBase{CheckpointStore: s, key: &key} })

			// The cases name the stack stk-test. The fixture stack has
			// another ID, so name that one: the cases keep their meaning.
			built := tc.build(f.nsID.String(), "upd-x/1")
			key = blob.Key(strings.ReplaceAll(built, "/stk-test/", "/"+f.stackID.String()+"/"))
			if err := f.blobs.Put(t.Context(), key, strings.NewReader("planted")); err != nil &&
				!errors.Is(err, blob.ErrOutOfScope) {
				t.Fatalf("plant the object: %v", err)
			}

			f.startJournaled(t)

			_, err := f.svc.Export(t.Context(), f.ref)
			if !errors.Is(err, blob.ErrOutOfScope) {
				t.Fatalf("err = %v, want blob.ErrOutOfScope", err)
			}

			if strings.Contains(err.Error(), "ns-other") {
				t.Fatalf("error holds the other namespace: %v", err)
			}

			recs := f.refusals.Records()
			if len(recs) != 1 || recs[0].Namespace != f.nsID || recs[0].Op != "get" || recs[0].Where != tc.where {
				t.Fatalf("refusals = %+v, want one get in %s with the place %q", recs, f.nsID, tc.where)
			}
		})
	}
}
