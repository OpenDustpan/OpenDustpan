package storetest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// BlobDeleteStore is a store for the cases that run a deleter of blobs while a
// backup or a restore runs: the backup ports, the ports of the retention
// sweep, and the start of a restore row.
type BlobDeleteStore interface {
	BackupStore
	backup.PortableExporter
	backup.KindReader
	inventory.ExpiredDeleted
	inventory.BlobPurges
	inventory.SweepLocker
	// StartRestore stores a checking restore row and starts its operation.
	StartRestore(ctx context.Context, r backup.RestoreRecord, start durable.StartRequest) error
	// MarkRestoreApplying moves the checking restore to applying.
	MarkRestoreApplying(
		ctx context.Context, f durable.StepFence, id backup.RestoreID, preBackupID backup.ID, result []byte,
	) error
}

// kindClaimer claims the installation kind, which a backup records.
type kindClaimer interface {
	ClaimKind(ctx context.Context, kind installation.Kind) (installation.Kind, error)
}

// BlobDeleteFactory returns a fresh, empty store for one case.
type BlobDeleteFactory func(t *testing.T) BlobDeleteStore

// RunBlobDelete runs the cases of the deleters of blobs against a backup and a
// restore that run. In each case a backup snapshot names a blob, the deleter
// runs while the backup copies, and then the backup finishes: the blob must
// be in the backup. After the backup ended, the deleter deletes the blob. One
// case starts a restore and checks that the deleters wait for it.
func RunBlobDelete(t *testing.T, newStore BlobDeleteFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, r *blobDeleteRig)
		name string
	}{
		{name: "a checkpoint replace during a backup keeps the previous blob", test: testBlobDeleteReplace},
		{
			name: "an update sweep during a backup keeps the blob that the snapshot names",
			test: testBlobDeleteUpdateSweep,
		},
		{name: "a stack sweep during a backup keeps the blob that the snapshot names", test: testBlobDeleteStackSweep},
		{name: "a journal build during a backup keeps the batch blobs", test: testBlobDeleteJournalBuild},
		{name: "a Pulumi purge during a backup keeps the checkpoint blobs", test: testBlobDeletePulumiPurge},
		{name: "an OpenTofu purge during a backup keeps the state blobs", test: testBlobDeleteOpenTofuPurge},
		{name: "the retention sweep during a backup keeps the blobs of a purged unit", test: testBlobDeleteRetention},
		{name: "a purge during a restore keeps the blobs", test: testBlobDeleteRestore},
		{
			name: "a save whose commit stalls past the sweep of its blob is refused",
			test: testStalledSaveAfterSweep,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newBlobDeleteRig(t, newStore(t)))
		})
	}
}

// gatedBlobs is the live blob store as the backup reads it. The first Get
// signals that the backup copies, then waits for the release.
type gatedBlobs struct {
	blob.Store

	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

// Get implements blob.Store.
func (g *gatedBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	g.once.Do(func() { close(g.reached) })

	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for the release of the backup copy: %w", ctx.Err())
	}

	rc, err := g.Store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get blob: %w", err)
	}

	return rc, nil
}

// blobDeleteRig holds the services of one case over one store: the services
// that delete blobs, and a backup service that reads the live blob store
// through a gate.
type blobDeleteRig struct {
	store      BlobDeleteStore
	live       blob.Store
	target     blob.Store
	gate       *gatedBlobs
	liveDir    string
	backups    *backup.Service
	updates    *stack.Updates
	stacks     *stack.Service
	workspaces *workspace.Service
	ns         namespace.Namespace
}

// blobDeleteInstallation names the installation of the namespace of a case.
const blobDeleteInstallation = "eu-west"

// newBlobDeleteRig builds the services of a case over store.
func newBlobDeleteRig(t *testing.T, store BlobDeleteStore) *blobDeleteRig {
	t.Helper()

	ctx := t.Context()

	claimer, ok := store.(kindClaimer)
	if !ok {
		t.Fatalf("store %T does not claim the installation kind", store)
	}

	if _, err := claimer.ClaimKind(ctx, installation.KindProduction); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	liveDir := t.TempDir()
	live := openCaseBlobsIn(t, liveDir)
	target := openCaseBlobsIn(t, t.TempDir())

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: now(), Name: "acme", PrimaryInstallation: blobDeleteInstallation,
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := store.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	nsSvc, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: blobDeleteInstallation})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	r := &blobDeleteRig{
		store:   store,
		live:    live,
		liveDir: liveDir,
		target:  target,
		ns:      ns,
		gate: &gatedBlobs{
			Store:   live,
			reached: make(chan struct{}),
			release: make(chan struct{}),
			once:    sync.Once{},
		},
		backups:    nil,
		updates:    nil,
		stacks:     nil,
		workspaces: nil,
	}
	r.buildServices(t, nsSvc)

	return r
}

// openCaseBlobsIn opens a blob store in dir.
func openCaseBlobsIn(t *testing.T, dir string) *blobfs.Store {
	t.Helper()

	store, err := blobfs.Open(dir)
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return store
}

// buildServices builds the services of the rig. Each deleter gets the store
// as its guard, and the backup service reads through the gate.
func (r *blobDeleteRig) buildServices(t *testing.T, nsSvc *namespace.Service) {
	t.Helper()

	var err error

	r.workspaces, err = workspace.NewService(workspace.Config{
		BlobGuard: r.store, ManualLocks: nil, Workspaces: r.store, States: r.store, Blobs: r.live,
		Namespaces: nsSvc, OutOfScope: workspacetest.NewRefusals(), Sealer: testSealer(t, nsSvc), Logger: nil,
		Now: nil, MaxStateBytes: 0, MaxPendingVersions: 0, Retention: 0,
	})
	if err != nil {
		t.Fatalf("workspace service: %v", err)
	}

	refusals := stacktest.NewRefusals()

	journal, err := stack.NewJournalEntries(r.live, r.store, pulumijournal.Verifier{}, 1<<20, refusals)
	if err != nil {
		t.Fatalf("journal writer: %v", err)
	}

	r.updates, err = stack.NewUpdates(stack.UpdatesConfig{
		Sealer:    secrettest.NewMemorySealer(),
		BlobGuard: r.store, ManualLocks: nil, Updates: r.store, Tokens: nil, Checkpoints: r.store, Events: r.store,
		Namespaces: nsSvc, Stacks: r.store, Blobs: r.live, Refusals: refusals, Now: nil, Logger: nil,
		MaxCheckpointBytes: 1 << 20, Journal: journal, Replayer: pulumijournal.NewReplayer(time.Now, ""),
	})
	if err != nil {
		t.Fatalf("update service: %v", err)
	}

	r.stacks, err = stack.NewService(stack.Config{
		Store: r.store, Namespaces: nsSvc, Now: nil, State: nil, Retention: 0, ManualLocks: nil,
	})
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	r.backups, err = backup.NewService(backup.Config{
		Exporter: r.store, Kinds: r.store, Registry: r.store, Blobs: r.gate, StateOpener: r.workspaces,
		Target: r.target, TargetName: "dir:test", Now: nil, Version: "test", MaxSQLBytes: 0,
		Operations: r.store.DurableOperationsPort(), Owner: "", Lease: 0, Logger: nil,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}
}

// runningBackup is a backup that has taken its snapshot and waits in its first
// copy of a blob.
type runningBackup struct {
	id   backup.ID
	done chan error
}

// startHeldBackup starts a backup and waits until it copies its first blob,
// which is after its snapshot. The backup stays there until finish.
func (r *blobDeleteRig) startHeldBackup(t *testing.T) *runningBackup {
	t.Helper()

	id := backup.ID(ident.NewID("bkp-"))
	if err := r.backups.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	b := &runningBackup{id: id, done: make(chan error, 1)}

	go func() {
		_, err := r.backups.Await(t.Context(), id)
		b.done <- err
	}()

	select {
	case <-r.gate.reached:
	case err := <-b.done:
		t.Fatalf("backup ended before it copied a blob: %v", err)
	case <-time.After(backupWaitLimit):
		t.Fatal("backup did not reach its first blob copy")
	}

	t.Cleanup(func() {
		select {
		case <-r.gate.release:
		default:
			close(r.gate.release)
		}
	})

	return b
}

// finish lets the backup go on and requires that it succeeds, that it passes
// the check, and that its manifest names every key in want, with the bytes of
// the live blob in the target.
func (r *blobDeleteRig) finish(t *testing.T, b *runningBackup, want []blob.Key) {
	t.Helper()

	close(r.gate.release)

	select {
	case err := <-b.done:
		if err != nil {
			t.Fatalf("backup %s ended with an error: %v", b.id, err)
		}
	case <-time.After(backupWaitLimit):
		t.Fatalf("backup %s did not end", b.id)
	}

	if err := r.backups.Check(t.Context(), b.id); err != nil {
		t.Fatalf("check backup %s: %v", b.id, err)
	}

	manifest, err := r.backups.ReadManifest(t.Context(), b.id)
	if err != nil {
		t.Fatalf("read manifest of backup %s: %v", b.id, err)
	}

	for _, key := range want {
		if !slices.ContainsFunc(manifest.Blobs, func(m backup.ManifestBlob) bool { return m.Key == key.String() }) {
			t.Errorf("manifest of backup %s has no entry for blob %q", b.id, key)
		}

		if !sameBlob(t, r.target, "blobs/"+key.String(), r.live, key.String()) {
			t.Errorf("backup copy of blob %q differs from the live blob", key)
		}
	}
}

// ageBlobs makes every blob of the live store two hours older, so the sweeps
// count it as past their grace period. The leases of the updates use the clock
// of the services, which a case does not move.
func (r *blobDeleteRig) ageBlobs(t *testing.T) {
	t.Helper()

	root, err := os.OpenRoot(r.liveDir)
	if err != nil {
		t.Fatalf("open the root of the live store: %v", err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close the root of the live store: %v", closeErr)
		}
	}()

	old := time.Now().Add(-2 * time.Hour)

	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		return root.Chtimes(path, old, old)
	})
	if err != nil {
		t.Fatalf("age the blobs of the live store: %v", err)
	}
}

// requireStored checks that every key is stored in the live store.
func (r *blobDeleteRig) requireStored(t *testing.T, keys []blob.Key, when string) {
	t.Helper()

	for _, key := range keys {
		if _, err := r.live.Get(t.Context(), key); err != nil {
			t.Errorf("blob %q %s: err = %v, want it stored", key, when, err)
		}
	}
}

// requireDeleted checks that no key is stored in the live store.
func (r *blobDeleteRig) requireDeleted(t *testing.T, keys []blob.Key, when string) {
	t.Helper()

	for _, key := range keys {
		if _, err := r.live.Get(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
			t.Errorf("blob %q %s: err = %v, want ErrNotFound", key, when, err)
		}
	}
}

// stackCase is a stack with a running update.
type stackCase struct {
	ref     stack.Ref
	stack   stack.Stack
	started stack.Started
}

// newStackCase creates a stack and starts an update on it.
func (r *blobDeleteRig) newStackCase(t *testing.T) *stackCase {
	t.Helper()

	st := mustCreateStack(t, r.store, r.ns, "dev")
	u, err := r.updates.Create(t.Context(), st.Ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create update: %v", err)
	}

	started, err := r.updates.Start(t.Context(), st.Ref, u.ID)
	if err != nil {
		t.Fatalf("start update: %v", err)
	}

	return &stackCase{ref: st.Ref, stack: st, started: started}
}

// save saves the checkpoint seq of the update with an empty deployment.
func (r *blobDeleteRig) save(t *testing.T, c *stackCase, seq int64) {
	t.Helper()

	err := r.updates.SaveCheckpoint(t.Context(), c.ref, c.started.Update.ID, c.started.Token, stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(`{"deployment":{"n":` + strconv.FormatInt(seq, 10) + `}}`),
		SchemaVersion: 3,
		Sequence:      seq,
		Invalid:       false,
	})
	if err != nil {
		t.Fatalf("save checkpoint %d: %v", seq, err)
	}
}

// checkpointKey returns the blob key of the checkpoint row of the update.
func (r *blobDeleteRig) checkpointKey(t *testing.T, c *stackCase) blob.Key {
	t.Helper()

	cp, err := r.store.CheckpointByUpdate(t.Context(), c.ref, c.started.Update.ID)
	if err != nil {
		t.Fatalf("checkpoint of the update: %v", err)
	}

	return cp.BlobKey
}

// testBlobDeleteReplace replaces the checkpoint that a snapshot names while
// the backup copies. The previous blob stays, the backup succeeds with it, and
// a save after the backup deletes it.
func testBlobDeleteReplace(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c := r.newStackCase(t)
	r.save(t, c, 1)
	named := r.checkpointKey(t, c)

	b := r.startHeldBackup(t)
	r.save(t, c, 2)
	r.requireStored(t, []blob.Key{named}, "after a replace during the backup")
	r.finish(t, b, []blob.Key{named})

	r.ageBlobs(t)
	r.save(t, c, 3)
	r.requireDeleted(t, []blob.Key{named}, "after the backup ended")
}

// testBlobDeleteUpdateSweep lets the sweep of a save run on blobs that are
// older than the grace while the backup copies.
func testBlobDeleteUpdateSweep(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c := r.newStackCase(t)
	r.save(t, c, 1)
	named := r.checkpointKey(t, c)

	b := r.startHeldBackup(t)
	r.save(t, c, 2)
	r.ageBlobs(t)
	r.save(t, c, 3)
	r.requireStored(t, []blob.Key{named}, "after the sweep of a save during the backup")
	r.finish(t, b, []blob.Key{named})

	r.save(t, c, 4)
	r.requireDeleted(t, []blob.Key{named}, "after the backup ended")
}

// testBlobDeleteStackSweep lets the sweep of the start of an update run on
// blobs that are older than the grace while the backup copies.
func testBlobDeleteStackSweep(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c := r.newStackCase(t)
	r.save(t, c, 1)
	named := r.checkpointKey(t, c)

	b := r.startHeldBackup(t)
	r.save(t, c, 2)

	r.complete(t, c)

	r.ageBlobs(t)
	r.startPreview(t, c)
	r.requireStored(t, []blob.Key{named}, "after the start of an update during the backup")
	r.finish(t, b, []blob.Key{named})

	r.startPreview(t, c)
	r.requireDeleted(t, []blob.Key{named}, "after the backup ended")
}

// complete completes the update of c.
func (r *blobDeleteRig) complete(t *testing.T, c *stackCase) {
	t.Helper()

	_, err := r.updates.Complete(t.Context(), c.ref, c.started.Update.ID, c.started.Token, stack.StatusSucceeded)
	if err != nil {
		t.Fatalf("complete update: %v", err)
	}
}

// startPreview starts a preview on the stack of c, which sweeps the stack.
func (r *blobDeleteRig) startPreview(t *testing.T, c *stackCase) {
	t.Helper()

	u, err := r.updates.Create(t.Context(), c.ref, stack.Plan{
		Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create preview: %v", err)
	}

	if _, err := r.updates.Start(t.Context(), c.ref, u.ID); err != nil {
		t.Fatalf("start preview: %v", err)
	}
}

// journalBaseDeployment is the deployment of the update that a journaled
// update starts from.
const journalBaseDeployment = `{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},` +
	`"resources":[{"urn":"urn:pulumi:dev::web::t:mod:T::a","custom":true,"type":"t:mod:T","id":"a"}]}`

// journalBatches returns the two batches of a journaled update that add a
// resource.
func journalBatches() []string {
	return []string{
		`{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
		`{"entries":[{"version":1,"kind":1,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null,` +
			`"state":{"urn":"urn:pulumi:dev::web::t:mod:T::b","custom":true,"type":"t:mod:T","id":"b"}}]}`,
	}
}

// testBlobDeleteJournalBuild builds the checkpoint of a journaled update while
// the backup copies the journal batches that its snapshot names.
func testBlobDeleteJournalBuild(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	ctx := t.Context()
	base := r.newStackCase(t)

	err := r.updates.SaveCheckpoint(ctx, base.ref, base.started.Update.ID, base.started.Token, stack.CheckpointInput{
		Features: nil, Deployment: []byte(journalBaseDeployment), SchemaVersion: 3, Sequence: 0, Invalid: false,
	})
	if err != nil {
		t.Fatalf("save base checkpoint: %v", err)
	}

	r.complete(t, base)

	u, err := r.updates.Create(
		ctx,
		base.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
	)
	if err != nil {
		t.Fatalf("create journaled update: %v", err)
	}

	started, err := r.updates.StartWithJournalVersion(ctx, base.ref, u.ID, 1)
	if err != nil {
		t.Fatalf("start journaled update: %v", err)
	}

	for _, body := range journalBatches() {
		if err = r.updates.AppendJournal(ctx, base.ref, started.Update.ID, started.Token, []byte(body)); err != nil {
			t.Fatalf("append journal batch: %v", err)
		}
	}

	keys, err := r.store.JournalKeys(ctx, base.stack.ID, started.Update.ID)
	if err != nil || len(keys) != len(journalBatches()) {
		t.Fatalf("journal keys = %v, %v, want %d", keys, err, len(journalBatches()))
	}

	b := r.startHeldBackup(t)

	if _, err = r.updates.Complete(ctx, base.ref, started.Update.ID, started.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete journaled update during the backup: %v", err)
	}

	r.requireStored(t, keys, "after the build during the backup")
	r.finish(t, b, keys)

	r.ageBlobs(t)
	r.startPreview(t, base)
	r.requireDeleted(t, keys, "after the backup ended")
}

// purgedStack is a stack that a purge removed after its checkpoint was saved.
type purgedStack struct {
	nsID namespace.ID
	id   stack.ID
	keys []blob.Key
}

// seedPurgeable saves a checkpoint of a new stack and returns the key of its
// blob. The purge runs later, in purge.
func (r *blobDeleteRig) seedPurgeable(t *testing.T) (*stackCase, []blob.Key) {
	t.Helper()

	c := r.newStackCase(t)
	r.save(t, c, 1)

	r.complete(t, c)

	return c, []blob.Key{r.checkpointKey(t, c)}
}

// blobDeleteEntry returns the audit entry of a delete or a purge in the
// namespace of the rig.
func (r *blobDeleteRig) blobDeleteEntry(action, object string) audit.Entry {
	e := deleteEntry(r.ns.Name)
	e.Action = action
	e.Object = object

	return e
}

// purge removes the stack of c for good, as the purge of a unit does: the
// rows go and the blob purge record stays. The caller deletes the blobs.
func (r *blobDeleteRig) purge(t *testing.T, c *stackCase, keys []blob.Key) purgedStack {
	t.Helper()

	ctx := t.Context()

	err := r.store.ForceDeleteStack(ctx, c.ref, r.ns.Fence(), inventory.Deletion{
		Entry: r.blobDeleteEntry("unit:manage", "pulumi:acme/web/dev"), PurgeAfter: purgeAt(),
	})
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	nsID, err := r.stacks.PurgeDeleted(
		ctx,
		r.ns.Name,
		c.stack.ID,
		r.blobDeleteEntry("unit:purge", "pulumi:acme/web/dev"),
	)
	if err != nil {
		t.Fatalf("purge stack: %v", err)
	}

	return purgedStack{nsID: nsID, id: c.stack.ID, keys: keys}
}

// requireWaits checks that err says that a deleter waits for work of kind.
func requireWaits(t *testing.T, err error, kind blobguard.Kind, what string) {
	t.Helper()

	wait, isWait := errors.AsType[*blobguard.WaitError](err)
	if !isWait || wait.Work.Kind != kind || !errors.Is(err, blobguard.ErrWait) {
		t.Fatalf("%s: err = %v, want a wait for a %s", what, err, kind)
	}
}

// requireRecords checks the number of blob purge records.
func (r *blobDeleteRig) requireRecords(t *testing.T, want int, when string) []inventory.BlobPurge {
	t.Helper()

	records, err := r.store.ListBlobPurges(t.Context(), nil, inventory.SweepPageSize)
	if err != nil || len(records) != want {
		t.Fatalf("blob purge records %s = %d, %v, want %d", when, len(records), err, want)
	}

	return records
}

// testBlobDeletePulumiPurge purges a stack while the backup copies its
// checkpoint blob. The delete of the blobs waits and the record stays.
func testBlobDeletePulumiPurge(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	c, keys := r.seedPurgeable(t)
	b := r.startHeldBackup(t)
	p := r.purge(t, c, keys)

	err := r.updates.DeleteStackBlobs(t.Context(), p.nsID, p.id)
	requireWaits(t, err, blobguard.Backup, "delete of the blobs of a purged stack during the backup")
	r.requireStored(t, p.keys, "after the purge during the backup")
	r.requireRecords(t, 1, "after the purge during the backup")
	r.finish(t, b, p.keys)

	if err := r.updates.DeleteStackBlobs(t.Context(), p.nsID, p.id); err != nil {
		t.Fatalf("delete the blobs after the backup ended: %v", err)
	}

	r.requireDeleted(t, p.keys, "after the backup ended")
}

// testBlobDeleteOpenTofuPurge purges a workspace while the backup copies its
// state blob.
func testBlobDeleteOpenTofuPurge(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	ctx := t.Context()
	w, keys := r.seedWorkspace(t)
	b := r.startHeldBackup(t)

	nsID := r.purgeWorkspace(t, w)

	err := r.workspaces.DeletePurgedBlobs(ctx, nsID, w.ID)
	requireWaits(t, err, blobguard.Backup, "delete of the state blobs of a purged workspace during the backup")
	r.requireStored(t, keys, "after the purge during the backup")
	r.finish(t, b, keys)

	if err := r.workspaces.DeletePurgedBlobs(ctx, nsID, w.ID); err != nil {
		t.Fatalf("delete the state blobs after the backup ended: %v", err)
	}

	r.requireDeleted(t, keys, "after the backup ended")
}

// seedWorkspace creates a workspace with one finalized state version and
// returns it with the keys of the blobs in the live store.
func (r *blobDeleteRig) seedWorkspace(t *testing.T) (workspace.Workspace, []blob.Key) {
	t.Helper()

	ctx := t.Context()

	name, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	cols, ok := r.store.(collection.Store)
	if !ok {
		t.Fatalf("store %T does not keep collections", r.store)
	}

	w, err := r.workspaces.Create(
		ctx, workspace.Ref{Namespace: r.ns.Name, Name: name}, placementtest.Workspace(t, cols, r.ns.Fence()),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if _, err = r.workspaces.Lock(ctx, w.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	pushThrough(t, r.workspaces, w.ID, 1, `{"v":1}`)

	if _, err = r.workspaces.Unlock(ctx, w.ID, "alice"); err != nil {
		t.Fatalf("unlock workspace: %v", err)
	}

	var keys []blob.Key

	err = blob.Walk(ctx, r.live, "", func(obj blob.Object) error {
		keys = append(keys, obj.Key)

		return nil
	})
	if err != nil || len(keys) == 0 {
		t.Fatalf("state blobs after the push = %d, %v, want the pushed state", len(keys), err)
	}

	return w, keys
}

// purgeWorkspace deletes and purges the workspace and returns the ID of the
// namespace row.
func (r *blobDeleteRig) purgeWorkspace(t *testing.T, w workspace.Workspace) namespace.ID {
	t.Helper()

	object := "opentofu:acme/" + w.Ref.Name.String()

	if err := r.workspaces.Delete(t.Context(), w.Ref, r.blobDeleteEntry("unit:manage", object)); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	nsID, err := r.workspaces.PurgeDeleted(t.Context(), r.ns.Name, w.ID, r.blobDeleteEntry("unit:purge", object))
	if err != nil {
		t.Fatalf("purge workspace: %v", err)
	}

	return nsID
}

// stackSweepTool is the sweep tool of the Pulumi stacks of a case.
type stackSweepTool struct {
	updates *stack.Updates
}

// PurgeExpired implements inventory.SweepTool. The cases purge before the
// sweep, so no unit is due.
func (stackSweepTool) PurgeExpired(context.Context, namespace.Name, string, time.Time, audit.Entry) error {
	return inventory.ErrNotFound
}

// DeletePurgedBlobs implements inventory.SweepTool.
func (t stackSweepTool) DeletePurgedBlobs(ctx context.Context, ns namespace.ID, unitID string) error {
	if err := t.updates.DeleteStackBlobs(ctx, ns, stack.ID(unitID)); err != nil {
		return fmt.Errorf("delete blobs of stack %s: %w", unitID, err)
	}

	return nil
}

// testBlobDeleteRetention runs the retention sweep, past the grace of the
// blob purge record, while the backup copies the blobs of the purged stack.
func testBlobDeleteRetention(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	ctx := t.Context()
	c, keys := r.seedPurgeable(t)
	b := r.startHeldBackup(t)
	r.purge(t, c, keys)

	tool := r.requireRecords(t, 1, "after the purge during the backup")[0].Tool

	sweeper, err := inventory.NewSweeper(inventory.SweeperConfig{
		Expired: r.store, BlobPurges: r.store, Lock: r.store,
		Tools:  map[string]inventory.SweepTool{tool: stackSweepTool{updates: r.updates}},
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { return time.Now().Add(inventory.BlobPurgeGrace + time.Hour) },
	})
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}

	res, err := sweeper.Sweep(ctx, r.blobDeleteEntry("unit:purge", "server"))
	if err != nil {
		t.Fatalf("sweep during the backup: %v", err)
	}

	if res.WaitingBlobPurges != 1 || res.Failed != 0 || res.FinishedBlobPurges != 0 || res.PendingBlobPurges != 1 ||
		res.WaitingFor != (blobguard.Work{Kind: blobguard.Backup, ID: b.id.String()}).String() {
		t.Fatalf("sweep during the backup = %+v, want one record that waits for backup %s", res, b.id)
	}

	r.requireStored(t, keys, "after the sweep during the backup")
	r.finish(t, b, keys)

	res, err = sweeper.Sweep(ctx, r.blobDeleteEntry("unit:purge", "server"))
	if err != nil || res.FinishedBlobPurges != 1 || res.PendingBlobPurges != 0 || res.WaitingBlobPurges != 0 {
		t.Fatalf("sweep after the backup ended = %+v, %v, want the record finished", res, err)
	}

	r.requireDeleted(t, keys, "after the sweep that followed the backup")
}

// testBlobDeleteRestore starts a restore row and runs the purge of a stack and
// of a workspace. Both deletes of blobs wait for the restore, while it is
// checking and while it is applying.
func testBlobDeleteRestore(t *testing.T, r *blobDeleteRig) {
	t.Helper()

	ctx := t.Context()
	c, keys := r.seedPurgeable(t)
	w, stateKeys := r.seedWorkspace(t)

	id := backup.MintRestoreID()
	if err := r.store.StartRestore(ctx, newRestoreRecord(id, "bkp-op", now()), restoreOpStart(id)); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	p := r.purge(t, c, keys)
	nsID := r.purgeWorkspace(t, w)

	for _, state := range []string{"checking", "applying"} {
		err := r.updates.DeleteStackBlobs(ctx, p.nsID, p.id)
		requireWaits(t, err, blobguard.Restore, "delete of the blobs of a purged stack while the restore is "+state)

		err = r.workspaces.DeletePurgedBlobs(ctx, nsID, w.ID)
		requireWaits(
			t,
			err,
			blobguard.Restore,
			"delete of the state blobs of a purged workspace while the restore is "+state,
		)

		r.requireStored(t, append(slices.Clone(keys), stateKeys...), "while the restore is "+state)

		if state == "checking" {
			claimed := mustClaimRestoreOp(t, r.store.DurableOperationsPort(), "instance-a")

			err = r.store.MarkRestoreApplying(
				ctx, claimed.Claim.Step(backup.RestoreStepPreBackup), id, backup.ID(ident.NewID("bkp-")), nil,
			)
			if err != nil {
				t.Fatalf("mark restore applying: %v", err)
			}
		}
	}
}
