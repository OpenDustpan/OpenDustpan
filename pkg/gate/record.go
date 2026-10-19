package gate

import (
	"time"

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
	Kind ChangeKind `json:"kind"`
	// ID is the ID of the job, the change order, the revision or the
	// profile. It is empty for ChangeStateWrite and ChangeAccess.
	ID string `json:"id"`
}

// PolicyResult is the result of one gate or one access limit for one unit
// or one call.
type PolicyResult struct {
	Policy  PolicyID `json:"policy"`
	Version int64    `json:"version"`
	Mode    Mode     `json:"mode"`
	Outcome Outcome  `json:"outcome"`
	// Matched is true when the policy matched. It is false for an error.
	Matched bool `json:"matched"`
	// Error is true when the engine gave a diagnostic error for the policy,
	// or did not answer.
	Error bool `json:"error"`
	// Diagnostic is the message of the engine.
	Diagnostic string `json:"diagnostic"`
	// Skipped is true for an error of a gate with OnErrorSkip.
	Skipped bool `json:"skipped"`
	// BrokenBy is the ID of the audit entry of the break-glass that passed
	// the gate in error, or empty.
	BrokenBy string `json:"brokenBy"`
	// Message is the rendered message of a matched gate.
	Message string `json:"message"`
}

// UnitResult is the evaluation of one unit.
type UnitResult struct {
	// Unit is the unit scope of the unit, so its JSON form is its scope
	// key.
	Unit Scope `json:"unit"`
	// Facts is the fact document of the unit.
	Facts Facts `json:"facts"`
	// Digest is the SHA-256 of the fact document, in lowercase hex.
	Digest string         `json:"digest"`
	Gates  []PolicyResult `json:"gates"`
	Result Result         `json:"result"`
}

// CarriedFrom names the first signature of a signature that carried over
// to a new plan revision.
type CarriedFrom struct {
	Revision int       `json:"revision"`
	Digest   string    `json:"digest"`
	At       time.Time `json:"at"`
	// Scope is the scope whose carry-over setting allowed it.
	Scope Scope `json:"scope"`
}

// Signature is one approval that counts for one requirement.
type Signature struct {
	Subject     string       `json:"subject"`
	At          time.Time    `json:"at"`
	CarriedFrom *CarriedFrom `json:"carriedFrom"`
}

// Requirement is the need of one approval source for one change: a count
// of signatures by its eligible approvers. One signature fills one slot of
// one requirement only, so the approvals across requirements come from
// distinct people.
type Requirement struct {
	Name RequirementName `json:"name"`
	// Version is the version of the rule, the default or the ownership
	// rule that gave the requirement.
	Version          int64     `json:"version"`
	Approvers        Approvers `json:"approvers"`
	Count            int       `json:"count"`
	AuthorMayApprove bool      `json:"authorMayApprove"`
	FreshAuth        FreshAuth `json:"freshAuthSeconds"`
	// TimeoutSeconds is the approval timeout, in whole seconds.
	TimeoutSeconds int64       `json:"timeoutSeconds"`
	Signatures     []Signature `json:"signatures"`
	// Evaluate is true for an ownership rule in evaluate mode. Such a
	// requirement is in the record and asks for nothing.
	Evaluate bool `json:"evaluate"`
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
	Kind FixedControlKind `json:"kind"`
	// Passed is false when the control refused the change.
	Passed bool `json:"passed"`
	// Detail is a short text, for example the ID of a freeze.
	Detail string `json:"detail"`
}

// Record is the decision record of one evaluation of one decision point
// for one change. Each refusal, each approval wait and each warning names
// its record.
type Record struct {
	ID     RecordID      `json:"id"`
	Point  DecisionPoint `json:"decisionPoint"`
	Change ChangeRef     `json:"change"`
	// Namespace is the namespace of the change, or audit.ServerScope for
	// the record of an access limit on an installation object.
	Namespace namespace.Name `json:"namespace"`
	At        time.Time      `json:"at"`
	// Subject is the principal of the change or the call.
	Subject string `json:"subject"`
	// Schema is the version of the fact schema.
	Schema SchemaVersion `json:"schema"`
	// PathSets holds the ID and the version of each path set that the
	// schema of the unit held.
	PathSets []PathSetVersion `json:"pathSets"`
	Units    []UnitResult     `json:"units"`
	// Requirements holds the requirements, with the signatures so far.
	Requirements []Requirement `json:"requirements"`
	// Warnings holds the messages of the matched warn gates.
	Warnings []string `json:"warnings"`
	// AutoApply is true only for the result allow.
	AutoApply bool           `json:"autoApply"`
	Controls  []FixedControl `json:"controls"`
	// BreakGlass is the ID of the audit entry of a break-glass for gates
	// in error, or empty.
	BreakGlass string `json:"breakGlass"`
	Result     Result `json:"result"`
	// Count is the count of refusals that share the record: refusals of
	// one principal, one action and one access limit within
	// AccessRefusalWindow. It is 1 for every other record.
	Count int `json:"count"`
	// LastAt is the time of the last refusal that the record counts.
	LastAt time.Time `json:"lastAt"`
}

// PathSetVersion names one version of one path set.
type PathSetVersion struct {
	Scope   Scope `json:"scope"`
	ID      ID    `json:"id"`
	Version int64 `json:"version"`
}
