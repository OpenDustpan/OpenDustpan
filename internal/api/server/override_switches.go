package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// maxOverrideBodyBytes bounds the body of a namespace override switch change.
// The body holds two booleans, so this is ample.
const maxOverrideBodyBytes = 1024

// overrideSwitchesBody is the JSON body of the override switches of a
// namespace (ADR 44, decision 6). The switches are off by default.
type overrideSwitchesBody struct {
	AllowForcedWrites   bool `json:"allowForcedWrites"`
	AllowUnlockedWrites bool `json:"allowUnlockedWrites"`
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
			Resolve: resolveNamespacePath,
			Serve:   h.readNamespaceOverrides,
		},
		{
			Method: http.MethodPut,
			Path:   "/api/server/namespaces/{namespace}/override-switches",
			// Changing the switches needs namespace:set-overrides. A namespace
			// switch is the outer bound of the switches of its units.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceSetOverrides},
			Resolve: resolveNamespacePath,
			Serve:   h.writeNamespaceOverrides,
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
// It answers 200 with the switches as stored. An empty or malformed body answers
// 400 and changes no switch.
func (h *Handler) writeNamespaceOverrides(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOverrideBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidNamespaceBody)

		return
	}

	var in overrideSwitchesBody
	if decodeErr := json.NewDecoder(bytes.NewReader(body)).Decode(&in); decodeErr != nil {
		h.writeError(ctx, w, errInvalidBody)

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
