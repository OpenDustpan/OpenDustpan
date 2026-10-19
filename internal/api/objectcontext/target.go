package objectcontext

import (
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// target is the object of a request, found before any authorization check.
// unit is set for a live unit and deleted for a soft-deleted unit; both are
// nil for a namespace.
type target struct {
	unit    *coreinventory.Entry
	deleted *coreinventory.DeletedEntry
	ns      namespace.Namespace
	obj     identity.Object
	ref     objectcontext.Ref
}

// resolveNamespace finds the namespace of the path. A name that is not valid
// and a namespace that does not exist answer errNotFound alike.
func (h *Handler) resolveNamespace(r *http.Request) (target, error) {
	ns, err := h.findNamespace(r)
	if err != nil {
		return target{}, err
	}

	return target{
		ns: ns, obj: identity.NamespaceObject(ns.Name), ref: objectcontext.NamespaceRef(ns.Name),
		unit: nil, deleted: nil,
	}, nil
}

// findNamespace returns the namespace that the path names.
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

// resolveUnit finds the unit of the path in the namespace of the path, live
// or soft-deleted. A unit of another namespace, an unknown unit and a purged
// unit answer errNotFound alike.
func (h *Handler) resolveUnit(r *http.Request) (target, error) {
	return h.findUnit(r, r.PathValue("tool"), r.PathValue("unit_id"))
}

// resolveUnitByID finds the unit with the unit ID of the path in the
// namespace of the path, live or soft-deleted. The unit ID is unique in the
// installation, so the path holds no tool. The answers for a missing unit are
// those of resolveUnit.
func (h *Handler) resolveUnitByID(r *http.Request) (target, error) {
	return h.findUnit(r, "", r.PathValue("unit_id"))
}

// findUnit finds the unit with the ID unitID in the namespace of the path.
// A tool that is not empty must be the tool of the unit.
func (h *Handler) findUnit(r *http.Request, tool, unitID string) (target, error) {
	ns, err := h.findNamespace(r)
	if err != nil {
		return target{}, err
	}

	ctx := r.Context()

	live, err := h.units.ListUnits(ctx, ns.Name)
	if err != nil {
		return target{}, fmt.Errorf("list units: %w", err)
	}

	for i := range live {
		if live[i].UnitID != unitID || (tool != "" && live[i].Tool != tool) {
			continue
		}

		return h.liveTarget(ns, live[i])
	}

	if tool == "" {
		return h.deletedTargetByID(r, ns, unitID)
	}

	return h.deletedTarget(r, ns, tool, unitID)
}

// liveTarget returns the target of the live unit e.
func (h *Handler) liveTarget(ns namespace.Namespace, e coreinventory.Entry) (target, error) {
	ref := objectcontext.UnitRef(graph.Unit{Namespace: ns.Name, Tool: e.Tool, ID: e.UnitID})
	if ref.Validate() != nil {
		return target{}, errNotFound
	}

	obj, err := unitObject(e.Tool, ns.Name, e.Name, e.UnitID)
	if err != nil {
		return target{}, err
	}

	return target{ns: ns, obj: obj, ref: ref, unit: &e, deleted: nil}, nil
}

// deletedTargetByID returns the target of the soft-deleted unit unitID, or
// errNotFound.
func (h *Handler) deletedTargetByID(r *http.Request, ns namespace.Namespace, unitID string) (target, error) {
	all, err := h.deleted.ListDeletedUnits(r.Context(), ns.Name)
	if err != nil {
		return target{}, fmt.Errorf("list deleted units: %w", err)
	}

	for i := range all {
		if all[i].UnitID == unitID {
			return h.goneTarget(ns, all[i])
		}
	}

	return target{}, errNotFound
}

// deletedTarget returns the target of the soft-deleted unit unitID of tool,
// or errNotFound.
func (h *Handler) deletedTarget(r *http.Request, ns namespace.Namespace, tool, unitID string) (target, error) {
	if objectcontext.UnitRef(graph.Unit{Namespace: ns.Name, Tool: tool, ID: unitID}).Validate() != nil {
		return target{}, errNotFound
	}

	gone, err := h.deleted.GetDeletedUnit(r.Context(), ns.Name, tool, unitID)
	if errors.Is(err, coreinventory.ErrNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get deleted unit: %w", err)
	}

	return h.goneTarget(ns, gone)
}

// goneTarget returns the target of the soft-deleted unit gone.
func (h *Handler) goneTarget(ns namespace.Namespace, gone coreinventory.DeletedEntry) (target, error) {
	ref := objectcontext.UnitRef(graph.Unit{Namespace: ns.Name, Tool: gone.Tool, ID: gone.UnitID})
	if ref.Validate() != nil {
		return target{}, errNotFound
	}

	obj, err := unitObject(gone.Tool, ns.Name, gone.Name, gone.UnitID)
	if err != nil {
		return target{}, err
	}

	return target{ns: ns, obj: obj, ref: ref, unit: nil, deleted: &gone}, nil
}

// unitObject returns the authorization object of a unit, with its stable ID.
func unitObject(tool string, ns namespace.Name, name, unitID string) (identity.Object, error) {
	obj, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit object: %w", err)
	}

	return obj.WithUnitID(unitID), nil
}
