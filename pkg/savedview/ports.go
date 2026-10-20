package savedview

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store keeps saved views. A SQL adapter serves it from the database of the
// namespace. Every write carries the fence of the namespace and the audit entry
// of the change. The caller stamps the time and the actor on the entry. The
// store checks the fence and appends the entry in the transaction of the write,
// and it changes nothing when either fails (fail closed).
//
// Every read names the owner who asks, by the ID of the principal. The store
// returns only the views of that owner. A view whose owner is not a local
// principal matches no reader, so it fails closed. So a caller that skips its own check cannot read the view of another
// person.
type Store interface {
	// Create stores v. It returns:
	//   - ErrInvalid when v.Validate fails.
	//   - ErrExists when the owner has a view with the same name in the
	//     namespace.
	//   - ErrLimit when the owner has MaxPerOwner views in the namespace, or
	//     the namespace has MaxPerNamespace views.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than v.
	Create(ctx context.Context, v View, fence namespace.Fence, e audit.Entry) error
	// Get returns the view with ID id, if owner owns it in ns. It returns ErrNotFound for an
	// unknown ID, for a view of another owner and for a view of another
	// namespace. The three answers are the same, so a caller cannot learn that
	// a view exists.
	Get(ctx context.Context, ns namespace.Name, id ID, owner string) (View, error)
	// List returns the views of owner in ns, ordered by name, then ID. It
	// returns an empty list for an owner with no view.
	List(ctx context.Context, ns namespace.Name, owner string) ([]View, error)
	// Delete removes the view with ID id of owner in ns. It returns ErrNotFound as Get
	// does, and namespace.ErrFenced. On an error it changes nothing.
	Delete(ctx context.Context, ns namespace.Name, id ID, owner string, fence namespace.Fence, e audit.Entry) error
}
