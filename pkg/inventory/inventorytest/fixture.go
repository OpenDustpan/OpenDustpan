package inventorytest

import (
	"context"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Fixture is what the suite needs from a store: the read port, a namespace
// store, and two fixture methods. They are not port methods: each tool
// appends its change in its own write transaction.
type Fixture interface {
	inventory.HistoryReader
	namespace.Store

	// AppendChange runs the append code of the store in a fenced transaction
	// of its own. The store sets Seq and ignores the value of c.Seq. It
	// returns the stored row, namespace.ErrFenced for a stale fence, and
	// writes no row in that case.
	AppendChange(ctx context.Context, c inventory.Change, fence namespace.Fence) (inventory.Change, error)
	// PurgeNamespace deletes the namespace ns and leaves its change log rows
	// in place, because namespace.Store has no purge. A namespace that a
	// caller creates again under the name of ns has a new ID.
	PurgeNamespace(ctx context.Context, ns namespace.Name) error
}
