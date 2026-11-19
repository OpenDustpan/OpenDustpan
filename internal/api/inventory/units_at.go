package inventory

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
)

// errNoHistory reports a request for a view at a time on an installation
// whose inventory store keeps no change log.
var errNoHistory = errors.New("the inventory store keeps no change log")

// errFilterAt reports a label, reference or state filter with the at value:
// the view at a time takes no filter.
var errFilterAt = fmt.Errorf(
	"%w: the label, reference and state query values do not work with the at query value", errInvalidQuery,
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
// At is the time of the request as an RFC 3339 time in UTC. Purged counts the units
// that existed at the time and that a purge removed since: the view never
// names them. Omitted counts the units that the caller may not read now.
//
// Contract: never remove or rename these fields.
type listUnitsAtResponse struct {
	Units   []unitAtEntry `json:"units"`
	At      string        `json:"at"`
	Purged  int           `json:"purged"`
	Omitted int           `json:"omitted"`
}

// errAtFormat is the text of the 400 answer for an at value that is not an
// RFC 3339 time.
const errAtFormat = "at must be an RFC 3339 time, for example 2026-10-18T09:30:00Z"

// atGrammar is the allowlist for the at value: a date, the letter T, a time
// with an optional fraction of 1 to 9 digits, and Z or a numeric offset. It is
// stricter than time.Parse, which also takes a comma as the fraction mark and
// an offset of 24 hours. It also bounds the length: the longest match has 35
// characters.
var atGrammar = regexp.MustCompile(
	`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`,
)

// atQuery reads the query value at: an RFC 3339 time, with or without a
// fraction, with Z or any numeric offset. It returns the time in UTC, cut to
// whole milliseconds, which is the precision of the store. The bool is false
// when the request has no such value. A time before 1970-01-01T00:00:00Z is
// refused.
func atQuery(r *http.Request) (time.Time, bool, error) {
	raw := queryOf(r).Get("at")
	if raw == "" {
		return time.Time{}, false, nil
	}

	at, err := parseAt(raw)
	if err != nil {
		return time.Time{}, false, err
	}

	return at, true, nil
}

// parseAt checks one at value with the allowlist and reads it.
func parseAt(raw string) (time.Time, error) {
	if !atGrammar.MatchString(raw) {
		return time.Time{}, fmt.Errorf("%w: %s", errInvalidQuery, errAtFormat)
	}

	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s", errInvalidQuery, errAtFormat)
	}

	ms := t.UnixMilli()
	if ms < 0 {
		return time.Time{}, fmt.Errorf("%w: %s", errInvalidQuery, errAtFormat)
	}

	return time.UnixMilli(ms).UTC(), nil
}

// toUnitAtEntry maps one unit of the view to its wire shape.
func (h *Handler) toUnitAtEntry(u coreinventory.UnitAt) unitAtEntry {
	entry := h.toUnitEntry(u.Entry)
	if u.Deleted() {
		entry.ConsoleURL = ""
	}

	out := unitAtEntry{
		unitEntry:   entry,
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

// listUnitsAt answers GET /api/dustpan/v1/namespaces/{namespace}/units with the at value: the units
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
		Units: units, At: formatTime(at), Purged: view.Purged, Omitted: omitted,
	})
}
