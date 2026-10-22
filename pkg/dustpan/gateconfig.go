package dustpan

import (
	"context"
	"fmt"
	"strings"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// newGateConfigService returns the service of the approval defaults and the
// approval rules over the namespace store, or nil when the store keeps no
// gate configuration. Both SQL adapters keep it. The server then mounts no
// route of the gate configuration.
func newGateConfigService(
	cfg *settings, namespaces *namespace.Service, principals *principal.Service,
) (*gate.ConfigService, error) {
	store, ok := cfg.namespaceStore.(gate.Store)
	if !ok {
		return nil, nil //nolint:nilnil // A store without gate configuration is a valid setup: the routes stay unmounted.
	}

	svc, err := gate.NewConfigService(gate.ConfigServiceConfig{
		Store: store, Namespaces: namespaces, Approvers: groupDirectory{groups: principals}, Now: cfg.now,
	})
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

// groupDirectory answers the group questions of the gate configuration from
// the principal service. A server that keeps no principals has no group, so
// it refuses every group name: fail closed.
type groupDirectory struct {
	groups *principal.Service
}

var _ gate.ApproverDirectory = groupDirectory{groups: nil}

// GroupMembers implements gate.ApproverDirectory.
func (d groupDirectory) GroupMembers(ctx context.Context, name string) (int64, error) {
	if err := principal.ValidateGroupName(name); err != nil {
		return 0, fmt.Errorf("%w: %w", gate.ErrInvalid, err)
	}

	if d.groups == nil {
		return 0, fmt.Errorf("%w: group %q: this server keeps no groups", gate.ErrNotFound, name)
	}

	page, err := d.groups.ListGroups(ctx, principal.PageRequest{Prefix: name, Cursor: "", Limit: 0})
	if err != nil {
		return 0, fmt.Errorf("list groups: %w", err)
	}

	for _, g := range page.Items {
		if strings.EqualFold(g.Name, name) {
			return g.MemberCount, nil
		}
	}

	return 0, fmt.Errorf("%w: group %q", gate.ErrNotFound, name)
}
