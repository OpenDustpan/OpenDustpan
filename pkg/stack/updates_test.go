package stack_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// memNamespaces is a namespace.Store holding one namespace.
type memNamespaces struct {
	ns namespace.Namespace
	// reads counts the namespace reads, so a test proves that a blob call makes
	// no read beyond the ones it needs for the fence.
	reads atomic.Int64
}

func (m *memNamespaces) CreateNamespace(context.Context, namespace.Namespace) error {
	return namespace.ErrAlreadyExists
}

func (m *memNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.reads.Add(1)

	if name != m.ns.Name {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return m.ns, nil
}

func (m *memNamespaces) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return []namespace.Namespace{m.ns}, nil
}

func (m *memNamespaces) Ping(context.Context) error {
	return nil
}

// memUpdates implements the update, checkpoint and event ports in memory,
// following their documented contracts. It also holds the namespaces and
// stacks the history cases create, so it can check their fences.
type memUpdates struct {
	updates      map[stack.UpdateID]stack.Update
	namespaces   map[namespace.Name]namespace.Namespace
	stacks       map[stack.Ref]stack.Stack
	stackVersion map[stack.Ref]int64
	checkpoints  map[stack.UpdateID]stack.Checkpoint
	events       map[stack.UpdateID][]stack.Event
	mu           sync.Mutex
	// saves counts SaveUpdate calls, so tests prove that a retried end
	// writes nothing.
	saves int
	// cancelOnPut cancels the caller's context and fails PutCheckpoint, as a
	// client hanging up mid-upload would.
	cancelOnPut func()
	// renewFirst makes the next SaveUpdate lose to a simulated lease renewal.
	renewFirst bool
	// dbNow is the clock of the database that DatabaseNow reads. Nil reads
	// the clock of the machine.
	dbNow func() time.Time
	// dbErr, when set, is the error of each read of the clock of the
	// database.
	dbErr error
	// nsID is the ID of the namespace row that the reads of stacks and
	// checkpoints fill, as the SQL stores do from the join of the query.
	nsID namespace.ID
}

func newMemUpdates() *memUpdates {
	return &memUpdates{
		nsID:         "",
		updates:      map[stack.UpdateID]stack.Update{},
		namespaces:   map[namespace.Name]namespace.Namespace{},
		stacks:       map[stack.Ref]stack.Stack{},
		stackVersion: map[stack.Ref]int64{},
		checkpoints:  map[stack.UpdateID]stack.Checkpoint{},
		events:       map[stack.UpdateID][]stack.Event{},
		mu:           sync.Mutex{},
		saves:        0,
		cancelOnPut:  nil,
		renewFirst:   false,
		dbNow:        nil,
		dbErr:        nil,
	}
}

// Ping implements namespace.Store: the in-memory store is always ready.
// DatabaseNow implements blob.DatabaseClock.
func (m *memUpdates) DatabaseNow(context.Context) (time.Time, error) {
	if m.dbErr != nil {
		return time.Time{}, m.dbErr
	}

	if m.dbNow != nil {
		return m.dbNow(), nil
	}

	return time.Now(), nil
}

func (m *memUpdates) Ping(context.Context) error {
	return nil
}

func (m *memUpdates) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *memUpdates) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *memUpdates) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]namespace.Namespace, 0, len(m.namespaces))
	for _, ns := range m.namespaces {
		out = append(out, ns)
	}

	slices.SortFunc(
		out,
		func(a, b namespace.Namespace) int { return strings.Compare(a.Name.String(), b.Name.String()) },
	)

	return out, nil
}

func (m *memUpdates) CreateStack(_ context.Context, st stack.Stack, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[st.Ref.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if fence != ns.Fence() {
		return namespace.ErrFenced
	}

	if _, ok := m.stacks[st.Ref]; ok {
		return stack.ErrAlreadyExists
	}

	m.stacks[st.Ref] = st

	return nil
}

func (m *memUpdates) GetStack(_ context.Context, ref stack.Ref) (stack.Stack, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.stacks[ref]
	if !ok {
		return stack.Stack{}, stack.ErrNotFound
	}

	st.NamespaceID = m.nsID

	return st, nil
}

func (m *memUpdates) CreateUpdate(
	ctx context.Context, u stack.Update, key stack.UpdateKey, fence namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(u.Ref, fence); err != nil {
		return err
	}

	st, ok := m.stacks[u.Ref]
	if !ok {
		return stack.ErrNotFound
	}

	item, err := key.RefFor(u.Ref.Namespace, st.ID, u.ID)
	if err != nil {
		return err
	}

	// The fake has no transaction: a failed key stores no update. A sealer of
	// the tests needs no transaction.
	if _, err := key.NewKey(ctx, nil, item, fence); err != nil {
		return err
	}

	m.updates[u.ID] = u

	return nil
}

func (m *memUpdates) GetUpdate(ctx context.Context, _ stack.Ref, id stack.UpdateID) (stack.Update, error) {
	if err := ctx.Err(); err != nil {
		return stack.Update{}, fmt.Errorf("get update: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.updates[id]
	if !ok {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	return m.withSize(u), nil
}

func (m *memUpdates) StartUpdate(_ context.Context, u stack.Update, fence namespace.Fence) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(u.Ref, fence); err != nil {
		return stack.Update{}, err
	}

	if m.updates[u.ID].Revision != u.Revision-1 {
		return stack.Update{}, stack.ErrConcurrentModification
	}

	var highest int64

	for id := range m.updates {
		other := m.updates[id]
		if !other.Mutates() || other.Ref != u.Ref {
			continue
		}

		if other.Status == stack.StatusRunning && u.Mutates() {
			return stack.Update{}, stack.ErrUpdateConflict
		}

		highest = max(highest, other.Version)
	}

	u.Version = m.stackVersion[u.Ref]
	if u.Mutates() {
		u.Version = highest + 1
	}

	m.updates[u.ID] = u

	return u, nil
}

func (m *memUpdates) SaveUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("save update: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(u.Ref, fence); err != nil {
		return err
	}

	if m.renewFirst {
		m.renewFirst = false
		renewed := m.updates[u.ID]
		renewed.Lease.ExpiresAt = renewed.Lease.ExpiresAt.Add(time.Minute)
		renewed.Revision++
		m.updates[u.ID] = renewed
	}

	if m.updates[u.ID].Revision != u.Revision-1 {
		return stack.ErrConcurrentModification
	}

	if u.Mutates() && u.Status.Terminal() && u.Version > 0 {
		m.stackVersion[u.Ref] = u.Version
	}

	m.updates[u.ID] = u
	m.saves++

	return nil
}

func (m *memUpdates) ActiveUpdate(_ context.Context, ref stack.Ref) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.updates {
		if u := m.updates[id]; u.Ref == ref && u.Mutates() && u.Status == stack.StatusRunning {
			return u, nil
		}
	}

	return stack.Update{}, stack.ErrUpdateNotFound
}

func (m *memUpdates) ListUpdates(_ context.Context, ref stack.Ref, _ stack.Page) ([]stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var list []stack.Update

	for id := range m.updates {
		if u := m.updates[id]; u.Ref == ref && u.Mutates() && u.Status != stack.StatusNotStarted {
			list = append(list, m.withSize(u))
		}
	}

	slices.SortFunc(list, func(a, b stack.Update) int { return int(b.Version - a.Version) })

	return list, nil
}

func (m *memUpdates) PutCheckpoint(_ context.Context, cp stack.Checkpoint, fence namespace.Fence) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(cp.Ref, fence); err != nil {
		return false, err
	}

	if !m.runningUnder(cp.Update, cp.Lease) {
		return false, stack.ErrWrongUpdateState
	}

	if !m.updates[cp.Update].Mutates() {
		return false, stack.ErrWrongUpdateState
	}

	if m.cancelOnPut != nil {
		m.cancelOnPut()

		return false, context.Canceled
	}

	if cp.Sequence > 0 {
		if last, ok := m.checkpoints[cp.Update]; ok {
			switch {
			case cp.Sequence < last.Sequence:
				return false, stack.ErrStaleCheckpoint
			case cp.Sequence == last.Sequence && cp.Digest == last.Digest:
				return true, nil
			case cp.Sequence == last.Sequence:
				return false, stack.ErrCheckpointConflict
			}
		}
	}

	m.checkpoints[cp.Update] = cp

	return false, nil
}

func (m *memUpdates) LatestCheckpoint(_ context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		best    stack.Checkpoint
		found   bool
		highest int64 = -1
	)

	for id := range m.checkpoints {
		cp := m.checkpoints[id]
		if cp.Ref != ref {
			continue
		}

		if v := m.updates[cp.Update].Version; v > highest {
			highest, best, found = v, cp, true
		}
	}

	if !found {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	return m.withScope(best), nil
}

func (m *memUpdates) CheckpointByVersion(
	_ context.Context, ref stack.Ref, version int64,
) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.checkpoints {
		cp := m.checkpoints[id]
		if cp.Ref != ref {
			continue
		}

		if m.updates[cp.Update].Version == version {
			return m.withScope(cp), nil
		}
	}

	return stack.Checkpoint{}, stack.ErrNoState
}

// CheckpointByUpdate implements stack.CheckpointStore.
func (m *memUpdates) CheckpointByUpdate(
	_ context.Context, ref stack.Ref, id stack.UpdateID,
) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp, ok := m.checkpoints[id]
	if !ok || cp.Ref != ref {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	return m.withScope(cp), nil
}

// ReferencedBlobKeys implements stack.CheckpointStore.
func (m *memUpdates) ReferencedBlobKeys(
	_ context.Context, stackID stack.ID,
) ([]blob.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ref, ok := m.refOfLocked(stackID)
	if !ok {
		return nil, stack.ErrNotFound
	}

	var out []blob.Key

	for id := range m.checkpoints {
		if cp := m.checkpoints[id]; cp.Ref == ref && cp.BlobKey != "" {
			out = append(out, cp.BlobKey)
		}
	}

	slices.Sort(out)

	return out, nil
}

func (m *memUpdates) AppendEvents(_ context.Context, batch stack.EventBatch, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.runningUnder(batch.Update, batch.Lease) {
		return stack.ErrWrongUpdateState
	}

	id := batch.Update
	for _, e := range batch.Events {
		if !slices.ContainsFunc(m.events[id], func(have stack.Event) bool { return have.Sequence == e.Sequence }) {
			m.events[id] = append(m.events[id], e)
		}
	}

	return nil
}

func (m *memUpdates) ListEvents(
	_ context.Context, _ stack.Ref, id stack.UpdateID, after int64, limit int,
) ([]stack.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []stack.Event

	for _, e := range m.events[id] {
		if e.Sequence > after && len(out) < limit {
			out = append(out, e)
		}
	}

	return out, nil
}

// clock is a settable time source.
type clock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// memBlob is an in-memory blob.Store. Writes are write-once, as the port
// requires: Put on an existing key fails with ErrExists.
type memBlob struct {
	objects map[blob.Key]memObject
	clock   *clock
	mu      sync.Mutex
}

type memObject struct {
	data       []byte
	modifiedAt time.Time
}

func newMemBlob(clock *clock) *memBlob {
	return &memBlob{objects: map[blob.Key]memObject{}, clock: clock, mu: sync.Mutex{}}
}

func (m *memBlob) Put(_ context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read blob: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.objects[key]; ok {
		return blob.ErrExists
	}

	m.objects[key] = memObject{data: data, modifiedAt: m.clock.Now()}

	return nil
}

func (m *memBlob) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.objects[key]
	if !ok {
		return nil, blob.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(obj.data)), nil
}

func (m *memBlob) Delete(_ context.Context, key blob.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.objects, key)

	return nil
}

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (m *memBlob) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := m.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

func (m *memBlob) Ping(context.Context) error {
	return nil
}

func (m *memBlob) list(_ context.Context, prefix string) ([]blob.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []blob.Object

	for key, obj := range m.objects {
		if strings.HasPrefix(key.String(), prefix) {
			out = append(out, blob.Object{ModifiedAt: obj.modifiedAt, Key: key})
		}
	}

	slices.SortFunc(out, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return out, nil
}

// ageAll moves every stored modification time back by d, so tests prove the
// sweep grace period without advancing the lease clock past the lease.
func (m *memBlob) ageAll(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, obj := range m.objects {
		obj.modifiedAt = obj.modifiedAt.Add(-d)
		m.objects[key] = obj
	}
}

// clearModified removes the modification time of every object, as a store
// that does not give one does.
func (m *memBlob) clearModified() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, obj := range m.objects {
		obj.modifiedAt = time.Time{}
		m.objects[key] = obj
	}
}

type updatesFixture struct {
	nsStore    *memNamespaces
	refusals   *stacktest.Refusals
	prefix     string
	nsID       namespace.ID
	svc        *stack.Updates
	store      *memUpdates
	blobs      blob.Store
	clock      *clock
	namespaces *namespace.Service
	sealer     *secrettest.MemorySealer
	ref        stack.Ref
}

// newClock returns a clock at the fixture start time.
func newClock() *clock {
	return &clock{now: time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC), mu: sync.Mutex{}}
}

func newUpdates(t *testing.T) updatesFixture {
	t.Helper()

	return newUpdatesWith(t, newClock(), nil, nil)
}

// newUpdatesWith builds the fixture around blobs and logger: a nil blob store
// means a fresh in-memory store on clk, and a nil logger discards sweep
// failures.
func newUpdatesWith(t *testing.T, clk *clock, blobs blob.Store, logger *slog.Logger) updatesFixture {
	t.Helper()

	return newUpdatesFull(t, clk, blobs, logger, updatesFixtureOptions{guard: blobguardtest.None{}, wrap: nil})
}

// newUpdatesGuarded is newUpdatesWith with guard as the guard of the blob
// deletes.
func newUpdatesGuarded(
	t *testing.T, clk *clock, blobs blob.Store, logger *slog.Logger, guard blobguard.Watch,
) updatesFixture {
	t.Helper()

	return newUpdatesFull(t, clk, blobs, logger, updatesFixtureOptions{guard: guard, wrap: nil})
}

// newUpdatesWrapped is newUpdatesWith with a wrap of the checkpoint store: a
// nil wrap uses the in-memory store itself.
func newUpdatesWrapped(
	t *testing.T, clk *clock, blobs blob.Store, logger *slog.Logger, wrap func(*memUpdates) stack.CheckpointStore,
) updatesFixture {
	t.Helper()

	return newUpdatesFull(t, clk, blobs, logger, updatesFixtureOptions{guard: blobguardtest.None{}, wrap: wrap})
}

// updatesFixtureOptions holds the parts of the fixture that some tests set:
// the guard of the blob deletes and the wrap of the checkpoint store. A nil
// wrap uses the in-memory store itself.
type updatesFixtureOptions struct {
	guard blobguard.Watch
	wrap  func(*memUpdates) stack.CheckpointStore
}

// newUpdatesFull builds the fixture with the guard and the wrap of opts.
func newUpdatesFull(
	t *testing.T, clk *clock, blobs blob.Store, logger *slog.Logger, opts updatesFixtureOptions,
) updatesFixture {
	t.Helper()

	guard, wrap := opts.guard, opts.wrap

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           clk.now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	nsStore := &memNamespaces{ns: ns, reads: atomic.Int64{}}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: nsStore, Now: clk.Now, Installation: "local"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	store := newMemUpdates()
	// The database runs on the clock of the test, as the service does.
	store.dbNow = clk.Now
	refusals := stacktest.NewRefusals()

	if blobs == nil {
		blobs = newMemBlob(clk)
	}

	var checkpoints stack.CheckpointStore = store
	if wrap != nil {
		checkpoints = wrap(store)
	}

	sealer := secrettest.NewMemorySealer()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             sealer,
		BlobGuard:          guard,
		ManualLocks:        nil,
		Updates:            store,
		Tokens:             nil,
		Checkpoints:        checkpoints,
		Events:             store,
		Namespaces:         namespaces,
		Stacks:             store,
		Blobs:              blobs,
		Now:                clk.Now,
		Logger:             logger,
		MaxCheckpointBytes: 1024,
		Journal:            nil,
		Replayer:           nil,
		Refusals:           refusals,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	store.nsID = ns.ID
	store.addStack(ref, "stk-test")

	return updatesFixture{
		svc: svc, store: store, blobs: blobs, clock: clk, namespaces: namespaces, ref: ref,
		refusals: refusals, nsID: ns.ID, nsStore: nsStore, prefix: "namespaces/" + ns.ID.String() + "/pulumi/",
		sealer: sealer,
	}
}

func (f updatesFixture) started(t *testing.T, kind stack.UpdateKind) stack.Started {
	t.Helper()

	return f.startedPlan(t, stack.Plan{Kind: kind, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""})
}

func (f updatesFixture) startedPlan(t *testing.T, plan stack.Plan) stack.Started {
	t.Helper()

	u, err := f.svc.Create(t.Context(), f.ref, plan)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	s, err := f.svc.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return s
}

func checkpoint(seq int64, body string) stack.CheckpointInput {
	return stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(body),
		SchemaVersion: 3,
		Sequence:      seq,
		Invalid:       false,
	}
}

// envelope wraps inner in the versioned envelope that the CLI sends on a
// sequenced save. The server refuses an envelope with no deployment member.
func envelope(inner string) string {
	return `{"deployment":` + inner + `}`
}

// TestUpdateHistoryConformance runs the staged history cases of ADR 28
// decision 4 on the in-memory store, before the SQL adapters grow the
// matching columns in issues #135 and #136.
func TestUpdateHistoryConformance(t *testing.T) {
	t.Parallel()

	storetest.RunUpdateHistory(t, func(t *testing.T) storetest.HistoryStore {
		t.Helper()

		return newMemUpdates()
	})
}

func TestUpdateLifecycle(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	if s.Update.Status != stack.StatusRunning || s.Update.Version != 1 {
		t.Fatalf("started update: status %q version %d, want running version 1", s.Update.Status, s.Update.Version)
	}

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"a":1}`))); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	event := stack.Event{Timestamp: f.clock.Now(), Payload: []byte(`{}`), Sequence: 1}
	if err := f.svc.AppendEvents(ctx, f.ref, s.Update.ID, s.Token, []stack.Event{event, event}); err != nil {
		t.Fatalf("append events: %v", err)
	}

	done, err := f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if done.Status != stack.StatusSucceeded || done.EndedAt.IsZero() {
		t.Errorf("completed update: status %q ended %v", done.Status, done.EndedAt)
	}

	checkStored(t, f, s.Update.ID)

	checkRetries(t, f, s, done)
}

// checkRetries checks completion retries after s ended as done, and that a
// preview then carries the version the finished update set.
func checkRetries(t *testing.T, f updatesFixture, s stack.Started, done stack.Update) {
	t.Helper()

	ctx := t.Context()

	// The CLI retries completion when a response is lost; the same status succeeds.
	if again, retryErr := f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); retryErr != nil ||
		again.Revision != done.Revision {
		t.Errorf("retried complete: revision %d, %v; want revision %d, nil", again.Revision, retryErr, done.Revision)
	}

	// A retry with a token that never held the lease fails, even for the
	// status the update ended with.
	foreign, tokErr := token.New()
	if tokErr != nil {
		t.Fatalf("new token: %v", tokErr)
	}

	_, foreignErr := f.svc.Complete(ctx, f.ref, s.Update.ID, foreign, stack.StatusSucceeded)
	if !errors.Is(foreignErr, stack.ErrLeaseInvalid) {
		t.Errorf("retried complete with a foreign token: %v, want ErrLeaseInvalid", foreignErr)
	}

	_, err := f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusFailed)
	if !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Errorf("completing with another status: %v, want ErrWrongUpdateState", err)
	}

	// A preview carries the stack's version, which the finished update set.
	if preview := f.started(t, stack.KindPreview); preview.Update.Version != 1 {
		t.Errorf("preview version %d, want 1", preview.Update.Version)
	}

	next := f.started(t, stack.KindUpdate)
	if next.Update.Version != 2 {
		t.Errorf("second update version %d, want 2", next.Update.Version)
	}
}

// checkStored checks what a finished update left behind: its checkpoint as the
// stack's state, and its events with retries stored once.
func checkStored(t *testing.T, f updatesFixture, id stack.UpdateID) {
	t.Helper()

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil || string(cp.Deployment) != `{"a":1}` {
		t.Errorf("export: %q, %v", cp.Deployment, err)
	}

	events, err := f.svc.Events(t.Context(), f.ref, id, 0, 0)
	if err != nil || len(events) != 1 {
		t.Errorf("events: %d, %v; want 1 (retried sequence stored once)", len(events), err)
	}
}

func TestOneMutatingUpdateAtATime(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	running := f.started(t, stack.KindUpdate)

	second, err := f.svc.Create(ctx, f.ref, stack.Plan{
		Kind: stack.KindDestroy, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := f.svc.Start(ctx, f.ref, second.ID); !errors.Is(err, stack.ErrUpdateConflict) {
		t.Fatalf("start while another runs: %v, want ErrUpdateConflict", err)
	}

	// Previews neither block nor wait.
	preview := f.started(t, stack.KindPreview)
	if preview.Update.Version != 0 {
		t.Errorf("preview version %d, want the stack's current version 0", preview.Update.Version)
	}

	if _, err := f.svc.Cancel(ctx, f.ref, running.Update.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if _, err := f.svc.Cancel(ctx, f.ref, running.Update.ID); err != nil {
		t.Errorf("retried cancel: %v", err)
	}

	if _, err := f.svc.Start(ctx, f.ref, second.ID); err != nil {
		t.Errorf("start after cancel: %v", err)
	}
}

// Canceling again with the same final status succeeds without a write: the
// CLI retries cancel when a response is lost.
func TestCancelTwiceWritesOnce(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	done, err := f.svc.Cancel(ctx, f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if done.Status != stack.StatusCanceled {
		t.Fatalf("cancel status = %q, want %q", done.Status, stack.StatusCanceled)
	}

	f.store.mu.Lock()
	saves := f.store.saves
	f.store.mu.Unlock()

	again, err := f.svc.Cancel(ctx, f.ref, s.Update.ID)
	if err != nil {
		t.Fatalf("retried cancel: %v", err)
	}

	if again.Revision != done.Revision {
		t.Errorf("retried cancel revision = %d, want %d", again.Revision, done.Revision)
	}

	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	if f.store.saves != saves {
		t.Errorf("retried cancel wrote %d times, want no write after %d", f.store.saves, saves)
	}
}

// The CLI renews its lease from a background loop; a renewal that wins the
// race must not make a valid completion fail.
func TestCompleteSurvivesAConcurrentRenewal(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)

	f.store.mu.Lock()
	f.store.renewFirst = true
	f.store.mu.Unlock()

	done, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if err != nil || done.Status != stack.StatusSucceeded {
		t.Errorf("complete after a concurrent renewal: %q, %v; want succeeded", done.Status, err)
	}
}

// The CLI runs the preview phase of `pulumi up` as kind "update" with DryRun.
func TestDryRunsDoNotMutate(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	running := f.started(t, stack.KindUpdate)

	dry := f.startedPlan(
		t,
		stack.Plan{Kind: stack.KindUpdate, DryRun: true, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if dry.Update.Mutates() || dry.Update.Version != 0 {
		t.Errorf("dry run: mutates %t version %d, want false 0", dry.Update.Mutates(), dry.Update.Version)
	}

	err := f.svc.SaveCheckpoint(ctx, f.ref, dry.Update.ID, dry.Token, checkpoint(1, envelope("{}")))
	if !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Errorf("dry run checkpoint: %v, want ErrWrongUpdateState", err)
	}

	if _, err = f.svc.Complete(ctx, f.ref, dry.Update.ID, dry.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete dry run: %v", err)
	}

	if _, err = f.svc.Complete(ctx, f.ref, running.Update.ID, running.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete update: %v", err)
	}

	history, err := f.svc.History(ctx, f.ref, stack.Page{Limit: 0, Offset: 0})
	if err != nil || len(history) != 1 {
		t.Errorf("history: %d entries, %v; want 1, without the dry run", len(history), err)
	}
}

func TestLeaseRules(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	other, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	err = f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, other, checkpoint(1, envelope("{}")))
	if !errors.Is(err, stack.ErrLeaseInvalid) {
		t.Errorf("foreign token: %v, want ErrLeaseInvalid", err)
	}

	for _, d := range []time.Duration{0, stack.MaxLease + time.Second} {
		_, renewErr := f.svc.Renew(ctx, f.ref, s.Update.ID, s.Token, d)
		if !errors.Is(renewErr, stack.ErrInvalidUpdate) {
			t.Errorf("renew for %s: %v, want ErrInvalidUpdate", d, renewErr)
		}
	}

	f.clock.Advance(4 * time.Minute)

	if _, err = f.svc.Renew(ctx, f.ref, s.Update.ID, s.Token, 2*time.Minute); err != nil {
		t.Fatalf("renew: %v", err)
	}

	f.clock.Advance(3 * time.Minute)

	err = f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope("{}")))
	if !errors.Is(err, stack.ErrLeaseExpired) {
		t.Errorf("after expiry: %v, want ErrLeaseExpired", err)
	}

	// An expired lease keeps the stack locked until someone cancels.
	if _, err := f.svc.Active(ctx, f.ref); err != nil {
		t.Errorf("active after expiry: %v, want the expired update", err)
	}
}

func TestCheckpointRules(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)
	save := func(in stack.CheckpointInput) error {
		return f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, in)
	}

	tests := []struct {
		want error
		name string
		in   stack.CheckpointInput
	}{
		{name: "first", in: checkpoint(2, envelope(`{"v":2}`)), want: nil},
		{name: "retried", in: checkpoint(2, envelope(`{"v":2}`)), want: nil},
		{
			name: "same sequence, other content",
			in:   checkpoint(2, envelope(`{"v":3}`)),
			want: stack.ErrCheckpointConflict,
		},
		{name: "older sequence", in: checkpoint(1, envelope(`{}`)), want: stack.ErrStaleCheckpoint},
		{name: "too large", in: checkpoint(3, string(make([]byte, 2048))), want: stack.ErrTooLarge},
		{name: "no schema version", in: stack.CheckpointInput{
			Features: nil, Deployment: []byte("{}"), SchemaVersion: 0, Sequence: 3, Invalid: false,
		}, want: stack.ErrInvalidUpdate},
	}

	for _, tt := range tests {
		if err := save(tt.in); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		}
	}

	preview := f.started(t, stack.KindPreview)

	err := f.svc.SaveCheckpoint(ctx, f.ref, preview.Update.ID, preview.Token, checkpoint(1, envelope("{}")))
	if !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Errorf("preview checkpoint: %v, want ErrWrongUpdateState", err)
	}
}

func TestImportRecordsAnUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	u, err := f.svc.Import(ctx, f.ref, "carol@example.com", checkpoint(0, `{"imported":true}`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if u.Kind != stack.KindImport || u.Status != stack.StatusSucceeded {
		t.Errorf("import update: kind %q status %q", u.Kind, u.Status)
	}

	if u.CreatedBy != "carol@example.com" {
		t.Errorf("import creator = %q, want %q", u.CreatedBy, "carol@example.com")
	}

	history, err := f.svc.History(ctx, f.ref, stack.Page{Limit: 0, Offset: 0})
	if err != nil || len(history) != 1 {
		t.Errorf("history: %d entries, %v; want 1", len(history), err)
	} else if history[0].CreatedBy != "carol@example.com" {
		t.Errorf("history creator = %q, want %q", history[0].CreatedBy, "carol@example.com")
	}
}

func TestImportRejectsInvalidInputWithoutAnUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)

	_, err := f.svc.Import(t.Context(), f.ref, "", checkpoint(0, string(make([]byte, 2048))))
	if !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("import oversized: %v, want ErrTooLarge", err)
	}

	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	if n := len(f.store.updates); n != 0 {
		t.Errorf("%d updates recorded for rejected input, want none", n)
	}
}

// The import update belongs to the server; a client that hangs up must not
// leave the stack locked.
func TestImportEndsItsUpdateWhenTheCallerGivesUp(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx, cancel := context.WithCancel(t.Context())

	f.store.mu.Lock()
	f.store.cancelOnPut = cancel
	f.store.mu.Unlock()

	if _, err := f.svc.Import(ctx, f.ref, "", checkpoint(0, "{}")); !errors.Is(err, context.Canceled) {
		t.Fatalf("import: %v, want context.Canceled", err)
	}

	if u, err := f.svc.Active(t.Context(), f.ref); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Errorf("active update after an abandoned import: %q, %v; want none", u.Status, err)
	}
}

func TestImportFailureKeepsLatestState(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)

	seedFirstCheckpoint(t, f)

	done, importErr := abandonImport(t, f)
	if !errors.Is(importErr, context.Canceled) {
		t.Fatalf("import: %v, want context.Canceled", importErr)
	}

	checkFailedImport(t, f, done)
}

// seedFirstCheckpoint records version 1 with a checkpoint, so a later failed
// import has earlier state to keep.
func seedFirstCheckpoint(t *testing.T, f updatesFixture) {
	t.Helper()

	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)

	saveErr := f.svc.SaveCheckpoint(ctx, f.ref, first.Update.ID, first.Token, checkpoint(0, `{"v":1}`))
	if saveErr != nil {
		t.Fatalf("save checkpoint: %v", saveErr)
	}

	_, completeErr := f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded)
	if completeErr != nil {
		t.Fatalf("complete: %v", completeErr)
	}
}

// abandonImport runs an import whose save fails after the import update
// started, as a client that hangs up mid-upload would. It returns the ended
// update and the save error.
func abandonImport(t *testing.T, f updatesFixture) (stack.Update, error) {
	t.Helper()

	abandoned, cancel := context.WithCancel(t.Context())

	f.store.mu.Lock()
	f.store.cancelOnPut = cancel
	f.store.mu.Unlock()

	return f.svc.Import(abandoned, f.ref, "", checkpoint(0, `{"v":2}`))
}

// checkFailedImport proves a failed import ended without state: nothing runs,
// the export still returns the first checkpoint, and history holds the failed
// import on top.
func checkFailedImport(t *testing.T, f updatesFixture, done stack.Update) {
	t.Helper()

	ctx := t.Context()

	if done.Kind != stack.KindImport || done.Status != stack.StatusFailed || done.Version != 2 {
		t.Errorf("import update: kind %q status %q version %d, want import failed version 2",
			done.Kind, done.Status, done.Version)
	}

	active, activeErr := f.svc.Active(ctx, f.ref)
	if !errors.Is(activeErr, stack.ErrUpdateNotFound) {
		t.Errorf("active update after a failed import: %q, %v; want none", active.Status, activeErr)
	}

	cp, exportErr := f.svc.Export(ctx, f.ref)
	if exportErr != nil || string(cp.Deployment) != `{"v":1}` {
		t.Errorf("export after a failed import: %q, %v; want the first checkpoint", cp.Deployment, exportErr)
	}

	history, historyErr := f.svc.History(ctx, f.ref, stack.Page{Limit: 0, Offset: 0})
	if historyErr != nil || len(history) != 2 {
		t.Fatalf("history: %d entries, %v; want the update and the failed import", len(history), historyErr)
	}

	if history[0].ID != done.ID || history[0].Status != stack.StatusFailed {
		t.Errorf("newest history entry = %+v, want the failed import", history[0])
	}
}

// TestCreateRecordsRequester checks that Create keeps the subject and the
// restore source from the plan.
func TestCreateRecordsRequester(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	u, err := f.svc.Create(
		ctx,
		f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "alice@example.com", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := f.svc.Get(ctx, f.ref, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.CreatedBy != "alice@example.com" || got.RestoredFrom != 0 {
		t.Errorf("update = %+v, want creator alice@example.com and no restore source", got)
	}
}

// finishVersion runs one mutating update with body as its checkpoint and
// returns the finished update.
func finishVersion(t *testing.T, f updatesFixture, body string) stack.Update {
	t.Helper()

	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(0, body)); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	done, err := f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	return done
}

// checkLockFree fails when the stack still has a running update.
func checkLockFree(t *testing.T, f updatesFixture) {
	t.Helper()

	if u, err := f.svc.Active(t.Context(), f.ref); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("active update after restore: %q, %v; want none", u.Status, err)
	}
}

// TestRestoreCopiesOldCheckpoint restores version 1 after two updates and
// checks the new update, the state and the history.
func TestRestoreCopiesOldCheckpoint(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	finishVersion(t, f, `{"v":1}`)
	finishVersion(t, f, `{"v":2}`)

	done, err := f.svc.Restore(ctx, f.ref, 1, "bob@example.com")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	checkRestoreUpdate(t, done)
	checkRestoreExport(t, f, `{"v":1}`)
	checkRestoreHistory(t, f, done)
	checkLockFree(t, f)
}

// checkRestoreUpdate checks the kind, status, version, creator and source of
// a restore of version 1 after two updates.
func checkRestoreUpdate(t *testing.T, done stack.Update) {
	t.Helper()

	if done.Kind != stack.KindRestore || done.Status != stack.StatusSucceeded || done.Version != 3 {
		t.Errorf("restore update: kind %q status %q version %d, want restore succeeded version 3",
			done.Kind, done.Status, done.Version)
	}

	if done.CreatedBy != "bob@example.com" || done.RestoredFrom != 1 {
		t.Errorf("restore update = %+v, want creator bob@example.com and source 1", done)
	}
}

// checkRestoreExport checks that the stack state holds body after a restore.
func checkRestoreExport(t *testing.T, f updatesFixture, body string) {
	t.Helper()

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil || string(cp.Deployment) != body {
		t.Errorf("export after restore: %q, %v; want %s", cp.Deployment, err, body)
	}
}

// checkRestoreHistory checks that done is the newest history entry with its
// kind, source and creator.
func checkRestoreHistory(t *testing.T, f updatesFixture, done stack.Update) {
	t.Helper()

	history, err := f.svc.History(t.Context(), f.ref, stack.Page{Limit: 0, Offset: 0})
	if err != nil || len(history) != 3 {
		t.Fatalf("history: %d entries, %v; want two updates and the restore", len(history), err)
	}

	if history[0].ID != done.ID || history[0].Kind != stack.KindRestore ||
		history[0].RestoredFrom != 1 || history[0].CreatedBy != "bob@example.com" {
		t.Errorf("newest history entry = %+v, want the restore of version 1", history[0])
	}
}

// TestRestoreMissingVersion checks that a restore of a version without a
// checkpoint fails with ErrNoState and records no update.
func TestRestoreMissingVersion(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	finishVersion(t, f, `{"v":1}`)

	if _, err := f.svc.Restore(ctx, f.ref, 2, "bob@example.com"); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("restore of a missing version: %v, want ErrNoState", err)
	}

	f.store.mu.Lock()
	n := len(f.store.updates)
	f.store.mu.Unlock()

	if n != 1 {
		t.Errorf("%d updates recorded for a missing version, want 1", n)
	}

	history, err := f.svc.History(ctx, f.ref, stack.Page{Limit: 0, Offset: 0})
	if err != nil || len(history) != 1 {
		t.Errorf("history: %d entries, %v; want only the first update", len(history), err)
	}

	checkLockFree(t, f)
}

// TestRestoreWhileUpdateRuns checks that a restore waits for the stack lock:
// while another mutating update runs it fails with ErrUpdateConflict and the
// running update keeps the lock.
func TestRestoreWhileUpdateRuns(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	finishVersion(t, f, `{"v":1}`)
	running := f.started(t, stack.KindUpdate)

	if _, err := f.svc.Restore(ctx, f.ref, 1, "bob@example.com"); !errors.Is(err, stack.ErrUpdateConflict) {
		t.Errorf("restore while an update runs: %v, want ErrUpdateConflict", err)
	}

	active, err := f.svc.Active(ctx, f.ref)
	if err != nil || active.ID != running.Update.ID {
		t.Errorf("active update during a conflicting restore: %q, %v; want the running update",
			active.ID, err)
	}

	if _, err := f.svc.Cancel(ctx, f.ref, running.Update.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	checkLockFree(t, f)
}

// TestRestoreFailureEndsUpdate checks that a restore whose save fails still
// ends its update as failed and frees the stack, keeping the latest state.
func TestRestoreFailureEndsUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)

	finishVersion(t, f, `{"v":1}`)

	abandoned, cancel := context.WithCancel(t.Context())

	f.store.mu.Lock()
	f.store.cancelOnPut = cancel
	f.store.mu.Unlock()

	done, err := f.svc.Restore(abandoned, f.ref, 1, "bob@example.com")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("restore: %v, want context.Canceled", err)
	}

	checkFailedRestore(t, done)
	checkRestoreExport(t, f, `{"v":1}`)
	checkFailedRestoreHistory(t, f, done)
	checkLockFree(t, f)
}

// checkFailedRestore checks the kind, status, version, source and creator of
// a restore whose save failed.
func checkFailedRestore(t *testing.T, done stack.Update) {
	t.Helper()

	if done.Kind != stack.KindRestore || done.Status != stack.StatusFailed || done.Version != 2 {
		t.Errorf("restore update: kind %q status %q version %d, want restore failed version 2",
			done.Kind, done.Status, done.Version)
	}

	if done.RestoredFrom != 1 || done.CreatedBy != "bob@example.com" {
		t.Errorf("restore update = %+v, want source 1 and creator bob@example.com", done)
	}
}

// checkFailedRestoreHistory checks that done is the newest history entry with
// a failed status.
func checkFailedRestoreHistory(t *testing.T, f updatesFixture, done stack.Update) {
	t.Helper()

	history, err := f.svc.History(t.Context(), f.ref, stack.Page{Limit: 0, Offset: 0})
	if err != nil || len(history) != 2 {
		t.Fatalf("history: %d entries, %v; want the update and the failed restore", len(history), err)
	}

	if history[0].ID != done.ID || history[0].Status != stack.StatusFailed {
		t.Errorf("newest history entry = %+v, want the failed restore", history[0])
	}
}

func TestParseStackVersion(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"0", "1", "99"} {
		if _, err := stack.ParseStackVersion(s); err != nil {
			t.Errorf("ParseStackVersion(%q): %v", s, err)
		}
	}

	for _, s := range []string{"", "abc", "1.5", "-1"} {
		if _, err := stack.ParseStackVersion(s); !errors.Is(err, stack.ErrInvalidUpdate) {
			t.Errorf("ParseStackVersion(%q): %v, want ErrInvalidUpdate", s, err)
		}
	}
}

func TestParseCompletion(t *testing.T) {
	t.Parallel()

	for _, s := range []stack.UpdateStatus{stack.StatusSucceeded, stack.StatusFailed, stack.StatusCanceled} {
		if _, err := stack.ParseCompletion(string(s)); err != nil {
			t.Errorf("ParseCompletion(%q): %v", s, err)
		}
	}

	for _, s := range []string{"running", "not started", "done", ""} {
		if _, err := stack.ParseCompletion(s); !errors.Is(err, stack.ErrInvalidUpdate) {
			t.Errorf("ParseCompletion(%q): %v, want ErrInvalidUpdate", s, err)
		}
	}
}

// TestCrashBetweenBlobWriteAndCommitLeavesNoRow proves a crash between the
// blob write and the SQL commit leaves no row that names a missing blob, and
// that the retry with the same content then succeeds instead of reporting a
// conflict.
func TestCrashBetweenBlobWriteAndCommitLeavesNoRow(t *testing.T) {
	t.Parallel()

	for _, seq := range []int64{1, 0} {
		t.Run(fmt.Sprintf("sequence %d", seq), func(t *testing.T) {
			t.Parallel()

			crashLeavesNoRow(t, seq)
		})
	}
}

// crashLeavesNoRow abandons one save of a started update after its blob
// write, proves no row names the missing blob, and proves the retry with the
// same content succeeds.
func crashLeavesNoRow(t *testing.T, seq int64) {
	t.Helper()

	f := newUpdates(t)
	s := f.started(t, stack.KindUpdate)

	abandoned, cancel := context.WithCancel(t.Context())

	f.store.mu.Lock()
	f.store.cancelOnPut = cancel
	f.store.mu.Unlock()

	body := `{"a":1}`
	if seq > 0 {
		body = envelope(body)
	}

	in := checkpoint(seq, body)

	if err := f.svc.SaveCheckpoint(abandoned, f.ref, s.Update.ID, s.Token, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned save: %v, want context.Canceled", err)
	}

	if _, err := f.store.CheckpointByUpdate(t.Context(), f.ref, s.Update.ID); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("checkpoint after the crash: %v, want ErrNoState", err)
	}

	f.store.mu.Lock()
	f.store.cancelOnPut = nil
	f.store.mu.Unlock()

	if err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, in); err != nil {
		t.Fatalf("retry after the crash: %v", err)
	}

	cp, err := f.svc.Export(t.Context(), f.ref)
	if err != nil || string(cp.Deployment) != `{"a":1}` {
		t.Fatalf("export after the retry: %q, %v; want the checkpoint", cp.Deployment, err)
	}
}

// TestSweepDeletesUnreferencedBlobs proves the save-time sweep deletes the
// orphan blobs of the update older than the grace period while it keeps the
// blob the checkpoint names.
func TestSweepDeletesUnreferencedBlobs(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"a":1}`))); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	orphan, orphanErr := blob.ParseKey(f.prefix + "stk-test/" + s.Update.ID.String() + "/99")
	if orphanErr != nil {
		t.Fatalf("parse orphan key: %v", orphanErr)
	}

	mem, ok := f.blobs.(*memBlob)
	if !ok {
		t.Fatalf("fixture blobs = %T, want *memBlob", f.blobs)
	}

	if putErr := mem.Put(ctx, orphan, strings.NewReader("orphan")); putErr != nil {
		t.Fatalf("write orphan blob: %v", putErr)
	}

	mem.ageAll(2 * time.Hour)

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(2, envelope(`{"a":2}`))); err != nil {
		t.Fatalf("second save: %v", err)
	}

	if _, err := mem.Get(ctx, orphan); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("orphan blob: %v, want it deleted", err)
	}

	cp, err := f.svc.Export(ctx, f.ref)
	if err != nil || string(cp.Deployment) != `{"a":2}` {
		t.Fatalf("export after the sweep: %q, %v; want the second checkpoint", cp.Deployment, err)
	}
}

// TestStartSweepsOldOrphanBlobs proves the start-time sweep deletes an old
// orphan blob of an idle update when the next update starts: an orphan of an
// update that never saves again is collected by the next update on the stack.
func TestStartSweepsOldOrphanBlobs(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)

	orphan, orphanErr := blob.ParseKey(f.prefix + "stk-test/" + first.Update.ID.String() + "/99")
	if orphanErr != nil {
		t.Fatalf("parse orphan key: %v", orphanErr)
	}

	mem, ok := f.blobs.(*memBlob)
	if !ok {
		t.Fatalf("fixture blobs = %T, want *memBlob", f.blobs)
	}

	if putErr := mem.Put(ctx, orphan, strings.NewReader("orphan")); putErr != nil {
		t.Fatalf("write orphan blob: %v", putErr)
	}

	if _, err := f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete first update: %v", err)
	}

	mem.ageAll(2 * time.Hour)

	next, err := f.svc.Create(ctx, f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create next update: %v", err)
	}

	if _, err := f.svc.Start(ctx, f.ref, next.ID); err != nil {
		t.Fatalf("start next update: %v", err)
	}

	if _, err := mem.Get(ctx, orphan); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("orphan blob: %v, want it deleted", err)
	}
}

// TestStartKeepsYoungBlobs proves the start-time sweep keeps a blob younger
// than the grace period, so a save that still commits is never collected.
func TestStartKeepsYoungBlobs(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)

	orphan, orphanErr := blob.ParseKey(f.prefix + "stk-test/" + first.Update.ID.String() + "/99")
	if orphanErr != nil {
		t.Fatalf("parse orphan key: %v", orphanErr)
	}

	mem, ok := f.blobs.(*memBlob)
	if !ok {
		t.Fatalf("fixture blobs = %T, want *memBlob", f.blobs)
	}

	if putErr := mem.Put(ctx, orphan, strings.NewReader("orphan")); putErr != nil {
		t.Fatalf("write orphan blob: %v", putErr)
	}

	if _, err := f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete first update: %v", err)
	}

	next, err := f.svc.Create(ctx, f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create next update: %v", err)
	}

	if _, err := f.svc.Start(ctx, f.ref, next.ID); err != nil {
		t.Fatalf("start next update: %v", err)
	}

	if _, err := mem.Get(ctx, orphan); err != nil {
		t.Fatalf("young orphan blob: %v, want it kept", err)
	}
}

// TestStartKeepsReferencedBlobs proves the start-time sweep keeps a blob that
// a checkpoint row names, even when it is older than the grace period.
func TestStartKeepsReferencedBlobs(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)

	in := checkpoint(1, envelope(`{"a":1}`))

	if err := f.svc.SaveCheckpoint(ctx, f.ref, first.Update.ID, first.Token, in); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	if _, err := f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete first update: %v", err)
	}

	refs, err := f.store.ReferencedBlobKeys(ctx, "stk-test")
	if err != nil || len(refs) != 1 {
		t.Fatalf("referenced keys: %v, %v; want one", refs, err)
	}

	mem, ok := f.blobs.(*memBlob)
	if !ok {
		t.Fatalf("fixture blobs = %T, want *memBlob", f.blobs)
	}

	mem.ageAll(90 * time.Minute)

	next, err := f.svc.Create(ctx, f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create next update: %v", err)
	}

	if _, startErr := f.svc.Start(ctx, f.ref, next.ID); startErr != nil {
		t.Fatalf("start next update: %v", startErr)
	}

	if _, getErr := mem.Get(ctx, refs[0]); getErr != nil {
		t.Fatalf("referenced blob: %v, want it kept", getErr)
	}

	cp, err := f.svc.Export(ctx, f.ref)
	if err != nil || string(cp.Deployment) != `{"a":1}` {
		t.Fatalf("export after the sweep: %q, %v; want the checkpoint", cp.Deployment, err)
	}
}

// TestStartSucceedsWhenSweepDeleteFails proves a failed blob delete does not
// fail the update start: the sweep is best effort and the blob stays for the
// next sweep.
func TestStartSucceedsWhenSweepDeleteFails(t *testing.T) {
	t.Parallel()

	clk := newClock()
	mem := newMemBlob(clk)
	failing := &failBlob{memBlob: mem, listErr: nil, deleteErr: errDeleteBoom}
	f := newUpdatesWith(t, clk, failing, nil)
	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)

	orphan, orphanErr := blob.ParseKey(f.prefix + "stk-test/" + first.Update.ID.String() + "/99")
	if orphanErr != nil {
		t.Fatalf("parse orphan key: %v", orphanErr)
	}

	if putErr := mem.Put(ctx, orphan, strings.NewReader("orphan")); putErr != nil {
		t.Fatalf("write orphan blob: %v", putErr)
	}

	if _, err := f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete first update: %v", err)
	}

	mem.ageAll(2 * time.Hour)

	next, err := f.svc.Create(ctx, f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create next update: %v", err)
	}

	if _, err := f.svc.Start(ctx, f.ref, next.ID); err != nil {
		t.Fatalf("start next update with a failing sweep delete: %v", err)
	}

	if _, err := mem.Get(ctx, orphan); err != nil {
		t.Fatalf("orphan blob after a failed delete: %v, want it kept for the next sweep", err)
	}
}

// TestSweepListsOneUpdatePrefix proves the save-time sweep lists one update
// prefix, so a stack with many updates never loads every blob key.
func TestSweepListsOneUpdatePrefix(t *testing.T) {
	t.Parallel()

	clk := newClock()
	mem := newMemBlob(clk)
	rec := &recordBlob{memBlob: mem, listed: nil}
	f := newUpdatesWith(t, clk, rec, nil)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	// The start-time sweep lists the stack prefix; the save-time sweep below
	// must list one update prefix only.
	rec.listed = nil

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"a":1}`))); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(2, envelope(`{"a":2}`))); err != nil {
		t.Fatalf("second save: %v", err)
	}

	want := f.prefix + "stk-test/" + s.Update.ID.String() + "/"

	if len(rec.listed) == 0 {
		t.Fatal("sweep listed no prefix")
	}

	for _, prefix := range rec.listed {
		if prefix != want {
			t.Fatalf("sweep listed %q, want only %q", prefix, want)
		}
	}
}

// TestCheckpointLogsHoldNoBlobKey proves no log line and no returned error
// names a checkpoint blob key: sweep and delete failures log the stack
// without the key, and a missing blob fails without it too.
func TestCheckpointLogsHoldNoBlobKey(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, nil))

	clk := newClock()
	mem := newMemBlob(clk)
	failing := &failBlob{memBlob: mem, listErr: errListBoom, deleteErr: errDeleteBoom}
	f := newUpdatesWith(t, clk, failing, logger)
	ctx := t.Context()
	s := f.started(t, stack.KindUpdate)

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(`{"a":1}`))); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(2, envelope(`{"a":2}`))); err != nil {
		t.Fatalf("second save: %v", err)
	}

	missing, err := blob.ParseKey(f.prefix + "stk-test/" + s.Update.ID.String() + "/7")
	if err != nil {
		t.Fatalf("parse missing key: %v", err)
	}

	stored := stack.Checkpoint{
		NamespaceID: "", StackID: "",
		SavedAt:       clk.Now(),
		Features:      nil,
		Deployment:    nil,
		Update:        s.Update.ID,
		Ref:           f.ref,
		Lease:         s.Update.Lease.Digest,
		SchemaVersion: 3,
		Sequence:      5,
		Digest:        [sha256.Size]byte{},
		Invalid:       false,
		BlobKey:       missing,
		BlobSize:      0,
		BlobMD5:       [16]byte{},

		AttemptStartedAt: clk.Now(), Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	if _, err := f.store.PutCheckpoint(ctx, stored, namespace.Fence{Namespace: "", FailoverVersion: 0}); err != nil {
		t.Fatalf("store checkpoint with a missing blob: %v", err)
	}

	exportErr := checkExportHoldsNoKey(t, f)

	if out := buf.String(); !strings.Contains(out, "sweep checkpoint blobs") {
		t.Fatalf("log holds no sweep failure: %q", out)
	} else if strings.Contains(out, "pulumi/") {
		t.Fatalf("log holds a blob key: %q", out)
	}

	if exportErr != nil && strings.Contains(exportErr.Error(), "pulumi/") {
		t.Fatalf("export error holds a blob key: %v", exportErr)
	}
}

// checkExportHoldsNoKey exports the stack whose checkpoint names a missing
// blob and returns the error the export fails with.
func checkExportHoldsNoKey(t *testing.T, f updatesFixture) error {
	t.Helper()

	_, err := f.svc.Export(t.Context(), f.ref)
	if err == nil {
		t.Fatal("export of a missing blob succeeded, want an error")
	}

	return err
}

// errListBoom and errDeleteBoom fail blob listings and deletes in the log
// redaction test.
var (
	errListBoom   = errors.New("list boom")
	errDeleteBoom = errors.New("delete boom")
)

// recordBlob records the prefixes a sweep lists.
type recordBlob struct {
	*memBlob

	listed []string
}

// ListPage implements blob.Store, recording the prefix of each request.
func (m *recordBlob) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	m.listed = append(m.listed, req.Prefix)

	return m.memBlob.ListPage(ctx, req)
}

// failBlob fails listings and deletes, to prove sweep failures stay out of
// saves and hold no blob key in the logs.
type failBlob struct {
	*memBlob

	listErr   error
	deleteErr error
}

// List implements blob.Store.
func (m *failBlob) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if m.listErr != nil {
		return blob.Page{}, m.listErr
	}

	return m.memBlob.ListPage(ctx, req)
}

// Delete implements blob.Store.
func (m *failBlob) Delete(ctx context.Context, key blob.Key) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}

	return m.memBlob.Delete(ctx, key)
}

// SetOverrides is not used by this fake.
func (*memUpdates) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

// withScope fills the namespace ID and the stack ID of a checkpoint that a
// read returns, as the SQL stores do from the query. Callers hold mu.
func (m *memUpdates) withScope(cp stack.Checkpoint) stack.Checkpoint {
	cp.NamespaceID = m.nsID
	cp.StackID = m.stacks[cp.Ref].ID

	return cp
}

// refOfLocked returns the current reference of the stack with the given ID,
// as the stores resolve it from the stack row. Callers hold mu.
func (m *memUpdates) refOfLocked(id stack.ID) (stack.Ref, bool) {
	for ref := range m.stacks {
		if m.stacks[ref].ID == id {
			return ref, true
		}
	}

	return stack.Ref{Namespace: "", Project: "", Stack: ""}, false
}

// refOf returns the current reference of the stack with the given ID.
func (m *memUpdates) refOf(id stack.ID) (stack.Ref, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.refOfLocked(id)
}

// renameStack moves the stack row and the references of its checkpoints to
// newRef and keeps the stack ID, as a rename does in the stores.
func (m *memUpdates) renameStack(oldRef, newRef stack.Ref) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st := m.stacks[oldRef]
	st.Ref = newRef

	delete(m.stacks, oldRef)

	m.stacks[newRef] = st

	for id := range m.checkpoints {
		if cp := m.checkpoints[id]; cp.Ref == oldRef {
			cp.Ref = newRef
			m.checkpoints[id] = cp
		}
	}
}

// addStack registers a stack with a stable ID under ref. Callers hold no lock.
func (m *memUpdates) addStack(ref stack.Ref, id stack.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stacks[ref] = stack.Stack{
		NamespaceID: "",
		CreatedAt:   time.Time{},
		UpdatedAt:   time.Time{},
		Tags:        nil,
		Ref:         ref,
		ID:          id,
		Version:     0,
	}
}

// runningUnder reports whether update id is running under lease; callers hold mu.
func (m *memUpdates) runningUnder(id stack.UpdateID, lease token.Digest) bool {
	u := m.updates[id]

	return u.Status == stack.StatusRunning && u.Lease.Digest == lease
}

// checkFence reports whether fence authorizes a write to ref. Namespaces the
// store does not know stay lenient: the service tests drive this store
// without creating any, so their writes skip the check. Callers hold mu.
func (m *memUpdates) checkFence(ref stack.Ref, fence namespace.Fence) error {
	ns, ok := m.namespaces[ref.Namespace]
	if !ok {
		return nil
	}

	if fence != ns.Fence() {
		return namespace.ErrFenced
	}

	return nil
}

// withSize fills the CheckpointBytes of u from its checkpoint row, or 0 when
// it saved none. Callers hold mu.
func (m *memUpdates) withSize(u stack.Update) stack.Update {
	if cp, ok := m.checkpoints[u.ID]; ok {
		u.CheckpointBytes = int64(len(cp.Deployment))
	}

	return u
}

// TestHistoryOfMissingStack proves that History answers ErrNotFound for a
// stack that does not exist, and an empty list for a stack with no update.
func TestHistoryOfMissingStack(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ghost := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "ghost"}

	_, err := f.svc.History(t.Context(), ghost, stack.Page{Limit: 0, Offset: 0})
	if !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("history of a missing stack = %v, want stack.ErrNotFound", err)
	}

	list, err := f.svc.History(t.Context(), f.ref, stack.Page{Limit: 0, Offset: 0})
	if err != nil || len(list) != 0 {
		t.Fatalf("history of a stack with no update = %v, %v, want an empty list", list, err)
	}
}

// deleteAfterGetStacks deletes the stack after its first successful read, as a
// delete that lands between the reads of History.
type deleteAfterGetStacks struct {
	stack.CheckpointStacks

	drop func()
	read atomic.Bool
}

func (s *deleteAfterGetStacks) GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error) {
	st, err := s.CheckpointStacks.GetStack(ctx, ref)
	if err != nil {
		return stack.Stack{}, err
	}

	if s.read.CompareAndSwap(false, true) {
		s.drop()
	}

	return st, nil
}

// withStacks returns the fixture's service with other stack reads.
func (f updatesFixture) withStacks(t *testing.T, stacks stack.CheckpointStacks) *stack.Updates {
	t.Helper()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             f.sealer,
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            f.store,
		Tokens:             nil,
		Checkpoints:        f.store,
		Events:             f.store,
		Namespaces:         f.namespaces,
		Stacks:             stacks,
		Blobs:              f.blobs,
		Now:                f.clock.Now,
		Logger:             nil,
		MaxCheckpointBytes: 1024,
		Journal:            nil,
		Replayer:           nil,
		Refusals:           f.refusals,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	return svc
}

// TestHistoryOfStackDeletedBetweenReads proves that a stack deleted between
// the existence check and the listing answers ErrNotFound, not an empty
// history.
func TestHistoryOfStackDeletedBetweenReads(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	stacks := &deleteAfterGetStacks{
		CheckpointStacks: f.store,
		drop:             func() { f.store.dropStack(f.ref) },
		read:             atomic.Bool{},
	}

	_, err := f.withStacks(t, stacks).History(t.Context(), f.ref, stack.Page{Limit: 0, Offset: 0})
	if !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("history of a stack deleted between the reads = %v, want stack.ErrNotFound", err)
	}
}

// hookedKeepSet runs before the keep-set read of a sweep, so a test changes
// the stack between the listing of the blobs and that read.
type hookedKeepSet struct {
	*memUpdates

	before func()
}

// ReferencedBlobKeys implements stack.CheckpointStore.
func (h hookedKeepSet) ReferencedBlobKeys(ctx context.Context, id stack.ID) ([]blob.Key, error) {
	h.before()

	return h.memUpdates.ReferencedBlobKeys(ctx, id)
}

// dropStack removes the stack row, as a remove does for its name.
func (m *memUpdates) dropStack(ref stack.Ref) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.stacks, ref)
}

// completedWithBlob saves one checkpoint, completes the update and returns
// the blob key that the row names, with the blob aged past the grace period.
func (f updatesFixture) completedWithBlob(t *testing.T) (blob.Key, *memBlob) {
	t.Helper()

	ctx := t.Context()
	first := f.started(t, stack.KindUpdate)

	err := f.svc.SaveCheckpoint(ctx, f.ref, first.Update.ID, first.Token, checkpoint(1, envelope(`{"a":1}`)))
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	_, err = f.svc.Complete(ctx, f.ref, first.Update.ID, first.Token, stack.StatusSucceeded)
	if err != nil {
		t.Fatalf("complete first update: %v", err)
	}

	named, err := f.store.ReferencedBlobKeys(ctx, "stk-test")
	if err != nil || len(named) != 1 {
		t.Fatalf("referenced keys: %v, %v; want one", named, err)
	}

	mem, ok := f.blobs.(*memBlob)
	if !ok {
		t.Fatalf("fixture blobs = %T, want *memBlob", f.blobs)
	}

	mem.ageAll(90 * time.Minute)

	return named[0], mem
}

// withKeepSetHook returns an Updates service whose keep-set read runs before
// first.
func (f updatesFixture) withKeepSetHook(t *testing.T, before func()) *stack.Updates {
	t.Helper()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:      secrettest.NewMemorySealer(),
		BlobGuard:   blobguardtest.None{},
		ManualLocks: nil, Updates: f.store, Tokens: nil,
		Checkpoints: hookedKeepSet{memUpdates: f.store, before: before},
		Events:      f.store, Namespaces: f.namespaces, Stacks: f.store, Blobs: f.blobs,
		Now: f.clock.Now, Logger: nil, MaxCheckpointBytes: 1024,
		Journal: nil, Replayer: nil, Refusals: f.refusals,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	return svc
}

// withKeepSetHookAndGuard is withKeepSetHook with guard as the guard of the
// blob deletes.
func (f updatesFixture) withKeepSetHookAndGuard(t *testing.T, guard blobguard.Watch, before func()) *stack.Updates {
	t.Helper()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:      secrettest.NewMemorySealer(),
		BlobGuard:   guard,
		ManualLocks: nil, Updates: f.store, Tokens: nil,
		Checkpoints: hookedKeepSet{memUpdates: f.store, before: before},
		Events:      f.store, Namespaces: f.namespaces, Stacks: f.store, Blobs: f.blobs,
		Now: f.clock.Now, Logger: nil, MaxCheckpointBytes: 1024,
		Journal: nil, Replayer: nil, Refusals: f.refusals,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	return svc
}

// TestStartSweepKeepsBlobsWhenTheStackChanges proves that a change of the
// stack between the listing of its blobs and the read of the keep set deletes
// no blob that a row names. A rename must not empty the keep set, and a stack
// row that is gone must stop the sweep.
func TestStartSweepKeepsBlobsWhenTheStackChanges(t *testing.T) {
	t.Parallel()

	renamed, err := stack.ParseRef("acme", "web", "prod2")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	tests := []struct {
		name   string
		change func(m *memUpdates, ref stack.Ref)
	}{
		{name: "rename", change: func(m *memUpdates, ref stack.Ref) { m.renameStack(ref, renamed) }},
		{name: "remove", change: func(m *memUpdates, ref stack.Ref) { m.dropStack(ref) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdates(t)
			key, mem := f.completedWithBlob(t)
			svc := f.withKeepSetHook(t, func() { tc.change(f.store, f.ref) })

			preview, createErr := f.svc.Create(t.Context(), f.ref, stack.Plan{
				Kind: stack.KindUpdate, DryRun: true, CreatedBy: "", RestoredFrom: 0, GitHead: "",
			})
			if createErr != nil {
				t.Fatalf("create preview: %v", createErr)
			}

			if _, startErr := svc.Start(t.Context(), f.ref, preview.ID); startErr != nil {
				t.Fatalf("start preview: %v", startErr)
			}

			if _, getErr := mem.Get(t.Context(), key); getErr != nil {
				t.Fatalf("named blob after a %s during the sweep: %v, want it kept", tc.name, getErr)
			}
		})
	}
}
