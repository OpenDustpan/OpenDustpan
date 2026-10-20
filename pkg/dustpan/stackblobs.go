package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/stack"
)

// stackRefusals returns the recorder that writes each refusal of a stack blob
// key outside the scope of a namespace to the audit log, as a security entry.
// The namespace store must also append entries of the installation scope. The
// server fails to start without it, so a refusal is never lost.
func stackRefusals(cfg *settings) (*stack.AuditOutOfScope, error) {
	entries, ok := cfg.namespaceStore.(audit.ServerStore)
	if !ok {
		return nil, fmt.Errorf("%w: audit store for security entries", stack.ErrMissingDependency)
	}

	recorder, err := stack.NewAuditOutOfScope(entries, audit.ChannelAPI, cfg.now)
	if err != nil {
		return nil, fmt.Errorf("stack blob refusals: %w", err)
	}

	return recorder, nil
}
