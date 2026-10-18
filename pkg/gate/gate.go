// Package gate is a spike of one policy model: gates at fixed decision
// points, access limits, approval rules and facts, with one decision record
// for each evaluation. It is not for merge. See docs/spikes/policy-model.md.
//
// A gate is a stored rule. Its condition is the body of a Cedar permit
// policy and it reads a typed fact document that is computed before the
// decision point. A condition never computes a fact. An access limit is a
// Cedar forbid policy that narrows what a grant allows. Approval rules hold
// data only.
package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	cedar "github.com/cedar-policy/cedar-go"

	"opendustpan.org/opendustpan/pkg/policy"
)

// ErrRuleFailed is the error of pkg/policy for a rule that failed to run. A
// Cedar diagnostic error is such a failure: the change stops, and the
// result is neither an allow nor a denial.
var ErrRuleFailed = policy.ErrRuleFailed

// Sentinel errors of this package.
var (
	// ErrInvalid reports a gate, a limit, a rule or a request that fails
	// validation at save.
	ErrInvalid = errors.New("gate is not valid")
	// ErrFinal reports a gate whose ID a narrower scope tried to replace
	// under a final gate of a wider scope.
	ErrFinal = errors.New("gate is final in a wider scope")
	// ErrLimited reports a call that an access limit forbids.
	ErrLimited = errors.New("access limit forbids the call")
	// ErrNotApprover reports an approval from a person who is not an
	// approver of any open requirement.
	ErrNotApprover = errors.New("person is not an approver of the rule")
	// ErrAuthorApproval reports an approval from the author of the change.
	ErrAuthorApproval = errors.New("the author of a change may not approve it")
	// ErrFreshAuth reports an approval without the fresh authentication
	// that the rule needs.
	ErrFreshAuth = errors.New("rule needs a fresh authentication")
	// ErrNotCleared reports a change whose decision is deny, or whose
	// approvals are open.
	ErrNotCleared = errors.New("decision does not clear the change")
	// ErrNotFound reports a decision record that does not exist.
	ErrNotFound = errors.New("decision record not found")
)

// DecisionPoint names a fixed place where gates are evaluated.
type DecisionPoint string

// The decision points of the spike.
const (
	// PointChangePlanned is the point after a plan and before an apply.
	PointChangePlanned DecisionPoint = "change.planned"
	// PointAccessLimit is the point of an access limit, after a grant
	// allowed the call.
	PointAccessLimit DecisionPoint = "access.limit"
)

// SchemaVersion is the version of the fact schema of PointChangePlanned.
const SchemaVersion = 0

// Level is the level of a scope.
type Level string

// The levels of the scope chain, widest first.
const (
	LevelInstallation Level = "installation"
	LevelNamespace    Level = "namespace"
	LevelCollection   Level = "collection"
	LevelUnit         Level = "unit"
)

// Scope says where a gate or a limit applies. Name is empty for the
// installation, "acme" for a namespace, "acme/prod" for a collection and
// "acme/prod/net" for a unit.
type Scope struct {
	Level Level  `json:"level"`
	Name  string `json:"name,omitempty"`
}

// String returns the wire form of s.
func (s Scope) String() string {
	if s.Level == LevelInstallation {
		return string(s.Level)
	}

	return string(s.Level) + ":" + s.Name
}

// Outcome is what a matched gate asks for.
type Outcome string

// The outcomes. An approval outcome is "approval:<rule>".
const (
	OutcomeDeny Outcome = "deny"
	OutcomeWarn Outcome = "warn"
)

// ApprovalOutcome returns the outcome that asks for the approval rule rule.
func ApprovalOutcome(rule string) Outcome { return Outcome("approval:" + rule) }

// Rule returns the name of the approval rule, and true, when o is an
// approval outcome.
func (o Outcome) Rule() (string, bool) {
	rule, ok := strings.CutPrefix(string(o), "approval:")

	return rule, ok && rule != ""
}

func (o Outcome) valid() bool {
	_, approval := o.Rule()

	return o == OutcomeDeny || o == OutcomeWarn || approval
}

// Mode says whether a match changes the result.
type Mode string

// The modes. An evaluate match is recorded and changes nothing.
const (
	ModeEnforce  Mode = "enforce"
	ModeEvaluate Mode = "evaluate"
)

// Gate is a stored rule at one decision point.
type Gate struct {
	ID            string        `json:"id"`
	Scope         Scope         `json:"scope"`
	DecisionPoint DecisionPoint `json:"decisionPoint"`
	// Condition is the body of the when clause of one Cedar permit policy.
	Condition string `json:"condition"`
	// Unless is the optional body of the unless clause. The gate carves its
	// own exemptions out with it: no outcome relaxes another outcome.
	Unless  string  `json:"unless,omitempty"`
	Outcome Outcome `json:"outcome"`
	Message string  `json:"message"`
	Mode    Mode    `json:"mode"`
	// Final stops a narrower scope from replacing the gate with a gate of
	// the same ID.
	Final bool `json:"final,omitempty"`
}

// Version returns a short digest of the stored fields of g.
func (g Gate) Version() string {
	return digest(g.ID, g.Scope.String(), string(g.DecisionPoint), g.Condition, g.Unless,
		string(g.Outcome), g.Message, string(g.Mode), strconv.FormatBool(g.Final))[:12]
}

func (g Gate) policyText() string {
	return permitText(string(g.DecisionPoint), g.Condition, g.Unless)
}

// permitText returns one Cedar permit policy for action, with a when and an
// optional unless clause.
func permitText(action, when, unless string) string {
	text := "permit (principal, action == Action::\"" + action + "\", resource) when { " + when + " }"
	if unless != "" {
		text += " unless { " + unless + " }"
	}

	return text + ";"
}

// Validate parses the gate. A condition that holds more than one policy,
// for example through a closing brace, is refused.
func (g Gate) Validate() error {
	switch {
	case g.ID == "":
		return fmt.Errorf("%w: missing ID", ErrInvalid)
	case strings.HasPrefix(g.ID, defaultPrefix) || strings.HasPrefix(g.ID, ownerPrefix):
		return fmt.Errorf("%w: gate ID %q starts with a reserved prefix", ErrInvalid, g.ID)
	case g.DecisionPoint != PointChangePlanned:
		return fmt.Errorf("%w: gate %s: decision point %q", ErrInvalid, g.ID, g.DecisionPoint)
	case !g.Outcome.valid():
		return fmt.Errorf("%w: gate %s: outcome %q", ErrInvalid, g.ID, g.Outcome)
	case g.Mode != ModeEnforce && g.Mode != ModeEvaluate:
		return fmt.Errorf("%w: gate %s: mode %q", ErrInvalid, g.ID, g.Mode)
	}

	_, err := parseOne(g.policyText(), cedar.Permit)
	if err != nil {
		return fmt.Errorf("%w: gate %s: %w", ErrInvalid, g.ID, err)
	}

	return nil
}

// Limit is an access limit: one Cedar forbid policy at a scope.
type Limit struct {
	ID    string `json:"id"`
	Scope Scope  `json:"scope"`
	// Policy is the text of exactly one forbid policy.
	Policy string `json:"policy"`
}

// Version returns a short digest of the stored fields of l.
func (l Limit) Version() string { return digest(l.ID, l.Scope.String(), l.Policy)[:12] }

// Validate parses the limit. A permit is refused: a limit only narrows.
func (l Limit) Validate() error {
	if l.ID == "" {
		return fmt.Errorf("%w: missing limit ID", ErrInvalid)
	}

	_, err := parseOne(l.Policy, cedar.Forbid)
	if err != nil {
		return fmt.Errorf("%w: limit %s: %w", ErrInvalid, l.ID, err)
	}

	return nil
}

func parseOne(text string, effect cedar.Effect) (*cedar.Policy, error) {
	set, err := cedar.NewPolicySetFromBytes("", []byte(text))
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	var found *cedar.Policy

	count := 0

	for _, p := range set.All() {
		found = p
		count++
	}

	if count != 1 || found == nil {
		return nil, fmt.Errorf("%w: text holds %d policies, want 1", ErrInvalid, count)
	}

	if found.Effect() != effect {
		return nil, fmt.Errorf("%w: policy is not a %s", ErrInvalid, strings.ToLower(effectName(effect)))
	}

	return found, nil
}

func effectName(e cedar.Effect) string {
	if e == cedar.Forbid {
		return "Forbid"
	}

	return "Permit"
}

func digest(parts ...string) string {
	h := sha256.New()

	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}
