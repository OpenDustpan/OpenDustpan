package server

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// Tags of the audit chain, of the retention sweep and of the namespace
// override switches.
const (
	tagAudit        = "audit"
	tagDeletedUnits = "deleted-units"
	tagNamespaces   = "namespaces"
)

// auditChainOperationLabels names the audit chain routes, the retention
// sweep and the namespace override switch routes in the description. The
// key is the pattern of the route. The approval answers 202: the migration
// runs in the background.
func auditChainOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, auditChainPath): apidesc.Name("getAuditChain", tagAudit).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[auditChainResponse]("AuditChain")),
		apidesc.Pattern(http.MethodPost, auditChainPath): apidesc.Name("approveAuditChain", tagAudit).
			WithStatus(http.StatusAccepted).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[auditChainResponse]("AuditChain")),
		apidesc.Pattern(http.MethodPost, deletedSweepPath): apidesc.Name("sweepDeletedUnits", tagDeletedUnits).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[deletedSweepResponse](
				"DeletedUnitSweep", apidesc.Named[sweptUnitResponse]("SweptUnit"),
			)),
		apidesc.Pattern(http.MethodGet, namespaceOverrideSwitchesPath): apidesc.Name("getNamespaceOverrideSwitches", tagNamespaces).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.Typed[overrideSwitchesBody]("OverrideSwitches")),
		apidesc.Pattern(http.MethodPut, namespaceOverrideSwitchesPath): apidesc.Name("setNamespaceOverrideSwitches", tagNamespaces).
			On(apidesc.KindNamespace).
			WithBodies(
				apidesc.Typed[overrideSwitchesBody]("OverrideSwitches"),
				apidesc.Typed[overrideSwitchesBody]("OverrideSwitches"),
			),
	}
}
