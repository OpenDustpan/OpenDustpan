package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/secret"
)

// newItemSealer builds the sealer of the item keys of the Pulumi updates. The
// item key record of an update commits in the transaction that creates the
// update, so the update store must also be the item key index
// (secret.ItemKeyIndex), as the SQLite and PostgreSQL stores are. A store that
// is not returns ErrMissingOption, so the server never stores an event
// payload as the CLI sent it.
func newItemSealer(cfg *settings) (*secret.ItemKeySealer, error) {
	index, ok := cfg.updateStore.(secret.ItemKeyIndex)
	if !ok {
		return nil, fmt.Errorf(
			"%w: WithUpdateStore store must also implement secret.ItemKeyIndex, for the item keys of updates",
			ErrMissingOption,
		)
	}

	refusals, err := blobRefusals(cfg, secret.ItemKeyFamily())
	if err != nil {
		return nil, fmt.Errorf("item key blobs: %w", err)
	}

	blobs, err := secret.NewItemKeyBlobs(cfg.blobStore, refusals)
	if err != nil {
		return nil, fmt.Errorf("item key blobs: %w", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: index, Blobs: blobs, Wrapper: cfg.secretWrapper, Namespaces: cfg.namespaceStore, Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("item key sealer: %w", err)
	}

	return sealer, nil
}
