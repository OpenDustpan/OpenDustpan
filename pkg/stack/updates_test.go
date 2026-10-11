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
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// memNamespaces is a namespace.Store holding one namespace.
type memNamespaces struct{ ns namespace.Namespace }

func (m *memNamespaces) CreateNamespace(context.Context, namespace.Namespace) error {
	return namespace.ErrAlreadyExists
}

func (m *memNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
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
}

func newMemUpdates() *memUpdates {
	return &memUpdates{
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
	}
}

// Ping implements namespace.Store: the in-memory store is always ready.
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

	return st, nil
}

func (m *memUpdates) CreateUpdate(_ context.Context, u stack.Update, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(u.Ref, fence); err != nil {
		return err
	}

	if _, ok := m.namespaces[u.Ref.Namespace]; ok {
		if _, ok := m.stacks[u.Ref]; !ok {
			return stack.ErrNotFound
		}
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

func (m *memUpdates) PutCheckpoint(_ context.Context, cp stack.Checkpoint, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(cp.Ref, fence); err != nil {
		return err
	}

	if !m.runningUnder(cp.Update, cp.Lease) {
		return stack.ErrWrongUpdateState
	}

	if !m.updates[cp.Update].Mutates() {
		return stack.ErrWrongUpdateState
	}

	if m.cancelOnPut != nil {
		m.cancelOnPut()

		return context.Canceled
	}

	if cp.Sequence > 0 {
		if last, ok := m.checkpoints[cp.Update]; ok {
			switch {
			case cp.Sequence < last.Sequence:
				return stack.ErrStaleCheckpoint
			case cp.Sequence == last.Sequence && cp.Digest == last.Digest:
				return nil
			case cp.Sequence == last.Sequence:
				return stack.ErrCheckpointConflict
			}
		}
	}

	m.checkpoints[cp.Update] = cp

	return nil
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

	return best, nil
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
			return cp, nil
		}
	}

	return stack.Checkpoint{}, stack.ErrNoState
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

type updatesFixture struct {
	svc   *stack.Updates
	store *memUpdates
	clock *clock
	ref   stack.Ref
}

func newUpdates(t *testing.T) updatesFixture {
	t.Helper()

	clk := &clock{now: time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC), mu: sync.Mutex{}}
	ns := namespace.Namespace{CreatedAt: clk.now, Name: "acme", HomeRegion: "local", FailoverVersion: 1}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: &memNamespaces{ns: ns}, Now: clk.Now, Region: "local"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	store := newMemUpdates()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            store,
		Checkpoints:        store,
		Events:             store,
		Namespaces:         namespaces,
		Now:                clk.Now,
		MaxCheckpointBytes: 1024,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return updatesFixture{svc: svc, store: store, clock: clk, ref: ref}
}

func (f updatesFixture) started(t *testing.T, kind stack.UpdateKind) stack.Started {
	t.Helper()

	return f.startedPlan(t, stack.Plan{Kind: kind, DryRun: false, CreatedBy: "", RestoredFrom: 0})
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

	if err := f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, `{"a":1}`)); err != nil {
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
		Kind: stack.KindDestroy, DryRun: false, CreatedBy: "", RestoredFrom: 0,
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

	dry := f.startedPlan(t, stack.Plan{Kind: stack.KindUpdate, DryRun: true, CreatedBy: "", RestoredFrom: 0})
	if dry.Update.Mutates() || dry.Update.Version != 0 {
		t.Errorf("dry run: mutates %t version %d, want false 0", dry.Update.Mutates(), dry.Update.Version)
	}

	err := f.svc.SaveCheckpoint(ctx, f.ref, dry.Update.ID, dry.Token, checkpoint(1, "{}"))
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

	err = f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, other, checkpoint(1, "{}"))
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

	err = f.svc.SaveCheckpoint(ctx, f.ref, s.Update.ID, s.Token, checkpoint(1, "{}"))
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
		{name: "first", in: checkpoint(2, `{"v":2}`), want: nil},
		{name: "retried", in: checkpoint(2, `{"v":2}`), want: nil},
		{name: "same sequence, other content", in: checkpoint(2, `{"v":3}`), want: stack.ErrCheckpointConflict},
		{name: "older sequence", in: checkpoint(1, `{}`), want: stack.ErrStaleCheckpoint},
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

	err := f.svc.SaveCheckpoint(ctx, f.ref, preview.Update.ID, preview.Token, checkpoint(1, "{}"))
	if !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Errorf("preview checkpoint: %v, want ErrWrongUpdateState", err)
	}
}

func TestImportRecordsAnUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)
	ctx := t.Context()

	u, err := f.svc.Import(ctx, f.ref, checkpoint(0, `{"imported":true}`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if u.Kind != stack.KindImport || u.Status != stack.StatusSucceeded {
		t.Errorf("import update: kind %q status %q", u.Kind, u.Status)
	}

	history, err := f.svc.History(ctx, f.ref, stack.Page{Limit: 0, Offset: 0})
	if err != nil || len(history) != 1 {
		t.Errorf("history: %d entries, %v; want 1", len(history), err)
	}
}

func TestImportRejectsInvalidInputWithoutAnUpdate(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)

	_, err := f.svc.Import(t.Context(), f.ref, checkpoint(0, string(make([]byte, 2048))))
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

	if _, err := f.svc.Import(ctx, f.ref, checkpoint(0, "{}")); !errors.Is(err, context.Canceled) {
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

	return f.svc.Import(abandoned, f.ref, checkpoint(0, `{"v":2}`))
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
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "alice@example.com", RestoredFrom: 0},
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
