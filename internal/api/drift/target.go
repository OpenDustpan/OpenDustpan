package drift

import (
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// maxUnitIDLength bounds the unit ID of a path. A longer value is no unit ID.
const maxUnitIDLength = 128

// target is the object of a request, found before any authorization check: the
// namespace of the path and the live unit of the path.
type target struct {
	ns   namespace.Namespace
	obj  identity.Object
	unit graph.Unit
}

// resolveUnit finds the live unit with the unit ID of the path in the
// namespace of the path. The unit ID is unique in the installation, so the
// path holds no tool. A name that is not valid, an unknown namespace, an
// unknown unit, a unit of another namespace, a deleted unit and a purged unit
// answer errNotFound alike.
func (h *Handler) resolveUnit(r *http.Request) (target, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return target{}, errNotFound
	}

	ns, err := h.namespaces.Get(r.Context(), name)
	if errors.Is(err, namespace.ErrNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get namespace: %w", err)
	}

	unitID := r.PathValue("unit_id")
	if !ident.Valid(unitID, maxUnitIDLength) {
		return target{}, errNotFound
	}

	entry, err := h.units.GetUnit(r.Context(), unitID)
	if errors.Is(err, coreinventory.ErrUnitNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get unit: %w", err)
	}

	// Defense in depth: the unit ID is unique in the installation, and the
	// reader finds a unit by it alone. A unit of another namespace must not
	// answer under this namespace, whatever the caller may read there.
	if entry.Namespace != ns.Name || entry.UnitID != unitID {
		return target{}, errNotFound
	}

	return unitTarget(ns, entry.Tool, entry.Name, entry.UnitID)
}

// unitTarget returns the target of the unit with the given tool, name and ID.
func unitTarget(ns namespace.Namespace, tool, name, unitID string) (target, error) {
	unit := graph.Unit{Namespace: ns.Name, Tool: tool, ID: unitID}
	if unit.Validate() != nil {
		return target{}, errNotFound
	}

	obj, err := identity.UnitObject(tool, ns.Name, name)
	if err != nil {
		return target{}, errNotFound
	}

	return target{ns: ns, obj: obj.WithUnitID(unitID), unit: unit}, nil
}
