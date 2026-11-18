package decisionrecord

import (
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// tagGates is the tag of every operation of this adapter.
const tagGates = "gates"

const (
	namespaceRecordPath    = "/api/dustpan/v1/namespaces/{namespace}/decision-records/{id}"
	installationRecordPath = "/api/dustpan/v1/decision-records/{id}"
)

// operationLabels names the routes of this adapter. The key is the pattern of
// the route.
func operationLabels() map[string]apidesc.Label {
	body := recordBody()

	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, namespaceRecordPath): apidesc.Name("getNamespaceDecisionRecord", tagGates).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, body),
		apidesc.Pattern(http.MethodGet, installationRecordPath): apidesc.Name("getInstallationDecisionRecord", tagGates).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, body),
	}
}

// recordBody is one stored decision record as the read routes answer it: the
// stored masked form, with the keys of the secrets and no value.
func recordBody() apidesc.Body {
	return apidesc.Typed[gate.Record](
		"DecisionRecord",
		apidesc.Named[gate.ChangeRef]("DecisionRecordChange"),
		apidesc.DeclareString[gate.RecordID](),
		apidesc.DeclareString[gate.Scope](),
		apidesc.DeclareString[gate.SchemaVersion](),
		apidesc.DeclareString[gate.PolicyID](),
		apidesc.DeclareString[gate.RequirementName](),
		apidesc.DeclareString[gate.UnitRef](),
		apidesc.Declare[gate.Facts](map[string]any{
			"description": "The fact document of the unit, with the keys of the secrets and no value.",
		}),
		apidesc.Declare[gate.FreshAuth](map[string]any{
			"type": []any{
				"integer",
				"null",
			},
			"description": "The fresh authentication window in seconds, or null for none.",
		}),
		apidesc.Named[gate.PathSetVersion]("DecisionRecordPathSet"),
		apidesc.Named[gate.UnitResult]("DecisionRecordUnit"),
		apidesc.Named[gate.UnitFacts]("DecisionRecordUnitFacts"),
		apidesc.Named[gate.ChangeFacts]("DecisionRecordChangeFacts"),
		apidesc.Named[gate.PolicyResult]("DecisionRecordPolicy"),
		apidesc.Named[gate.Requirement]("DecisionRecordRequirement"),
		apidesc.Named[gate.Approvers]("DecisionRecordApprovers"),
		apidesc.Named[gate.Signature]("DecisionRecordSignature"),
		apidesc.Named[gate.CarriedFrom]("DecisionRecordCarriedFrom"),
		apidesc.Named[gate.FixedControl]("DecisionRecordControl"),
	)
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, rt := range h.routes {
		// The wrapper reads the namespace of the path before the reads on
		// the record, for the namespace route.
		var namespaceActions []string

		actions := make([]string, 0, len(rt.Reads)+len(rt.Actions)+1)

		if rt.Scope != route.ScopePublic && !isInstallationPath(rt.Path) {
			namespaceActions = []string{string(identity.ActionNamespaceRead)}
			actions = append(actions, namespaceActions...)
		}

		for _, action := range slices.Concat(rt.Reads, rt.Actions) {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: rt.Method, Path: rt.Path, Actions: actions, NamespaceActions: namespaceActions,
			Public: rt.Scope == route.ScopePublic, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels())
}

// isInstallationPath reports whether path is the installation route, which
// names no namespace.
func isInstallationPath(path string) bool {
	return path == installationRecordPath
}
