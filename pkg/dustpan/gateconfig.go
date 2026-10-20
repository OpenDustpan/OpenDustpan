package dustpan

import (
	"fmt"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// newGateConfigService returns the service of the approval defaults and the
// approval rules over the namespace store, or nil when the store keeps no
// gate configuration. Both SQL adapters keep it. The server then mounts no
// route of the gate configuration.
func newGateConfigService(cfg *settings, namespaces *namespace.Service) (*gate.ConfigService, error) {
	store, ok := cfg.namespaceStore.(gate.Store)
	if !ok {
		return nil, nil //nolint:nilnil // A store without gate configuration is a valid setup: the routes stay unmounted.
	}

	svc, err := gate.NewConfigService(gate.ConfigServiceConfig{Store: store, Namespaces: namespaces, Now: cfg.now})
	if err != nil {
		return nil, fmt.Errorf("gate config service: %w", err)
	}

	return svc, nil
}

// gatesPort returns svc as the port of the server API, and a nil port for a
// nil service: a typed nil in the interface would mount the routes.
//
//nolint:ireturn // The server API takes the port, and nil must stay an untyped nil.
func gatesPort(svc *gate.ConfigService) serverapi.GateConfig {
	if svc == nil {
		return nil
	}

	return svc
}
