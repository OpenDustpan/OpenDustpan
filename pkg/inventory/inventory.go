// Package inventory is the cross-tool read model of every unit in a
// namespace: its tool, its reference, its display name, its last change,
// its status and its resource count.
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
// ResourceCount is nil when unknown.
type Entry struct {
	LastChanged   time.Time
	Namespace     namespace.Name
	Tool          string
	Name          string
	UnitID        string
	DisplayName   string
	Status        string
	ResourceCount *int64
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
