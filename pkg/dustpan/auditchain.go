package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// auditChainCheckInterval is the time between two scheduled chain checks.
const auditChainCheckInterval = time.Hour

// migrationWaits is the count of cache limits that a migration waits after
// the enter of maintenance mode, and again after it stores the new state:
// every instance reads the state again within one cache limit.
const migrationWaits = 2

// WithAuditChain sets the hash chain of the audit log of this instance:
// audit.ChainOn, the default, or audit.ChainOff. The server reads it once at
// start. When it differs from the state that the database keeps, the server
// starts in maintenance mode, and a person approves the change through the
// installation API, which migrates the log. The server checks the chain of
// every scope at start and every hour, and sets
// dustpan_audit_chain_invalid.
func WithAuditChain(setting audit.ChainSetting) Option {
	return func(o *settings) { o.auditChain = setting }
}

// auditChain holds the setting of this instance, the ports of the chain and
// the last state of the chain that the instance read. The zero value is not
// usable: call newAuditChain.
type auditChain struct {
	store      audit.ChainStore
	entries    audit.Store
	server     audit.ServerStore
	namespaces namespace.Store
	logger     *slog.Logger
	now        func() time.Time
	observed   *metrics.ServerMetrics
	// maint, restoreRunning and freshness are set after the services that
	// need the chain exist. A nil freshness asks for no MFA step: built-in
	// MFA does not exist yet.
	maint          *maintenance.Service
	restoreRunning func(ctx context.Context) (bool, error)
	freshness      accesstoken.Freshness
	// queue carries an approved change to the migration loop of Run.
	queue     chan audit.ChainChange
	migrating atomic.Bool
	// migrationsRunning reports whether startMigrations runs its loop.
	migrationsRunning atomic.Bool
	limit             time.Duration
	setting           audit.ChainSetting
	mu                sync.Mutex
	rec               audit.ChainRecord
	readAt            time.Time
	read              bool
}

// Messages of the audit chain gate.
const (
	// chainMismatchMessage answers a request to an instance whose setting
	// differs from the stored state.
	chainMismatchMessage = "The audit chain setting of this server differs from the stored state. " +
		"Restart this server with the stored setting, or approve the change with " +
		"'opendustpan audit-chain approve --server <url>'."
	// chainPendingMessage answers a request while a change of the chain
	// waits for approval.
	chainPendingMessage = "An audit chain change waits for approval. " +
		"Run 'opendustpan audit-chain approve --server <url>'."
	// chainMigratingMessage answers a request while a migration of the
	// chain runs.
	chainMigratingMessage = "An audit chain migration runs. Requests stop until it is complete."
)

// errMigrationsStopped reports an approval while the server runs no
// migration loop.
var errMigrationsStopped = errors.New(
	"the server does not run audit chain migrations now: start the server and approve again",
)

// newAuditChain answers the chain of the namespace store, or nil when the
// store keeps no chain and the setting is on: such a store keeps its own
// behavior. The setting off on such a store fails.
func newAuditChain(cfg *settings, observed *metrics.ServerMetrics) (*auditChain, error) {
	store, okChain := cfg.namespaceStore.(audit.ChainStore)
	entries, okEntries := cfg.namespaceStore.(audit.Store)
	server, okServer := cfg.namespaceStore.(audit.ServerStore)

	if !okChain || !okEntries || !okServer {
		if cfg.auditChain != audit.ChainOn {
			return nil, fmt.Errorf("namespace store cannot turn the audit chain off: %w", ErrMissingOption)
		}

		return nil, nil //nolint:nilnil // No chain without an error means the store keeps its own chain.
	}

	return &auditChain{
		store:             store,
		entries:           entries,
		server:            server,
		namespaces:        cfg.namespaceStore,
		logger:            cfg.logger,
		now:               cfg.now,
		observed:          observed,
		maint:             nil,
		restoreRunning:    nil,
		freshness:         nil,
		queue:             make(chan audit.ChainChange, 1),
		migrating:         atomic.Bool{},
		migrationsRunning: atomic.Bool{},
		limit:             maintenance.DefaultCacheLimit,
		setting:           cfg.auditChain,
		mu:                sync.Mutex{},
		rec: audit.ChainRecord{
			ChangedAt: time.Time{}, ChangedBy: "", State: audit.ChainStateOn, Pending: false,
			MigratingAt: time.Time{}, LogReplaced: false,
		},
		readAt: time.Time{},
		read:   false,
	}, nil
}

// Setting implements serverapi.AuditChain.
func (c *auditChain) Setting() audit.ChainSetting {
	return c.setting
}

// Status implements serverapi.AuditChain. It reads the stored state, not
// the cache.
func (c *auditChain) Status(ctx context.Context) (serverapi.AuditChainStatus, error) {
	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return serverapi.AuditChainStatus{}, fmt.Errorf("read audit chain state: %w", err)
	}

	return serverapi.AuditChainStatus{
		ChangedAt: rec.ChangedAt,
		ChangedBy: rec.ChangedBy,
		State:     rec.State,
		Setting:   c.setting,
		Pending:   rec.Pending,
		Migrating: c.migrating.Load() || chainClaimHolds(rec, c.chainNow()),
	}, nil
}

// Approve implements serverapi.AuditChain: it checks the MFA step of a
// change to off, refuses while a restore runs, enters maintenance mode,
// writes the entry of the approval in the server scope and hands the change
// to the migration loop.
func (c *auditChain) Approve(ctx context.Context, caller audit.Entry) error {
	if !c.migrationsRunning.Load() {
		return errMigrationsStopped
	}

	if c.setting == audit.ChainOff && c.freshness != nil {
		if err := c.freshness.RequireFresh(ctx, caller.Subject); err != nil {
			return fmt.Errorf("%w: %w", audit.ErrChainStepRequired, err)
		}
	}

	if !c.migrating.CompareAndSwap(false, true) {
		return audit.ErrChainMigrating
	}

	change, err := c.recordApproval(ctx, caller)
	if err != nil {
		c.migrating.Store(false)

		return err
	}

	select {
	case c.queue <- change:
		return nil
	default:
		c.releaseClaim(ctx)
		c.migrating.Store(false)

		return audit.ErrChainMigrating
	}
}

// chainNow answers the time of this instance.
func (c *auditChain) chainNow() time.Time {
	if c.now != nil {
		return c.now()
	}

	return time.Now()
}

// chainClaimHolds reports whether rec holds a claim within the lease at now.
func chainClaimHolds(rec audit.ChainRecord, now time.Time) bool {
	if rec.MigratingAt.IsZero() {
		return false
	}

	return now.Sub(rec.MigratingAt) < audit.ChainMigrationLease
}

// held reports whether a change of the chain waits: the maintenance service
// does not leave maintenance mode while it does.
func (c *auditChain) held(ctx context.Context) (bool, error) {
	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return false, fmt.Errorf("read audit chain state: %w", err)
	}

	return rec.Pending, nil
}

// startUp compares the setting with the stored state before the server
// serves. Every append of this instance follows the stored state. When the
// swap of a restore replaced the log, it compares the log with the stored
// state first. When the setting differs, or the log does not agree, the
// change waits for an approval and the installation enters maintenance mode.
func (c *auditChain) startUp(ctx context.Context) error {
	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
	}

	if rec.LogReplaced {
		rec, err = c.compareRestoredLog(ctx, rec)
		if err != nil {
			return fmt.Errorf("audit chain: %w", err)
		}
	}

	rec, err = c.cancelWaitingChange(ctx, rec)
	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
	}

	if c.setting.State() != rec.State && !rec.Pending {
		rec.Pending = true

		if err := c.store.SaveChainRecord(ctx, rec); err != nil {
			return fmt.Errorf("audit chain: %w", err)
		}
	}

	c.remember(rec)

	if !rec.Pending {
		return nil
	}

	c.logger.WarnContext(ctx, "audit chain change waits for approval",
		slog.String("setting", c.setting.String()), slog.String("state", string(rec.State)))

	return c.enterMaintenance(ctx)
}

// cancelWaitingChange clears a waiting change when the setting matches the
// stored state, the log agrees with it and no migration holds a claim. A log
// that does not agree keeps the wait. It saves the cleared record, logs the
// end and leaves maintenance mode.
func (c *auditChain) cancelWaitingChange(ctx context.Context, rec audit.ChainRecord) (audit.ChainRecord, error) {
	if !rec.Pending || c.setting.State() != rec.State || chainClaimHolds(rec, c.chainNow()) {
		return rec, nil
	}

	scopes, err := c.scopes(ctx)
	if err != nil {
		return rec, err
	}

	agrees, err := audit.LogAgrees(ctx, c.store, scopes, rec.State)
	if err != nil {
		return rec, err
	}

	if !agrees {
		return rec, nil
	}

	rec.Pending = false

	if err := c.store.SaveChainRecord(ctx, rec); err != nil {
		return rec, err
	}

	c.logger.InfoContext(ctx, "audit chain change canceled: the setting matches the stored state")

	if c.maint == nil {
		return rec, nil
	}

	if err := c.maint.Leave(ctx); err != nil && !errors.Is(err, maintenance.ErrHeld) {
		return rec, fmt.Errorf("leave maintenance mode: %w", err)
	}

	return rec, nil
}

// enterMaintenance enters maintenance mode for a waiting change. Active work
// keeps normal operation and logs a warning: the gate still refuses writes,
// and the approval enters maintenance mode again.
func (c *auditChain) enterMaintenance(ctx context.Context) error {
	if c.maint == nil {
		return nil
	}

	_, err := c.maint.Enter(ctx, false)
	if errors.Is(err, maintenance.ErrActiveWork) {
		c.logger.WarnContext(ctx, "audit chain change found active work; maintenance mode starts at the approval")

		return nil
	}

	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
	}

	return nil
}

// compareRestoredLog compares the log that a restore swapped in with the
// state in rec, and stores the result: a log that agrees clears the note of
// the swap, else the change waits for an approval that migrates the
// restored log.
func (c *auditChain) compareRestoredLog(ctx context.Context, rec audit.ChainRecord) (audit.ChainRecord, error) {
	scopes, err := c.scopes(ctx)
	if err != nil {
		return rec, err
	}

	agrees, err := audit.LogAgrees(ctx, c.store, scopes, rec.State)
	if err != nil {
		return rec, fmt.Errorf("compare the restored audit log: %w", err)
	}

	rec.LogReplaced = false

	if !agrees {
		rec.Pending = true

		c.logger.WarnContext(ctx, "restored audit log does not agree with the audit chain state",
			slog.String("state", string(rec.State)))
	}

	if err := c.store.SaveChainRecord(ctx, rec); err != nil {
		return rec, fmt.Errorf("save audit chain state: %w", err)
	}

	if agrees {
		c.checkRestoredLog(ctx, scopes)
	}

	return rec, nil
}

// checkRestoredLog checks the chain of every scope of a restored log that
// agrees with the stored state. A failed check counts the restore in
// dustpan_audit_restore_check_failures_total and logs an error; the restore
// still succeeds.
func (c *auditChain) checkRestoredLog(ctx context.Context, scopes []namespace.Name) {
	invalid, err := audit.CheckScopes(ctx, c.entries, scopes)
	if err == nil && invalid == 0 {
		return
	}

	if c.observed != nil {
		c.observed.IncAuditRestoreCheckFailure()
	}

	c.logger.ErrorContext(ctx, "restored audit log failed the chain check", slog.Int("scopes", invalid))
}

// afterRestore is the after-apply step of a restore: when the swap
// replaced the log, it compares the restored log with the stored state.
func (c *auditChain) afterRestore(ctx context.Context) error {
	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return fmt.Errorf("audit chain after restore: %w", err)
	}

	if !rec.LogReplaced {
		return nil
	}

	rec, err = c.compareRestoredLog(ctx, rec)
	if err != nil {
		return fmt.Errorf("audit chain after restore: %w", err)
	}

	c.remember(rec)

	return nil
}

// admitRestore refuses a restore while a change of the chain waits or
// migrates.
func (c *auditChain) admitRestore(ctx context.Context) error {
	if c.migrating.Load() {
		return audit.ErrChainMigrating
	}

	pending, err := c.held(ctx)
	if err != nil {
		return err
	}

	if pending {
		return audit.ErrChainChangeWaits
	}

	return nil
}

// remember stores rec as the last state that this instance read.
func (c *auditChain) remember(rec audit.ChainRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.rec, c.readAt, c.read = rec, time.Now(), true
}

// current answers the stored state, read at most once per cache limit.
// The gate uses this cache. Appends read the stored state in their own
// transaction. A failed read keeps the last state it read.
func (c *auditChain) current(ctx context.Context) audit.ChainRecord {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.read && time.Since(c.readAt) < c.limit {
		return c.rec
	}

	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return c.rec
	}

	c.rec, c.readAt, c.read = rec, time.Now(), true

	return rec
}

// recordApproval runs the checks and the writes of an approval while this
// instance holds the migration flag, and answers the change to migrate. The
// claim in the store orders approvals on every instance: exactly one wins.
func (c *auditChain) recordApproval(ctx context.Context, caller audit.Entry) (audit.ChainChange, error) {
	none := audit.ChainChange{At: time.Time{}, By: "", To: ""}

	if err := c.store.ClaimChainMigration(ctx, c.chainNow(), audit.ChainMigrationLease); err != nil {
		return none, err
	}

	if err := c.refuseWhileRestoring(ctx); err != nil {
		c.releaseClaim(ctx)

		return none, err
	}

	if c.maint != nil {
		if _, err := c.maint.Enter(ctx, false); err != nil {
			c.releaseClaim(ctx)

			return none, fmt.Errorf("enter maintenance mode: %w", err)
		}
	}

	change := audit.ChainChange{At: c.now(), By: caller.Subject, To: c.setting.State()}

	if _, err := c.server.AppendServer(ctx, audit.ChainSetEntry(caller, change)); err != nil {
		c.releaseClaim(ctx)

		return none, fmt.Errorf("append audit chain approval: %w", err)
	}

	return change, nil
}

// releaseClaim releases the claim of a migration that stops before it runs.
// A failed release only logs: the lease bounds the block.
func (c *auditChain) releaseClaim(ctx context.Context) {
	if err := c.store.ReleaseChainMigration(ctx); err != nil {
		c.logger.WarnContext(ctx, "release audit chain migration", slog.String("error", err.Error()))
	}
}

// refuseWhileRestoring reports backup.ErrRestoreRunning while a restore
// runs.
func (c *auditChain) refuseWhileRestoring(ctx context.Context) error {
	if c.restoreRunning == nil {
		return nil
	}

	running, err := c.restoreRunning(ctx)
	if err != nil {
		return fmt.Errorf("read running restores: %w", err)
	}

	if running {
		return backup.ErrRestoreRunning
	}

	return nil
}

// startMigrations runs each approved change until ctx is done. The returned
// function waits until the loop stopped.
func (c *auditChain) startMigrations(ctx context.Context) func() {
	var wg sync.WaitGroup

	c.migrationsRunning.Store(true)

	wg.Go(func() {
		defer c.migrationsRunning.Store(false)

		for {
			select {
			case <-ctx.Done():
				return
			case change := <-c.queue:
				c.migrate(ctx, change)
			}
		}
	})

	return wg.Wait
}

// migrate runs the migration of change: it waits two cache limits so every
// instance refuses writes, writes the start or end entry in each scope,
// stores the new state, waits two cache limits so every instance sees it,
// and leaves maintenance mode. A failure releases the claim and logs an
// error, and keeps the change waiting, so a person can approve it again.
func (c *auditChain) migrate(ctx context.Context, change audit.ChainChange) {
	defer c.migrating.Store(false)

	if err := c.migrateSteps(ctx, change); err != nil {
		c.releaseClaim(context.WithoutCancel(ctx))

		if ctx.Err() == nil {
			c.logger.ErrorContext(ctx, "audit chain migration failed", slog.String("error", err.Error()))
		}

		return
	}

	c.logger.InfoContext(ctx, "audit chain migration is complete",
		slog.String("state", string(change.To)), slog.String("approved_by", change.By))
}

// migrateSteps runs the steps of migrate and answers the first failure.
func (c *auditChain) migrateSteps(ctx context.Context, change audit.ChainChange) error {
	if err := sleepContext(ctx, migrationWaits*c.limit); err != nil {
		return err
	}

	if err := c.refuseWhileRestoring(ctx); err != nil {
		return err
	}

	fences, err := c.fences(ctx)
	if err != nil {
		return err
	}

	if err := audit.MigrateChain(ctx, c.store, fences, change); err != nil {
		return fmt.Errorf("migrate audit chain: %w", err)
	}

	c.forget()

	if err := sleepContext(ctx, migrationWaits*c.limit); err != nil {
		return err
	}

	if c.maint == nil {
		return nil
	}

	if err := c.maint.Leave(ctx); err != nil {
		return fmt.Errorf("leave maintenance mode: %w", err)
	}

	return nil
}

// forget drops the cached state, so the next request reads it again.
func (c *auditChain) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.read = false
}

// sleepContext waits d, or answers the error of ctx when it ends first.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("audit chain migration: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// fences answers the fence of every namespace and of the server scope: the
// scopes of a migration.
func (c *auditChain) fences(ctx context.Context) ([]namespace.Fence, error) {
	all, err := c.namespaces.ListNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	fences := make([]namespace.Fence, 0, len(all)+1)
	for _, ns := range all {
		fences = append(fences, ns.Fence())
	}

	return append(fences, audit.ServerFence()), nil
}

// scopes answers every namespace and the server scope: the scopes of a
// check of the whole log.
func (c *auditChain) scopes(ctx context.Context) ([]namespace.Name, error) {
	all, err := c.namespaces.ListNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	scopes := make([]namespace.Name, 0, len(all)+1)
	for _, ns := range all {
		scopes = append(scopes, ns.Name)
	}

	return append(scopes, audit.ServerScope), nil
}

// startCheck checks the chain of every scope now and then every hour until
// ctx is done. The returned function waits until the check loop stopped.
func (c *auditChain) startCheck(ctx context.Context) func() {
	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := time.NewTicker(auditChainCheckInterval)
		defer ticker.Stop()

		for {
			c.check(ctx)

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})

	return wg.Wait
}

// check checks every namespace and the server scope once, and sets
// dustpan_audit_chain_invalid. A failed check logs the error, never an
// entry.
func (c *auditChain) check(ctx context.Context) {
	scopes, err := c.scopes(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.WarnContext(ctx, "audit chain check: list namespaces", slog.String("error", err.Error()))
		}

		return
	}

	invalid, err := audit.CheckScopes(ctx, c.entries, scopes)
	if err != nil && ctx.Err() != nil {
		return
	}

	if err != nil {
		c.logger.WarnContext(ctx, "audit chain check failed", slog.String("error", err.Error()))
	}

	if c.observed != nil {
		c.observed.SetAuditChainInvalid(invalid)
	}

	if invalid > 0 {
		c.logger.ErrorContext(ctx, "audit chain check found scopes that do not match",
			slog.Int("scopes", invalid))
	}
}

// bindAuditChain gives chain the services that are built after it: the
// maintenance service and the check for a running restore.
func bindAuditChain(chain *auditChain, maint *maintenance.Service, restores *backup.RestoreService, cfg *settings) {
	if chain == nil {
		return
	}

	chain.maint = maint

	switch registry, ok := cfg.namespaceStore.(backup.RestoreRegistry); {
	case restores != nil:
		chain.restoreRunning = restores.Running
	case ok:
		chain.restoreRunning = func(ctx context.Context) (bool, error) {
			return restoreRowRunning(ctx, registry)
		}
	default:
		// The store keeps no restores, so no restore can run.
	}
}

// restoreRowRunning reports whether registry holds a restore that is still
// checking or applying.
func restoreRowRunning(ctx context.Context, registry backup.RestoreRegistry) (bool, error) {
	records, err := registry.ListRestores(ctx)
	if err != nil {
		return false, fmt.Errorf("list restores: %w", err)
	}

	for i := range records {
		if records[i].State == backup.RestoreChecking || records[i].State == backup.RestoreApplying {
			return true, nil
		}
	}

	return false, nil
}

// auditChainPort answers chain as the port of the installation API, or nil
// without a chain, never an interface that holds a nil pointer.
//
//nolint:ireturn // The installation API takes the port.
func auditChainPort(chain *auditChain) serverapi.AuditChain {
	if chain == nil {
		return nil
	}

	return chain
}

// startUpAuditChain compares the setting of the chain with the stored state
// before the server serves.
func (s *Server) startUpAuditChain(ctx context.Context) error {
	if s.chain == nil {
		return nil
	}

	return s.chain.startUp(ctx)
}

// startAuditChain starts the scheduled check and the migration loop of the
// chain until ctx is done. The returned function waits until both stopped.
func (s *Server) startAuditChain(ctx context.Context) func() {
	if s.chain == nil {
		return func() {}
	}

	waitCheck := s.chain.startCheck(ctx)
	waitMigrations := s.chain.startMigrations(ctx)

	return func() {
		waitCheck()
		waitMigrations()
	}
}
