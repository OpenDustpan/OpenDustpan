package backup

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
)

// RestoreLive is the live store ports that a restore needs: the export that
// reads the live summary, the key peek of the key check, the installation
// kind of the dev data rules, the restores table and the staging areas of a
// restore that runs as a durable operation. Every SQL store adapter serves
// them from one database.
type RestoreLive interface {
	PortableExporter
	KeyPeeker
	KindReader
	Fencer
	RestoreOperations
	RestoreStaging
}

// RestoreMaintenance enters and leaves maintenance mode around the swap
// (ADR 38 decision 4), with each state write through a writer that the
// restore binds to its claim. The server wires the domain service.
type RestoreMaintenance interface {
	// Get returns the installation state.
	Get(ctx context.Context) (maintenance.State, error)
	// EnterWith moves the installation to maintenance mode through the
	// writer of opts. While an update or a run is active it reports
	// maintenance.ErrActiveWork naming each one, unless opts.Force cancels
	// them first.
	EnterWith(ctx context.Context, opts maintenance.EnterOptions) (maintenance.EnterResult, error)
	// RecheckWith lists the active work again for a restore that entered
	// maintenance mode and lost its claim before its confirm.
	RecheckWith(ctx context.Context, opts maintenance.EnterOptions) (maintenance.EnterResult, error)
	// LeaveWith moves the installation back to normal operation through w.
	// It reports maintenance.ErrHeld while the hold of the server keeps
	// maintenance mode.
	LeaveWith(ctx context.Context, w maintenance.StateWriter) error
	// Held reports whether the hold of the server keeps maintenance mode.
	Held(ctx context.Context) (bool, error)
}

// RestoreRequest asks for one restore: the backup to apply, whether to check
// only, whether to cancel active work, who asked, and whether to keep the
// current access tables. KeepAccessTables is true when the caller does not
// hold server:restore-access: the restore replaces the other data and keeps
// the current rows of the access tables. PreBackupID names the backup of the
// live data that the restore takes before the swap; empty mints one.
type RestoreRequest struct {
	BackupID         ID
	PreBackupID      ID
	CheckOnly        bool
	Force            bool
	RequestedBy      string
	KeepAccessTables bool
}

// RestoreConfig holds the dependencies of a RestoreService.
type RestoreConfig struct {
	// Live is the live SQL store: the checks read it, the restores table
	// and the staging areas live in it.
	Live RestoreLive
	// Operations keeps the durable operation of each restore. It must also
	// implement durable.StepCanceler. Live starts the operation in the
	// transaction of the restore row, so both must name one database.
	Operations durable.Store
	// Backups takes the backup of the live data before the swap.
	Backups *Service
	// Maintenance enters and leaves maintenance mode around the swap.
	Maintenance RestoreMaintenance
	// Blobs is the live content store the restore copies into.
	Blobs blob.Store
	// Target is the backup target that holds blobs/<key>,
	// backups/<id>/sql/<table>.jsonl.gz and backups/<id>/manifest.json.
	Target blob.Store
	// Key is the key-encryption key the checks prove against the backup.
	Key secret.Wrapper
	// Installation is the installation name of this server, for the warning
	// that the backup holds no namespace with it as primary installation. A
	// blank name disables the warning.
	Installation string
	// AfterApply runs after the swap, while the server still holds
	// maintenance mode, so a restore of data that predates a later change
	// converges to it: for example, the server compares the restored audit
	// log with the stored chain state. It must be safe to run again after a
	// takeover. A failure fails the restore. Nil skips it.
	AfterApply func(ctx context.Context) error
	// Admit runs when a restore starts, before it records the restore. An
	// error refuses the restore: the server refuses a restore while a
	// change of the audit chain waits for an approval. Nil admits every
	// restore.
	Admit func(ctx context.Context) error
	// AllowDevBackups lets a production installation restore a backup of a
	// dev installation. The restore then removes the dev data from the staged
	// data and warns. The default is false. Only code sets it: the server
	// reads it from a command-line flag and no setting layer sets it.
	AllowDevBackups bool
	// AllowDevHint holds the words that the refusal text uses to name how to
	// allow a dev backup, for example the flag in code quotes. Empty leaves
	// the sentence out.
	AllowDevHint string
	// Owner names this instance in the claims on restore operations, for
	// status only. An empty Owner means "restore".
	Owner string
	// Lease is the time from a claim or a renewal to its expiry. Zero means
	// the default of the durable runner.
	Lease time.Duration
	// CleanupLockWait bounds the wait of each drop of the cleanup step for
	// its locks. Zero means DefaultCleanupLockWait.
	CleanupLockWait time.Duration
	// RetryBase is the delay before the first retry of a failed step. Each
	// next retry doubles it, up to durable.DefaultBackoffMax. Zero means
	// durable.DefaultBackoffBase.
	RetryBase time.Duration
	Now       func() time.Time
	Logger    *slog.Logger
}

// DefaultCleanupLockWait bounds the wait of each drop of the cleanup step for
// its locks. A drop that waits longer fails the attempt, and the step tries
// again with backoff.
const DefaultCleanupLockWait = 5 * time.Second

// RestoreService runs online restores of a whole installation (ADR 38) as
// durable operations of RestoreOperationKind. Start records the restore and
// its operation; a worker of any instance runs the steps that the doc
// comment of RestoreOperationKind names.
type RestoreService struct {
	live         RestoreLive
	operations   durable.Store
	canceler     durable.StepCanceler
	backups      *Service
	maintenance  RestoreMaintenance
	blobs        blob.Store
	target       blob.Store
	key          secret.Wrapper
	installation string
	afterApply   func(ctx context.Context) error
	admit        func(ctx context.Context) error
	allowDev     bool
	devHint      string
	lease        time.Duration
	lockWait     time.Duration
	now          func() time.Time
	logger       *slog.Logger
	runner       *durable.Runner
}

// NewRestoreService returns a RestoreService. Live, Operations, Backups,
// Maintenance, Blobs and Target are required, and Operations must implement
// durable.StepCanceler. A nil Now means time.Now; a nil Logger discards the
// restore log lines.
func NewRestoreService(cfg RestoreConfig) (*RestoreService, error) {
	switch {
	case cfg.Live == nil:
		return nil, fmt.Errorf("%w: live store", ErrMissingDependency)
	case cfg.Operations == nil:
		return nil, fmt.Errorf("%w: durable operations", ErrMissingDependency)
	case cfg.Backups == nil:
		return nil, fmt.Errorf("%w: backup service", ErrMissingDependency)
	case cfg.Maintenance == nil:
		return nil, fmt.Errorf("%w: maintenance", ErrMissingDependency)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: blob store", ErrMissingDependency)
	case cfg.Target == nil:
		return nil, fmt.Errorf("%w: target", ErrMissingDependency)
	}

	canceler, ok := cfg.Operations.(durable.StepCanceler)
	if !ok {
		return nil, fmt.Errorf("%w: the durable operations cannot cancel before a step", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	s := &RestoreService{
		live:         cfg.Live,
		operations:   cfg.Operations,
		canceler:     canceler,
		backups:      cfg.Backups,
		maintenance:  cfg.Maintenance,
		blobs:        cfg.Blobs,
		target:       cfg.Target,
		key:          cfg.Key,
		installation: cfg.Installation,
		afterApply:   cfg.AfterApply,
		admit:        cfg.Admit,
		allowDev:     cfg.AllowDevBackups,
		devHint:      cfg.AllowDevHint,
		lease:        cmp.Or(cfg.Lease, durable.DefaultLease),
		lockWait:     cmp.Or(cfg.CleanupLockWait, DefaultCleanupLockWait),
		now:          now,
		logger:       cmp.Or(cfg.Logger, slog.New(slog.DiscardHandler)),
		runner:       nil,
	}

	runner, err := s.newRunner(cfg)
	if err != nil {
		return nil, err
	}

	s.runner = runner

	return s, nil
}

// Start records restore id and starts its durable operation, in one
// transaction, and returns at once, so an HTTP handler answers 202 before
// the restore ends. It does not run the restore: a worker of any instance
// claims the operation. A second Start while one restore runs answers
// ErrRestoreRunning, and a GET sees the row at once. A canceled request never
// stops the restore. A dev backup into a production installation is refused
// here, before the row exists.
func (s *RestoreService) Start(ctx context.Context, id RestoreID, req RestoreRequest) error {
	warnings, err := s.admitStart(ctx, id, req)
	if err != nil {
		return err
	}

	if refused := s.checkPreBackupFree(ctx, req.PreBackupID); refused != nil {
		return refused
	}

	input, err := startInput(id, req)
	if err != nil {
		return err
	}

	start, err := s.runner.StartRequest(RestoreOperationKind, RestoreOperationKey(id), input)
	if err != nil {
		return fmt.Errorf("begin restore %s: %w", id, err)
	}

	record := RestoreRecord{
		StartedAt: s.now().UTC(), EndedAt: time.Time{}, ID: id, BackupID: req.BackupID, PreBackupID: "",
		RequestedBy: req.RequestedBy, Error: "", State: RestoreChecking, DevReport: nil,
	}

	if len(warnings) > 0 {
		report := emptyDevReport()
		report.Warnings = warnings
		record.DevReport = &report
	}

	if err := s.live.StartRestore(ctx, record, start); err != nil {
		return fmt.Errorf("create restore %s: %w", id, err)
	}

	return nil
}

// Get returns the restore row id, or ErrRestoreNotFound. Any instance
// answers it: the row lives in the control tables, which the swap keeps.
func (s *RestoreService) Get(ctx context.Context, id RestoreID) (RestoreRecord, error) {
	record, err := s.live.GetRestore(ctx, id)
	if err != nil {
		return RestoreRecord{}, fmt.Errorf("get restore %s: %w", id, err)
	}

	return record, nil
}

// ReadManifest returns the manifest of backup id from the target. A backup
// without a readable manifest reports ErrCorrupt: only a complete backup
// counts.
func (s *RestoreService) ReadManifest(ctx context.Context, id ID) (Manifest, error) {
	manifest, err := s.backups.ReadManifest(ctx, id)
	if err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

// List returns every restore row, newest first.
func (s *RestoreService) List(ctx context.Context) ([]RestoreRecord, error) {
	records, err := s.live.ListRestores(ctx)
	if err != nil {
		return nil, fmt.Errorf("list restores: %w", err)
	}

	return records, nil
}

// Running reports whether a restore row is checking or applying, on any
// instance.
func (s *RestoreService) Running(ctx context.Context) (bool, error) {
	records, err := s.live.ListRestores(ctx)
	if err != nil {
		return false, fmt.Errorf("list restores: %w", err)
	}

	for i := range records {
		if records[i].State == RestoreChecking || records[i].State == RestoreApplying {
			return true, nil
		}
	}

	return false, nil
}

// Cancel stops restore id before its swap. It returns nil when the cancel is
// accepted, also when a cancel already stopped the restore. It returns
// ErrRestoreNotFound for an unknown restore, ErrRestoreSwapped when the
// restore already swapped the data, and ErrRestoreNotRunning when the
// restore ended. The check of the swap and the cancel are one transaction,
// and the swap transaction checks the cancel, so a restore never ends
// canceled after its swap. The row ends failed when the operation ends: the
// failed-row sweep of the workers marks it, and Cancel runs that sweep once
// at once.
func (s *RestoreService) Cancel(ctx context.Context, id RestoreID) error {
	record, err := s.live.GetRestore(ctx, id)
	if err != nil {
		return fmt.Errorf("cancel restore %s: %w", id, err)
	}

	op, err := s.operationOf(ctx, id)
	if err != nil {
		return fmt.Errorf("cancel restore %s: %w", id, err)
	}

	if record.State != RestoreChecking && record.State != RestoreApplying {
		// A cancel that a cancel already stopped is accepted again.
		if record.State == RestoreFailed && op.State == durable.StateCanceled {
			return nil
		}

		return fmt.Errorf("cancel restore %s: %w", id, ErrRestoreNotRunning)
	}

	err = s.canceler.CancelBefore(ctx, op.ID, RestoreStepSwap)

	switch {
	case errors.Is(err, durable.ErrStepDone):
		return fmt.Errorf("cancel restore %s: %w", id, ErrRestoreSwapped)
	case errors.Is(err, durable.ErrFinished):
		// An operation that a cancel already ended accepts the cancel again,
		// also before the row is marked failed.
		if ended, getErr := s.operations.Get(ctx, op.ID); getErr == nil && ended.State == durable.StateCanceled {
			s.failEndedOnce(ctx)

			return nil
		}

		return fmt.Errorf("cancel restore %s: %w", id, ErrRestoreNotRunning)
	case err != nil:
		return fmt.Errorf("cancel restore %s: %w", id, err)
	}

	s.failEndedOnce(ctx)

	return nil
}

// Work claims and runs restore operations until ctx ends, and marks failed
// the restore rows whose operation ended without a finish write. A worker
// process calls it. When ctx ends during a step, the runner releases the
// claim, so another instance, or this one after a restart, continues the
// restore. Work returns after the loops stopped.
func (s *RestoreService) Work(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Go(func() { s.runner.Run(ctx) })
	wg.Go(func() { s.failEnded(ctx) })

	wg.Wait()
}

// RunOnce claims one restore operation and runs it until it ends, until the
// runner loses its claim, or until ctx ends, and then marks failed the rows
// of ended operations once. It returns the error of durable.Runner.RunOnce.
// A command that runs one restore with no worker calls it.
func (s *RestoreService) RunOnce(ctx context.Context) error {
	err := s.runner.RunOnce(ctx)

	s.failEndedOnce(ctx)

	if err != nil {
		return fmt.Errorf("run a restore operation: %w", err)
	}

	return nil
}

// newRunner builds the runner that claims and runs the restore operations.
// It stops a step at the first renewal that fails, because the steps change
// things outside the fence: blob copies and, with force, the cancel of
// active work.
func (s *RestoreService) newRunner(cfg RestoreConfig) (*durable.Runner, error) {
	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store: cfg.Operations,
		Owner: cmp.Or(cfg.Owner, "restore"),
		Kinds: []durable.KindSpec{{
			Kind:       RestoreOperationKind,
			Version:    RestoreOperationVersion,
			MinVersion: RestoreOperationVersion,
			Steps:      s.steps(),
		}},
		Lease:       cfg.Lease,
		RenewEvery:  0,
		PollEvery:   0,
		MaxAttempts: 0,
		ExpiryLimit: 0,
		Backoff: durable.Backoff{
			Base: cmp.Or(cfg.RetryBase, durable.DefaultBackoffBase),
			Max:  durable.DefaultBackoffMax,
		},
		ClaimBackoff:       durable.Backoff{Base: 0, Max: 0},
		WriteTimeout:       0,
		Deferred:           nil,
		StopOnRenewalError: true,
		Logger:             cfg.Logger,
		Rand:               nil,
	})
	if err != nil {
		return nil, fmt.Errorf("restore runner: %w", err)
	}

	return runner, nil
}

// refuseWhileRunning returns ErrRestoreRunning when a restore row is
// checking or applying.
func (s *RestoreService) refuseWhileRunning(ctx context.Context) error {
	rows, err := s.live.ListRestores(ctx)
	if err != nil {
		return fmt.Errorf("list restores: %w", err)
	}

	for i := range rows {
		if rows[i].State == RestoreChecking || rows[i].State == RestoreApplying {
			return ErrRestoreRunning
		}
	}

	return nil
}

// operationOf returns the durable operation of restore id.
func (s *RestoreService) operationOf(ctx context.Context, id RestoreID) (durable.Operation, error) {
	ops, err := s.operations.List(ctx, durable.Filter{Kind: RestoreOperationKind, States: nil, Limit: 0})
	if err != nil {
		return durable.Operation{}, fmt.Errorf("list restore operations: %w", err)
	}

	for i := range ops {
		if ops[i].Key == RestoreOperationKey(id) {
			return ops[i], nil
		}
	}

	return durable.Operation{}, ErrRestoreNotFound
}

// admitStart checks the IDs of a start, runs the admit hook, refuses the
// start while a restore runs, and compares the kinds of the backup and the
// installation. It returns the warnings of the kinds.
func (s *RestoreService) admitStart(
	ctx context.Context,
	id RestoreID,
	req RestoreRequest,
) ([]RestoreWarningCode, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("begin restore %s: %w", id, err)
	}

	if _, err := ParseRestoreID(id.String()); err != nil {
		return nil, fmt.Errorf("begin restore: %w", err)
	}

	if _, err := ParseID(req.BackupID.String()); err != nil {
		return nil, fmt.Errorf("begin restore %s: %w", id, err)
	}

	if s.admit != nil {
		if err := s.admit(ctx); err != nil {
			return nil, fmt.Errorf("begin restore %s: %w", id, err)
		}
	}

	// A restore that runs refuses the start before it reads the target. The
	// insert of StartRestore checks it again in its transaction: two starts
	// at the same time pass this read, and only one insert commits.
	if err := s.refuseWhileRunning(ctx); err != nil {
		return nil, fmt.Errorf("begin restore %s: %w", id, err)
	}

	warnings, err := s.startDevCheck(ctx, req.BackupID)
	if err != nil {
		return nil, fmt.Errorf("begin restore %s: %w", id, err)
	}

	return warnings, nil
}

// startInput returns the input of the operation of restore id, with a new
// pre-backup ID when req names none.
func startInput(id RestoreID, req RestoreRequest) ([]byte, error) {
	in := restoreInput{
		ID: id, BackupID: req.BackupID, PreBackupID: req.PreBackupID, CheckOnly: req.CheckOnly, Force: req.Force,
		RequestedBy: req.RequestedBy, KeepAccessTables: req.KeepAccessTables,
	}

	if in.PreBackupID == "" {
		in.PreBackupID = ID(ident.NewID("bkp-"))
	}

	if _, err := ParseID(in.PreBackupID.String()); err != nil {
		return nil, fmt.Errorf("begin restore %s: %w", id, err)
	}

	input, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("encode input of restore %s: %w", id, err)
	}

	return input, nil
}

// failEnded marks failed the rows of restores whose operation ended failed
// or canceled, once in each lease period, until ctx ends.
func (s *RestoreService) failEnded(ctx context.Context) {
	ticker := time.NewTicker(s.lease)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.failEndedOnce(ctx)
		}
	}
}

// failEndedOnce marks failed the rows of restores whose operation ended
// failed or canceled. It leaves the maintenance mode that such a restore
// entered, unless the hold of the server keeps it, and writes the audit
// entries of each row that it marked. A failure here is not final: the next
// period tries again, and the row stays running until then.
func (s *RestoreService) failEndedOnce(ctx context.Context) {
	held, err := s.maintenance.Held(ctx)
	if err != nil {
		// A hold that cannot be read keeps maintenance mode: fail closed.
		held = true
	}

	ops, err := s.endedOperations(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "restore operations were not read", slog.String("error", durable.ErrorClass(err)))
		}

		return
	}

	// The fence comes before the rows fail: when it fails, the next round
	// still sees the ended restore and fences again.
	if fenceErr := s.fenceEnded(ctx, ops); fenceErr != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "namespaces were not fenced after a restore that stopped",
				slog.String("error", durable.ErrorClass(fenceErr)))
		}

		return
	}

	rows, err := s.live.FailEndedRestores(ctx, s.now().UTC(), !held)
	if err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "restore rows of ended operations were not marked failed",
			slog.String("error", durable.ErrorClass(err)))
	}

	for i := range rows {
		s.logger.WarnContext(ctx, "restore ended with no end write and was marked failed",
			slog.String("restore_id", rows[i].ID.String()), slog.String("error", rows[i].Error))

		if op, ok := ops[RestoreOperationKey(rows[i].ID)]; ok {
			s.auditEnded(ctx, op)
		}
	}
}

// endedOperations returns the operations of the checking or applying restore
// rows that ended, by their start key: the restores that stopped with no end
// write.
func (s *RestoreService) endedOperations(ctx context.Context) (map[string]durable.Operation, error) {
	rows, err := s.live.ListRestores(ctx)
	if err != nil {
		return nil, fmt.Errorf("list restores: %w", err)
	}

	running := make(map[string]bool)

	for i := range rows {
		if rows[i].State == RestoreChecking || rows[i].State == RestoreApplying {
			running[RestoreOperationKey(rows[i].ID)] = true
		}
	}

	ended := make(map[string]durable.Operation)

	if len(running) == 0 {
		return ended, nil
	}

	ops, err := s.operations.List(ctx, durable.Filter{Kind: RestoreOperationKind, States: nil, Limit: 0})
	if err != nil {
		return nil, fmt.Errorf("list restore operations: %w", err)
	}

	for i := range ops {
		if running[ops[i].Key] && ops[i].State.Terminal() {
			ended[ops[i].Key] = ops[i]
		}
	}

	return ended, nil
}

// fenceEnded increments the failover version of every namespace once when an
// ended restore of ops is not check-only and touchedLive reports it: such a
// restore can have canceled work, and its swap can have committed. A restore
// that stopped in stage or check changed no live data, so its stop leaves the
// leases, locks and update tokens of the installation alone. Each fence
// writes a log line that names the restores.
func (s *RestoreService) fenceEnded(ctx context.Context, ops map[string]durable.Operation) error {
	var stopped []string

	for key := range ops {
		op := ops[key]

		in, err := readRestoreInput(op)
		if err != nil {
			// An input that does not decode is a restore of unknown scope:
			// fail closed and fence.
			stopped = append(stopped, op.Key)

			continue
		}

		if in.CheckOnly {
			continue
		}

		touched, err := s.touchedLive(ctx, op, in.ID)
		if err != nil {
			return err
		}

		if touched {
			stopped = append(stopped, in.ID.String())
		}
	}

	if len(stopped) == 0 {
		return nil
	}

	slices.Sort(stopped)

	n, err := s.live.BumpFailoverVersions(ctx)
	if err != nil {
		return fmt.Errorf("fence namespaces after a stopped restore: %w", err)
	}

	s.logger.WarnContext(ctx, "fenced every namespace after a restore that stopped in maintenance mode",
		slog.String("restores", strings.Join(stopped, ", ")), slog.Int64("namespaces", n))

	return nil
}

// touchedLive reports whether the restore id of op can have changed the
// installation: it recorded enter-maintenance, or its row holds the
// maintenance flag of an enter that it did not confirm, or it stored audit
// events, which it stores before each forced cancel and each refusal to overwrite a live blob.
func (s *RestoreService) touchedLive(ctx context.Context, op durable.Operation, id RestoreID) (bool, error) {
	if _, entered := op.Step(RestoreStepEnterMaintenance); entered {
		return true, nil
	}

	flag, err := s.live.RestoreMaintenanceEntered(ctx, id)
	if err != nil {
		return false, fmt.Errorf("read the maintenance flag of restore %s: %w", id, err)
	}

	if flag {
		return true, nil
	}

	events, err := s.live.RestoreEvents(ctx, id)
	if err != nil {
		return false, fmt.Errorf("read the audit events of restore %s: %w", id, err)
	}

	return len(events) > 0, nil
}

// auditEnded writes the audit entries of the restore of op, which a sweep
// marked failed. After the swap, when the after-apply step did not write
// them, it writes the entries of that step, because the swap replaced the
// audit log. Before the swap it writes the audit events of the restore.
func (s *RestoreService) auditEnded(ctx context.Context, op durable.Operation) {
	in, err := readRestoreInput(op)
	if err != nil {
		return
	}

	_, swapped := op.Step(RestoreStepSwap)
	_, applied := op.Step(RestoreStepAfterApply)

	if !swapped {
		end := restoreEndStopped
		if op.State == durable.StateCanceled {
			end = restoreEndCanceled
		}

		s.appendEvents(ctx, in, end)

		return
	}

	if applied {
		return
	}

	store, ok := s.live.(serverAuditAppender)
	if !ok {
		return
	}

	entries, err := s.afterApplyEntries(ctx, op, in, swappedFailed)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"audit entries of a restore were not built",
			slog.String("restore_id", in.ID.String()),
		)

		return
	}

	for i := range entries {
		if _, err := store.AppendServer(ctx, entries[i]); err != nil {
			s.logger.ErrorContext(ctx, "audit append failed", slog.String("restore_id", in.ID.String()))
		}
	}
}

// storedRestoreSentinels lists the sentinels whose own text a restore row
// stores, in the order that sanitizeRestoreError tests them.
//
//nolint:gochecknoglobals // A frozen list of sentinels, like the table lists of this package.
var storedRestoreSentinels = []error{
	ErrPreBackupForeign,
	ErrManifestKindInvalid,
	ErrDevStripIncomplete,
	ErrRestoreRunning,
	ErrRestoreAccessMismatch,
	principal.ErrNoWorkingBreakGlass,
	ErrProtectedKey,
	ErrConflictingBlob,
	ErrCorrupt,
	ErrSchemaBeforeBaseline,
	ErrSchemaMismatch,
	ErrNotFound,
	ErrRestoreNotFound,
}

// restoreOutcome carries one finished restore to the log line.
type restoreOutcome struct {
	id      RestoreID
	req     RestoreRequest
	state   RestoreState
	errText string
	full    error
}

// logObjectPath matches a quoted backup-target object path, for example
// "backups/bkp-1/manifest.json" or "blobs/namespaces/ns-1/pulumi/stk-1/upd-1/0": storage layout, not
// data. Blob content keys without the target prefix stay, so the log still
// names the blob that failed.
var logObjectPath = regexp.MustCompile(`"[^"]*(?:backups|blobs)/[^"]*"`)

// logToken matches a token value after the word token. Our own messages
// never print one, but a wrapped foreign error may carry it.
var logToken = regexp.MustCompile(`(?i)\btoken\s+("[^"]*"|\S+)`)

// logFrame is one error of the flattened cause chain: its own text, whether
// it wraps nothing further, and the value for the sentinel check.
type logFrame struct {
	value error
	text  string
	leaf  bool
}

// pinnedManifest pins the manifest fields the second stage must still match,
// so a target mutated after the check cannot be applied.
type pinnedManifest struct {
	sqlSize  int64
	sqlSHA   string
	schema   int
	dialect  Dialect
	kind     installation.Kind
	wrappers []string
	// digest is the SHA-256 of the whole manifest, in hex: the blob list, the
	// row counts and every other field. A manifest that a person swapped in
	// the target after the stage has another digest.
	digest string
}

// sanitizeRestoreError maps a restore failure to the stable text stored in
// the row and returned by GET and List. Stored text never holds driver detail,
// a capability path, a token, a scope string, or a key fingerprint: only the
// sentinel decides the message. A backup object that is missing adds its key
// in the backup, as the backup check shows it, so that the operator knows
// which object to look for. The server log keeps the full error.
func sanitizeRestoreError(err error) string {
	if text, ok := secretRestoreMessage(err); ok {
		return text
	}

	if refusal, ok := errors.AsType[*devRefusalError](err); ok {
		return refusal.Text()
	}

	if errors.Is(err, maintenance.ErrActiveWork) {
		return activeWorkMessage(err)
	}

	if errors.Is(err, ErrDevBackupRefused) {
		return refusalSentence(reasonDevSwap, "")
	}

	if text, ok := missingObjectText(err); ok {
		return text
	}

	if notBacked, ok := errors.AsType[*BlobsNotBackedError](err); ok && notBacked != nil {
		return notBacked.Error()
	}

	if text, ok := liveBlobsRestoreMessage(err); ok {
		return text
	}

	for _, sentinel := range storedRestoreSentinels {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}

	return "restore failed"
}

// liveBlobsRestoreMessage answers the stored text of a refusal at the live
// blobs, which names the counts and the keys, and of a damaged list in a
// backup that no restore started. That text ends with the corrupt sentinel,
// as backup check reports it, so a client gives the help of a corrupt
// backup. It reports whether err is one of them.
func liveBlobsRestoreMessage(err error) (string, bool) {
	if refused, ok := errors.AsType[*LiveBlobsError](err); ok && refused != nil {
		return refused.Error(), true
	}

	if errors.Is(err, ErrDamagedListForeign) {
		return ErrDamagedListForeign.Error() + ": " + ErrCorrupt.Error(), true
	}

	return "", false
}

// activeWorkMessage answers the stored text for a refused maintenance
// enter: the sentinel with the unit names it carries, without any wrapping
// prefix. Wrapping can add driver text (for example a store failure), but
// the unit names after the sentinel are safe to show and tell the operator
// what to finish or cancel first.
func activeWorkMessage(err error) string {
	const sentinel = "cannot enter maintenance mode while work is active"

	msg := err.Error()

	if i := strings.Index(msg, sentinel); i >= 0 {
		return msg[i:]
	}

	return maintenance.ErrActiveWork.Error()
}

// secretRestoreMessage maps key-encryption-key failures to stable text that
// names the key settings of the server but no key material. The server holds
// the key and runs the check, so the text says to start the server with the
// key of the backup. It reports whether err is such a failure.
func secretRestoreMessage(err error) (string, bool) {
	const start = "start the server with the key that made the backup " +
		"(DUSTPAN_KEY_ENCRYPTION_KEY or DUSTPAN_KEY_ENCRYPTION_KEY_FILE)"

	switch {
	case errors.Is(err, secret.ErrKeyNotConfigured):
		return "the server has no key-encryption key to open the backup: " + start, true
	case errors.Is(err, secret.ErrWrapperMismatch), errors.Is(err, secret.ErrDecrypt):
		return "the key of this server does not open the backup: " + start, true
	default:
		return "", false
	}
}

// logRestore writes one log line for a finished restore (issue 140): the
// restore ID, the backup ID, the backup taken before it, who asked, whether
// the access tables were replaced or kept, and the end state. It holds no
// secret and no driver text. The sanitized detail of the failure goes to a
// second error line, so the operator can tell which table, blob or check
// failed from the log alone.
func (s *RestoreService) logRestore(ctx context.Context, out restoreOutcome) {
	record, getErr := s.live.GetRestore(ctx, out.id)
	pre := out.req.PreBackupID.String()

	if getErr == nil && record.PreBackupID != "" {
		pre = record.PreBackupID.String()
	}

	accessMode := "replaced"
	if out.req.KeepAccessTables {
		accessMode = "kept"
	}

	attrs := []slog.Attr{
		slog.String("restore_id", out.id.String()),
		slog.String("backup_id", out.req.BackupID.String()),
		slog.String("requested_by", out.req.RequestedBy),
		slog.String("access_tables", accessMode),
		slog.String("state", string(out.state)),
	}

	if pre != "" {
		attrs = append(attrs, slog.String("pre_backup_id", pre))
	}

	if out.errText != "" {
		attrs = append(attrs, slog.String("error", out.errText))
	}

	s.logger.LogAttrs(ctx, slog.LevelInfo, "restore", attrs...)

	if out.full != nil {
		s.logger.ErrorContext(
			ctx,
			"restore failed",
			slog.String("restore_id", out.id.String()),
			slog.String("error", sanitizeLogError(out.full)),
		)
	}
}

// sanitizeLogError renders err for the server log: our own wrapping context
// with object paths and tokens redacted, down to the first foreign leaf,
// which never reaches the log. Known sentinels stay: they name the cause
// without driver detail. The row keeps the shorter stable text from
// sanitizeRestoreError; the log detail names which table, blob or check
// failed, so the operator can tell from the log alone.
func sanitizeLogError(err error) string {
	if err == nil {
		return ""
	}

	var parts []string

	for _, frame := range flattenLogFrames(err) {
		if frame.leaf && !isLogSentinel(frame.value) {
			continue
		}

		text := redactLogFrame(frame.text)
		if text == "" {
			continue
		}

		if len(parts) > 0 && parts[len(parts)-1] == text {
			continue
		}

		parts = append(parts, text)
	}

	return strings.Join(parts, ": ")
}

// flattenLogFrames flattens err into its wrapping messages, outermost first.
// Each frame holds only its own text: for `fmt.Errorf("outer: %w", inner)`
// the frame is "outer". Joined errors contribute each branch in order, since
// a join carries no text of its own.
func flattenLogFrames(err error) []logFrame {
	var out []logFrame

	var walk func(e error)
	walk = func(e error) {
		if e == nil {
			return
		}

		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range joined.Unwrap() {
				walk(inner)
			}

			return
		}

		if single, ok := e.(interface{ Unwrap() error }); ok {
			if inner := single.Unwrap(); inner != nil {
				out = append(out, logFrame{
					value: e,
					text:  ownFrameText(e.Error(), inner.Error()),
					leaf:  false,
				})

				walk(inner)

				return
			}
		}

		out = append(out, logFrame{value: e, text: e.Error(), leaf: true})
	}

	walk(err)

	return out
}

// ownFrameText returns the text full adds around inner: the wrapping context
// without the cause it wraps. Every wrap in this package ends with the cause,
// so a trailing cut keeps the context.
func ownFrameText(full, inner string) string {
	if inner == "" {
		return full
	}

	if rest, ok := strings.CutSuffix(full, inner); ok {
		return strings.TrimRight(rest, ": ")
	}

	return full
}

// redactLogFrame redacts capability paths and tokens from one frame of
// restore context. Unit references, table names, backup IDs and blob content
// keys stay: the operator needs them to tell what failed.
func redactLogFrame(text string) string {
	text = logObjectPath.ReplaceAllString(text, `"[backup object]"`)
	text = logToken.ReplaceAllString(text, "token [redacted]")

	return strings.TrimSpace(text)
}

// isLogSentinel reports whether err is a stable domain or context value that
// the server log may name. Anything else at the end of a cause chain may
// hold driver text, so the log drops it.
func isLogSentinel(err error) bool {
	switch {
	case errors.Is(err, ErrCorrupt),
		errors.Is(err, ErrSchemaMismatch),
		errors.Is(err, ErrSchemaBeforeBaseline),
		errors.Is(err, ErrNotFound),
		errors.Is(err, ErrAlreadyExists),
		errors.Is(err, ErrNotRunning),
		errors.Is(err, ErrAlreadyRunning),
		errors.Is(err, ErrTooLarge),
		errors.Is(err, ErrMissingDependency),
		errors.Is(err, ErrRestoreNotFound),
		errors.Is(err, ErrRestoreExists),
		errors.Is(err, ErrRestoreNotRunning),
		errors.Is(err, ErrRestoreRunning),
		errors.Is(err, ErrRestoreAccessMismatch),
		errors.Is(err, ErrDevBackupRefused),
		errors.Is(err, ErrManifestKindInvalid),
		errors.Is(err, ErrDevStripIncomplete),
		errors.Is(err, installation.ErrUnavailable),
		errors.Is(err, maintenance.ErrActiveWork),
		errors.Is(err, maintenance.ErrUnavailable),
		errors.Is(err, maintenance.ErrBusy),
		errors.Is(err, maintenance.ErrInvalidState),
		errors.Is(err, maintenance.ErrStateChanged),
		errors.Is(err, secret.ErrDecrypt),
		errors.Is(err, secret.ErrInvalidScope),
		errors.Is(err, secret.ErrKeyNotFound),
		errors.Is(err, secret.ErrTooLarge),
		errors.Is(err, secret.ErrMissingDependency),
		errors.Is(err, secret.ErrKeyNotConfigured),
		errors.Is(err, secret.ErrWrapperMismatch),
		errors.Is(err, secret.ErrInvalidKey),
		errors.Is(err, blob.ErrNotFound),
		errors.Is(err, blob.ErrInvalidKey),
		errors.Is(err, blob.ErrExists),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return true
	default:
		return false
	}
}

// devFlagText names in the audit entry of a restore that the server runs with
// the setting that lets a production installation restore a dev backup. It is
// empty when the setting is off, which is the normal case.
func (s *RestoreService) devFlagText() string {
	if s.allowDev {
		return ", dev backups into production allowed"
	}

	return ""
}

// manifestDigest returns the SHA-256 of the manifest in hex. It encodes the
// parsed manifest, so two reads of one manifest give one digest, and a change
// to any field, the blob list included, gives another.
func manifestDigest(manifest Manifest) (string, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode manifest of backup %s: %w", manifest.ID, err)
	}

	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:]), nil
}

// pinManifest reads the manifest of id before the checks.
func (s *RestoreService) pinManifest(ctx context.Context, id ID) (pinnedManifest, error) {
	manifest, err := readManifest(ctx, s.target, id)
	if err != nil {
		return pinnedManifest{}, err
	}

	sha, parseErr := parseSQLSHA(manifest, id)
	if parseErr != nil {
		return pinnedManifest{}, parseErr
	}

	digest, err := manifestDigest(manifest)
	if err != nil {
		return pinnedManifest{}, err
	}

	return pinnedManifest{
		sqlSize:  manifest.SQLSize,
		sqlSHA:   string(sha),
		schema:   manifest.SchemaVersion,
		dialect:  manifest.Dialect,
		kind:     manifest.InstallationKind,
		wrappers: append([]string{}, manifest.WrapperIDs...),
		digest:   digest,
	}, nil
}

// readPinnedManifest reads the manifest of id once and refuses it when it is
// not the manifest that the stage pinned: fail closed on a change of the
// target. A step uses the manifest that this call returns and reads no
// second copy, so no swap can fall between the check and the use.
func (s *RestoreService) readPinnedManifest(ctx context.Context, id ID, pinned pinnedManifest) (Manifest, error) {
	manifest, err := readManifest(ctx, s.target, id)
	if err != nil {
		return Manifest{}, err
	}

	if err := checkPinnedManifest(manifest, id, pinned); err != nil {
		return Manifest{}, err
	}

	if err := checkDamagedOrigin(ctx, s.backups, manifest); err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

// checkPinnedManifest refuses a manifest that differs from the pinned one in
// the SQL part, the schema, the dialect, the kind, or in any other field: the
// digest covers the whole manifest.
func checkPinnedManifest(manifest Manifest, id ID, pinned pinnedManifest) error {
	sha, parseErr := parseSQLSHA(manifest, id)
	if parseErr != nil {
		return parseErr
	}

	digest, err := manifestDigest(manifest)
	if err != nil {
		return err
	}

	if manifest.SQLSize != pinned.sqlSize || string(sha) != pinned.sqlSHA ||
		manifest.SchemaVersion != pinned.schema || manifest.Dialect != pinned.dialect ||
		manifest.InstallationKind != pinned.kind || digest != pinned.digest {
		return fmt.Errorf("%w: backup %s changed after the checks", ErrCorrupt, id)
	}

	return nil
}

// verifyLiveBlob checks that the live store already holds ref with the size
// and checksum of the manifest. It writes nothing.
func (s *RestoreService) verifyLiveBlob(ctx context.Context, ref ManifestBlob) error {
	key, err := blob.ParseKey(ref.Key)
	if err != nil {
		return fmt.Errorf("restore blob key %q: %w", ref.Key, err)
	}

	return s.verifyObject(ctx, key, ref, "live blob")
}

// verifyObject checks that the live store holds the object at key with the size
// and checksum that the manifest names for ref. what names the object in the
// error text. It writes nothing. A mismatch wraps ErrCorrupt. A read error does
// not.
func (s *RestoreService) verifyObject(ctx context.Context, key blob.Key, ref ManifestBlob, what string) error {
	check, err := newObjectCheck(ref.Key, blobWant(ref))
	if err != nil {
		return err
	}

	reader, err := s.blobs.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("read %s %q: %w", what, ref.Key, err)
	}

	_, copyErr := io.Copy(check, reader)
	closeErr := reader.Close()

	if copyErr != nil {
		return fmt.Errorf("read %s %q: %w", what, ref.Key, copyErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close %s %q: %w", what, ref.Key, closeErr)
	}

	if verifyErr := check.verify(ref.Key); verifyErr != nil {
		return fmt.Errorf("%s: %w", what, verifyErr)
	}

	return nil
}

// MintRestoreID mints one restore ID: rst- for the restore row. The service
// mints the pre-backup ID itself after the checks pass, so a failed check
// never names a backup that was never taken.
func MintRestoreID() RestoreID {
	return RestoreID(ident.NewID("rst-"))
}

// MintRestoreIDs mints the restore ID and its pre-backup ID: rst- for the
// restore row, bkp- for the backup of the live data taken before the apply.
// It stays for callers that mint both up front; routes mint only the restore
// ID through MintRestoreID so a failed check never names a backup that was
// never taken.
func MintRestoreIDs() (RestoreID, ID) {
	return MintRestoreID(), ID(ident.NewID("bkp-"))
}
