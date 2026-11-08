package agent_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
)

func TestChangePolicyPlan(t *testing.T) {
	t.Parallel()

	allDirect := agent.ChangePolicy{ServerDirect: true, NamespaceDirect: true, UserDirect: true}
	none := agent.ChangePolicy{ServerDirect: false, NamespaceDirect: false, UserDirect: false}
	both := agent.UnitSupport{WriteBack: true, Runner: true}
	runnerOnly := agent.UnitSupport{WriteBack: false, Runner: true}
	writeBackOnly := agent.UnitSupport{WriteBack: true, Runner: false}

	deliver := []agent.PhaseKind{agent.PhaseDeliver}
	apply := []agent.PhaseKind{agent.PhaseApply}

	reviewRequest := agent.ChangePlan{
		Mode: agent.ModeReviewRequest, Executor: agent.ExecutorWriteBack,
		Delivery: agent.DeliveryReviewRequest, Confirm: deliver,
	}
	approveRunner := agent.ChangePlan{
		Mode: agent.ModeApproveFirst, Executor: agent.ExecutorRunner, Delivery: "", Confirm: apply,
	}

	tests := []struct {
		name    string
		policy  agent.ChangePolicy
		support agent.UnitSupport
		req     agent.ChangeRequest
		want    agent.ChangePlan
		wantErr error
	}{
		{"default apply with a code location", allDirect, both, req(agent.OperationApply, "", ""), reviewRequest, nil},
		{
			"default apply without a code location",
			allDirect,
			runnerOnly,
			req(agent.OperationApply, "", ""),
			approveRunner,
			nil,
		},
		{
			"default destroy takes a runner",
			none,
			both,
			req(agent.OperationDestroy, "", ""),
			agent.ChangePlan{
				Mode:     agent.ModeApproveFirst,
				Executor: agent.ExecutorRunner,
				Delivery: "",
				Confirm:  apply,
			},
			nil,
		},
		{
			"a plan needs no confirmation",
			none, runnerOnly, req(agent.OperationPlan, "", ""),
			agent.ChangePlan{Mode: agent.ModeApproveFirst, Executor: agent.ExecutorRunner, Delivery: "", Confirm: nil},
			nil,
		},
		{
			"review request is never a direct push",
			allDirect, both, req(agent.OperationApply, agent.ExecutorWriteBack, agent.ModeReviewRequest),
			reviewRequest, nil,
		},
		{
			"approve-first write-back still delivers a review request",
			none, both, req(agent.OperationApply, agent.ExecutorWriteBack, agent.ModeApproveFirst),
			agent.ChangePlan{
				Mode: agent.ModeApproveFirst, Executor: agent.ExecutorWriteBack,
				Delivery: agent.DeliveryReviewRequest, Confirm: deliver,
			},
			nil,
		},
		{
			"direct write-back pushes after the confirmation of its delivery",
			allDirect, both, req(agent.OperationApply, agent.ExecutorWriteBack, agent.ModeDirect),
			agent.ChangePlan{
				Mode: agent.ModeDirect, Executor: agent.ExecutorWriteBack,
				Delivery: agent.DeliveryDirectPush, Confirm: deliver,
			},
			nil,
		},
		{
			"direct runner apply has no confirmation",
			allDirect, runnerOnly, req(agent.OperationApply, "", agent.ModeDirect),
			agent.ChangePlan{Mode: agent.ModeDirect, Executor: agent.ExecutorRunner, Delivery: "", Confirm: nil},
			nil,
		},
		{
			"direct destroy",
			allDirect,
			runnerOnly,
			req(agent.OperationDestroy, "", agent.ModeDirect),
			zeroPlan(),
			agent.ErrDirectNotAllowed,
		},
		{
			"direct without the server setting",
			agent.ChangePolicy{ServerDirect: false, NamespaceDirect: true, UserDirect: true},
			runnerOnly, req(agent.OperationApply, "", agent.ModeDirect), zeroPlan(), agent.ErrDirectNotAllowed,
		},
		{
			"direct without the namespace opt-in",
			agent.ChangePolicy{ServerDirect: true, NamespaceDirect: false, UserDirect: true},
			runnerOnly, req(agent.OperationApply, "", agent.ModeDirect), zeroPlan(), agent.ErrDirectNotAllowed,
		},
		{
			"direct without the person opt-in",
			agent.ChangePolicy{ServerDirect: true, NamespaceDirect: true, UserDirect: false},
			runnerOnly, req(agent.OperationApply, "", agent.ModeDirect), zeroPlan(), agent.ErrDirectNotAllowed,
		},
		{
			"review request on a runner",
			none, both, req(agent.OperationApply, agent.ExecutorRunner, agent.ModeReviewRequest),
			zeroPlan(), agent.ErrModeNotSupported,
		},
		{
			"write-back without a code location",
			none, runnerOnly, req(agent.OperationApply, agent.ExecutorWriteBack, ""),
			zeroPlan(), agent.ErrModeNotSupported,
		},
		{
			"write-back of a destroy",
			none, both, req(agent.OperationDestroy, agent.ExecutorWriteBack, ""),
			zeroPlan(), agent.ErrModeNotSupported,
		},
		{
			"runner without a pool",
			none,
			writeBackOnly,
			req(agent.OperationPlan, "", ""),
			zeroPlan(),
			agent.ErrModeNotSupported,
		},
		{"unknown operation", none, both, req("import", "", ""), zeroPlan(), agent.ErrInvalid},
		{"unknown executor", none, both, req(agent.OperationApply, "shell", ""), zeroPlan(), agent.ErrInvalid},
		{"unknown mode", allDirect, both, req(agent.OperationApply, "", "apply-now"), zeroPlan(), agent.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.policy.Plan(tt.support, &tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Plan() error = %v, want %v", err, tt.wantErr)
			}

			if got.Mode != tt.want.Mode || got.Executor != tt.want.Executor || got.Delivery != tt.want.Delivery ||
				!slices.Equal(got.Confirm, tt.want.Confirm) {
				t.Fatalf("Plan() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestChangeStates(t *testing.T) {
	t.Parallel()

	// The states are the eleven states of a job, five of them terminal.
	const wantStates, wantTerminal = 11, 5

	terminal := 0

	for _, s := range agent.ChangeStates() {
		if s.Terminal() {
			terminal++
		}
	}

	if n := len(agent.ChangeStates()); n != wantStates || terminal != wantTerminal {
		t.Fatalf("ChangeStates() has %d states, %d terminal, want %d, %d", n, terminal, wantStates, wantTerminal)
	}

	if agent.ChangeState("waiting").Terminal() {
		t.Fatal("an unknown state is terminal")
	}
}

func req(op agent.Operation, exec agent.Executor, mode agent.ChangeMode) agent.ChangeRequest {
	var r agent.ChangeRequest

	r.Operation, r.Executor, r.Mode = op, exec, mode

	return r
}

func zeroPlan() agent.ChangePlan {
	var p agent.ChangePlan

	return p
}
