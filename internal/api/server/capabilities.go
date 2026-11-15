package server

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
)

// capabilitiesPath is the route of the capabilities document.
const capabilitiesPath = "/api/dustpan/v1/capabilities"

// apiVersion is the version of the tool-neutral API that this server speaks.
const apiVersion = "v1"

// capabilitiesResponse is the capabilities document on the wire. A caller
// with no sign-in gets APIVersion only, so a client can check the version
// before a person signs in. InstallationKind and StateWriteGates need a
// signed-in caller with the action caller:read on the caller.
//
// Contract: never remove or rename these fields.
type capabilitiesResponse struct {
	APIVersion       string               `json:"apiVersion"`
	InstallationKind string               `json:"installationKind,omitempty"`
	StateWriteGates  *stateWriteGatesBody `json:"stateWriteGates,omitempty"`
}

// stateWriteGatesBody is the effective value of the state.write gates on the
// wire: the mode on or off, the reason, the write paths that the gates check
// and the write paths that they do not check. The gates are off only when the
// store keeps no gates.
type stateWriteGatesBody struct {
	Mode      stategate.Mode        `json:"mode"`
	Reason    string                `json:"reason"`
	Checked   []stategate.WritePath `json:"checked"`
	Unchecked []stategate.WritePath `json:"unchecked"`
}

// capabilitiesRoutes returns the row of the capabilities document.
func (h *Handler) capabilitiesRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet, Path: capabilitiesPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.capabilities,
		},
	}
}

// capabilities answers the capabilities document. The route is public, so
// it never answers an error for a missing or a wrong credential: such a
// caller gets the document with no installation kind.
func (h *Handler) capabilities(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()
	doc := capabilitiesResponse{APIVersion: apiVersion, InstallationKind: "", StateWriteGates: nil}

	if h.mayReadCaller(r) {
		doc.InstallationKind = h.installationKind().String()

		if h.stateWrite.Mode != "" {
			doc.StateWriteGates = &stateWriteGatesBody{
				Mode: h.stateWrite.Mode, Reason: h.stateWrite.Reason,
				Checked: nonNilPaths(h.stateWrite.Checked), Unchecked: nonNilPaths(h.stateWrite.Unchecked),
			}
		}
	}

	h.writeJSON(ctx, w, http.StatusOK, doc)
}

// installationKind returns the kind of the installation: production when the
// wiring set none.
func (h *Handler) installationKind() installation.Kind {
	if h.kind == "" {
		return installation.KindProduction
	}

	return h.kind
}

// mayReadCaller reports whether r carries a credential that
// authenticates and that holds caller:read on the caller.
func (h *Handler) mayReadCaller(r *http.Request) bool {
	p, caller, err := h.authenticate(r)
	if err != nil {
		return false
	}

	ctx := withPrincipal(r.Context(), p)
	if caller != nil {
		ctx = context.WithValue(ctx, sessionKey{}, *caller)
	}

	return h.authz.Authorize(ctx, p, identity.ActionCallerRead, identity.CallerObject()) == nil
}

// nonNilPaths returns paths, or an empty list for nil, so that the document holds
// a list and never null.
func nonNilPaths(paths []stategate.WritePath) []stategate.WritePath {
	if paths == nil {
		return []stategate.WritePath{}
	}

	return paths
}
