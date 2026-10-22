// Package jobgate is the adapter that turns the approval defaults of a unit
// into the approval gates of a job. It implements job.Decider.
//
// The adapter reads the approval default of the namespace, of the collection
// and of the unit of a job, and gives the phases that change a gate. With no
// default set, a job needs no approval. This version expresses one
// requirement with a count and groups. A set of defaults that needs more, or
// that asks for something a job gate cannot enforce, makes the start fail:
// the adapter never weakens a requirement.
package jobgate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// Snapshotter reads the configuration of a chain of scopes in one
// transaction. gate.Store implements it.
type Snapshotter interface {
	// Snapshot returns each kind of object and each setting of each scope of
	// scopes. It returns gate.ErrInvalid for a zero scope.
	Snapshot(ctx context.Context, scopes []gate.Scope) (gate.Config, error)
}

// Config holds the ports of a Decider.
type Config struct {
	Config  Snapshotter
	Members gate.Membership
}

// Decider is a job.Decider. It allows every start, and adds the approval gate
// of the approval defaults to each phase that changes.
type Decider struct {
	config  Snapshotter
	members gate.Membership
}

var _ job.Decider = (*Decider)(nil)

// New returns a Decider. It returns gate.ErrInvalid when a port is nil.
func New(cfg Config) (*Decider, error) {
	if cfg.Config == nil || cfg.Members == nil {
		return nil, fmt.Errorf("%w: the job gate decider needs a configuration source and a membership source",
			gate.ErrInvalid)
	}

	return &Decider{config: cfg.Config, members: cfg.Members}, nil
}

// PolicyVersion is the PolicyVersion of a decision that comes from the
// approval defaults. The audit entry of the start holds it.
const PolicyVersion = "approval-defaults"

// Decide returns the decision for in. The start is allowed. A phase that
// changes gets the gate of the approval defaults of the namespace, the
// collection and the unit of the job. A plan phase gets none.
//
// It returns an error, and so the start fails, when the defaults need more
// than this version enforces: more than one requirement, approvers that are
// users or unit owners, a fresh authentication window, or an unless body.
// The error wraps gate.ErrUnavailable. It also returns the errors of the
// store.
//
//nolint:gocritic // The signature is fixed by job.Decider, which takes the input by value.
func (d *Decider) Decide(ctx context.Context, in job.DecisionInput) (job.Decision, error) {
	unit := in.Spec.Unit
	if !unit.IsUnit() {
		return job.Decision{}, fmt.Errorf("%w: %q is not a unit", gate.ErrInvalid, unit.String())
	}

	kinds, err := job.PhasesFor(in.Spec.Executor, in.Spec.Operation, in.Spec.Purpose)
	if err != nil {
		return job.Decision{}, fmt.Errorf("phases of the job: %w", err)
	}

	if !slices.ContainsFunc(kinds, job.PhaseKind.Changes) {
		return allow(nil), nil
	}

	g, found, err := d.gateOfUnit(ctx, unit)
	if err != nil {
		return job.Decision{}, fmt.Errorf("approval defaults of %s: %w", unit.String(), err)
	}

	if !found {
		return allow(nil), nil
	}

	gates := make(map[job.PhaseKind]job.Gate)

	for i, k := range kinds {
		if k.Changes() {
			phaseGate := g
			// A gate binds its approvals to the plan of the phase before.
			phaseGate.BindPlan = i > 0 && kinds[i-1] == job.PhasePlan
			gates[k] = phaseGate
		}
	}

	return allow(gates), nil
}

// gateOfUnit reads the approval defaults of the scopes of unit and returns
// their gate, and false when no default is set.
func (d *Decider) gateOfUnit(ctx context.Context, unit identity.Object) (job.Gate, bool, error) {
	col, err := d.members.Collection(ctx, unit)
	if err != nil && !errors.Is(err, gate.ErrNotFound) {
		return job.Gate{}, false, fmt.Errorf("collection: %w", err)
	}

	chain, err := gate.UnitChain(unit, col)
	if err != nil {
		return job.Gate{}, false, fmt.Errorf("scopes: %w", err)
	}

	snapshot, err := d.config.Snapshot(ctx, chain)
	if err != nil {
		return job.Gate{}, false, fmt.Errorf("read: %w", err)
	}

	reqs, err := gate.DefaultRequirements(inChain(snapshot.ApprovalDefaults, chain))
	if err != nil || len(reqs) == 0 {
		return job.Gate{}, false, err
	}

	g, err := gateOf(reqs)
	if err != nil {
		return job.Gate{}, false, err
	}

	return g, true, nil
}

func allow(gates map[job.PhaseKind]job.Gate) job.Decision {
	return job.Decision{Allow: true, Reason: "", Gates: gates, PolicyVersion: PolicyVersion}
}

// inChain returns the defaults whose scope is in chain.
//
// Defense in depth: the store returns only the scopes it was asked for. This
// filter stays so that a source that returns more cannot add the requirement
// of another namespace to this unit.
func inChain(defaults []gate.ApprovalDefault, chain []gate.Scope) []gate.ApprovalDefault {
	out := make([]gate.ApprovalDefault, 0, len(defaults))

	for i := range defaults {
		if slices.ContainsFunc(chain, func(s gate.Scope) bool { return s.Key() == defaults[i].Scope.Key() }) {
			out = append(out, defaults[i])
		}
	}

	return out
}

// gateOf returns the job gate for reqs. A job gate holds a count and groups,
// and its approvers are distinct people of one requirement. So it expresses
// one requirement of groups, and nothing else. For anything else it returns
// an error that wraps gate.ErrUnavailable and says what to change.
func gateOf(reqs []gate.Requirement) (job.Gate, error) {
	if len(reqs) > 1 {
		return job.Gate{}, fmt.Errorf("%w: %d approval requirements apply (%s), and a job enforces one; "+
			"set the approval default in one scope only", gate.ErrUnavailable, len(reqs), names(reqs))
	}

	r := reqs[0]

	switch {
	case len(r.Approvers.Users) > 0 || r.Approvers.UnitOwners:
		return job.Gate{}, fmt.Errorf("%w: the requirement %s names users or unit owners, and a job enforces "+
			"groups only; name groups instead", gate.ErrUnavailable, r.Name)
	case r.FreshAuth.Set:
		return job.Gate{}, fmt.Errorf("%w: the requirement %s asks for a fresh authentication, and a job "+
			"approval does not enforce it; remove freshAuthSeconds", gate.ErrUnavailable, r.Name)
	}

	return job.Gate{
		Type:      job.GateApproval,
		Approvals: r.Count,
		Groups:    slices.Clone(r.Approvers.Groups),
		AllowSelf: r.AuthorMayApprove,
		Timeout:   time.Duration(r.TimeoutSeconds) * time.Second,
		BindPlan:  false,
	}, nil
}

func names(reqs []gate.Requirement) string {
	out := make([]string, 0, len(reqs))

	for i := range reqs {
		out = append(out, string(reqs[i].Name))
	}

	return strings.Join(out, ", ")
}
