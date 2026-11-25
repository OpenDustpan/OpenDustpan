package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// AgentConnectionAllowlistInvalid means that the egress allowlist is not
	// valid.
	AgentConnectionAllowlistInvalid = "agent-connection.allowlist-invalid"

	// AgentConnectionNotConnected means that the installation has no current
	// model connection document.
	AgentConnectionNotConnected = "agent-connection.not-connected"

	// AgentConnectionSecretInUse means that a stored secret item is still
	// referenced by a connection document.
	AgentConnectionSecretInUse = "agent-connection.secret-in-use"

	// AgentConnectionSecretInvalid means that a stored secret item does not
	// have a valid name or value.
	AgentConnectionSecretInvalid = "agent-connection.secret-invalid"

	// AgentConnectionSecretNotFound means that a stored secret item does not
	// exist.
	AgentConnectionSecretNotFound = "agent-connection.secret-not-found"
)

// agentConnectionSpecs are the codes of the model connection document and
// its secret items.
func agentConnectionSpecs() []Spec {
	return []Spec{
		{
			Code:    AgentConnectionAllowlistInvalid,
			Meaning: "the egress allowlist is not valid",
			Args:    nil,
		},
		{
			Code:    AgentConnectionNotConnected,
			Meaning: "the installation has no current model connection document",
			Args:    nil,
		},
		{
			Code:    AgentConnectionSecretInUse,
			Meaning: "a stored secret item is still referenced by a connection document",
			Args:    nil,
		},
		{
			Code:    AgentConnectionSecretInvalid,
			Meaning: "a stored secret item does not have a valid name or value",
			Args:    nil,
		},
		{
			Code:    AgentConnectionSecretNotFound,
			Meaning: "a stored secret item does not exist",
			Args:    nil,
		},
	}
}
