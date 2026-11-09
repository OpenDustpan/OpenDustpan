// Package job defines jobs: work that OpenDustpan asks an executor to do on
// one unit. A job plans, applies or destroys the resources of a unit, or
// writes a change to the code location of the unit in git.
//
// A job belongs to exactly one unit, named by its unit reference: the tool,
// the namespace and the tool's own name. The package never reads the model
// of a tool. Each tool package gives the default steps of its operations
// through the StepPlanner port, and reports its state versions through the
// rule of Store.ObserveStateVersion.
//
// A job is a list of phases. An executor runs one phase at a time. Each
// phase is one durable operation, so the claim, its expiry, the fencing
// token, the takeover and the cancel of a phase are the rules of package
// durable. The job record holds what a person reads: the state of
// the job, its phases, its steps, its plans and its logs. Gates between
// phases (approvals, change freezes, one change at a time on a unit) are
// rules of this package.
//
// Two executors implement the Executor port. The write-back executor
// commits a change to git through a forge, and the team's own CI applies
// it. The job ends when the new state version of the unit arrives. A runner
// is a process that the team hosts. It claims phases and runs their steps
// as argument lists, with no shell.
//
// The package is tool-neutral. It never imports a store adapter, a forge
// adapter or a transport, and no tool package imports it: the adapters of
// StepPlanner and the report of state versions live in the internal wiring
// of the server.
package job

import (
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

var (
	// ErrInvalid reports a request with an empty or bad field.
	ErrInvalid = errors.New("job request is not valid")
	// ErrNotFound reports a job ID that the store does not know.
	ErrNotFound = errors.New("job not found")
	// ErrDamaged reports a stored job row that this version cannot read: the
	// body is not JSON, or it holds a value that this version does not know,
	// for example a source kind, an agent mode or a verification result that
	// another version of the server wrote. Store.Get returns it for such a
	// row. Store.List and Store.Orphans report the row in Damaged and leave
	// it out of their jobs, and every batch reader skips it: the server
	// never dispatches, expires or closes a damaged job. A person reads the
	// reason and cancels the job by its ID (Store.CancelDamaged).
	ErrDamaged = errors.New("stored job is damaged")
	// ErrKeyConflict reports a start whose idempotency key names a job with
	// another request: another spec, or the same spec with another reason,
	// pool or code location.
	ErrKeyConflict = errors.New("idempotency key names a job with another request")
	// ErrTransition reports an event that the state machine does not allow
	// in the current state of the job.
	ErrTransition = errors.New("job state does not allow this event")
	// ErrConflict reports a write that read an older version of the job.
	// The caller reads the job again and decides again.
	ErrConflict = errors.New("job changed since it was read")
	// ErrLogDiffers reports the end of a step whose log is not the log that
	// the caller says it sent. It is an ErrConflict (errors.Is is true for
	// both), with a text that names the cause.
	ErrLogDiffers error = logDiffersError{}
	// ErrUnsupported reports an operation that the executor of the job
	// cannot do, for example a destroy through write-back.
	ErrUnsupported = errors.New("executor does not support this operation")
	// ErrUnitBusy reports a dispatch of a phase that changes resources while
	// another job holds the unit for a change.
	ErrUnitBusy = errors.New("another job holds the unit for a change")
	// ErrFrozen reports a start or a dispatch of a phase that changes
	// resources while a change freeze of the namespace is active, without
	// break-glass.
	ErrFrozen = errors.New("a change freeze of the namespace is active")
	// ErrLocked reports a start or a dispatch of any phase while a manual lock
	// covers the unit: a lock on the unit, on its collection or on its
	// namespace. Break-glass does not pass it. The store wraps the error of
	// the lock (manuallock.LockedError), which names the holder and the
	// reason. A phase that runs already is not affected.
	ErrLocked = errors.New("a manual lock covers the unit")
	// ErrUnitGone reports a dispatch of a job whose unit is not in the
	// inventory: no manual lock of the unit can be read, so the dispatch
	// fails closed. The job stays queued.
	ErrUnitGone = errors.New("the unit of the job is not in the inventory")
	// ErrPolicyDenied reports a start that the policy decision denied.
	ErrPolicyDenied = errors.New("policy denied the job")
	// ErrGateEnforced reports a start on a unit that an enforced gate at
	// change.propose or state.write covers. The server cannot evaluate such
	// a gate for a job yet, so it refuses the start and never skips the gate.
	ErrGateEnforced = errors.New("an enforced gate covers the unit")
	// ErrFinished reports a cancel or an approval of a job that already
	// ended.
	ErrFinished = errors.New("job already ended")
	// ErrSelfApproval reports an approval by the principal that started the
	// job, when the decision does not allow it.
	ErrSelfApproval = errors.New("the principal that started the job cannot approve it")
	// ErrPlanChanged reports an approval or an apply for plan hashes that are
	// not the hashes of the stored plans.
	ErrPlanChanged = errors.New("plan changed since the approval")
	// ErrPlanStale reports the dispatch of an apply phase whose plan read
	// another state version than the current one, or is older than the
	// maximum plan age. The job then plans again and asks for approval
	// again.
	ErrPlanStale = errors.New("plan is stale")
	// ErrNoPlan reports the dispatch of an apply phase whose plan phase
	// stored no plan. A new plan phase would store none either, so the job
	// fails and does not plan again. An error with ErrNoPlan also wraps
	// ErrPlanStale.
	ErrNoPlan = errors.New("the plan phase stored no plan")
	// ErrDelivered reports a cancel of a write-back job whose change is in
	// git for good: a direct push, or a review request that merged. The job
	// stays and waits for its state version.
	ErrDelivered = errors.New("the change of the job is already in git")
	// ErrAgentDenied reports an approval, a confirmation or a break-glass
	// that an agent asks for, and a start that the agent mode does not
	// allow. Only a person can approve, confirm or break a freeze.
	ErrAgentDenied = errors.New("an agent cannot do this")
	// ErrNotRequester reports a confirmation by a principal that is not the
	// person who asked the agent for the job.
	ErrNotRequester = errors.New("only the person who asked the agent can confirm the job")
	// ErrAuthStale reports a confirmation with no fresh authentication
	// (ConfirmAuth) inside ConfirmFreshness.
	ErrAuthStale = errors.New("the confirmation needs a fresh authentication")
	// ErrLimit reports a start above a limit of open jobs.
	ErrLimit = errors.New("too many open jobs")
	// ErrClaimLost reports a write by an executor whose claim on the phase is
	// no longer live. It wraps durable.ErrClaimLost.
	ErrClaimLost = errors.New("claim on the job phase is lost")
	// ErrCanceled reports a write by an executor to a phase that a cancel
	// stops. It wraps durable.ErrCanceled.
	ErrCanceled = errors.New("job is canceled")
)

// ID identifies a job. The service makes it at start, with the prefix job-,
// before it builds the start keys of the durable operations of the job.
type ID string

// IDPrefix starts every job ID.
const IDPrefix = "job-"

// Operation is the tool-neutral verb of a job. Each tool maps it to its own
// command: a Pulumi preview, up, destroy or refresh, or an OpenTofu plan,
// apply, apply -destroy or apply -refresh-only. User-facing text keeps the
// words of the tool.
type Operation string

// The operations of a job.
const (
	// OperationPlan shows the changes and changes no resource.
	OperationPlan Operation = "plan"
	// OperationApply changes resources to match the program and its config.
	OperationApply Operation = "apply"
	// OperationDestroy deletes the resources of the unit.
	OperationDestroy Operation = "destroy"
	// OperationRefresh reads the resources and writes a new state version.
	// It changes no resource, but it writes state.
	OperationRefresh Operation = "refresh"
)

// Valid reports whether o is one of the operations of this package.
func (o Operation) Valid() bool {
	switch o {
	case OperationPlan, OperationApply, OperationDestroy, OperationRefresh:
		return true
	default:
		return false
	}
}

// WritesState reports whether a job of the operation can write a state
// version of the unit. Only a plan cannot.
func (o Operation) WritesState() bool {
	return o.Valid() && o != OperationPlan
}

// Purpose tells why a job runs, when its operation alone does not say. The
// server fixes it at start and a later change of the unit settings never
// changes it. It selects the phase list of the operation.
type Purpose string

// The purposes of a job.
const (
	// PurposeNone is a job whose operation says all: the default.
	PurposeNone Purpose = ""
	// PurposeDriftCheck is a drift check: the operation refresh with the
	// one phase plan. It changes no resource and writes no state version.
	PurposeDriftCheck Purpose = "drift-check"
)

// Valid reports whether p is one of the purposes of this package, with the
// empty purpose.
func (p Purpose) Valid() bool {
	return p == PurposeNone || p == PurposeDriftCheck
}

// ExecutorKind names the executor of a job.
type ExecutorKind string

// The executors of a job.
const (
	// ExecutorWriteBack commits a change to the code location of the unit, and the
	// team's own CI applies it.
	ExecutorWriteBack ExecutorKind = "write-back"
	// ExecutorRunner dispatches the phases to a runner that the team hosts.
	ExecutorRunner ExecutorKind = "runner"
)

// Valid reports whether k is one of the executors of this package.
func (k ExecutorKind) Valid() bool {
	return k == ExecutorWriteBack || k == ExecutorRunner
}

// PhaseKind names what one phase of a job does.
type PhaseKind string

// The kinds of phase.
const (
	// PhasePlan runs the plan steps on a runner and stores the plan. It
	// changes no resource.
	PhasePlan PhaseKind = "plan"
	// PhaseApply runs the steps that change resources or write state, on a
	// runner.
	PhaseApply PhaseKind = "apply"
	// PhaseDeliver commits the change of a write-back job to its code location:
	// a direct push or a review request.
	PhaseDeliver PhaseKind = "deliver"
)

// Changes reports whether a phase of kind k changes resources, state or
// the code in git. A freeze and the one-change rule of a unit gate only such
// phases.
func (k PhaseKind) Changes() bool {
	return k == PhaseApply || k == PhaseDeliver
}

// Location names where the program and the config of a unit are in git: a
// forge connection, a repository, a ref and a directory. Revision is the commit
// that a runner checks out. A start may leave it empty, and the server then
// resolves the head of Ref at start, so that every phase of the job runs on
// one commit.
type Location struct {
	Connection string
	Repository string
	Ref        string
	Directory  string
	Revision   string
}

// Delivery names how a write-back job delivers its commit.
type Delivery string

// The deliveries of a write-back job.
const (
	// DeliveryReviewRequest commits to a new branch and opens a review
	// request. It is the default.
	DeliveryReviewRequest Delivery = "review-request"
	// DeliveryDirectPush commits to the ref of the code location. The branch
	// protection of the forge decides whether the push is allowed.
	DeliveryDirectPush Delivery = "direct-push"
)

// FileEdit is one change to one structured file at the code location of a
// write-back job: a set of keys and their new values. The executor reads the
// file at the base commit, changes only these keys, and refuses the edit
// when a key changed after BaseRevision.
type FileEdit struct {
	// Path is relative to the directory of the code location.
	Path string
	// Keys maps a key path in the file to its new value as JSON. A null value
	// deletes the key.
	Keys map[string][]byte
}

// Spec is what a job does. The server fixes it at start: a later change of
// the unit settings, a blueprint or a profile does not change a job that
// exists.
type Spec struct {
	// Unit is the unit reference, with the stable unit ID set
	// (identity.Object.WithUnitID).
	Unit      identity.Object
	Operation Operation
	// Purpose is empty, or PurposeDriftCheck for a drift check.
	Purpose  Purpose
	Executor ExecutorKind
	Location Location
	// Steps are the steps of a runner job, resolved by the server at start
	// from the settings of the unit, its blueprint or the defaults of its
	// tool. The server sets Step.ChangesResources (see MarkCustomSteps). A
	// caller never gives steps. A write-back job has no steps.
	Steps []Step
	// Pool is the ID of the runner pool of a runner job. The pool belongs to the
	// namespace of the unit.
	Pool string
	// Profile names the profile that supplies the secrets of a runner job.
	// The server fixes it at start, so a later change of the unit settings
	// cannot point a job at other secrets.
	Profile string
	// Edits are the file edits of a write-back job.
	Edits []FileEdit
	// Delivery is the delivery of a write-back job.
	Delivery Delivery
	// Defaulted names the values that the request did not give and that the
	// server took from the settings of the unit or the namespace: some of
	// DefaultedPool, DefaultedLocation and DefaultedProfile, in that order.
	// It is empty when the request gave every value. The service sets it at
	// start, after the planner, and a planner never sets it. It is fixed with
	// the rest of the spec, so a later change of the settings changes neither
	// a value nor this list.
	Defaulted []string
}

// The names that Spec.Defaulted holds.
const (
	DefaultedPool     = "pool"
	DefaultedLocation = "location"
	DefaultedProfile  = "profile"
)

// DefaultedValues returns the names of the values of spec that req did not
// give. A request has no profile, so a profile in the spec is always a
// default. It returns nil when req gave every value that the spec holds.
func DefaultedValues(req *StartRequest, spec *Spec) []string {
	var out []string

	if req.Pool == "" && spec.Pool != "" {
		out = append(out, DefaultedPool)
	}

	if req.Location.IsZero() && !spec.Location.IsZero() {
		out = append(out, DefaultedLocation)
	}

	if spec.Profile != "" {
		out = append(out, DefaultedProfile)
	}

	return out
}

// Changes reports whether a job of s can change resources, state or the
// code in git: every job but a runner job that only plans, and a drift
// check, which has a plan phase only.
func (s *Spec) Changes() bool {
	if s.Purpose == PurposeDriftCheck {
		return false
	}

	return s.Executor == ExecutorWriteBack || s.Operation != OperationPlan
}

// Actor is who asks for a change to a job: the principal of the request,
// and the agent that acts for it, if any. An agent such as Dusty runs in the
// server and acts as the user who asked, through an act-as edge. The
// actions of the user decide. The job, its approvals, its decision input and its audit
// entries record both.
type Actor struct {
	Principal identity.Principal
	// Agent names the agent that acts for Principal, or is empty when the
	// principal acts itself.
	Agent string
	// Mode is the agent mode that the user set for Agent. It is empty when
	// Agent is empty. An agent with an empty mode works in
	// AgentModeApproveFirst.
	Mode AgentMode
	// ConfirmAuth is the fresh authentication that the server can ask of
	// Principal for a confirmation: ConfirmAuthMFA when built-in MFA covers
	// the person, else ConfirmAuthSSO when the person signs in through an
	// SSO identity provider, else empty, for example in an installation
	// with static tokens only. The server sets it from the sign-in of the
	// principal. No client gives it.
	ConfirmAuth ConfirmAuth
	// AuthAt is the time of the last fresh authentication of the kind
	// ConfirmAuth in the session of the request, or the zero time. The
	// server sets it. Confirm records it in Approval.AuthAt.
	AuthAt time.Time
}

// BreakGlass records a start through an active change freeze. Reason is
// the text that the principal gave. It is in the audit entry of the start.
type BreakGlass struct {
	Reason string
	Freeze string
}

// Job is what any instance can read about a job.
type Job struct {
	ID    ID
	Spec  Spec
	State State
	// Wait tells why a queued job does not dispatch now. It is empty when
	// the job is not queued, or when nothing holds it.
	Wait WaitReason
	// Reason tells why a terminal job ended, for example the reason of a
	// failure. A job that stays queued keeps the reason of its last replan,
	// which names the state versions that made its plan stale.
	Reason string
	// Phases are the phases of the job, in order. Executor.Phases fixes
	// them at start.
	Phases []Phase
	// Current is the index in Phases of the phase that runs or runs next.
	// It equals len(Phases) after the last phase.
	Current int
	// StartedBy is the subject of the principal that started the job. A
	// user who starts a job through an agent is the starter.
	StartedBy string
	// Source tells where the start came from. Service.Start always sets it.
	Source SourceKind
	// Agent names the agent that started the job for StartedBy, or is
	// empty.
	Agent string
	// AgentMode is the agent mode of the start, or empty when no agent
	// started the job.
	AgentMode AgentMode
	// BreakGlass is set when the job started through an active freeze.
	BreakGlass *BreakGlass
	// Trigger names the run trigger that started the job, or is nil for a job
	// that no trigger started. The server sets it from StartRequest.Trigger.
	// A read of the output of another unit, by a job that a trigger started,
	// finds the last saver of the trigger by this record.
	Trigger *TriggerRef
	// Delivery is the result of the deliver phase of a write-back job.
	Delivery *DeliveryResult
	// Closed is the state version that ended a write-back job.
	Closed *StateVersion
	// Verification tells whether the closing state version came from the
	// delivered commit. A durable operation of KindVerify sets it after the
	// state write, through the forge.
	Verification Verification
	// IdempotencyKey is the key of the start. Two starts with one key give
	// one job.
	IdempotencyKey string
	// RequestHash is the hash of the whole request of the start: the spec and
	// the reason. A second start with the key of the job and another hash
	// fails with ErrKeyConflict. It is empty for a job that has no key.
	RequestHash string
	// Version grows on each change of the job record. A write names the
	// version that it read, so a write on a stale read fails with
	// ErrConflict.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
	// StateChangedAt is the time of the last change of State. UpdatedAt also
	// moves for a write that keeps the state, so only this time tells how long
	// the job has been in its state. The reconcile schedule reads it for the
	// queue, dispatch and state timeouts.
	StateChangedAt time.Time
}

// Phase is one phase of a job and its record.
type Phase struct {
	Kind PhaseKind
	// Operation is the durable operation of the phase, or empty before its
	// dispatch. A phase has one operation: a takeover is a new claim on the
	// same operation, never a new operation.
	Operation string
	// Claims counts the claims of the operation, as a record. The first
	// claim sets 1, and each takeover adds 1.
	Claims int
	// Executor names the runner or the instance that holds or held the
	// last claim.
	Executor string
	// Steps are the recorded steps of the phase, in order. After a takeover
	// the phase runs again from its first step, and the records of the
	// earlier claims stay.
	Steps []StepRecord
	// StateVersion is the current state version of the unit at the last
	// claim of a plan phase. The store sets it in the transaction of the
	// claim. PutPlan copies it to the stored plan, and fails the phase when
	// the current version changed before the plan is stored. It is empty
	// for other phases.
	StateVersion string
	// Plan is the stored plan of a plan phase, or nil.
	Plan *PlanRef
	// Gates are the gates in front of the phase, evaluated before its
	// dispatch: the approval gate of the policy decision, and the
	// confirmation gate of an agent job in AgentModeApproveFirst.
	Gates []Gate
	// Approvals are the approvals and confirmations that the gates got.
	Approvals  []Approval
	StartedAt  time.Time
	FinishedAt time.Time
}

// DeliveryResult is what the deliver phase of a write-back job wrote.
type DeliveryResult struct {
	Delivery Delivery
	// Commit is the commit that the executor made.
	Commit string
	// Branch is the branch of the commit.
	Branch string
	// ReviewRequest is the URL of the review request, or empty for a direct
	// push.
	ReviewRequest string
	// MergedAt is the time that the forge reported a merge of the review
	// request, or the zero time.
	MergedAt time.Time
	// DeliveredAt is the time that the commit landed on the forge, as the
	// forge reports it. After a takeover that finds the commit by its
	// trailer, it is the time of that commit, not the time of the record.
	DeliveredAt time.Time
}

// Limits of the text fields of a DeliveryResult that an executor gives.
const (
	maxCommitLen = 128
	maxBranchLen = 255
	maxReviewLen = 2048
)

// Validate returns an error that wraps ErrInvalid for a result that an
// executor must not record: an unknown delivery kind, no commit or branch,
// a review request URL on a direct push or none on a review request, a text
// that is too long or holds a control character, no delivery time, or a merge
// time (the merge comes later, as EventMerged). The result goes to the audit
// entry of the delivery, so the text rules are those of an audit reason.
func (d *DeliveryResult) Validate() error {
	if err := d.validateTexts(); err != nil {
		return err
	}

	switch {
	case !d.MergedAt.IsZero():
		return fmt.Errorf("%w: a delivery has no merge time yet", ErrInvalid)
	case d.DeliveredAt.IsZero():
		return fmt.Errorf("%w: a delivery needs the time of the commit", ErrInvalid)
	case audit.ValidateReason(d.AuditReason()) != nil:
		return fmt.Errorf("%w: the commit, the branch and the review request are too long or hold a control character",
			ErrInvalid)
	default:
		return nil
	}
}

// checkBounded returns ErrInvalid unless text has 1 to max bytes.
func checkBounded(what, text string, limit int) error {
	if text == "" || len(text) > limit {
		return fmt.Errorf("%w: a delivery needs a %s of 1 to %d bytes", ErrInvalid, what, limit)
	}

	return nil
}

// AuditReason returns the reason of the audit entry of the delivery: the
// commit, the branch and the review request.
func (d *DeliveryResult) AuditReason() string {
	reason := "commit " + d.Commit + " on branch " + d.Branch
	if d.ReviewRequest != "" {
		reason += ", review request " + d.ReviewRequest
	}

	return reason
}

// Same reports whether d and o hold the same result, with the times compared
// as instants.
func (d *DeliveryResult) Same(o *DeliveryResult) bool {
	return d.Delivery == o.Delivery && d.Commit == o.Commit && d.Branch == o.Branch &&
		d.ReviewRequest == o.ReviewRequest && d.MergedAt.Equal(o.MergedAt) && d.DeliveredAt.Equal(o.DeliveredAt)
}

// CloseAfter returns the time after which a final state version closes
// the job of d: the delivery of a direct push, or the merge of a review
// request. It returns false for a review request with no known merge,
// which waits.
func (d *DeliveryResult) CloseAfter() (time.Time, bool) {
	switch {
	case d == nil:
		return time.Time{}, false
	case d.Delivery == DeliveryDirectPush:
		return d.DeliveredAt, true
	case !d.MergedAt.IsZero():
		return d.MergedAt, true
	default:
		return time.Time{}, false
	}
}

// ClosedBy reports whether the state version v closes j: j waits in
// awaiting-state, v is a final version of the unit of j, and v is at or
// after the time of CloseAfter. The store applies the rule in three
// places, in the transaction of each: ObserveStateVersion for a new
// version, and EventMerged and EventDelivered for the last final version
// of the unit that the store knows, so that a merge or a delivery that
// the job sees late still closes it.
func (j *Job) ClosedBy(v StateVersion) bool {
	if j.State != StateAwaitingState || !v.Final || j.Spec.Unit.UnitID() != v.Unit.UnitID() {
		return false
	}

	after, ok := j.Delivery.CloseAfter()

	return ok && !v.At.Before(after)
}

// Settled reports whether the change of d is in git for good: a direct
// push, or a review request that merged. A cancel of such a job fails with
// ErrDelivered.
func (d *DeliveryResult) Settled() bool {
	return d != nil && (d.Delivery == DeliveryDirectPush || !d.MergedAt.IsZero())
}

// TrailerJob is the git trailer that the write-back executor writes in each
// commit, with the job ID as its value. After a takeover, the executor
// looks for a commit with the trailer on the target ref before it commits,
// so a direct push never lands twice.
const TrailerJob = "Dustpan-Job"

// Verification tells whether the state version that closed a write-back
// job came from the delivered commit.
type Verification string

// The results of a verification.
const (
	// VerificationNone: the job is not closed yet.
	VerificationNone Verification = ""
	// VerificationPending: the job closed, and the verify operation has not
	// asked the forge yet.
	VerificationPending Verification = "pending"
	// VerificationVerified: the commit of the state version contains the
	// delivered commit.
	VerificationVerified Verification = "verified"
	// VerificationUnverified: the state version reported no commit, or a
	// commit that does not contain the delivered commit. The console shows
	// it.
	VerificationUnverified Verification = "unverified"
)

// Valid reports whether v is one of the verification results of this
// package. VerificationNone, the empty result, is one of them.
func (v Verification) Valid() bool {
	switch v {
	case VerificationNone, VerificationPending, VerificationVerified, VerificationUnverified:
		return true
	default:
		return false
	}
}

// StateVersion is a new state version of a unit, as the internal wiring
// of the server reports it for the tool package that wrote it. Commit is
// the commit that the tool reported with the write, or empty when the tool
// reported none.
type StateVersion struct {
	Unit      identity.Object
	VersionID string
	Commit    string
	// Job is the job-ID claim of the principal that wrote the version: the
	// job whose job token the tool used, or empty for a write with another
	// credential.
	Job ID
	At  time.Time
	// Final is true for the last state version of a run: the release of
	// the OpenTofu state lock, or the end of a Pulumi update. Only a final
	// version closes a write-back job. An intermediate write of a run
	// never does.
	Final bool
}

// validateTexts checks the delivery kind and the text fields of d.
func (d *DeliveryResult) validateTexts() error {
	if d.Delivery != DeliveryReviewRequest && d.Delivery != DeliveryDirectPush {
		return fmt.Errorf("%w: unknown delivery %q", ErrInvalid, d.Delivery)
	}

	if err := checkBounded("commit", d.Commit, maxCommitLen); err != nil {
		return err
	}

	if err := checkBounded("branch", d.Branch, maxBranchLen); err != nil {
		return err
	}

	return d.validateReview()
}

// validateReview checks that a review request delivery has a URL and a direct
// push has none.
func (d *DeliveryResult) validateReview() error {
	switch {
	case d.Delivery == DeliveryDirectPush && d.ReviewRequest != "":
		return fmt.Errorf("%w: a direct push has no review request", ErrInvalid)
	case d.Delivery == DeliveryReviewRequest:
		return checkBounded("review request", d.ReviewRequest, maxReviewLen)
	default:
		return nil
	}
}

// logDiffersError is the type of ErrLogDiffers.
type logDiffersError struct{}

// Error implements error.
func (logDiffersError) Error() string { return "the log differs from the stored chunks" }

// Is makes ErrLogDiffers an ErrConflict.
func (logDiffersError) Is(target error) bool { return target == ErrConflict }
