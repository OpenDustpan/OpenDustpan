package agent

import "opendustpan.org/opendustpan/pkg/identity"

// The actions of the agent. Package identity holds their strings. Each one
// names its object.
const (
	// ActionAgentUse allows the agent and outside agents to act for the
	// holder in a namespace. Every tool call that touches an object in a
	// namespace checks it on that namespace, in addition to the action of
	// the tool. Its object is a namespace. The built-in roles that read a
	// namespace hold it, so an administrator removes it with a custom role.
	ActionAgentUse = identity.ActionAgentUse
	// ActionAgentStartRun allows a person to start a run of the built-in
	// agent and to read, continue and cancel its own runs. Its object is
	// CallerObject. Every signed-in person holds it with no grant while the
	// agent is on.
	ActionAgentStartRun = identity.ActionAgentStartRun
	// ActionNamespaceManageAgentRuns allows reading and canceling the runs
	// of other persons that started in a namespace. Its object is a
	// namespace.
	ActionNamespaceManageAgentRuns = identity.ActionNamespaceManageAgentRuns
	// ActionNamespaceManageAgent allows changing the agent settings of a
	// namespace: the opt-in for direct changes, the opt-in for secret
	// values, the default change mode and lower limits. Its object is a
	// namespace.
	ActionNamespaceManageAgent = identity.ActionNamespaceManageAgent
	// ActionServerManageAgent allows connecting and disconnecting a model,
	// setting the limits of the installation and reading the token use of
	// every run. Its object is ServerObject.
	ActionServerManageAgent = identity.ActionServerManageAgent
	// ActionServerAcceptAgentSecretAccess allows the acceptance that opens
	// the secret gate after a start with the secret access flag. Its object
	// is ServerObject. Of the built-in roles, only the site administrator
	// holds it.
	ActionServerAcceptAgentSecretAccess = identity.ActionServerAcceptAgentSecretAccess
	// ActionCallerManageAgentOptIns allows a person to set its own opt-ins:
	// direct changes and secret values. Its object is CallerObject. Every
	// signed-in person holds it with no grant.
	ActionCallerManageAgentOptIns = identity.ActionCallerManageAgentOptIns
)

// The actions of jobs that the tools check. Package identity holds their
// strings, and package job owns their rules.
const (
	// ActionJobRead allows reading the jobs of a unit. Its object is a unit.
	ActionJobRead = identity.ActionJobRead
	// ActionJobReadLogs allows reading the content of job logs and of the
	// events of an update. Its object is a unit.
	ActionJobReadLogs = identity.ActionJobReadLogs
	// ActionJobCancel allows canceling a job. Its object is a unit.
	ActionJobCancel = identity.ActionJobCancel
	// ActionUnitRunPlan, ActionUnitRunApply, ActionUnitRunDestroy and
	// ActionUnitWriteBack start jobs. The job model returns the ones that a
	// start needs. Their object is a unit.
	ActionUnitRunPlan    = identity.ActionUnitRunPlan
	ActionUnitRunApply   = identity.ActionUnitRunApply
	ActionUnitRunDestroy = identity.ActionUnitRunDestroy
	ActionUnitWriteBack  = identity.ActionUnitWriteBack
)

// ActionUnitObserve allows reading the live status of the resources of a
// unit from its cloud: events of workloads and the health of targets. Its
// object is a unit. It is a read, but it reaches the cloud account of the
// unit with short-lived read-only credentials, so it has its own action.
const ActionUnitObserve = identity.ActionUnitObserve
