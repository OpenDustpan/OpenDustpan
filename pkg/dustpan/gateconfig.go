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

// GroupMembers implements gate.ApproverDirectory. It reads the members of the
// group in pages and counts the active memberships of principals that are not
// disabled, and of those the persons.
func (d groupDirectory) GroupMembers(ctx context.Context, name string) (gate.MemberCounts, error) {
	none := gate.MemberCounts{Enabled: 0, Persons: 0}

	if err := principal.ValidateGroupName(name); err != nil {
		return none, fmt.Errorf("%w: %w", gate.ErrInvalid, err)
	}

	if d.groups == nil {
		return none, fmt.Errorf("%w: group %q: this server keeps no groups", gate.ErrNotFound, name)
	}

	group, err := d.group(ctx, name)
	if err != nil {
		return none, err
	}

	return d.count(ctx, group)
}

// count reads the members of group in pages and counts the active memberships
// of principals that are not disabled, and of those the persons.
func (d groupDirectory) count(ctx context.Context, group principal.Group) (gate.MemberCounts, error) {
	counts := gate.MemberCounts{Enabled: 0, Persons: 0}
	cursor := ""

	for {
		page, err := d.groups.ListMembers(ctx, group.ID, principal.PageRequest{
			Prefix: "", Cursor: cursor, Limit: principal.MaxLimit,
		})
		if err != nil {
			return gate.MemberCounts{Enabled: 0, Persons: 0},
				fmt.Errorf("list the members of group %q: %w", group.Name, err)
		}

		for i := range page.Items {
			m := &page.Items[i]
			if m.State != principal.MembershipStateActive || !m.Principal.DisabledAt.IsZero() {
				continue
			}

			counts.Enabled++

			if m.Principal.Kind == principal.KindPerson {
				counts.Persons++
			}
		}

		if page.NextCursor == "" {
			return counts, nil
		}

		cursor = page.NextCursor
	}
}

// group returns the group called name without regard to case, or
// gate.ErrNotFound. The groups are ordered by the lower-case name and the
// prefix starts the range, so the exact name is the first row of its range:
// every longer name with the same prefix sorts after it.
func (d groupDirectory) group(ctx context.Context, name string) (principal.Group, error) {
	page, err := d.groups.ListGroups(ctx, principal.PageRequest{Prefix: name, Cursor: "", Limit: 0})
	if err != nil {
		return principal.Group{}, fmt.Errorf("list groups: %w", err)
	}

	for _, g := range page.Items {
		if strings.EqualFold(g.Name, name) {
			return g, nil
		}
	}

	return principal.Group{}, fmt.Errorf("%w: group %q", gate.ErrNotFound, name)
}
