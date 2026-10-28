package backup_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
)

// durableWait is the longest time that a test waits for a state of the
// durable backup. Polls end as soon as the state holds.
const durableWait = 15 * time.Second

// durablePoll is the pause between two polls.
const durablePoll = 10 * time.Millisecond

// errFrozenForever is not used as a value: it names the stop of a test wait.
var errTestWaitTimeout = errors.New("test wait timed out")

// waitFor polls cond until it holds or durableWait passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(durableWait)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%v: %s", errTestWaitTimeout, what)
		}

		select {
		case <-time.After(durablePoll):
		case <-t.Context().Done():
			t.Fatalf("test ended while it waited: %s", what)
		}
	}
}

// waitRow waits until the row of backup id is in state, and returns it.
func waitRow(t *testing.T, reg *fakeRegistry, id backup.ID, state backup.State) backup.Record {
	t.Helper()

	var row backup.Record

	waitFor(t, fmt.Sprintf("backup %s in state %s", id, state), func() bool {
		got, err := reg.GetBackup(t.Context(), id)
		row = got

		return err == nil && got.State == state
	})

	return row
}

// findOperation returns the durable operation of backup id.
func findOperation(t *testing.T, ops durable.Store, id backup.ID) (durable.Operation, bool) {
	t.Helper()

	all, err := ops.List(t.Context(), durable.Filter{Kind: backup.OperationKind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list backup operations: %v", err)
	}

	for i := range all {
		if all[i].Key == backup.OperationKey(id) {
			return all[i], true
		}
	}

	var none durable.Operation

	return none, false
}

// waitOperation waits until the operation of backup id satisfies cond.
func waitOperation(
	t *testing.T, ops durable.Store, id backup.ID, what string, cond func(durable.Operation) bool,
) durable.Operation {
	t.Helper()

	var op durable.Operation

	waitFor(t, what, func() bool {
		got, ok := findOperation(t, ops, id)
		op = got

		return ok && cond(got)
	})

	return op
}

// durableSetup holds one registry and one target that several services share,
// as several instances share one database and one target.
type durableSetup struct {
	reg    *fakeRegistry
	target *memStore
	source *memStore
}

func newDurableSetup() durableSetup {
	return durableSetup{reg: newFakeRegistry(), target: newMemStore(), source: newMemStore()}
}

// service builds one instance over the shared registry and target. ops is the
// durable store of the instance.
func (d durableSetup) service(
	t *testing.T, exporter backup.PortableExporter, ops durable.Store, lease time.Duration, owner string,
) *backup.Service {
	t.Helper()

	svc, err := backup.NewService(backup.Config{
		Exporter:    exporter,
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    d.reg,
		Operations:  ops,
		Blobs:       d.source,
		StateOpener: nil,
		Target:      d.target,
		TargetName:  "mem:target",
		Now:         time.Now,
		Version:     "test-1.0",
		MaxSQLBytes: 0,
		Owner:       owner,
		Lease:       lease,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc
}

// worker runs svc.Work until stop is called. stop cancels the context and
// waits for Work to return. The cleanup calls stop too, so no goroutine
// outlives the test.
type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func startWorker(t *testing.T, svc *backup.Service) *worker {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	w := &worker{cancel: cancel, done: make(chan struct{}), once: sync.Once{}}

	go func() {
		defer close(w.done)
		defer cancel()

		svc.Work(ctx)
	}()

	t.Cleanup(w.stop)

	return w
}

func (w *worker) stop() {
	w.once.Do(w.cancel)
	<-w.done
}

// tableData is the content of every table file of the test exports.
var tableData = []byte("table file content") //nolint:gochecknoglobals // A read-only fixture shared by the tests.

// gateExporter writes the table files like a real export. Its first call
// writes the first table file, announces it, and waits for the gate; the
// call ignores its context, as a hung export does. Later calls run at once.
type gateExporter struct {
	entered chan struct{}
	gate    chan struct{}
	// testDone ends the wait when the test ends, so a failed test does not
	// leave the worker stuck in the export.
	testDone <-chan struct{}
	calls    atomic.Int64
	// idle makes the first call wait before it writes any file. Without it,
	// the first call writes one table file first, so a takeover finds a
	// partial backup in the target.
	idle bool
}

func newGateExporter(t *testing.T) *gateExporter {
	t.Helper()

	return &gateExporter{
		entered:  make(chan struct{}),
		gate:     make(chan struct{}),
		testDone: t.Context().Done(),
		calls:    atomic.Int64{},
		idle:     false,
	}
}

func (g *gateExporter) ExportPortable(
	_ context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	if g.calls.Add(1) > 1 {
		return writeTestTableFile(sink, tableData, testSummary())
	}

	if !g.idle {
		if err := writeOneTableFile(sink); err != nil {
			return backup.Summary{}, nil, err
		}
	}

	close(g.entered)

	select {
	case <-g.gate:
	case <-g.testDone:
	}

	return writeTestTableFile(sink, tableData, testSummary())
}

// writeOneTableFile writes the first table file only.
func writeOneTableFile(sink backup.PortableSink) error {
	first, err := sink(backup.PortablePath(backup.Tables[0]))
	if err != nil {
		return fmt.Errorf("open first table file: %w", err)
	}

	if _, err := first.Write(tableData); err != nil {
		return fmt.Errorf("write first table file: %w", err)
	}

	if err := first.Close(); err != nil {
		return fmt.Errorf("close first table file: %w", err)
	}

	return nil
}

// frozenStore is the durable store handle of an instance that stopped
// without a release: after freeze, Extend answers success with a far expiry
// and does not reach the store, so the instance keeps working and believes it
// holds the claim, while the real claim expires. Every other write reaches
// the store, which refuses it once another instance holds the claim.
type frozenStore struct {
	durable.Store

	frozen atomic.Bool
}

func (f *frozenStore) Extend(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	if f.frozen.Load() {
		c.ExpiresAt = time.Now().Add(time.Hour)

		return c, nil
	}

	claim, err := f.Store.Extend(ctx, c, lease)
	if err != nil {
		return durable.Claim{}, fmt.Errorf("extend: %w", err)
	}

	return claim, nil
}

func TestTakeoverAfterTheHolderStopsWithoutRelease(t *testing.T) {
	t.Parallel()

	const lease = 2 * time.Second

	setup := newDurableSetup()
	id := backup.ID("bkp-takeover")

	// The instance A holds the claim and stops inside the export.
	exporterA := newGateExporter(t)
	storeA := &frozenStore{Store: setup.reg.ops, frozen: atomic.Bool{}}
	svcA := setup.service(t, exporterA, storeA, lease, "instance-a")
	workerA := startWorker(t, svcA)

	if err := svcA.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	<-exporterA.entered
	storeA.frozen.Store(true)

	// The instance B takes the operation over after the lease ends.
	svcB := setup.service(
		t,
		&stubExporter{sum: testSummary(), data: tableData, err: nil},
		setup.reg.ops,
		lease,
		"instance-b",
	)
	startWorker(t, svcB)

	row := waitRow(t, setup.reg, id, backup.StateSucceeded)

	// The runner's Complete lands after the row reads succeeded.
	op := waitOperation(t, setup.reg.ops, id, "the operation succeeded", func(o durable.Operation) bool {
		return o.State == durable.StateSucceeded
	})
	if op.State != durable.StateSucceeded || op.Attempt != 2 || op.Expiries != 1 {
		t.Fatalf(
			"operation = state %s, attempt %d, expiries %d, want succeeded, 2, 1",
			op.State,
			op.Attempt,
			op.Expiries,
		)
	}

	assertCompleteBackup(t, setup.target, id)

	// A resumes after B finished. Its write of the row must fail on the claim.
	close(exporterA.gate)
	waitFor(t, "the refused finish of the first instance", func() bool { return setup.reg.lostFinishes.Load() == 1 })
	workerA.stop()

	after, err := setup.reg.GetBackup(t.Context(), id)
	if err != nil || after != row {
		t.Fatalf("row after the stale holder resumed = %+v, %v, want unchanged %+v", after, err, row)
	}

	assertCompleteBackup(t, setup.target, id)
}

// assertCompleteBackup proves that the target holds exactly one complete
// backup id: every table file once, the manifest, and nothing else under the
// directory of the backup.
func assertCompleteBackup(t *testing.T, target *memStore, id backup.ID) {
	t.Helper()

	if err := backup.Check(t.Context(), target, id); err != nil {
		t.Fatalf("check backup %s: %v", id, err)
	}

	objects, err := target.List(t.Context(), "backups/"+id.String())
	if err != nil {
		t.Fatalf("list backup %s: %v", id, err)
	}

	got := map[string]bool{}
	for _, obj := range objects {
		got[obj.Key.String()] = true
	}

	want := map[string]bool{"backups/" + id.String() + "/manifest.json": true}
	for _, table := range backup.Tables {
		want["backups/"+id.String()+"/"+backup.PortablePath(table)] = true
	}

	if len(got) != len(want) || len(objects) != len(want) {
		t.Fatalf("target holds %d objects of backup %s, want %d", len(objects), id, len(want))
	}

	for key := range want {
		if !got[key] {
			t.Fatalf("target misses %q of backup %s", key, id)
		}
	}
}

func TestTakeoverKeepsTheBlobCopies(t *testing.T) {
	t.Parallel()

	const lease = 2 * time.Second

	setup := newDurableSetup()
	id := backup.ID("bkp-takeover-blob")
	content := []byte(`{"state":1}`)
	setup.source.storeObject(t, "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1", content)

	exporter := &blobGateExporter{
		gateExporter: newGateExporter(t),
		ref:          blobRef("namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1", content),
	}
	storeA := &frozenStore{Store: setup.reg.ops, frozen: atomic.Bool{}}
	svcA := setup.service(t, exporter, storeA, lease, "instance-a")
	workerA := startWorker(t, svcA)

	if err := svcA.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	<-exporter.entered
	storeA.frozen.Store(true)

	svcB := setup.service(t, exporter, setup.reg.ops, lease, "instance-b")
	startWorker(t, svcB)

	waitRow(t, setup.reg, id, backup.StateSucceeded)
	close(exporter.gate)
	waitFor(t, "the refused finish of the first instance", func() bool { return setup.reg.lostFinishes.Load() == 1 })
	workerA.stop()

	assertCompleteBackup(t, setup.target, id)

	got := setup.target.readObject(t, "blobs/namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws1/sv1/a1")
	if !bytes.Equal(got, content) {
		t.Fatalf("blob copy = %q, want %q", got, content)
	}
}

// blobGateExporter is a gateExporter whose summary names one blob.
type blobGateExporter struct {
	*gateExporter

	ref backup.BlobRef
}

func (b *blobGateExporter) ExportPortable(
	ctx context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	sum, files, err := b.gateExporter.ExportPortable(ctx, sink)
	sum.Blobs = []backup.BlobRef{b.ref}

	return sum, files, err
}

func TestWriteStepFailureEndsRowAndOperationFailed(t *testing.T) {
	t.Parallel()

	setup := newDurableSetup()
	id := backup.ID("bkp-write-fails")
	svc := setup.service(t, &stubExporter{sum: testSummary(), data: nil, err: errExport}, setup.reg.ops,
		200*time.Millisecond, "")
	startWorker(t, svc)

	if err := svc.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	row := waitRow(t, setup.reg, id, backup.StateFailed)
	if !strings.Contains(row.Error, errExport.Error()) {
		t.Fatalf("row error = %q, want the text of the export error", row.Error)
	}

	op := waitOperation(t, setup.reg.ops, id, "the operation ends", func(o durable.Operation) bool {
		return o.State.Terminal()
	})
	if op.State != durable.StateFailed {
		t.Fatalf("operation state = %s, want failed", op.State)
	}

	if setup.target.has("backups/" + id.String() + "/manifest.json") {
		t.Fatal("target holds a manifest of the failed backup, want none")
	}
}

// stopExporter blocks its first call until its context ends, as an export
// does while the worker shuts down. Later calls export at once.
type stopExporter struct {
	entered chan struct{}
	calls   atomic.Int64
}

func (s *stopExporter) ExportPortable(
	ctx context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	if s.calls.Add(1) == 1 {
		close(s.entered)
		<-ctx.Done()

		return backup.Summary{}, nil, fmt.Errorf("stopped: %w", ctx.Err())
	}

	return writeTestTableFile(sink, tableData, testSummary())
}

func TestShutdownDuringWriteStepLeavesTheBackupForAnotherWorker(t *testing.T) {
	t.Parallel()

	const lease = 200 * time.Millisecond

	setup := newDurableSetup()
	id := backup.ID("bkp-shutdown")
	exporter := &stopExporter{entered: make(chan struct{}), calls: atomic.Int64{}}
	svc := setup.service(t, exporter, setup.reg.ops, lease, "old-instance")
	old := startWorker(t, svc)

	if err := svc.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	<-exporter.entered
	old.stop()

	// The row stays running: the stop writes nothing to it. The runner
	// released the claim, so the operation waits for a new claim.
	row, err := setup.reg.GetBackup(t.Context(), id)
	if err != nil || row.State != backup.StateRunning {
		t.Fatalf("row after the shutdown = %+v, %v, want running", row, err)
	}

	op := waitOperation(t, setup.reg.ops, id, "the claim is released", func(o durable.Operation) bool {
		return o.State == durable.StatePending
	})
	if op.Attempt != 1 || op.Failures != 0 {
		t.Fatalf("operation = attempt %d, failures %d, want 1, 0", op.Attempt, op.Failures)
	}

	// The new instance finishes the same backup.
	replacement := setup.service(t, exporter, setup.reg.ops, lease, "new-instance")
	startWorker(t, replacement)
	waitRow(t, setup.reg, id, backup.StateSucceeded)
	assertCompleteBackup(t, setup.target, id)

	// The runner's Complete lands after the row reads succeeded.
	final := waitOperation(t, setup.reg.ops, id, "the operation ends", func(o durable.Operation) bool {
		return o.State.Terminal()
	})
	if final.State != durable.StateSucceeded || final.Attempt != 2 || final.Expiries != 0 {
		t.Fatalf("operation = %s, attempt %d, expiries %d, want succeeded, 2, 0",
			final.State, final.Attempt, final.Expiries)
	}
}

// panicExporter panics on its first call and exports on later calls.
type panicExporter struct {
	calls atomic.Int64
}

func (p *panicExporter) ExportPortable(
	_ context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	if p.calls.Add(1) == 1 {
		panic("exporter bug")
	}

	return writeTestTableFile(sink, tableData, testSummary())
}

func TestEndedOperationWithoutFinishWriteFailsTheRow(t *testing.T) {
	t.Parallel()

	setup := newDurableSetup()
	first := backup.ID("bkp-panics")
	second := backup.ID("bkp-after-panic")
	svc := setup.service(t, &panicExporter{calls: atomic.Int64{}}, setup.reg.ops, 150*time.Millisecond, "")
	startWorker(t, svc)

	if err := svc.Start(t.Context(), first); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	op := waitOperation(t, setup.reg.ops, first, "the operation ends", func(o durable.Operation) bool {
		return o.State.Terminal()
	})
	if op.State != durable.StateFailed || !strings.Contains(op.LastError, durable.ErrStepPanic.Error()) {
		t.Fatalf("operation = %s with error %q, want failed with the panic error", op.State, op.LastError)
	}

	// The step wrote no row. The reconcile loop of Work marks it failed.
	row := waitRow(t, setup.reg, first, backup.StateFailed)
	if row.Error == "" || row.EndedAt.IsZero() {
		t.Fatalf("row = %+v, want an error text and an end time", row)
	}

	// A new backup can start now.
	if err := svc.Start(t.Context(), second); err != nil {
		t.Fatalf("start backup after the failed one: %v", err)
	}

	waitRow(t, setup.reg, second, backup.StateSucceeded)
}

func TestStartRefusesAnInvalidID(t *testing.T) {
	t.Parallel()

	setup := newDurableSetup()
	svc := setup.service(t, &stubExporter{sum: testSummary(), data: tableData, err: nil}, setup.reg.ops, 0, "")

	for _, id := range []backup.ID{"", "BKP Upper", "bkp-a/../b", "bkp-a b"} {
		if err := svc.Start(t.Context(), id); err == nil {
			t.Fatalf("start backup %q: err = nil, want an error", id)
		}
	}

	rows, err := setup.reg.ListBackups(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows = %v, %v, want none", rows, err)
	}

	ops, err := setup.reg.ops.List(t.Context(), durable.Filter{Kind: "", States: nil, Limit: 0})
	if err != nil || len(ops) != 0 {
		t.Fatalf("operations = %v, %v, want none", ops, err)
	}
}

func TestStartWhileAnotherBackupRunsLeavesNoOperation(t *testing.T) {
	t.Parallel()

	setup := newDurableSetup()
	svc := setup.service(t, &stubExporter{sum: testSummary(), data: tableData, err: nil}, setup.reg.ops, 0, "")
	first, refused := backup.ID("bkp-first"), backup.ID("bkp-refused")

	if err := svc.Start(t.Context(), first); err != nil {
		t.Fatalf("start first backup: %v", err)
	}

	if err := svc.Start(t.Context(), refused); !errors.Is(err, backup.ErrAlreadyRunning) {
		t.Fatalf("start second backup: err = %v, want ErrAlreadyRunning", err)
	}

	if _, ok := findOperation(t, setup.reg.ops, refused); ok {
		t.Fatal("the refused backup has an operation, want none")
	}

	if _, ok := findOperation(t, setup.reg.ops, first); !ok {
		t.Fatal("the first backup has no operation, want one")
	}

	if _, err := setup.reg.GetBackup(t.Context(), refused); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("row of the refused backup: err = %v, want ErrNotFound", err)
	}
}

func TestLeaseRenewalKeepsASlowStepFromBeingTakenOver(t *testing.T) {
	t.Parallel()

	// The lease is long against the renewal of lease/3, so a renewal that a
	// loaded machine delays by a few hundred milliseconds still lands inside
	// the lease. The step still runs for more than three leases.
	const lease = 1500 * time.Millisecond

	setup := newDurableSetup()
	id := backup.ID("bkp-slow")
	exporter := newGateExporter(t)
	exporter.idle = true
	first := setup.service(t, exporter, setup.reg.ops, lease, "instance-a")
	second := setup.service(t, exporter, setup.reg.ops, lease, "instance-b")
	startWorker(t, first)
	startWorker(t, second)

	if err := first.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	<-exporter.entered

	// The step runs for more than three leases.
	select {
	case <-time.After(4 * lease):
	case <-t.Context().Done():
		t.Fatal("test ended before the step finished")
	}

	close(exporter.gate)
	waitRow(t, setup.reg, id, backup.StateSucceeded)

	op, _ := findOperation(t, setup.reg.ops, id)
	if op.Attempt != 1 || op.Expiries != 0 {
		t.Fatalf("operation = attempt %d, expiries %d, want 1, 0: the step was taken over", op.Attempt, op.Expiries)
	}

	if calls := exporter.calls.Load(); calls != 1 {
		t.Fatalf("export ran %d times, want 1", calls)
	}

	assertCompleteBackup(t, setup.target, id)
}

var _ blob.Store = (*memStore)(nil)
