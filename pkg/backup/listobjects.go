package backup

import (
	"context"

	"opendustpan.org/opendustpan/pkg/blob"
)

// listObjects walks every page under prefix and returns the objects. A step
// of a backup or a restore compares the whole listing of one backup or one
// restore with a manifest or with a set of ended restores, so it keeps the
// objects of that one directory, not their contents.
func listObjects(ctx context.Context, store blob.Store, prefix string) ([]blob.Object, error) {
	var out []blob.Object

	err := blob.Walk(ctx, store, prefix, func(o blob.Object) error {
		out = append(out, o)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}
