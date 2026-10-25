package trigger

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// ErrPaused reports a start of a trigger that is paused. The start makes no
// job.
var ErrPaused = errors.New("trigger is paused")

// PauseReason names why a trigger is paused.
type PauseReason string

// The reasons of a pause.
const (
	// PauseGrantRevoked is a revoke of a grant that the server-made
	// principal of the purpose of the trigger needs.
	PauseGrantRevoked PauseReason = "grant-revoked"
	// PauseGrantMissing is a start that the Authorizer refused because the
	// server-made principal of the purpose did not hold a grant that the
	// purpose needs.
	PauseGrantMissing PauseReason = "grant-missing"
)

// Pause records that one trigger starts no plan, and why. Action is the
// action that the purpose needs and that no current grant gives. GrantID is
// the ID of the revoked grant, and is empty for PauseGrantMissing. By is the
// subject of the principal that revoked the grant, or the subject of the
// server for PauseGrantMissing. A pause does not delete the trigger and
// does not change its fields: a new save by an owner that holds the action
// ends the pause.
type Pause struct {
	At      time.Time
	Trigger ID
	Reason  PauseReason
	Action  identity.Action
	GrantID string
	By      string
}

// PauseStore keeps the pauses of the triggers. Every write carries the fence
// of the namespace, checks it in the transaction of the write, and appends
// its audit entry in that transaction (fail closed). On every error the store
// changes nothing.
//
// A pause makes the reason visible. It is not the check that stops a start:
// the Authorizer refuses the start of a principal with no grant, also when
// no pause exists. The dispatcher refuses a start of a paused trigger with
// ErrPaused before it asks the Authorizer.
type PauseStore interface {
	// PauseNeeding pauses every trigger of unit whose purpose kind is kind
	// and that is not paused yet, records p for each with its trigger ID,
	// and appends the entry e once. It returns the IDs of the triggers that
	// it paused, ordered by ID. A unit with no such trigger gives an empty
	// list and no error. It returns ErrInvalid for a unit or a pause that
	// fails validation, and namespace.ErrFenced.
	PauseNeeding(
		ctx context.Context, unit graph.Unit, kind principal.PurposeKind, p Pause, fence namespace.Fence, e audit.Entry,
	) ([]ID, error)
	// Paused returns the pauses of the triggers of unit, ordered by trigger
	// ID. A unit with no paused trigger gives an empty list and no error.
	Paused(ctx context.Context, unit graph.Unit) ([]Pause, error)
	// Resume ends the pause of the trigger id of the namespace ns and
	// appends the entry e. It returns ErrNotFound when the trigger is not
	// paused, ErrInvalid, and namespace.ErrFenced.
	Resume(ctx context.Context, ns namespace.Name, id ID, fence namespace.Fence, e audit.Entry) error
}

// PurposeGrants makes the server-made principal of the purpose of a trigger
// and its grants when a person saves the trigger. The access service meets
// it through the same grant path as a grant that a person makes.
type PurposeGrants interface {
	// Ensure makes, or finds, the server-made principal of purpose in the
	// namespace of the served unit, with owner as its accountable owner, and
	// gives it each action that the purpose kind needs on the served unit.
	// The escalation check runs against the current grants of owner: the
	// owner must hold each such action on the unit. The audit entry of each
	// new grant names the server as its subject and reason as its reason. A
	// grant that exists is not made again. It returns an error that wraps
	// access.ErrEscalation when the owner does not hold an action, and the
	// errors of principal.ServerPrincipalStore.
	Ensure(
		ctx context.Context, owner identity.Principal, purpose principal.Purpose, reason string,
	) (principal.ServerPrincipal, error)
}
