package dustpan

import "opendustpan.org/opendustpan/internal/api/pulumi"

// newStackPlacer returns the Placer of the Pulumi adapter: the placement rules
// choose the collection of a new stack, and the collection service puts the
// stack in it. It returns nil when the store keeps no placement rules or no
// collections. A nil Placer turns placement off.
func newStackPlacer(svc services) *pulumi.Placer {
	if svc.placements == nil || svc.collections == nil {
		return nil
	}

	return &pulumi.Placer{Rules: svc.placements, Collections: svc.collections}
}
