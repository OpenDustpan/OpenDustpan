package storetest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
)

// UpdatesStore is every port of the update cycle, plus the namespace and
// stack ports its fixtures need.
type UpdatesStore interface {
	Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
}

// UpdatesFactory returns a fresh, empty store for one subtest.
type UpdatesFactory func(t *testing.T) UpdatesStore

// DeleteStore deletes a stack for tests until issue #49 adds the port. The
// delete removes its updates, checkpoints and events.
type DeleteStore interface {
	DeleteStackForTest(ctx context.Context, ref stack.Ref, fence namespace.Fence) error
}

// RunUpdates executes the conformance suite of the update-cycle ports
// (stack.UpdateStore, stack.CheckpointStore, stack.EventStore) against stores
// produced by newStore.
func RunUpdates(t *testing.T, newStore UpdatesFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, f fixture)
		name string
	}{
		{name: "update round trip", test: testUpdateRoundTrip},
		{name: "missing update is ErrUpdateNotFound", test: testMissingUpdate},
		{name: "update of missing stack is ErrNotFound", test: testUpdateMissingStack},
		{name: "stale fence is ErrFenced on every write", test: testUpdateWritesFenced},
		{name: "start assigns versions and ending moves the stack", test: testStartVersions},
		{name: "one mutating update runs per stack", test: testOneRunning},
		{name: "concurrent starts admit exactly one", test: testConcurrentStarts},
		{name: "stale revision is ErrConcurrentModification", test: testStaleRevision},
		{name: "concurrent transitions admit exactly one", test: testConcurrentSaves},
		{name: "active update is the running mutating one", test: testActiveUpdate},
		{name: "history is started mutating updates, newest first", test: testListUpdates},
		{name: "checkpoint round trip keeps bytes", test: testCheckpointRoundTrip},
		{name: "checkpoint needs the running update's lease", test: testCheckpointLease},
		{name: "checkpoint sequence numbers are idempotent", test: testCheckpointSequence},
		{name: "latest checkpoint spans updates", test: testCheckpointAcrossUpdates},
		{name: "latest checkpoint is the highest version", test: testCheckpointLatestIsHighest},
		{name: "checkpoint by version returns only that version", test: testCheckpointByVersion},
		{name: "checkpoint of a preview is rejected", test: testCheckpointPreviewRejected},
		{name: "canceled or failed update keeps its checkpoint", test: testCheckpointKeepsAfterEnd},
		{name: "delete of the stack deletes its checkpoints", test: testDeleteStackDeletesCheckpoints},
		{name: "events append, dedupe and page", test: testEvents},
		{name: "events need the running update's lease", test: testEventsLease},
		{name: "rename keeps history, checkpoint and events", test: testRenameKeepsHistory},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newFixture(t, newStore(t)))
		})
	}
}

// fixture is a store holding namespace "acme" and stack "acme/web/prod".
type fixture struct {
	s     UpdatesStore
	ref   stack.Ref
	fence namespace.Fence
}

func newFixture(t *testing.T, s UpdatesStore) fixture {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	return fixture{s: s, ref: st.Ref, fence: ns.Fence()}
}

// other creates stack "acme/web/dev".
func (f fixture) other(t *testing.T) stack.Ref {
	t.Helper()

	st := newStack(f.ref.Namespace, f.ref.Project.String(), "dev")
	if err := f.s.CreateStack(t.Context(), st, f.fence); err != nil {
		t.Fatalf("create stack %s: %v", st.Ref, err)
	}

	return st.Ref
}

// create persists a new, not-started update of ref.
func (f fixture) create(t *testing.T, ref stack.Ref, id string, plan stack.Plan) stack.Update {
	t.Helper()

	u := stack.Update{
		CreatedAt: now(),
		StartedAt: time.Time{},
		EndedAt:   time.Time{},
		Lease:     stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}},
		ID:        stack.UpdateID(id),
		Kind:      plan.Kind,
		Status:    stack.StatusNotStarted,
		Ref:       ref,
		DryRun:    plan.DryRun,
		Version:   0,
		Revision:  1,
	}

	if err := f.s.CreateUpdate(t.Context(), u, f.fence); err != nil {
		t.Fatalf("create update %s: %v", id, err)
	}

	return u
}

// started returns u moved to running under a fresh lease, not yet persisted.
func started(t *testing.T, u stack.Update) stack.Update {
	t.Helper()

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new lease token: %v", err)
	}

	u.Status = stack.StatusRunning
	u.StartedAt = now().Add(time.Second)
	u.Lease = stack.Lease{ExpiresAt: now().Add(stack.DefaultLease), Digest: tok.Digest()}
	u.Revision++

	return u
}

// start persists u as started and returns it with its version.
func (f fixture) start(t *testing.T, u stack.Update) stack.Update {
	t.Helper()

	got, err := f.s.StartUpdate(t.Context(), started(t, u), f.fence)
	if err != nil {
		t.Fatalf("start update %s: %v", u.ID, err)
	}

	return got
}

// run creates and starts an update of ref.
func (f fixture) run(t *testing.T, ref stack.Ref, id string, plan stack.Plan) stack.Update {
	t.Helper()

	return f.start(t, f.create(t, ref, id, plan))
}

// ended returns u ended with status, not yet persisted.
func ended(u stack.Update, status stack.UpdateStatus) stack.Update {
	u.Status = status
	u.EndedAt = now().Add(time.Minute)
	u.Lease = stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}}
	u.Revision++

	return u
}

// end persists u as ended with status.
func (f fixture) end(t *testing.T, u stack.Update, status stack.UpdateStatus) stack.Update {
	t.Helper()

	u = ended(u, status)
	if err := f.s.SaveUpdate(t.Context(), u, f.fence); err != nil {
		t.Fatalf("end update %s: %v", u.ID, err)
	}

	return u
}

func (f fixture) stackVersion(t *testing.T, ref stack.Ref) int64 {
	t.Helper()

	st, err := f.s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", ref, err)
	}

	return st.Version
}

func mutating() stack.Plan { return stack.Plan{Kind: stack.KindUpdate, DryRun: false} }

func preview() stack.Plan { return stack.Plan{Kind: stack.KindPreview, DryRun: false} }

// dryRun is the preview phase of `pulumi up`: the real kind, not mutating.
func dryRun() stack.Plan { return stack.Plan{Kind: stack.KindUpdate, DryRun: true} }

func testUpdateRoundTrip(t *testing.T, f fixture) {
	t.Helper()

	want := f.create(t, f.ref, "u1", stack.Plan{Kind: stack.KindRefresh, DryRun: true})
	assertUpdate(t, f, want)

	want = f.start(t, want)
	assertUpdate(t, f, want)

	want = f.end(t, want, stack.StatusSucceeded)
	assertUpdate(t, f, want)
}

func testMissingUpdate(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.s.GetUpdate(t.Context(), f.ref, "nope"); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("get missing update: err = %v, want ErrUpdateNotFound", err)
	}

	f.create(t, f.ref, "u1", mutating())
	other := f.other(t)

	if _, err := f.s.GetUpdate(t.Context(), other, "u1"); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("get update through another stack: err = %v, want ErrUpdateNotFound", err)
	}

	if _, err := f.s.ListEvents(t.Context(), f.ref, "nope", 0, 10); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("list events of missing update: err = %v, want ErrUpdateNotFound", err)
	}
}

func testUpdateMissingStack(t *testing.T, f fixture) {
	t.Helper()

	ghost := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "ghost"}
	u := stack.Update{
		CreatedAt: now(),
		StartedAt: time.Time{},
		EndedAt:   time.Time{},
		Lease:     stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}},
		ID:        "u1",
		Kind:      stack.KindUpdate,
		Status:    stack.StatusNotStarted,
		Ref:       ghost,
		DryRun:    false,
		Version:   0,
		Revision:  1,
	}

	if err := f.s.CreateUpdate(t.Context(), u, f.fence); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("create update of missing stack: err = %v, want stack.ErrNotFound", err)
	}
}

func testUpdateWritesFenced(t *testing.T, f fixture) {
	t.Helper()

	stale := namespace.Fence{Namespace: f.fence.Namespace, FailoverVersion: f.fence.FailoverVersion + 1}
	running := f.run(t, f.ref, "running", mutating())
	waiting := f.create(t, f.other(t), "waiting", mutating())

	writes := map[string]func() error{
		"CreateUpdate": func() error {
			u := waiting
			u.ID = "fenced"

			return f.s.CreateUpdate(t.Context(), u, stale)
		},
		"StartUpdate": func() error {
			_, err := f.s.StartUpdate(t.Context(), started(t, waiting), stale)

			return err
		},
		"SaveUpdate": func() error {
			return f.s.SaveUpdate(t.Context(), ended(running, stack.StatusSucceeded), stale)
		},
		"PutCheckpoint": func() error {
			return f.s.PutCheckpoint(t.Context(), newCheckpoint(running, 0, `{}`), stale)
		},
		"AppendEvents": func() error {
			return f.s.AppendEvents(t.Context(), newEvents(running, 0), stale)
		},
	}

	for name, write := range writes {
		if err := write(); !errors.Is(err, namespace.ErrFenced) {
			t.Errorf("%s with stale fence: err = %v, want ErrFenced", name, err)
		}
	}

	if _, err := f.s.GetUpdate(t.Context(), f.ref, "fenced"); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Errorf("fenced update was created: err = %v, want ErrUpdateNotFound", err)
	}

	assertUpdate(t, f, running)
	assertUpdate(t, f, waiting)

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("fenced checkpoint was saved: err = %v, want ErrNoState", err)
	}

	assertEvents(t, f, running)
}

func testStartVersions(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	if first.Version != 1 {
		t.Fatalf("first update version = %d, want 1", first.Version)
	}

	if v := f.stackVersion(t, f.ref); v != 0 {
		t.Fatalf("stack version while the first update runs = %d, want 0", v)
	}

	f.end(t, first, stack.StatusFailed)

	if v := f.stackVersion(t, f.ref); v != 1 {
		t.Fatalf("stack version after a failed update = %d, want 1", v)
	}

	// A preview takes the stack's version, not a new one.
	if p := f.run(t, f.ref, "p1", preview()); p.Version != 1 {
		t.Fatalf("preview version = %d, want 1", p.Version)
	}

	second := f.run(t, f.ref, "u2", stack.Plan{Kind: stack.KindDestroy, DryRun: false})
	if second.Version != 2 {
		t.Fatalf("second update version = %d, want 2", second.Version)
	}

	// A canceled update keeps its number; the next one does not reuse it.
	f.end(t, second, stack.StatusCanceled)

	third := f.run(t, f.ref, "u3", mutating())
	if third.Version != 3 {
		t.Fatalf("third update version = %d, want 3", third.Version)
	}

	f.end(t, third, stack.StatusSucceeded)

	if v := f.stackVersion(t, f.ref); v != 3 {
		t.Fatalf("stack version after the third update = %d, want 3", v)
	}

	// Ending a preview leaves the stack alone; versions are per stack.
	f.end(t, f.run(t, f.ref, "p2", dryRun()), stack.StatusSucceeded)

	if v := f.stackVersion(t, f.ref); v != 3 {
		t.Fatalf("stack version after a preview = %d, want 3", v)
	}

	if u := f.run(t, f.other(t), "d1", mutating()); u.Version != 1 {
		t.Fatalf("first update of another stack: version = %d, want 1", u.Version)
	}
}

func testOneRunning(t *testing.T, f fixture) {
	t.Helper()

	running := f.run(t, f.ref, "u1", mutating())

	blocked := f.create(t, f.ref, "u2", stack.Plan{Kind: stack.KindRefresh, DryRun: false})
	if _, err := f.s.StartUpdate(t.Context(), started(t, blocked), f.fence); !errors.Is(err, stack.ErrUpdateConflict) {
		t.Fatalf("start a second mutating update: err = %v, want ErrUpdateConflict", err)
	}

	assertUpdate(t, f, blocked)

	// Previews and dry runs never wait, and other stacks are independent.
	f.run(t, f.ref, "p1", preview())
	f.run(t, f.ref, "p2", dryRun())
	f.run(t, f.other(t), "d1", mutating())

	f.end(t, running, stack.StatusSucceeded)

	if u := f.start(t, blocked); u.Version != 2 {
		t.Fatalf("start after the first ended: version = %d, want 2", u.Version)
	}
}

func testConcurrentStarts(t *testing.T, f fixture) {
	t.Helper()

	const n = 8

	pending := make([]stack.Update, n)
	for i := range pending {
		pending[i] = started(t, f.create(t, f.ref, fmt.Sprintf("u%d", i), mutating()))
	}

	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := range pending {
		wg.Go(func() {
			_, errs[i] = f.s.StartUpdate(t.Context(), pending[i], f.fence)
		})
	}

	wg.Wait()

	var won int

	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, stack.ErrUpdateConflict):
			t.Errorf("start u%d: err = %v, want nil or ErrUpdateConflict", i, err)
		default:
		}
	}

	if won != 1 {
		t.Fatalf("%d of %d concurrent starts succeeded, want 1", won, n)
	}

	active, err := f.s.ActiveUpdate(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("active update: %v", err)
	}

	if active.Version != 1 {
		t.Fatalf("winning update version = %d, want 1", active.Version)
	}
}

func testStaleRevision(t *testing.T, f fixture) {
	t.Helper()

	u := f.create(t, f.ref, "u1", mutating())

	skipped := started(t, u)
	skipped.Revision++

	if _, err := f.s.StartUpdate(t.Context(), skipped, f.fence); !errors.Is(err, stack.ErrConcurrentModification) {
		t.Fatalf("start with a skipped revision: err = %v, want ErrConcurrentModification", err)
	}

	running := f.start(t, u)

	renewed := running
	renewed.Lease.ExpiresAt = renewed.Lease.ExpiresAt.Add(time.Minute)
	renewed.Revision++

	if err := f.s.SaveUpdate(t.Context(), renewed, f.fence); err != nil {
		t.Fatalf("renew: %v", err)
	}

	// Ending from the pre-renewal read loses the compare-and-swap.
	stale := ended(running, stack.StatusSucceeded)
	if err := f.s.SaveUpdate(t.Context(), stale, f.fence); !errors.Is(err, stack.ErrConcurrentModification) {
		t.Fatalf("save from a stale read: err = %v, want ErrConcurrentModification", err)
	}

	assertUpdate(t, f, renewed)

	if v := f.stackVersion(t, f.ref); v != 0 {
		t.Fatalf("stack version after a lost save = %d, want 0", v)
	}
}

func testConcurrentSaves(t *testing.T, f fixture) {
	t.Helper()

	running := f.run(t, f.ref, "u1", mutating())
	statuses := []stack.UpdateStatus{
		stack.StatusSucceeded, stack.StatusFailed, stack.StatusCanceled,
		stack.StatusSucceeded, stack.StatusFailed, stack.StatusCanceled,
	}
	errs := make([]error, len(statuses))

	var wg sync.WaitGroup
	for i, status := range statuses {
		wg.Go(func() {
			errs[i] = f.s.SaveUpdate(t.Context(), ended(running, status), f.fence)
		})
	}

	wg.Wait()

	var won []stack.UpdateStatus

	for i, err := range errs {
		switch {
		case err == nil:
			won = append(won, statuses[i])
		case !errors.Is(err, stack.ErrConcurrentModification):
			t.Errorf("save %d: err = %v, want nil or ErrConcurrentModification", i, err)
		default:
		}
	}

	if len(won) != 1 {
		t.Fatalf("%d of %d concurrent saves succeeded, want 1", len(won), len(statuses))
	}

	assertUpdate(t, f, ended(running, won[0]))
}

func testActiveUpdate(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.s.ActiveUpdate(t.Context(), f.ref); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("active update of an idle stack: err = %v, want ErrUpdateNotFound", err)
	}

	f.create(t, f.ref, "waiting", mutating())
	f.run(t, f.ref, "p1", preview())
	f.run(t, f.ref, "p2", dryRun())
	f.run(t, f.other(t), "other", mutating())

	if _, err := f.s.ActiveUpdate(t.Context(), f.ref); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("active update with only previews running: err = %v, want ErrUpdateNotFound", err)
	}

	want := f.run(t, f.ref, "u1", mutating())

	got, err := f.s.ActiveUpdate(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("active update: %v", err)
	}

	if !sameUpdate(got, want) {
		t.Fatalf("active update = %+v, want %+v", got, want)
	}

	f.end(t, want, stack.StatusSucceeded)

	if _, err := f.s.ActiveUpdate(t.Context(), f.ref); !errors.Is(err, stack.ErrUpdateNotFound) {
		t.Fatalf("active update after it ended: err = %v, want ErrUpdateNotFound", err)
	}
}

func testListUpdates(t *testing.T, f fixture) {
	t.Helper()

	all := stack.Page{Limit: 100, Offset: 0}

	if list, err := f.s.ListUpdates(t.Context(), f.ref, all); err != nil || len(list) != 0 {
		t.Fatalf("history of a new stack = %v, %v; want empty", list, err)
	}

	ghost := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "ghost"}
	if list, err := f.s.ListUpdates(t.Context(), ghost, all); err != nil || len(list) != 0 {
		t.Fatalf("history of a missing stack = %v, %v; want empty", list, err)
	}

	f.end(t, f.run(t, f.ref, "u1", mutating()), stack.StatusSucceeded)
	f.end(t, f.run(t, f.ref, "p1", preview()), stack.StatusSucceeded)
	f.end(t, f.run(t, f.ref, "u2", stack.Plan{Kind: stack.KindRefresh, DryRun: false}), stack.StatusFailed)
	f.end(t, f.run(t, f.ref, "p2", dryRun()), stack.StatusSucceeded)
	f.end(t, f.run(t, f.ref, "u3", mutating()), stack.StatusCanceled)
	f.create(t, f.ref, "waiting", mutating())
	f.run(t, f.ref, "u4", stack.Plan{Kind: stack.KindImport, DryRun: false})
	f.end(t, f.run(t, f.other(t), "other", mutating()), stack.StatusSucceeded)

	for _, tc := range []struct {
		page stack.Page
		want []stack.UpdateID
	}{
		{page: all, want: []stack.UpdateID{"u4", "u3", "u2", "u1"}},
		{page: stack.Page{Limit: 2, Offset: 0}, want: []stack.UpdateID{"u4", "u3"}},
		{page: stack.Page{Limit: 2, Offset: 2}, want: []stack.UpdateID{"u2", "u1"}},
		{page: stack.Page{Limit: 2, Offset: 4}, want: []stack.UpdateID{}},
	} {
		list, err := f.s.ListUpdates(t.Context(), f.ref, tc.page)
		if err != nil {
			t.Fatalf("list updates %+v: %v", tc.page, err)
		}

		got := make([]stack.UpdateID, 0, len(list))
		for i := range list {
			got = append(got, list[i].ID)
		}

		if !slices.Equal(got, tc.want) {
			t.Errorf("list updates %+v = %v, want %v", tc.page, got, tc.want)
		}
	}
}

// newCheckpoint returns a checkpoint of u written under its current lease.
func newCheckpoint(u stack.Update, seq int64, deployment string) stack.Checkpoint {
	return stack.Checkpoint{
		SavedAt:       now().Add(2 * time.Second),
		Features:      []string{"deltaCheckpointUploadsV2", "verbatim"},
		Deployment:    []byte(deployment),
		Update:        u.ID,
		Ref:           u.Ref,
		Lease:         u.Lease.Digest,
		SchemaVersion: 3,
		Sequence:      seq,
		Digest:        sha256.Sum256([]byte(deployment)),
		Invalid:       false,
	}
}

func testCheckpointRoundTrip(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("latest checkpoint of a new stack: err = %v, want ErrNoState", err)
	}

	u := f.run(t, f.ref, "u1", mutating())

	// Not normalized JSON: key order, spacing and duplicates must survive.
	want := newCheckpoint(u, 0, "{\"b\": 1,  \"a\":[ ], \"b\":2}\n")
	want.Invalid = true

	if err := f.s.PutCheckpoint(t.Context(), want, f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	assertCheckpoint(t, f, f.ref, want)

	empty := newCheckpoint(u, 0, "")
	empty.Features = nil

	if err := f.s.PutCheckpoint(t.Context(), empty, f.fence); err != nil {
		t.Fatalf("put empty checkpoint: %v", err)
	}

	assertCheckpoint(t, f, f.ref, empty)

	if _, err := f.s.LatestCheckpoint(t.Context(), f.other(t)); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("latest checkpoint of another stack: err = %v, want ErrNoState", err)
	}
}

func testCheckpointLease(t *testing.T, f fixture) {
	t.Helper()

	waiting := f.create(t, f.ref, "waiting", mutating())
	if err := f.s.PutCheckpoint(t.Context(), newCheckpoint(waiting, 0, `{}`), f.fence); !errors.Is(
		err, stack.ErrWrongUpdateState,
	) {
		t.Fatalf("checkpoint of a not-started update: err = %v, want ErrWrongUpdateState", err)
	}

	u := f.run(t, f.ref, "u1", mutating())

	forged := newCheckpoint(u, 0, `{}`)
	forged.Lease = sha256.Sum256([]byte("another lease"))

	if err := f.s.PutCheckpoint(t.Context(), forged, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("checkpoint under another lease: err = %v, want ErrWrongUpdateState", err)
	}

	// Authorized while running, written after a cancel: rejected.
	late := newCheckpoint(u, 0, `{}`)
	f.end(t, u, stack.StatusCanceled)

	if err := f.s.PutCheckpoint(t.Context(), late, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("checkpoint after cancel: err = %v, want ErrWrongUpdateState", err)
	}

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("a rejected checkpoint was saved: err = %v, want ErrNoState", err)
	}
}

func testCheckpointSequence(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	put := func(seq int64, body string) error {
		return f.s.PutCheckpoint(t.Context(), newCheckpoint(u, seq, body), f.fence)
	}

	if err := put(2, `{"n":2}`); err != nil {
		t.Fatalf("put sequence 2: %v", err)
	}

	if err := put(2, `{"n":2}`); err != nil {
		t.Fatalf("retry sequence 2: err = %v, want nil", err)
	}

	if err := put(2, `{"n":"other"}`); !errors.Is(err, stack.ErrCheckpointConflict) {
		t.Fatalf("different content at sequence 2: err = %v, want ErrCheckpointConflict", err)
	}

	if err := put(1, `{"n":1}`); !errors.Is(err, stack.ErrStaleCheckpoint) {
		t.Fatalf("sequence 1 after 2: err = %v, want ErrStaleCheckpoint", err)
	}

	assertCheckpoint(t, f, f.ref, newCheckpoint(u, 2, `{"n":2}`))

	if err := put(3, `{"n":3}`); err != nil {
		t.Fatalf("put sequence 3: %v", err)
	}

	// Without a sequence number the last write wins.
	if err := put(0, `{"n":"a"}`); err != nil {
		t.Fatalf("put without sequence: %v", err)
	}

	if err := put(0, `{"n":"b"}`); err != nil {
		t.Fatalf("put without sequence again: %v", err)
	}

	assertCheckpoint(t, f, f.ref, newCheckpoint(u, 0, `{"n":"b"}`))
}

func testCheckpointAcrossUpdates(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	if err := f.s.PutCheckpoint(t.Context(), newCheckpoint(first, 5, `{"from":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, first, stack.StatusSucceeded)

	// Sequence numbers belong to an update: a new update starts again at 1.
	second := f.run(t, f.ref, "u2", mutating())
	want := newCheckpoint(second, 1, `{"from":2}`)

	if err := f.s.PutCheckpoint(t.Context(), want, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	assertCheckpoint(t, f, f.ref, want)

	other := f.run(t, f.other(t), "d1", mutating())
	if err := f.s.PutCheckpoint(t.Context(), newCheckpoint(other, 9, `{"from":"dev"}`), f.fence); err != nil {
		t.Fatalf("put checkpoint of another stack: %v", err)
	}

	assertCheckpoint(t, f, f.ref, want)
}

// testCheckpointLatestIsHighest checks that the latest checkpoint is the one
// with the highest version, and that an update without a checkpoint does not
// hide the one before it.
func testCheckpointLatestIsHighest(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	firstCP := newCheckpoint(first, 0, `{"v":1}`)

	if err := f.s.PutCheckpoint(t.Context(), firstCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, first, stack.StatusSucceeded)

	second := f.run(t, f.ref, "u2", mutating())
	secondCP := newCheckpoint(second, 0, `{"v":2}`)

	if err := f.s.PutCheckpoint(t.Context(), secondCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	f.end(t, second, stack.StatusSucceeded)

	assertCheckpoint(t, f, f.ref, secondCP)

	// An update that saves no checkpoint leaves the latest one in place.
	third := f.run(t, f.ref, "u3", mutating())
	f.end(t, third, stack.StatusSucceeded)

	assertCheckpoint(t, f, f.ref, secondCP)
}

// testCheckpointByVersion checks that version N returns only N: no fallback
// to an older checkpoint, and ErrNoState for a missing or empty version.
func testCheckpointByVersion(t *testing.T, f fixture) {
	t.Helper()

	first := f.run(t, f.ref, "u1", mutating())
	firstCP := newCheckpoint(first, 0, `{"v":1}`)

	if err := f.s.PutCheckpoint(t.Context(), firstCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, first, stack.StatusSucceeded)

	second := f.run(t, f.ref, "u2", mutating())
	secondCP := newCheckpoint(second, 0, `{"v":2}`)

	if err := f.s.PutCheckpoint(t.Context(), secondCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	f.end(t, second, stack.StatusSucceeded)

	assertCheckpointVersion(t, f, f.ref, 1, firstCP)
	assertCheckpointVersion(t, f, f.ref, 2, secondCP)

	for _, version := range []int64{0, 3, 99} {
		if _, err := f.s.CheckpointByVersion(t.Context(), f.ref, version); !errors.Is(err, stack.ErrNoState) {
			t.Errorf("checkpoint of missing version %d: err = %v, want ErrNoState", version, err)
		}
	}

	// An update without a checkpoint is empty, not a fallback.
	third := f.run(t, f.ref, "u3", mutating())
	f.end(t, third, stack.StatusSucceeded)

	if _, err := f.s.CheckpointByVersion(t.Context(), f.ref, third.Version); !errors.Is(
		err, stack.ErrNoState,
	) {
		t.Errorf("checkpoint of empty version %d: err = %v, want ErrNoState", third.Version, err)
	}

	assertCheckpointVersion(t, f, f.ref, 1, firstCP)
	assertCheckpointVersion(t, f, f.ref, 2, secondCP)

	ghost := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "ghost"}
	if _, err := f.s.CheckpointByVersion(t.Context(), ghost, 1); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("checkpoint of a missing stack: err = %v, want ErrNoState", err)
	}
}

// testCheckpointPreviewRejected checks that a preview or a dry run saves no
// checkpoint.
func testCheckpointPreviewRejected(t *testing.T, f fixture) {
	t.Helper()

	for name, plan := range map[string]stack.Plan{
		"preview": preview(),
		"dry run": dryRun(),
	} {
		u := f.run(t, f.ref, name, plan)
		if err := f.s.PutCheckpoint(t.Context(), newCheckpoint(u, 0, `{}`), f.fence); !errors.Is(
			err, stack.ErrWrongUpdateState,
		) {
			t.Errorf("%s checkpoint: err = %v, want ErrWrongUpdateState", name, err)
		}

		f.end(t, u, stack.StatusSucceeded)
	}

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("latest checkpoint after rejected previews: err = %v, want ErrNoState", err)
	}
}

// testCheckpointKeepsAfterEnd checks that a canceled or failed update keeps
// the last checkpoint it saved.
func testCheckpointKeepsAfterEnd(t *testing.T, f fixture) {
	t.Helper()

	failed := f.run(t, f.ref, "u1", mutating())
	failedCP := newCheckpoint(failed, 0, `{"v":1}`)

	if err := f.s.PutCheckpoint(t.Context(), failedCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u1: %v", err)
	}

	f.end(t, failed, stack.StatusFailed)
	assertCheckpoint(t, f, f.ref, failedCP)
	assertCheckpointVersion(t, f, f.ref, failed.Version, failedCP)

	canceled := f.run(t, f.ref, "u2", mutating())
	canceledCP := newCheckpoint(canceled, 0, `{"v":2}`)

	if err := f.s.PutCheckpoint(t.Context(), canceledCP, f.fence); err != nil {
		t.Fatalf("put checkpoint of u2: %v", err)
	}

	f.end(t, canceled, stack.StatusCanceled)
	assertCheckpoint(t, f, f.ref, canceledCP)
	assertCheckpointVersion(t, f, f.ref, canceled.Version, canceledCP)
}

// testDeleteStackDeletesCheckpoints checks that the final delete of a stack
// removes its checkpoints.
func testDeleteStackDeletesCheckpoints(t *testing.T, f fixture) {
	t.Helper()

	deleter, ok := f.s.(DeleteStore)
	if !ok {
		t.Skip("store does not implement DeleteStackForTest until issue #49")
	}

	u := f.run(t, f.ref, "u1", mutating())
	if err := f.s.PutCheckpoint(t.Context(), newCheckpoint(u, 0, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	f.end(t, u, stack.StatusSucceeded)

	if err := deleter.DeleteStackForTest(t.Context(), f.ref, f.fence); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	if _, err := f.s.LatestCheckpoint(t.Context(), f.ref); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("latest checkpoint after delete: err = %v, want ErrNoState", err)
	}

	if _, err := f.s.CheckpointByVersion(t.Context(), f.ref, u.Version); !errors.Is(err, stack.ErrNoState) {
		t.Errorf("checkpoint of version %d after delete: err = %v, want ErrNoState", u.Version, err)
	}
}

// newEvents returns a batch for u under its current lease, one event per
// sequence number, with the payload naming the number and the given round.
func newEvents(u stack.Update, round int, seqs ...int64) stack.EventBatch {
	events := make([]stack.Event, 0, len(seqs))
	for _, seq := range seqs {
		events = append(events, stack.Event{
			Timestamp: now().Add(time.Duration(seq) * time.Millisecond),
			Payload:   fmt.Appendf(nil, "{\"sequence\": %d, \"round\":%d}", seq, round),
			Sequence:  seq,
		})
	}

	return stack.EventBatch{Events: events, Ref: u.Ref, Update: u.ID, Lease: u.Lease.Digest}
}

func testEvents(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	if got, err := f.s.ListEvents(t.Context(), f.ref, u.ID, -1, 10); err != nil || len(got) != 0 {
		t.Fatalf("events of a new update = %v, %v; want empty", got, err)
	}

	for _, batch := range []stack.EventBatch{
		newEvents(u, 1, 0, 1, 2),
		newEvents(u, 2, 2, 3, 5), // retried 2 is skipped, the first write wins
		newEvents(u, 3, 4),
		newEvents(u, 4),
	} {
		if err := f.s.AppendEvents(t.Context(), batch, f.fence); err != nil {
			t.Fatalf("append events: %v", err)
		}
	}

	other := f.run(t, f.other(t), "d1", mutating())
	if err := f.s.AppendEvents(t.Context(), newEvents(other, 9, 0, 1), f.fence); err != nil {
		t.Fatalf("append events of another update: %v", err)
	}

	want := slices.Concat(newEvents(u, 1, 0, 1, 2).Events, newEvents(u, 2, 3).Events,
		newEvents(u, 3, 4).Events, newEvents(u, 2, 5).Events)

	for _, tc := range []struct {
		want  []stack.Event
		after int64
		limit int
	}{
		{after: -1, limit: 100, want: want},
		{after: -1, limit: 2, want: want[:2]},
		{after: 1, limit: 3, want: want[2:5]},
		{after: 5, limit: 10, want: nil},
	} {
		got, err := f.s.ListEvents(t.Context(), f.ref, u.ID, tc.after, tc.limit)
		if err != nil {
			t.Fatalf("list events after %d: %v", tc.after, err)
		}

		if !sameEvents(got, tc.want) {
			t.Errorf(
				"events after %d, limit %d = %s, want %s",
				tc.after,
				tc.limit,
				eventsString(got),
				eventsString(tc.want),
			)
		}
	}

	// Events stay readable after the update ends.
	f.end(t, u, stack.StatusSucceeded)

	if got, err := f.s.ListEvents(t.Context(), f.ref, u.ID, -1, 100); err != nil || !sameEvents(got, want) {
		t.Fatalf("events after the update ended = %s, %v; want %s", eventsString(got), err, eventsString(want))
	}
}

func testEventsLease(t *testing.T, f fixture) {
	t.Helper()

	waiting := f.create(t, f.ref, "waiting", mutating())
	if err := f.s.AppendEvents(t.Context(), newEvents(waiting, 1, 0), f.fence); !errors.Is(
		err, stack.ErrWrongUpdateState,
	) {
		t.Fatalf("events of a not-started update: err = %v, want ErrWrongUpdateState", err)
	}

	u := f.run(t, f.ref, "u1", mutating())

	forged := newEvents(u, 1, 0)
	forged.Lease = sha256.Sum256([]byte("another lease"))

	if err := f.s.AppendEvents(t.Context(), forged, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("events under another lease: err = %v, want ErrWrongUpdateState", err)
	}

	late := newEvents(u, 1, 0)
	f.end(t, u, stack.StatusCanceled)

	if err := f.s.AppendEvents(t.Context(), late, f.fence); !errors.Is(err, stack.ErrWrongUpdateState) {
		t.Fatalf("events after cancel: err = %v, want ErrWrongUpdateState", err)
	}

	assertEvents(t, f, u)
}

// testRenameKeepsHistory shows that a rename keeps the stack ID, so its
// history, checkpoint and events stay with the stack.
func testRenameKeepsHistory(t *testing.T, f fixture) {
	t.Helper()

	ended, wantCP, wantEvents := seedRenameHistory(t, f)
	oldID := f.stackID(t, f.ref)
	newRef := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "renamed"}

	if err := f.s.RenameStack(t.Context(), f.ref, "renamed", f.fence); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	checkRenamedStack(t, f, newRef, oldID)
	checkRenamedUpdate(t, f, newRef, ended)
	checkRenamedHistory(t, f, newRef, ended)
	checkRenamedCheckpoint(t, f, newRef, wantCP)
	checkRenamedEvents(t, f, newRef, ended, wantEvents)

	next := f.run(t, newRef, "u2", mutating())
	if next.Version != 2 {
		t.Fatalf("update after rename version = %d, want 2", next.Version)
	}
}

// seedRenameHistory creates one finished update with a checkpoint and events.
func seedRenameHistory(
	t *testing.T, f fixture,
) (stack.Update, stack.Checkpoint, stack.EventBatch) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())

	wantCP := newCheckpoint(u, 0, `{"v":1}`)
	if err := f.s.PutCheckpoint(t.Context(), wantCP, f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	wantEvents := newEvents(u, 1, 0, 1)
	if err := f.s.AppendEvents(t.Context(), wantEvents, f.fence); err != nil {
		t.Fatalf("append events: %v", err)
	}

	ended := f.end(t, u, stack.StatusSucceeded)

	return ended, wantCP, wantEvents
}

// checkRenamedStack checks the ID stayed and the old name is gone.
func checkRenamedStack(t *testing.T, f fixture, newRef stack.Ref, oldID string) {
	t.Helper()

	gotStack, err := f.s.GetStack(t.Context(), newRef)
	if err != nil {
		t.Fatalf("get renamed stack: %v", err)
	}

	if gotStack.ID.String() != oldID {
		t.Fatalf("renamed stack ID = %q, want %q", gotStack.ID, oldID)
	}

	_, oldErr := f.s.GetStack(t.Context(), f.ref)
	if !errors.Is(oldErr, stack.ErrNotFound) {
		t.Fatalf("get old stack: err = %v, want ErrNotFound", oldErr)
	}
}

// checkRenamedUpdate checks the update moved to the new name.
func checkRenamedUpdate(t *testing.T, f fixture, newRef stack.Ref, ended stack.Update) {
	t.Helper()

	gotUpdate, err := f.s.GetUpdate(t.Context(), newRef, ended.ID)
	if err != nil {
		t.Fatalf("get update after rename: %v", err)
	}

	ended.Ref = newRef

	if !sameUpdate(gotUpdate, ended) {
		t.Fatalf("update after rename = %+v, want %+v", gotUpdate, ended)
	}

	_, oldErr := f.s.GetUpdate(t.Context(), f.ref, ended.ID)
	if !errors.Is(oldErr, stack.ErrUpdateNotFound) {
		t.Fatalf("get update through old name: err = %v, want ErrUpdateNotFound", oldErr)
	}
}

// checkRenamedHistory checks the history moved to the new name.
func checkRenamedHistory(t *testing.T, f fixture, newRef stack.Ref, ended stack.Update) {
	t.Helper()

	history, err := f.s.ListUpdates(t.Context(), newRef, stack.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("history after rename: %v", err)
	}

	if len(history) != 1 || history[0].ID != ended.ID || history[0].Ref != newRef {
		t.Fatalf("history after rename = %+v, want one update of %s", history, newRef)
	}

	oldHistory, oldErr := f.s.ListUpdates(t.Context(), f.ref, stack.Page{Limit: 100, Offset: 0})
	if oldErr != nil || len(oldHistory) != 0 {
		t.Fatalf("history of old name = %v, %v; want empty", oldHistory, oldErr)
	}
}

// checkRenamedCheckpoint checks the checkpoint moved to the new name.
func checkRenamedCheckpoint(t *testing.T, f fixture, newRef stack.Ref, wantCP stack.Checkpoint) {
	t.Helper()

	wantCP.Ref = newRef

	assertCheckpoint(t, fixture{s: f.s, ref: newRef, fence: f.fence}, newRef, wantCP)

	_, oldErr := f.s.LatestCheckpoint(t.Context(), f.ref)
	if !errors.Is(oldErr, stack.ErrNoState) {
		t.Fatalf("checkpoint of old name: err = %v, want ErrNoState", oldErr)
	}
}

// checkRenamedEvents checks the events moved to the new name.
func checkRenamedEvents(
	t *testing.T, f fixture, newRef stack.Ref, ended stack.Update, wantEvents stack.EventBatch,
) {
	t.Helper()

	gotEvents, err := f.s.ListEvents(t.Context(), newRef, ended.ID, -1, 100)
	if err != nil {
		t.Fatalf("events after rename: %v", err)
	}

	if !sameEvents(gotEvents, wantEvents.Events) {
		t.Fatalf(
			"events after rename = %s, want %s",
			eventsString(gotEvents),
			eventsString(wantEvents.Events),
		)
	}

	_, oldErr := f.s.ListEvents(t.Context(), f.ref, ended.ID, -1, 100)
	if !errors.Is(oldErr, stack.ErrUpdateNotFound) {
		t.Fatalf("events of old name: err = %v, want ErrUpdateNotFound", oldErr)
	}
}

func (f fixture) stackID(t *testing.T, ref stack.Ref) string {
	t.Helper()

	st, err := f.s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack %s: %v", ref, err)
	}

	return st.ID.String()
}

func assertUpdate(t *testing.T, f fixture, want stack.Update) {
	t.Helper()

	got, err := f.s.GetUpdate(t.Context(), want.Ref, want.ID)
	if err != nil {
		t.Fatalf("get update %s: %v", want.ID, err)
	}

	if !sameUpdate(got, want) {
		t.Fatalf("update %s = %+v, want %+v", want.ID, got, want)
	}
}

func sameUpdate(a, b stack.Update) bool {
	return a.ID == b.ID && a.Ref == b.Ref && a.Kind == b.Kind && a.Status == b.Status &&
		a.DryRun == b.DryRun && a.Version == b.Version && a.Revision == b.Revision &&
		a.Lease.Digest == b.Lease.Digest && sameTime(a.Lease.ExpiresAt, b.Lease.ExpiresAt) &&
		sameTime(a.CreatedAt, b.CreatedAt) && sameTime(a.StartedAt, b.StartedAt) && sameTime(a.EndedAt, b.EndedAt)
}

// sameTime compares instants, and requires a zero time to stay zero.
func sameTime(a, b time.Time) bool {
	return a.IsZero() == b.IsZero() && a.Equal(b)
}

func assertCheckpoint(t *testing.T, f fixture, ref stack.Ref, want stack.Checkpoint) {
	t.Helper()

	got, err := f.s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint of %s: %v", ref, err)
	}

	assertSameCheckpoint(t, got, want, "latest checkpoint")
}

// assertCheckpointVersion checks the checkpoint of one version.
func assertCheckpointVersion(t *testing.T, f fixture, ref stack.Ref, version int64, want stack.Checkpoint) {
	t.Helper()

	got, err := f.s.CheckpointByVersion(t.Context(), ref, version)
	if err != nil {
		t.Fatalf("checkpoint of version %d of %s: %v", version, ref, err)
	}

	assertSameCheckpoint(t, got, want, "checkpoint of version")
}

func assertSameCheckpoint(t *testing.T, got, want stack.Checkpoint, what string) {
	t.Helper()

	same := got.Ref == want.Ref && got.Update == want.Update && got.Lease == want.Lease &&
		got.SchemaVersion == want.SchemaVersion && got.Sequence == want.Sequence &&
		got.Digest == want.Digest && got.Invalid == want.Invalid && sameTime(got.SavedAt, want.SavedAt) &&
		bytes.Equal(got.Deployment, want.Deployment) && slices.Equal(got.Features, want.Features)
	if !same {
		t.Fatalf("%s = %+v (deployment %q), want %+v (deployment %q)",
			what, got, got.Deployment, want, want.Deployment)
	}
}

// assertEvents checks that update u has no events.
func assertEvents(t *testing.T, f fixture, u stack.Update) {
	t.Helper()

	got, err := f.s.ListEvents(t.Context(), u.Ref, u.ID, -1, 100)
	if err != nil {
		t.Fatalf("list events of %s: %v", u.ID, err)
	}

	if len(got) != 0 {
		t.Fatalf("events of %s = %s, want none", u.ID, eventsString(got))
	}
}

func sameEvents(a, b []stack.Event) bool {
	return slices.EqualFunc(a, b, func(x, y stack.Event) bool {
		return x.Sequence == y.Sequence && sameTime(x.Timestamp, y.Timestamp) && bytes.Equal(x.Payload, y.Payload)
	})
}

func eventsString(events []stack.Event) string {
	var b bytes.Buffer

	for _, e := range events {
		fmt.Fprintf(&b, "[%d %s] ", e.Sequence, e.Payload)
	}

	return b.String()
}
