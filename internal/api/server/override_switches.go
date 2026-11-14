package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// maxOverrideBodyBytes bounds the body of a namespace override switch change.
// The body holds two booleans, so this is ample.
const maxOverrideBodyBytes = 1024

// errOverrideSwitchesBody names the body that a switch change needs. Its text
// is the error of the 400 answer.
var errOverrideSwitchesBody = errors.New(
	`invalid override switches body: provide {"allowForcedWrites":true|false,"allowUnlockedWrites":true|false}`,
)

// overrideSwitchesBody is the JSON body of the override switches of a
// namespace (ADR 44, decision 6). The switches are off by default. A change
// sets both fields.
type overrideSwitchesBody struct {
	AllowForcedWrites   bool `json:"allowForcedWrites"`
	AllowUnlockedWrites bool `json:"allowUnlockedWrites"`
}

// overrideSwitchesInput is the body of a change. Its pointers tell a missing
// field from false, so a change must name both switches.
type overrideSwitchesInput struct {
	AllowForcedWrites   *bool `json:"allowForcedWrites"`
	AllowUnlockedWrites *bool `json:"allowUnlockedWrites"`
}

// decodeOverrideSwitches reads a change body. It refuses an unknown field,
// a body that is not one object, a body that lacks either switch, and a null
// body.
func decodeOverrideSwitches(body []byte) (overrideSwitchesBody, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in overrideSwitchesInput
	if err := dec.Decode(&in); err != nil {
		return overrideSwitchesBody{}, errOverrideSwitchesBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return overrideSwitchesBody{}, errOverrideSwitchesBody
	}

	if in.AllowForcedWrites == nil || in.AllowUnlockedWrites == nil {
		return overrideSwitchesBody{}, errOverrideSwitchesBody
	}

	return overrideSwitchesBody{
		AllowForcedWrites:   *in.AllowForcedWrites,
		AllowUnlockedWrites: *in.AllowUnlockedWrites,
	}, nil
}

// overrideSwitchesOf returns the body that shows ov.
func overrideSwitchesOf(ov namespace.Overrides) overrideSwitchesBody {
	return overrideSwitchesBody{
		AllowForcedWrites:   ov.Forced,
		AllowUnlockedWrites: ov.Unlocked,
	}
}

// namespaceOverrideRoutes returns the override switch rows of the namespace
// (ADR 17, ADR 44 decision 6): a read and a change of the switches of one
// namespace.
func (h *Handler) namespaceOverrideRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet,
			Path:   "/api/server/namespaces/{namespace}/override-switches",
			// Reading the switches needs namespace:read on the namespace.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceRead},
			Resolve: resolveNamespacePath, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.readNamespaceOverrides,
		},
		{
			Method: http.MethodPut,
			Path:   "/api/server/namespaces/{namespace}/override-switches",
			// Changing the switches needs namespace:set-overrides. A namespace
			// switch is the outer bound of the switches of its units.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceSetOverrides},
			Resolve: resolveNamespacePath, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.writeNamespaceOverrides,
		},
	}
}

// resolveNamespacePath returns the namespace named in the path. A bad name
// fails before authorization runs.
func resolveNamespacePath(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, err
	}

	return identity.NamespaceObject(name), nil
}

// readNamespaceOverrides serves GET /api/server/namespaces/{namespace}/override-switches.
func (h *Handler) readNamespaceOverrides(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	ns, err := h.namespaces.Get(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, overrideSwitchesOf(ns.Overrides))
}

// writeNamespaceOverrides serves PUT /api/server/namespaces/{namespace}/override-switches.
// It answers 200 with the switches as stored. A body that is empty, null, lacks
// either switch, or has an unknown field answers 400 and changes no switch.
func (h *Handler) writeNamespaceOverrides(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOverrideBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	in, err := decodeOverrideSwitches(body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ns, err := h.namespaces.SetOverrides(
		ctx,
		obj.Namespace(),
		namespace.Overrides{Forced: in.AllowForcedWrites, Unlocked: in.AllowUnlockedWrites},
	)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, overrideSwitchesOf(ns.Overrides))
}
