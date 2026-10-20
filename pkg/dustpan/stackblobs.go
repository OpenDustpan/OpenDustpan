package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/stack"
)

// attachStackRefusals builds the one recorder of the refusals of the stack blob
// scope and keeps it in cfg. The update service and the journal writer share
// it.
func attachStackRefusals(cfg *settings) error {
	recorder, err := stackRefusals(cfg)
	if err != nil {
		return err
	}

	cfg.stackRefusals = recorder

	return nil
}

// stackRefusals returns the recorder that writes each refusal of a stack blob
// key outside the scope of a stack to the audit log, as a security entry of
// the scope @server. The namespace store must also append entries of that
// scope (audit.ServerStore). A store that does not returns ErrMissingOption,
// so the server never starts without a place for the refusals. The caller
// builds the recorder once and gives it to each service that reads or writes
// stack blobs.
func stackRefusals(cfg *settings) (*stack.AuditOutOfScope, error) {
	entries, ok := cfg.namespaceStore.(audit.ServerStore)
	if !ok {
		return nil, fmt.Errorf(
			"%w: WithNamespaceStore store must also implement audit.ServerStore, for the security entries of stack blobs",
			ErrMissingOption,
		)
	}

	recorder, err := stack.NewAuditOutOfScope(entries, cfg.now)
	if err != nil {
		return nil, fmt.Errorf("stack blob refusals: %w", err)
	}

	return recorder, nil
}
