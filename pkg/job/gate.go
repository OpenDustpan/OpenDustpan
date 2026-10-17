package job

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// DefaultApprovalTimeout is the time that a job waits for approval before it
// expires, when the decision names no time.
const DefaultApprovalTimeout = 7 * 24 * time.Hour

// DefaultStateTimeout is the time that a write-back job waits for the state
// version that closes it before it expires.
const DefaultStateTimeout = 14 * 24 * time.Hour

// Gate is what the dispatch of a phase needs, from the decision at start.
// The zero Gate needs nothing.
type Gate struct {
	// Approvals is the number of approvals that the phase needs. Zero needs
	// none.
	Approvals int
	// Groups limits the approvers to members of these groups. Empty allows
	// any principal with the approve action on the unit.
	Groups []string
	// AllowSelf lets the principal that started the job approve it.
	AllowSelf bool
	// Timeout is the time from the start of the wait to its expiry. Zero
	// takes DefaultApprovalTimeout.
	Timeout time.Duration
	// BindPlan binds each approval to the hash of the plan of the phase
	// before. The apply phase refuses a plan with another hash.
	BindPlan bool
}

// Verdict is the answer of one approver.
type Verdict string

// The verdicts of an approval.
const (
	VerdictApprove Verdict = "approve"
	VerdictReject  Verdict = "reject"
)

// Approval is one principal's verdict on the gate of one phase.
type Approval struct {
	Subject string
	Verdict Verdict
	Comment string
	// PlanSHA256 is the hash of the plan that the approver saw, when the
	// gate binds to a plan.
	PlanSHA256 string
	At         time.Time
}

// GateStatus is the result of the approvals of a gate so far.
type GateStatus string

// The results of a gate.
const (
	// GateWaiting: the gate needs more approvals.
	GateWaiting GateStatus = "waiting"
	// GateOpen: the gate has the approvals that it needs.
	GateOpen GateStatus = "open"
	// GateRejected: an approver rejected the job.
	GateRejected GateStatus = "rejected"
)

// Status returns the result of approvals for g, for a job that startedBy
// started. One rejection rejects. Each subject counts once. An approval by
// startedBy counts only when g allows it. Status does not check groups: the
// service checks the groups of each approver when it records the approval.
func (g Gate) Status(approvals []Approval, startedBy string) GateStatus {
	seen := make(map[string]bool, len(approvals))

	for _, a := range approvals {
		if a.Verdict == VerdictReject {
			return GateRejected
		}

		if a.Subject == startedBy && !g.AllowSelf {
			continue
		}

		seen[a.Subject] = true
	}

	if len(seen) >= g.Approvals {
		return GateOpen
	}

	return GateWaiting
}

// DecisionInput is what the policy sees at the start of a job. It holds no
// secret value.
type DecisionInput struct {
	Principal identity.Principal
	Spec      Spec
	// Keys are the key paths that the edits of a write-back job change.
	Keys []string
}

// Decision is the result of a policy evaluation at start. Allow false
// denies the start, and Reason tells why. Gates maps a phase kind to its
// gate. A phase kind with no entry needs nothing.
type Decision struct {
	Allow  bool
	Reason string
	Gates  map[PhaseKind]Gate
	// PolicyVersion names the policy that decided, for the audit entry.
	PolicyVersion string
}

// Decider is the port of the policy check of a job start. With no policy
// installed, the adapter allows every start and adds no gate. The gates of
// the decision add to the approvals that the forge asks for. They never
// apply to a change that a team makes and applies in its own CI.
type Decider interface {
	// Decide returns the decision for in. An error is a failure of the
	// policy, not a denial: the start then fails and nothing changes.
	Decide(ctx context.Context, in DecisionInput) (Decision, error)
}

// Freeze is a change freeze of a namespace. While it is active, no phase
// that changes resources, state or the code in git dispatches, unless its job
// started with break-glass. A phase that runs when the freeze starts runs
// to its end. Plans still run. A freeze never stops a state write from a
// tool: the state of real resources is never refused.
type Freeze struct {
	// ID has the prefix frz-.
	ID        string
	Namespace namespace.Name
	Reason    string
	StartsAt  time.Time
	// EndsAt is the end of the freeze, or the zero time for a freeze that
	// lasts until a principal ends it.
	EndsAt    time.Time
	CreatedBy string
	// EndedBy is the subject that ended the freeze early, or empty.
	EndedBy string
}

// ActiveAt reports whether f is active at t.
func (f Freeze) ActiveAt(t time.Time) bool {
	if t.Before(f.StartsAt) {
		return false
	}

	return f.EndsAt.IsZero() || t.Before(f.EndsAt)
}

// Validate returns ErrInvalid for a freeze with no namespace, no reason or
// no start, or with an end that is not after its start.
func (f Freeze) Validate() error {
	switch {
	case f.Namespace == "":
		return fmt.Errorf("%w: a freeze needs a namespace", ErrInvalid)
	case f.Reason == "":
		return fmt.Errorf("%w: a freeze needs a reason", ErrInvalid)
	case f.StartsAt.IsZero():
		return fmt.Errorf("%w: a freeze needs a start time", ErrInvalid)
	case !f.EndsAt.IsZero() && !f.EndsAt.After(f.StartsAt):
		return fmt.Errorf("%w: the end of a freeze must be after its start", ErrInvalid)
	default:
		return nil
	}
}

// FreezeStore is the persistence port for change freezes. A namespace has
// no freeze until a principal creates one. Every write checks fence and
// appends e in its transaction. It returns namespace.ErrFenced for a stale
// fence.
type FreezeStore interface {
	// CreateFreeze persists f. It returns ErrInvalid for a freeze that fails
	// Validate.
	CreateFreeze(ctx context.Context, f Freeze, fence namespace.Fence, e audit.Entry) error
	// EndFreeze sets the end of the freeze id to now on the clock of the
	// store, and records by. It returns ErrNotFound for an unknown ID, and
	// ErrFinished for a freeze that already ended.
	EndFreeze(ctx context.Context, id, by string, fence namespace.Fence, e audit.Entry) error
	// ListFreezes returns the freezes of ns that end after since, or never
	// end, ordered by start.
	ListFreezes(ctx context.Context, ns namespace.Name, since time.Time) ([]Freeze, error)
}
