package settings

import (
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// maxUnitIDLength bounds the unit ID of a path. A longer value is no unit ID.
const maxUnitIDLength = 100

// target is the object of a request, found before any authorization check: the
// namespace, and for a unit route the live unit of the path and its
// authorization object. For a namespace route, obj is the namespace.
type target struct {
	ns   namespace.Namespace
	unit coreinventory.Entry
	obj  identity.Object
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

// resolveNamespace finds the namespace of the path.
func (h *Handler) resolveNamespace(r *http.Request) (target, error) {
	ns, err := h.findNamespace(r)
	if err != nil {
		return target{}, err
	}

	var none coreinventory.Entry

	return target{ns: ns, unit: none, obj: identity.NamespaceObject(ns.Name)}, nil
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

	return h.unitIn(r, ns, r.PathValue("unit_id"))
}

// unitIn finds the live unit unitID of ns.
func (h *Handler) unitIn(r *http.Request, ns namespace.Namespace, unitID string) (target, error) {
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

	return target{ns: ns, unit: e, obj: obj.WithUnitID(e.UnitID)}, nil
}
