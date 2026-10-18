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
	ns, err := h.findNamespace(r)
	if err != nil {
		return target{}, err
	}

	ctx := r.Context()
	tool, unitID := r.PathValue("tool"), r.PathValue("unit_id")
	ref := objectcontext.UnitRef(graph.Unit{Namespace: ns.Name, Tool: tool, ID: unitID})

	if ref.Validate() != nil {
		return target{}, errNotFound
	}

	live, err := h.units.ListUnits(ctx, ns.Name)
	if err != nil {
		return target{}, fmt.Errorf("list units: %w", err)
	}

	for i := range live {
		if live[i].Tool != tool || live[i].UnitID != unitID {
			continue
		}

		obj, objErr := unitObject(tool, ns.Name, live[i].Name, unitID)
		if objErr != nil {
			return target{}, objErr
		}

		return target{ns: ns, obj: obj, ref: ref, unit: &live[i], deleted: nil}, nil
	}

	gone, err := h.deleted.GetDeletedUnit(ctx, ns.Name, tool, unitID)
	if errors.Is(err, coreinventory.ErrNotFound) {
		return target{}, errNotFound
	}

	if err != nil {
		return target{}, fmt.Errorf("get deleted unit: %w", err)
	}

	obj, err := unitObject(tool, ns.Name, gone.Name, unitID)
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
