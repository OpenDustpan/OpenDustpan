// Package gate is the domain of the policy model: gates at fixed decision
// points, access limits, approval rules, approval defaults, ownership rules,
// path sets, typed fact documents and decision records.
//
// The model answers three questions for every tool with one set of
// objects. Access limits say who may not call an action, after the grants
// allow it. Gates say whether a change may go on, who must agree and when
// to warn. Approval rules say who may approve and how many people must
// approve. Facts are typed values that the server produces before a
// decision point. A fact never decides. No outcome relaxes another outcome:
// each part can only add a refusal, a requirement or a warning.
//
// The package is tool-neutral. A unit is a scope with a tool, a namespace
// and the tool's own name. Each tool adapter reaches the decision point
// state.write through Service.Evaluate, and no adapter calls another.
//
// The package holds the types, the identifier grammar, the ports and the
// Service. The policy engine port has a linked adapter and an adapter for an
// external service. The store port has one adapter for each SQL dialect. A
// consumer of the Service declares its own interface with the one method
// that it calls.
package gate

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. Each refusal also names its decision record: see
// RefusalError.
var (
	// ErrDenied reports a change that a gate in enforce mode denies, or a
	// write at state.write that needs an approval, which a CLI cannot wait
	// for.
	ErrDenied = errors.New("a gate denies the change")
	// ErrLimited reports a call that an access limit forbids. The error
	// names the limit.
	ErrLimited = errors.New("an access limit forbids the call")
	// ErrNotApprover reports an approval from a principal that is not an
	// eligible approver of an open requirement.
	ErrNotApprover = errors.New("the principal is not an eligible approver")
	// ErrAuthorApproval reports an approval from the author of the change,
	// for a rule that does not let the author approve.
	ErrAuthorApproval = errors.New("the author of the change cannot approve it")
	// ErrRuleFailed reports a gate or an access limit that failed to
	// evaluate: an engine diagnostic, a gate in the state invalid, or a
	// message that does not render. It is never an allow and never a deny.
	ErrRuleFailed = errors.New("a gate failed to evaluate")
	// ErrFinal reports a write that a final gate of a wider scope forbids,
	// or a final gate made weaker with no reason.
	ErrFinal = errors.New("a final gate forbids the change")
	// ErrInvalid reports a gate, a limit, a rule, a path set, a setting or
	// an identifier that is not valid.
	ErrInvalid = errors.New("the gate configuration is not valid")
	// ErrApprovers reports approvers that no one can meet: a group name that
	// is not valid, a group that does not exist, a user name that is not a
	// name, or a count above the people who can approve.
	ErrApprovers = errors.New("the approvers cannot be met")
	// ErrLimit reports a write above a limit of its scope, a condition
	// above its size limit, or a fact document that does not fit.
	ErrLimit = errors.New("a gate limit is exceeded")
	// ErrLimitMembership reports the text of an access limit whose
	// membership test of principal names a value that is not a named group
	// and not a set of named groups. The server cannot tell which group
	// such a limit names, so it refuses the limit at save and refuses a
	// change of the members of a group at check time.
	ErrLimitMembership = errors.New("an access limit can test membership only of a named group")
	// ErrUnavailable reports a policy engine or a store that does not
	// answer, or an external engine whose answer does not authenticate.
	ErrUnavailable = errors.New("the policy engine is unavailable")
	// ErrNotFound reports a gate, a limit, a rule, a path set or a decision
	// record that does not exist in the scope that the call names.
	ErrNotFound = errors.New("gate: object not found")
	// ErrUnitNotFound reports a write at a unit scope whose name no live
	// unit has: the unit does not exist, or it is deleted. The configuration
	// of a unit keys on the stable ID of the unit, so a write needs the unit.
	// It matches ErrNotFound too.
	ErrUnitNotFound = fmt.Errorf("%w: no live unit has the name of the scope", ErrNotFound)
	// ErrConflict reports a write whose expected version is not the stored
	// version.
	ErrConflict = errors.New("the stored version changed")
)

// errorCode is one sentinel and its stable error code.
type errorCode struct {
	err  error
	code string
}

// codes returns each sentinel with its stable error code, in the order
// that Code checks them.
func codes() []errorCode {
	return []errorCode{
		{ErrDenied, "gate.denied"},
		{ErrLimited, "gate.limited"},
		{ErrNotApprover, "gate.not-approver"},
		{ErrAuthorApproval, "gate.author-approval"},
		{ErrRuleFailed, "gate.rule-failed"},
		{ErrFinal, "gate.final"},
		{ErrInvalid, "gate.invalid"},
		{ErrLimit, "gate.limit"},
		{ErrLimitMembership, "gate.limit-membership"},
		{ErrUnavailable, "gate.unavailable"},
	}
}

// Code returns the stable error code of the first sentinel of this package
// that err wraps, for example "gate.denied", and true. It returns false
// for an error that wraps none of them. ErrNotFound and ErrConflict have
// no code of their own: each adapter maps them to its own not-found and
// conflict shape.
func Code(err error) (string, bool) {
	for _, c := range codes() {
		if errors.Is(err, c.err) {
			return c.code, true
		}
	}

	return "", false
}

// RefusalError is the error of a refused change or call. It wraps one sentinel
// (Err) and names the decision record that explains it. Each tool adapter
// writes Messages, the code of Err and Record in its own error shape.
type RefusalError struct {
	// Err is the sentinel: ErrDenied, ErrLimited, ErrRuleFailed, ErrFinal
	// or ErrUnavailable.
	Err error
	// Record is the ID of the decision record.
	Record RecordID
	// Messages are the rendered messages of the gates or the limit that
	// refused, in the order of the record.
	Messages []string
}

// Error returns the sentinel text, the messages and the record ID.
func (r *RefusalError) Error() string {
	var b strings.Builder

	b.WriteString(r.Err.Error())

	for _, m := range r.Messages {
		b.WriteString(": ")
		b.WriteString(m)
	}

	fmt.Fprintf(&b, " (decision record %s)", r.Record)

	return b.String()
}

// Unwrap returns the sentinel.
func (r *RefusalError) Unwrap() error {
	return r.Err
}

// DecisionPoint is a fixed moment where the server evaluates the gates of
// a change. It is also the Cedar action of each gate policy. A new
// decision point needs a new design decision.
type DecisionPoint string

// The six decision points.
const (
	// PointChangePropose runs before a job, a change order or a self-service request
	// revision starts, before a plan.
	PointChangePropose DecisionPoint = "change.propose"
	// PointChangePlanned runs after the plan is stored and its facts are
	// ready.
	PointChangePlanned DecisionPoint = "change.planned"
	// PointChangeApply runs before the dispatch of each phase that changes.
	PointChangeApply DecisionPoint = "change.apply"
	// PointProfileSave runs before a save, a rollback, a tag move or a
	// draft apply of a profile.
	PointProfileSave DecisionPoint = "profile.save"
	// PointProfileOpen runs before each open of a profile.
	PointProfileOpen DecisionPoint = "profile.open"
	// PointStateWrite runs before a write of state that no job of the
	// server makes: a write from a person's own CLI or CI, and a
	// tool-neutral state operation.
	PointStateWrite DecisionPoint = "state.write"
)

// DecisionPoints returns the six decision points, in a fixed order.
func DecisionPoints() []DecisionPoint {
	return []DecisionPoint{
		PointChangePropose, PointChangePlanned, PointChangeApply,
		PointProfileSave, PointProfileOpen, PointStateWrite,
	}
}

// ParseDecisionPoint returns the decision point s, or ErrInvalid.
func ParseDecisionPoint(s string) (DecisionPoint, error) {
	for _, p := range DecisionPoints() {
		if string(p) == s {
			return p, nil
		}
	}

	return "", fmt.Errorf("%w: unknown decision point %q", ErrInvalid, s)
}

// Mode says whether a match of a gate or an ownership rule has an effect.
type Mode string

// The modes. A new gate and a new ownership rule start in ModeEvaluate
// unless the request names ModeEnforce.
const (
	// ModeEnforce gives the outcome of a match.
	ModeEnforce Mode = "enforce"
	// ModeEvaluate writes the match in the decision record, with no
	// effect.
	ModeEvaluate Mode = "evaluate"
)

// ParseMode returns the mode s, or ErrInvalid.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case ModeEnforce, ModeEvaluate:
		return m, nil
	default:
		return "", fmt.Errorf("%w: unknown mode %q", ErrInvalid, s)
	}
}

// OnError says what an engine error of a gate does.
type OnError string

// The values of OnError. OnErrorFail is the default.
const (
	// OnErrorFail stops the change with ErrRuleFailed, or with
	// ErrUnavailable when the engine does not answer.
	OnErrorFail OnError = "fail"
	// OnErrorSkip counts an error, or an engine that does not answer, as
	// no match. The record shows the error and that the gate was skipped.
	OnErrorSkip OnError = "skip"
)

// ParseOnError returns the value s, or ErrInvalid.
func ParseOnError(s string) (OnError, error) {
	switch o := OnError(s); o {
	case OnErrorFail, OnErrorSkip:
		return o, nil
	default:
		return "", fmt.Errorf("%w: unknown onError %q", ErrInvalid, s)
	}
}

// Outcome is what a matched gate gives: "deny", "warn" or
// "approval:<rule ID>".
type Outcome string

// The outcomes with no rule.
const (
	OutcomeDeny Outcome = "deny"
	OutcomeWarn Outcome = "warn"
)

// approvalPrefix starts an approval outcome.
const approvalPrefix = "approval:"

// ApprovalOutcome returns the outcome that asks for the approval rule id.
func ApprovalOutcome(id ID) Outcome {
	return Outcome(approvalPrefix + string(id))
}

// ParseOutcome returns the outcome s, or ErrInvalid. The rule ID of an
// approval outcome must pass ParseRuleID, so "approval:owners" and
// "approval:default" are refused.
func ParseOutcome(s string) (Outcome, error) {
	switch o := Outcome(s); o {
	case OutcomeDeny, OutcomeWarn:
		return o, nil
	default:
	}

	rule, ok := strings.CutPrefix(s, approvalPrefix)
	if !ok {
		return "", fmt.Errorf("%w: unknown outcome %q", ErrInvalid, s)
	}

	if _, err := ParseRuleID(rule); err != nil {
		return "", fmt.Errorf("%w: outcome %q", err, s)
	}

	return Outcome(s), nil
}

// Rule returns the ID of the approval rule of o, and true, when o is an
// approval outcome.
func (o Outcome) Rule() (ID, bool) {
	rule, ok := strings.CutPrefix(string(o), approvalPrefix)
	if !ok || rule == "" {
		return "", false
	}

	return ID(rule), true
}

// State is the check state of a gate. No store holds it. The Service
// computes it when it loads the configuration: a gate whose CheckedWith is
// not the engine version and the schema version of the current build is
// checked again with Engine.Check, and a diagnostic of the gate gives
// StateInvalid. So each installation computes the same state from the same
// stored gate, with no fence and no audit entry.
type State string

// The states of a gate.
const (
	// StateValid is a gate that passed its last check.
	StateValid State = "valid"
	// StateInvalid is a gate that fails the check of the current engine or
	// fact schema. In enforce mode, it gives ErrRuleFailed at each
	// evaluation until a person saves it again.
	StateInvalid State = "invalid"
)

// CheckedWith names the engine version and the fact schema version that a
// gate passed at its last save.
type CheckedWith struct {
	// Engine is the value of Engine.Version at the save.
	Engine string
	Schema SchemaVersion
}

// Gate is a stored rule at one decision point. Its Cedar policy ID is the
// PolicyID of its scope and its ID. Condition and Unless are Cedar
// expressions over the fact document of the decision point. The engine
// parses each one and builds the policy from the parsed form, with the
// action pinned to the decision point. It never splices text.
type Gate struct {
	// Scope holds the gate. The route path names it, never the body.
	Scope Scope
	// ID is unique in the scope. Only the server makes an ID with the
	// prefix "builtin/".
	ID ID
	// Version is the stored version, 1 for a new gate. The store sets it.
	Version int64
	// DecisionPoint is the point where the gate runs.
	DecisionPoint DecisionPoint
	// Condition is the body of the when clause. Empty means true.
	Condition string
	// Unless is the body of the optional unless clause, with which a gate
	// carves out its own exemptions.
	Unless string
	// Outcome is what a match gives.
	Outcome Outcome
	// Message is the text that the record and the client show. It can
	// name facts as {{fact.path}}.
	Message string
	// Mode is enforce or evaluate.
	Mode Mode
	// OnError is fail or skip.
	OnError OnError
	// Final stops a narrower scope from shadowing the gate and from
	// passing it with a break-glass.
	Final bool
	// CheckedWith is the engine and the schema that the gate passed at its
	// last save. The Service sets it and the store keeps it.
	CheckedWith CheckedWith
	// State is computed at load (see State). The store ignores it on write
	// and gives the empty state on read.
	State State
	// Reads holds the IDs of the path sets that Condition and Unless read.
	// The service fills it from the parsed policy, so the store can refuse
	// the removal of a path set that a gate reads.
	Reads []ID
}

// PolicyID returns the Cedar policy ID of g.
func (g Gate) PolicyID() PolicyID {
	return PolicyID{Scope: g.Scope, ID: g.ID}
}

// Ref returns the scope, the ID and the version of g.
func (g Gate) Ref() ObjectRef {
	return ObjectRef{Scope: g.Scope, ID: g.ID, Version: g.Version}
}
