package server

import (
	"maps"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Tags of the operations of this adapter.
const (
	tagApprovals   = "approvals"
	tagPreferences = "preferences"
	tagGitHostLink = "git-host-links"
	tagSettings    = "settings"
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
		WithBodies(apidesc.NoBody, apidesc.Typed[capabilitiesResponse]("Capabilities",
			apidesc.Named[stateWriteGatesBody]("StateWriteGates"),
			apidesc.DeclareOpenEnum[stategate.Mode]("StateWriteGatesMode",
				string(stategate.ModeOn), string(stategate.ModeOff)),
			apidesc.DeclareOpenEnum[stategate.WritePath]("StateWritePath", writePaths()...))),
	apidesc.Pattern(http.MethodGet, openAPIPath): apidesc.Name("getApiDescription", "description").
		WithBodies(apidesc.NoBody, apidesc.Free("ApiDescription")),
	apidesc.Pattern(http.MethodPost, gitHostLinkRequestsPath): apidesc.Name("startGitHostLinkRequest", tagGitHostLink).
		WithStatus(http.StatusCreated).
		On(apidesc.KindCaller).
		WithBodies(apidesc.NoBody, apidesc.Typed[startGitHostLinkResponse]("StartGitHostLinkResponse")),
	apidesc.Pattern(http.MethodPost, gitHostLinkShowPath): apidesc.Name("showGitHostLinkRequest", tagGitHostLink).
		On(apidesc.KindCaller).
		WithBodies(
			apidesc.Typed[gitHostLinkTokenRequest]("GitHostLinkTokenRequest"),
			apidesc.Typed[gitHostLinkRequestResponse](
				"GitHostLinkRequest", apidesc.Named[gitHostAccountBody]("GitHostLinkAccount"),
				apidesc.Named[currentGitHostLinkBody]("CurrentGitHostLink"),
			),
		),
	apidesc.Pattern(http.MethodPost, gitHostLinksPath): apidesc.Name("acceptGitHostLink", tagGitHostLink).
		WithStatus(http.StatusCreated).
		On(apidesc.KindCaller).
		WithBodies(
			apidesc.Typed[acceptGitHostLinkRequest]("AcceptGitHostLinkRequest"),
			apidesc.Typed[acceptGitHostLinkResponse](
				"AcceptGitHostLinkResponse",
				apidesc.Named[gitHostLinkBody]("GitHostLink"), apidesc.Named[gitHostAccountBody]("GitHostLinkAccount"),
			),
		),
	apidesc.Pattern(http.MethodGet, gitHostLinksPath): apidesc.Name("listGitHostLinks", tagGitHostLink).
		On(apidesc.KindCaller).
		WithBodies(
			apidesc.NoBody,
			apidesc.Typed[gitHostLinkListResponse](
				"ListGitHostLinksResponse",
				apidesc.Named[gitHostLinkBody]("GitHostLink"), apidesc.Named[gitHostAccountBody]("GitHostLinkAccount"),
			),
		),
	apidesc.Pattern(http.MethodDelete, gitHostLinkPath): apidesc.Name("deleteGitHostLink", tagGitHostLink).
		On(apidesc.KindCaller).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodDelete, principalGitHostLinks): apidesc.Name("deletePrincipalGitHostLink", tagGitHostLink).
		On(apidesc.KindServer).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
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
	apidesc.Pattern(http.MethodGet, settingsPath): apidesc.Name("getSettings", tagSettings).
		WithETag().
		On(apidesc.KindServer).
		WithBodies(apidesc.NoBody, settingsSchema),
	apidesc.Pattern(http.MethodPatch, settingsPath): apidesc.Name("updateSettings", tagSettings).
		WithIfMatchRequired().
		WithETag().
		On(apidesc.KindServer).
		WithBodies(settingsPatchSchema, settingsSchema),
	apidesc.Pattern(http.MethodGet, namespaceSettingsPath): apidesc.Name("getNamespaceSettings", tagSettings).
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, settingsSchema),
	apidesc.Pattern(http.MethodPatch, namespaceSettingsPath): apidesc.Name("updateNamespaceSettings", tagSettings).
		WithIfMatchRequired().
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(settingsPatchSchema, settingsSchema),
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
	settingsSchema = apidesc.Typed[settingsBody](
		"Settings", apidesc.Named[settingBody]("Setting"), settingValueSchema,
	)
	settingsPatchSchema = apidesc.Typed[settingsPatchRequest]("UpdateSettingsRequest", settingValueSchema)
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

	return apidesc.Collect(rows, allLabels)
}

// allLabels joins the labels of the routes of this adapter: the table that
// Operations and queryOf read.
//
//nolint:gochecknoglobals // A fixed table, built once from the two tables above.
var allLabels = joinLabels()

// joinLabels returns one table with the labels of operationLabels, of
// gitHostOperationLabels, of the gate and access limit routes and of
// storeCheckOperationLabels.
func joinLabels() map[string]apidesc.Label {
	labels := maps.Clone(operationLabels)
	maps.Copy(labels, gitHostOperationLabels)
	maps.Copy(labels, gateObjectLabels())
	maps.Copy(labels, storeCheckOperationLabels())
	maps.Copy(labels, localUserOperationLabels())
	maps.Copy(labels, passkeyOperationLabels())
	maps.Copy(labels, directorySignInOperationLabels())

	return labels
}

// queryOf returns the query of a request, as the label of its route allows
// the handler to read it. A route that the unmoved paths still serve is not in
// the description, and its handler reads every name.
func queryOf(r *http.Request) apidesc.Query {
	return apidesc.QueryOf(r, allLabels)
}

// actionNames returns the actions as text.
func actionNames(actions []identity.Action) []string {
	out := make([]string, len(actions))
	for i, action := range actions {
		out[i] = string(action)
	}

	return out
}

// userAgent returns the User-Agent header of a request, for the audit record.
func userAgent(r *http.Request) string {
	return apidesc.Header(r, "User-Agent")
}

// writePaths returns the names of every write path of the state.write gates,
// the values of the enum StateWritePath.
func writePaths() []string {
	all := append(stategate.CheckedPaths(), stategate.UncheckedPaths()...)
	out := make([]string, 0, len(all))

	for _, p := range all {
		out = append(out, string(p))
	}

	return out
}
