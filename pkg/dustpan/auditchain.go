package dustpan

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
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

// chainDeps holds the ports of the chain switch: the switch, the audit
// store and the namespace list.
type chainDeps struct {
	sw         audit.ChainSwitch
	entries    audit.Store
	namespaces namespace.Store
	setting    audit.ChainSetting
}

// newChainDeps answers the ports of the chain switch from the namespace
// store, or nil when the store has no chain switch and the setting is
// auto: such a store keeps its own behavior. An explicit setting on such a
// store fails.
func newChainDeps(cfg *settings) (*chainDeps, error) {
	sw, okSwitch := cfg.namespaceStore.(audit.ChainSwitch)
	entries, okEntries := cfg.namespaceStore.(audit.Store)

	if !okSwitch || !okEntries {
		if cfg.auditChain != audit.ChainAuto {
			return nil, fmt.Errorf("namespace store cannot turn the audit chain on or off: %w", ErrMissingOption)
		}

		return nil, nil //nolint:nilnil // No switch without an error means the store keeps its own chain.
	}

	return &chainDeps{
		sw:         sw,
		entries:    entries,
		namespaces: cfg.namespaceStore,
		setting:    cfg.auditChain,
	}, nil
}

// resolveAuditChain resolves the chain setting before the server serves.
func (s *Server) resolveAuditChain(ctx context.Context) error {
	if s.chain == nil {
		return nil
	}

	ports := audit.ChainPorts{Switch: s.chain.sw, Logger: s.logger}

	on, err := audit.ResolveChain(ctx, ports, s.chain.setting, s.now())
	if err != nil {
		return fmt.Errorf("audit chain: %w", err)
	}

	s.chainOn = on

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
	all, err := s.chain.namespaces.ListNamespaces(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "audit chain check: list namespaces", slog.String("error", err.Error()))
		}

		return
	}

	scopes := make([]namespace.Name, 0, len(all)+1)
	for _, ns := range all {
		scopes = append(scopes, ns.Name)
	}

	scopes = append(scopes, audit.ServerScope)

	invalid, err := audit.CheckScopes(ctx, s.chain.entries, scopes)
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

// recheckChainAfterRestore returns the after-apply step of a restore for the
// audit chain (RFC 22): the restore keeps the chain record of this
// installation, and the step writes a switch entry for that state, notes
// the chained log and logs the warning of a start while the chain is off
// (see audit.RecheckChain). It never changes the state.
// Without a chain switch it does nothing.
func recheckChainAfterRestore(
	chain *chainDeps, logger *slog.Logger, now func() time.Time,
) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if chain == nil {
			return nil
		}

		ports := audit.ChainPorts{Switch: chain.sw, Logger: logger}
		if err := audit.RecheckChain(ctx, ports, now()); err != nil {
			return fmt.Errorf("audit chain after restore: %w", err)
		}

		return nil
	}
}
