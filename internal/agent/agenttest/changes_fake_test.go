package agenttest_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/agent/agenttest"
	"opendustpan.org/opendustpan/pkg/identity"
)

func TestChangesSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunChanges(t, func(t *testing.T) agenttest.ChangesHarness {
		t.Helper()

		unit := mustUnit(t, "web")
		runnerOnly := mustUnit(t, "db")
		f := &fakeChanges{
			mu: sync.Mutex{}, byKey: map[string]agent.Change{}, sigs: map[string]string{}, frozen: false,
			writeBack: unit.String(),
		}

		return agenttest.ChangesHarness{
			Changes:    f,
			Actor:      actor("ana"),
			Outsider:   actor("eve"),
			Service:    serviceActor("ana"),
			Unit:       unit,
			RunnerOnly: runnerOnly,
			Freeze:     func(*testing.T) { f.freeze() },
			Confirm:    func(_ *testing.T, id agent.ChangeID, subject string) error { return f.confirm(id, subject) },
		}
	})
}

func mustUnit(t *testing.T, name string) identity.Object {
	t.Helper()

	unit, err := identity.UnitObject("pulumi", "acme", name)
	if err != nil {
		t.Fatalf("UnitObject() = %v", err)
	}

	return unit
}

// fakeChanges is a job model in memory, with no policy and no direct
// changes. Only "ana" holds the actions. The unit writeBack has a code
// location. Every unit has a runner pool.
type fakeChanges struct {
	mu        sync.Mutex
	byKey     map[string]agent.Change
	sigs      map[string]string
	frozen    bool
	writeBack string
}

func (f *fakeChanges) Support(_ context.Context, a agent.Actor, unit identity.Object) (agent.UnitSupport, error) {
	if a.Principal.Subject != "ana" {
		return agent.UnitSupport{WriteBack: false, Runner: false}, fmt.Errorf("support: %w", identity.ErrDenied)
	}

	return agent.UnitSupport{WriteBack: unit.String() == f.writeBack, Runner: true}, nil
}

func (*fakeChanges) StartActions(req agent.ChangeRequest, plan agent.ChangePlan) ([]identity.Action, error) {
	switch {
	case plan.Executor == agent.ExecutorWriteBack && req.Operation == agent.OperationApply:
		return []identity.Action{agent.ActionUnitWriteBack}, nil
	case plan.Executor != agent.ExecutorRunner:
		return nil, agent.ErrModeNotSupported
	}

	action, ok := map[agent.Operation]identity.Action{
		agent.OperationPlan:    agent.ActionUnitRunPlan,
		agent.OperationApply:   agent.ActionUnitRunApply,
		agent.OperationRefresh: agent.ActionUnitRunApply,
		agent.OperationDestroy: agent.ActionUnitRunDestroy,
	}[req.Operation]
	if !ok {
		return nil, agent.ErrInvalid
	}

	return []identity.Action{action}, nil
}

func (f *fakeChanges) Start(ctx context.Context, req agent.ChangeRequest) (agent.Change, error) {
	var zero agent.Change

	if req.Key == "" || req.Reason == "" || req.Actor.Agent.Validate() != nil {
		return zero, agent.ErrInvalid
	}

	if req.Actor.ServicePrincipal {
		return zero, agent.ErrServicePrincipal
	}

	support, err := f.Support(ctx, req.Actor, req.Unit)
	if err != nil {
		return zero, err
	}

	policy := agent.ChangePolicy{ServerDirect: false, NamespaceDirect: false, UserDirect: false}

	plan, err := policy.Plan(support, req)
	if err != nil {
		return zero, fmt.Errorf("start: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	sig := fmt.Sprintf("%s|%s|%s|%s|%v", req.Unit, req.Operation, req.Executor, req.Mode, req.Edits)

	switch {
	case f.frozen:
		return zero, agent.ErrChangeFrozen
	case f.sigs[req.Key] != "" && f.sigs[req.Key] != sig:
		return zero, agent.ErrChangeKeyConflict
	default:
	}

	if c, ok := f.byKey[req.Key]; ok {
		return c, nil
	}

	c := newChange(req, plan)
	f.byKey[req.Key], f.sigs[req.Key] = c, sig

	return c, nil
}

// newChange returns the job of req: at the confirmation gate when its first
// phase that changes needs it, and queued for its plan phase otherwise.
func newChange(req agent.ChangeRequest, plan agent.ChangePlan) agent.Change {
	state, waiting := agent.ChangeQueued, false
	if plan.Executor == agent.ExecutorWriteBack && slices.Contains(plan.Confirm, agent.PhaseDeliver) {
		state, waiting = agent.ChangeAwaitingApproval, true
	}

	return agent.Change{
		ID: agent.ChangeID("job-" + req.Key), Key: req.Key, Unit: req.Unit, Operation: req.Operation, Plan: plan,
		State: state, AwaitingConfirmation: waiting, ConfirmURL: "https://console.example/confirm/" + req.Key,
		ReviewURL: "", StartedBy: req.Actor.Principal.Subject, Agent: req.Actor.Agent, CreatedAt: time.Now(),
	}
}

func (f *fakeChanges) Get(_ context.Context, a agent.Actor, id agent.ChangeID) (agent.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.find(a.Principal.Subject, id)
}

func (f *fakeChanges) Cancel(_ context.Context, a agent.Actor, id agent.ChangeID, _ string) (agent.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.find(a.Principal.Subject, id)
	if err != nil {
		return c, err
	}

	if c.State.Terminal() {
		return c, agent.ErrChangeFinished
	}

	c.State, c.AwaitingConfirmation = agent.ChangeCanceled, false
	f.byKey[c.Key] = c

	return c, nil
}

func (f *fakeChanges) confirm(id agent.ChangeID, subject string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.find("ana", id)
	if err != nil {
		return err
	}

	if subject != c.StartedBy {
		return fmt.Errorf("confirm: %w", identity.ErrDenied)
	}

	c.State, c.AwaitingConfirmation = agent.ChangeDispatched, false
	f.byKey[c.Key] = c

	return nil
}

func (f *fakeChanges) freeze() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.frozen = true
}

func (f *fakeChanges) find(subject string, id agent.ChangeID) (agent.Change, error) {
	for key := range f.byKey {
		if c := f.byKey[key]; c.ID == id && subject == "ana" {
			return c, nil
		}
	}

	var zero agent.Change

	return zero, agent.ErrChangeNotFound
}
