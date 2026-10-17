package agent

import "opendustpan.org/opendustpan/pkg/identity"

// The actions of the agent. They are not yet in the action list of package
// identity. Each one names its object.
const (
	// ActionAgentUse allows the agent and outside agents to act for the
	// holder in a namespace. Every tool call that touches an object in a
	// namespace checks it on that namespace, in addition to the action of
	// the tool. Its object is a namespace. The built-in roles that read a
	// namespace hold it, so an administrator removes it with a custom role.
	ActionAgentUse identity.Action = "agent:use"
	// ActionAgentStartRun allows a person to start a run of the built-in
	// agent and to read, continue and cancel its own runs. Its object is
	// CallerObject. Every signed-in person holds it with no grant while the
	// agent is on.
	ActionAgentStartRun identity.Action = "agent:start-run"
	// ActionNamespaceManageAgentRuns allows reading and canceling the runs
	// of other persons that started in a namespace. Its object is a
	// namespace.
	ActionNamespaceManageAgentRuns identity.Action = "namespace:manage-agent-runs"
	// ActionNamespaceManageAgent allows changing the agent settings of a
	// namespace: the opt-in for direct changes, the opt-in for secret
	// values, the default change mode and lower limits. Its object is a
	// namespace.
	ActionNamespaceManageAgent identity.Action = "namespace:manage-agent"
	// ActionServerManageAgent allows connecting and disconnecting a model,
	// setting the limits of the installation and reading the token use of
	// every run. Its object is ServerObject.
	ActionServerManageAgent identity.Action = "server:manage-agent"
	// ActionServerAcceptAgentSecretAccess allows the acceptance that opens
	// the secret gate after a start with the secret access flag. Its object
	// is ServerObject. Of the built-in roles, only the site administrator
	// holds it.
	ActionServerAcceptAgentSecretAccess identity.Action = "server:accept-agent-secret-access"
	// ActionCallerManageAgentOptIns allows a person to set its own opt-ins:
	// direct changes and secret values. Its object is CallerObject. Every
	// signed-in person holds it with no grant.
	ActionCallerManageAgentOptIns identity.Action = "caller:manage-agent-opt-ins"
)
