package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// OutputSharingSettingsStore is a namespace store that also keeps the output
// sharing setting of each namespace. The SQLite and the PostgreSQL stores
// implement it. With such a store, the server serves the setting under
// /api/dustpan/v1/namespaces/{namespace}/output-sharing.
type OutputSharingSettingsStore interface {
	// OutputSharingSettings returns the port of the setting.
	OutputSharingSettings() outputshare.Settings
}

// newOutputSharingService builds the service of the output sharing setting on
// the namespace store when that store also keeps the setting. A custom store
// without it gets no service, and the server serves no setting route.
func newOutputSharingService(cfg *settings) (*outputsharesvc.Service, error) {
	source, ok := cfg.namespaceStore.(OutputSharingSettingsStore)
	if !ok {
		return nil, nil //nolint:nilnil // No setting service: the store keeps no setting.
	}

	svc, err := outputsharesvc.New(outputsharesvc.Config{
		Settings:   source.OutputSharingSettings(),
		Namespaces: cfg.namespaceStore,
		Authorizer: cfg.authorizer,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("output sharing service: %w", err)
	}

	return svc, nil
}
