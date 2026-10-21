package agenttest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/durable"
)

// TurnHarness is one fresh loop for one case of the turn suite: the durable
// kind of a turn as the loop registers it (agent.TurnKind with the handler
// of the loop), on a run store and a store of durable operations on one
// database.
type TurnHarness struct {
	Kind durable.KindSpec
	Runs agent.RunStore
	Ops  durable.Store
	// Start creates a run of a person with a valid credential, whose model
	// answers one call of describe_unit in turn 1 and a final answer in
	// turn 2.
	Start func(t *testing.T) agent.Run
	// Revoke revokes the credential that started run.
	Revoke func(t *testing.T, run agent.Run)
	// ToolCalls returns the number of tool calls that the Toolset ran.
	ToolCalls func() int
}

// TurnFactory returns a fresh harness for one case.
type TurnFactory func(t *testing.T) TurnHarness

// RunTurns runs the turn suite against the loop that f makes. It drives the
// steps of a turn one at a time, as the durable runner does, so a case can
// act between two steps.
func RunTurns(t *testing.T, f TurnFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h TurnHarness)
	}{
		{name: "a turn runs its tool call", run: caseTurnRuns},
		{name: "revoke before the check", run: caseTurnRevokeBeforeCheck},
		{name: "revoke between check and tools, then take over", run: caseTurnRevokeTakeover},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
		})
	}
}

// claimOp claims the one operation of a turn that can run.
func claimOp(t *testing.T, ops durable.Store) durable.Operation {
	t.Helper()

	op, err := ops.Claim(t.Context(), durable.ClaimRequest{
		Owner: "agenttest", Kinds: []durable.Kind{agent.KindTurn}, MaxVersions: nil, Lease: testLease,
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("Claim() = %v", err)
	}

	return op
}

// runStep runs the step name of op and records it, as the durable runner
// does, and returns op with the record.
func runStep(t *testing.T, h TurnHarness, op durable.Operation, name string) durable.Operation {
	t.Helper()

	for _, st := range h.Kind.Steps {
		if st.Name != name {
			continue
		}

		fence := op.Claim.Step(name)

		result, err := st.Run(t.Context(), durable.StepContext{Operation: op, Fence: fence})
		if err != nil {
			t.Fatalf("step %q = %v", name, err)
		}

		if err := h.Ops.RecordStep(t.Context(), fence, result); err != nil && !errors.Is(err, durable.ErrStepDone) {
			t.Fatalf("RecordStep(%q) = %v", name, err)
		}

		op.Steps = append(op.Steps, durable.StepRecord{Name: name, Result: result, DoneAt: op.UpdatedAt})

		return op
	}

	t.Fatalf("the turn kind has no step %q", name)

	return op
}

// wantStop fails t unless run ended with stop.
func wantStop(t *testing.T, h TurnHarness, run agent.Run, stop agent.StopReason) {
	t.Helper()

	got, err := h.Runs.Get(t.Context(), run.ID)
	if err != nil || !got.State.Terminal() || got.Stop != stop {
		t.Fatalf("Get() = %+v, %v, want a run that ended with %q", got, err, stop)
	}
}

func caseTurnRuns(t *testing.T, h TurnHarness) {
	t.Helper()

	run := h.Start(t)
	op := claimOp(t, h.Ops)

	for _, name := range []string{agent.StepCheck, agent.StepModel, agent.StepTools, agent.StepAdvance} {
		op = runStep(t, h, op, name)
	}

	if n := h.ToolCalls(); n != 1 {
		t.Fatalf("the turn ran %d tool calls, want 1", n)
	}

	if got, err := h.Runs.Get(t.Context(), run.ID); err != nil || got.State.Terminal() {
		t.Fatalf("Get() = %+v, %v, want a run that goes on to turn 2", got, err)
	}
}

func caseTurnRevokeBeforeCheck(t *testing.T, h TurnHarness) {
	t.Helper()

	run := h.Start(t)
	op := claimOp(t, h.Ops)

	h.Revoke(t, run)
	runStep(t, h, op, agent.StepCheck)
	wantStop(t, h, run, agent.StopCredentialEnded)
}

func caseTurnRevokeTakeover(t *testing.T, h TurnHarness) {
	t.Helper()

	run := h.Start(t)
	op := claimOp(t, h.Ops)
	op = runStep(t, h, op, agent.StepCheck)
	runStep(t, h, op, agent.StepModel)

	// The person loses the credential after the check, and the instance
	// stops. Another instance takes the turn over with a new claim, and
	// continues at the tools step. It must resolve the person again, and
	// run no tool call.
	h.Revoke(t, run)

	if err := h.Ops.Release(t.Context(), op.Claim); err != nil {
		t.Fatalf("Release() = %v", err)
	}

	next := claimOp(t, h.Ops)
	if next.Claim.Token == op.Claim.Token {
		t.Fatalf("takeover claim token %d, want a new token", next.Claim.Token)
	}

	if _, done := next.Step(agent.StepModel); !done {
		t.Fatal("the takeover lost the record of the model step")
	}

	runStep(t, h, next, agent.StepTools)

	if n := h.ToolCalls(); n != 0 {
		t.Fatalf("the takeover ran %d tool calls with a revoked credential, want 0", n)
	}

	wantStop(t, h, run, agent.StopCredentialEnded)
}
