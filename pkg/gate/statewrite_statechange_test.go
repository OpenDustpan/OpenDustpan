package gate_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// rollbackWrite returns a rollback of the unit web that changes state as sc
// says.
func rollbackWrite(t *testing.T, sc *gate.StateChange) gate.StateWrite {
	t.Helper()

	w := swWrite(t, "alice", "web", gate.OperationRollback)
	w.State = sc

	return w
}

func okChange() *gate.StateChange {
	return &gate.StateChange{
		Current:   "sv-current",
		Resources: 3,
		Removed:   []string{"aws_s3_bucket.data", "aws_iam_role.app"},
	}
}

// TestStateFactsAreFilledFromTheStateChange proves that each state fact comes
// from the state change, that a gate reads it, and that the decision record
// names the facts that the gate read.
func TestStateFactsAreFilledFromTheStateChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		state      *gate.StateChange
		condition  string
		wantRefuse bool
		message    string
		wantFacts  gate.StateFacts
	}{
		{
			name: "a removal that the gate denies", state: okChange(),
			condition:  `context.state.available && context.state.removed.contains("aws_s3_bucket.data")`,
			wantRefuse: true, message: "Removes aws_iam_role.app, aws_s3_bucket.data from 3 resources",
			wantFacts: gate.StateFacts{
				Available: true, Resources: 3, Removed: []string{"aws_iam_role.app", "aws_s3_bucket.data"},
			},
		},
		{
			name: "the resource count is filled", state: okChange(),
			condition:  `context.state.available && context.state.resources == 3`,
			wantRefuse: true, message: "Removes aws_iam_role.app, aws_s3_bucket.data from 3 resources",
			wantFacts: gate.StateFacts{
				Available: true, Resources: 3, Removed: []string{"aws_iam_role.app", "aws_s3_bucket.data"},
			},
		},
		{
			name:       "a removal that no gate names is allowed",
			state:      &gate.StateChange{Current: "sv-current", Resources: 5, Removed: []string{}},
			condition:  `context.state.available && context.state.removed.contains("aws_s3_bucket.data")`,
			wantRefuse: false, message: "",
			wantFacts: gate.StateFacts{Available: true, Resources: 5, Removed: []string{}},
		},
		{
			name: "no state change gives available false and the zero facts", state: nil,
			condition:  `context.state.available && context.state.removed.contains("aws_s3_bucket.data")`,
			wantRefuse: false, message: "",
			wantFacts: gate.StateFacts{Available: false, Resources: 0, Removed: nil},
		},
		{
			name: "duplicate addresses count once and the list is sorted",
			state: &gate.StateChange{
				Current: "sv-current", Resources: 1, Removed: []string{"b.x", "a.x", "b.x"},
			},
			condition:  `context.state.available && context.state.removed.contains("a.x")`,
			wantRefuse: true, message: "Removes a.x, b.x from 1 resources",
			wantFacts: gate.StateFacts{Available: true, Resources: 1, Removed: []string{"a.x", "b.x"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSWEnv(t, swUnitsOfTests(), nil)
			e.putGate(t, newSWGate(atUnit, "keep", tc.condition, gate.OutcomeDeny,
				"Removes {{state.removed}} from {{state.resources}} resources"))

			d, err := e.svc.GuardStateWrite(t.Context(), rollbackWrite(t, tc.state))
			if tc.wantRefuse != (err != nil) || (tc.wantRefuse && !errors.Is(err, gate.ErrDenied)) {
				t.Fatalf("GuardStateWrite() = %+v, %v, want refuse %v", d, err, tc.wantRefuse)
			}

			if tc.wantRefuse && !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %q, want the facts %q in the message", err, tc.message)
			}

			checkStateRecord(t, e.newestRecord(t), stateExpect{facts: tc.wantFacts, matched: tc.wantRefuse})
		})
	}
}

// stateExpect is what a decision record must hold: the state facts and whether
// its one gate matched.
type stateExpect struct {
	facts   gate.StateFacts
	matched bool
}

// checkStateRecord checks that the decision record names the state facts that
// the gate read, and that its one gate matched as wantMatched says.
func checkStateRecord(t *testing.T, rec gate.Record, want stateExpect) {
	t.Helper()

	got := rec.Units[0].Facts.State
	if got.Available != want.facts.Available || got.Resources != want.facts.Resources ||
		!slices.Equal(got.Removed, want.facts.Removed) {
		t.Fatalf("record facts state = %+v, want %+v", got, want.facts)
	}

	if len(rec.Units[0].Gates) != 1 || rec.Units[0].Gates[0].Matched != want.matched {
		t.Fatalf("record gates = %+v, want one gate with matched %v", rec.Units[0].Gates, want.matched)
	}
}

// TestStateFactsDifferByTool proves that the state facts of an OpenTofu
// workspace and of a Pulumi stack differ: only a rollback that holds both
// states gives a state change, and the unit facts name each tool.
func TestStateFactsDifferByTool(t *testing.T) {
	t.Parallel()

	units := swUnits{
		collections: map[string]collection.Name{"opentofu:acme/web": "prod", "pulumi:acme/web/prod": "prod"},
		labels:      nil,
	}

	pulumi, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		write     gate.StateWrite
		wantTool  string
		wantAvail bool
	}{
		{"an OpenTofu rollback fills the state facts", rollbackWrite(t, okChange()), "opentofu", true},
		{"a Pulumi write has no state change and fills nothing", gate.StateWrite{
			Principal: principal("alice", false), Unit: pulumi, Operation: gate.OperationRollback,
			Action: identity.ActionUnitWrite, Source: gate.SourceCLI, PlanLock: false, State: nil,
		}, "pulumi", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSWEnv(t, units, nil)

			if _, err := e.svc.GuardStateWrite(t.Context(), tc.write); err != nil {
				t.Fatalf("GuardStateWrite() = %v", err)
			}

			f := e.newestRecord(t).Units[0].Facts
			if f.Unit.Tool != tc.wantTool || f.State.Available != tc.wantAvail {
				t.Fatalf("facts unit.tool %q state.available %v, want %q and %v",
					f.Unit.Tool, f.State.Available, tc.wantTool, tc.wantAvail)
			}
		})
	}
}

// TestStateChangeHostileInputIsRefused sends a state change that is not valid
// straight to the guard, as if the adapter had not checked it. Each one is
// refused, and no decision record names an invented fact.
func TestStateChangeHostileInputIsRefused(t *testing.T) {
	t.Parallel()

	many := make([]string, gate.MaxRemovedAddresses+1)
	for i := range many {
		many[i] = "r.a" + strconv.Itoa(i)
	}

	big := make([]string, 0, 200)
	for range 200 {
		big = append(big, strings.Repeat("x", 900))
	}

	tests := []struct {
		name  string
		op    gate.OperationKind
		state *gate.StateChange
		want  error
	}{
		{"a lock never holds a state change", gate.OperationLock, okChange(), gate.ErrInvalid},
		{"an unlock never holds a state change", gate.OperationUnlock, okChange(), gate.ErrInvalid},
		{
			"no current version", gate.OperationRollback,
			&gate.StateChange{Current: "", Resources: 1, Removed: nil}, gate.ErrInvalid,
		},
		{
			"a current version with a newline", gate.OperationRollback,
			&gate.StateChange{Current: "sv-1\nx", Resources: 1, Removed: nil}, gate.ErrInvalid,
		},
		{
			"a negative resource count", gate.OperationRollback,
			&gate.StateChange{Current: "sv-1", Resources: -1, Removed: nil}, gate.ErrInvalid,
		},
		{
			"an empty address", gate.OperationRollback,
			&gate.StateChange{Current: "sv-1", Resources: 1, Removed: []string{""}}, gate.ErrInvalid,
		},
		{
			"an address with CR and LF", gate.OperationRollback,
			&gate.StateChange{Current: "sv-1", Resources: 1, Removed: []string{"a.b\r\nforged"}}, gate.ErrInvalid,
		},
		{
			"an address that is not UTF-8", gate.OperationRollback,
			&gate.StateChange{Current: "sv-1", Resources: 1, Removed: []string{"a\xff.b"}}, gate.ErrInvalid,
		},
		{
			"too many addresses", gate.OperationRollback,
			&gate.StateChange{Current: "sv-1", Resources: 1, Removed: many}, gate.ErrLimit,
		},
		{
			"an address above its limit", gate.OperationRollback,
			&gate.StateChange{
				Current: "sv-1", Resources: 1, Removed: []string{strings.Repeat("a", gate.MaxAddressBytes+1)},
			}, gate.ErrLimit,
		},
		{
			"addresses above the total limit", gate.OperationRollback,
			&gate.StateChange{Current: "sv-1", Resources: 1, Removed: big}, gate.ErrLimit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSWEnv(t, swUnitsOfTests(), nil)
			e.putGate(t, newSWGate(atUnit, "watch", "", gate.OutcomeWarn, "x"))

			w := swWrite(t, "alice", "web", tc.op)
			w.State = tc.state

			_, err := e.svc.GuardStateWrite(t.Context(), w)

			refusal, ok := errors.AsType[*gate.RefusalError](err)
			if !ok || !errors.Is(err, tc.want) {
				t.Fatalf("GuardStateWrite() = %v, want a refusal that wraps %v", err, tc.want)
			}

			if code, known := gate.Code(refusal); !known || code == "" {
				t.Fatalf("refusal %v has no catalog code", refusal)
			}

			if recs := e.records(t); len(recs) != 0 {
				t.Fatalf("records = %d, want none: a refused input writes no fact", len(recs))
			}
		})
	}
}

// TestStateChangeNeedsExactlyOneUnit proves the evaluation, called without the
// guard, refuses a state change for two units: the change belongs to one unit.
func TestStateChangeNeedsExactlyOneUnit(t *testing.T) {
	t.Parallel()

	e := newSWEnv(t, swUnitsOfTests(), nil)
	w := rollbackWrite(t, okChange())

	subject, err := swSubjects{}.Subject(t.Context(), w.Principal)
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.svc.Evaluate(t.Context(), gate.Change{
		Point: gate.PointStateWrite, Namespace: "acme", Ref: gate.ChangeRef{Kind: gate.ChangeStateWrite, ID: ""},
		Subject: subject, Source: gate.SourceCLI, Author: "alice", Operation: gate.OperationRollback,
		Units: []identity.Object{w.Unit, swUnit(t, "db")}, Holders: nil, PlanLock: false, BreakGlass: "",
		Fence: e.fence, State: w.State,
	})
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Evaluate() = %v, want ErrInvalid", err)
	}
}
