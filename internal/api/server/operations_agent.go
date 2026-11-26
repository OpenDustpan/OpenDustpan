package server

import (
	"encoding/json"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// The tag of the model connection operations.
const tagAgent = "agent"

// agentDocumentSchema declares the schema of the connection document on the
// wire. The declaration matches the one of the same type in the other
// adapters: a declaration of one type must be equal everywhere.
func agentDocumentSchema() apidesc.Declaration {
	return apidesc.Declare[json.RawMessage](map[string]any{
		schemaDescription: "Any JSON value.",
	})
}

// agentOperationLabels names the model connection routes in the description:
// the secret items, the current document, the formats and the egress
// allowlist of the installation. The paths stay outside the prefix of the
// tool-neutral REST API: the API description keeps these paths, and clients
// depend on them.
func agentOperationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodPost, agentItemsPath): apidesc.Name("createAgentSecret", tagAgent).
			WithStatus(http.StatusCreated).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[createAgentSecretRequest]("CreateAgentSecretRequest"),
				apidesc.Typed[agentSecretResponse]("AgentSecret"),
			),
		apidesc.Pattern(http.MethodGet, agentItemsPath): apidesc.Name("listAgentSecrets", tagAgent).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.NoBody,
				apidesc.Typed[agentSecretListResponse](
					"ListAgentSecretsResponse", apidesc.Named[agentSecretResponse]("AgentSecret"),
				),
			),
		apidesc.Pattern(http.MethodPut, agentItemPath): apidesc.Name("replaceAgentSecret", tagAgent).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[replaceAgentSecretRequest]("ReplaceAgentSecretRequest"),
				apidesc.Typed[agentSecretResponse]("AgentSecret"),
			),
		apidesc.Pattern(http.MethodDelete, agentItemPath): apidesc.Name("deleteAgentSecret", tagAgent).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.NoBody),
		apidesc.Pattern(http.MethodGet, agentConnectionPath): apidesc.Name("getAgentConnection", tagAgent).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.NoBody,
				apidesc.Typed[agentConnectionResponse]("AgentConnection", agentDocumentSchema()),
			),
		apidesc.Pattern(http.MethodDelete, agentConnectionPath): apidesc.Name("deleteAgentConnection", tagAgent).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.NoBody),
		apidesc.Pattern(http.MethodGet, agentFormatsPath): apidesc.Name("listAgentFormats", tagAgent).
			On(apidesc.KindServer).
			WithBodies(apidesc.NoBody, apidesc.Typed[agentFormatsResponse]("AgentFormats")),
		apidesc.Pattern(http.MethodGet, egressAllowlistPath): apidesc.Name("getEgressAllowlist", tagSettings).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.NoBody,
				apidesc.Typed[egressAllowlistResponse](
					"EgressAllowlist", apidesc.Named[cidrRuleResponse]("EgressAllowlistRule"),
				),
			),
		apidesc.Pattern(http.MethodPut, egressAllowlistPath): apidesc.Name("updateEgressAllowlist", tagSettings).
			On(apidesc.KindServer).
			WithBodies(
				apidesc.Typed[egressAllowlistRequest](
					"UpdateEgressAllowlistRequest", apidesc.Named[cidrRuleRequest]("EgressAllowlistRuleRequest"),
				),
				apidesc.Typed[egressAllowlistResponse](
					"EgressAllowlist", apidesc.Named[cidrRuleResponse]("EgressAllowlistRule"),
				),
			),
	}
}
