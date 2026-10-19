package gate

import (
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Result is the result of one evaluation, for one unit or for a change.
type Result string

// The results, from the least strict to the most strict. A change of
// several units takes the strictest result of its units.
const (
	ResultAllow            Result = "allow"
	ResultWarn             Result = "warn"
	ResultAwaitingApproval Result = "awaiting-approval"
	ResultDeny             Result = "deny"
	ResultError            Result = "error"
)

// ChangeKind is the kind of the object that a decision record explains.
type ChangeKind string

// The change kinds. A write at state.write and a refused call of an access
// limit have no change object: the record stands on its own.
const (
	ChangeJob             ChangeKind = "job"
	ChangeChangeOrder     ChangeKind = "change-order"
	ChangeCatalogRevision ChangeKind = "catalog-revision"
	ChangeProfile         ChangeKind = "profile"
	ChangeStateWrite      ChangeKind = "state-write"
	ChangeAccess          ChangeKind = "access"
)

// ChangeRef names the object that a decision record explains.
type ChangeRef struct {
	Kind ChangeKind
	// ID is the ID of the job, the change order, the revision or the
	// profile. It is empty for ChangeStateWrite and ChangeAccess.
	ID string
}

// PolicyResult is the result of one gate or one access limit for one unit
// or one call.
type PolicyResult struct {
	Policy  PolicyID
	Version int64
	Mode    Mode
	Outcome Outcome
	// Matched is true when the policy matched. It is false for an error.
	Matched bool
	// Error is true when the engine gave a diagnostic error for the policy,
	// or did not answer.
	Error bool
	// Diagnostic is the message of the engine.
	Diagnostic string
	// Skipped is true for an error of a gate with OnErrorSkip.
	Skipped bool
	// BrokenBy is the ID of the audit entry of the break-glass that passed
	// the gate in error, or empty.
	BrokenBy string
	// Message is the rendered message of a matched gate.
	Message string
}

// UnitResult is the evaluation of one unit.
type UnitResult struct {
	Unit   identity.Object
	Facts  Facts
	Digest string
	Gates  []PolicyResult
	Result Result
}

// CarriedFrom names the first signature of a signature that carried over
// to a new plan revision.
type CarriedFrom struct {
	Revision int
	Digest   string
	At       time.Time
	// Level is the scope whose carry-over setting allowed it.
	Level Scope
}

// Signature is one approval that counts for one requirement.
type Signature struct {
	Subject     string
	At          time.Time
	CarriedFrom *CarriedFrom
}

// Requirement is the need of one approval source for one change: a count
// of signatures by its eligible approvers. One signature fills one slot of
// one requirement only, so the approvals across requirements come from
// distinct people.
type Requirement struct {
	Name RequirementName
	// Version is the version of the rule, the default or the ownership
	// rule that gave the requirement.
	Version          int64
	Approvers        Approvers
	Count            int
	AuthorMayApprove bool
	FreshAuth        FreshAuth
	Timeout          time.Duration
	Signatures       []Signature
	// Evaluate is true for an ownership rule in evaluate mode. Such a
	// requirement is in the record and asks for nothing.
	Evaluate bool
}

// FixedControlKind names a fixed control: a control that refuses on its
// own, outside the gates.
type FixedControlKind string

// The fixed controls that a record can show.
const (
	ControlWorkspaceLock    FixedControlKind = "workspace-lock"
	ControlManualLock       FixedControlKind = "manual-lock"
	ControlFreeze           FixedControlKind = "freeze"
	ControlOverrideSwitches FixedControlKind = "override-switches"
	ControlModuleSwitches   FixedControlKind = "module-switches"
	ControlQuotaReservation FixedControlKind = "quota-reservation"
	ControlQuotaCount       FixedControlKind = "quota-count"
	ControlSourceFilter     FixedControlKind = "source-filter"
	ControlSafeDelete       FixedControlKind = "safe-delete"
	ControlPlanStaleness    FixedControlKind = "plan-staleness"
	ControlAgentMode        FixedControlKind = "agent-mode"
	ControlConfirmationStep FixedControlKind = "confirmation-step"
	ControlSecretConsent    FixedControlKind = "secret-consent"
)

// FixedControl is the state of one fixed control that the server checked
// for the change. It does not change the combining of the gates.
type FixedControl struct {
	Kind FixedControlKind
	// Passed is false when the control refused the change.
	Passed bool
	// Detail is a short text, for example the ID of a freeze.
	Detail string
}

// Record is the decision record of one evaluation of one decision point
// for one change. Each refusal, each approval wait and each warning names
// its record.
type Record struct {
	ID        RecordID
	Point     DecisionPoint
	Change    ChangeRef
	Namespace namespace.Name
	At        time.Time
	// Subject is the principal of the change or the call.
	Subject string
	// Schema is the version of the fact schema.
	Schema SchemaVersion
	// PathSets holds the ID and the version of each path set that the
	// schema of the unit held.
	PathSets []PathSetVersion
	Units    []UnitResult
	// Requirements holds the requirements, with the signatures so far.
	Requirements []Requirement
	// Warnings holds the messages of the matched warn gates.
	Warnings []string
	// AutoApply is true only for the result allow.
	AutoApply bool
	Controls  []FixedControl
	// BreakGlass is the ID of the audit entry of a break-glass for gates
	// in error, or empty.
	BreakGlass string
	Result     Result
	// Count is the count of refusals that share the record: refusals of
	// one principal, one action and one access limit within 60 seconds.
	// It is 1 for every other record.
	Count int
	// LastAt is the time of the last refusal that the record counts.
	LastAt time.Time
}

// PathSetVersion names one version of one path set.
type PathSetVersion struct {
	Scope   Scope
	ID      ID
	Version int64
}
