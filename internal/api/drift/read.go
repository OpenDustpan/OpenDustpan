package drift

import (
	"errors"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
)

// unitResponse names the unit of an answer. The name is the name at the time
// of the answer; the unit ID stays the same after a rename.
type unitResponse struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

// resourceResponse is one resource that differs: its address and the kind of
// difference. It never holds an attribute name or an attribute value.
type resourceResponse struct {
	Address string     `json:"address"`
	Kind    drift.Kind `json:"kind"`
}

// driftResponse is the answered document of a drift read: the status of the
// unit and the latest drift result that it follows from. A unit with no
// result has the status unknown, no time and no job.
//
// Contract: never remove or rename these fields.
type driftResponse struct {
	Unit      unitResponse       `json:"unit"`
	Status    drift.Status       `json:"status"`
	CheckedAt *time.Time         `json:"checkedAt,omitempty"`
	JobID     string             `json:"jobId,omitempty"` //nolint:tagliatelle // The description of the API demands camelCase: jobId.
	Resources []resourceResponse `json:"resources"`
	Truncated bool               `json:"truncated"`
}

// readDrift answers GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift
// with the latest drift result of the unit. The route wrapper already allowed
// namespace:read on the namespace and unit:read on the unit. A unit that no
// check has run on has the status unknown, never in-sync: nothing has said that
// the unit matches its state.
func (h *Handler) readDrift(w http.ResponseWriter, r *http.Request, _ identity.Principal, t *target) {
	ctx := r.Context()

	out := driftResponse{
		Unit:      toUnitResponse(t),
		Status:    drift.StatusUnknown,
		CheckedAt: nil,
		JobID:     "",
		Resources: []resourceResponse{},
		Truncated: false,
	}

	latest, err := h.results.GetLatest(ctx, t.unit)

	switch {
	case errors.Is(err, drift.ErrNotFound):
		h.writeJSON(ctx, w, http.StatusOK, out)

		return
	case err != nil:
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the store reads the result of the unit of the call.
	// This check stays so that a result of another unit never answers under
	// this one, whatever a store gives back.
	if latest.Unit != t.unit || !latest.Status.Valid() {
		h.writeJSON(ctx, w, http.StatusOK, out)

		return
	}

	checkedAt := latest.CheckedAt
	out.Status, out.CheckedAt, out.JobID, out.Truncated = latest.Status, &checkedAt, string(
		latest.JobID,
	), latest.Truncated

	for _, res := range latest.Resources {
		out.Resources = append(out.Resources, resourceResponse{Address: res.Address, Kind: res.Kind})
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// toUnitResponse maps the unit of a target to its wire shape.
func toUnitResponse(t *target) unitResponse {
	return unitResponse{ID: t.unit.ID, Tool: toolLabel(t.unit.Tool), Name: t.obj.UnitName()}
}

// toolLabel maps a stored lowercase tool code to the tool label of the
// glossary ("Pulumi" or "OpenTofu"). A code without a label answers
// unchanged, so that a new tool still reads.
func toolLabel(code string) string {
	switch code {
	case "pulumi":
		return "Pulumi"
	case "opentofu":
		return "OpenTofu"
	default:
		return code
	}
}
