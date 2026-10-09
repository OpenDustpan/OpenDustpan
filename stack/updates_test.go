package stack_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
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

// memUpdates implements the update, checkpoint and event ports in memory,
// following their documented contracts.
type memUpdates struct {
	updates     map[stack.UpdateID]stack.Update
	checkpoints []stack.Checkpoint
	events      map[stack.UpdateID][]stack.Event
	mu          sync.Mutex
}

func newMemUpdates() *memUpdates {
	return &memUpdates{
		updates:     map[stack.UpdateID]stack.Update{},
		checkpoints: nil,
		events:      map[stack.UpdateID][]stack.Event{},
		mu:          sync.Mutex{},
	}
}

func (m *memUpdates) CreateUpdate(_ context.Context, u stack.Update, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updates[u.ID] = u

	return nil
}

func (m *memUpdates) GetUpdate(_ context.Context, _ stack.Ref, id stack.UpdateID) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.updates[id]
	if !ok {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	return u, nil
}

func (m *memUpdates) StartUpdate(_ context.Context, u stack.Update, _ namespace.Fence) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updates[u.ID].Revision != u.Revision-1 {
		return stack.Update{}, stack.ErrConcurrentModification
	}

	var highest int64

	for id := range m.updates {
		other := m.updates[id]
		if !other.Kind.Mutates() || other.Ref != u.Ref {
			continue
		}

		if other.Status == stack.StatusRunning && u.Kind.Mutates() {
			return stack.Update{}, stack.ErrUpdateConflict
		}

		highest = max(highest, other.Version)
	}

	if u.Kind.Mutates() {
		u.Version = highest + 1
	}

	m.updates[u.ID] = u

	return u, nil
}

func (m *memUpdates) SaveUpdate(_ context.Context, u stack.Update, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updates[u.ID].Revision != u.Revision-1 {
		return stack.ErrConcurrentModification
	}

	m.updates[u.ID] = u

	return nil
}

func (m *memUpdates) ActiveUpdate(_ context.Context, ref stack.Ref) (stack.Update, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.updates {
		if u := m.updates[id]; u.Ref == ref && u.Kind.Mutates() && u.Status == stack.StatusRunning {
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
		if u := m.updates[id]; u.Ref == ref && u.Kind.Mutates() && u.Status != stack.StatusNotStarted {
			list = append(list, u)
		}
	}

	slices.SortFunc(list, func(a, b stack.Update) int { return int(b.Version - a.Version) })

	return list, nil
}

func (m *memUpdates) PutCheckpoint(_ context.Context, cp stack.Checkpoint, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cp.Sequence > 0 {
		for i := range slices.Backward(m.checkpoints) {
			last := &m.checkpoints[i]
			if last.Update != cp.Update {
				continue
			}

			switch {
			case cp.Sequence < last.Sequence:
				return stack.ErrStaleCheckpoint
			case cp.Sequence == last.Sequence && cp.Digest == last.Digest:
				return nil
			case cp.Sequence == last.Sequence:
				return stack.ErrCheckpointConflict
			}

			break
		}
	}

	m.checkpoints = append(m.checkpoints, cp)

	return nil
}

func (m *memUpdates) LatestCheckpoint(_ context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range slices.Backward(m.checkpoints) {
		if m.checkpoints[i].Ref == ref {
			return m.checkpoints[i], nil
		}
	}

	return stack.Checkpoint{}, stack.ErrNoState
}

func (m *memUpdates) AppendEvents(
	_ context.Context, _ stack.Ref, id stack.UpdateID, events []stack.Event, _ namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, e := range events {
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

	u, err := f.svc.Create(t.Context(), f.ref, kind)
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
	return stack.CheckpointInput{Features: nil, Deployment: []byte(body), SchemaVersion: 3, Sequence: seq}
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

	_, err = f.svc.Complete(ctx, f.ref, s.Update.ID, s.Token, stack.StatusSucceeded)
	if !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Errorf("completing twice: %v, want ErrWrongUpdateState", err)
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

	second, err := f.svc.Create(ctx, f.ref, stack.KindDestroy)
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

	if _, err := f.svc.Start(ctx, f.ref, second.ID); err != nil {
		t.Errorf("start after cancel: %v", err)
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
			Features: nil, Deployment: []byte("{}"), SchemaVersion: 0, Sequence: 3,
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

func TestParseCompletion(t *testing.T) {
	t.Parallel()

	for _, s := range []stack.UpdateStatus{stack.StatusSucceeded, stack.StatusFailed, stack.StatusCancelled} {
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
