package inventory

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
)

// errNoHistory reports a request for a view at a time on an installation
// whose inventory store keeps no change log.
var errNoHistory = errors.New("the inventory store keeps no change log")

// errFilterAt reports a label or reference filter with the at value: the
// change log keeps no labels.
var errFilterAt = fmt.Errorf(
	"%w: the label and reference query values do not work with the at query value", errInvalidQuery,
)

// unitAtEntry is one unit in the view at a time. The embedded entry is the
// unit as it was at the time: its name and its last change then. CurrentName
// is the name of the unit now, or its name before the delete when the unit is
// soft-deleted. UnitID is the stable ID of the unit, so a client follows the
// unit through a rename. Deleted is true, with DeletedAt, when the unit is
// soft-deleted now.
type unitAtEntry struct {
	unitEntry

	UnitID      string `json:"unitID"`
	CurrentName string `json:"currentName"`
	Deleted     bool   `json:"deleted,omitempty"`
	DeletedAt   string `json:"deletedAt,omitempty"`
}

// listUnitsAtResponse is the answered document of the unit list at a time.
// At is the time of the request in Unix milliseconds. Purged counts the units
// that existed at the time and that a purge removed since: the view never
// names them. Omitted counts the units that the caller may not read now.
//
// Contract: never remove or rename these fields.
type listUnitsAtResponse struct {
	Units   []unitAtEntry `json:"units"`
	At      int64         `json:"at"`
	Purged  int           `json:"purged"`
	Omitted int           `json:"omitted"`
}

// atQuery reads the query value at: a time in Unix milliseconds. The bool is
// false when the request has no such value.
func atQuery(r *http.Request) (time.Time, bool, error) {
	raw := r.URL.Query().Get("at")
	if raw == "" {
		return time.Time{}, false, nil
	}

	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 {
		return time.Time{}, false, fmt.Errorf("%w: at must be a time in Unix milliseconds", errInvalidQuery)
	}

	return time.UnixMilli(ms).UTC(), true, nil
}

// toUnitAtEntry maps one unit of the view to its wire shape.
func (h *Handler) toUnitAtEntry(u coreinventory.UnitAt) unitAtEntry {
	out := unitAtEntry{
		unitEntry:   h.toUnitEntry(u.Entry),
		UnitID:      u.Entry.UnitID,
		CurrentName: u.CurrentName,
		Deleted:     u.Deleted(),
		DeletedAt:   "",
	}

	if u.Deleted() {
		out.DeletedAt = formatTime(u.DeletedAt)
	}

	return out
}

// unitAtObject maps one unit of the view to its tool-neutral unit reference:
// the name it has now, with its stable ID, as deletedObject does for a
// deleted unit. A caller needs unit:read on the unit now, never the access
// that the caller had at the time.
func unitAtObject(u coreinventory.UnitAt) (identity.Object, error) {
	obj, err := identity.UnitObject(u.Entry.Tool, u.Entry.Namespace, u.CurrentName)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit reference: %w", err)
	}

	return obj.WithUnitID(u.Entry.UnitID), nil
}

// listUnitsAt answers GET /api/inventory/units with the at value: the units
// of the namespace at that time. The route wrapper already allowed
// namespace:read on the namespace; this removes every unit that the caller
// may not read now (unit:read) and counts it in omitted. It lists a renamed
// unit under its name at the time with its current name, and a soft-deleted
// unit with the mark deleted. It counts a purged unit and never names it. The
// entries hold no collection, because a move to another collection appends no
// row to the change log. The view is complete from the time that the
// installation first ran the change log: a unit that existed then has one
// baseline row at its last change time, or at its delete time, and the view
// at an earlier time does not list it.
func (h *Handler) listUnitsAt(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, at time.Time,
) {
	ctx := r.Context()
	ns := obj.Namespace()

	if h.history == nil {
		h.writeError(ctx, w, errNoHistory)

		return
	}

	if _, err := h.namespaces.Get(ctx, ns); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	view, err := h.history.ViewAt(ctx, ns, at)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	units := make([]unitAtEntry, 0, len(view.Units))
	omitted := 0

	for i := range view.Units {
		uobj, objErr := unitAtObject(view.Units[i])
		if objErr != nil {
			h.writeError(ctx, w, objErr)

			return
		}

		if authErr := h.authz.Authorize(ctx, p, identity.ActionUnitRead, uobj); authErr != nil {
			if errors.Is(authErr, identity.ErrDenied) {
				omitted++

				continue
			}

			h.writeError(ctx, w, authErr)

			return
		}

		units = append(units, h.toUnitAtEntry(view.Units[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, listUnitsAtResponse{
		Units: units, At: at.UnixMilli(), Purged: view.Purged, Omitted: omitted,
	})
}
