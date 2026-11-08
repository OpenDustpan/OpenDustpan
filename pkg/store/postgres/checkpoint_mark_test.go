package postgres_test

import (
	"context"
	"crypto/sha256"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// markFixture is a store holding namespace "acme" and stack "acme/web/prod"
// for the tests of the superseded marks. now is the time on the clock of the
// database when the fixture opens: the store checks the start of a write
// attempt against that clock.
type markFixture struct {
	now     time.Time
	store   *postgres.Store
	ref     stack.Ref
	fence   namespace.Fence
	nsID    namespace.ID
	stackID stack.ID
}

// openMarkFixture returns a fresh store with one namespace and stack.
func openMarkFixture(t *testing.T) *markFixture {
	t.Helper()

	ctx := t.Context()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	store := open(t, base)

	now, err := store.DatabaseNow(ctx)
	if err != nil {
		t.Fatalf("read the clock of the database: %v", err)
	}

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if createErr := store.CreateNamespace(ctx, ns); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	stackID := stack.ID(ident.NewID("stk-"))
	st := stack.Stack{
		NamespaceID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        map[string]string{},
		Ref:         ref,
		ID:          stackID,
		Version:     0,
	}
	if err = store.CreateStack(ctx, st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	return &markFixture{now: now, store: store, ref: ref, fence: ns.Fence(), nsID: ns.ID, stackID: stackID}
}

// startUpdate creates and starts an update of the fixture stack.
func (f *markFixture) startUpdate(t *testing.T, id string) stack.Update {
	t.Helper()

	ctx := t.Context()
	u := stack.Update{
		TokenID:         "",
		JournalVersion:  0,
		CreatedAt:       f.now,
		StartedAt:       time.Time{},
		EndedAt:         time.Time{},
		Lease:           stack.Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}},
		ID:              stack.UpdateID(id),
		Kind:            stack.KindUpdate,
		Status:          stack.StatusNotStarted,
		Ref:             f.ref,
		DryRun:          false,
		Version:         0,
		Revision:        1,
		CreatedBy:       "",
		RestoredFrom:    0,
		CheckpointBytes: 0,
	}
	if err := f.store.CreateUpdate(ctx, u, storetest.UnrecordedKey(), f.fence); err != nil {
		t.Fatalf("create update %s: %v", id, err)
	}

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new lease token: %v", err)
	}

	u.Status = stack.StatusRunning
	u.StartedAt = f.now
	u.Lease = stack.Lease{ExpiresAt: f.now.Add(stack.DefaultLease), Digest: tok.Digest()}
	u.Revision++

	started, err := f.store.StartUpdate(ctx, u, f.fence)
	if err != nil {
		t.Fatalf("start update %s: %v", id, err)
	}

	return started
}

// checkpoint returns a save of u under the blob key of one attempt.
func (f *markFixture) checkpoint(
	u stack.Update, seq int64, digest [sha256.Size]byte, attempt string,
) stack.Checkpoint {
	dir := "namespaces/" + f.nsID.String() + "/pulumi/" + f.stackID.String() + "/" + u.ID.String() + "/"

	return stack.Checkpoint{
		SavedAt:          f.now,
		Features:         nil,
		Deployment:       nil,
		Update:           u.ID,
		Ref:              u.Ref,
		NamespaceID:      "",
		StackID:          "",
		Lease:            u.Lease.Digest,
		SchemaVersion:    3,
		Sequence:         seq,
		Digest:           digest,
		Invalid:          false,
		BlobKey:          blob.Key(dir + strconv.FormatInt(seq, 10) + "-" + attempt),
		BlobSize:         0,
		BlobMD5:          [16]byte{},
		AttemptStartedAt: f.now,
	}
}

// save saves a checkpoint of u and returns it.
func (f *markFixture) save(
	t *testing.T, u stack.Update, seq int64, deployment, attempt string,
) stack.Checkpoint {
	t.Helper()

	cp := f.checkpoint(u, seq, sha256.Sum256([]byte(deployment)), attempt)
	if _, err := f.store.PutCheckpoint(t.Context(), cp, f.fence); err != nil {
		t.Fatalf("save checkpoint %d of attempt %s: %v", seq, attempt, err)
	}

	return cp
}

// markRead selects the key that the sequence check read in a test case.
type markRead int

const (
	markReadReplaced markRead = iota
	markReadLive
)

// TestSupersededMarkUsesTheReadKey checks that the mark names only the row
// that the sequence check read. A row that changed after the read is not
// marked.
func TestSupersededMarkUsesTheReadKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		read markRead
	}{
		{name: "the read key of the replaced row marks nothing new", read: markReadReplaced},
		{name: "the read key of the live row marks that row", read: markReadLive},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSupersededMarkUsesTheReadKey(t, tc.read)
		})
	}
}

// checkSupersededMarkUsesTheReadKey runs one case of the test of the read key.
func checkSupersededMarkUsesTheReadKey(t *testing.T, read markRead) {
	t.Helper()

	f := openMarkFixture(t)
	ctx := t.Context()
	u := f.startUpdate(t, "mark")

	old := f.save(t, u, 1, "old", "attemptold")
	live := f.save(t, u, 2, "live", "attemptliv")
	next := f.checkpoint(u, 3, sha256.Sum256([]byte("next")), "attemptnxt")

	readKey := old.BlobKey
	want := []blob.Key{old.BlobKey}

	if read == markReadLive {
		readKey = live.BlobKey
		want = append(want, live.BlobKey)
	}

	if err := postgres.MarkSupersededCheckpointInTx(ctx, f.store, f.stackID, next, readKey); err != nil {
		t.Fatalf("mark superseded checkpoint: %v", err)
	}

	marks, err := f.store.SupersededBlobs(ctx, f.stackID)
	if err != nil {
		t.Fatalf("read the marks: %v", err)
	}

	assertMarkKeys(t, marks, want)
}

// saveResult is the answer of one save on its goroutine.
type saveResult struct {
	err   error
	known bool
}

// saveKind is the answer that a save must give in a test case.
type saveKind int

const (
	saveNew saveKind = iota
	saveReplay
)

// startSave runs one checkpoint save and returns the channel of its answer.
func startSave(
	ctx context.Context, store *postgres.Store, cp stack.Checkpoint, fence namespace.Fence,
) <-chan saveResult {
	done := make(chan saveResult, 1)

	go func() {
		known, err := store.PutCheckpoint(ctx, cp, fence)
		done <- saveResult{err: err, known: known}
	}()

	return done
}

// holdSecondSave sets the hook that holds the first save after its sequence
// check, and returns the release function and the channel that the hook closes.
func holdSecondSave(
	ctx context.Context, t *testing.T, store *postgres.Store,
) (func(), <-chan struct{}) {
	t.Helper()

	held := make(chan struct{})
	release := make(chan struct{})

	var (
		once  sync.Once
		first atomic.Bool
	)

	releaseHeld := func() { once.Do(func() { close(release) }) }

	postgres.SetAfterSequenceCheck(store, func(string) {
		if !first.CompareAndSwap(false, true) {
			return
		}

		close(held)

		select {
		case <-release:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { postgres.SetAfterSequenceCheck(store, nil) })

	return releaseHeld, held
}

// waitForHook waits until the held save reaches the hook.
func waitForHook(ctx context.Context, t *testing.T, held <-chan struct{}, heldDone <-chan saveResult) {
	t.Helper()

	select {
	case <-held:
	case result := <-heldDone:
		t.Fatalf("the held save ended before the hook: known %t, err %v", result.known, result.err)
	case <-ctx.Done():
		t.Fatalf("wait for the held save: %v", ctx.Err())
	}
}

// waitForSave waits for one save and checks its answer.
func waitForSave(ctx context.Context, t *testing.T, done <-chan saveResult, kind saveKind, name string) {
	t.Helper()

	wantKnown := kind == saveReplay

	select {
	case result := <-done:
		if result.err != nil {
			t.Errorf("%s: %v", name, result.err)
		}

		if result.known != wantKnown {
			t.Errorf("%s: known %t, want %t", name, result.known, wantKnown)
		}
	case <-ctx.Done():
		t.Fatalf("%s: %v", name, ctx.Err())
	}
}

// assertConcurrentMark checks the row and the marks after the race.
func assertConcurrentMark(
	t *testing.T, f *markFixture, u stack.Update, old, commitSave stack.Checkpoint,
) {
	t.Helper()

	ctx := t.Context()

	cp, err := f.store.CheckpointByUpdate(ctx, f.ref, u.ID)
	if err != nil {
		t.Fatalf("read the checkpoint: %v", err)
	}

	if cp.BlobKey != commitSave.BlobKey {
		t.Fatalf("the row names %s, want %s", cp.BlobKey, commitSave.BlobKey)
	}

	marks, err := f.store.SupersededBlobs(ctx, f.stackID)
	if err != nil {
		t.Fatalf("read the marks: %v", err)
	}

	if len(marks) != 1 || marks[0].Key != old.BlobKey {
		t.Fatalf("marks = %+v, want only %s", marks, old.BlobKey)
	}
}

// TestSupersededMarkOfAConcurrentSave runs the race of two attempts of one
// save. The held attempt reads the old row, the other attempt replaces it and
// commits, and then the held attempt marks. It must not mark the live row.
func TestSupersededMarkOfAConcurrentSave(t *testing.T) {
	t.Parallel()

	f := openMarkFixture(t)
	ctx := t.Context()
	u := f.startUpdate(t, "interleave")

	old := f.save(t, u, 1, "old", "attemptold")

	digest := sha256.Sum256([]byte("same"))
	commitSave := f.checkpoint(u, 2, digest, "attemptfst")
	heldSave := f.checkpoint(u, 2, digest, "attemptsnd")

	release, held := holdSecondSave(ctx, t, f.store)
	defer release()

	heldDone := startSave(ctx, f.store, heldSave, f.fence)
	waitForHook(ctx, t, held, heldDone)

	commitDone := startSave(ctx, f.store, commitSave, f.fence)
	waitForSave(ctx, t, commitDone, saveNew, "the save that commits")

	release()
	waitForSave(ctx, t, heldDone, saveReplay, "the held save")

	assertConcurrentMark(t, f, u, old, commitSave)
}

// assertMarkKeys checks the keys of the marks, in order.
func assertMarkKeys(t *testing.T, marks []stack.SupersededBlob, want []blob.Key) {
	t.Helper()

	got := make([]blob.Key, 0, len(marks))
	for i := range marks {
		got = append(got, marks[i].Key)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("marks = %v, want %v", got, want)
	}
}
