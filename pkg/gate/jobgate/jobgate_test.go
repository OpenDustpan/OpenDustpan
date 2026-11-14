package jobgate_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/jobgate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// constError is a static error for the failure of a port in a test.
type constError string

func (e constError) Error() string { return string(e) }

const errBoom = constError("boom")

// source answers Snapshot with cfg and records the scopes it was asked for.
type source struct {
	cfg    gate.Config
	err    error
	scopes []gate.Scope
	calls  int
}

func (s *source) Snapshot(_ context.Context, scopes []gate.Scope) (gate.Config, error) {
	s.calls++
	s.scopes = scopes

	return s.cfg, s.err
}

// withDefaults returns a source that holds defaults.
func withDefaults(defaults ...gate.ApprovalDefault) *source {
	return &source{
		cfg: gate.Config{
			Gates: nil, AccessLimits: nil, ApprovalRules: nil, OwnershipRules: nil, PathSets: nil,
			ApprovalDefaults: defaults, CarryOver: nil,
		},
		err: nil, scopes: nil, calls: 0,
	}
}

// failingSource returns a source whose Snapshot fails with err.
func failingSource(err error) *source {
	s := withDefaults()
	s.err = err

	return s
}

// members puts every unit in the collection col, or in none for "".
type members struct {
	col collection.Name
	err error
}

func inCollection(col collection.Name) members { return members{col: col, err: nil} }

func failingMembers(err error) members { return members{col: "", err: err} }

func (m members) Collection(context.Context, identity.Object) (collection.Name, error) {
	if m.err != nil {
		return "", m.err
	}

	if m.col == "" {
		return "", gate.ErrNotFound
	}

	return m.col, nil
}

func unit(t *testing.T) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	return u
}

func input(t *testing.T, executor job.ExecutorKind, op job.Operation) job.DecisionInput {
	t.Helper()

	return job.DecisionInput{
		Principal: identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: zeroTime(), Subject: "bo", DisplayName: "Bo",
			Admin: false, TokenID: "", TokenExpiresAt: zeroTime(), Kind: "", Credential: "", JobID: "",
		},
		Agent: "", AgentMode: "", Source: job.SourceAPI, Keys: nil,
		Spec: job.Spec{
			Unit: unit(t), Operation: op, Purpose: "", Executor: executor, Location: job.Location{
				Connection: "", Repository: "", Ref: "", Directory: "", Revision: "",
			},
			LocationSource: "",
			Steps:          nil, Pool: "", Profile: "", Edits: nil, Delivery: "",
			Defaulted: nil,
		},
	}
}

// def returns an approval default of scope with count and groups.
func def(scope gate.Scope, count int, groups ...string) gate.ApprovalDefault {
	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = "id-" + g
	}

	return gate.ApprovalDefault{
		Scope: scope, Version: 1, Count: count, FreshAuth: gate.NoFreshAuth(),
		Approvers: gate.Approvers{Groups: groups, GroupIDs: ids, Users: nil, UnitOwners: false}, Unless: "", Reads: nil,
	}
}

func newDecider(t *testing.T, src *source, m members) *jobgate.Decider {
	t.Helper()

	d, err := jobgate.New(jobgate.Config{Config: src, Members: m})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return d
}

func nsScope() gate.Scope { return gate.NamespaceScope("acme") }

func colScope() gate.Scope { return gate.CollectionScope("acme", "prod") }

func unitScope(t *testing.T) gate.Scope {
	t.Helper()

	s, err := gate.UnitScope("acme", "pulumi", "web/prod")
	if err != nil {
		t.Fatalf("unit scope: %v", err)
	}

	return s
}

// TestDecideWithNoDefaultNeedsNoApproval proves the opt-in: with no default
// set, a start is allowed and no phase gets a gate.
func TestDecideWithNoDefaultNeedsNoApproval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		exec job.ExecutorKind
		op   job.Operation
	}{
		{"runner apply", job.ExecutorRunner, job.OperationApply},
		{"runner destroy", job.ExecutorRunner, job.OperationDestroy},
		{"write-back apply", job.ExecutorWriteBack, job.OperationApply},
	}

	for _, tt := range tests {
		d := newDecider(t, withDefaults(), inCollection(""))

		got, err := d.Decide(t.Context(), input(t, tt.exec, tt.op))
		if err != nil || !got.Allow || len(got.Gates) != 0 {
			t.Fatalf("%s: Decide() = %+v, %v; want allow with no gate", tt.name, got, err)
		}
	}
}

// gateCase is one case of TestDecideGatesTheApplyPhase.
type gateCase struct {
	name     string
	exec     job.ExecutorKind
	op       job.Operation
	defaults []gate.ApprovalDefault
	kind     job.PhaseKind
	count    int
	groups   int
	bind     bool
}

// TestDecideGatesTheApplyPhase proves that a default gives the phase that
// changes a gate with its count, and leaves the plan phase open.
func TestDecideGatesTheApplyPhase(t *testing.T) {
	t.Parallel()

	run, wb := job.ExecutorRunner, job.ExecutorWriteBack
	apply, destroy, refresh := job.OperationApply, job.OperationDestroy, job.OperationRefresh

	tests := []gateCase{
		{"namespace default", run, apply, []gate.ApprovalDefault{def(nsScope(), 2)}, job.PhaseApply, 2, 0, true},
		{"destroy is gated too", run, destroy, []gate.ApprovalDefault{def(nsScope(), 1)}, job.PhaseApply, 1, 0, true},
		{
			"refresh has no plan to bind",
			run,
			refresh,
			[]gate.ApprovalDefault{def(nsScope(), 1)},
			job.PhaseApply,
			1,
			0,
			false,
		},
		{"write-back deliver", wb, apply, []gate.ApprovalDefault{def(nsScope(), 1)}, job.PhaseDeliver, 1, 0, false},
		{
			"strictest of three scopes", run, apply,
			[]gate.ApprovalDefault{def(nsScope(), 3), def(colScope(), 1), def(unitScope(t), 2)},
			job.PhaseApply, 3, 0, true,
		},
		{
			"a narrower default raises", run, apply,
			[]gate.ApprovalDefault{def(nsScope(), 1), def(colScope(), 4)},
			job.PhaseApply, 4, 0, true,
		},
		{
			"one named group",
			run,
			apply,
			[]gate.ApprovalDefault{def(nsScope(), 2, "platform")},
			job.PhaseApply,
			2,
			1,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkGate(t, tt)
		})
	}
}

func checkGate(t *testing.T, tt gateCase) {
	t.Helper()

	d := newDecider(t, withDefaults(tt.defaults...), inCollection("prod"))

	got, err := d.Decide(t.Context(), input(t, tt.exec, tt.op))
	if err != nil || !got.Allow {
		t.Fatalf("Decide() = %+v, %v; want allow", got, err)
	}

	if len(got.Gates) != 1 {
		t.Fatalf("gates = %+v, want one gate for %s", got.Gates, tt.kind)
	}

	g, ok := got.Gates[tt.kind]
	if !ok || g.Type != job.GateApproval || g.Approvals != tt.count || g.AllowSelf || g.BindPlan != tt.bind ||
		len(g.Groups) != tt.groups {
		t.Fatalf("gate of %s = %+v, want %d approvals, %d groups, bind %t, no self approval", tt.kind, g,
			tt.count, tt.groups, tt.bind)
	}

	if g.Timeout != gate.DefaultApprovalWait {
		t.Fatalf("timeout = %s, want %s", g.Timeout, gate.DefaultApprovalWait)
	}
}

// TestDecideNamesTheGroupsAndNeverCountsTheAuthor proves that the gate lists
// the groups of the default by name, and that the author never counts as an
// approver, also when the author is in an approver group: the default sets no
// way to let the author approve.
func TestDecideNamesTheGroupsAndNeverCountsTheAuthor(t *testing.T) {
	t.Parallel()

	d := newDecider(t, withDefaults(def(nsScope(), 2, "platform", "bo-team")), inCollection("prod"))
	in := input(t, job.ExecutorRunner, job.OperationApply)

	got, err := d.Decide(t.Context(), in)
	if err != nil || !got.Allow {
		t.Fatalf("Decide() = %+v, %v; want allow", got, err)
	}

	g, ok := got.Gates[job.PhaseApply]
	if !ok {
		t.Fatalf("gates = %+v, want a gate on the apply phase", got.Gates)
	}

	if !slices.Equal(g.Groups, []string{"platform", "bo-team"}) {
		t.Fatalf("groups = %v, want [platform bo-team]", g.Groups)
	}

	if !slices.Equal(g.GroupIDs, []string{"id-platform", "id-bo-team"}) {
		t.Fatalf(
			"group IDs = %v, want [id-platform id-bo-team]: the gate keeps the IDs, not only the names",
			g.GroupIDs,
		)
	}

	if g.AllowSelf {
		t.Fatalf("gate = %+v, want no self approval for %s", g, in.Principal.Subject)
	}
}

// TestDecideRefusesGroupsWithoutIDs proves that a default that names groups
// and holds no group IDs, as a default saved before the IDs existed, makes the
// start fail: the gate never falls back to the group names.
func TestDecideRefusesGroupsWithoutIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ids  []string
		want error
	}{
		{"no IDs", nil, gate.ErrUnavailable},
		// Defense in depth: the validation of the default refuses this too.
		{"fewer IDs than names", []string{"id-platform"}, gate.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d0 := def(nsScope(), 2, "platform", "bo-team")
			d0.Approvers.GroupIDs = tt.ids

			d := newDecider(t, withDefaults(d0), inCollection("prod"))

			got, err := d.Decide(t.Context(), input(t, job.ExecutorRunner, job.OperationApply))
			if !errors.Is(err, tt.want) || got.Allow {
				t.Fatalf("Decide() = %+v, %v; want %v and no allow", got, err, tt.want)
			}
		})
	}
}

// TestDecideReadsTheChainOfTheUnit proves that the decider asks for the
// installation, the namespace, the collection and the unit, and for no
// collection scope when the unit is in none.
func TestDecideReadsTheChainOfTheUnit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		col  collection.Name
		want int
	}{
		{"unit in a collection", "prod", 4},
		{"unit in no collection", "", 3},
	}

	for _, tt := range tests {
		src := withDefaults()
		d := newDecider(t, src, inCollection(tt.col))

		if _, err := d.Decide(t.Context(), input(t, job.ExecutorRunner, job.OperationApply)); err != nil {
			t.Fatalf("%s: Decide() = %v", tt.name, err)
		}

		if len(src.scopes) != tt.want || src.scopes[1] != nsScope() {
			t.Fatalf("%s: scopes = %v, want %d with the namespace second", tt.name, src.scopes, tt.want)
		}
	}
}

// TestDecideIgnoresADefaultOutsideTheChain proves that a default of another
// namespace, which a source returned by mistake, adds no requirement.
func TestDecideIgnoresADefaultOutsideTheChain(t *testing.T) {
	t.Parallel()

	src := withDefaults(def(gate.NamespaceScope("beta"), 9), def(gate.CollectionScope("acme", "dev"), 9))
	d := newDecider(t, src, inCollection("prod"))

	got, err := d.Decide(t.Context(), input(t, job.ExecutorRunner, job.OperationApply))
	if err != nil || len(got.Gates) != 0 {
		t.Fatalf("Decide() = %+v, %v; want no gate", got, err)
	}
}

// TestDecideAPlanReadsNothing proves that a job that only plans needs no
// approval and never reads the store.
func TestDecideAPlanReadsNothing(t *testing.T) {
	t.Parallel()

	src := withDefaults(def(nsScope(), 5))
	d := newDecider(t, src, inCollection(""))

	got, err := d.Decide(t.Context(), input(t, job.ExecutorRunner, job.OperationPlan))
	if err != nil || !got.Allow || len(got.Gates) != 0 || src.calls != 0 {
		t.Fatalf("Decide(plan) = %+v, %v after %d reads; want allow, no gate and no read", got, err, src.calls)
	}
}

// TestDecideADriftCheckNeedsNoApproval proves that a drift check has no gate,
// also when a default asks for approvals and the start comes from a schedule:
// a drift check has the plan phase alone and changes nothing, so the decider
// reads no default for it.
func TestDecideADriftCheckNeedsNoApproval(t *testing.T) {
	t.Parallel()

	for _, source := range []job.SourceKind{job.SourceAPI, job.SourceSchedule, job.SourceTrigger} {
		src := withDefaults(def(nsScope(), 5), def(colScope(), 2), def(unitScope(t), 3))
		d := newDecider(t, src, inCollection("prod"))

		in := input(t, job.ExecutorRunner, job.OperationRefresh)
		in.Spec.Purpose, in.Source = job.PurposeDriftCheck, source

		got, err := d.Decide(t.Context(), in)
		if err != nil || !got.Allow || len(got.Gates) != 0 || src.calls != 0 {
			t.Fatalf("Decide(drift check from %s) = %+v, %v after %d reads; want allow, no gate and no read", source,
				got, err, src.calls)
		}
	}

	// The same refresh with no purpose writes state, so the default gates it.
	src := withDefaults(def(nsScope(), 2))
	d := newDecider(t, src, inCollection(""))

	got, err := d.Decide(t.Context(), input(t, job.ExecutorRunner, job.OperationRefresh))
	if err != nil || len(got.Gates) != 1 {
		t.Fatalf("Decide(refresh) = %+v, %v; want one gate on the apply phase", got, err)
	}
}

// TestDecideFailsClosed proves that defaults which a job cannot enforce make
// the start fail with ErrUnavailable, and that a stored default which is not
// valid fails with ErrInvalid. No case returns a decision that allows.
func TestDecideFailsClosed(t *testing.T) {
	t.Parallel()

	users := def(nsScope(), 1)
	users.Approvers.Users = []string{"bo"}

	owners := def(nsScope(), 1)
	owners.Approvers.UnitOwners = true

	fresh := def(nsScope(), 1)
	fresh.FreshAuth = gate.FreshAuth{Set: true, Seconds: 0}

	unless := def(nsScope(), 1)
	unless.Unless = "true"

	tests := []struct {
		name     string
		defaults []gate.ApprovalDefault
		want     error
	}{
		{
			"two requirements",
			[]gate.ApprovalDefault{def(nsScope(), 2, "platform"), def(colScope(), 1)},
			gate.ErrUnavailable,
		},
		{
			"two named defaults",
			[]gate.ApprovalDefault{def(nsScope(), 2, "platform"), def(colScope(), 1, "dba")},
			gate.ErrUnavailable,
		},
		{"users", []gate.ApprovalDefault{users}, gate.ErrUnavailable},
		{"unit owners", []gate.ApprovalDefault{owners}, gate.ErrUnavailable},
		{"fresh authentication", []gate.ApprovalDefault{fresh}, gate.ErrUnavailable},
		{"unless body", []gate.ApprovalDefault{unless}, gate.ErrUnavailable},
		{"count zero in a stored row", []gate.ApprovalDefault{def(nsScope(), 0)}, gate.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newDecider(t, withDefaults(tt.defaults...), inCollection("prod"))

			got, err := d.Decide(t.Context(), input(t, job.ExecutorRunner, job.OperationApply))
			if !errors.Is(err, tt.want) || got.Allow || len(got.Gates) != 0 {
				t.Fatalf("Decide() = %+v, %v; want a refusal that wraps %v", got, err, tt.want)
			}
		})
	}
}

// TestDecideStoreErrorsRefuse proves that an error of the store or of the
// membership refuses the start, and that a bad unit is refused.
func TestDecideStoreErrorsRefuse(t *testing.T) {
	t.Parallel()

	apply := input(t, job.ExecutorRunner, job.OperationApply)

	d := newDecider(t, failingSource(errBoom), inCollection(""))

	got, err := d.Decide(t.Context(), apply)
	if !errors.Is(err, errBoom) || got.Allow {
		t.Fatalf("store error: Decide() = %+v, %v; want the error and no allow", got, err)
	}

	d = newDecider(t, withDefaults(), failingMembers(errBoom))

	got, err = d.Decide(t.Context(), apply)
	if !errors.Is(err, errBoom) || got.Allow {
		t.Fatalf("membership error: Decide() = %+v, %v; want the error and no allow", got, err)
	}

	d = newDecider(t, withDefaults(), inCollection(""))

	notUnit := apply
	notUnit.Spec.Unit = identity.NamespaceObject("acme")

	got, err = d.Decide(t.Context(), notUnit)
	if !errors.Is(err, gate.ErrInvalid) || got.Allow {
		t.Fatalf("not a unit: Decide() = %+v, %v; want ErrInvalid and no allow", got, err)
	}
}

// TestNewNeedsPorts proves that a decider without a port is refused.
func TestNewNeedsPorts(t *testing.T) {
	t.Parallel()

	if _, err := jobgate.New(jobgate.Config{Config: nil, Members: inCollection("")}); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("no source = %v, want ErrInvalid", err)
	}

	if _, err := jobgate.New(jobgate.Config{Config: withDefaults(), Members: nil}); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("no members = %v, want ErrInvalid", err)
	}
}

// zeroTime returns the zero time, for the fields that a test leaves unset.
func zeroTime() time.Time { return time.Time{} }
