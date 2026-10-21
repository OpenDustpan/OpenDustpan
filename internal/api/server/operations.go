package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Tags of the operations of this adapter.
const (
	tagApprovals   = "approvals"
	tagPreferences = "preferences"
)

// operationLabels names the routes of this adapter that sit under
// apidesc.Prefix and that the description holds. The key is the pattern of the
// route. A route under the prefix with no entry here has no operationId, so
// the server refuses to start with it.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, approvalDefaultPath): apidesc.Name("getApprovalDefault", tagApprovals).
		WithETag(),
	apidesc.Pattern(http.MethodPut, approvalDefaultPath): apidesc.Name("setApprovalDefault", tagApprovals).
		WithIfMatch().
		WithETag(),
	apidesc.Pattern(http.MethodDelete, approvalDefaultPath): apidesc.Name("deleteApprovalDefault", tagApprovals).
		WithIfMatch(),
	apidesc.Pattern(http.MethodGet, approvalRulesPath):    apidesc.Name("listApprovalRules", tagApprovals),
	apidesc.Pattern(http.MethodGet, approvalRuleItemPath): apidesc.Name("getApprovalRule", tagApprovals).WithETag(),
	apidesc.Pattern(http.MethodPut, approvalRuleItemPath): apidesc.Name("setApprovalRule", tagApprovals).
		WithIfMatch().
		WithETag(),
	apidesc.Pattern(http.MethodDelete, approvalRuleItemPath): apidesc.Name("deleteApprovalRule", tagApprovals).
		WithIfMatch(),
	apidesc.Pattern(http.MethodGet, capabilitiesPath): apidesc.Name("getCapabilities", "capabilities"),
	apidesc.Pattern(http.MethodGet, openAPIPath):      apidesc.Name("getApiDescription", "description"),
	apidesc.Pattern(http.MethodGet, preferencesPath):  apidesc.Name("getPreferences", tagPreferences).WithETag(),
	apidesc.Pattern(http.MethodPatch, preferencesPath): apidesc.Name("updatePreferences", tagPreferences).
		WithIfMatch().
		WithETag(),
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix. A route that
// the unmoved paths still serve is not in the list.
//
// The sign-in with the one-time code of the dev command is not in the list.
// It is on apidesc.Undescribed with the reason, and apidesc.Collect leaves it
// out by its exact pattern.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		rows = append(rows, apidesc.Row{
			Method: route.Method, Path: route.Path, Actions: actionNames(route.Actions),
			Public:         route.Scope == ScopePublic,
			IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}

// actionNames returns the actions as text.
func actionNames(actions []identity.Action) []string {
	out := make([]string, len(actions))
	for i, action := range actions {
		out[i] = string(action)
	}

	return out
}
