package job

import "fmt"

// State is the life-cycle state of a job.
type State string

// The states of a job. Succeeded, failed, canceled, rejected and expired
// are terminal.
const (
	// StateQueued: the job waits to dispatch its current phase. Wait tells
	// what holds it, if anything.
	StateQueued State = "queued"
	// StateAwaitingApproval: the gate of the current phase needs approvals.
	StateAwaitingApproval State = "awaiting-approval"
	// StateDispatched: the current phase is a durable operation that no
	// executor holds yet.
	StateDispatched State = "dispatched"
	// StateRunning: an executor holds the claim of the current phase.
	StateRunning State = "running"
	// StateCanceling: a cancel arrived while an executor held the claim.
	// The executor stops the phase at its next write.
	StateCanceling State = "canceling"
	// StateAwaitingState: a write-back job delivered its commit and waits
	// for the next state version of the unit.
	StateAwaitingState State = "awaiting-state"
	// StateSucceeded: every phase succeeded. A write-back job also saw the
	// state version that closes it.
	StateSucceeded State = "succeeded"
	// StateFailed: a phase failed, or its executor stopped during a step
	// that changes resources.
	StateFailed State = "failed"
	// StateCanceled: a principal canceled the job.
	StateCanceled State = "canceled"
	// StateRejected: an approver rejected the job, or a person closed its
	// review request without a merge.
	StateRejected State = "rejected"
	// StateExpired: the job waited longer than its limit for approval, for
	// a dispatch, or for the state version.
	StateExpired State = "expired"
)

// States lists every state, in the order of the table above.
func States() []State {
	return []State{
		StateQueued, StateAwaitingApproval, StateDispatched, StateRunning, StateCanceling, StateAwaitingState,
		StateSucceeded, StateFailed, StateCanceled, StateRejected, StateExpired,
	}
}

// Terminal reports whether s is an end state.
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCanceled || s == StateRejected ||
		s == StateExpired
}

// HoldsUnit reports whether a job in state s holds its unit for a change,
// when its current phase changes resources. A job that waits for approval
// or for a state version does not hold the unit: a hold of days would stop
// every other change.
func (s State) HoldsUnit() bool {
	return s == StateDispatched || s == StateRunning || s == StateCanceling
}

// WaitReason tells why a queued job does not dispatch now.
type WaitReason string

// The reasons that hold a queued job.
const (
	// WaitNone: nothing holds the job. The next dispatch takes it.
	WaitNone WaitReason = ""
	// WaitUnitBusy: another job holds the unit for a change.
	WaitUnitBusy WaitReason = "unit-busy"
	// WaitFrozen: a change freeze of the namespace is active and the job has
	// no break-glass.
	WaitFrozen WaitReason = "frozen"
)

// Event is something that happens to a job.
type Event string

// The events of a job.
const (
	// EventDispatch starts the durable operation of the current phase.
	EventDispatch Event = "dispatch"
	// EventNeedApproval: the gate of the current phase needs approvals.
	EventNeedApproval Event = "need-approval"
	// EventApproved: the gate got the approvals that it needs.
	EventApproved Event = "approved"
	// EventRejected: an approver rejected the job, or a person closed its
	// review request without a merge.
	EventRejected Event = "rejected"
	// EventClaim: an executor took the claim of the current phase.
	EventClaim Event = "claim"
	// EventRelease: the executor gave the phase back before it started a
	// step that changes resources, or its claim expired before such a step.
	// The phase dispatches again.
	EventRelease Event = "release"
	// EventPhaseDone: the current phase succeeded, and more phases follow.
	EventPhaseDone Event = "phase-done"
	// EventLastPhaseDone: the last phase of a runner job succeeded.
	EventLastPhaseDone Event = "last-phase-done"
	// EventDelivered: the deliver phase of a write-back job made its commit.
	EventDelivered Event = "delivered"
	// EventPhaseFailed: the current phase failed, or its executor stopped
	// after it started a step that changes resources.
	EventPhaseFailed Event = "phase-failed"
	// EventCancel: a principal asked to cancel the job.
	EventCancel Event = "cancel"
	// EventStateObserved: the state version that closes a write-back job
	// arrived.
	EventStateObserved Event = "state-observed"
	// EventExpire: the job waited longer than its limit.
	EventExpire Event = "expire"
)

// Events lists every event, in the order of the table above.
func Events() []Event {
	return []Event{
		EventDispatch, EventNeedApproval, EventApproved, EventRejected, EventClaim, EventRelease, EventPhaseDone,
		EventLastPhaseDone, EventDelivered, EventPhaseFailed, EventCancel, EventStateObserved, EventExpire,
	}
}

// transitions is the whole state machine. A pair that is not in it is an
// error.
//
//nolint:gochecknoglobals // A fixed table, read only.
var transitions = map[State]map[Event]State{
	StateQueued: {
		EventDispatch:     StateDispatched,
		EventNeedApproval: StateAwaitingApproval,
		EventCancel:       StateCanceled,
		EventExpire:       StateExpired,
	},
	StateAwaitingApproval: {
		// An approval goes back to queued, so that the freeze and the
		// one-change rule apply again before the dispatch.
		EventApproved: StateQueued,
		EventRejected: StateRejected,
		EventCancel:   StateCanceled,
		EventExpire:   StateExpired,
	},
	StateDispatched: {
		EventClaim:  StateRunning,
		EventCancel: StateCanceled,
		EventExpire: StateExpired,
		// The durable operation can end failed with no claim that the job
		// saw, for example at its limit of expired claims.
		EventPhaseFailed: StateFailed,
	},
	StateRunning: {
		EventRelease:       StateDispatched,
		EventPhaseDone:     StateQueued,
		EventLastPhaseDone: StateSucceeded,
		EventDelivered:     StateAwaitingState,
		EventPhaseFailed:   StateFailed,
		EventCancel:        StateCanceling,
	},
	StateCanceling: {
		// The executor stopped, or gave the phase back: the cancel wins.
		EventRelease:     StateCanceled,
		EventPhaseFailed: StateCanceled,
		EventPhaseDone:   StateCanceled,
		// The last phase finished before the executor saw the cancel. The
		// change happened, so the job tells the truth.
		EventLastPhaseDone: StateSucceeded,
		// A push cannot be undone. The job waits for the state version, as
		// without the cancel.
		EventDelivered: StateAwaitingState,
		// A second cancel changes nothing.
		EventCancel: StateCanceling,
	},
	StateAwaitingState: {
		EventStateObserved: StateSucceeded,
		EventRejected:      StateRejected,
		EventCancel:        StateCanceled,
		EventExpire:        StateExpired,
	},
}

// Next returns the state of a job in the state from after the event ev. It returns an error
// that wraps ErrFinished for any event on a terminal state, and ErrTransition
// for an event that from does not allow.
func Next(from State, ev Event) (State, error) {
	if from.Terminal() {
		return from, fmt.Errorf("%w: %s on %s", ErrFinished, ev, from)
	}

	to, ok := transitions[from][ev]
	if !ok {
		return from, fmt.Errorf("%w: %s on %s", ErrTransition, ev, from)
	}

	return to, nil
}
