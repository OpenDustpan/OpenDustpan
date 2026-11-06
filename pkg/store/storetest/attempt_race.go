package storetest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// stallingCheckpoints holds the first PutCheckpoint after its blob write,
// before the store sees it, until the test releases it. It is a save whose
// commit stalls, for example in a long pause of the process.
type stallingCheckpoints struct {
	stack.CheckpointStore

	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

// PutCheckpoint implements stack.CheckpointStore.
func (s *stallingCheckpoints) PutCheckpoint(
	ctx context.Context, cp stack.Checkpoint, fence namespace.Fence,
) (bool, error) {
	first := false
	s.once.Do(func() { first = true })

	if first {
		close(s.reached)

		select {
		case <-s.release:
		case <-ctx.Done():
			return false, fmt.Errorf("wait for the release of the stalled commit: %w", ctx.Err())
		}
	}

	known, err := s.CheckpointStore.PutCheckpoint(ctx, cp, fence)
	if err != nil {
		return false, fmt.Errorf("put checkpoint: %w", err)
	}

	return known, nil
}

// errNoClock reports a wrapped store that does not read the clock of the
// database.
var errNoClock = errors.New("the wrapped store does not read the clock of the database")

// DatabaseNow forwards to the wrapped store, whose method the embedded
// CheckpointStore interface hides, so the sweeps of the case read the clock
// of the database.
func (s *stallingCheckpoints) DatabaseNow(ctx context.Context) (time.Time, error) {
	clock, ok := s.CheckpointStore.(blob.DatabaseClock)
	if !ok {
		return time.Time{}, errNoClock
	}

	now, err := clock.DatabaseNow(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("database now: %w", err)
	}

	return now, nil
}

// testStalledSaveAfterSweep runs the race between a sweep and a save whose
// commit stalls. The save writes its blob and stalls before its commit. The
// blob gets older than the grace period, and a second save sweeps the update
// and deletes the blob, because no row names it. The stalled commit then
// arrives. The store must refuse it with blob.ErrAttemptExpired: it must never
// commit a row that names the deleted blob.
func testStalledSaveAfterSweep(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c := r.newStackCase(t)

	stall := &stallingCheckpoints{
		CheckpointStore: r.store, reached: make(chan struct{}), release: make(chan struct{}), once: sync.Once{},
	}

	// The clock of the service is behind for the stalled save: its attempt
	// started more than the grace period before the clock of the database.
	var shift atomic.Int64

	shift.Store(int64(-blob.UnnamedGracePeriod - time.Minute))

	updates := r.raceUpdates(t, stall, func() time.Time { return time.Now().Add(time.Duration(shift.Load())) })

	stalled := make(chan error, 1)

	go func() {
		stalled <- updates.SaveCheckpoint(context.WithoutCancel(t.Context()), c.ref, c.started.Update.ID,
			c.started.Token, stack.CheckpointInput{
				Features: nil, Deployment: []byte(`{"deployment":{"n":2}}`), SchemaVersion: 3, Sequence: 2,
				Invalid: false,
			})
	}()

	select {
	case <-stall.reached:
	case err := <-stalled:
		t.Fatalf("the stalled save ended before its commit: %v", err)
	case <-time.After(backupWaitLimit):
		t.Fatal("the stalled save did not reach its commit")
	}

	stalledKeys := r.listUpdateKeys(t, c)
	if len(stalledKeys) != 1 {
		t.Fatalf("blobs of the update before the sweep = %v, want the one blob of the stalled save", stalledKeys)
	}

	// The blob of the stalled save is now older than the grace period. The
	// next save commits and sweeps the blobs of the update.
	shift.Store(0)
	r.ageBlobs(t)
	r.saveWith(t, updates, c, 1)
	r.requireDeleted(t, stalledKeys, "after the sweep")

	close(stall.release)

	select {
	case err := <-stalled:
		if !errors.Is(err, blob.ErrAttemptExpired) {
			t.Fatalf("stalled save after the sweep: err = %v, want ErrAttemptExpired", err)
		}
	case <-time.After(backupWaitLimit):
		t.Fatal("the stalled save did not end after its release")
	}

	r.requireNoMissingBlob(t, c, stalledKeys[0])
}

// saveWith saves the checkpoint seq of the update of c through updates.
func (r *blobDeleteRig) saveWith(t *testing.T, updates *stack.Updates, c *stackCase, seq int64) {
	t.Helper()

	in := stack.CheckpointInput{
		Features: nil, Deployment: []byte(`{"deployment":{"n":` + strconv.FormatInt(seq, 10) + `}}`),
		SchemaVersion: 3, Sequence: seq, Invalid: false,
	}

	if err := updates.SaveCheckpoint(t.Context(), c.ref, c.started.Update.ID, c.started.Token, in); err != nil {
		t.Fatalf("save checkpoint %d: %v", seq, err)
	}
}

// requireNoMissingBlob checks that the row of the update of c does not name
// the deleted blob refused, and that every blob that a row names is stored.
func (r *blobDeleteRig) requireNoMissingBlob(t *testing.T, c *stackCase, refused blob.Key) {
	t.Helper()

	cp, err := r.store.CheckpointByUpdate(t.Context(), c.ref, c.started.Update.ID)
	if err != nil {
		t.Fatalf("checkpoint of the update: %v", err)
	}

	if cp.Sequence != 1 || cp.BlobKey == refused {
		t.Fatalf("row after the race: sequence %d, key %q; want sequence 1 under the key of the second save",
			cp.Sequence, cp.BlobKey)
	}

	named, err := r.store.ReferencedBlobKeys(t.Context(), c.stack.ID)
	if err != nil {
		t.Fatalf("referenced blob keys: %v", err)
	}

	r.requireStored(t, named, "named by a row after the race")
}

// raceUpdates returns an update service of the rig over checkpoints, with the
// clock now.
func (r *blobDeleteRig) raceUpdates(
	t *testing.T, checkpoints stack.CheckpointStore, now func() time.Time,
) *stack.Updates {
	t.Helper()

	nsSvc, err := namespace.NewService(namespace.Config{Store: r.store, Now: nil, Installation: blobDeleteInstallation})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	refusals := stacktest.NewRefusals()

	journal, err := stack.NewJournalEntries(r.live, r.store, pulumijournal.Verifier{}, 1<<20, refusals)
	if err != nil {
		t.Fatalf("journal writer: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:    secrettest.NewMemorySealer(),
		BlobGuard: r.store, ManualLocks: nil, Updates: r.store, Tokens: nil, Checkpoints: checkpoints,
		Events: r.store, Namespaces: nsSvc, Stacks: r.store, Blobs: r.live, Refusals: refusals, Now: now,
		Logger: nil, MaxCheckpointBytes: 1 << 20, Journal: journal, Replayer: pulumijournal.NewReplayer(time.Now, ""),
	})
	if err != nil {
		t.Fatalf("update service: %v", err)
	}

	return updates
}

// listUpdateKeys returns the keys of the blobs under the directory of the
// update of c.
func (r *blobDeleteRig) listUpdateKeys(t *testing.T, c *stackCase) []blob.Key {
	t.Helper()

	prefix := "namespaces/" + r.ns.ID.String() + "/pulumi/" + c.stack.ID.String() + "/" +
		c.started.Update.ID.String() + "/"

	page, err := r.live.ListPage(t.Context(), blob.PageRequest{Prefix: prefix, After: "", Limit: blob.MaxPageLimit})
	if err != nil {
		t.Fatalf("list the blobs of the update: %v", err)
	}

	keys := make([]blob.Key, 0, len(page.Objects))
	for _, obj := range page.Objects {
		keys = append(keys, obj.Key)
	}

	return keys
}
