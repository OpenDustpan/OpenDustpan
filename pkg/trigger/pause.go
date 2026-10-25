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
	// principal of the trigger needs.
	PauseGrantRevoked PauseReason = "grant-revoked"
	// PauseGrantMissing is a start that the Authorizer refused with an
	// identity.DeniedError whose cause is identity.DenyNoGrant, for the
	// action of the purpose, on a start by the server-made principal of the
	// trigger. No other refusal pauses a trigger: a refusal by a code rule,
	// a refusal that failed closed, a refusal with no cause, and a switch
	// that is off each fail the start and write its audit entry only.
	PauseGrantMissing PauseReason = "grant-missing"
)

// Pause records that one trigger starts no plan, and why. Action is the
// action that the purpose needs and that no current grant gives. GrantID is
// the ID of the revoked grant, and is empty for PauseGrantMissing. By is the
// subject of the principal that revoked the grant, or the subject of the
// server for PauseGrantMissing. A pause does not delete the trigger and
// does not change its fields. A save of the trigger that makes the grant
// again ends the pause of that trigger only.
type Pause struct {
	At        time.Time
	Namespace namespace.Name
	Trigger   ID
	Reason    PauseReason
	Action    identity.Action
	GrantID   string
	By        string
}

// PauseStore keeps the pauses of the triggers, one at most for each
// trigger. Every write carries the fence of the namespace, checks it in the
// transaction of the write, and appends its audit entry in that transaction
// (fail closed). On every error the store changes nothing.
//
// A pause makes the reason visible. It is not the check that stops a start:
// the Authorizer refuses the start of a principal with no grant, also when
// no pause exists. The dispatcher refuses a start of a paused trigger with
// ErrPaused before it asks the Authorizer.
type PauseStore interface {
	// Pause records p for the trigger p.Trigger of the namespace
	// p.Namespace and appends the entry e. It returns true when it paused
	// the trigger. A trigger that is paused already keeps its pause: the
	// call returns false, makes no change and appends no entry. It returns
	// ErrNotFound for an unknown trigger, ErrInvalid for a pause that fails
	// validation, and namespace.ErrFenced.
	Pause(ctx context.Context, p Pause, fence namespace.Fence, e audit.Entry) (bool, error)
	// Paused returns the pause of the trigger id of the namespace ns, or
	// ErrNotFound when the trigger is not paused.
	Paused(ctx context.Context, ns namespace.Name, id ID) (Pause, error)
	// ListPaused returns the pauses of the triggers of unit, ordered by
	// trigger ID. A unit with no paused trigger gives an empty list and no
	// error.
	ListPaused(ctx context.Context, unit graph.Unit) ([]Pause, error)
	// Resume ends the pause of the trigger id of the namespace ns and
	// appends the entry e. It returns ErrNotFound when the trigger is not
	// paused, ErrInvalid, and namespace.ErrFenced.
	Resume(ctx context.Context, ns namespace.Name, id ID, fence namespace.Fence, e audit.Entry) error
}

// PurposeGrants makes the server-made principal of a trigger and its grant
// when a person saves the trigger. The access service meets it through the
// same grant path as a grant that a person makes. It is the only path that
// makes a grant to a server-made principal: the grant API refuses such a
// grant when a person asks for it.
type PurposeGrants interface {
	// Ensure makes, or finds, the server-made principal of purpose in the
	// namespace of the served unit, and gives it the grant of the built-in
	// role of the purpose kind on the served unit. owner is the owner that
	// the save gives: the saver, with the source principal.OwnerLastSaver.
	// When the stored owner has the source principal.OwnerTransfer, the
	// stored owner stays, and the escalation check runs against it.
	//
	// The escalation check reads the stored record of the owner and its
	// stored grants only, never a flag of a principal: the owner must hold
	// each action that the purpose kind needs on the unit. The audit entry
	// of each new grant names the server as its subject and reason as its
	// reason. A grant that exists is not made again. It returns the
	// principal with its current owner. It returns an error that wraps
	// access.ErrEscalation when the owner does not hold an action, and the
	// errors of principal.ServerPrincipalStore.
	Ensure(
		ctx context.Context, owner principal.Owner, purpose principal.Purpose, reason string,
	) (principal.ServerPrincipal, error)
}
