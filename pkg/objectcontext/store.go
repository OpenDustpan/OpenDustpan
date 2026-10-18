package objectcontext

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// OutputItem names the state item that an output came from. Seq orders the
// state items of one unit: the update version for the Pulumi CLI, the order
// of finalized state versions for OpenTofu. The store keeps the output of
// the highest Seq.
type OutputItem struct {
	ID  string
	Seq int64
}

// Store is the persistence port of context. Every write checks the fence of
// the namespace of its object in the same transaction. An API write appends
// its audit entry in the same transaction, so a write that cannot be audited
// is not made. An output write is a projection of a state write and appends
// no entry of its own. Every adapter runs the conformance suite in
// objectcontexttest.
type Store interface {
	// Entries returns the stored entries of objects, of both sources, in the
	// order of objects and then API before output. An object with no entry
	// is not an error. objects holds 1 to MaxBatch references. It returns
	// ErrInvalidObject.
	Entries(ctx context.Context, objects []Ref) ([]Entry, error)
	// SetAPI applies p to the API entry of object, or makes the entry, and
	// appends e. In the same transaction, it reads the output entry of
	// object and applies CheckPatch, so a field from the code is never set
	// through the API. It sets the Changes of e from the fields before and
	// after the write (ChangesOf), because only the transaction knows the
	// fields before. It returns the new entry. It returns ErrInvalidObject,
	// ErrInvalidContext, ErrFieldFromCode, and namespace.ErrFenced when the
	// fence is stale. A namespace or a collection that the store does not
	// hold is ErrInvalidObject. So is a unit, or a resource of a unit, that
	// the inventory holds neither live nor deleted: the store checks it in
	// the transaction of the write, so a purge that committed first refuses
	// the write and no row stays for a purged unit.
	SetAPI(ctx context.Context, object Ref, p Patch, by string, fence namespace.Fence, e audit.Entry) (Entry, error)
	// DeleteAPI deletes the API entry of object and appends e, with the
	// Changes of e set to the fields that the delete cleared (ChangesOf). It
	// returns ErrNotFound when the object has no API entry, ErrInvalidObject
	// (also for a unit that a purge removed, as SetAPI), and
	// namespace.ErrFenced.
	DeleteAPI(ctx context.Context, object Ref, fence namespace.Fence, e audit.Entry) error
	// ReplaceOutput replaces the output entries of one unit and its
	// resources with entries, which come from item. When the stored entries
	// come from an item with a higher Seq, it changes nothing and returns
	// nil, so an operation that runs again or late does no harm. Each entry
	// names the unit or a resource of it, with the source SourceOutput. An
	// empty entries deletes the output entries of the unit. It returns
	// ErrInvalidObject (also for a unit that a purge removed, as SetAPI),
	// ErrInvalidContext and namespace.ErrFenced.
	ReplaceOutput(ctx context.Context, unit Ref, item OutputItem, entries []Entry, fence namespace.Fence) error
	// DropUnit deletes every entry of unit and of its resources, of both
	// sources, at the purge of the unit. A soft delete keeps them, so a
	// restore brings them back. It returns ErrInvalidObject and
	// namespace.ErrFenced.
	DropUnit(ctx context.Context, unit Ref, fence namespace.Fence) error
	// ByOwner returns the entries of namespace ns, of both sources, that
	// name owner, ordered by the kind, the ID and the resource key of the
	// object, and then API before output. It starts at the cursor after,
	// which is empty for the first page, and returns at most limit entries,
	// from 1 to MaxBatch, and the cursor of the next page, which is empty
	// after the last page. A cursor is opaque. It holds the IDs of objects,
	// not names, so a rename between two pages skips and repeats no entry.
	// It returns ErrInvalidContext for an invalid owner, limit or cursor.
	//
	// ByOwner does not resolve inherited owners. It matches the entries that
	// name owner themselves. A unit that sets no owners and gets owner from
	// its collection or its namespace is not in the answer: the answer holds
	// the entry of that collection or namespace, and the caller lists the
	// units of a collection to expand it.
	ByOwner(ctx context.Context, ns namespace.Name, owner, after string, limit int) ([]Entry, string, error)
}
