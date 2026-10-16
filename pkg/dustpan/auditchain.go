package dustpan

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
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

// WithAuditCheckpoints turns signed checkpoints of the audit log on. They
// are off by default. They need the chain on and a blob store: the server
// start fails when the chain is off. While they are on, each scheduled
// chain check signs the last hash of each scope whose chain matches with
// signer, stores the checkpoint in the blob store, and checks every stored
// checkpoint. A scope with a checkpoint that does not match counts in
// dustpan_audit_chain_invalid.
func WithAuditCheckpoints(signer *audit.Signer) Option {
	return func(o *settings) { o.auditSigner = signer }
}

// ErrCheckpointsNeedChain reports signed checkpoints with the chain off.
var ErrCheckpointsNeedChain = errors.New("audit checkpoints need the audit chain on: set --audit-chain=on")

// auditChain holds the setting of this instance, the ports of the chain and
// the last state of the chain that the instance read. The zero value is not
// usable: call newAuditChain.
type auditChain struct {
	store      audit.ChainStore
	entries    audit.Store
	server     audit.ServerStore
	namespaces namespace.Store
	principals principalReader
	logger     *slog.Logger
	now        func() time.Time
	observed   *metrics.ServerMetrics
	// checkpoints signs and checks the signed checkpoints of the log, or
	// nil while they are off.
	checkpoints *audit.Checkpoints
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

// principalReader answers the principal with a subject. It returns
// principal.ErrNotFound when no principal has the subject.
type principalReader interface {
	// GetPrincipalBySubject answers the principal with the subject subject,
	// or principal.ErrNotFound when no principal has it.
	GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error)
}

// newAuditChain answers the chain of the namespace store, or nil when the
// store keeps no chain and the setting is on: such a store keeps its own
// behavior. The setting off on such a store fails.
func newAuditChain(cfg *settings, observed *metrics.ServerMetrics) (*auditChain, error) {
	store, okChain := cfg.namespaceStore.(audit.ChainStore)
	entries, okEntries := cfg.namespaceStore.(audit.Store)
	server, okServer := cfg.namespaceStore.(audit.ServerStore)

	if !okChain || !okEntries || !okServer {
		if cfg.auditChain != audit.ChainOn || cfg.auditSigner != nil {
			return nil, fmt.Errorf("namespace store cannot turn the audit chain on or off: %w", ErrMissingOption)
		}

		return nil, nil //nolint:nilnil // No chain without an error means the store keeps its own chain.
	}

	var principals principalReader

	if p, ok := cfg.namespaceStore.(principalReader); ok {
		principals = p
	}

	checkpoints, err := newCheckpoints(cfg)
	if err != nil {
		return nil, err
	}

	return &auditChain{
		store:             store,
		entries:           entries,
		server:            server,
		namespaces:        cfg.namespaceStore,
		principals:        principals,
		logger:            cfg.logger,
		now:               cfg.now,
		observed:          observed,
		checkpoints:       checkpoints,
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
			EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
		},
		readAt: time.Time{},
		read:   false,
	}, nil
}

// newCheckpoints answers the signed checkpoints of cfg, or nil while they
// are off. They need a blob store and a store that reads one entry at a
// sequence.
func newCheckpoints(cfg *settings) (*audit.Checkpoints, error) {
	if cfg.auditSigner == nil {
		return nil, nil //nolint:nilnil // Nil without an error means signed checkpoints are off.
	}

	if cfg.blobStore == nil {
		return nil, fmt.Errorf("audit checkpoints need a blob store: %w", ErrMissingOption)
	}

	reader, ok := cfg.namespaceStore.(audit.EntryReader)
	if !ok {
		return nil, fmt.Errorf("audit checkpoints need a store that reads one audit entry: %w", ErrMissingOption)
	}

	return audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs:   cfg.blobStore,
		Entries: reader,
		Signer:  cfg.auditSigner,
	}), nil
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
		return audit.ErrChainMigrationsStopped
	}

	if c.setting == audit.ChainOff {
		if err := c.refuseServiceCaller(ctx, caller.Subject); err != nil {
			return err
		}
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

// refuseServiceCaller refuses an approval of a change to off when subject
// belongs to a service principal. A subject with no principal row is a
// person and passes.
func (c *auditChain) refuseServiceCaller(ctx context.Context, subject string) error {
	if c.principals == nil {
		return nil
	}

	got, err := c.principals.GetPrincipalBySubject(ctx, subject)
	if errors.Is(err, principal.ErrNotFound) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read principal: %w", err)
	}

	if got.Kind == principal.KindService {
		return audit.ErrChainNeedsPerson
	}

	return nil
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
	if c.checkpoints != nil && c.setting != audit.ChainOn {
		return ErrCheckpointsNeedChain
	}

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

	if c.checkpoints != nil {
		signer := c.checkpoints.Signer()
		c.logger.InfoContext(ctx, "audit checkpoints are on",
			slog.String("key_id", signer.KeyID()),
			slog.String("public_key", base64.StdEncoding.EncodeToString(signer.PublicKey())))
	}

	if !rec.Pending {
		return nil
	}

	c.logger.WarnContext(ctx, "audit chain change waits for approval",
		slog.String("setting", c.setting.String()), slog.String("state", string(rec.State)))

	return c.enterMaintenance(ctx)
}

// cancelWaitingChange clears a waiting change when the setting matches the
// stored state, the log agrees with it and no migration holds a claim. A log
// that does not agree keeps the wait. It saves the cleared record and logs
// the end. It leaves maintenance mode only when the chain entered it itself
// and no restore runs, else it keeps maintenance mode.
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

	return c.leaveForCancel(ctx, rec)
}

// leaveForCancel leaves maintenance mode for a canceled wait. It leaves only
// when the chain entered maintenance mode itself and no restore runs, else
// it keeps maintenance mode.
func (c *auditChain) leaveForCancel(ctx context.Context, rec audit.ChainRecord) (audit.ChainRecord, error) {
	if c.maint == nil || !rec.EnteredMaintenance {
		return rec, nil
	}

	if err := c.refuseWhileRestoring(ctx); err != nil {
		if errors.Is(err, backup.ErrRestoreRunning) {
			return rec, nil
		}

		return rec, err
	}

	if err := c.maint.Leave(ctx); err != nil {
		if errors.Is(err, maintenance.ErrHeld) {
			return rec, nil
		}

		return rec, fmt.Errorf("leave maintenance mode: %w", err)
	}

	rec.EnteredMaintenance = false

	if err := c.store.SaveChainRecord(ctx, rec); err != nil {
		return rec, err
	}

	return rec, nil
}

// enterMaintenance enters maintenance mode for a waiting change. Active work
// keeps normal operation and logs a warning: the gate still refuses writes,
// and the approval enters maintenance mode again. It notes in the stored
// record when the chain entered maintenance mode itself.
func (c *auditChain) enterMaintenance(ctx context.Context) error {
	if c.maint == nil {
		return nil
	}

	before, err := c.maint.Get(ctx)
	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
	}

	if before == maintenance.StateMaintenance {
		return nil
	}

	_, err = c.maint.Enter(ctx, false)
	if errors.Is(err, maintenance.ErrActiveWork) {
		c.logger.WarnContext(ctx, "audit chain change found active work; maintenance mode starts at the approval")

		return nil
	}

	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
	}

	return c.markEnteredMaintenance(ctx)
}

// markEnteredMaintenance notes in the stored record that the chain entered
// maintenance mode itself.
func (c *auditChain) markEnteredMaintenance(ctx context.Context) error {
	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
	}

	if rec.EnteredMaintenance {
		return nil
	}

	rec.EnteredMaintenance = true

	if err := c.store.SaveChainRecord(ctx, rec); err != nil {
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

// newEpochAfterRestore returns the after-apply step of a restore for the
// signed audit checkpoints: it starts a new epoch, because the restored log
// can lose entries that older checkpoints sign, and writes one entry of the
// kind change in @server. Without checkpoints it does nothing.
func newEpochAfterRestore(
	checkpoints *audit.Checkpoints, server audit.ServerStore, now func() time.Time,
) restoreStep {
	return func(ctx context.Context) error {
		if checkpoints == nil {
			return nil
		}

		at := now()

		epoch, err := checkpoints.NewEpoch(ctx, at)
		if err != nil {
			return fmt.Errorf("audit checkpoints after restore: %w", err)
		}

		if server == nil {
			return nil
		}

		if _, err := server.AppendServer(ctx, epochEntry(epoch, at)); err != nil {
			return fmt.Errorf("record audit checkpoint epoch: %w", err)
		}

		return nil
	}
}

// epochEntry returns the entry that records the checkpoint epoch that a
// restore started.
func epochEntry(epoch int64, at time.Time) audit.Entry {
	return audit.Entry{
		At:           at,
		Namespace:    audit.ServerScope,
		Subject:      "opendustpan serve",
		IDPSubject:   "",
		TokenID:      "",
		Action:       string(identity.ActionServerManageAudit),
		Object:       audit.ServerScope.String(),
		ObjectID:     "",
		Version:      "",
		ValuePath:    "/audit/checkpoints/epoch/" + strconv.FormatInt(epoch, 10),
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
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
// It notes in the stored record when the chain entered maintenance mode
// itself.
func (c *auditChain) recordApproval(ctx context.Context, caller audit.Entry) (audit.ChainChange, error) {
	none := audit.ChainChange{At: time.Time{}, By: "", To: ""}

	if err := c.store.ClaimChainMigration(ctx, c.chainNow(), audit.ChainMigrationLease); err != nil {
		return none, err
	}

	if err := c.refuseWhileRestoring(ctx); err != nil {
		c.releaseClaim(ctx)

		return none, err
	}

	if err := c.enterForApproval(ctx); err != nil {
		c.releaseClaim(ctx)

		return none, err
	}

	change := audit.ChainChange{At: c.now(), By: caller.Subject, To: c.setting.State()}

	if _, err := c.server.AppendServer(ctx, audit.ChainSetEntry(caller, change)); err != nil {
		c.releaseClaim(ctx)

		return none, fmt.Errorf("append audit chain approval: %w", err)
	}

	return change, nil
}

// enterForApproval enters maintenance mode for an approval. It notes in the
// stored record when the chain entered maintenance mode itself.
func (c *auditChain) enterForApproval(ctx context.Context) error {
	if c.maint == nil {
		return nil
	}

	before, err := c.maint.Get(ctx)
	if err != nil {
		return fmt.Errorf("enter maintenance mode: %w", err)
	}

	if _, err := c.maint.Enter(ctx, false); err != nil {
		return fmt.Errorf("enter maintenance mode: %w", err)
	}

	if before != maintenance.StateNormal {
		return nil
	}

	return c.markEnteredMaintenance(ctx)
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

// migrateSteps runs the steps of migrate and answers the first failure. It
// leaves maintenance mode only when the chain entered it itself, else it
// keeps the maintenance mode of the operator.
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

	err = audit.MigrateChain(ctx, c.store, fences, change, c.chainNow)
	if err != nil {
		return fmt.Errorf("migrate audit chain: %w", err)
	}

	c.forget()

	err = sleepContext(ctx, migrationWaits*c.limit)
	if err != nil {
		return err
	}

	if c.maint == nil {
		return nil
	}

	var rec audit.ChainRecord

	rec, err = c.store.ChainRecord(ctx)
	if err != nil {
		return fmt.Errorf("read audit chain state: %w", err)
	}

	if !rec.EnteredMaintenance {
		return nil
	}

	err = c.maint.Leave(ctx)
	if err != nil {
		return fmt.Errorf("leave maintenance mode: %w", err)
	}

	rec.EnteredMaintenance = false

	err = c.store.SaveChainRecord(ctx, rec)
	if err != nil {
		return fmt.Errorf("save audit chain state: %w", err)
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

	if c.checkpoints == nil {
		c.checkChain(ctx, scopes)

		return
	}

	c.checkChainAndCheckpoints(ctx, scopes)
}

// checkChain runs the chain check of every scope of scopes and reports the
// result.
func (c *auditChain) checkChain(ctx context.Context, scopes []namespace.Name) {
	invalid, err := audit.CheckScopes(ctx, c.entries, scopes)

	c.reportCheck(ctx, invalid, err)
}

// checkChainAndCheckpoints runs the chain check of each scope, then the
// checkpoint step: it checks the stored checkpoints of the scope and signs
// a new checkpoint only when both match. While a change of the chain waits,
// it checks but signs nothing. A scope that fails either check counts in
// dustpan_audit_chain_invalid.
func (c *auditChain) checkChainAndCheckpoints(ctx context.Context, scopes []namespace.Name) {
	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.WarnContext(ctx, "audit chain check: read chain state", slog.String("error", err.Error()))
		}

		return
	}

	var (
		invalid int
		errs    []error
	)

	for _, scope := range scopes {
		ok, err := c.checkScope(ctx, scope)
		if err != nil {
			errs = append(errs, fmt.Errorf("check %s: %w", scope, err))
		}

		if !ok {
			invalid++

			continue
		}

		if rec.Pending {
			continue
		}

		if _, err := c.checkpoints.Write(ctx, scope, c.chainNow()); err != nil {
			errs = append(errs, fmt.Errorf("check %s: checkpoint: %w", scope, err))
			invalid++
		}
	}

	c.reportCheck(ctx, invalid, errors.Join(errs...))
}

// checkScope checks the chain of scope and the stored checkpoints of
// scope. It answers false when either does not match, and an error when a
// check fails. It never signs: the caller signs the head after both match.
func (c *auditChain) checkScope(ctx context.Context, scope namespace.Name) (bool, error) {
	chain, err := c.entries.Check(ctx, scope)
	if err != nil {
		return false, fmt.Errorf("chain: %w", err)
	}

	if !chain.Valid {
		return false, nil
	}

	result, err := c.checkpoints.Verify(ctx, scope)
	if err != nil {
		return false, fmt.Errorf("checkpoints: %w", err)
	}

	if !result.Valid {
		c.logger.ErrorContext(ctx, "audit checkpoint does not match",
			slog.String("scope", scope.String()), slog.Int64("sequence", result.BadSequence))

		return false, nil
	}

	return true, nil
}

// reportCheck sets dustpan_audit_chain_invalid to invalid and logs the
// result of a check. A check that the caller stopped reports nothing.
func (c *auditChain) reportCheck(ctx context.Context, invalid int, err error) {
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

// checkpointEpochsOf answers the checkpoint epochs of chain for the server
// API, or nil while signed checkpoints are off.
//
//nolint:ireturn // A nil *audit.Checkpoints in the interface is not nil, so the route would call it; answer a nil interface.
func checkpointEpochsOf(chain *auditChain) serverapi.CheckpointEpochs {
	if chain == nil || chain.checkpoints == nil {
		return nil
	}

	return chain.checkpoints
}

// chainChangeWaits reports whether a change of the chain waits for
// approval. The retention sweep and the inactivity sweep skip one run
// while it does. Both write audit entries outside the gate, so they pause
// until the migration ends. A read error also skips the run: it logs at
// WARN and answers true, so no sweep writes while the state is unknown.
func (s *Server) chainChangeWaits(ctx context.Context) bool {
	if s.chain == nil {
		return false
	}

	held, err := s.chain.held(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "skipping the sweep: the audit chain state is unknown")

		return true
	}

	if held {
		s.logger.DebugContext(ctx, "skipping the sweep: an audit chain change waits")
	}

	return held
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
