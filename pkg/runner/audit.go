package runner

import (
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// Actions of runner pools and runners, beside job.ActionNamespaceManageRunners
// and job.ActionRunnerClaim. Each comment names the object of the action.
const (
	// ActionNamespaceReadRunners allows reading the pools, the runners and
	// the join tokens of a namespace, with no secret. Its object is a
	// namespace.
	ActionNamespaceReadRunners = identity.ActionNamespaceReadRunners
	// ActionRunnerJoin allows registering a runner key in a pool. Only a
	// join token holds it. Its object is a runner pool.
	ActionRunnerJoin = identity.ActionRunnerJoin
)

// Event names one write of this package that appends an audit entry.
type Event string

// The events that append an audit entry.
const (
	EventPoolCreated      Event = "pool-created"
	EventPoolChanged      Event = "pool-changed"
	EventPoolDeleted      Event = "pool-deleted"
	EventJoinTokenCreated Event = "join-token-created" //nolint:gosec // An event name, not a credential.
	EventJoinTokenRevoked Event = "join-token-revoked" //nolint:gosec // An event name, not a credential.
	EventRunnerJoined     Event = "runner-joined"
	// EventRunnerRejoined is a join again: the runner keeps its ID and gets
	// a new key. Its claims with no job session end. The launchers of its
	// job sessions run until they end.
	EventRunnerRejoined Event = "runner-rejoined"
	EventKeyRotated     Event = "runner-key-rotated"
	EventRunnerDrained  Event = "runner-drained"
	EventRunnerRetired  Event = "runner-retired"
	// EventCredentialsIssued is the hand of the secrets of a profile, or of
	// a job token, to the session of a claim. One entry for each step that
	// gets them.
	EventCredentialsIssued Event = "credentials-issued"
	// EventHandoffRefused is a handoff token that a coordinator refused to
	// a pod, or a handoff token or a proof that the server refused. Its
	// outcome is denied.
	EventHandoffRefused Event = "handoff-refused"
)

// AuditRule is the action and the kind of the audit entry of one event.
// The subject is the principal of the request, or the runner or the
// session for the events of the protocol. The object is the pool, or the
// unit of the job for EventCredentialsIssued.
type AuditRule struct {
	Action identity.Action
	Kind   audit.Kind
}

// AuditRules maps each event to the action and the kind of its audit
// entry. The entries hold IDs, names, releases and key IDs. They never
// hold a secret: no token, no private key, no credential value.
func AuditRules() map[Event]AuditRule {
	manage := AuditRule{Action: job.ActionNamespaceManageRunners, Kind: audit.KindChange}

	return map[Event]AuditRule{
		EventPoolCreated:       manage,
		EventPoolChanged:       manage,
		EventPoolDeleted:       manage,
		EventJoinTokenCreated:  manage,
		EventJoinTokenRevoked:  manage,
		EventRunnerDrained:     manage,
		EventRunnerRetired:     manage,
		EventRunnerJoined:      {Action: ActionRunnerJoin, Kind: audit.KindChange},
		EventRunnerRejoined:    {Action: ActionRunnerJoin, Kind: audit.KindChange},
		EventKeyRotated:        {Action: job.ActionRunnerClaim, Kind: audit.KindChange},
		EventCredentialsIssued: {Action: job.ActionRunnerClaim, Kind: audit.KindReveal},
		EventHandoffRefused:    {Action: job.ActionRunnerClaim, Kind: audit.KindChange},
	}
}
