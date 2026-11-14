package backup_test

import (
	"bytes"
	"context"
	"encoding/json"
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

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/secret"
)

// orderLog records the order of maintenance enter and pre-backup export
// calls, so the order test proves the pre-backup runs after Enter.
type orderLog struct {
	mu     sync.Mutex
	events []string
}

func (l *orderLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.events = append(l.events, event)
}

func (l *orderLog) index(event string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	for i, got := range l.events {
		if got == event {
			return i
		}
	}

	return -1
}

func (l *orderLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string{}, l.events...)
}

// restoreServiceSetup wires a RestoreService over fakes: a target holding one
// backup, a live store that passes the checks, a backup service for the
// pre-backup, an in-memory restore registry, and a maintenance stub.
type restoreServiceSetup struct {
	service *backup.RestoreService
	target  *memStore
	live    *restoreServiceLive
	blobs   *memStore
	backups *backup.Service
	regs    *restoreServiceRegistry
	maint   *restoreServiceMaintenance
	order   *orderLog
	id      backup.ID
	// exporter is the exporter of the backup before the restore.
	exporter *stubBackupExporter
	// backupOps holds the operations of the backup service.
	backupOps durable.Store
}

// restoreServiceLive is a fake backup.RestoreLive: the check store with an
// applier and a fencer. ApplyStaging emulates the swap by dropping the
// restore rows, like the adapters replace the live data with staged data
// that predates the row. Hooks let tests block stages and count applies.
// Resurrect, when non-nil, is stored as a checking row after the drop, like
// a staged snapshot that predates a still-running restore.
type restoreServiceLive struct {
	checks     *checkLiveStore
	registry   *restoreServiceRegistry
	mu         sync.Mutex
	stageBlock chan struct{}
	// applyGate, when non-nil, holds the second StageRestore call open: the
	// first call serves the checks, the second stages the apply. Tests close
	// it to let the apply proceed, so the restore parks after it left
	// checking. stageBlock still gates every call when set.
	applyGate  chan struct{}
	stageCalls int
	applied    int
	kept       int
	bumped     int
	keepingErr error
	// applyErr is the error of ApplyStaging, to test a refusal by the swap.
	applyErr error
	dev      devLiveState
	// staged holds the input of each StageRestore call.
	staged []backup.RestoreStageInput
	// keeps holds the installation that each DropStagedIssuerKeys call was
	// given, and dropReport is what the fake answers to it.
	keeps      []string
	dropReport []backup.IssuerKeysDropped
	// ops is the store of durable operations of the restores, maint the
	// installation state that the fenced writes change, entered the
	// maintenance flag of each restore, and areas the staging areas.
	ops     durable.Store
	maint   *restoreServiceMaintenance
	entered map[backup.RestoreID]bool
	areas   []backup.StagingArea
	// events holds the stored audit events of each restore.
	events map[backup.RestoreID]string
	// minErr is the answer of CheckBinaryMinimum.
	minErr error
	// afterStage runs once StageRestore has staged, and onApplying runs at
	// the start of MarkRestoreApplying (the pre-backup step). A test uses
	// them to change the backup target at a chosen point of the restore.
	afterStage func()
	onApplying func()
	// stageBlobs are added to the blobs of the staged summary: rows of the
	// staged data that name a blob.
	stageBlobs []backup.BlobRef
}

// devLiveState holds what the fake live store does for the dev data ports.
// A test sets the kind, the reports of the strip and the errors; the fake
// records what the service asks for.
type devLiveState struct {
	kind    installation.Kind
	kindErr error
	// detect and remove are the reports of the two strip modes. stripErr
	// fails the mode.
	detect   backup.DevStripReport
	remove   backup.DevStripReport
	stripErr map[backup.DevStripMode]error
	// dropped is the count that the keeping swap reports.
	dropped int64
	// order, when set, logs "strip-detect", "strip-remove", "record" and
	// "apply" in the order the service calls them.
	order *orderLog
	// appendErr fails the audit appends after the swap.
	appendErr error

	stageOpts []backup.StageOptions
	strips    []backup.DevStripMode
	records   []backup.DevStripReport
	appends   []audit.Entry
	discards  int
}

func (l *restoreServiceLive) ExportPortable(
	ctx context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	return l.checks.ExportPortable(ctx, sink)
}

func (l *restoreServiceLive) PeekPortableKey(
	ctx context.Context, open backup.PortableOpener, file backup.PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	return l.checks.PeekPortableKey(ctx, open, file, wrapperID)
}

func (l *restoreServiceLive) StagePortable(
	ctx context.Context,
	open backup.PortableOpener,
	want backup.Summary,
	files []backup.PortableFile,
	opts backup.StageOptions,
) (backup.Summary, error) {
	l.mu.Lock()
	block := l.stageBlock
	l.stageCalls++
	call := l.stageCalls
	gate := l.applyGate
	l.mu.Unlock()

	if block != nil {
		select {
		case <-ctx.Done():
			return backup.Summary{}, fmt.Errorf("wait for the stage gate: %w", ctx.Err())
		case <-block:
		}
	}

	if gate != nil && call >= 2 {
		select {
		case <-ctx.Done():
			return backup.Summary{}, fmt.Errorf("wait for the apply gate: %w", ctx.Err())
		case <-gate:
		}
	}

	l.mu.Lock()
	l.dev.stageOpts = append(l.dev.stageOpts, opts)
	l.mu.Unlock()

	return l.checks.StagePortable(ctx, open, want, files, opts)
}

func (l *restoreServiceLive) DiscardStaging(ctx context.Context) error {
	l.mu.Lock()
	l.dev.discards++
	l.mu.Unlock()

	return l.checks.DiscardStaging(ctx)
}

func (l *restoreServiceLive) ApplyStaging(_ context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.applyErr != nil {
		return l.applyErr
	}

	l.applied++
	l.bumped++

	if l.dev.order != nil {
		l.dev.order.add("apply")
	}

	return nil
}

// ApplyStagingKeepingAccess implements backup.AccessKeepingApplier for the
// service tests: it counts keeping applies separately. When keepingErr is
// set it reports it instead, so tests prove a mismatch fails the restore.
func (l *restoreServiceLive) ApplyStagingKeepingAccess(ctx context.Context) (backup.KeepAccessResult, error) {
	l.mu.Lock()
	l.kept++
	keepingErr := l.keepingErr
	l.mu.Unlock()

	if keepingErr != nil {
		return backup.KeepAccessResult{}, keepingErr
	}

	l.mu.Lock()
	dropped := l.dev.dropped
	l.mu.Unlock()

	return backup.KeepAccessResult{DroppedTempAdmins: dropped}, l.ApplyStaging(ctx)
}

func (l *restoreServiceLive) BumpFailoverVersions(_ context.Context) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.bumped++

	return 1, nil
}

// CheckBinaryMinimum implements installation.MinimumChecker with the answer
// of the test.
func (l *restoreServiceLive) CheckBinaryMinimum(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.minErr
}

// ReadKind implements backup.KindReader with the kind of the test.
func (l *restoreServiceLive) ReadKind(_ context.Context) (installation.Kind, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.dev.kindErr != nil {
		return "", l.dev.kindErr
	}

	return l.dev.kind, nil
}

// StripDevStaged implements backup.DevStripper with the reports of the test.
func (l *restoreServiceLive) StripDevStaged(
	_ context.Context, mode backup.DevStripMode,
) (backup.DevStripReport, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.dev.strips = append(l.dev.strips, mode)

	if l.dev.order != nil {
		l.dev.order.add(map[backup.DevStripMode]string{
			backup.DevStripDetect: "strip-detect", backup.DevStripRemove: "strip-remove",
		}[mode])
	}

	if err := l.dev.stripErr[mode]; err != nil {
		return backup.DevStripReport{}, err
	}

	if mode == backup.DevStripDetect {
		return l.dev.detect, nil
	}

	return l.dev.remove, nil
}

// RecordDevReport implements backup.DevReportRecorder: it keeps each report
// and stores it in the row of the registry, like the adapters do.
func (l *restoreServiceLive) RecordDevReport(
	ctx context.Context, id backup.RestoreID, report backup.DevStripReport,
) error {
	l.mu.Lock()
	l.dev.records = append(l.dev.records, report)

	if l.dev.order != nil {
		l.dev.order.add("record")
	}
	l.mu.Unlock()

	return l.registry.setDevReport(ctx, id, report)
}

// AppendServer implements the server audit port that the service writes to
// after the swap.
func (l *restoreServiceLive) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.dev.appendErr != nil {
		return audit.Entry{}, l.dev.appendErr
	}

	l.dev.appends = append(l.dev.appends, e)

	return e, nil
}

// devSnapshot returns a copy of the dev state, under the lock.
func (l *restoreServiceLive) devSnapshot() devLiveState {
	l.mu.Lock()
	defer l.mu.Unlock()

	snap := l.dev
	snap.stageOpts = slices.Clone(l.dev.stageOpts)
	snap.strips = slices.Clone(l.dev.strips)
	snap.records = slices.Clone(l.dev.records)
	snap.appends = slices.Clone(l.dev.appends)

	return snap
}

// liveCounts counts the applies and fences of the fake live store.
type liveCounts struct {
	applied int
	kept    int
	bumped  int
}

func (l *restoreServiceLive) counts() liveCounts {
	l.mu.Lock()
	defer l.mu.Unlock()

	return liveCounts{applied: l.applied, kept: l.kept, bumped: l.bumped}
}

// setStageBlock sets the stage gate under the lock: StageRestore reads it on
// the restore goroutine.
func (l *restoreServiceLive) setStageBlock(block chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.stageBlock = block
}

// stageCallCount answers how many StageRestore calls started, under the lock.
func (l *restoreServiceLive) stageCallCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.stageCalls
}

// restoreServiceRegistry is an in-memory backup.RestoreRegistry. finishBlock,
// when non-nil, holds FinishRestore open, so tests prove the single-restore
// slot stays held until the row reaches its end state.
type restoreServiceRegistry struct {
	mu          sync.Mutex
	rows        map[backup.RestoreID]backup.RestoreRecord
	finishBlock chan struct{}
	// worker runs the restore operations while a test waits, and runErr is
	// the error of its last run.
	worker *backup.RestoreService
	runErr error
}

func newRestoreServiceRegistry() *restoreServiceRegistry {
	return &restoreServiceRegistry{
		mu:          sync.Mutex{},
		rows:        map[backup.RestoreID]backup.RestoreRecord{},
		finishBlock: nil,
		worker:      nil,
		runErr:      nil,
	}
}

func (r *restoreServiceRegistry) CreateRestore(_ context.Context, row backup.RestoreRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.rows[row.ID]; ok {
		return backup.ErrRestoreExists
	}

	for key := range r.rows {
		if other := r.rows[key]; other.State == backup.RestoreChecking || other.State == backup.RestoreApplying {
			return backup.ErrRestoreRunning
		}
	}

	r.rows[row.ID] = row

	return nil
}

func (r *restoreServiceRegistry) GetRestore(_ context.Context, id backup.RestoreID) (backup.RestoreRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[id]
	if !ok {
		return backup.RestoreRecord{}, backup.ErrRestoreNotFound
	}

	return row, nil
}

func (r *restoreServiceRegistry) ListRestores(_ context.Context) ([]backup.RestoreRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]backup.RestoreRecord, 0, len(r.rows))

	for _, id := range sortedRestoreIDs(r.rows) {
		out = append(out, r.rows[id])
	}

	return out, nil
}

func (r *restoreServiceRegistry) FinishRestore(
	_ context.Context, id backup.RestoreID, state backup.RestoreState, errText string, endedAt time.Time,
) (backup.RestoreRecord, error) {
	r.mu.Lock()

	block := r.finishBlock
	row, ok := r.rows[id]

	if !ok {
		r.mu.Unlock()

		return backup.RestoreRecord{}, backup.ErrRestoreNotFound
	}

	if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
		r.mu.Unlock()

		return backup.RestoreRecord{}, backup.ErrRestoreNotRunning
	}

	if state != backup.RestoreSucceeded && state != backup.RestoreFailed {
		r.mu.Unlock()

		return backup.RestoreRecord{}, backup.ErrRestoreNotRunning
	}

	r.mu.Unlock()

	if block != nil {
		<-block
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok = r.rows[id]
	if !ok {
		return backup.RestoreRecord{}, backup.ErrRestoreNotFound
	}

	row.State = state
	row.Error = errText
	row.EndedAt = endedAt
	r.rows[id] = row

	return row, nil
}

func (r *restoreServiceRegistry) MarkRestoreApplying(
	_ context.Context, id backup.RestoreID, pre backup.ID,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[id]
	if !ok {
		return backup.ErrRestoreNotFound
	}

	if row.State != backup.RestoreChecking {
		return backup.ErrRestoreNotRunning
	}

	row.State = backup.RestoreApplying
	row.PreBackupID = pre
	r.rows[id] = row

	return nil
}

// setWorker sets the service whose restore operations waitRestoreEnd runs:
// no worker runs in these tests.
func (r *restoreServiceRegistry) setWorker(service *backup.RestoreService) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.worker = service
}

// runWorker runs one restore operation of the worker, when set.
func (r *restoreServiceRegistry) runWorker(ctx context.Context) {
	r.mu.Lock()
	worker := r.worker
	r.mu.Unlock()

	if worker == nil {
		return
	}

	// The wait reads the row, not the run: a run that found no work or
	// failed an attempt leaves its record in the row or in the operation.
	err := worker.RunOnce(ctx)

	r.mu.Lock()
	r.runErr = err
	r.mu.Unlock()
}

// dropAll removes every restore row, emulating the swap that replaces the
// live data with staged data predating the rows.
// setDevReport stores the report in the row, like RecordDevReport of an
// adapter. An unknown ID reports backup.ErrRestoreNotFound.
func (r *restoreServiceRegistry) setDevReport(
	_ context.Context,
	id backup.RestoreID,
	report backup.DevStripReport,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	row, ok := r.rows[id]
	if !ok {
		return backup.ErrRestoreNotFound
	}

	row.DevReport = &report
	r.rows[id] = row

	return nil
}

func sortedRestoreIDs(rows map[backup.RestoreID]backup.RestoreRecord) []backup.RestoreID {
	ids := make([]backup.RestoreID, 0, len(rows))

	for id := range rows {
		ids = append(ids, id)
	}

	sortRestoreIDs(ids)

	return ids
}

func sortRestoreIDs(ids []backup.RestoreID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// restoreServiceMaintenance is a fake backup.RestoreMaintenance with an
// injected enter error and hold. It records each enter in order, when set.
// cancelScript, when set, is the active work of a forced enter: EnterWith
// runs its cancel sequence through the writer before it writes the state.
type restoreServiceMaintenance struct {
	mu           sync.Mutex
	enterErr     error
	enters       int
	leaves       int
	order        *orderLog
	state        maintenance.State
	held         bool
	cancelScript *cancelScript
}

// setEnterErr sets the injected enter error under the lock: Enter reads it on
// the restore goroutine.
func (m *restoreServiceMaintenance) setEnterErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.enterErr = err
}

// setCancelScript sets the active work of the next forced enter under the
// lock: EnterWith reads it on the restore goroutine.
func (m *restoreServiceMaintenance) setCancelScript(script *cancelScript) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cancelScript = script
}

// stubBackupExporter is a fake backup.Exporter for the pre-backup service.
// It records each export in order, when set.
type stubBackupExporter struct {
	sum   backup.Summary
	data  []byte
	order *orderLog
	// hook, when set, runs at the start of each export.
	hook atomic.Pointer[func()]
}

func (e *stubBackupExporter) ExportPortable(
	_ context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	if e.order != nil {
		e.order.add("export")
	}

	if hook := e.hook.Load(); hook != nil {
		(*hook)()
	}

	// Every table gets a file, as the real export does: a backup without a
	// file for each table is corrupt, and the finish step checks the files.
	return writeTestTableFile(sink, e.data, e.sum)
}

// newRestoreServiceSetup builds the fakes and the RestoreService under test.
// The target holds backup bkp-restore with two blobs; the live store passes
// every check for it.
func newRestoreServiceSetup(t *testing.T) *restoreServiceSetup {
	t.Helper()

	return newRestoreServiceSetupWith(t, func(*backup.RestoreConfig) {})
}

// newRestoreServiceSetupWith is newRestoreServiceSetup with a change to the
// config of the restore service, for example the setting that allows a dev
// backup.
func newRestoreServiceSetupWith(t *testing.T, change func(*backup.RestoreConfig)) *restoreServiceSetup {
	t.Helper()

	setup := newCheckSetup(t)
	regs := newRestoreServiceRegistry()
	order := &orderLog{mu: sync.Mutex{}, events: nil}
	maint := &restoreServiceMaintenance{
		mu: sync.Mutex{}, enterErr: nil, enters: 0, leaves: 0, order: order, state: maintenance.StateNormal,
		held: false, cancelScript: nil,
	}
	blobs := newMemStore()
	live := newRestoreServiceLive(setup.live, regs)
	live.dev.order = order
	live.maint = maint

	backupRegs := newFakeRegistry()
	exporter := &stubBackupExporter{
		sum: testSummary(), data: []byte("pre-backup-sql"), order: order, hook: atomic.Pointer[func()]{},
	}
	backups, err := backup.NewService(backup.Config{
		Exporter:    exporter,
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    backupRegs,
		Operations:  backupRegs.ops,
		Blobs:       newMemStore(),
		StateOpener: nil,
		Target:      setup.target,
		TargetName:  "mem:target",
		Now:         nil,
		Version:     "test-1.0",
		MaxSQLBytes: 0,
		Owner:       "",
		Lease:       0,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	cfg := backup.RestoreConfig{
		Live:            live,
		Operations:      live.ops,
		Backups:         backups,
		Maintenance:     maint,
		Blobs:           blobs,
		Target:          setup.target,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
		Now:             nil,
		Logger:          nil,
	}
	change(&cfg)

	service, err := backup.NewRestoreService(cfg)
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	regs.setWorker(service)

	return &restoreServiceSetup{
		service:   service,
		target:    setup.target,
		live:      live,
		blobs:     blobs,
		backups:   backups,
		regs:      regs,
		maint:     maint,
		order:     order,
		id:        setup.id,
		exporter:  exporter,
		backupOps: backupRegs.ops,
	}
}

// waitRestoreEnd polls id until it leaves checking and applying, failing on
// timeout, and returns the final row.
func waitRestoreEnd(t *testing.T, regs *restoreServiceRegistry, id backup.RestoreID) backup.RestoreRecord {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		row, err := regs.GetRestore(t.Context(), id)

		switch {
		case errors.Is(err, backup.ErrRestoreNotFound):
			// The swap in ApplyStaging drops every row until the service
			// stores its row again. RestoreService.Get serves the held row
			// in that window, but this poll reads the registry, so it waits
			// for the row to come back (issue 379).
			row.State = backup.RestoreChecking
		case err != nil:
			t.Fatalf("get restore %s: %v", id, err)
		default:
		}

		if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
			return row
		}

		regs.runWorker(t.Context())

		select {
		case <-t.Context().Done():
			t.Fatalf("restore %s: %v", id, t.Context().Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	t.Fatalf("restore %s never finished", id)

	return backup.RestoreRecord{
		StartedAt:   time.Time{},
		EndedAt:     time.Time{},
		ID:          "",
		BackupID:    "",
		PreBackupID: "",
		RequestedBy: "",
		Error:       "",
		State:       "",
		DevReport:   nil,
	}
}

// TestRestoreErrorTextHoldsNoDriverDetail proves a restore failure lands in
// the row as stable text: no driver detail, no capability path, no token,
// and no scope string reach GET. The injected maintenance error carries all
// of them; only the refusal may surface.
func TestRestoreErrorTextHoldsNoDriverDetail(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	setup.maint.setEnterErr(fmt.Errorf(
		`enter maintenance: export SQL data: driver boom at backups/bkp-restore/manifest.json`+
			` with token tok-secret in scope acme/stk-test01: %w`,
		maintenance.ErrActiveWork,
	))

	id := backup.MintRestoreID()

	startErr := setup.service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if startErr != nil {
		t.Fatalf("start restore: %v", startErr)
	}

	row := waitRestoreEnd(t, setup.regs, id)

	if row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want failed", row.State)
	}

	for _, leaked := range []string{"driver boom", "backups/", "tok-secret", "stk-test01"} {
		if strings.Contains(row.Error, leaked) {
			t.Fatalf("restore error %q holds %q", row.Error, leaked)
		}
	}

	if !strings.Contains(row.Error, "active") {
		t.Fatalf("restore error %q names no active work", row.Error)
	}
}

// TestRestoreSecondStartWhileRunningConflicts proves the single-restore slot
// is shared through the database: a second Start on the same service and a
// Start on another service over the same registry both answer
// ErrRestoreRunning while one runs.
func TestRestoreSecondStartWhileRunningConflicts(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	stageBlock := make(chan struct{})
	setup.live.setStageBlock(stageBlock)

	first := backup.MintRestoreID()

	startErr := setup.service.Start(t.Context(), first, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if startErr != nil {
		t.Fatalf("start first restore: %v", startErr)
	}

	second := backup.MintRestoreID()

	err := setup.service.Start(t.Context(), second, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if !errors.Is(err, backup.ErrRestoreRunning) {
		t.Fatalf("second start: err = %v, want ErrRestoreRunning", err)
	}

	other, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            setup.live,
		Operations:      setup.live.ops,
		Backups:         setup.backups,
		Maintenance:     setup.maint,
		Blobs:           setup.blobs,
		Target:          setup.target,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}

	setup.regs.setWorker(other)

	err = other.Start(t.Context(), second, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if !errors.Is(err, backup.ErrRestoreRunning) {
		t.Fatalf("start on another service: err = %v, want ErrRestoreRunning", err)
	}

	close(stageBlock)
	setup.live.setStageBlock(nil)

	row := waitRestoreEnd(t, setup.regs, first)

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("first restore state = %q, want succeeded (%q)", row.State, row.Error)
	}
}

// mutatingTarget serves the objects of an inner blob store, but answers the
// manifest with tampered bytes from the manifest read at from on, emulating a
// backup target mutated after the pin.
type mutatingTarget struct {
	inner     *memStore
	tampered  []byte
	mu        sync.Mutex
	manifests int
	from      int
}

func (m *mutatingTarget) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	return m.inner.Put(ctx, key, r)
}

func (m *mutatingTarget) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if strings.HasSuffix(key.String(), "/manifest.json") {
		m.mu.Lock()
		m.manifests++

		if m.manifests >= m.from {
			data := m.tampered
			m.mu.Unlock()

			return io.NopCloser(strings.NewReader(string(data))), nil
		}

		m.mu.Unlock()
	}

	return m.inner.Get(ctx, key)
}

func (m *mutatingTarget) Delete(ctx context.Context, key blob.Key) error {
	return m.inner.Delete(ctx, key)
}

func (m *mutatingTarget) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	return m.inner.ListPage(ctx, req)
}

func (m *mutatingTarget) Ping(ctx context.Context) error {
	return m.inner.Ping(ctx)
}

// TestRestoreMutatedTargetCannotApply proves a backup target mutated after
// the checks cannot be applied: the service pins the manifest before the
// checks and refuses to stage when it changed, leaving the live data
// untouched.
func TestRestoreMutatedTargetCannotApply(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	raw := setup.target.readObject(t, "backups/"+setup.id.String()+"/manifest.json")

	var manifest backup.Manifest

	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	manifest.SQLSize++

	tampered, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode tampered manifest: %v", err)
	}

	proxy := &mutatingTarget{inner: setup.target, tampered: tampered, mu: sync.Mutex{}, manifests: 0, from: 3}

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            setup.live,
		Operations:      setup.live.ops,
		Backups:         setup.backups,
		Maintenance:     setup.maint,
		Blobs:           setup.blobs,
		Target:          proxy,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	setup.regs.setWorker(service)

	id := backup.MintRestoreID()

	mutatedErr := service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if mutatedErr != nil {
		t.Fatalf("start restore: %v", mutatedErr)
	}

	row := waitRestoreEnd(t, setup.regs, id)

	if row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want failed", row.State)
	}

	if row.Error != backup.ErrCorrupt.Error() {
		t.Fatalf("restore error = %q, want %q", row.Error, backup.ErrCorrupt)
	}

	if got := setup.live.counts(); got.applied != 0 || got.bumped != 0 {
		t.Fatalf("applied = %d, bumped = %d, want no apply after a target mutation", got.applied, got.bumped)
	}
}

// TestRestoreBacksUpAfterEnteringMaintenance proves the pre-backup runs after
// Enter: a forced restore backs up the live data with the active work already
// canceled, and a refused enter takes no backup at all. An order that backs
// up before entering fails here.
func TestRestoreBacksUpAfterEnteringMaintenance(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	id := backup.MintRestoreID()

	startErr := setup.service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            true,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if startErr != nil {
		t.Fatalf("start restore: %v", startErr)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state = %q, want succeeded (%q)", row.State, row.Error)
	}

	enterAt := setup.order.index("enter")
	exportAt := setup.order.index("export")

	if enterAt < 0 || exportAt < 0 || enterAt > exportAt {
		t.Fatalf("order = %v, want enter before export", setup.order.snapshot())
	}

	refused := newRestoreServiceSetup(t)
	refused.maint.setEnterErr(maintenance.ErrActiveWork)

	denied := backup.MintRestoreID()

	denyErr := refused.service.Start(t.Context(), denied, backup.RestoreRequest{
		BackupID:         refused.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if denyErr != nil {
		t.Fatalf("start refused restore: %v", denyErr)
	}

	if row := waitRestoreEnd(t, refused.regs, denied); row.State != backup.RestoreFailed {
		t.Fatalf("refused restore state = %q, want failed", row.State)
	}

	if exportAt := refused.order.index("export"); exportAt >= 0 {
		t.Fatalf("order = %v, want no pre-backup after a refused enter", refused.order.snapshot())
	}
}

// TestRestoreBackToBackStartsAfterPollSucceeds proves a client that polls GET
// to succeeded and starts the next restore at once never meets
// ErrRestoreRunning: the slot frees before the row reaches its end state.
// Each round polls tightly, so a finish that records the end state before
// freeing the slot fails the next start.
func TestRestoreBackToBackStartsAfterPollSucceeds(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	const rounds = 10

	for range rounds {
		id := backup.MintRestoreID()

		startErr := setup.service.Start(t.Context(), id, backup.RestoreRequest{
			BackupID:         setup.id,
			PreBackupID:      "",
			CheckOnly:        false,
			Force:            false,
			RequestedBy:      "tester",
			KeepAccessTables: false,
		})
		if startErr != nil {
			t.Fatalf("start restore %s: %v", id, startErr)
		}

		pollRestoreSucceeded(t, setup.regs, id)
	}
}

// pollRestoreSucceeded polls id tightly until it leaves checking and
// applying, failing on timeout, and returns the final row. It tolerates the
// missing row between the swap dropping the old rows and the restore
// recording its own row again.
func pollRestoreSucceeded(
	t *testing.T, regs *restoreServiceRegistry, id backup.RestoreID,
) backup.RestoreRecord {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		row, err := regs.GetRestore(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore %s: %v", id, err)
		}

		if row.State == backup.RestoreChecking || row.State == backup.RestoreApplying {
			regs.runWorker(t.Context())

			continue
		}

		if row.State != backup.RestoreSucceeded {
			t.Fatalf("restore %s state = %q, want succeeded (%q)", id, row.State, row.Error)
		}

		return row
	}

	t.Fatalf("restore %s never finished", id)

	return backup.RestoreRecord{
		StartedAt:   time.Time{},
		EndedAt:     time.Time{},
		ID:          "",
		BackupID:    "",
		PreBackupID: "",
		RequestedBy: "",
		Error:       "",
		State:       "",
		DevReport:   nil,
	}
}

// errFakeDriver is a fake PostgreSQL driver error: the driver text the log
// must drop while the check context stays.
var errFakeDriver = errors.New(`ERROR: relation "namespaces" does not exist (SQLSTATE 42P01)`)

// TestRestoreFailureLogsCauseWithoutDriverDetail proves a faulted restore
// lands its cause in the server log: the check context stays, while driver
// text, capability paths and tokens do not.
// syncBuffer is a goroutine-safe sink for log output that a background
// restore writes while the test polls: the restore logs after it records its
// end state, so reading a plain buffer right after the row finishes races
// with the log write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // Transparent writer: callers must see the original error.
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestRestoreFailureLogsCauseWithoutDriverDetail(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	buf := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}

	logged, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            setup.live,
		Operations:      setup.live.ops,
		Backups:         setup.backups,
		Maintenance:     setup.maint,
		Blobs:           setup.blobs,
		Target:          setup.target,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		Now:             nil,
		Logger:          slog.New(slog.NewTextHandler(buf, nil)),
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	setup.regs.setWorker(logged)

	setup.maint.setEnterErr(fmt.Errorf(
		`enter maintenance: list namespaces at "backups/bkp-restore00000001/db" with token tok-secret: %w`,
		errFakeDriver,
	))

	id := backup.MintRestoreID()

	logErr := logged.Start(t.Context(), id, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if logErr != nil {
		t.Fatalf("start restore: %v", logErr)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want failed", row.State)
	}

	// The restore logs after it records the end state the poll above waited
	// for, so wait until the log line lands instead of reading at once.
	line := waitLogLine(t, buf)

	for _, want := range []string{"enter maintenance", "list namespaces"} {
		if !strings.Contains(line, want) {
			t.Fatalf("restore log lacks %q:\n%s", want, line)
		}
	}

	for _, leaked := range []string{"SQLSTATE", "42P01", "backups/", "tok-secret"} {
		if strings.Contains(line, leaked) {
			t.Fatalf("restore log holds %q:\n%s", leaked, line)
		}
	}
}

// waitLogLine polls buf until it holds the restore failure detail the
// background work logs after recording the end state, failing on timeout.
// logRestore writes two lines: first the restore line, whose error attribute
// is the stored text "restore failed", then the error line with the message
// "restore failed" and the sanitized cause. It waits for the second line:
// the first one already holds the words "restore failed" (issue 379).
func waitLogLine(t *testing.T, buf *syncBuffer) string {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if line := buf.String(); strings.Contains(line, `msg="restore failed"`) {
			return line
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("restore log: %v", t.Context().Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	t.Fatal("restore log never held the failure detail")

	return ""
}

// TestRestorePreservesPreBackupAcrossSwap proves the backup taken before the
// apply survives the swap: the control tables stay across the swap with the
// live rows, so the pre-backup row survives and the preserve after the swap
// does nothing. The undo source stays listed and readable.
func TestRestorePreservesPreBackupAcrossSwap(t *testing.T) {
	t.Parallel()

	check := newCheckSetup(t)
	regs := newRestoreServiceRegistry()
	backupRegs := newFakeRegistry()
	maint := &restoreServiceMaintenance{
		mu: sync.Mutex{}, enterErr: nil, enters: 0, leaves: 0, order: nil, state: maintenance.StateNormal, held: false,
		cancelScript: nil,
	}
	blobs := newMemStore()
	live := newRestoreServiceLive(check.live, regs)
	live.maint = maint

	backups, err := backup.NewService(backup.Config{
		Exporter: &stubBackupExporter{
			sum: testSummary(), data: []byte("pre-backup-sql"), order: nil, hook: atomic.Pointer[func()]{},
		},
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    backupRegs,
		Operations:  backupRegs.ops,
		Blobs:       newMemStore(),
		StateOpener: nil,
		Target:      check.target,
		TargetName:  "mem:target",
		Now:         nil,
		Version:     "test-1.0",
		MaxSQLBytes: 0,
		Owner:       "",
		Lease:       0,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            live,
		Operations:      live.ops,
		Backups:         backups,
		Maintenance:     maint,
		Blobs:           blobs,
		Target:          check.target,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	regs.setWorker(service)

	id := backup.MintRestoreID()
	req := backup.RestoreRequest{
		BackupID:         check.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	}

	if startErr := service.Start(t.Context(), id, req); startErr != nil {
		t.Fatalf("start restore: %v", startErr)
	}

	row := waitRestoreEnd(t, regs, id)

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state = %q, want succeeded (%q)", row.State, row.Error)
	}

	if row.PreBackupID == "" {
		t.Fatal("restore pre-backup ID is empty, want the backup taken before the apply")
	}

	pre, err := backups.Get(t.Context(), row.PreBackupID)
	if err != nil {
		t.Fatalf("get pre-backup %s: %v", row.PreBackupID, err)
	}

	if pre.State != backup.StateSucceeded {
		t.Fatalf("pre-backup state = %q, want succeeded", pre.State)
	}

	listed, err := backups.List(t.Context())
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}

	found := false

	for _, record := range listed {
		if record.ID == row.PreBackupID {
			found = true

			break
		}
	}

	if !found {
		t.Fatalf("pre-backup %s missing from the backups list %+v", row.PreBackupID, listed)
	}
}

// restoreServiceWithHook rebuilds the restore service of setup with an
// after-apply step, sharing its fakes. The setup service stays idle.
func restoreServiceWithHook(
	t *testing.T, setup *restoreServiceSetup, hook func(ctx context.Context) error,
) *backup.RestoreService {
	t.Helper()

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            setup.live,
		Operations:      setup.live.ops,
		Backups:         setup.backups,
		Maintenance:     setup.maint,
		Blobs:           setup.blobs,
		Target:          setup.target,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      hook,
		Admit:           nil,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service with a hook: %v", err)
	}

	setup.regs.setWorker(service)

	return service
}

// restoreServiceAdmitting rebuilds the restore service of setup with an
// admit hook, sharing its fakes.
func restoreServiceAdmitting(
	t *testing.T, setup *restoreServiceSetup, admit func(ctx context.Context) error,
) *backup.RestoreService {
	t.Helper()

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            setup.live,
		Operations:      setup.live.ops,
		Backups:         setup.backups,
		Maintenance:     setup.maint,
		Blobs:           setup.blobs,
		Target:          setup.target,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           admit,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service with an admit hook: %v", err)
	}

	setup.regs.setWorker(service)

	return service
}

// TestRestoreAdmitRefusesAStart proves that a refusal of the admit hook
// refuses the start before the restore stores a row or enters maintenance
// mode, and that the next start runs once the hook admits it.
func TestRestoreAdmitRefusesAStart(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	var (
		mu     sync.Mutex
		refuse = errFakeDriver
	)

	service := restoreServiceAdmitting(t, setup, func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()

		return refuse
	})

	req := backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	}

	refused := backup.MintRestoreID()
	if err := service.Start(t.Context(), refused, req); !errors.Is(err, errFakeDriver) {
		t.Fatalf("start while refused: %v, want the refusal", err)
	}

	if _, err := setup.regs.GetRestore(t.Context(), refused); !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("row of a refused start: %v, want ErrRestoreNotFound", err)
	}

	if running, err := service.Running(t.Context()); err != nil || running {
		t.Fatalf("running after a refused start = %v, %v; want false", running, err)
	}

	mu.Lock()
	refuse = nil
	mu.Unlock()

	admitted := backup.MintRestoreID()
	if err := service.Start(t.Context(), admitted, req); err != nil {
		t.Fatalf("start once admitted: %v", err)
	}

	if row := pollRestoreSucceeded(t, setup.regs, admitted); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state = %q, want succeeded", row.State)
	}
}

// TestRestoreRunsAfterApplyAfterTheSwap proves the after-apply step runs
// after a restore applies: the server runs its audit chain steps through
// it.
func TestRestoreRunsAfterApplyAfterTheSwap(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	ran := make(chan struct{}, 1)

	service := restoreServiceWithHook(t, setup, func(_ context.Context) error {
		ran <- struct{}{}

		return nil
	})

	id := backup.MintRestoreID()

	req := backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	}

	if err := service.Start(t.Context(), id, req); err != nil {
		t.Fatalf("start restore %s: %v", id, err)
	}

	row := pollRestoreSucceeded(t, setup.regs, id)

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state = %q, want succeeded", row.State)
	}

	select {
	case <-ran:
	default:
		t.Fatal("after-apply step never ran")
	}
}

// TestRestoreFailsWhenAfterApplyFails proves a failing after-apply step
// fails the restore: a failed audit chain step never passes silently.
func TestRestoreFailsWhenAfterApplyFails(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	service := restoreServiceWithHook(t, setup, func(_ context.Context) error {
		return errFakeDriver
	})

	id := backup.MintRestoreID()

	req := backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	}

	if err := service.Start(t.Context(), id, req); err != nil {
		t.Fatalf("start restore %s: %v", id, err)
	}

	row := waitRestoreEnd(t, setup.regs, id)

	if row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want failed", row.State)
	}
}

// TestRestoreRefusesForeignDescriptorPath proves a manifest whose descriptor
// names an object outside the table files, here the manifest itself, fails the
// restore with ErrCorrupt before any object is opened or applied.
func TestRestoreRefusesForeignDescriptorPath(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	raw := setup.target.readObject(t, "backups/"+setup.id.String()+"/manifest.json")

	var manifest backup.Manifest

	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	renameSecretKeysPath(t, &manifest, "manifest.json")

	tampered, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode tampered manifest: %v", err)
	}

	proxy := &foreignManifestTarget{
		&mutatingTarget{inner: setup.target, tampered: tampered, mu: sync.Mutex{}, manifests: 0, from: 3},
	}

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            setup.live,
		Operations:      setup.live.ops,
		Backups:         setup.backups,
		Maintenance:     setup.maint,
		Blobs:           setup.blobs,
		Target:          proxy,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	setup.regs.setWorker(service)

	id := backup.MintRestoreID()

	startErr := service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if startErr != nil {
		t.Fatalf("start restore: %v", startErr)
	}

	row := waitRestoreEnd(t, setup.regs, id)

	if row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want failed", row.State)
	}

	if row.Error != backup.ErrCorrupt.Error() {
		t.Fatalf("restore error = %q, want %q", row.Error, backup.ErrCorrupt)
	}

	if got := setup.live.counts(); got.applied != 0 || got.bumped != 0 {
		t.Fatalf("applied = %d, bumped = %d, want no apply for a foreign descriptor path", got.applied, got.bumped)
	}
}

// foreignManifestTarget serves the tampered manifest on every read, so the
// restore reads the foreign descriptor path first.
type foreignManifestTarget struct {
	*mutatingTarget
}

func (f *foreignManifestTarget) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if strings.HasSuffix(key.String(), "/manifest.json") {
		return io.NopCloser(strings.NewReader(string(f.tampered))), nil
	}

	return f.inner.Get(ctx, key)
}

// renameSecretKeysPath sets the path of the secret_keys descriptor of manifest.
func renameSecretKeysPath(t *testing.T, manifest *backup.Manifest, path string) {
	t.Helper()

	for i := range manifest.SQLFiles {
		if manifest.SQLFiles[i].Table == "secret_keys" {
			manifest.SQLFiles[i].Path = path

			return
		}
	}

	t.Fatal("manifest holds no descriptor of secret_keys")
}

// TestRestoreRefusesOtherFormat proves a backup whose manifest has another
// format number fails the restore as corrupt, before any object is applied.
func TestRestoreRefusesOtherFormat(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	other, err := json.Marshal(map[string]any{"id": setup.id.String(), "format": 1})
	if err != nil {
		t.Fatalf("encode manifest of another format: %v", err)
	}

	proxy := &foreignManifestTarget{
		&mutatingTarget{inner: setup.target, tampered: other, mu: sync.Mutex{}, manifests: 0, from: 3},
	}

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            setup.live,
		Operations:      setup.live.ops,
		Backups:         setup.backups,
		Maintenance:     setup.maint,
		Blobs:           setup.blobs,
		Target:          proxy,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	setup.regs.setWorker(service)

	id := backup.MintRestoreID()

	startErr := service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	})
	if startErr != nil {
		t.Fatalf("start restore: %v", startErr)
	}

	row := waitRestoreEnd(t, setup.regs, id)

	if row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want failed", row.State)
	}

	if row.Error != backup.ErrCorrupt.Error() {
		t.Fatalf("restore error = %q, want %q", row.Error, backup.ErrCorrupt)
	}

	if got := setup.live.counts(); got.applied != 0 || got.bumped != 0 {
		t.Fatalf("applied = %d, bumped = %d, want no apply for a backup of another format", got.applied, got.bumped)
	}
}

// TestRestoreKeepAccessTablesCallsKeepingApply proves the restore service
// routes the apply by the request: a request that keeps the access tables
// calls ApplyStagingKeepingAccess, and one that replaces them calls
// ApplyStaging.
func TestRestoreKeepAccessTablesCallsKeepingApply(t *testing.T) {
	t.Parallel()

	kept := newRestoreServiceSetup(t)

	keepID := backup.MintRestoreID()
	keepReq := backup.RestoreRequest{
		BackupID:         kept.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: true,
	}
	if err := kept.service.Start(t.Context(), keepID, keepReq); err != nil {
		t.Fatalf("start restore that keeps the access tables: %v", err)
	}

	if row := waitRestoreEnd(t, kept.regs, keepID); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore that keeps the access tables state = %q, want succeeded (%q)",
			row.State, row.Error)
	}

	if got := kept.live.counts(); got.kept != 1 || got.applied != 1 {
		t.Fatalf("kept = %d, applied = %d, want 1 keeping apply", got.kept, got.applied)
	}

	replaced := newRestoreServiceSetup(t)

	replaceID := backup.MintRestoreID()
	replaceReq := backup.RestoreRequest{
		BackupID:         replaced.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	}
	if err := replaced.service.Start(t.Context(), replaceID, replaceReq); err != nil {
		t.Fatalf("start restore that replaces the access tables: %v", err)
	}

	if row := waitRestoreEnd(t, replaced.regs, replaceID); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore that replaces the access tables state = %q, want succeeded (%q)",
			row.State, row.Error)
	}

	if got := replaced.live.counts(); got.kept != 0 || got.applied != 1 {
		t.Fatalf("kept = %d, applied = %d, want 1 replacing apply", got.kept, got.applied)
	}
}

// TestRestoreKeepAccessMismatchFailsTheRestore proves a keeping apply that
// reports ErrRestoreAccessMismatch fails the restore with the mismatch text,
// which names the site administrator who can restore the access tables.
func TestRestoreKeepAccessMismatchFailsTheRestore(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	setup.live.keepingErr = backup.ErrRestoreAccessMismatch

	id := backup.MintRestoreID()
	keepReq := backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: true,
	}
	if err := setup.service.Start(t.Context(), id, keepReq); err != nil {
		t.Fatalf("start restore that keeps the access tables: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)

	if row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want failed", row.State)
	}

	if row.Error != backup.ErrRestoreAccessMismatch.Error() {
		t.Fatalf("restore error = %q, want %q", row.Error, backup.ErrRestoreAccessMismatch.Error())
	}
}

// TestRestoreKeepAccessChecksTheKey proves that a restore that keeps the
// access tables still runs the key-encryption key check on the backup: a
// key that does not open the backup, and a backup whose installation keys
// the store cannot read, fail the restore before any apply.
func TestRestoreKeepAccessChecksTheKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		edit func(live *checkLiveStore)
		name string
		id   backup.RestoreID
	}{
		{
			name: "a key that does not open the backup",
			id:   "rst-keepaccessaaaaaaaaaa",
			edit: func(live *checkLiveStore) { live.key.Ciphertext = []byte("wrapped-by-another-key") },
		},
		{
			name: "installation keys that the store cannot read",
			id:   "rst-keepaccessbbbbbbbbbb",
			edit: func(live *checkLiveStore) {
				live.keyErr = fmt.Errorf("secret_keys of the backup holds no data key: %w", backup.ErrNoDataKey)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := newRestoreServiceSetup(t)
			tc.edit(setup.live.checks)

			keepReq := backup.RestoreRequest{
				BackupID:         setup.id,
				PreBackupID:      "",
				CheckOnly:        false,
				Force:            false,
				RequestedBy:      "tester",
				KeepAccessTables: true,
			}
			if err := setup.service.Start(t.Context(), tc.id, keepReq); err != nil {
				t.Fatalf("start restore that keeps the access tables: %v", err)
			}

			if row := waitRestoreEnd(t, setup.regs, tc.id); row.State != backup.RestoreFailed {
				t.Fatalf("restore state = %q, want failed", row.State)
			}

			if got := setup.live.counts(); got.kept != 0 || got.applied != 0 {
				t.Fatalf("kept = %d, applied = %d, want no apply", got.kept, got.applied)
			}
		})
	}
}

// fixedKind is a backup.KindReader that returns one kind.
type fixedKind installation.Kind

// ReadKind implements backup.KindReader.
func (k fixedKind) ReadKind(_ context.Context) (installation.Kind, error) {
	return installation.Kind(k), nil
}

// emptyReport returns a strip report that names no removal.
func emptyReport() backup.DevStripReport {
	return backup.DevStripReport{
		Principals: nil, OtherPrincipals: 0, OtherCounts: nil, Classes: nil, Approvers: nil, Warnings: nil,
		PreBackupFound: nil, NotHeld: nil, ErrorCode: "",
	}
}

// newRestoreServiceLive returns the fake live store over the check store and
// the registry. It is a production installation with no dev data in the staged
// data.
func newRestoreServiceLive(checks *checkLiveStore, regs *restoreServiceRegistry) *restoreServiceLive {
	return &restoreServiceLive{
		checks:     checks,
		registry:   regs,
		mu:         sync.Mutex{},
		stageBlock: nil,
		applyGate:  nil,
		stageCalls: 0,
		staged:     nil,
		keeps:      nil,
		dropReport: nil,
		applied:    0,
		kept:       0,
		bumped:     0,
		keepingErr: nil,
		applyErr:   nil,
		dev: devLiveState{
			kind: installation.KindProduction, kindErr: nil, detect: emptyReport(), remove: emptyReport(),
			stripErr: map[backup.DevStripMode]error{}, dropped: 0, order: nil, appendErr: nil,
			stageOpts: nil, strips: nil, records: nil, appends: nil, discards: 0,
		},
		ops:        durabletest.NewMem().Store(),
		maint:      nil,
		entered:    map[backup.RestoreID]bool{},
		areas:      nil,
		events:     map[backup.RestoreID]string{},
		minErr:     nil,
		afterStage: nil,
		onApplying: nil,
		stageBlobs: nil,
	}
}

// maintenanceCounts counts the enters and leaves of the fake maintenance.
type maintenanceCounts struct {
	enters int
	leaves int
}

func (m *restoreServiceMaintenance) counts() maintenanceCounts {
	m.mu.Lock()
	defer m.mu.Unlock()

	return maintenanceCounts{enters: m.enters, leaves: m.leaves}
}
