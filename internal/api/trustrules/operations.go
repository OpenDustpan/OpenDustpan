package trustrules

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// tag is the tag of the routes of the trust rules.
const tag = "trust-rules"

// operationLabels names the routes of this adapter. The server refuses to
// start without a label for a route under apidesc.Prefix.
func operationLabels() map[string]apidesc.Label {
	keySource := apidesc.DeclareOpenEnum[trustrule.KeySource]("TrustRuleKeySource",
		string(trustrule.KeySourceDiscovery), string(trustrule.KeySourceStatic))
	condition := apidesc.Named[conditionDocument]("TrustRuleCondition")
	rule := apidesc.Typed[ruleDocument]("TrustRule", keySource, condition)

	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, RulesPath): apidesc.Name("listTrustRules", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.Typed[listDocument]("TrustRuleList", keySource, condition,
				apidesc.Named[ruleDocument]("TrustRule"))),
		apidesc.Pattern(http.MethodPost, RulesPath): apidesc.Name("createTrustRule", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.Typed[createRequest]("CreateTrustRuleRequest", keySource, condition), rule),
		apidesc.Pattern(http.MethodGet, RulePath): apidesc.Name("getTrustRule", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, rule),
		apidesc.Pattern(http.MethodDelete, RulePath): apidesc.Name("deleteTrustRule", tag).
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, apidesc.NoBody),
	}
}

// Operations returns the routes of this adapter for the description of the
// tool-neutral REST API. Each row lists namespace:read first, which the wrapper
// checks before the action of the route.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, rt := range h.routes {
		actions := make([]string, 0, 1+len(rt.Actions))
		actions = append(actions, "namespace:read")

		for _, action := range rt.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: rt.Method, Path: rt.Path, Actions: actions, NamespaceActions: []string{"namespace:read"},
			Public: rt.Scope == route.ScopePublic, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels())
}
