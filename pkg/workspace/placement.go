package workspace

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Placement puts a new workspace in a collection in the transaction that makes
// the workspace. The caller has already chosen the collection and checked the
// right to make a unit in it.
type Placement struct {
	// Collection is the collection of the new workspace. It must be in the
	// namespace of the workspace.
	Collection collection.Ref
	// CollectionID is the stable ID of Collection that the caller checked. The
	// store refuses the create when the collection has another ID now.
	CollectionID string
	// Entry is the audit entry of the placement. The store appends it in the
	// same transaction. The service sets its time.
	Entry audit.Entry
}

// PlacedStore is the port of a store that makes a workspace, its inventory
// entry, its membership of a collection and the audit entry of the placement
// in one fenced transaction. A failure of any part makes none of them, so no
// workspace exists without its collection.
type PlacedStore interface {
	// CreateWorkspacePlaced persists w as CreateWorkspace does and puts it in
	// the collection of p, in the same transaction. It also returns
	// collection.ErrNoCollection when p names no collection,
	// collection.ErrNotFound when the collection does not exist,
	// collection.ErrCollectionChanged when it has another ID than
	// p.CollectionID, and the errors of a refused join (for example a gate).
	CreateWorkspacePlaced(ctx context.Context, w Workspace, p Placement, fence namespace.Fence) error
}

// Complete reports whether p names a collection in ns and the checked ID of it.
func (p Placement) Complete(ns namespace.Name) bool {
	return p.Collection.Name != "" && p.CollectionID != "" && p.Collection.Namespace == ns
}

// errNoCollection returns the refusal of a create with no collection.
func errNoCollection(ref Ref) error {
	return fmt.Errorf("create workspace %s: %w", ref, collection.ErrNoCollection)
}
