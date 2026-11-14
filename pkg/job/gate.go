package job

import (
	"context"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// DefaultApprovalTimeout is the time that a job waits for approval before it
// expires, when the decision names no time.
const DefaultApprovalTimeout = 7 * 24 * time.Hour

// DefaultStateTimeout is the time that a write-back job waits for the state
// version that closes it before it expires.
const DefaultStateTimeout = 14 * 24 * time.Hour

// Limits of open jobs. Installation settings can change them later. A start
// above a count limit fails with ErrLimit. A job above a time limit
// expires.
const (
	// MaxOpenPerUnit is the most jobs of one unit that are not terminal.
	// The start of one more fails with ErrLimit.
	MaxOpenPerUnit = 16
	// MaxOpenPerNamespace is the most jobs of one namespace that are not
	// terminal.
	MaxOpenPerNamespace = 1000
	// DefaultQueueTimeout is the time that a job stays queued, for example
	// behind a busy unit or a freeze, before it expires.
	DefaultQueueTimeout = 7 * 24 * time.Hour
	// DefaultDispatchTimeout is the time that a phase stays dispatched with
	// no claim, for example while no runner of its pool is online, before
	// its job expires.
	DefaultDispatchTimeout = 24 * time.Hour
)

// ConfirmFreshness is the maximum age of the fresh authentication of a
// confirmation: the longest time from the MFA step, or from the new
// authentication at the SSO provider, to the confirmation that the person
// records. It is the same number as the default freshness window for a
// secret reveal and for a token from an SSO session.
const ConfirmFreshness = 15 * time.Minute

// ConfirmAuth is the fresh authentication that a person gives for a
// confirmation.
type ConfirmAuth string

// The fresh authentications of a confirmation.
const (
	// ConfirmAuthMFA is a new step of the built-in MFA, where MFA covers
	// the person.
	ConfirmAuthMFA ConfirmAuth = "mfa"
	// ConfirmAuthSSO is a new authentication at the SSO identity provider
	// of the person, asked with a maximum age, where MFA does not cover the
	// person. Its time is the authentication time that the provider gives.
	ConfirmAuthSSO ConfirmAuth = "sso"
)

// Valid reports whether c is one of the fresh authentications.
func (c ConfirmAuth) Valid() bool {
	return c == ConfirmAuthMFA || c == ConfirmAuthSSO
}

// GateType names who can pass a gate.
type GateType string

// The types of a gate.
const (
	// GateApproval comes from the policy decision. Approvers with the
	// approve action pass it, and the starter counts only when AllowSelf
	// is true.
	GateApproval GateType = "approval"
	// GateConfirmation is in front of each phase that changes, for a job
	// that an agent started in AgentModeApproveFirst. Only the person who
	// asked the agent passes it, with a request of their own (no agent) and
	// a fresh authentication (ConfirmAuth). It applies also with no policy
	// installed. A policy
	// never gives it and never removes it.
	GateConfirmation GateType = "confirmation"
)

// Gate is what the dispatch of a phase needs. The zero Gate of type
// GateApproval needs nothing.
type Gate struct {
	// Type names who can pass the gate. Empty is GateApproval.
	Type GateType
	// Approvals is the number of approvals that a gate of type GateApproval
	// needs. Zero needs none. A confirmation gate needs one confirmation.
	Approvals int
	// Groups holds the names of the approver groups of the gate. Empty names
	// no group. This version does not check the groups of an approver: it does
	// not record approvals (see Service.Approve).
	Groups []string
	// GroupIDs holds the immutable ID of each group in Groups, at the same
	// index. No check of the approvers runs in this version. A check of the
	// approvers must match a member by these IDs, never by the names: a group
	// that someone made again with the same name has another ID. Groups is for
	// display only.
	GroupIDs []string
	// AllowSelf lets the principal that started the job approve it. It has
	// no effect on a confirmation gate.
	AllowSelf bool
	// Timeout is the time from the start of the wait to its expiry. Zero
	// takes DefaultApprovalTimeout.
	Timeout time.Duration
	// BindPlan binds each approval to the plan of the phase before. The
	// apply phase refuses a plan with another hash.
	BindPlan bool
}

// ConfirmationGate returns the gate of type GateConfirmation. bindPlan
// binds the confirmation to the plan of the phase before, when there is
// one.
func ConfirmationGate(bindPlan bool) Gate {
	return Gate{
		Type: GateConfirmation, Approvals: 1, Groups: nil, GroupIDs: nil, AllowSelf: false, Timeout: 0,
		BindPlan: bindPlan,
	}
}

// PolicyGates returns the gates of each phase of kinds, in order: the gate
// that d gives for the kind, as a gate of type GateApproval. A phase with no
// gate in d gets none.
func PolicyGates(kinds []PhaseKind, d Decision) [][]Gate {
	out := make([][]Gate, len(kinds))

	for i, k := range kinds {
		if g, ok := d.Gates[k]; ok {
			g.Type = GateApproval
			out[i] = append(out[i], g)
		}
	}

	return out
}

// WithConfirmations returns gates, the gates of each phase of kinds, with a
// confirmation gate added in front of each phase that changes. A
// confirmation gate binds to the plan when a plan phase comes before its
// phase. The service calls it when CheckAgent asks for confirmations.
func WithConfirmations(kinds []PhaseKind, gates [][]Gate) [][]Gate {
	out := make([][]Gate, len(kinds))

	for i, k := range kinds {
		if i < len(gates) {
			out[i] = slices.Clone(gates[i])
		}

		if k.Changes() {
			out[i] = append(out[i], ConfirmationGate(i > 0 && kinds[i-1] == PhasePlan))
		}
	}

	return out
}

// Verdict is the answer of one approver.
type Verdict string

// The verdicts of an approval.
const (
	VerdictApprove Verdict = "approve"
	VerdictReject  Verdict = "reject"
)

// Approval is one principal's verdict on a gate of one phase.
type Approval struct {
	// Gate is the type of the gate that the verdict is for. Empty is
	// GateApproval.
	Gate    GateType
	Subject string
	// Agent names the agent that recorded the verdict for Subject, or is
	// empty. An agent can record a rejection, never an approval.
	Agent   string
	Verdict Verdict
	Comment string
	// Plan is what the approver saw, when the gate binds to a plan: the
	// hashes of the readable plan and of the plan to apply, and the state
	// version that they read.
	Plan *PlanBinding
	At   time.Time
	// Auth is the fresh authentication of a confirmation, or empty for a
	// verdict on a gate of type GateApproval.
	Auth ConfirmAuth
	// AuthAt is the time of the fresh authentication of a confirmation:
	// the MFA step, or the authentication at the SSO provider. A
	// confirmation counts only when Auth is valid and At - AuthAt is at
	// most ConfirmFreshness.
	AuthAt time.Time
}

// typeOf returns the gate type t, with empty as GateApproval.
func typeOf(t GateType) GateType {
	if t == "" {
		return GateApproval
	}

	return t
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
// started. policy decides which signatures count: an approval or a
// confirmation of a subject that policy.CountsAsSignature refuses never
// counts, so a dev subject that a backup carried into a production
// installation fills no slot. A rejection counts for every subject.
// One rejection of any gate rejects. Only the verdicts for the
// type of g count. A verdict that an agent records never counts as an
// approval or a confirmation.
//
// A gate of type GateApproval: each subject counts once, and an approval
// by startedBy counts only when g allows it. A user who started the job
// through an agent is the starter too. Status does not check groups. This
// version does not record approvals, so no code checks the groups of an
// approver.
//
// A gate of type GateConfirmation opens with one confirmation by startedBy
// with a fresh authentication at most ConfirmFreshness before it.
func (g Gate) Status(approvals []Approval, startedBy string, policy gate.ApproverPolicy) GateStatus {
	passed := 0
	seen := make(map[string]bool, len(approvals))

	for i := range approvals {
		a := &approvals[i]
		if a.Verdict == VerdictReject {
			return GateRejected
		}

		if g.counts(a, startedBy, policy) && !seen[a.Subject] {
			seen[a.Subject] = true
			passed++
		}
	}

	need := g.Approvals
	if typeOf(g.Type) == GateConfirmation {
		need = 1
	}

	if passed >= need {
		return GateOpen
	}

	return GateWaiting
}

// counts reports whether the approval a counts for g, in a job that
// startedBy started.
func (g Gate) counts(a *Approval, startedBy string, policy gate.ApproverPolicy) bool {
	if a.Agent != "" || a.Verdict != VerdictApprove || typeOf(a.Gate) != typeOf(g.Type) {
		return false
	}

	// Defense in depth: the strip of a restore removes dev subjects from the
	// rules, and the write paths refuse them. This check stays so that a
	// signature that a backup already holds fills no slot on production.
	if !policy.CountsAsSignature(a.Subject) {
		return false
	}

	if typeOf(g.Type) == GateConfirmation {
		return a.Subject == startedBy && freshAuth(a)
	}

	return a.Subject != startedBy || g.AllowSelf
}

// freshAuth reports whether a has a valid fresh authentication at most
// ConfirmFreshness before a, and not after it.
func freshAuth(a *Approval) bool {
	return a.Auth.Valid() && !a.AuthAt.IsZero() && !a.AuthAt.After(a.At) &&
		a.At.Sub(a.AuthAt) <= ConfirmFreshness
}

// GatesStatus returns the result of approvals for all gates of one
// phase: rejected when a verdict rejects, open when every gate is open,
// and waiting otherwise. A phase with no gate is open.
func GatesStatus(gates []Gate, approvals []Approval, startedBy string, policy gate.ApproverPolicy) GateStatus {
	status := GateOpen

	for _, g := range gates {
		st := g.Status(approvals, startedBy, policy)
		if st == GateRejected {
			return GateRejected
		}

		if st == GateWaiting {
			status = GateWaiting
		}
	}

	return status
}

// AgentMode is how a job that an agent starts reaches the code or the
// resources of a unit. The user sets it for the agent.
type AgentMode string

// The agent modes.
const (
	// AgentModeApproveFirst puts a confirmation gate in front of each phase
	// that changes. Only the person who asked the agent passes it, with a
	// fresh authentication. It is the default. It is not available to a
	// person whom the server cannot ask for a fresh authentication
	// (Actor.ConfirmAuth is empty), for example in an installation with
	// static tokens only: such a person uses AgentModeReviewRequest.
	AgentModeApproveFirst AgentMode = "approve-first"
	// AgentModeReviewRequest lets an agent change a unit only through a
	// write-back job that delivers as a review request. The review of the
	// forge is the check of a person. A direct push and a runner job that
	// changes resources or state (apply, destroy, refresh) fail with
	// ErrAgentDenied. A runner plan is allowed in every mode.
	AgentModeReviewRequest AgentMode = "review-request"
	// AgentModeDirect lets an agent change a unit with no confirmation. It
	// needs all three switches of DirectSwitches.
	AgentModeDirect AgentMode = "direct"
)

// Valid reports whether m is one of the agent modes of this package. The
// empty mode is not one of them: it tells that no agent started the job.
func (m AgentMode) Valid() bool {
	switch m {
	case AgentModeApproveFirst, AgentModeReviewRequest, AgentModeDirect:
		return true
	default:
		return false
	}
}

// DirectSwitches are the three settings that allow AgentModeDirect. Each
// is off by default, and each is a separate opt-in: the installation
// setting of the server, the setting of the namespace of the unit, and
// the setting of the user who asks the agent.
type DirectSwitches struct {
	Server    bool
	Namespace bool
	User      bool
}

// CheckAgent returns what the agent mode of a needs for the start of
// spec. confirm is true when each phase that changes needs a confirmation
// gate (WithConfirmations). It returns false and nil for a start with no agent,
// and for a job that changes nothing (a runner plan), in every mode. It
// returns an error that wraps:
//   - ErrAgentDenied in AgentModeApproveFirst, when the server cannot ask
//     the person for a fresh authentication (a.ConfirmAuth is not valid),
//   - ErrAgentDenied in AgentModeReviewRequest, for a direct push or a
//     runner job that changes (apply, destroy, refresh),
//   - ErrAgentDenied in AgentModeDirect, when a switch of sw is off,
//   - ErrInvalid for an unknown mode.
func CheckAgent(spec *Spec, a Actor, sw DirectSwitches) (bool, error) {
	if a.Agent == "" || !spec.Changes() {
		return false, nil
	}

	switch a.Mode {
	case "", AgentModeApproveFirst:
		return checkApproveFirst(a)
	case AgentModeReviewRequest:
		if spec.Executor == ExecutorWriteBack && spec.Delivery != DeliveryDirectPush {
			return false, nil
		}

		return false, fmt.Errorf("%w: agent %s works in mode %s and delivers only as a review request",
			ErrAgentDenied, a.Agent, a.Mode)
	case AgentModeDirect:
		if !sw.Server || !sw.Namespace || !sw.User {
			return false, fmt.Errorf("%w: agent %s: direct changes need the server, the namespace and the user "+
				"to allow them (server %t, namespace %t, user %t)", ErrAgentDenied, a.Agent, sw.Server,
				sw.Namespace, sw.User)
		}

		return false, nil
	default:
		return false, fmt.Errorf("%w: unknown agent mode %q", ErrInvalid, a.Mode)
	}
}

// checkApproveFirst returns true when the server can ask the person of a
// for the fresh authentication of a confirmation, and ErrAgentDenied when
// it cannot.
func checkApproveFirst(a Actor) (bool, error) {
	if !a.ConfirmAuth.Valid() {
		return false, fmt.Errorf("%w: agent %s: a confirmation needs a fresh MFA step or SSO sign-in, "+
			"and the server cannot ask %s for one; use the mode %s", ErrAgentDenied, a.Agent,
			a.Principal.Subject, AgentModeReviewRequest)
	}

	return true, nil
}

// DecisionInput is what the policy sees at the start of a job. It holds no
// secret value. Agent names the agent that acts for Principal, or is
// empty, so a policy can allow or gate a start by agent.
type DecisionInput struct {
	Principal identity.Principal
	Agent     string
	// AgentMode is the mode of Agent, or empty.
	AgentMode AgentMode
	// Source tells where the start came from.
	Source SourceKind
	Spec   Spec
	// Keys are the key paths that the edits of a write-back job change.
	Keys []string
}

// Decision is the result of a policy evaluation at start. Allow false
// denies the start, and Reason tells why. Gates maps a phase kind to its
// approval gate. A phase kind with no entry needs no approval. A decision
// never gives or removes a confirmation gate (PolicyGates).
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
