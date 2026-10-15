package stack_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// journalMemStore implements the journal and built-checkpoint ports in
// memory, over an embedded memUpdates. Journal rows keep arrival order;
// PutBuiltCheckpoint upserts the checkpoint row and removes the journal of
// the update in the same locked section, returning the removed blob keys.
type journalMemStore struct {
	*memUpdates

	journal map[stack.UpdateID][]stack.JournalRow
	seqs    map[stack.UpdateID]map[int64][sha256.Size]byte
	closed  map[stack.UpdateID]bool
}

func newJournalMemStore() *journalMemStore {
	return &journalMemStore{
		memUpdates: newMemUpdates(),
		journal:    map[stack.UpdateID][]stack.JournalRow{},
		seqs:       map[stack.UpdateID]map[int64][sha256.Size]byte{},
		closed:     map[stack.UpdateID]bool{},
	}
}

// PutJournalRow implements stack.JournalStore.
func (m *journalMemStore) PutJournalRow(
	_ context.Context, row stack.JournalRow, fence namespace.Fence,
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkJournalWrite(row, fence); err != nil {
		return false, err
	}

	return m.storeJournalRow(row)
}

// JournalKeys implements stack.JournalStore.
func (m *journalMemStore) JournalKeys(
	_ context.Context, _ stack.ID, update stack.UpdateID,
) ([]blob.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rows := m.journal[update]
	out := make([]blob.Key, 0, len(rows))

	for i := range rows {
		out = append(out, rows[i].BlobKey)
	}

	return out, nil
}

// JournalSweepable implements stack.JournalStore.
func (m *journalMemStore) JournalSweepable(
	_ context.Context, _ stack.ID, update stack.UpdateID,
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.updates[update]
	if !ok {
		return false, stack.ErrUpdateNotFound
	}

	return m.closed[update] || u.Status != stack.StatusRunning, nil
}

// PutBuiltCheckpoint implements stack.BuiltCheckpointStore.
func (m *journalMemStore) PutBuiltCheckpoint(
	_ context.Context, cp stack.Checkpoint, fence namespace.Fence,
) ([]blob.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(cp.Ref, fence); err != nil {
		return nil, err
	}

	if !m.runningUnder(cp.Update, cp.Lease) {
		return nil, stack.ErrWrongUpdateState
	}

	if !m.updates[cp.Update].Mutates() {
		return nil, stack.ErrWrongUpdateState
	}

	m.checkpoints[cp.Update] = cp

	rows := m.journal[cp.Update]
	keys := make([]blob.Key, 0, len(rows))

	for i := range rows {
		keys = append(keys, rows[i].BlobKey)
	}

	delete(m.journal, cp.Update)
	delete(m.seqs, cp.Update)
	m.closed[cp.Update] = true

	return keys, nil
}

// checkJournalWrite runs the fence, lease, kind and closed checks of a
// batch. The caller holds the lock.
func (m *journalMemStore) checkJournalWrite(row stack.JournalRow, fence namespace.Fence) error {
	if err := m.checkFence(row.Ref, fence); err != nil {
		return err
	}

	if !m.runningUnder(row.Update, row.Lease) {
		return stack.ErrWrongUpdateState
	}

	if !m.updates[row.Update].Mutates() {
		return stack.ErrWrongUpdateState
	}

	if m.closed[row.Update] {
		return stack.ErrWrongUpdateState
	}

	return nil
}

// storeJournalRow answers a retry without a write: every sequence ID stored
// with the same content. A sequence stored with other content, or only some
// of them stored, is a conflict. Otherwise it verifies the row against the
// stored operations and keeps it. The caller holds the lock.
func (m *journalMemStore) storeJournalRow(row stack.JournalRow) (bool, error) {
	stored := m.seqs[row.Update]
	if stored == nil {
		stored = map[int64][sha256.Size]byte{}
		m.seqs[row.Update] = stored
	}

	seen := 0

	for i := range row.SequenceIDs {
		sum, ok := stored[row.SequenceIDs[i]]
		if !ok {
			continue
		}

		if sum != row.SHA256 {
			return false, stack.ErrJournalConflict
		}

		seen++
	}

	switch {
	case seen == 0:
		// A new batch: verified and stored below.
	case seen == len(row.SequenceIDs):
		return true, nil
	default:
		return false, stack.ErrJournalConflict
	}

	if row.Verify != nil {
		var known []int64
		for i := range m.journal[row.Update] {
			known = append(known, m.journal[row.Update][i].OperationIDs...)
		}

		if err := row.Verify(known); err != nil {
			return false, err
		}
	}

	m.journal[row.Update] = append(m.journal[row.Update], row)

	for i := range row.SequenceIDs {
		stored[row.SequenceIDs[i]] = row.SHA256
	}

	return false, nil
}

// buildFixture is an Updates service with a journal writer and a replayer,
// over one stack with one finished update that holds the base checkpoint.
type buildFixture struct {
	svc     *stack.Updates
	store   *journalMemStore
	blobs   *memBlob
	clock   *clock
	ref     stack.Ref
	stackID stack.ID
	journal *stack.JournalEntries
	base    []byte
}

// baseResource is the one resource of the base checkpoint.
const baseResource = `{"urn":"urn:pulumi:dev::web::t:mod:T::a","custom":true,"type":"t:mod:T","id":"a"}`

// newResource is the resource the journal batches add.
const newResource = `{"urn":"urn:pulumi:dev::web::t:mod:T::b","custom":true,"type":"t:mod:T","id":"b"}`

// baseDeploymentBytes is the deployment of the finished update the journaled
// update starts from.
func baseDeploymentBytes() []byte {
	return []byte(`{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},` +
		`"resources":[` + baseResource + `]}`)
}

// journalBatch returns a batch body with the given entries.
func journalBatch(entries ...string) []byte {
	var b bytes.Buffer
	b.WriteString(`{"entries":[`)
	for i, e := range entries {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(e)
	}
	b.WriteString(`]}`)

	return b.Bytes()
}

// beginEntry starts operation op with sequence seq.
func beginEntry(seq, op int64) string {
	return `{"version":1,"kind":0,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":` + strconv.FormatInt(op, 10) + `,"removeOld":null,"removeNew":null}`
}

// successEntry adds state for operation op with sequence seq.
func successEntry(seq, op int64, state string) string {
	return `{"version":1,"kind":1,"sequenceID":` + strconv.FormatInt(seq, 10) +
		`,"operationID":` + strconv.FormatInt(op, 10) +
		`,"removeOld":null,"removeNew":null,"state":` + state + `}`
}

// newBuildFixture finishes one update with the base checkpoint and returns
// the fixture. The service builds with the real replayer over clk, so a
// direct replay with the same clock gives the same bytes.
func newBuildFixture(t *testing.T, clk *clock, replayer stack.JournalReplayer) *buildFixture {
	t.Helper()

	store := newJournalMemStore()
	blobs := newMemBlob(clk)

	ns := namespace.Namespace{
		CreatedAt:       clk.now,
		Name:            "acme",
		HomeRegion:      "local",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: &memNamespaces{ns: ns}, Now: clk.Now, Region: "local"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	journal, err := stack.NewJournalEntries(blobs, store, pulumijournal.Verifier{})
	if err != nil {
		t.Fatalf("journal writer: %v", err)
	}

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            store,
		Checkpoints:        store,
		Events:             store,
		Namespaces:         namespaces,
		Stacks:             store,
		Blobs:              blobs,
		Tokens:             nil,
		Now:                clk.Now,
		Logger:             nil,
		MaxCheckpointBytes: 1 << 20,
		Journal:            journal,
		Replayer:           replayer,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	store.addStack(ref, "stk-build")

	ctx := t.Context()

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0}

	first, createErr := svc.Create(ctx, ref, plan)
	if createErr != nil {
		t.Fatalf("create base update: %v", createErr)
	}

	started, startErr := svc.Start(ctx, ref, first.ID)
	if startErr != nil {
		t.Fatalf("start base update: %v", startErr)
	}

	base := baseDeploymentBytes()
	in := stack.CheckpointInput{
		Features:      nil,
		Deployment:    base,
		SchemaVersion: 3,
		Sequence:      0,
		Invalid:       false,
	}

	if saveErr := svc.SaveCheckpoint(ctx, ref, first.ID, started.Token, in); saveErr != nil {
		t.Fatalf("save base checkpoint: %v", saveErr)
	}

	if _, completeErr := svc.Complete(ctx, ref, first.ID, started.Token, stack.StatusSucceeded); completeErr != nil {
		t.Fatalf("complete base update: %v", completeErr)
	}

	got, getErr := store.GetStack(ctx, ref)
	if getErr != nil {
		t.Fatalf("get stack: %v", getErr)
	}

	return &buildFixture{
		svc: svc, store: store, blobs: blobs, clock: clk,
		ref: ref, stackID: got.ID, journal: journal, base: base,
	}
}

// startJournaled starts a journaled update and appends two batches that add
// one resource. It returns the started update.
func (f *buildFixture) startJournaled(t *testing.T) stack.Started {
	t.Helper()

	ctx := t.Context()

	plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0}

	u, err := f.svc.Create(ctx, f.ref, plan)
	if err != nil {
		t.Fatalf("create journaled update: %v", err)
	}

	s, err := f.svc.StartWithJournalVersion(ctx, f.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start journaled update: %v", err)
	}

	batches := [][]byte{
		journalBatch(beginEntry(1, 1)),
		journalBatch(successEntry(2, 1, newResource)),
	}

	for _, body := range batches {
		if err := f.svc.AppendJournal(ctx, f.ref, s.Update.ID, s.Token, body); err != nil {
			t.Fatalf("append journal batch: %v", err)
		}
	}

	return s
}

// replay runs the real replayer on the base and the two batches the fixture
// appends, with the fixture clock and an empty CLI version like the service.
// It returns the inner deployment the service stores.
func (f *buildFixture) replay(t *testing.T) []byte {
	t.Helper()

	combined := journalBatch(beginEntry(1, 1), successEntry(2, 1, newResource))

	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(f.base)), nil
	}

	var out bytes.Buffer

	replayer := pulumijournal.NewReplayer(f.clock.Now, "")
	replayErr := replayer.Replay(t.Context(), open, bytes.NewReader(combined), &out)
	if replayErr != nil {
		t.Fatalf("replay: %v", replayErr)
	}

	var doc struct {
		Deployment json.RawMessage `json:"deployment"`
	}

	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("decode replay output: %v", err)
	}

	return []byte(doc.Deployment)
}

// journalKeys returns the stored journal blob keys of the update.
func (f *buildFixture) journalKeys(t *testing.T, id stack.UpdateID) []blob.Key {
	t.Helper()

	keys, err := f.store.JournalKeys(t.Context(), f.stackID, id)
	if err != nil {
		t.Fatalf("journal keys: %v", err)
	}

	return keys
}

// TestJournalBuildCompleteStoresReplay checks that completing a journaled
// update stores the replay of its batches as its checkpoint.
func TestJournalBuildCompleteStoresReplay(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete journaled update: %v", err)
	}

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export after complete: %v", err)
	}

	if want := f.replay(t); !bytes.Equal(cp.Deployment, want) {
		t.Fatalf("export after complete = %s, want replay %s", cp.Deployment, want)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 0 {
		t.Fatalf("journal keys after complete = %v, want none", keys)
	}
}

// TestJournalBuildCancelStoresReplay checks that canceling a journaled
// update builds and stores its checkpoint first.
func TestJournalBuildCancelStoresReplay(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	done, err := f.svc.Cancel(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("cancel journaled update: %v", err)
	}

	if done.Status != stack.StatusCanceled {
		t.Fatalf("cancel status = %q, want %q", done.Status, stack.StatusCanceled)
	}

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export after cancel: %v", err)
	}

	if want := f.replay(t); !bytes.Equal(cp.Deployment, want) {
		t.Fatalf("export after cancel = %s, want replay %s", cp.Deployment, want)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 0 {
		t.Fatalf("journal keys after cancel = %v, want none", keys)
	}
}

// TestJournalBuildReadDuringUpdate checks that a read while the journaled
// update runs returns the built state without storing it: the journal stays
// until the update ends.
func TestJournalBuildReadDuringUpdate(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("export during update: %v", err)
	}

	if want := f.replay(t); !bytes.Equal(cp.Deployment, want) {
		t.Fatalf("export during update = %s, want replay %s", cp.Deployment, want)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 2 {
		t.Fatalf("journal keys during update = %v, want the two batches", keys)
	}

	u, err := f.svc.Get(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("get update during read: %v", err)
	}

	if u.Status != stack.StatusRunning {
		t.Fatalf("update status = %q, want running", u.Status)
	}

	versioned, err := f.svc.ExportVersion(t.Context(), f.ref, u.Version)
	if err != nil {
		t.Fatalf("export version %d during update: %v", u.Version, err)
	}

	if want := f.replay(t); !bytes.Equal(versioned.Deployment, want) {
		t.Fatalf("export version %d during update = %s, want replay %s", u.Version, versioned.Deployment, want)
	}

	previous, err := f.svc.ExportVersion(t.Context(), f.ref, u.Version-1)
	if err != nil {
		t.Fatalf("export version %d during update: %v", u.Version-1, err)
	}

	if !bytes.Equal(previous.Deployment, f.base) {
		t.Fatalf("export version %d during update = %s, want base %s", u.Version-1, previous.Deployment, f.base)
	}
}

// TestJournalBuildDeletesBlobs checks that the journal blobs are gone after
// the build stored the checkpoint.
func TestJournalBuildDeletesBlobs(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	before := f.journalKeys(t, s.Update.ID)
	if len(before) != 2 {
		t.Fatalf("journal keys before complete = %v, want two", before)
	}

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete journaled update: %v", err)
	}

	for _, key := range before {
		if _, err := f.blobs.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("journal blob %s: err = %v, want ErrNotFound", key, err)
		}
	}

	cp, err := f.store.CheckpointByUpdate(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("built checkpoint: %v", err)
	}

	if cp.BlobKey == "" {
		t.Fatal("built checkpoint has no blob key")
	}

	if _, err := f.blobs.Get(t.Context(), cp.BlobKey); err != nil {
		t.Fatalf("checkpoint blob: %v, want the built checkpoint", err)
	}
}

// failReplayer is a JournalReplayer that always fails.
type failReplayer struct{ err error }

func (f failReplayer) Replay(
	_ context.Context, _ func(context.Context) (io.ReadCloser, error), _ io.Reader, _ io.Writer,
) error {
	return f.err
}

// TestJournalBuildFailureKeepsEntries checks that a failed build ends
// nothing and keeps the journal for the next try.
func TestJournalBuildFailureKeepsEntries(t *testing.T) {
	t.Parallel()

	f := newBuildFixture(t, newClock(), failReplayer{err: io.ErrUnexpectedEOF})

	s := f.startJournaled(t)

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err == nil {
		t.Fatal("complete with a failed build: err = nil, want the build error")
	} else if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("complete with a failed build: err = %v, want the build error", err)
	}

	u, err := f.svc.Get(t.Context(), f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("get update after failed build: %v", err)
	}

	if u.Status != stack.StatusRunning {
		t.Fatalf("update status = %q, want running", u.Status)
	}

	if keys := f.journalKeys(t, s.Update.ID); len(keys) != 2 {
		t.Fatalf("journal keys after failed build = %v, want the two batches", keys)
	}

	if _, err := f.store.CheckpointByUpdate(t.Context(), f.ref, s.Update.ID); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("checkpoint after failed build: err = %v, want ErrNoState", err)
	}
}

// TestJournalBuildBatches checks the batch reader the build replays: the
// bodies arrive in the order they were stored.
func TestJournalBuildBatches(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

	s := f.startJournaled(t)

	bodies, err := f.journal.Batches(t.Context(), f.stackID, s.Update.ID)
	if err != nil {
		t.Fatalf("batches: %v", err)
	}

	want := [][]byte{
		journalBatch(beginEntry(1, 1)),
		journalBatch(successEntry(2, 1, newResource)),
	}

	if len(bodies) != len(want) {
		t.Fatalf("batches = %d bodies, want %d", len(bodies), len(want))
	}

	for i := range want {
		if !bytes.Equal(bodies[i], want[i]) {
			t.Fatalf("batch %d = %s, want %s", i, bodies[i], want[i])
		}
	}
}
