package workspace_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// stateDoc is a state document with one resource. The fence keeps the
// resources and changes only the lineage and the serial.
const stateDoc = `{"version":4,"lineage":"lin-old","serial":1,"resources":[{"type":"null_resource","name":"a"}]}`

// smallDoc is a state document that fits the fixture, which caps state
// at 64 bytes.
const smallDoc = `{"lineage":"lin-old","serial":1,"resources":[]}`

// readFenced reads the plain content of sv through the service that owns it.
func readFenced(t *testing.T, svc *workspace.Service, sv workspace.StateVersion) map[string]json.RawMessage {
	t.Helper()

	r, err := svc.Open(t.Context(), sv)
	if err != nil {
		t.Fatalf("open fence %s: %v", sv.ID, err)
	}

	raw, readErr := io.ReadAll(r)
	closeErr := r.Close()

	if readErr != nil || closeErr != nil {
		t.Fatalf("read fence %s: %v, close %v", sv.ID, readErr, closeErr)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fence %s is not a JSON object: %v", sv.ID, err)
	}

	return doc
}

// A force-unlock of a sealed state writes a sealed fence: the blob holds no
// plain state, and the plain content reads back with the new lineage and the
// next serial, and the resources of the state before it.
func TestForceUnlockFencesSealedState(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	f.pushSealed(t, stateDoc, nil, 1)

	if _, err := f.sealed.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if _, _, err := f.sealed.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry()); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	cur, err := f.sealed.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if !cur.Sealed || cur.FenceBy != "admin" || cur.Serial != 2 || cur.Lineage == "lin-old" {
		t.Fatalf("fence sealed=%t by %q serial %d lineage %q, want a sealed admin fence at serial 2",
			cur.Sealed, cur.FenceBy, cur.Serial, cur.Lineage)
	}

	assertBlobHidesState(t, f, cur)

	doc := readFenced(t, f.sealed, cur)

	if string(doc["lineage"]) != `"`+cur.Lineage+`"` || string(doc["serial"]) != "2" {
		t.Fatalf(
			"plain fence lineage %s serial %s, want the stored lineage and serial 2",
			doc["lineage"],
			doc["serial"],
		)
	}

	if !strings.Contains(string(doc["resources"]), "null_resource") {
		t.Fatalf("plain fence lost the resources: %s", doc["resources"])
	}
}

// assertBlobHidesState checks that the blob of a sealed fence holds neither the
// resources nor the lineage of the state in plain text.
func assertBlobHidesState(t *testing.T, f sealFixture, cur workspace.StateVersion) {
	t.Helper()

	raw, err := f.blobs.Get(t.Context(), cur.Content)
	if err != nil {
		t.Fatalf("blob of the fence: %v", err)
	}

	stored, readErr := io.ReadAll(raw)
	closeErr := raw.Close()

	if readErr != nil || closeErr != nil {
		t.Fatalf("read the sealed blob: %v, close %v", readErr, closeErr)
	}

	if strings.Contains(string(stored), "null_resource") || strings.Contains(string(stored), cur.Lineage) {
		t.Fatal("the fence blob holds plain state")
	}
}

// A force-unlock of a workspace with no state only releases its lock: no
// version is written.
func TestForceUnlockWithoutStateOnlyReleases(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	released, _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry())
	if err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	if released.Lock.Locked() {
		t.Fatalf("workspace still locked by %q", released.Lock.Holder)
	}

	if _, curErr := f.svc.Current(ctx, f.ws.ID); !errors.Is(curErr, workspace.ErrNoState) {
		t.Fatalf("current after a force-unlock with no state: err = %v, want ErrNoState", curErr)
	}
}

// A finalize that lands between the read of the current state and the fence
// refuses the fence with ErrConcurrentModification, leaves the lock held, and
// deletes the blob of the refused fence.
func TestForceUnlockRefusedByAConcurrentFinalizeDeletesItsBlob(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
		t.Fatalf("push v1: %v", err)
	}

	const next = `{"lineage":"lin-old","serial":2,"resources":[]}`

	var fenceKey blob.Key

	var fenceID workspace.StateVersionID

	f.store.beforeFence = func(sv workspace.StateVersion) {
		fenceKey, fenceID = sv.Content, sv.ID

		// A holder finalizes a newer version before the fence takes the store lock.
		if err := f.push(t, newState("lin-old", 2, next), next); err != nil {
			t.Errorf("push v2 during the fence: %v", err)
		}
	}

	_, _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry())
	if !errors.Is(err, workspace.ErrConcurrentModification) {
		t.Fatalf("force unlock beside a finalize: err = %v, want ErrConcurrentModification", err)
	}

	held, getErr := f.svc.GetByID(ctx, f.ws.ID)
	if getErr != nil {
		t.Fatalf("get workspace: %v", getErr)
	}

	if !held.Lock.Locked() {
		t.Fatal("the lock was released by a refused fence")
	}

	if fenceKey == "" {
		t.Fatal("the hook saw no fence")
	}

	if r, blobErr := f.blobs.Get(ctx, fenceKey); blobErr == nil {
		if closeErr := r.Close(); closeErr != nil {
			t.Errorf("close the blob: %v", closeErr)
		}

		t.Fatal("the blob of the refused fence was kept")
	}

	if _, getErr := f.store.GetStateVersion(ctx, fenceID); !errors.Is(getErr, workspace.ErrStateVersionNotFound) {
		t.Fatalf("pending row of the refused fence: err = %v, want ErrStateVersionNotFound", getErr)
	}
}

// failPutBlobs fails every Put with errPutFailed and records the attempted
// key, so tests prove a force-unlock whose content never lands leaves nothing.
type failPutBlobs struct {
	blob.Store

	mu   sync.Mutex
	keys []blob.Key
}

// errPutFailed is a blob failure during a force-unlock: the content never lands.
var errPutFailed = errors.New("put failed")

// Put records key and fails without writing.
func (b *failPutBlobs) Put(_ context.Context, key blob.Key, _ io.Reader) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.keys = append(b.keys, key)

	return errPutFailed
}

// attempted returns every key Put saw.
func (b *failPutBlobs) attempted() []blob.Key {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]blob.Key(nil), b.keys...)
}

// A force-unlock whose content write fails leaves neither a version row nor a
// blob: the staged fence row is discarded.
func TestForceUnlockWithFailedPutLeavesNoFence(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
		t.Fatalf("push v1: %v", err)
	}

	failing := &failPutBlobs{Store: f.blobs, mu: sync.Mutex{}, keys: nil}

	failingSvc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              failing,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	_, _, err = failingSvc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry())
	if !errors.Is(err, errPutFailed) {
		t.Fatalf("force unlock with a failed content write: err = %v, want the failed write", err)
	}

	keys := failing.attempted()
	if len(keys) != 1 {
		t.Fatalf("attempted content writes = %d, want 1", len(keys))
	}

	parts := strings.Split(keys[0].String(), "/")
	if len(parts) != 6 {
		t.Fatalf("fence content key %q has %d parts, want 6", keys[0], len(parts))
	}

	fenceID := workspace.StateVersionID(parts[4])

	if _, err := f.store.GetStateVersion(ctx, fenceID); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("fence row %s: err = %v, want ErrStateVersionNotFound", fenceID, err)
	}

	if r, err := f.blobs.Get(ctx, keys[0]); err == nil {
		if closeErr := r.Close(); closeErr != nil {
			t.Errorf("close the blob: %v", closeErr)
		}

		t.Fatal("the blob of the failed fence exists")
	} else if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("blob of the failed fence: err = %v, want ErrNotFound", err)
	}
}

// A force-unlock of a state the client encrypted is refused, and the lock
// stays. The refusal names the way to release the lock: wait for the run, or
// use the action and the switches of a forced write.
func TestForceUnlockClientEncryptedNamesTheWayOut(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-old", 1, envelope), envelope); err != nil {
		t.Fatalf("push encrypted state: %v", err)
	}

	_, _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry())
	if !errors.Is(err, workspace.ErrClientEncrypted) {
		t.Fatalf("force unlock of an encrypted state: err = %v, want ErrClientEncrypted", err)
	}

	for _, want := range []string{
		"Let the run that holds the lock finish; it releases the lock",
		"unit:write-forced",
		"forced-write switches",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal does not name %q: %v", want, err)
		}
	}

	held, getErr := f.svc.GetByID(ctx, f.ws.ID)
	if getErr != nil {
		t.Fatalf("get workspace: %v", getErr)
	}

	if !held.Lock.Locked() {
		t.Fatal("the lock was released although the state cannot be fenced")
	}
}

// A sweep between the blob write of a force-unlock and the commit that makes
// the fence current keeps the fence blob: a version names the key before the
// bytes land. The sweep runs right after the write and waits for no grace
// period, so only the row protects the blob.
func TestForceUnlockFenceBlobSurvivesASweepBeforeTheCommit(t *testing.T) {
	t.Parallel()

	inner := openFixtureBlobs(t)
	blobs := &sweepAfterPutBlobs{Store: inner, sweep: nil}
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
		t.Fatalf("push v1: %v", err)
	}

	swept := 0
	blobs.sweep = func() {
		swept++

		sweepUnnamedBlobs(t, f, inner)
	}

	if _, _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry()); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	if swept != 1 {
		t.Fatalf("sweeps after the fence write = %d, want 1", swept)
	}

	cur, err := f.svc.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if cur.FenceBy != "admin" {
		t.Fatalf("current version fenced by %q, want admin", cur.FenceBy)
	}

	doc := readFenced(t, f.svc, cur)

	if string(doc["serial"]) != "2" {
		t.Fatalf("fence serial %s, want 2", doc["serial"])
	}
}

// sweepAfterPutBlobs runs sweep once after the next Put while sweep is set,
// so a sweep lands between a blob write and the commit that follows it.
type sweepAfterPutBlobs struct {
	blob.Store

	sweep func()
}

func (b *sweepAfterPutBlobs) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if err := b.Store.Put(ctx, key, r); err != nil {
		return err
	}

	if sweep := b.sweep; sweep != nil {
		b.sweep = nil
		sweep()
	}

	return nil
}

// sweepUnnamedBlobs deletes every blob in blobs under the fixture workspace
// that no version names, at once: the listing comes before the reference
// read, as in the sweeps of the service.
func sweepUnnamedBlobs(t *testing.T, f fixture, blobs blob.Store) {
	t.Helper()

	ctx := t.Context()

	listed, err := blobtest.ListAll(ctx, blobs, f.stateDir(t, f.ws.ID))
	if err != nil {
		t.Errorf("list blobs: %v", err)

		return
	}

	refs, err := f.store.ReferencedContentKeys(ctx, f.ws.ID)
	if err != nil {
		t.Errorf("referenced keys: %v", err)

		return
	}

	for i := range listed {
		if slices.Contains(refs, listed[i].Key) {
			continue
		}

		if err := blobs.Delete(ctx, listed[i].Key); err != nil {
			t.Errorf("delete %s: %v", listed[i].Key, err)
		}
	}
}
