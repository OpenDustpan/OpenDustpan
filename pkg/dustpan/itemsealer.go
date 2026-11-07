package dustpan

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
)

// newItemSealer builds the sealer of the item keys of the Pulumi updates. The
// item key record of an update commits in the transaction that creates the
// update, so the update store must also be the item key index
// (secret.ItemKeyIndex), as the SQLite and PostgreSQL stores are. A store that
// is not returns ErrMissingOption, so the server never stores an event
// payload as the CLI sent it.
func newItemSealer(cfg *settings) (*secret.ItemKeySealer, error) {
	return newItemSealerOf(&itemSealerInputs{
		updates: cfg.updateStore, blobs: cfg.blobStore, namespaces: cfg.namespaceStore,
		wrapper: cfg.secretWrapper, now: cfg.now,
	})
}

// itemSealerInputs are the values that newItemSealerOf reads.
type itemSealerInputs struct {
	// updates must also be the item key index.
	updates    stack.UpdateStore
	blobs      blob.Store
	namespaces namespace.Store
	wrapper    secret.Wrapper
	now        func() time.Time
}

// newItemSealerOf is newItemSealer with its inputs as arguments.
func newItemSealerOf(in *itemSealerInputs) (*secret.ItemKeySealer, error) {
	index, ok := in.updates.(secret.ItemKeyIndex)
	if !ok {
		return nil, fmt.Errorf(
			"%w: WithUpdateStore store must also implement secret.ItemKeyIndex, for the item keys of updates",
			ErrMissingOption,
		)
	}

	refusals, err := blobRefusalsOf(in.namespaces, secret.ItemKeyFamily(), in.now)
	if err != nil {
		return nil, fmt.Errorf("item key blobs: %w", err)
	}

	blobs, err := secret.NewItemKeyBlobs(in.blobs, refusals, refusals)
	if err != nil {
		return nil, fmt.Errorf("item key blobs: %w", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: index, Blobs: blobs, Wrapper: in.wrapper, Namespaces: in.namespaces, Now: in.now,
	})
	if err != nil {
		return nil, fmt.Errorf("item key sealer: %w", err)
	}

	return sealer, nil
}
