package access

import (
	"context"

	"opendustpan.org/opendustpan/pkg/identity"
)

// CollectionUnitCapForTest exposes the resolution of the cap of the export
// of a collection to the black-box tests of the package.
func CollectionUnitCapForTest(ctx context.Context, settings Settings) int {
	return collectionUnitCap(ctx, settings)
}

// BoundCollectionUnitCapForTest exposes the bound of the cap of the export of
// a collection to the black-box tests of the package.
func BoundCollectionUnitCapForTest(n int) int {
	return boundCollectionUnitCap(n)
}

// HousekeepingRevokeCheckForTest exposes the check that a revoke under the
// housekeeping revoke rule runs in its write, so a test can call it with no
// test of the service before it.
func HousekeepingRevokeCheckForTest(
	s *Service, caller identity.Principal, g Grant, chain []identity.Object,
) GrantCheck {
	return s.housekeepingRevokeCheck(caller, g, chain)
}
