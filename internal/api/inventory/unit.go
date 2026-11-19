package inventory

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/logtext"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
)

// maxUnitIDLength bounds the unit ID of a path. A longer value is no unit ID.
const maxUnitIDLength = 100

// resolveUnitByID returns the unit reference of the live unit whose stable
// ID is in the path. An unknown ID and a deleted unit give the error that
// answers 404, the same answer as a denied caller gets.
func (h *Handler) resolveUnitByID(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	unitID := r.PathValue("unitID")

	// Defense in depth: the store may check the ID too; this boundary check
	// stays so that a raw path value never reaches the store.
	if !ident.Valid(unitID, maxUnitIDLength) {
		return identity.Object{}, coreinventory.ErrUnitNotFound
	}

	e, err := h.units.GetUnit(r.Context(), unitID)
	if err != nil {
		if errors.Is(err, coreinventory.ErrUnitNotFound) {
			return identity.Object{}, coreinventory.ErrUnitNotFound
		}

		return identity.Object{}, fmt.Errorf("inventory unit: %w", err)
	}

	// Defense in depth: the store looks a unit up by its ID. A row with
	// another unit ID must not answer for this path, whatever the store
	// returned. The path check and the authorization check do not replace it.
	if e.UnitID != unitID {
		return identity.Object{}, coreinventory.ErrUnitNotFound
	}

	obj, err := unitObject(e)
	if err != nil {
		// A stored row with a tool or a name that is not valid answers as an
		// unknown unit, before any authorization check, so that the answer
		// does not tell a caller that the damaged row exists and does not
		// repeat the stored text. The cause goes to the log.
		h.logger.WarnContext(r.Context(), "stored unit has no valid object",
			slog.String("unit_id", logtext.Clean(e.UnitID)), slog.String("error", logtext.Clean(err.Error())))

		return identity.Object{}, coreinventory.ErrUnitNotFound
	}

	return obj, nil
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
