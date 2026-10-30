// Package policy is the port of edit policy: code that decides whether a
// change goes in now, waits for approvals, or does not go in. A policy reads
// a tool-neutral input document and gives a decision.
//
// The package holds the port Engine, the input and decision types, the rule
// that joins the decisions of more than one engine, and the contracts of the
// default adapters: the built-in Expr engine, an OPA server over its REST
// API, Cedar, and a webhook that the server signs. It imports no transport
// and no store.
//
// An error of an engine is a failure of the policy, never a denial and never
// an allow. A caller that gets an error changes nothing and tries again
// later.
package policy

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/principal"
)

var (
	// ErrUnavailable reports an engine that did not answer in time, or that
	// the server could not reach.
	ErrUnavailable = errors.New("policy engine is not available")
	// ErrInvalidDecision reports an answer of an engine that does not parse
	// as a decision, or that is outside the limits of a decision. An
	// undefined result of an OPA query is such an answer, not a denial.
	ErrInvalidDecision = errors.New("policy engine gave a decision that is not valid")
	// ErrUnsupportedKind reports an input of a kind that the engine does not
	// decide.
	ErrUnsupportedKind = errors.New("policy engine does not decide this kind of input")
	// ErrInvalidInput reports an input with an empty kind, a schema version
	// below 1 or an empty document.
	ErrInvalidInput = errors.New("policy input is not valid")
	// ErrRuleFailed reports a rule of a policy whose condition failed to
	// run, for example an Expr condition that reads a missing field or
	// passes its time limit. The engine gives no decision: a rule that
	// cannot run is never a pass, so the engine does not skip it and decide
	// on the other rules.
	ErrRuleFailed = errors.New("policy rule failed to run")
)

// Kind names what an input document describes. Each kind has its own
// document, versioned by Input.SchemaVersion.
type Kind string

// The kinds of input.
const (
	// KindCatalogRequest is a revision of a Catalog request at a recheck
	// point.
	KindCatalogRequest Kind = "catalog.request"
	// KindJobStart is the start of a job.
	KindJobStart Kind = "job.start"
)

// Valid reports whether k is one of the kinds of this package.
func (k Kind) Valid() bool {
	return k == KindCatalogRequest || k == KindJobStart
}

// Input is what an engine decides on. Document is the JSON document of Kind
// at SchemaVersion. It never holds a secret value.
type Input struct {
	Kind          Kind
	SchemaVersion int
	Document      []byte
}

// Validate reports whether in can go to an engine. It returns
// ErrInvalidInput otherwise.
func (in Input) Validate() error {
	switch {
	case !in.Kind.Valid():
		return fmt.Errorf("%w: kind %q", ErrInvalidInput, in.Kind)
	case in.SchemaVersion < 1:
		return fmt.Errorf("%w: schema version %d", ErrInvalidInput, in.SchemaVersion)
	case len(in.Document) == 0:
		return fmt.Errorf("%w: empty document", ErrInvalidInput)
	default:
		return nil
	}
}

// Effect is what a decision lets happen.
type Effect string

// The effects of a decision, from the least to the most strict.
const (
	// EffectAllow lets the change go in with no approval.
	EffectAllow Effect = "allow"
	// EffectApprove lets the change go in after the approvals of the
	// decision.
	EffectApprove Effect = "approve"
	// EffectDeny stops the change.
	EffectDeny Effect = "deny"
)

// Valid reports whether e is one of the effects of this package.
func (e Effect) Valid() bool {
	return e == EffectAllow || e == EffectApprove || e == EffectDeny
}

// Limits of a decision. An answer of an engine outside them is
// ErrInvalidDecision.
const (
	// MaxReasons bounds the reasons of one decision.
	MaxReasons = 32
	// MaxReasonBytes bounds one reason.
	MaxReasonBytes = 1 << 10
	// MaxApprovals bounds the approvals that one decision asks for.
	MaxApprovals = 10
	// MaxApprovalGroups bounds the groups of the approvals of one decision.
	MaxApprovalGroups = 32
	// MaxAnswerBytes bounds the answer of an engine over the network.
	MaxAnswerBytes = 64 << 10
)

// Approvals is what a decision of EffectApprove asks for: Count approvals
// by members of Groups. Empty Groups lets any principal with the approve
// action approve.
type Approvals struct {
	Groups []string `json:"groups,omitempty"`
	Count  int      `json:"count"`
}

// Decision is the result of one evaluation. Engine names the adapter and
// PolicyVersion the version of the policy that decided, for the record and
// the audit entry.
type Decision struct {
	Effect        Effect     `json:"effect"`
	Reasons       []string   `json:"reasons,omitempty"`
	Approvals     *Approvals `json:"approvals,omitempty"`
	Engine        string     `json:"engine,omitempty"`
	PolicyVersion string     `json:"policyVersion,omitempty"`
}

// Validate reports whether d is a decision within the limits. It returns
// ErrInvalidDecision otherwise. A decision of EffectApprove needs from 1 to
// MaxApprovals approvals, by at most MaxApprovalGroups groups that each have a
// valid group name. Another effect has no approvals. The error text
// never quotes a value of d, because d can come from the answer of an
// engine over the network.
func (d Decision) Validate() error {
	if !d.Effect.Valid() {
		return fmt.Errorf("%w: the effect is not allow, approve or deny", ErrInvalidDecision)
	}

	if err := validateReasons(d.Reasons); err != nil {
		return err
	}

	switch {
	case d.Effect == EffectApprove && d.Approvals == nil:
		return fmt.Errorf("%w: approve with no approvals", ErrInvalidDecision)
	case d.Effect == EffectApprove && (d.Approvals.Count < 1 || d.Approvals.Count > MaxApprovals):
		return fmt.Errorf("%w: %d approvals, give 1 to %d", ErrInvalidDecision, d.Approvals.Count, MaxApprovals)
	case d.Effect != EffectApprove && d.Approvals != nil:
		return fmt.Errorf("%w: approvals on the effect %q", ErrInvalidDecision, d.Effect)
	case d.Approvals != nil:
		return validateGroups(d.Approvals.Groups)
	default:
		return nil
	}
}

// validateGroups checks the approver groups of a decision. The answer of an
// engine over the network is untrusted, so a group name is checked here too,
// before the caller resolves it.
func validateGroups(groups []string) error {
	if len(groups) > MaxApprovalGroups {
		return fmt.Errorf("%w: %d approver groups, the limit is %d", ErrInvalidDecision, len(groups),
			MaxApprovalGroups)
	}

	for i, g := range groups {
		if principal.ValidateGroupName(g) != nil {
			return fmt.Errorf("%w: the approver group %d has a name that is not valid", ErrInvalidDecision, i)
		}
	}

	return nil
}

func validateReasons(reasons []string) error {
	if len(reasons) > MaxReasons {
		return fmt.Errorf("%w: %d reasons, the limit is %d", ErrInvalidDecision, len(reasons), MaxReasons)
	}

	for _, r := range reasons {
		if len(r) > MaxReasonBytes {
			return fmt.Errorf("%w: a reason of %d bytes, the limit is %d", ErrInvalidDecision, len(r), MaxReasonBytes)
		}
	}

	return nil
}

// Engine is the port of a policy engine. The server ships four adapters:
// the built-in Expr engine, an OPA server over its Data API, Cedar, and a
// signed webhook (see Sign). A team can write its own and build its own
// binary with the SDK. Each adapter runs the suite in package policytest.
type Engine interface {
	// Decide returns the decision for in. It returns an error that wraps
	// ErrInvalidInput for an input that fails Input.Validate,
	// ErrUnsupportedKind for a kind that the engine does not decide,
	// ErrUnavailable when the engine does not answer within the deadline of
	// ctx, ErrInvalidDecision for an answer that is not a valid decision,
	// and ErrRuleFailed when a rule of the policy fails to run. A rule that
	// fails to run makes the whole decision an error, also when another
	// rule matches. An error is never a denial and never an allow: the
	// caller changes nothing, and a recheck stops.
	Decide(ctx context.Context, in Input) (Decision, error)
}

// Combined is the join of the decisions of more than one engine (Combine).
// Each item of Approvals is one requirement, and a change needs each of
// them: the approvals of two engines never merge into one requirement with
// the groups of both.
type Combined struct {
	Effect    Effect      `json:"effect"`
	Reasons   []string    `json:"reasons,omitempty"`
	Approvals []Approvals `json:"approvals,omitempty"`
}

// Combine joins the decisions of more than one engine: the strictest effect
// wins. One deny gives deny, with the reasons of each deny. Else one approve
// gives approve, with one requirement for each approve: its count of
// approvals by members of its groups, with the groups sorted and with no
// duplicates. Two approves with the same groups give one requirement with
// the higher count. So an approve by security and an approve by dba need an
// approval by security and an approval by dba, never one approval by
// either. The requirements are sorted by their groups. Else the result is
// allow, also for no decision. The record keeps each decision with its own
// Engine and PolicyVersion.
//
// Combine first checks each decision with Decision.Validate, and returns an
// error that wraps ErrInvalidDecision and names the index of the first
// decision that fails: an empty or unknown effect, an approve with no
// approvals or with a count outside 1 to MaxApprovals, or approvals on
// another effect. So a decision that an engine or a caller built wrong never
// becomes an allow.
func Combine(ds []Decision) (Combined, error) {
	for i := range ds {
		if err := ds[i].Validate(); err != nil {
			return Combined{}, fmt.Errorf("decision %d: %w", i, err)
		}
	}

	out := Combined{Effect: EffectAllow, Reasons: nil, Approvals: nil}

	for i := range ds {
		if ds[i].Effect == EffectDeny {
			out.Effect = EffectDeny
			out.Reasons = append(out.Reasons, ds[i].Reasons...)
		}
	}

	if out.Effect == EffectDeny {
		return out, nil
	}

	return combineApprovals(ds), nil
}

// combineApprovals joins the decisions of EffectApprove of ds, which
// Decision.Validate checked: one requirement for each set of groups, with
// the highest count of that set. With no approve, the result is allow.
func combineApprovals(ds []Decision) Combined {
	out := Combined{Effect: EffectAllow, Reasons: nil, Approvals: nil}

	for i := range ds {
		if ds[i].Effect != EffectApprove || ds[i].Approvals == nil {
			continue
		}

		out.Effect = EffectApprove
		out.Reasons = append(out.Reasons, ds[i].Reasons...)

		groups := slices.Clone(ds[i].Approvals.Groups)
		slices.Sort(groups)
		groups = slices.Compact(groups)

		at := slices.IndexFunc(out.Approvals, func(a Approvals) bool { return slices.Equal(a.Groups, groups) })
		if at >= 0 {
			out.Approvals[at].Count = max(out.Approvals[at].Count, ds[i].Approvals.Count)

			continue
		}

		out.Approvals = append(out.Approvals, Approvals{Groups: groups, Count: ds[i].Approvals.Count})
	}

	slices.SortFunc(out.Approvals, func(a, b Approvals) int { return slices.Compare(a.Groups, b.Groups) })

	return out
}
