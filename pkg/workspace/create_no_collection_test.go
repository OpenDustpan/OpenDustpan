package workspace_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// unplacedStore hides the placed-store port of the store that it wraps.
type unplacedStore struct {
	workspace.Store
}

// TestCreateRefusedWithoutCollection proves that Create makes no workspace
// when the placement does not name a collection in the namespace of the
// workspace.
func TestCreateRefusedWithoutCollection(t *testing.T) {
	t.Parallel()

	valid := memPlacement("acme")

	var none workspace.Placement

	var noRef collection.Ref

	tests := []struct {
		name      string
		placement workspace.Placement
	}{
		{name: "zero placement", placement: none},
		{
			name: "collection name without ID",
			placement: workspace.Placement{
				Collection:   valid.Collection,
				CollectionID: "",
				Entry:        valid.Entry,
			},
		},
		{
			name: "ID without collection name",
			placement: workspace.Placement{
				Collection:   noRef,
				CollectionID: valid.CollectionID,
				Entry:        valid.Entry,
			},
		},
		{
			name: "collection in another namespace",
			placement: workspace.Placement{
				Collection:   collection.Ref{Namespace: namespace.Name("other"), Name: valid.Collection.Name},
				CollectionID: valid.CollectionID,
				Entry:        valid.Entry,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			ref := freshRef(t)

			_, err := f.svc.Create(t.Context(), ref, tc.placement)
			if !errors.Is(err, collection.ErrNoCollection) {
				t.Fatalf("Create = %v, want collection.ErrNoCollection", err)
			}

			if _, getErr := f.svc.Get(t.Context(), ref); !errors.Is(getErr, workspace.ErrNotFound) {
				t.Fatalf("Get after the refused create = %v, want workspace.ErrNotFound", getErr)
			}
		})
	}
}

// TestCreateRefusedByStoreWithoutPlacement proves that Create never falls
// back to a create with no collection when the store is not a
// workspace.PlacedStore.
func TestCreateRefusedByStoreWithoutPlacement(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	svc := serviceOver(t, f, unplacedStore{Store: f.store}, nil)
	ref := freshRef(t)

	_, err := svc.Create(t.Context(), ref, memPlacement(ref.Namespace))
	if !errors.Is(err, workspace.ErrPlacementUnsupported) {
		t.Fatalf("Create = %v, want workspace.ErrPlacementUnsupported", err)
	}

	if _, getErr := svc.Get(t.Context(), ref); !errors.Is(getErr, workspace.ErrNotFound) {
		t.Fatalf("Get after the refused create = %v, want workspace.ErrNotFound", getErr)
	}
}
