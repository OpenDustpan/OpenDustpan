package dustpan

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// auditChainCheckInterval is the time between two scheduled chain checks.
const auditChainCheckInterval = time.Hour

// auditChainReadTimeout bounds the read of the stored check result when a
// scrape asks for dustpan_audit_chain_invalid.
const auditChainReadTimeout = 2 * time.Second

// auditChainDrainTimeout bounds the startup drain of left migration
// operations: a long log or a stuck database call delays startup by at
// most this time, then the background runner continues the migration.
const auditChainDrainTimeout = 30 * time.Second

// The durable operation that migrates the audit chain after an approval. One
// start key gives one operation, so two approvals give one operation while
// the first still runs.
const (
	auditChainKind    durable.Kind = "audit-chain-migration"
	auditChainKey     string       = "audit-chain-migration"
	auditChainVersion int          = 1
)

// auditChainStep is the single step of the migration: it writes the markers
// of every scope, stores the new state and leaves maintenance mode. The
// marker writes skip scopes that already agree, and a retry that finds the
// stored state already at the change does nothing, so the step is safe to
// run again after a stop.
const auditChainStep = "migrate"

// WithAuditChain sets the hash chain of the audit log of this instance:
// audit.ChainOn, the default, or audit.ChainOff. The server reads it once at
// start. When it differs from the state that the database keeps, the server
// starts in maintenance mode, and a person approves the change through the
// installation API, which migrates the log. One instance checks the chain of
// every scope at start and every hour, and stores the result. Each instance
// exports it as dustpan_audit_chain_invalid.
func WithAuditChain(setting audit.ChainSetting) Option {
	return func(o *settings) { o.auditChain = setting }
}

// WithAuditChainCheckInterval sets the time between two audit chain checks.
// The default is one hour. One instance runs each check. NewServer fails
// with ErrInvalidOption for a value of zero or less. Each check reads the
// chain and the checkpoints of every scope, so a small value loads the
// database and the blob store: keep the default unless a test needs less.
func WithAuditChainCheckInterval(every time.Duration) Option {
	return func(o *settings) { o.auditChainInterval = every }
}

// checkAuditChainInterval refuses a check interval of zero or less.
func checkAuditChainInterval(every time.Duration) error {
	if every <= 0 {
		return fmt.Errorf("%w: WithAuditChainCheckInterval needs more than 0, got %s", ErrInvalidOption, every)
	}

	return nil
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
	approve    chainApprovalStore
	namespaces namespace.Store
	principals principalReader
	logger     *slog.Logger
	now        func() time.Time
	observed   *metrics.ServerMetrics
	// checkpoints signs and checks the signed checkpoints of the log, or
	// nil while they are off.
	checkpoints *audit.Checkpoints
	// checks keeps the result of the last scheduled check and the mark of a
	// checkpoint mismatch, or is nil when the store keeps none. Every
	// instance reads the result at scrape time.
	checks audit.CheckStore
	// maint, restoreRunning and freshness are set after the services that
	// need the chain exist. A nil freshness asks for no MFA step: built-in
	// MFA does not exist yet.
	maint          *maintenance.Service
	restoreRunning func(ctx context.Context) (bool, error)
	freshness      accesstoken.Freshness
	// durableOps starts the migration operation of an approval. The runner
	// of every instance claims it.
	durableOps durable.Store
	// runner claims migration operations of this instance.
	runner *durable.Runner
	// migrationsRunning reports whether the runner loop runs.
	migrationsRunning atomic.Bool
	// readWarnAt is the time in Unix nanoseconds of the last warning about a
	// failed read of the stored result, or 0 when none was logged.
	readWarnAt atomic.Int64
	// runsMigrations is true when this process runs the worker component,
	// so its own loop must run for an approval. A process without the worker
	// leaves the migration to the loop of a worker process.
	runsMigrations bool
	limit          time.Duration
	setting        audit.ChainSetting
	mu             sync.Mutex
	rec            audit.ChainRecord
	readAt         time.Time
	read           bool
}

// Messages of the audit chain gate.
const (
	// chainMismatchMessage answers a request to an instance whose setting
	// differs from the stored state.
	chainMismatchMessage = "The audit chain setting of this server differs from the stored state. " +
		"Restart this server with the stored setting, or approve the change in the console."
	// chainPendingMessage answers a request while a change of the chain
	// waits for approval.
	chainPendingMessage = "An audit chain change waits for approval. Approve it in the console."
	// chainMigratingMessage answers a request while a migration of the
	// chain runs.
	chainMigratingMessage = "An audit chain migration runs. Requests stop until it is complete."
)

// chainApprovalStore appends an audit chain approval entry and starts its
// migration operation in one transaction, so only the approval whose start
// wins leaves an entry. It answers audit.ErrNoChainChange when no change
// waits and audit.ErrChainMigrating while a migration can run, both without
// an entry. When the fixed request names an ended operation, it starts the
// operation of retryKey instead. A failed start changes nothing.
type chainApprovalStore interface {
	// ApproveChainChange appends e and starts fixed, or the operation of
	// retryKey when fixed names an ended operation, in one transaction. It
	// returns the started operation.
	ApproveChainChange(
		ctx context.Context, e audit.Entry, fixed durable.StartRequest, retryKey string,
	) (durable.Operation, error)
}

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

	chain := &auditChain{
		store:             store,
		entries:           entries,
		server:            server,
		approve:           nil,
		namespaces:        cfg.namespaceStore,
		principals:        principals,
		logger:            cfg.logger,
		now:               cfg.now,
		observed:          observed,
		checkpoints:       checkpoints,
		checks:            checkStoreOf(cfg.namespaceStore),
		maint:             nil,
		restoreRunning:    nil,
		freshness:         nil,
		durableOps:        nil,
		runner:            nil,
		migrationsRunning: atomic.Bool{},
		readWarnAt:        atomic.Int64{},
		runsMigrations:    cfg.components.Has(ComponentWorker),
		limit:             maintenance.DefaultCacheLimit,
		setting:           cfg.auditChain,
		mu:                sync.Mutex{},
		rec:               initialChainRecord(),
		readAt:            time.Time{},
		read:              false,
	}

	if !bindDurableOps(chain, cfg.namespaceStore) {
		return nil, fmt.Errorf("namespace store keeps no durable operations: %w", ErrMissingOption)
	}

	if !bindChainApproval(chain, cfg.namespaceStore) {
		return nil, fmt.Errorf("namespace store keeps no atomic audit chain approval: %w", ErrMissingOption)
	}

	runner, err := newChainRunner(cfg, chain)
	if err != nil {
		return nil, err
	}

	chain.runner = runner

	chain.exportStoredResult()

	return chain, nil
}

// initialChainRecord answers the record of a chain that has not read the
// stored state: the state on, with no change waiting.
func initialChainRecord() audit.ChainRecord {
	return audit.ChainRecord{
		ChangedAt: time.Time{}, ChangedBy: "", State: audit.ChainStateOn, Pending: false,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	}
}

// checkStoreOf answers the check state port of s, or nil when s keeps none.
//
//nolint:ireturn // A nil interface means the store keeps no check state.
func checkStoreOf(s namespace.Store) audit.CheckStore {
	if c, ok := s.(audit.CheckStore); ok {
		return c
	}

	return nil
}

// newChainRunner answers the runner that claims migration operations for
// chain. Its claims go through the claim recorder of the worker watch, so a
// worker that runs only a migration still writes its heartbeat, and it takes
// claims with the configured lease period.
func newChainRunner(cfg *settings, chain *auditChain) (*durable.Runner, error) {
	ops := chain.durableOps
	if cfg.watch != nil {
		ops = trackingOps{Store: ops, watch: cfg.watch}
	}

	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store: ops,
		Owner: auditChainOwner(cfg),
		Kinds: []durable.KindSpec{
			{
				Kind:       auditChainKind,
				Version:    auditChainVersion,
				MinVersion: auditChainVersion,
				Steps:      []durable.Step{{Name: auditChainStep, Run: chain.runMigrationStep}},
			},
		},
		Lease:              cfg.operationLease,
		RenewEvery:         0,
		PollEvery:          0,
		MaxAttempts:        0,
		ExpiryLimit:        0,
		Backoff:            durable.Backoff{Base: 0, Max: 0},
		ClaimBackoff:       durable.Backoff{Base: 0, Max: 0},
		WriteTimeout:       0,
		Deferred:           nil,
		StopOnRenewalError: false,
		Logger:             cfg.logger,
		Rand:               nil,
	})
	if err != nil {
		return nil, fmt.Errorf("audit chain migration runner: %w", err)
	}

	return runner, nil
}

// durableStoreProvider is implemented by stores that keep durable
// operations next to their domain rows.
type durableStoreProvider interface {
	DurableOperationsPort() durable.Store
}

// bindDurableOps sets the durable operation store behind s on chain, and
// answers true. It answers false when s keeps none.
func bindDurableOps(chain *auditChain, s namespace.Store) bool {
	p, ok := s.(durableStoreProvider)
	if !ok || p == nil {
		return false
	}

	ops := p.DurableOperationsPort()
	if ops == nil {
		return false
	}

	chain.durableOps = ops

	return true
}

// bindChainApproval sets the atomic approval of s on chain, and answers
// true. It answers false when s keeps none.
func bindChainApproval(chain *auditChain, s namespace.Store) bool {
	approval, ok := s.(chainApprovalStore)
	if !ok || approval == nil {
		return false
	}

	chain.approve = approval

	return true
}

// auditChainOwner names this instance in the claims of the migration.
// The owner is for status only.
func auditChainOwner(cfg *settings) string {
	if cfg.instanceID != "" {
		return cfg.instanceID
	}

	return "audit-chain"
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
		Marks:   checkStoreOf(cfg.namespaceStore),
		Now:     cfg.now,
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

	migrating, err := c.migrationRunning(ctx)
	if err != nil {
		return serverapi.AuditChainStatus{}, err
	}

	return serverapi.AuditChainStatus{
		ChangedAt: rec.ChangedAt,
		ChangedBy: rec.ChangedBy,
		State:     rec.State,
		Setting:   c.setting,
		Pending:   rec.Pending,
		Migrating: migrating,
	}, nil
}

// Approve implements serverapi.AuditChain: it checks the MFA step of a
// change to off, refuses while a restore runs, enters maintenance mode,
// then appends the entry of the approval in the server scope and starts one
// durable operation for the change in one transaction. Only the approval
// whose start wins leaves an entry: a second approval while one operation
// can run answers ErrChainMigrating without an entry. The migration step
// runs only after the transaction commits, so it never runs before the
// entry exists.
func (c *auditChain) Approve(ctx context.Context, caller audit.Entry) error {
	if err := c.checkApprovalGate(ctx, caller); err != nil {
		return err
	}

	if err := c.requirePendingChange(ctx); err != nil {
		return err
	}

	if err := c.refuseWhileRestoring(ctx); err != nil {
		return err
	}

	if err := c.refuseWhileMigrating(ctx); err != nil {
		return err
	}

	change := audit.ChainChange{At: c.chainNow(), By: caller.Subject, To: c.setting.State()}

	if err := c.enterForApproval(ctx); err != nil {
		return err
	}

	input, err := json.Marshal(change)
	if err != nil {
		return fmt.Errorf("encode audit chain change: %w", err)
	}

	fixed, err := c.runner.StartRequest(auditChainKind, auditChainKey, input)
	if err != nil {
		return fmt.Errorf("approve audit chain change: %w", err)
	}

	_, err = c.approve.ApproveChainChange(
		ctx, audit.ChainSetEntry(caller, change), fixed, auditChainRetryKey(),
	)
	if err != nil {
		return fmt.Errorf("approve audit chain change: %w", err)
	}

	return nil
}

// checkApprovalGate refuses an approval while the migration loop stops,
// by a service principal for a change to off, or without a fresh MFA step.
func (c *auditChain) checkApprovalGate(ctx context.Context, caller audit.Entry) error {
	if c.runsMigrations && !c.migrationsRunning.Load() {
		return audit.ErrChainMigrationsStopped
	}

	if c.setting != audit.ChainOff {
		return nil
	}

	if err := c.refuseServiceCaller(ctx, caller.Subject); err != nil {
		return err
	}

	if c.freshness == nil {
		return nil
	}

	if err := c.freshness.RequireFresh(ctx, caller.Subject); err != nil {
		return fmt.Errorf("%w: %w", audit.ErrChainStepRequired, err)
	}

	return nil
}

// requirePendingChange answers nil when a change of the chain waits for
// approval, else the reason no approval can run.
func (c *auditChain) requirePendingChange(ctx context.Context) error {
	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return fmt.Errorf("read audit chain state: %w", err)
	}

	if !rec.Pending {
		return audit.ErrNoChainChange
	}

	return nil
}

// refuseWhileMigrating refuses an approval while a migration operation
// can run: a second approval while the first still runs answers
// ErrChainMigrating before it writes anything.
func (c *auditChain) refuseWhileMigrating(ctx context.Context) error {
	running, err := c.migrationRunning(ctx)
	if err != nil {
		return err
	}

	if running {
		return audit.ErrChainMigrating
	}

	return nil
}

// migrationRunning reports whether a migration operation can run now: one
// operation of the kind is pending or running.
func (c *auditChain) migrationRunning(ctx context.Context) (bool, error) {
	ops, err := c.durableOps.List(ctx, durable.Filter{
		Kind:   auditChainKind,
		States: []durable.State{durable.StatePending, durable.StateRunning},
		Limit:  1,
	})
	if err != nil {
		return false, fmt.Errorf("list audit chain migrations: %w", err)
	}

	return len(ops) > 0, nil
}

// auditChainRetryKey answers a new start key for an approval after a
// migration that already ended. The key is unique per call.
func auditChainRetryKey() string {
	return auditChainKey + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
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

	rec, err := c.startUpRecord(ctx)
	if err != nil {
		return err
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

	rec, err = c.markWaitingChange(ctx, rec)
	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
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

// startUpRecord answers the stored state for this start. It first drains
// the migration operations that an earlier instance left, with a timeout:
// a left operation may have stored the new state already, and without the
// drain this start would set the wait of its own change and the left
// operation would then clear it. After the timeout the background runner
// continues the migration, and the start sets no wait of its own while it
// runs.
func (c *auditChain) startUpRecord(ctx context.Context) (audit.ChainRecord, error) {
	// A process without the worker component claims no durable operation,
	// also not at start. It reads the stored record, and a worker process
	// continues a migration that an earlier instance left.
	if c.runsMigrations {
		drainCtx, cancel := context.WithTimeout(ctx, auditChainDrainTimeout)
		defer cancel()

		if err := c.drainMigrations(drainCtx); err != nil {
			if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(drainCtx.Err(), context.DeadlineExceeded) {
				return audit.ChainRecord{}, fmt.Errorf("audit chain: %w", err)
			}

			c.logger.WarnContext(ctx, "audit chain drain timed out; the background runner continues the migration")
		}
	}

	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return audit.ChainRecord{}, fmt.Errorf("audit chain: %w", err)
	}

	return rec, nil
}

// drainMigrations runs the migration operations that an earlier instance
// left before they ended, before this start reads the stored state. It
// stops at the first claim that finds no work, or at an operation of a
// newer binary, which stays for a newer instance.
func (c *auditChain) drainMigrations(ctx context.Context) error {
	for {
		err := c.runner.RunOnce(ctx)

		if errors.Is(err, durable.ErrNoWork) || errors.Is(err, durable.ErrNewerVersion) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("run pending audit chain migration: %w", err)
		}
	}
}

// markWaitingChange sets the wait of this start when its setting differs
// from the stored state and no wait exists. While a left migration still
// runs, it sets nothing: the migration owns the stored state until it ends,
// and the background runner continues it.
func (c *auditChain) markWaitingChange(ctx context.Context, rec audit.ChainRecord) (audit.ChainRecord, error) {
	if c.setting.State() == rec.State || rec.Pending {
		return rec, nil
	}

	running, err := c.migrationRunning(ctx)
	if err != nil {
		return rec, err
	}

	if running {
		return rec, nil
	}

	rec.Pending = true

	if err := c.store.SaveChainRecord(ctx, rec); err != nil {
		return rec, fmt.Errorf("save audit chain state: %w", err)
	}

	return rec, nil
}

// cancelWaitingChange clears a waiting change when the setting matches the
// stored state, the log agrees with it and no migration runs. A log that
// does not agree keeps the wait. It saves the cleared record and logs the
// end. It leaves maintenance mode only when the chain entered it itself and
// no restore runs, else it keeps maintenance mode.
func (c *auditChain) cancelWaitingChange(ctx context.Context, rec audit.ChainRecord) (audit.ChainRecord, error) {
	if !rec.Pending || c.setting.State() != rec.State {
		return rec, nil
	}

	running, err := c.migrationRunning(ctx)
	if err != nil {
		return rec, err
	}

	if running {
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
// agrees with the stored state. A scope that does not match counts the
// restore in dustpan_audit_restore_check_failures_total and logs an error;
// the restore still succeeds. A failed call counts in
// dustpan_audit_chain_check_errors_total, and the scheduled check checks
// the log again.
func (c *auditChain) checkRestoredLog(ctx context.Context, scopes []namespace.Name) {
	invalid, err := audit.CheckScopes(ctx, c.entries, scopes)
	if err != nil {
		c.logger.WarnContext(ctx, "restored audit log check could not check every scope",
			slog.String("error", err.Error()))

		if c.observed != nil {
			c.observed.IncAuditChainCheckError()
		}
	}

	if invalid == 0 {
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
		At:            at,
		Namespace:     audit.ServerScope,
		Subject:       "opendustpan serve",
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(identity.ActionServerManageAudit),
		Object:        audit.ServerScope.String(),
		ObjectID:      "",
		Version:       "",
		ValuePath:     "/audit/checkpoints/epoch/" + strconv.FormatInt(epoch, 10),
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// admitRestore refuses a restore while a change of the chain waits or
// migrates.
func (c *auditChain) admitRestore(ctx context.Context) error {
	running, err := c.migrationRunning(ctx)
	if err != nil {
		return err
	}

	if running {
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

// decodeMigrationChange answers the approved change that op carries.
func decodeMigrationChange(op durable.Operation) (audit.ChainChange, error) {
	var change audit.ChainChange

	if err := json.Unmarshal(op.Input, &change); err != nil {
		return audit.ChainChange{}, fmt.Errorf("decode audit chain change: %w", err)
	}

	return change, nil
}

// runMigrationStep runs the single step of a migration operation: it writes
// the start or end entry in each scope, stores the new state and leaves
// maintenance mode. A failure keeps the change waiting, so a person can
// approve it again. The step is safe to run again: the marker writes skip
// scopes that already agree, and a retry that finds the stored state
// already at the change does nothing.
func (c *auditChain) runMigrationStep(ctx context.Context, sc durable.StepContext) ([]byte, error) {
	change, err := decodeMigrationChange(sc.Operation)
	if err != nil {
		return nil, durable.Permanent(err)
	}

	if restoreErr := c.refuseWhileRestoring(ctx); restoreErr != nil {
		return nil, restoreErr
	}

	fences, err := c.fences(ctx)
	if err != nil {
		return nil, err
	}

	if err := audit.MigrateChain(ctx, c.store, fences, change); err != nil {
		return nil, fmt.Errorf("migrate audit chain: %w", err)
	}

	c.forget()

	if err := c.leaveAfterMigration(ctx); err != nil {
		return nil, err
	}

	c.logger.InfoContext(ctx, "audit chain migration is complete",
		slog.String("state", string(change.To)), slog.String("approved_by", change.By))

	return nil, nil
}

// leaveAfterMigration leaves maintenance mode after a migration. It leaves
// only when the chain entered maintenance mode itself, else it keeps the
// maintenance mode of the operator.
func (c *auditChain) leaveAfterMigration(ctx context.Context) error {
	if c.maint == nil {
		return nil
	}

	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		return fmt.Errorf("read audit chain state: %w", err)
	}

	if !rec.EnteredMaintenance {
		return nil
	}

	if err := c.maint.Leave(ctx); err != nil {
		return fmt.Errorf("leave maintenance mode: %w", err)
	}

	rec.EnteredMaintenance = false

	if err := c.store.SaveChainRecord(ctx, rec); err != nil {
		return fmt.Errorf("save audit chain state: %w", err)
	}

	return nil
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

// startMigrations claims and runs migration operations until ctx is done.
// The returned function waits until the loop stopped.
func (c *auditChain) startMigrations(ctx context.Context) func() {
	var wg sync.WaitGroup

	c.migrationsRunning.Store(true)

	wg.Go(func() {
		defer c.migrationsRunning.Store(false)

		c.runner.Run(ctx)
	})

	return wg.Wait
}

// forget drops the cached state, so the next request reads it again.
func (c *auditChain) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.read = false
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

// startCheckTicker checks the chain of every scope now and then every hour
// until ctx is done. The returned function waits until the check loop
// stopped. A server with a durable store runs the check as the schedule
// audit-chain-check and never calls it. A server without one has no
// schedule table, so each instance runs its own loop, as for the other jobs.
func (c *auditChain) startCheckTicker(ctx context.Context, every time.Duration) func() {
	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := time.NewTicker(every)
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
// a new checkpoint only when both match. While a change of the chain waits
// or a restore runs, it checks but signs nothing. A scope that does not
// match either check counts in dustpan_audit_chain_invalid. A failed read
// or checkpoint write counts only as a check error.
func (c *auditChain) checkChainAndCheckpoints(ctx context.Context, scopes []namespace.Name) {
	rec, err := c.store.ChainRecord(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.WarnContext(ctx, "audit chain check: read chain state", slog.String("error", err.Error()))
		}

		return
	}

	restoring := c.restoring(ctx)
	cycle := c.cycleEpoch(ctx)

	var (
		invalid int
		errs    []error
	)

	for _, scope := range scopes {
		ok, err := c.checkScope(ctx, scope, cycle)
		if err != nil {
			// A failed call checks nothing: it is not a mismatch.
			errs = append(errs, fmt.Errorf("check %s: %w", scope, err))

			continue
		}

		if !ok {
			invalid++

			continue
		}

		if rec.Pending || restoring {
			continue
		}

		_, err = cycle.write(ctx, c.checkpoints, scope, c.chainNow())

		switch {
		case errors.Is(err, audit.ErrCheckpointMismatch):
			c.logger.ErrorContext(ctx, "audit checkpoint does not match",
				slog.String("scope", scope.String()), slog.String("error", err.Error()))

			invalid++
		case err != nil:
			errs = append(errs, fmt.Errorf("check %s: checkpoint: %w", scope, err))
		default:
			// The checkpoint is stored, or the scope needs none.
		}
	}

	c.reportCheck(ctx, invalid, errors.Join(errs...))
}

// restoring reports whether a restore runs. A nil check means no restore
// runs. A failed check logs the error and skips the writes of this check.
func (c *auditChain) restoring(ctx context.Context) bool {
	if c.restoreRunning == nil {
		return false
	}

	running, err := c.restoreRunning(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.WarnContext(ctx, "audit chain check: read running restores", slog.String("error", err.Error()))
		}

		return true
	}

	return running
}

// cycleEpoch holds the epoch that one background cycle read once. known is
// false when that read failed: each scope then reads the epoch itself, so the
// error reaches the result of the scope as before.
type cycleEpoch struct {
	epoch int64
	known bool
}

// cycleEpoch reads the current epoch once for a whole cycle.
func (c *auditChain) cycleEpoch(ctx context.Context) cycleEpoch {
	epoch, err := c.checkpoints.Epoch(ctx)
	if err != nil {
		return cycleEpoch{epoch: 0, known: false}
	}

	return cycleEpoch{epoch: epoch, known: true}
}

// verify checks the checkpoints of scope with the epoch of the cycle.
func (e cycleEpoch) verify(
	ctx context.Context, cps *audit.Checkpoints, scope namespace.Name,
) (audit.CheckpointResult, error) {
	if !e.known {
		return cps.Verify(ctx, scope)
	}

	return cps.VerifyAt(ctx, scope, e.epoch)
}

// write signs a checkpoint of scope with the epoch of the cycle. The write
// itself reads the epoch again before it stores, so an epoch that changed
// during the cycle stores nothing.
func (e cycleEpoch) write(
	ctx context.Context, cps *audit.Checkpoints, scope namespace.Name, now time.Time,
) (bool, error) {
	if !e.known {
		return cps.Write(ctx, scope, now)
	}

	return cps.WriteAt(ctx, scope, now, e.epoch)
}

// checkScope checks the chain of scope and the stored checkpoints of
// scope. It answers false when either does not match, and an error when a
// check fails. It never signs: the caller signs the head after both match.
func (c *auditChain) checkScope(ctx context.Context, scope namespace.Name, cycle cycleEpoch) (bool, error) {
	chain, err := c.entries.Check(ctx, scope)
	if err != nil {
		return false, fmt.Errorf("chain: %w", err)
	}

	if !chain.Valid {
		return false, nil
	}

	result, err := cycle.verify(ctx, c.checkpoints, scope)
	if err != nil {
		return false, fmt.Errorf("checkpoints: %w", err)
	}

	if !result.Valid {
		c.logger.ErrorContext(ctx, "audit checkpoint does not match",
			slog.String("scope", scope.String()), slog.Int64("sequence", result.BadSequence),
			slog.String("reason", result.Reason))

		return false, nil
	}

	return true, nil
}

// reportCheck sets dustpan_audit_chain_invalid to invalid and logs the
// result of a check. A check that the caller stopped reports nothing. A
// check with err did not check every scope: it adds one to
// dustpan_audit_chain_check_errors_total and sets the gauge only when it
// found a scope that does not match, so a failed call never clears an
// alarm of an earlier check.
func (c *auditChain) reportCheck(ctx context.Context, invalid int, err error) {
	if err != nil && ctx.Err() != nil {
		return
	}

	if err != nil {
		c.logger.WarnContext(ctx, "audit chain check could not check every scope", slog.String("error", err.Error()))
	}

	if c.observed != nil {
		if err != nil {
			c.observed.IncAuditChainCheckError()
		}

		if err == nil || invalid > 0 {
			c.observed.SetAuditChainInvalid(invalid)
		}
	}

	if err == nil || invalid > 0 {
		c.storeResult(ctx, invalid)
	}

	if invalid > 0 {
		c.logger.ErrorContext(ctx, "audit chain check found scopes that do not match",
			slog.Int("scopes", invalid))
	}
}

// storeResult stores invalid as the result of the last check, so every
// instance exports it. A failed write logs a warning: the instance that
// reads the row then exports the result of an earlier check, and the next
// check writes it again.
func (c *auditChain) storeResult(ctx context.Context, invalid int) {
	if c.checks == nil {
		return
	}

	if err := c.checks.SaveCheckResult(ctx, invalid); err != nil && ctx.Err() == nil {
		c.logger.WarnContext(ctx, "audit chain check: store the result", slog.String("error", err.Error()))
	}
}

// exportStoredResult makes the gauge of the chain read the stored check
// result at scrape time, when the store keeps one.
func (c *auditChain) exportStoredResult() {
	if c.checks != nil && c.observed != nil {
		c.observed.SetAuditChainInvalidSource(c.storedInvalid)
	}
}

// auditChainReadWarnEvery is the least time between two warnings about a
// failed read of the stored result: Prometheus scrapes often, and a database
// fault would otherwise fill the log.
const auditChainReadWarnEvery = time.Minute

// warnReadFailure logs a failed read of the stored result, at most once for
// each auditChainReadWarnEvery.
func (c *auditChain) warnReadFailure(ctx context.Context, err error) {
	nowNano := c.chainNow().UnixNano()
	last := c.readWarnAt.Load()

	if last != 0 && nowNano-last < int64(auditChainReadWarnEvery) {
		return
	}

	if !c.readWarnAt.CompareAndSwap(last, nowNano) {
		return
	}

	c.logger.WarnContext(ctx, "audit chain check: read the stored result; export it as invalid",
		slog.String("error", err.Error()))
}

// storedInvalid answers the count of scopes that did not match, for
// dustpan_audit_chain_invalid: the larger of the count in the last stored
// result and the count of marks that stand in the current epoch. The stored
// result is last-writer-wins, so an older check that ends late can store a
// lower count. The mark is durable, so it keeps the alarm. It fails closed: a row that
// cannot be read counts as one scope that does not match, so a database
// fault never reads as a good chain. Each failed read adds one to
// dustpan_audit_chain_read_errors_total, which tells a fault from a
// mismatch, and the read logs a warning at a limited rate.
func (c *auditChain) storedInvalid() int {
	ctx, cancel := context.WithTimeout(context.Background(), auditChainReadTimeout)
	defer cancel()

	state, err := c.checks.CheckState(ctx)
	if err != nil {
		if c.observed != nil {
			c.observed.IncAuditChainReadError()
		}

		c.warnReadFailure(ctx, err)

		return 1
	}

	return max(state.Invalid, c.standingMarks(ctx, state))
}

// standingMarks answers the count of marks that stand in the current epoch of
// the checkpoints. A new epoch ends a mark, so the count uses the current
// epoch and not the newest epoch of a mark. It reads the epoch only while a
// mark exists. When the epoch cannot be read, or the server has no
// checkpoints, it counts the marks of the newest epoch: the alarm stays on.
func (c *auditChain) standingMarks(ctx context.Context, state audit.CheckState) int {
	if len(state.Marks) == 0 || c.checkpoints == nil {
		return state.Standing()
	}

	epoch, err := c.checkpoints.Epoch(ctx)
	if err != nil {
		return state.Standing()
	}

	return state.StandingFrom(epoch)
}

// bindAuditChain gives chain the services that are built after it: the
// maintenance service and the check for a running restore.
func bindAuditChain(chain *auditChain, maint *maintenance.Service, restores *backup.RestoreService, cfg *settings) {
	if chain == nil {
		return
	}

	chain.maint = maint

	switch registry, ok := cfg.namespaceStore.(restoreLister); {
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

// restoreLister lists the restore rows of a store.
type restoreLister interface {
	ListRestores(ctx context.Context) ([]backup.RestoreRecord, error)
}

// restoreRowRunning reports whether registry holds a restore that is still
// checking or applying.
func restoreRowRunning(ctx context.Context, registry restoreLister) (bool, error) {
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

// auditChainPeriod answers the period of the audit chain check: the
// configured one, or an hour when none is set.
func (s *Server) auditChainPeriod() time.Duration {
	if s.auditChainEvery > 0 {
		return s.auditChainEvery
	}

	return auditChainCheckInterval
}

// startAuditChain starts the check, when the server has a chain, and the
// migration loop of the chain until ctx is done. The returned function waits
// until both stopped.
func (s *Server) startAuditChain(ctx context.Context) func() {
	if s.chain == nil {
		return func() {}
	}

	waitCheck := s.startChainChecks(ctx)
	waitMigrations := s.chain.startMigrations(ctx)

	return func() {
		waitCheck()
		waitMigrations()
	}
}

// startChainChecks starts the audit chain checks of this instance. With a
// durable store the schedule audit-chain-check runs the check on one instance
// for each tick, and this instance checks once at start, so a restarted
// cluster does not wait one interval for a result. When the schedule runs at
// the same time on another instance, the two checks are safe: each stores the
// same kind of result, and a stored mark stays. Without a durable store there
// is no schedule table, so each instance runs its own loop. The returned
// function waits until the checks stopped.
func (s *Server) startChainChecks(ctx context.Context) func() {
	if s.durable == nil {
		return s.chain.startCheckTicker(ctx, s.auditChainPeriod())
	}

	var wg sync.WaitGroup

	wg.Go(func() { s.chain.check(ctx) })

	return wg.Wait
}

// admits reports whether the audit chain gate lets requests through: the
// stored chain state matches the setting of the process and no change waits.
func (c *auditChain) admits(ctx context.Context) bool {
	rec := c.current(ctx)

	return c.setting.State() == rec.State && !rec.Pending
}
