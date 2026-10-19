package inventory

import (
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
)

// resolveUnitByID returns the unit reference of the live unit whose stable
// ID is in the path. An unknown ID and a deleted unit give the error that
// answers 404, the same answer as a denied caller gets.
func (h *Handler) resolveUnitByID(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	e, err := h.units.GetUnit(r.Context(), r.PathValue("unitID"))
	if err != nil {
		if errors.Is(err, coreinventory.ErrUnitNotFound) {
			return identity.Object{}, coreinventory.ErrUnitNotFound
		}

		return identity.Object{}, fmt.Errorf("inventory unit: %w", err)
	}

	return unitObject(e)
}

// getUnit answers GET /api/dustpan/v1/namespaces/-/units/{unitID} with the unit entry that
// the unit list gives for the same unit. The route wrapper already allowed
// unit:read on the unit and namespace:read on its namespace.
//
// The read can see a rename between the resolver and the handler. The handler
// answers 404 then, because the wrapper authorized the old name.
func (h *Handler) getUnit(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	e, err := h.units.GetUnit(ctx, r.PathValue("unitID"))
	if err != nil {
		if errors.Is(err, coreinventory.ErrUnitNotFound) {
			h.writeError(ctx, w, coreinventory.ErrUnitNotFound)

			return
		}

		h.writeError(ctx, w, fmt.Errorf("inventory unit: %w", err))

		return
	}

	if got, err := unitObject(e); err != nil || got.String() != obj.String() {
		h.writeError(ctx, w, coreinventory.ErrUnitNotFound)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, h.toUnitEntry(e))
}
