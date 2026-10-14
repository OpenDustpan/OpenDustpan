package dustpan

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// auditChainCheckInterval is the time between two scheduled chain checks.
const auditChainCheckInterval = time.Hour

// WithAuditChain sets the hash chain of the audit log (ADR 60 decisions 17
// and 19). audit.ChainAuto, the default, keeps the chain on when the
// installation already holds chained entries, and off otherwise.
// audit.ChainOn and audit.ChainOff decide alone. Run resolves the setting
// before it serves, writes an audit entry when the chain changes, and logs
// a warning while chained entries exist and the chain is off. While the
// chain is on, the server checks the chain of every scope at start and
// every hour, and sets dustpan_audit_chain_invalid.
func WithAuditChain(setting audit.ChainSetting) Option {
	return func(o *settings) { o.auditChain = setting }
}

// WithAuditCheckpoints turns signed checkpoints of the audit log on (ADR 60
// decision 18). They are off by default. They need the chain on and a blob
// store: Run fails when the chain resolves off. While they are on, each
// scheduled chain check signs the last hash of each scope whose chain
// matches with signer, stores the checkpoint in the blob store, and checks
// every stored checkpoint. A scope with a checkpoint that does not match
// counts in dustpan_audit_chain_invalid.
func WithAuditCheckpoints(signer *audit.Signer) Option {
	return func(o *settings) { o.auditSigner = signer }
}

// ErrCheckpointsNeedChain reports signed checkpoints with the chain off.
var ErrCheckpointsNeedChain = errors.New("audit checkpoints need the audit chain on: set --audit-chain=on")

// chainDeps holds the ports of the chain switch: the switch, the audit
// store, the check of a restored log, the namespace list and the signed
// checkpoints. checker is nil when the store cannot check a restored log,
// and checkpoints is nil while signed checkpoints are off.
type chainDeps struct {
	sw          audit.ChainSwitch
	entries     audit.Store
	checker     audit.RestoreChecker
	namespaces  namespace.Store
	checkpoints *audit.Checkpoints
	setting     audit.ChainSetting
}

// newChainDeps answers the ports of the chain switch from the namespace
// store, or nil when the store has no chain switch and the setting is
// auto: such a store keeps its own behavior. An explicit setting on such a
// store fails.
func newChainDeps(cfg *settings) (*chainDeps, error) {
	sw, okSwitch := cfg.namespaceStore.(audit.ChainSwitch)
	entries, okEntries := cfg.namespaceStore.(audit.Store)

	if !okSwitch || !okEntries {
		if cfg.auditChain != audit.ChainAuto || cfg.auditSigner != nil {
			return nil, fmt.Errorf("namespace store cannot turn the audit chain on or off: %w", ErrMissingOption)
		}

		return nil, nil //nolint:nilnil // No switch without an error means the store keeps its own chain.
	}

	checker, ok := cfg.namespaceStore.(audit.RestoreChecker)
	if !ok {
		checker = nil
	}

	checkpoints, err := newCheckpoints(cfg)
	if err != nil {
		return nil, err
	}

	return &chainDeps{
		sw:          sw,
		entries:     entries,
		checker:     checker,
		namespaces:  cfg.namespaceStore,
		checkpoints: checkpoints,
		setting:     cfg.auditChain,
	}, nil
}

// newCheckpoints answers the signed checkpoints of cfg, or nil while they
// are off. They need a blob store and a store that reads one entry at a
// sequence.
func newCheckpoints(cfg settings) (*audit.Checkpoints, error) {
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

// resolveAuditChain resolves the chain setting before the server serves.
func (s *Server) resolveAuditChain(ctx context.Context) error {
	if s.chain == nil {
		return nil
	}

	ports := audit.ChainPorts{Switch: s.chain.sw, Logger: s.logger}

	on, err := audit.ResolveChain(ctx, ports, s.chain.setting, s.now)
	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
	}

	s.chainOn = on

	if s.chain.checkpoints == nil {
		return nil
	}

	if !on {
		return ErrCheckpointsNeedChain
	}

	signer := s.chain.checkpoints.Signer()
	s.logger.InfoContext(ctx, "audit checkpoints are on",
		slog.String("key_id", signer.KeyID()),
		slog.String("public_key", base64.StdEncoding.EncodeToString(signer.PublicKey())))

	return nil
}

// startAuditChainCheck checks the chain of every scope now and then every
// hour until ctx is done, while the chain is on. The returned function
// waits until the check loop stopped.
func (s *Server) startAuditChainCheck(ctx context.Context) func() {
	if s.chain == nil || !s.chainOn {
		return func() {}
	}

	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := time.NewTicker(auditChainCheckInterval)
		defer ticker.Stop()

		for {
			s.checkAuditChain(ctx)

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})

	return wg.Wait
}

// checkAuditChain checks every namespace and the server scope once, and
// sets dustpan_audit_chain_invalid. A failed check logs the error, never
// an entry.
func (s *Server) checkAuditChain(ctx context.Context) {
	scopes, err := s.chain.scopes(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "audit chain check: list namespaces", slog.String("error", err.Error()))
		}

		return
	}

	invalid, err := s.checkScopes(ctx, scopes)
	if err != nil && ctx.Err() != nil {
		return
	}

	if err != nil {
		s.logger.WarnContext(ctx, "audit chain check failed", slog.String("error", err.Error()))
	}

	s.observed.SetAuditChainInvalid(invalid)

	if invalid > 0 {
		s.logger.ErrorContext(ctx, "audit chain check found scopes that do not match",
			slog.Int("scopes", invalid))
	}
}

// scopes answers every namespace and the server scope: the scopes of a
// check of the whole log.
func (c *chainDeps) scopes(ctx context.Context) ([]namespace.Name, error) {
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

// restoreChain holds what the after-apply step of the chain needs besides
// its ports: the logger, the clock and the metrics.
type restoreChain struct {
	logger   *slog.Logger
	now      func() time.Time
	observed *metrics.ServerMetrics
}

// recheckChainAfterRestore returns the after-apply step of a restore for the
// audit chain (RFC 22): the restore keeps the chain record of this
// installation. The step checks the restored log, then writes a switch
// entry for that state that marks the restore, notes the chained log and
// logs the warning of a start while the chain is off (see
// audit.RecheckChain). It never changes the state. When the check of the
// restored log fails, it counts the restore in
// dustpan_audit_restore_check_failures_total, and the restore still
// succeeds. Without a chain switch it does nothing.
func recheckChainAfterRestore(chain *chainDeps, rc restoreChain) restoreStep {
	return func(ctx context.Context, restore backup.AppliedRestore) error {
		if chain == nil {
			return nil
		}

		r := audit.ChainRestore{
			Checker:   chain.checker,
			Now:       rc.now,
			BackupID:  restore.BackupID.String(),
			Requester: restore.RequestedBy,
			Scopes:    nil,
		}

		if chain.checker != nil {
			scopes, err := chain.scopes(ctx)
			if err != nil {
				return fmt.Errorf("audit chain after restore: %w", err)
			}

			r.Scopes = scopes
		}

		logger := rc.logger
		if logger != nil {
			logger = logger.With(slog.String("restore_id", restore.ID.String()))
		}

		ports := audit.ChainPorts{Switch: chain.sw, Logger: logger}

		invalid, err := audit.RecheckChain(ctx, ports, r)
		if invalid > 0 && rc.observed != nil {
			rc.observed.IncAuditRestoreCheckFailure()
		}

		if err != nil {
			return fmt.Errorf("audit chain after restore: %w", err)
		}

		return nil
	}
}

// checkScopes runs the chain check of each scope, and the checkpoint step
// while checkpoints are on. It answers the count of scopes that fail. A
// scope whose check answers an error counts as failed too, and the error
// joins the answer.
func (s *Server) checkScopes(ctx context.Context, scopes []namespace.Name) (int, error) {
	if s.chain.checkpoints == nil {
		return audit.CheckScopes(ctx, s.chain.entries, scopes)
	}

	var (
		invalid int
		errs    []error
	)

	for _, scope := range scopes {
		ok, err := s.checkScope(ctx, scope)
		if err != nil {
			errs = append(errs, fmt.Errorf("check %s: %w", scope, err))
		}

		if !ok {
			invalid++
		}
	}

	return invalid, errors.Join(errs...)
}

// checkScope checks the chain of scope, then the stored checkpoints of
// scope, and signs a checkpoint of its last hash only when both match. It
// never signs a head while the chain or an older checkpoint does not match.
func (s *Server) checkScope(ctx context.Context, scope namespace.Name) (bool, error) {
	chain, err := s.chain.entries.Check(ctx, scope)
	if err != nil {
		return false, fmt.Errorf("chain: %w", err)
	}

	if !chain.Valid {
		return false, nil
	}

	cps := s.chain.checkpoints

	result, err := cps.Verify(ctx, scope)
	if err != nil {
		return false, fmt.Errorf("checkpoints: %w", err)
	}

	if !result.Valid {
		s.logger.ErrorContext(ctx, "audit checkpoint does not match",
			slog.String("scope", scope.String()), slog.Int64("sequence", result.BadSequence))

		return false, nil
	}

	if _, err := cps.Write(ctx, scope, s.now()); err != nil {
		return false, fmt.Errorf("checkpoint: %w", err)
	}

	return true, nil
}

// newEpochAfterRestore returns the after-apply step of a restore for the
// signed audit checkpoints: it starts a new epoch, because the restored log
// can lose entries that older checkpoints sign, and writes one entry of the
// kind change in @server. Without checkpoints it does nothing.
func newEpochAfterRestore(
	checkpoints *audit.Checkpoints, server audit.ServerStore, now func() time.Time,
) restoreStep {
	return func(ctx context.Context, _ backup.AppliedRestore) error {
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
		At:         at,
		Namespace:  audit.ServerScope,
		Subject:    "opendustpan serve",
		IDPSubject: "",
		TokenID:    "",
		Action:     string(identity.ActionServerManageAudit),
		Object:     audit.ServerScope.String(),
		Version:    "",
		ValuePath:  "/audit/checkpoints/epoch/" + strconv.FormatInt(epoch, 10),
		ValueCount: nil,
		SourceIP:   "",
		UserAgent:  "",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}
}

// checkpointEpochsOf answers the checkpoint epochs of cfg for the server
// API, or nil while signed checkpoints are off.
//
//nolint:ireturn // A nil *audit.Checkpoints in the interface is not nil, so the route would call it; answer a nil interface.
func checkpointEpochsOf(cfg settings) serverapi.CheckpointEpochs {
	checkpoints, err := newCheckpoints(cfg)
	if err != nil || checkpoints == nil {
		return nil
	}

	return checkpoints
}

// checkpointsOf answers the signed checkpoints of chain, or nil.
func checkpointsOf(chain *chainDeps) *audit.Checkpoints {
	if chain == nil {
		return nil
	}

	return chain.checkpoints
}
