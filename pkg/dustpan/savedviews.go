package dustpan

import (
	"fmt"

	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// WithSavedViewStore sets the store of saved views. With it, and with a
// namespace store that keeps principals, the server serves the saved view
// routes under /api/dustpan/v1/namespaces/{namespace}/views. Without it, the
// server serves no saved view route.
func WithSavedViewStore(st savedview.Store) Option {
	return func(o *settings) { o.savedViewStore = st }
}

// newViewsConfig returns the saved view config of the inventory API, or nil
// when no saved view store is set. The owner of a view is the ID of a
// principal record, so the namespace store must keep principals.
func newViewsConfig(cfg *settings) (*inventoryapi.ViewsConfig, error) {
	if cfg.savedViewStore == nil {
		return nil, nil //nolint:nilnil // No saved view routes: no saved view store is set.
	}

	principals, ok := cfg.namespaceStore.(principal.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no principals, so it cannot own saved views: %w", ErrMissingOption)
	}

	service, err := savedview.NewService(savedview.Config{
		Store: cfg.savedViewStore, Namespaces: cfg.namespaceStore, Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("saved view service: %w", err)
	}

	idem, err := idempotencyMiddleware(cfg)
	if err != nil {
		return nil, err
	}

	return &inventoryapi.ViewsConfig{Service: service, Principals: principals, Idempotency: idem}, nil
}
