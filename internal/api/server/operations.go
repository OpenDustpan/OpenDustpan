package server

import (
	"maps"
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
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, approvalDefaultSchema),
	apidesc.Pattern(http.MethodPut, approvalDefaultPath): apidesc.Name("setApprovalDefault", tagApprovals).
		WithIfMatch().
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(approvalDefaultSchema, approvalDefaultSchema),
	apidesc.Pattern(http.MethodDelete, approvalDefaultPath): apidesc.Name("deleteApprovalDefault", tagApprovals).
		WithIfMatch().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, approvalRulesPath): apidesc.Name("listApprovalRules", tagApprovals).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[approvalRuleListBody](
			"ListApprovalRulesResponse", apidesc.Named[approvalRuleBody]("ApprovalRule"),
			apidesc.Named[approversBody]("Approvers"),
		)),
	apidesc.Pattern(http.MethodGet, approvalRuleItemPath): apidesc.Name("getApprovalRule", tagApprovals).
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, approvalRuleSchema),
	apidesc.Pattern(http.MethodPut, approvalRuleItemPath): apidesc.Name("setApprovalRule", tagApprovals).
		WithIfMatch().
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(approvalRuleSchema, approvalRuleSchema),
	apidesc.Pattern(http.MethodDelete, approvalRuleItemPath): apidesc.Name("deleteApprovalRule", tagApprovals).
		WithIfMatch().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, capabilitiesPath): apidesc.Name("getCapabilities", "capabilities").
		WithBodies(apidesc.NoBody, apidesc.Typed[capabilitiesResponse]("Capabilities")),
	apidesc.Pattern(http.MethodGet, openAPIPath): apidesc.Name("getApiDescription", "description").
		WithBodies(apidesc.NoBody, apidesc.Free("ApiDescription")),
	apidesc.Pattern(http.MethodGet, preferencesPath): apidesc.Name("getPreferences", tagPreferences).
		WithETag().
		On(apidesc.KindCaller).
		WithBodies(apidesc.NoBody, preferencesSchema),
	apidesc.Pattern(http.MethodPatch, preferencesPath): apidesc.Name("updatePreferences", tagPreferences).
		WithIfMatch().
		WithETag().
		On(apidesc.KindCaller).
		WithBodies(apidesc.Typed[updatePreferencesRequest](
			"UpdatePreferencesRequest", apidesc.Named[terminalSchemesPatchRequest]("TerminalSchemesPatch"),
		), preferencesSchema),
}

// The bodies that several routes share. The types of the bodies are the types
// that the handlers write and read, so the description cannot drift from
// them.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	approvalDefaultSchema = apidesc.Typed[approvalDefaultBody](
		"ApprovalDefault",
		apidesc.Named[approversBody]("Approvers"),
	)
	approvalRuleSchema = apidesc.Typed[approvalRuleBody]("ApprovalRule", apidesc.Named[approversBody]("Approvers"))
	preferencesSchema  = apidesc.Typed[preferencesResponse](
		"Preferences", apidesc.Named[terminalSchemesResponse]("TerminalSchemes"),
	)
)

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
			Method: route.Method, Path: route.Path, Actions: actionNames(route.Actions), NamespaceActions: nil,
			Public:         route.Scope == ScopePublic,
			IdempotencyKey: false,
		})
	}

	labels := maps.Clone(operationLabels)
	maps.Copy(labels, gitHostOperationLabels)

	return apidesc.Collect(rows, labels)
}

// actionNames returns the actions as text.
func actionNames(actions []identity.Action) []string {
	out := make([]string, len(actions))
	for i, action := range actions {
		out[i] = string(action)
	}

	return out
}
