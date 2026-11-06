package setting

import (
	"context"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is the persistence port for the settings of the installation and of
// the namespaces. A SQL adapter serves it from one database.
//
// The database layer holds one row for each key that the API set. A key with
// no row has no database value. The store keeps one revision for each scope.
type Store interface {
	// Load returns the database values and the revision of scope. A scope
	// that never changed has no value and revision 0. It returns
	// namespace.ErrNotFound for a namespace that does not exist.
	Load(ctx context.Context, scope Scope) (Stored, error)

	// Update applies in.Patch to scope in one transaction with Change, and
	// returns the new resource and the changed keys.
	//
	// A patch that changes no database value writes nothing: no row, no new
	// revision and no audit entry. A patch that changes a value writes the
	// rows, the next revision and the audit entry in one transaction, so a
	// failed call changes nothing. When in.IfMatch is not the stored
	// revision, Update returns ErrPreconditionFailed and writes nothing. The
	// check runs in the transaction of the write, so two writers with the
	// same revision cannot both win.
	//
	// For the installation scope the audit entry has the scope
	// audit.ServerScope and fence is ignored. For a namespace scope the
	// entry has the namespace of the scope, and the store checks fence in
	// the same transaction: it returns namespace.ErrFenced when the fence is
	// stale or names another namespace. It returns namespace.ErrNotFound for
	// a namespace that does not exist, and audit.ErrInvalidEntry when the
	// entry fails validation or names another scope. It returns ErrUnknownKey,
	// ErrInvalidValue, ErrStaticOnly, ErrLocked and ErrFreshStepRequired as
	// Change describes.
	Update(ctx context.Context, scope Scope, fence namespace.Fence, in ChangeInput) (Result, error)

	// Sync stores digest as the digest of the static layer of the
	// installation. When the stored digest is different and not empty, it
	// also stores the next revision. It returns the revision and whether it
	// changed. It writes no audit entry: the entry of the start or of the
	// reload of the configuration file records the change.
	Sync(ctx context.Context, digest string) (revision int64, changed bool, err error)
}
