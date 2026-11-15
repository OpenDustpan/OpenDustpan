package access

import "context"

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
