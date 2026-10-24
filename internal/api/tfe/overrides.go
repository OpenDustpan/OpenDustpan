package tfe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errOverrideBody names the body that a switch change needs. Its text is the
// detail of the 400 answer.
var errOverrideBody = errors.New("the body must set allow-forced-writes and allow-unlocked-writes")

// overrideSwitchesBody is the JSON body of the override switches of a unit
// (ADR 44, decision 6). The unit switch narrows the namespace switch: an
// override needs both.
type overrideSwitchesBody struct {
	AllowForcedWrites   bool `json:"allow-forced-writes"`   //nolint:tagliatelle // The TFE-style wire name; it must not change.
	AllowUnlockedWrites bool `json:"allow-unlocked-writes"` //nolint:tagliatelle // The TFE-style wire name; it must not change.
}

// overrideSwitchesInput is the body of a change. Its pointers tell a missing
// field from false, so a change must name both switches.
type overrideSwitchesInput struct {
	AllowForcedWrites   *bool `json:"allow-forced-writes"`   //nolint:tagliatelle // The TFE-style wire name; it must not change.
	AllowUnlockedWrites *bool `json:"allow-unlocked-writes"` //nolint:tagliatelle // The TFE-style wire name; it must not change.
}

// decodeOverrideSwitches reads a change body. It refuses an unknown field,
// a body that is not one object, a body that lacks either switch, and a null
// body.
func decodeOverrideSwitches(body []byte) (namespace.Overrides, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in overrideSwitchesInput
	if err := dec.Decode(&in); err != nil {
		return namespace.Overrides{}, errOverrideBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return namespace.Overrides{}, errOverrideBody
	}

	if in.AllowForcedWrites == nil || in.AllowUnlockedWrites == nil {
		return namespace.Overrides{}, errOverrideBody
	}

	return namespace.Overrides{Forced: *in.AllowForcedWrites, Unlocked: *in.AllowUnlockedWrites}, nil
}

// overrideSwitchesOf returns the body that shows ov.
func overrideSwitchesOf(ov namespace.Overrides) overrideSwitchesBody {
	return overrideSwitchesBody{
		AllowForcedWrites:   ov.Forced,
		AllowUnlockedWrites: ov.Unlocked,
	}
}

// allowance returns the overrides that the caller may use on the workspace
// id for one request: the switch of the namespace, narrowed by the switch of
// the workspace, and each override action that the caller holds. It refuses
// nothing: the domain refuses an override that the result does not allow.
func (h *Handler) allowance(
	ctx context.Context, p identity.Principal, id workspace.ID, obj identity.Object,
) (namespace.Overrides, error) {
	w, err := h.workspaces.GetByID(ctx, id)
	if err != nil {
		return namespace.Overrides{}, err
	}

	return h.rules().Allowance(ctx, p, w, obj)
}

// appendOverrideAudit writes the audit entry of one allowed override: the
// action, the unit, and the version it wrote. It returns the append error, so
// the route answers 500 when the entry is lost.
func (h *Handler) appendOverrideAudit(
	r *http.Request, p identity.Principal, obj identity.Object, action identity.Action, version string,
) error {
	return h.rules().Append(r.Context(), tofuunit.Entry(r, p, obj, tofuunit.Detail{
		At: time.Now().UTC(), Action: action, Version: version, ValuePath: "",
		Kind: audit.KindOverride, Outcome: audit.OutcomeSuccess, Reason: "",
	}))
}

// overrideRoutes returns the override switch rows of the route table: a read
// and a change of the switches of one unit (ADR 17, ADR 44 decision 6).
func (h *Handler) overrideRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet,
			Path:   "/api/v2/workspaces/{id}/override-switches",
			// Reading the switches needs unit:read. The body shows the
			// switches of the unit, not of its namespace.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.readOverrideSwitches,
		},
		{
			Method: http.MethodPut,
			Path:   "/api/v2/workspaces/{id}/override-switches",
			// Changing the switches needs unit:set-overrides. A switch set on
			// a unit only narrows its namespace switch.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitSetOverrides},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.writeOverrideSwitches,
		},
	}
}

// readOverrideSwitches serves GET /api/v2/workspaces/{id}/override-switches.
func (h *Handler) readOverrideSwitches(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	ws, err := h.workspaces.GetByID(ctx, workspace.ID(r.PathValue("id")))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, overrideSwitchesOf(ws.Overrides))
}

// writeOverrideSwitches serves PUT /api/v2/workspaces/{id}/override-switches.
// It answers 200 with the switches as stored. A body that is empty, null, lacks
// either switch, or has an unknown field answers 400 and changes no switch.
func (h *Handler) writeOverrideSwitches(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	ov, err := decodeOverrideSwitches(body)
	if err != nil {
		h.writeClassError(ctx, w, errorClass{
			status: http.StatusBadRequest, title: titleBadRequest, detail: errOverrideBody.Error(),
			code: errcode.RequestBodyInvalid, args: nil,
		})

		return
	}

	ws, err := h.workspaces.SetOverrides(ctx, workspace.ID(r.PathValue("id")), ov)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, overrideSwitchesOf(ws.Overrides))
}
