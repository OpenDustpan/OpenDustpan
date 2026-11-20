package objectcontext

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/logtext"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// maxUnitIDLength bounds the unit ID of a path. A longer value is no unit ID.
const maxUnitIDLength = 100

// deletedLookupLimit is the page size of the lookup of one deleted unit. Two
// rows let a store that breaks the unit ID filter show itself.
const deletedLookupLimit = 2

// target is the object of a request, found before any authorization check.
// unit is set for a live unit and deleted for a soft-deleted unit; both are
// nil for a namespace.
type target struct {
	unit    *coreinventory.Entry
	deleted *coreinventory.DeletedEntry
	ns      namespace.Namespace
	obj     identity.Object
	ref     objectcontext.Ref
	// lookupErr is the error of a failed lookup. resolveTarget keeps it in
	// the target, and Authorize answers it after the address check.
	lookupErr error
}

// resolveNamespace finds the namespace of the path. A name that is not valid
// and a namespace that does not exist answer errNotFound alike.
func (h *Handler) resolveNamespace(_ http.ResponseWriter, r *http.Request) (target, error) {
	ns, err := h.findNamespace(r)
	if err != nil {
		return target{}, err
	}

	return target{
		ns: ns, obj: identity.NamespaceObject(ns.Name), ref: objectcontext.NamespaceRef(ns.Name),
		unit: nil, deleted: nil, lookupErr: nil,
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

// resolveUnitByID finds the unit with the unit ID of the path in the
// namespace of the path, live or soft-deleted. The unit ID is unique in the
// installation, so the path holds no tool. A unit of another namespace, an
// unknown unit and a purged unit answer errNotFound alike.
func (h *Handler) resolveUnitByID(_ http.ResponseWriter, r *http.Request) (target, error) {
	ns, err := h.findNamespace(r)
	if err != nil {
		return target{}, err
	}

	unitID := r.PathValue("unit_id")

	// Defense in depth: the route and the store may check the ID too; this
	// boundary check stays so that a raw path value never reaches the store.
	if !ident.Valid(unitID, maxUnitIDLength) {
		return target{}, errNotFound
	}

	// The unit ID is unique in the installation, so one indexed read finds the
	// unit. The read never loads the units of the namespace before any
	// authorization check.
	live, err := h.units.GetUnit(r.Context(), unitID)
	if err == nil {
		// Defense in depth: GetUnit names no namespace. A unit of another
		// namespace must not answer under this one, whatever the caller may
		// read there.
		if live.Namespace != ns.Name || live.UnitID != unitID {
			return target{}, errNotFound
		}

		return h.liveTarget(r, ns, live)
	}

	if !errors.Is(err, coreinventory.ErrUnitNotFound) {
		return target{}, fmt.Errorf("get unit: %w", err)
	}

	return h.deletedTargetByID(r, ns, unitID)
}

// liveTarget returns the target of the live unit e.
func (h *Handler) liveTarget(r *http.Request, ns namespace.Namespace, e coreinventory.Entry) (target, error) {
	ref := objectcontext.UnitRef(graph.Unit{Namespace: ns.Name, Tool: e.Tool, ID: e.UnitID})
	if ref.Validate() != nil {
		return target{}, h.damagedRow(r, e.UnitID, ref.Validate())
	}

	obj, err := unitObject(e.Tool, ns.Name, e.Name, e.UnitID)
	if err != nil {
		return target{}, h.damagedRow(r, e.UnitID, err)
	}

	return target{ns: ns, obj: obj, ref: ref, unit: &e, deleted: nil, lookupErr: nil}, nil
}

// deletedTargetByID returns the target of the soft-deleted unit unitID, or
// errNotFound.
func (h *Handler) deletedTargetByID(r *http.Request, ns namespace.Namespace, unitID string) (target, error) {
	// One restricted query finds the unit. It never loads the soft-deleted
	// units of the namespace before any authorization check. The page holds
	// two rows at most.
	page, err := h.deleted.QueryDeletedUnits(r.Context(), coreinventory.DeletedQuery{
		PurgeBefore: time.Time{}, After: nil, Namespace: ns.Name, Tool: "", NamePrefix: "",
		UnitIDs: []string{unitID}, Limit: deletedLookupLimit, Restricted: true,
	})
	if err != nil {
		return target{}, fmt.Errorf("query deleted units: %w", err)
	}

	for i := range page.Entries {
		// Defense in depth: the store filters by namespace and unit ID. A row
		// of another namespace or unit must not answer here, whatever the
		// store returned.
		if page.Entries[i].UnitID == unitID && page.Entries[i].Namespace == ns.Name {
			return h.goneTarget(r, ns, page.Entries[i])
		}
	}

	return target{}, errNotFound
}

// goneTarget returns the target of the soft-deleted unit gone.
func (h *Handler) goneTarget(r *http.Request, ns namespace.Namespace, gone coreinventory.DeletedEntry) (target, error) {
	ref := objectcontext.UnitRef(graph.Unit{Namespace: ns.Name, Tool: gone.Tool, ID: gone.UnitID})
	if ref.Validate() != nil {
		return target{}, h.damagedRow(r, gone.UnitID, ref.Validate())
	}

	obj, err := unitObject(gone.Tool, ns.Name, gone.Name, gone.UnitID)
	if err != nil {
		return target{}, h.damagedRow(r, gone.UnitID, err)
	}

	return target{ns: ns, obj: obj, ref: ref, unit: nil, deleted: &gone, lookupErr: nil}, nil
}

// unitObject returns the authorization object of a unit, with its stable ID.
func unitObject(tool string, ns namespace.Name, name, unitID string) (identity.Object, error) {
	obj, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit object: %w", err)
	}

	return obj.WithUnitID(unitID), nil
}

// damagedRow logs the cause of a stored unit row that fails its checks and
// returns errNotFound. The row answers as an unknown unit, before any
// authorization check, so that a caller with no right on the unit cannot tell
// that the row exists.
func (h *Handler) damagedRow(r *http.Request, unitID string, cause error) error {
	h.logger.WarnContext(r.Context(), "stored unit row is not served",
		slog.String("unit_id", logtext.Clean(unitID)), slog.String("error", logtext.Clean(cause.Error())))

	return errNotFound
}
