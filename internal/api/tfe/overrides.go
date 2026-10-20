package tfe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
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

// allowance returns the overrides that the caller may use on the unit for one
// request: the switch of the namespace narrowed by the switch of the unit, and
// the action of the override. It refuses nothing: the domain refuses an
// override that the result does not allow (ADR 44, decisions 4, 5 and 8).
func (h *Handler) allowance(
	ctx context.Context, p identity.Principal, id workspace.ID, obj identity.Object,
) (namespace.Overrides, error) {
	w, err := h.workspaces.GetByID(ctx, id)
	if err != nil {
		return namespace.Overrides{}, err
	}

	ns, err := h.namespaces.Get(ctx, w.Ref.Namespace)
	if err != nil {
		return namespace.Overrides{}, err
	}

	switches := ns.Overrides.And(w.Overrides)

	return namespace.Overrides{
		Forced:   switches.Forced && h.permits(ctx, p, identity.ActionUnitWriteForced, obj),
		Unlocked: switches.Unlocked && h.permits(ctx, p, identity.ActionUnitWriteUnlocked, obj),
	}, nil
}

// permits reports whether the principal may do action on obj.
func (h *Handler) permits(ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object) bool {
	return h.authz.Authorize(ctx, p, action, obj) == nil
}

// recordOverrideRefusal writes the audit entry of a write that the workspace
// refused because an override is off or not granted. The switch check in the
// domain and the permits check above write none, and the 403 alone leaves no
// trace. It does nothing for any other error.
func (h *Handler) recordOverrideRefusal(
	ctx context.Context, r *http.Request, p identity.Principal, obj identity.Object, err error,
) {
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		return
	}

	// The action comes from the typed refusal, never from its text. A refusal
	// without a type is a forced write.
	action := identity.ActionUnitWriteForced
	if override, ok := workspace.RefusedOverride(err); ok && override == workspace.OverrideUnlocked {
		action = identity.ActionUnitWriteUnlocked
	}

	h.guard.Record(ctx, r, p, refusal.Denial{Action: action, Object: obj, Reason: "override refused"})
}

// appendOverrideAudit writes the audit entry of one allowed override: the
// action, the unit, and the version it wrote (ADR 44, decision 7). A nil audit
// store writes no entry. It returns the append error, so the route answers
// 500 when the entry is lost.
func (h *Handler) appendOverrideAudit(
	r *http.Request, p identity.Principal, obj identity.Object, action identity.Action, version string,
) error {
	if h.audit == nil {
		return nil
	}

	ctx := r.Context()

	entry := audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     obj.Namespace(),
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(action),
		Object:        obj.String(),
		Version:       version,
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     truncateUserAgent(r.UserAgent()),
		Channel:       audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:          audit.KindOverride,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     requestid.FromContext(ctx),
		ObjectID:      obj.UnitID(),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: string(p.Kind),
		JobID:         p.JobID,
	}

	ns, err := h.namespaces.Get(ctx, entry.Namespace)
	if err != nil {
		return err
	}

	_, err = h.audit.Append(ctx, ns.Fence(), entry)

	return err
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
		h.writeJSONAPIError(ctx, w, http.StatusBadRequest, "bad request", errOverrideBody.Error())

		return
	}

	ws, err := h.workspaces.SetOverrides(ctx, workspace.ID(r.PathValue("id")), ov)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, overrideSwitchesOf(ws.Overrides))
}
