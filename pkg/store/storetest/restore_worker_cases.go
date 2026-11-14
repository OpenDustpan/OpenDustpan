package storetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// workerLease is the lease of the restore operations in the worker cases. A
// takeover waits for it to end, so it stays short, but long enough for the
// renewals of a loaded machine under the race detector.
const workerLease = 10 * time.Second

// laterNamespace is the namespace that a worker rig creates after its backup:
// a restored store does not hold it, and a store that the restore did not
// change still holds it.
const laterNamespace = "later"

// restoreWorkerCases are the cases of a whole restore that the workers of
// two instances run on one database. For each step: a takeover after a holder
// stopped, a takeover while the stale holder continues its work, and a start
// of another instance. Then the cases of a stop in the swap transaction, a
// cancel, the blob store and the maintenance mode. Each case runs the
// restore service with the ports of the store.
func restoreWorkerCases() []restoreCase {
	steps := backup.RestoreSteps()
	cases := make([]restoreCase, 0, 3*len(steps)+12)

	for _, step := range steps {
		cases = append(cases,
			restoreCase{
				name: "a takeover in the " + step + " step ends the restore once",
				test: func(t *testing.T, newStore RestoreFactory) {
					t.Helper()
					testWorkerTakeover(t, newStore, step)
				},
			},
			restoreCase{
				name: "a stale holder that continues the " + step + " step after a takeover changes nothing fenced",
				test: func(t *testing.T, newStore RestoreFactory) {
					t.Helper()
					testWorkerStaleHolder(t, newStore, step)
				},
			},
			restoreCase{
				name: "a start of another instance during the " + step + " step changes nothing",
				test: func(t *testing.T, newStore RestoreFactory) {
					t.Helper()
					testWorkerOtherStart(t, newStore, step)
				},
			},
		)
	}

	cases = append(cases, restoreCrashCases()...)
	cases = append(cases, restoreEndAuditCases()...)
	cases = append(cases, restoreDamagedCases()...)

	return append(cases, []restoreCase{
		{name: "a holder stopped in the swap transaction cannot swap after a takeover", test: testWorkerSwapKill},
		{
			name: "a cancel before the swap ends the restore failed and leaves maintenance mode",
			test: testWorkerCancelBeforeSwap,
		},
		{name: "a cancel after the swap is refused and the restore succeeds", test: testWorkerCancelAfterSwap},
		{name: "the cleanup of the next restore drops an area of an ended restore", test: testWorkerNextCleanup},
		{
			name: "a restore on a blob store with no conditional writes never replaces a live key that differs",
			test: testWorkerOverwritingBlobs,
		},
		{name: "a corrupt backup object fails the restore and deletes no live key", test: testWorkerCorruptObject},
		{
			name: "a blob deleter in flight across the restore loses no blob that the restored data names",
			test: testWorkerDeleterInFlight,
		},
		{
			name: "a restore that starts in the maintenance mode of an operator leaves it in maintenance mode",
			test: testWorkerOperatorMaintenance,
		},
		{name: "a cleanup that waits past its lock limit tries again and ends", test: testWorkerCleanupBusy},
		{name: "a check-only restore runs stage and check and changes no live data", test: testWorkerCheckOnly},
		{name: "an ended restore under the maintenance hold keeps maintenance mode", test: testWorkerHeld},
		{
			name: "a restore with force audits each canceled unit with the restore ID and the token",
			test: testWorkerForceAudit,
		},
	}...)
}

// restoreWorkerStore is the store of the worker cases: every port that the
// server wires into the backup and the restore services.
type restoreWorkerStore interface {
	RestoreStore
	backup.RestoreLive
	maintenance.Store
	secret.KeyStore
	secret.ItemKeyStore
	installation.KindStore
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
}

// workerRig is one store with a backup of seeded data, and the blob stores
// that its services share.
type workerRig struct {
	st       restoreWorkerStore
	ops      durable.Store
	live     *deleteWatch
	target   blob.Store
	key      secret.Wrapper
	backups  *backup.Service
	backupID backup.ID
	seed     itemKeyBackupSeed
	// failover is the failover version of the namespace acme before a
	// restore that a case cancels.
	failover int64
}

// newWorkerRig opens a store, seeds a namespace with a data key and an item
// key whose blob the data names, takes a backup, and then creates the
// namespace laterNamespace, which the backup does not hold.
func newWorkerRig(t *testing.T, newStore RestoreFactory) *workerRig {
	t.Helper()

	store := newStore(t)

	// A store without the restore operation ports skips, as for the port
	// cases: the ports land together.
	restorePortsOf(t, store)

	st, ok := store.(restoreWorkerStore)
	if !ok {
		t.Fatal("the store does not implement every port of a restore that a worker runs")
	}

	if _, err := st.ClaimKind(t.Context(), installation.KindProduction); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("key-encryption key: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper(kek)
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	r := &workerRig{
		st:       st,
		ops:      st.DurableOperationsPort(),
		live:     &deleteWatch{Store: openWorkerBlobs(t), mu: sync.Mutex{}, deleted: nil},
		target:   openWorkerBlobs(t),
		key:      wrapper,
		backups:  nil,
		backupID: "",
		seed:     noSeed(),
		failover: 0,
	}

	r.seed = seedItemKeyBackup(
		t,
		&ItemKeyBackupRig{Store: st, Backups: nil, Restores: nil, Live: r.live.Store},
		wrapper,
	)

	r.backups, err = backup.NewService(backup.Config{
		Exporter: st, Registry: st, Kinds: st, Blobs: r.live.Store, StateOpener: nil, Target: r.target,
		TargetName: "dir:test", Now: nil, Version: "test", MaxSQLBytes: 0, Operations: r.ops, Owner: "backup",
		Lease: 0, Logger: nil,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}

	r.backupID = backup.ID(ident.NewID("bkp-"))

	if _, err := r.backups.Run(t.Context(), r.backupID); err != nil {
		t.Fatalf("run backup %s: %v", r.backupID, err)
	}

	newNamespace(t, st, laterNamespace)

	return r
}

// noSeed returns an empty seed, for a rig before its seed.
func noSeed() itemKeyBackupSeed {
	var none itemKeyBackupSeed

	return none
}

// openWorkerBlobs opens a blob store in a new directory and closes it at the
// end of the test.
func openWorkerBlobs(t *testing.T) *blobfs.Store {
	t.Helper()

	store, err := blobfs.Open(t.TempDir())
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

// workerService holds what one instance of a restore service differs in.
type workerService struct {
	owner    string
	ops      durable.Store
	live     backup.RestoreLive
	maint    backup.RestoreMaintenance
	blobs    blob.Store
	lockWait time.Duration
	// afterApply is the after-apply hook of the service, nil for none.
	afterApply func(context.Context) error
}

// service returns the restore service of one instance over the rig. A zero
// field of w takes the port of the rig.
func (r *workerRig) service(t *testing.T, w workerService) *backup.RestoreService {
	t.Helper()

	if w.ops == nil {
		w.ops = r.ops
	}

	if w.live == nil {
		w.live = r.st
	}

	if w.maint == nil {
		w.maint = StateMaintenance{State: r.st, Hold: false}
	}

	if w.blobs == nil {
		w.blobs = r.live
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live: w.live, Operations: w.ops, Backups: r.backups, Maintenance: w.maint, Blobs: w.blobs,
		Target: r.target, Key: r.key, Installation: "local", AfterApply: w.afterApply, Admit: nil,
		AllowDevBackups: false, AllowDevHint: "", Owner: w.owner, Lease: workerLease,
		CleanupLockWait: w.lockWait, RetryBase: time.Millisecond, Now: nil, Logger: nil,
	})
	if err != nil {
		t.Fatalf("restore service of %s: %v", w.owner, err)
	}

	return restores
}

// start starts a restore of the backup of the rig through restores.
func (r *workerRig) start(t *testing.T, restores *backup.RestoreService, checkOnly, force bool) backup.RestoreID {
	t.Helper()

	id := backup.MintRestoreID()
	req := backup.RestoreRequest{
		BackupID: r.backupID, PreBackupID: "", CheckOnly: checkOnly, Force: force, RequestedBy: "tester",
		KeepAccessTables: false,
	}

	if err := restores.Start(t.Context(), id, req); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	return id
}

// runAsync runs one restore operation on restores in the background, as the
// worker of one instance does. The returned channel closes when the run
// returns. At the end of the test, gate frees the run first, so a case that
// fails early does not wait for a paused holder.
func runAsync(t *testing.T, restores *backup.RestoreService, gate *stepGate) <-chan struct{} {
	t.Helper()

	done := make(chan struct{})
	ctx := context.WithoutCancel(t.Context())

	go func() {
		defer close(done)

		if err := restores.RunOnce(ctx); err != nil && !errors.Is(err, durable.ErrNoWork) {
			t.Logf("run a restore operation: %v", err)
		}
	}()

	t.Cleanup(func() {
		gate.open()
		<-done
	})

	return done
}

// driveToEnd runs the restore operations of restores until the row of id
// and its operation end, as the worker of one instance does, and returns the
// row. A takeover after the finish write ends the row first, and the
// operation at its next claim.
func (r *workerRig) driveToEnd(
	t *testing.T,
	restores *backup.RestoreService,
	id backup.RestoreID,
) backup.RestoreRecord {
	t.Helper()

	deadline := time.Now().Add(StuckTestLimit)

	for {
		row, err := restores.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore %s: %v", id, err)
		}

		if (row.State == backup.RestoreSucceeded || row.State == backup.RestoreFailed) &&
			r.opOf(t, id).State.Terminal() {
			return row
		}

		if time.Now().After(deadline) {
			t.Fatalf("restore %s never ended", id)
		}

		runRestoreWorker(t, restores)

		select {
		case <-t.Context().Done():
			t.Fatalf("restore %s: %v", id, t.Context().Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// waitReached waits until the gate stops its holder. A run that ends before
// the gate fails the case at once.
func waitReached(t *testing.T, g *stepGate, done <-chan struct{}) {
	t.Helper()

	select {
	case <-g.reached:
		return
	case <-done:
	case <-time.After(StuckTestLimit):
	}

	select {
	case <-g.reached:
	default:
		t.Fatalf("the holder never reached the gate at the %s step", g.step)
	}
}

// opOf returns the durable operation of restore id.
func (r *workerRig) opOf(t *testing.T, id backup.RestoreID) durable.Operation {
	t.Helper()

	ops := restoreOps(t, r.ops)

	i := slices.IndexFunc(ops, func(op durable.Operation) bool { return op.Key == backup.RestoreOperationKey(id) })
	if i < 0 {
		t.Fatalf("restore %s has no operation", id)
	}

	return ops[i]
}

// restoreEntries counts the audit entries of the installation whose object
// starts with prefix.
func (r *workerRig) restoreEntries(t *testing.T, prefix string) int {
	t.Helper()

	listing, err := r.st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list the audit log: %v", err)
	}

	n := 0

	for i := range listing.Entries {
		if strings.HasPrefix(listing.Entries[i].Object, prefix) {
			n++
		}
	}

	return n
}

// hasLater reports whether the store holds laterNamespace: true while the
// restore did not change the live data.
func (r *workerRig) hasLater(t *testing.T) bool {
	t.Helper()

	_, err := r.st.GetNamespace(t.Context(), laterNamespace)
	if err != nil && !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get namespace %s: %v", laterNamespace, err)
	}

	return err == nil
}

// liveBlobBytes returns the bytes of the item key blob in the live store.
func (r *workerRig) liveBlobBytes(t *testing.T) []byte {
	t.Helper()

	return readBlob(t, r.live.Store, r.seed.liveBlob.String())
}

// backupBlobBytes returns the bytes of the item key blob in the backup.
func (r *workerRig) backupBlobBytes(t *testing.T) []byte {
	t.Helper()

	return readBlob(t, r.target, "blobs/"+r.seed.liveBlob.String())
}

// putBlob writes data under key of store, and replaces an object that is
// there.
func putBlob(t *testing.T, store blob.Store, key blob.Key, data []byte) {
	t.Helper()

	if err := store.Delete(t.Context(), key); err != nil && !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("delete blob %s: %v", key, err)
	}

	if err := store.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("put blob %s: %v", key, err)
	}
}

// areasOf returns the staging areas of restore id.
func (r *workerRig) areasOf(t *testing.T, id backup.RestoreID) []backup.StagingArea {
	t.Helper()

	areas, err := r.st.ListRestoreAreas(t.Context())
	if err != nil {
		t.Fatalf("list restore areas: %v", err)
	}

	var out []backup.StagingArea

	for _, a := range areas {
		if a.Restore == id {
			out = append(out, a)
		}
	}

	return out
}

// restoredCheck says which parts of a succeeded restore a case checks.
type restoredCheck struct {
	// areas checks that no staging area of the restore stays. A stale holder
	// of the stage step can stage its own area after the end: the next
	// restore drops it.
	areas bool
}

// mustRestoredOnce proves that restore id ended succeeded once: its row and
// its operation succeeded, the audit log holds its entry once, the live data
// is the data of the backup with the blob that it names, maintenance mode
// is off, and the restore deleted no live blob.
func (r *workerRig) mustRestoredOnce(t *testing.T, id backup.RestoreID, check restoredCheck) backup.RestoreRecord {
	t.Helper()

	row, err := r.st.GetRestore(t.Context(), id)
	if err != nil {
		t.Fatalf("get restore %s: %v", id, err)
	}

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore %s = %q (%s), want succeeded", id, row.State, row.Error)
	}

	if op := r.opOf(t, id); op.State != durable.StateSucceeded {
		t.Fatalf("operation of restore %s = %q, want succeeded", id, op.State)
	}

	if n := r.restoreEntries(t, "restore "+id.String()+" of backup "); n != 1 {
		t.Fatalf("audit entries of restore %s = %d, want 1", id, n)
	}

	if r.hasLater(t) {
		t.Fatalf("namespace %s survived restore %s", laterNamespace, id)
	}

	if _, err := r.st.GetNamespace(t.Context(), "acme"); err != nil {
		t.Fatalf("namespace acme after restore %s: %v", id, err)
	}

	if !bytes.Equal(r.liveBlobBytes(t), r.backupBlobBytes(t)) {
		t.Fatal("the live item key blob differs from the backup after the restore")
	}

	mustState(t, r.st, maintenance.StateNormal)

	if check.areas {
		if areas := r.areasOf(t, id); len(areas) != 0 {
			t.Fatalf("staging areas of restore %s = %v after the end, want none", id, areas)
		}
	}

	r.live.mustNoLiveDelete(t)

	return row
}

// failoverVersion returns the failover version of the namespace acme.
func (r *workerRig) failoverVersion(t *testing.T) int64 {
	t.Helper()

	ns, err := r.st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace acme: %v", err)
	}

	return ns.FailoverVersion
}

// mustUnchanged proves that the live data is the data from after the backup.
func (r *workerRig) mustUnchanged(t *testing.T) {
	t.Helper()

	if !r.hasLater(t) {
		t.Fatalf("namespace %s is gone, want the live data unchanged", laterNamespace)
	}

	r.live.mustNoLiveDelete(t)
}

// gateMode says what a stepGate does to its holder.
type gateMode int

const (
	// gateNone changes nothing: the holder stops only through crash.
	gateNone gateMode = iota
	// gateCrash stops the holder for good when it records the gate step:
	// the record and every later write are lost, as in a kill.
	gateCrash
	// gatePauseStale pauses the holder before the gate step and lets its
	// claim run out. After the release it continues with the stale claim.
	gatePauseStale
	// gatePauseLive pauses the holder before the gate step and keeps its
	// claim alive.
	gatePauseLive
)

// stepGate is the durable store of one instance. It stops or pauses the
// holder at one step, once.
type stepGate struct {
	durable.Store

	canceler durable.StepCanceler
	step     string
	mode     gateMode
	reached  chan struct{}
	release  chan struct{}
	fire     sync.Once
	free     sync.Once
	crashed  atomic.Bool
	frozen   atomic.Bool
}

// newStepGate returns a gate on the store of r at step. The gate is free at
// the end of the test, so no holder stays paused.
func newStepGate(t *testing.T, r *workerRig, step string, mode gateMode) *stepGate {
	t.Helper()

	canceler, ok := r.ops.(durable.StepCanceler)
	if !ok {
		t.Fatal("the durable store does not implement durable.StepCanceler")
	}

	g := &stepGate{
		Store: r.ops, canceler: canceler, step: step, mode: mode, reached: make(chan struct{}),
		release: make(chan struct{}), fire: sync.Once{}, free: sync.Once{}, crashed: atomic.Bool{},
		frozen: atomic.Bool{},
	}

	t.Cleanup(g.open)

	return g
}

// previous returns the step before step, or "" for the first step.
func previous(step string) string {
	steps := backup.RestoreSteps()

	for i, name := range steps {
		if name == step && i > 0 {
			return steps[i-1]
		}
	}

	return ""
}

// Claim implements durable.Store. A pause before the first step holds the
// claim that it returns.
func (g *stepGate) Claim(ctx context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	op, err := g.Store.Claim(ctx, req)
	if err != nil {
		return op, fmt.Errorf("claim: %w", err)
	}

	// A live pause before the first step happens in the stage call, with
	// pausingStage: the runner counts the lease from the start of the claim.
	if g.mode == gatePauseStale && previous(g.step) == "" {
		op.Claim = g.pause(ctx, op.Claim)
	}

	return op, nil
}

// Extend implements durable.Store. A frozen or crashed holder renews
// nothing, and believes that it did.
func (g *stepGate) Extend(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	if g.crashed.Load() || g.frozen.Load() {
		c.ExpiresAt = time.Now().Add(lease)

		return c, nil
	}

	next, err := g.Store.Extend(ctx, c, lease)
	if err != nil {
		return next, fmt.Errorf("extend: %w", err)
	}

	return next, nil
}

// RecordStep implements durable.Store.
func (g *stepGate) RecordStep(ctx context.Context, f durable.StepFence, result []byte) error {
	if g.crashed.Load() {
		return durable.ErrClaimLost
	}

	if g.mode == gateCrash && f.Step == g.step {
		g.crash()

		return durable.ErrClaimLost
	}

	err := g.Store.RecordStep(ctx, f, result)

	if (g.mode == gatePauseStale || g.mode == gatePauseLive) && f.Step == previous(g.step) &&
		(err == nil || errors.Is(err, durable.ErrStepDone)) {
		g.pause(ctx, durable.Claim{Operation: f.Operation, Owner: "", Token: f.Token, ExpiresAt: time.Time{}})
	}

	if err != nil {
		return fmt.Errorf("record step: %w", err)
	}

	return nil
}

// Complete implements durable.Store.
func (g *stepGate) Complete(ctx context.Context, c durable.Claim) error {
	if g.crashed.Load() {
		return durable.ErrClaimLost
	}

	if err := g.Store.Complete(ctx, c); err != nil {
		return fmt.Errorf("complete: %w", err)
	}

	return nil
}

// Fail implements durable.Store.
func (g *stepGate) Fail(ctx context.Context, c durable.Claim, f durable.Failure) error {
	if g.crashed.Load() {
		return durable.ErrClaimLost
	}

	if err := g.Store.Fail(ctx, c, f); err != nil {
		return fmt.Errorf("fail: %w", err)
	}

	return nil
}

// Release implements durable.Store.
func (g *stepGate) Release(ctx context.Context, c durable.Claim) error {
	if g.crashed.Load() {
		return durable.ErrClaimLost
	}

	if err := g.Store.Release(ctx, c); err != nil {
		return fmt.Errorf("release: %w", err)
	}

	return nil
}

// CancelBefore implements durable.StepCanceler.
func (g *stepGate) CancelBefore(ctx context.Context, id durable.ID, step string) error {
	if err := g.canceler.CancelBefore(ctx, id, step); err != nil {
		return fmt.Errorf("cancel before: %w", err)
	}

	return nil
}

// open frees a paused holder.
func (g *stepGate) open() {
	g.free.Do(func() { close(g.release) })
}

// crash stops the holder for good.
func (g *stepGate) crash() {
	g.crashed.Store(true)
	g.fire.Do(func() { close(g.reached) })
}

// pause stops the holder of c until the release. A live pause in a claim
// renews c, because the runner renews only after the claim returns.
func (g *stepGate) pause(ctx context.Context, c durable.Claim) durable.Claim {
	g.fire.Do(func() {
		if g.mode == gatePauseStale {
			g.frozen.Store(true)
		}

		close(g.reached)

		select {
		case <-g.release:
		case <-ctx.Done():
		}
	})

	return c
}

// pausingStage pauses the holder in its stage call, while the runner renews
// the claim: a live pause during the first step.
type pausingStage struct {
	backup.RestoreLive

	gate *stepGate
}

// StageRestore implements backup.RestoreStaging.
func (p pausingStage) StageRestore(
	ctx context.Context, a backup.StagingArea, in backup.RestoreStageInput,
) (backup.Summary, backup.DevStripReport, error) {
	p.gate.pause(ctx, durable.Claim{Operation: "", Owner: "", Token: a.Token, ExpiresAt: time.Time{}})

	sum, report, err := p.RestoreLive.StageRestore(ctx, a, in)
	if err != nil {
		return sum, report, fmt.Errorf("stage restore: %w", err)
	}

	return sum, report, nil
}

// deleteWatch is the live blob store of the services. It records each delete
// of a key outside the temporary keys of the restores: a restore never deletes
// a live key.
type deleteWatch struct {
	blob.Store

	mu      sync.Mutex
	deleted []string
}

// Delete implements blob.Store.
func (d *deleteWatch) Delete(ctx context.Context, key blob.Key) error {
	if !strings.HasPrefix(key.String(), "restore-tmp/") {
		d.mu.Lock()
		d.deleted = append(d.deleted, key.String())
		d.mu.Unlock()
	}

	if err := d.Store.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}

// mustNoLiveDelete proves that no live key was deleted.
func (d *deleteWatch) mustNoLiveDelete(t *testing.T) {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.deleted) != 0 {
		t.Fatalf("the restore deleted the live keys %v", d.deleted)
	}
}

// overwritingBlobs is a blob store with no conditional writes: a put over a
// key that holds an object replaces it, as on a store without If-None-Match.
type overwritingBlobs struct {
	*deleteWatch
}

// Put implements blob.Store.
func (o overwritingBlobs) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read the object of %s: %w", key, err)
	}

	err = o.Store.Put(ctx, key, bytes.NewReader(data))
	if !errors.Is(err, blob.ErrExists) {
		if err != nil {
			return fmt.Errorf("put: %w", err)
		}

		return nil
	}

	// The replace is one write of the store, not a delete of the restore.
	if err := o.Store.Delete(ctx, key); err != nil {
		return fmt.Errorf("replace: %w", err)
	}

	if err := o.Store.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("replace: %w", err)
	}

	return nil
}

// testWorkerTakeover stops the holder of instance a for good when it records
// step. Instance b takes the restore over after the lease and ends it once.
func testWorkerTakeover(t *testing.T, newStore RestoreFactory, step string) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	gate := newStepGate(t, r, step, gateCrash)
	a := r.service(
		t,
		workerService{owner: "instance-a", ops: gate, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)
	b := r.service(
		t,
		workerService{owner: "instance-b", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, false)
	aDone := runAsync(t, a, gate)
	<-aDone
	waitReached(t, gate, aDone)

	r.driveToEnd(t, b, id)
	r.mustRestoredOnce(t, id, restoredCheck{areas: true})
}

// testWorkerStaleHolder pauses the holder of instance a before step and lets
// its claim run out. Instance b takes the restore over and ends it. Then a
// continues step with its stale claim: no fenced write of it lands.
func testWorkerStaleHolder(t *testing.T, newStore RestoreFactory, step string) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	gate := newStepGate(t, r, step, gatePauseStale)
	a := r.service(
		t,
		workerService{owner: "instance-a", ops: gate, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)
	b := r.service(
		t,
		workerService{owner: "instance-b", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, false)
	aDone := runAsync(t, a, gate)
	waitReached(t, gate, aDone)

	r.driveToEnd(t, b, id)
	ended := r.mustRestoredOnce(t, id, restoredCheck{areas: true})

	gate.open()
	<-aDone

	again := r.mustRestoredOnce(t, id, restoredCheck{areas: step != backup.RestoreStepStage})
	if !again.EndedAt.Equal(ended.EndedAt) || again.State != ended.State {
		t.Fatalf("restore %s after the stale holder = %+v, want the end %+v", id, again, ended)
	}
}

// testWorkerOtherStart pauses the holder of instance a before step, and keeps
// its claim. Another instance starts and runs its worker: it claims nothing
// and fails no row. Then a ends the restore once.
func testWorkerOtherStart(t *testing.T, newStore RestoreFactory, step string) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	gate := newStepGate(t, r, step, gatePauseLive)

	var live backup.RestoreLive
	if step == backup.RestoreStepStage {
		live = pausingStage{RestoreLive: r.st, gate: gate}
	}

	a := r.service(
		t,
		workerService{owner: "instance-a", ops: gate, live: live, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, false)
	aDone := runAsync(t, a, gate)
	waitReached(t, gate, aDone)

	held := r.opOf(t, id)

	c := r.service(
		t,
		workerService{owner: "instance-c", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)
	workFor(t, c, 2*workerLease)

	row, err := r.st.GetRestore(t.Context(), id)
	if err != nil {
		t.Fatalf("get restore %s: %v", id, err)
	}

	if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
		t.Fatalf("restore %s = %q (%s) after the start of another instance, want running", id, row.State, row.Error)
	}

	if op := r.opOf(t, id); op.State != durable.StateRunning || op.Claim.Token != held.Claim.Token {
		t.Fatalf("operation after the start of another instance = %q token %d, want running with token %d",
			op.State, op.Claim.Token, held.Claim.Token)
	}

	gate.open()
	<-aDone

	r.mustRestoredOnce(t, id, restoredCheck{areas: true})
}

// workFor runs the worker of restores for d, as an instance that starts.
func workFor(t *testing.T, restores *backup.RestoreService, d time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), d)
	defer cancel()

	restores.Work(ctx)
}

// swapKiller stops the holder in the swap transaction: it cancels the
// context of the swap shortly after the swap starts, then stops the holder
// for good.
type swapKiller struct {
	backup.RestoreLive

	gate *stepGate
}

// ApplyRestoreArea implements backup.RestoreStaging.
func (k swapKiller) ApplyRestoreArea(
	ctx context.Context, f durable.StepFence, a backup.StagingArea, keepAccess bool,
) (backup.KeepAccessResult, error) {
	swapCtx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(time.Millisecond, cancel)

	defer timer.Stop()
	defer cancel()

	_, err := k.RestoreLive.ApplyRestoreArea(swapCtx, f, a, keepAccess)

	k.gate.crash()

	return backup.KeepAccessResult{DroppedTempAdmins: 0}, errors.Join(durable.ErrClaimLost, err)
}

func testWorkerSwapKill(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	gate := newStepGate(t, r, backup.RestoreStepSwap, gateNone)
	killer := swapKiller{RestoreLive: r.st, gate: gate}
	a := r.service(
		t,
		workerService{
			owner:      "instance-a",
			ops:        gate,
			live:       killer,
			maint:      nil,
			blobs:      nil,
			lockWait:   0,
			afterApply: nil,
		},
	)
	b := r.service(
		t,
		workerService{owner: "instance-b", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, false)
	aDone := runAsync(t, a, gate)
	<-aDone
	waitReached(t, gate, aDone)

	// The swap either committed with its step record or changed nothing.
	// Either way the restore ends once, with one swap.
	r.driveToEnd(t, b, id)
	r.mustRestoredOnce(t, id, restoredCheck{areas: true})
}

// cancelSpec says where cancelBefore cancels a restore: before step, of a
// check-only restore or not, with the maintenance that maintOf returns, or the
// maintenance of the rig when maintOf is nil.
type cancelSpec struct {
	step      string
	checkOnly bool
	maintOf   func(r *workerRig) backup.RestoreMaintenance
}

// cancelBefore pauses a restore before step and cancels it through another
// instance. It returns the rig, the restore ID and the other instance.
func cancelBefore(
	t *testing.T, newStore RestoreFactory, spec cancelSpec,
) (*workerRig, backup.RestoreID, *backup.RestoreService) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	step := spec.step

	var maint backup.RestoreMaintenance
	if spec.maintOf != nil {
		maint = spec.maintOf(r)
	}

	gate := newStepGate(t, r, step, gatePauseLive)
	a := r.service(
		t,
		workerService{
			owner:      "instance-a",
			ops:        gate,
			live:       nil,
			maint:      maint,
			blobs:      nil,
			lockWait:   0,
			afterApply: nil,
		},
	)
	c := r.service(
		t,
		workerService{owner: "instance-c", ops: nil, live: nil, maint: maint, blobs: nil, lockWait: 0, afterApply: nil},
	)

	r.failover = r.failoverVersion(t)

	id := r.start(t, a, spec.checkOnly, false)
	aDone := runAsync(t, a, gate)
	waitReached(t, gate, aDone)

	if err := c.Cancel(t.Context(), id); err != nil {
		t.Fatalf("cancel restore %s before the %s step: %v", id, step, err)
	}

	gate.open()
	<-aDone

	row := r.driveToEnd(t, c, id)
	if row.State != backup.RestoreFailed || row.Error != backup.RestoreCanceledText {
		t.Fatalf("canceled restore = %q (%s), want failed with %q", row.State, row.Error, backup.RestoreCanceledText)
	}

	if err := c.Cancel(t.Context(), id); err != nil {
		t.Fatalf("second cancel of restore %s: %v, want it accepted again", id, err)
	}

	if op := r.opOf(t, id); op.State != durable.StateCanceled {
		t.Fatalf("operation of the canceled restore = %q, want canceled", op.State)
	}

	return r, id, c
}

func testWorkerCancelBeforeSwap(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r, _, _ := cancelBefore(t, newStore, cancelSpec{step: backup.RestoreStepSwap, checkOnly: false, maintOf: nil})

	r.mustUnchanged(t)
	mustState(t, r.st, maintenance.StateNormal)
}

func testWorkerCancelAfterSwap(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	gate := newStepGate(t, r, backup.RestoreStepAfterApply, gatePauseLive)
	a := r.service(
		t,
		workerService{owner: "instance-a", ops: gate, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)
	c := r.service(
		t,
		workerService{owner: "instance-c", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, false)
	aDone := runAsync(t, a, gate)
	waitReached(t, gate, aDone)

	if err := c.Cancel(t.Context(), id); !errors.Is(err, backup.ErrRestoreSwapped) {
		t.Fatalf("cancel after the swap: err = %v, want ErrRestoreSwapped", err)
	}

	gate.open()
	<-aDone

	r.mustRestoredOnce(t, id, restoredCheck{areas: true})
}

func testWorkerNextCleanup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r, first, c := cancelBefore(t, newStore, cancelSpec{step: backup.RestoreStepSwap, checkOnly: false, maintOf: nil})

	if len(r.areasOf(t, first)) == 0 {
		t.Fatalf("restore %s canceled before the swap left no staging area, want one for the next cleanup", first)
	}

	// A temporary key of the ended restore goes, a key of a restore with no
	// row stays: another installation can share the store.
	endedKey := mustBlobKey(t, "restore-tmp/"+first.String()+"/7/left")
	foreignKey := mustBlobKey(t, "restore-tmp/"+backup.MintRestoreID().String()+"/1/foreign")
	putBlob(t, r.live.Store, endedKey, []byte("left"))
	putBlob(t, r.live.Store, foreignKey, []byte("foreign"))

	second := r.start(t, c, false, false)
	r.driveToEnd(t, c, second)
	r.mustRestoredOnce(t, second, restoredCheck{areas: true})

	if areas := r.areasOf(t, first); len(areas) != 0 {
		t.Fatalf("staging areas of the ended restore %s = %v after the next restore, want none", first, areas)
	}

	if _, err := r.live.Get(t.Context(), endedKey); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("temporary key of the ended restore after the next cleanup: err = %v, want ErrNotFound", err)
	}

	if got := readBlob(t, r.live.Store, foreignKey.String()); string(got) != "foreign" {
		t.Fatalf("temporary key of a restore with no row = %q, want it kept", got)
	}
}

func testWorkerOverwritingBlobs(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	gate := newStepGate(t, r, backup.RestoreStepCopyBlobs, gatePauseLive)
	a := r.service(t, workerService{
		owner:      "instance-a",
		ops:        gate,
		live:       nil,
		maint:      nil,
		blobs:      overwritingBlobs{deleteWatch: r.live},
		afterApply: nil,
		lockWait:   0,
	})

	id := r.start(t, a, false, false)
	aDone := runAsync(t, a, gate)
	waitReached(t, gate, aDone)

	// The live key holds other bytes after the pre-backup took its copy. The
	// store would replace the key, because it ignores conditional writes. The
	// copy step refuses: the restore fails, and the key keeps its bytes.
	differing := []byte("bytes that differ from the backup")
	putBlob(t, r.live.Store, r.seed.liveBlob, differing)

	gate.open()
	<-aDone

	row := r.driveToEnd(t, a, id)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, "corrupt") {
		t.Fatalf("restore over a live key that differs = %q %q, want failed as corrupt", row.State, row.Error)
	}

	if got := r.liveBlobBytes(t); !bytes.Equal(got, differing) {
		t.Fatalf("live key = %q, want the bytes that it held before the restore", got)
	}

	refusal := "restore " + id.String() + " refused to overwrite live blob " + strconv.Quote(r.seed.liveBlob.String())
	if n := r.restoreEntries(t, refusal); n != 1 {
		t.Fatalf("audit entries of the refusal = %d, want 1", n)
	}
}

func testWorkerCorruptObject(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	before := r.liveBlobBytes(t)
	putBlob(t, r.target, mustBlobKey(t, "blobs/"+r.seed.liveBlob.String()), []byte("corrupt"))

	a := r.service(
		t,
		workerService{owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, false)
	if row := r.driveToEnd(t, a, id); row.State != backup.RestoreFailed {
		t.Fatalf("restore of a corrupt backup = %q, want failed", row.State)
	}

	if !bytes.Equal(r.liveBlobBytes(t), before) {
		t.Fatal("the failed restore changed the live item key blob")
	}

	r.mustUnchanged(t)
	mustState(t, r.st, maintenance.StateNormal)
}

func testWorkerDeleterInFlight(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	gate := newStepGate(t, r, backup.RestoreStepAfterApply, gatePauseLive)
	a := r.service(
		t,
		workerService{owner: "instance-a", ops: gate, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, false)
	aDone := runAsync(t, a, gate)
	waitReached(t, gate, aDone)

	// A deleter that listed the keys before the swap deletes a key that the
	// restored data names.
	if err := r.live.Store.Delete(t.Context(), r.seed.liveBlob); err != nil {
		t.Fatalf("delete the live blob: %v", err)
	}

	gate.open()
	<-aDone

	r.mustRestoredOnce(t, id, restoredCheck{areas: true})
}

func testWorkerOperatorMaintenance(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	if err := r.st.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode as an operator: %v", err)
	}

	a := r.service(
		t,
		workerService{owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, false)
	if row := r.driveToEnd(t, a, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore in the maintenance mode of an operator = %q (%s), want succeeded", row.State, row.Error)
	}

	mustState(t, r.st, maintenance.StateMaintenance)

	if r.hasLater(t) {
		t.Fatalf("namespace %s survived the restore", laterNamespace)
	}
}

// busyCleanup refuses the first cleanup with ErrRestoreBusy, as a drop that
// waited past its lock limit.
type busyCleanup struct {
	backup.RestoreLive

	calls *atomic.Int32
}

// CleanRestoreAreas implements backup.RestoreStaging.
func (b busyCleanup) CleanRestoreAreas(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, lockWait time.Duration,
) ([]backup.StagingArea, error) {
	if b.calls.Add(1) == 1 {
		return nil, fmt.Errorf("drop the areas of restore %s: %w", id, backup.ErrRestoreBusy)
	}

	areas, err := b.RestoreLive.CleanRestoreAreas(ctx, f, id, lockWait)
	if err != nil {
		return nil, fmt.Errorf("clean restore areas: %w", err)
	}

	return areas, nil
}

func testWorkerCleanupBusy(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	calls := &atomic.Int32{}
	live := busyCleanup{RestoreLive: r.st, calls: calls}
	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: live, maint: nil, blobs: nil, lockWait: time.Millisecond, afterApply: nil,
	})

	id := r.start(t, a, false, false)
	r.driveToEnd(t, a, id)
	r.mustRestoredOnce(t, id, restoredCheck{areas: true})

	if n := calls.Load(); n < 2 {
		t.Fatalf("cleanup calls = %d, want a retry after the busy drop", n)
	}
}

func testWorkerCheckOnly(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	a := r.service(
		t,
		workerService{owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, true, false)
	if row := r.driveToEnd(t, a, id); row.State != backup.RestoreSucceeded || row.PreBackupID != "" {
		t.Fatalf("check-only restore = %q (%s) pre-backup %q, want succeeded with none",
			row.State, row.Error, row.PreBackupID)
	}

	r.mustUnchanged(t)
	mustState(t, r.st, maintenance.StateNormal)

	if areas := r.areasOf(t, id); len(areas) != 0 {
		t.Fatalf("staging areas of the check-only restore = %v, want none", areas)
	}

	op := r.opOf(t, id)
	for _, step := range []string{backup.RestoreStepStage, backup.RestoreStepCheck} {
		if _, ok := op.Step(step); !ok {
			t.Fatalf("check-only restore did not record the %s step", step)
		}
	}
}

func testWorkerHeld(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	held := func(r *workerRig) backup.RestoreMaintenance { return StateMaintenance{State: r.st, Hold: true} }
	r, _, _ := cancelBefore(t, newStore, cancelSpec{step: backup.RestoreStepPreBackup, checkOnly: false, maintOf: held})

	r.mustUnchanged(t)
	mustState(t, r.st, maintenance.StateMaintenance)
}

// forcedMaintenance enters maintenance mode as StateMaintenance does, and,
// with force, answers that it canceled one unit.
type forcedMaintenance struct {
	inner StateMaintenance
}

// canceledUnit is the unit that forcedMaintenance cancels.
const canceledUnit = "stack acme/web update upd-1"

// Get implements backup.RestoreMaintenance.
func (f forcedMaintenance) Get(ctx context.Context) (maintenance.State, error) {
	return f.inner.Get(ctx)
}

// EnterWith implements backup.RestoreMaintenance.
func (f forcedMaintenance) EnterWith(
	ctx context.Context, opts maintenance.EnterOptions,
) (maintenance.EnterResult, error) {
	res, err := f.inner.EnterWith(ctx, opts)
	if err != nil || !opts.Force {
		return res, err
	}

	// The domain service checks the claim of the writer just before each
	// cancel, and the writer of the restore stores the cancel then.
	if err := opts.Writer.Check(ctx, canceledUnit); err != nil {
		return maintenance.EnterResult{Canceled: nil}, fmt.Errorf("check before the cancel: %w", err)
	}

	return maintenance.EnterResult{Canceled: []string{canceledUnit}}, nil
}

// RecheckWith implements backup.RestoreMaintenance.
func (f forcedMaintenance) RecheckWith(
	ctx context.Context, opts maintenance.EnterOptions,
) (maintenance.EnterResult, error) {
	return f.inner.RecheckWith(ctx, opts)
}

// LeaveWith implements backup.RestoreMaintenance.
func (f forcedMaintenance) LeaveWith(ctx context.Context, w maintenance.StateWriter) error {
	return f.inner.LeaveWith(ctx, w)
}

// Held implements backup.RestoreMaintenance.
func (f forcedMaintenance) Held(ctx context.Context) (bool, error) {
	return f.inner.Held(ctx)
}

func testWorkerForceAudit(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	maint := forcedMaintenance{inner: StateMaintenance{State: r.st, Hold: false}}
	a := r.service(
		t,
		workerService{owner: "instance-a", ops: nil, live: nil, maint: maint, blobs: nil, lockWait: 0, afterApply: nil},
	)

	id := r.start(t, a, false, true)
	r.driveToEnd(t, a, id)
	r.mustRestoredOnce(t, id, restoredCheck{areas: true})

	rec, ok := r.opOf(t, id).Step(backup.RestoreStepEnterMaintenance)
	if !ok {
		t.Fatal("the restore recorded no enter step")
	}

	enter, err := backup.DecodeRestoreMaintenance(rec.Result)
	if err != nil {
		t.Fatalf("decode the enter result: %v", err)
	}

	want := "restore " + id.String() + " claim token " + strconv.FormatInt(enter.Token, 10) +
		" attempted to cancel the active work: " + canceledUnit
	if n := r.restoreEntries(t, want); n != 1 {
		t.Fatalf("audit entries of the canceled work = %d, want 1 that names %q", n, want)
	}
}
