package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/scopeaudit"
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

// stackRefusals returns the recorder of the refusals of the stack blob scope.
// See blobRefusals.
func stackRefusals(cfg *settings) (*scopeaudit.Audit, error) {
	return blobRefusals(cfg, stack.BlobFamily())
}

// blobRefusals returns the recorder that writes each refusal of a blob key
// outside the scope of a call to the audit log, as a security entry of the
// scope @server, for the blobs of family. The namespace store must also append
// entries of that scope (audit.ServerStore). A store that does not returns
// ErrMissingOption, so the server never starts without a place for the
// refusals.
func blobRefusals(cfg *settings, family scopeaudit.Family) (*scopeaudit.Audit, error) {
	entries, ok := cfg.namespaceStore.(audit.ServerStore)
	if !ok {
		return nil, fmt.Errorf(
			"%w: WithNamespaceStore store must also implement audit.ServerStore, for the security entries of blobs",
			ErrMissingOption,
		)
	}

	recorder, err := scopeaudit.New(entries, family, cfg.now)
	if err != nil {
		return nil, fmt.Errorf("blob refusals: %w", err)
	}

	return recorder, nil
}
