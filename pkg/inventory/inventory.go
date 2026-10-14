// Package inventory is the cross-tool read model of every unit in a
// namespace: its tool, its reference, its display name, its last change,
// its status, its resource count and its lock.
//
// It is a projection: each tool package publishes its own writes to it
// through this package, in the same transaction as the tool's own write,
// so it replicates with the namespace like any other data. Nothing else
// writes to the table, and it can be rebuilt from the tool packages' data.
// Tool-specific views read the tool's own model.
package inventory

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Entry is one unit in a namespace, as the console's cross-tool views read
// it. Tool names the tool that owns the unit, for example "pulumi". Name is
// the tool's own name for the unit, for example "web/prod". The core keeps
// both opaque and never parses them. UnitID is the unit's stable,
// opaque identifier, for example "stk-...". It lets a consumer follow a unit
// through a rename. DisplayName is the name the console shows. LastChanged
// is the time of the unit's last change. Status is empty when unknown.
// ResourceCount is nil when unknown. Lock is nil when the unit is not
// locked: a lock is a flag, not a status, so a unit can hold a lock in any
// status.
type Entry struct {
	LastChanged   time.Time
	Namespace     namespace.Name
	Tool          string
	Name          string
	UnitID        string
	DisplayName   string
	Status        string
	ResourceCount *int64
	Lock          *Lock
	// Collection is the name of the collection that holds the unit, as it was
	// written. It is empty when the unit has none (ADR 51). A collection never
	// changes the tool's own name of the unit.
	Collection string
}

// Lock is who holds a unit and why. Holder is the subject of the principal
// that holds the lock, never a token, a secret or a capability path. Since
// is when the holder took it. Operation is the tool's own word for what
// holds it: the update kind for the Pulumi CLI, the lock reason for
// OpenTofu, or "lock" when the tool names none.
type Lock struct {
	Since     time.Time
	Holder    string
	Operation string
}

// Reference returns the unit reference in the form <tool>:<namespace>/<name>.
func (e Entry) Reference() string {
	return e.Tool + ":" + e.Namespace.String() + "/" + e.Name
}

// Reader is the persistence port for the inventory projection. ListUnits returns
// the entries of one namespace, ordered by tool, then name. A namespace with
// no units reads as an empty list, as does a namespace that does not exist.
// It defines no sentinel error: implementations report storage failures
// without a dedicated value.
type Reader interface {
	// ListUnits returns the inventory entries of ns, ordered by tool, then name.
	ListUnits(ctx context.Context, ns namespace.Name) ([]Entry, error)
}

// DeletedRetention is the time that the server keeps a deleted unit before
// the final delete, until the retention setting exists (ADR 47, decision 2).
const DeletedRetention = 30 * 24 * time.Hour

// DeletedEntry is one deleted unit in a namespace (ADR 47). The delete of a
// unit moves its entry from the live inventory to the deleted units, in the
// same transaction as the tool's own write. Tool and UnitID identify it,
// because a new unit can take its name at once. Name is the tool's own name of
// the unit at the time of the delete. PurgeAfter is the end of its retention
// period.
type DeletedEntry struct {
	DeletedAt  time.Time
	PurgeAfter time.Time
	Namespace  namespace.Name
	Tool       string
	UnitID     string
	Name       string
}

// DeletedReader is the read port of the deleted units.
type DeletedReader interface {
	// ListDeletedUnits returns the deleted units of ns, ordered by tool, then
	// name, then unit ID.
	ListDeletedUnits(ctx context.Context, ns namespace.Name) ([]DeletedEntry, error)
}
