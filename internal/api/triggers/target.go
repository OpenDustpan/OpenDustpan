package triggers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// maxUnitIDLength bounds the unit ID of a path. A longer value is no unit ID.
const maxUnitIDLength = 100

// target is the object of a request, found before any authorization check: the
// namespace, the live unit of the path and its authorization object, and, for
// a trigger route, the trigger.
type target struct {
	ns      namespace.Namespace
	unit    coreinventory.Entry
	obj     identity.Object
	trigger trigger.Trigger
}

// findNamespace returns the namespace that the path names. A name that is not
// valid and a namespace that does not exist answer errNotFound alike.
func (h *Handler) findNamespace(r *http.Request) (namespace.Namespace, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return namespace.Namespace{}, errNotFound
	}

	ns, err := h.namespaces.Get(r.Context(), name)
	if errors.Is(err, namespace.ErrNotFound) {
		return namespace.Namespace{}, errNotFound
	}

	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("get namespace: %w", err)
	}

	return ns, nil
}

// resolveUnit finds the live unit with the unit ID of the path in the namespace
// of the path. The unit ID is unique in the installation, so the path holds no
// tool. A unit of another namespace, an unknown unit and a deleted unit answer
// errNotFound alike.
func (h *Handler) resolveUnit(r *http.Request) (target, error) {
	ns, err := h.findNamespace(r)
	if err != nil {
		return target{}, err
	}

	unitID := r.PathValue("unit_id")
	if !ident.Valid(unitID, maxUnitIDLength) {
		return target{}, errNotFound
	}

	e, err := h.units.GetUnit(r.Context(), unitID)
	if errors.Is(err, coreinventory.ErrUnitNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get unit: %w", err)
	}

	// Defense in depth: the inventory looks a unit up by its ID alone. A unit
	// of another namespace must not answer under this namespace, whatever the
	// caller may read there.
	if e.Namespace != ns.Name || e.UnitID != unitID {
		return target{}, errNotFound
	}

	obj, err := identity.UnitObject(e.Tool, ns.Name, e.Name)
	if err != nil {
		return target{}, errNotFound
	}

	return target{ns: ns, unit: e, obj: obj.WithUnitID(e.UnitID), trigger: noTrigger()}, nil
}

// resolveTrigger finds the unit of the path and the trigger of the path on it.
// An invalid ID, an unknown trigger and a trigger of another unit answer
// errNotFound alike.
func (h *Handler) resolveTrigger(r *http.Request) (target, error) {
	return h.resolveWith(r, h.store.Get)
}

// resolveStoredTrigger is resolveTrigger for a delete. It reads the row as the
// store holds it, so that a row that fails its checks can still be removed: a
// stored kind, frequency, source, enabled flag, last saver or last saver ID
// that is not valid does not stop a delete. The row must still name the live
// unit of the path: this route answers not-found for a trigger whose unit
// namespace or unit ID does not match the path, or whose ID is not valid, so
// such a row is not reachable through it. Every other route reads with Get,
// which refuses a row that fails its checks.
func (h *Handler) resolveStoredTrigger(r *http.Request) (target, error) {
	return h.resolveWith(r, h.store.Stored)
}

// resolveWith finds the trigger of the path with read.
func (h *Handler) resolveWith(
	r *http.Request, read func(context.Context, namespace.Name, trigger.ID) (trigger.Trigger, error),
) (target, error) {
	t, err := h.resolveUnit(r)
	if err != nil {
		return target{}, err
	}

	id := trigger.ID(r.PathValue("trigger_id"))
	if id.Validate() != nil {
		return target{}, errNotFound
	}

	got, err := read(r.Context(), t.ns.Name, id)
	if errors.Is(err, trigger.ErrNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get trigger: %w", err)
	}

	// Defense in depth: the store looks a trigger up by its namespace and ID.
	// A trigger of another unit must not answer under this unit.
	if got.Unit.Namespace != t.ns.Name || got.Unit.ID != t.unit.UnitID {
		return target{}, errNotFound
	}

	t.trigger = got

	return t, nil
}

// noTrigger is the trigger of a target that has none.
func noTrigger() trigger.Trigger {
	return trigger.Trigger{
		ID: "", Unit: graph.Unit{Namespace: "", Tool: "", ID: ""}, Kind: "",
		Frequency: trigger.Frequency{Preset: "", Cron: ""}, Source: graph.Unit{Namespace: "", Tool: "", ID: ""},
		Enabled: false, LastSaver: "", LastSaverID: "",
	}
}
