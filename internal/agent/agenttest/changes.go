package agenttest

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/identity"
)

// ChangesHarness is one fresh job model for one case of the changes suite.
// The job model has no policy installed and the default agent settings: no
// direct changes.
type ChangesHarness struct {
	Changes agent.Changes
	// Actor is an agent that acts for a person who holds every action of a
	// start, job:read and job:cancel on both units.
	Actor agent.Actor
	// Outsider is an agent that acts for a person who holds no action on
	// either unit.
	Outsider agent.Actor
	// Service is an agent that acts for a service principal (one that
	// cannot sign in to the console) that holds the actions of Actor.
	Service agent.Actor
	// Unit has a code location on a connected forge and a runner pool.
	Unit identity.Object
	// RunnerOnly has a runner pool and no code location.
	RunnerOnly identity.Object
	// Freeze starts a change freeze that covers both units.
	Freeze func(t *testing.T)
	// Confirm passes the confirmation gate of the change id as the person
	// subject, as the console does: a request of that person with a fresh
	// MFA step. It returns the error of the job model.
	Confirm func(t *testing.T, id agent.ChangeID, subject string) error
}

// ChangesFactory returns a fresh harness for one case.
type ChangesFactory func(t *testing.T) ChangesHarness

// changesCase is one case of the changes suite.
type changesCase struct {
	name string
	run  func(t *testing.T, h *ChangesHarness)
}

// RunChanges runs the changes suite against the job model that f makes.
func RunChanges(t *testing.T, f ChangesFactory) {
	t.Helper()

	for _, c := range changesCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			h := f(t)
			c.run(t, &h)
		})
	}
}

// ChangeRequestFor returns a request of h.Actor for op on unit in mode,
// with key. The request of an apply on h.Unit carries one file edit.
func ChangeRequestFor(
	h *ChangesHarness,
	key string,
	unit identity.Object,
	op agent.Operation,
	mode agent.ChangeMode,
) agent.ChangeRequest {
	var edits []agent.FileEdit
	if op == agent.OperationApply && unit.String() == h.Unit.String() {
		edits = []agent.FileEdit{
			{Path: "Pulumi.prod.yaml", Keys: map[string]json.RawMessage{"config.web:replicas": json.RawMessage("3")}},
		}
	}

	return agent.ChangeRequest{
		Key:       key,
		Actor:     h.Actor,
		Unit:      unit,
		Operation: op,
		Executor:  "",
		Mode:      mode,
		Edits:     edits,
		Reason:    "The service needs a third replica.",
	}
}

func changesCases() []changesCase {
	return []changesCase{
		{name: "support of each unit", run: caseSupport},
		{name: "start actions come from the job model", run: caseStartActions},
		{name: "default is a review request at the confirmation gate", run: caseStartReviewRequest},
		{name: "approve-first confirms the apply phase", run: caseStartApproveFirst},
		{name: "only the person who asked passes the confirmation gate", run: caseConfirm},
		{name: "start twice with one key", run: caseStartTwice},
		{name: "one key with another request", run: caseKeyConflict},
		{name: "review request without a code location", run: caseModeNotSupported},
		{name: "direct is off by default", run: caseDirectOff},
		{name: "change freeze", run: caseFrozen},
		{name: "person without the action", run: caseDenied},
		{name: "service principal cannot start a change", run: caseServicePrincipal},
		{name: "request that is not valid", run: caseInvalid},
		{name: "get", run: caseGet},
		{name: "cancel", run: caseCancel},
	}
}

func caseSupport(t *testing.T, h *ChangesHarness) {
	t.Helper()

	for unit, want := range map[identity.Object]agent.UnitSupport{
		h.Unit:       {WriteBack: true, Runner: true},
		h.RunnerOnly: {WriteBack: false, Runner: true},
	} {
		got, err := h.Changes.Support(t.Context(), h.Actor, unit)
		if err != nil || got != want {
			t.Fatalf("Support(%s) = %+v, %v, want %+v", unit, got, err, want)
		}
	}

	if _, err := h.Changes.Support(t.Context(), h.Outsider, h.Unit); !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Support() for the outsider = %v, want ErrDenied", err)
	}
}

func caseStartActions(t *testing.T, h *ChangesHarness) {
	t.Helper()

	tests := []struct {
		exec agent.Executor
		op   agent.Operation
		want identity.Action
	}{
		{agent.ExecutorWriteBack, agent.OperationApply, agent.ActionUnitWriteBack},
		{agent.ExecutorRunner, agent.OperationPlan, agent.ActionUnitRunPlan},
		{agent.ExecutorRunner, agent.OperationApply, agent.ActionUnitRunApply},
		{agent.ExecutorRunner, agent.OperationRefresh, agent.ActionUnitRunApply},
		{agent.ExecutorRunner, agent.OperationDestroy, agent.ActionUnitRunDestroy},
	}

	for _, tt := range tests {
		req := ChangeRequestFor(h, "k1", h.Unit, tt.op, "")
		req.Executor = tt.exec

		plan := agent.ChangePlan{Mode: agent.ModeApproveFirst, Executor: tt.exec, Delivery: "", Confirm: nil}

		got, err := h.Changes.StartActions(req, plan)
		if err != nil || !slices.Equal(got, []identity.Action{tt.want}) {
			t.Fatalf("StartActions(%s %s) = %v, %v, want only %q: an agent never asks for break-glass",
				tt.exec, tt.op, got, err, tt.want)
		}
	}
}

func mustStart(t *testing.T, h *ChangesHarness, req agent.ChangeRequest) agent.Change {
	t.Helper()

	c, err := h.Changes.Start(t.Context(), req)
	if err != nil {
		t.Fatalf("Start() = %v", err)
	}

	return c
}

func caseStartReviewRequest(t *testing.T, h *ChangesHarness) {
	t.Helper()

	c := mustStart(t, h, ChangeRequestFor(h, "k1", h.Unit, agent.OperationApply, ""))

	if c.ID == "" || c.Key != "k1" || c.Unit.String() != h.Unit.String() || c.Operation != agent.OperationApply {
		t.Fatalf("Start() = %+v, want an ID and the key, unit and operation of the request", c)
	}

	switch {
	case c.Plan.Mode != agent.ModeReviewRequest || c.Plan.Executor != agent.ExecutorWriteBack ||
		c.Plan.Delivery != agent.DeliveryReviewRequest:
		t.Fatalf("Start() plan = %+v, want a write-back delivered as a review request", c.Plan)
	case c.State != agent.ChangeAwaitingApproval || !c.AwaitingConfirmation || c.ConfirmURL == "":
		t.Fatalf("Start() state %q confirmation %v %q, want the deliver phase at the confirmation gate, "+
			"also with no policy", c.State, c.AwaitingConfirmation, c.ConfirmURL)
	default:
	}

	wantAgent(t, h, c)
}

// wantAgent fails t unless c records the person and the agent of h.Actor.
func wantAgent(t *testing.T, h *ChangesHarness, c agent.Change) {
	t.Helper()

	if c.StartedBy != h.Actor.Principal.Subject || c.Agent != h.Actor.Agent {
		t.Fatalf("change started by %q agent %+v, want %q %+v", c.StartedBy, c.Agent,
			h.Actor.Principal.Subject, h.Actor.Agent)
	}
}

func caseStartApproveFirst(t *testing.T, h *ChangesHarness) {
	t.Helper()

	c := mustStart(t, h, ChangeRequestFor(h, "k1", h.RunnerOnly, agent.OperationApply, ""))

	if c.Plan.Mode != agent.ModeApproveFirst || c.Plan.Executor != agent.ExecutorRunner ||
		!slices.Equal(c.Plan.Confirm, []agent.PhaseKind{agent.PhaseApply}) || c.State.Terminal() {
		t.Fatalf("Start() = %+v, want an active approve-first runner job that confirms its apply phase", c)
	}
}

func caseConfirm(t *testing.T, h *ChangesHarness) {
	t.Helper()

	c := mustStart(t, h, ChangeRequestFor(h, "k1", h.Unit, agent.OperationApply, ""))

	if err := h.Confirm(t, c.ID, h.Outsider.Principal.Subject); !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Confirm() by another person = %v, want ErrDenied", err)
	}

	if err := h.Confirm(t, c.ID, h.Actor.Principal.Subject); err != nil {
		t.Fatalf("Confirm() by the person who asked = %v", err)
	}

	got, err := h.Changes.Get(t.Context(), h.Actor, c.ID)
	if err != nil || got.AwaitingConfirmation {
		t.Fatalf("Get() after the confirmation = %+v, %v, want the gate passed", got, err)
	}
}

func caseStartTwice(t *testing.T, h *ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.Unit, agent.OperationApply, agent.ModeReviewRequest)
	first := mustStart(t, h, req)

	second, err := h.Changes.Start(t.Context(), req)
	if err != nil || second.ID != first.ID {
		t.Fatalf("second Start() = %+v, %v, want change %q", second, err, first.ID)
	}
}

func caseKeyConflict(t *testing.T, h *ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.RunnerOnly, agent.OperationApply, agent.ModeApproveFirst)
	mustStart(t, h, req)

	req.Operation = agent.OperationDestroy
	c, err := h.Changes.Start(t.Context(), req)
	wantChangeErr(t, "Start() with another operation", c, err, agent.ErrChangeKeyConflict)
}

func caseModeNotSupported(t *testing.T, h *ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.RunnerOnly, agent.OperationApply, agent.ModeReviewRequest)
	c, err := h.Changes.Start(t.Context(), req)
	wantChangeErr(t, "Start()", c, err, agent.ErrModeNotSupported)
}

func caseDirectOff(t *testing.T, h *ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.RunnerOnly, agent.OperationApply, agent.ModeDirect)
	c, err := h.Changes.Start(t.Context(), req)
	wantChangeErr(t, "Start() in direct mode", c, err, agent.ErrDirectNotAllowed)
}

func caseFrozen(t *testing.T, h *ChangesHarness) {
	t.Helper()

	h.Freeze(t)

	for _, mode := range []agent.ChangeMode{agent.ModeReviewRequest, agent.ModeApproveFirst} {
		req := ChangeRequestFor(h, "k-"+string(mode), h.Unit, agent.OperationApply, mode)
		c, err := h.Changes.Start(t.Context(), req)
		wantChangeErr(t, "Start() in "+string(mode), c, err, agent.ErrChangeFrozen)
	}
}

func caseDenied(t *testing.T, h *ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.RunnerOnly, agent.OperationApply, agent.ModeApproveFirst)
	req.Actor = h.Outsider
	c, err := h.Changes.Start(t.Context(), req)
	wantChangeErr(t, "Start() for the outsider", c, err, identity.ErrDenied)
}

func caseServicePrincipal(t *testing.T, h *ChangesHarness) {
	t.Helper()

	req := ChangeRequestFor(h, "k1", h.RunnerOnly, agent.OperationApply, agent.ModeApproveFirst)
	req.Actor = h.Service
	c, err := h.Changes.Start(t.Context(), req)
	wantChangeErr(t, "Start() for a service principal", c, err, agent.ErrServicePrincipal)
}

func caseInvalid(t *testing.T, h *ChangesHarness) {
	t.Helper()

	noKey := ChangeRequestFor(h, "", h.RunnerOnly, agent.OperationApply, agent.ModeApproveFirst)
	badOp := ChangeRequestFor(h, "k1", h.RunnerOnly, "import", agent.ModeApproveFirst)
	noReason := ChangeRequestFor(h, "k2", h.RunnerOnly, agent.OperationApply, agent.ModeApproveFirst)
	noReason.Reason = ""
	badAgent := ChangeRequestFor(h, "k3", h.RunnerOnly, agent.OperationApply, agent.ModeApproveFirst)
	badAgent.Actor.Agent.Channel = "console"

	reqs := map[string]*agent.ChangeRequest{
		"no key": &noKey, "unknown operation": &badOp, "no reason": &noReason, "no agent": &badAgent,
	}
	for name, req := range reqs {
		c, err := h.Changes.Start(t.Context(), *req)
		wantChangeErr(t, "Start() with "+name, c, err, agent.ErrInvalid)
	}
}

func caseGet(t *testing.T, h *ChangesHarness) {
	t.Helper()

	c := mustStart(t, h, ChangeRequestFor(h, "k1", h.RunnerOnly, agent.OperationApply, agent.ModeApproveFirst))

	got, err := h.Changes.Get(t.Context(), h.Actor, c.ID)
	if err != nil || got.ID != c.ID || got.Agent != h.Actor.Agent {
		t.Fatalf("Get() = %+v, %v, want the change %q with its agent", got, err, c.ID)
	}

	got, err = h.Changes.Get(t.Context(), h.Outsider, c.ID)
	wantChangeErr(t, "Get() by the outsider", got, err, agent.ErrChangeNotFound)
	got, err = h.Changes.Get(t.Context(), h.Actor, "no-such-change")
	wantChangeErr(t, "Get() of an unknown ID", got, err, agent.ErrChangeNotFound)
}

func caseCancel(t *testing.T, h *ChangesHarness) {
	t.Helper()

	c := mustStart(t, h, ChangeRequestFor(h, "k1", h.Unit, agent.OperationApply, ""))

	got, err := h.Changes.Cancel(t.Context(), h.Outsider, c.ID, "stop")
	wantChangeErr(t, "Cancel() by the outsider", got, err, agent.ErrChangeNotFound)

	canceled, err := h.Changes.Cancel(t.Context(), h.Actor, c.ID, "stop")
	if err != nil || canceled.State != agent.ChangeCanceled {
		t.Fatalf("Cancel() = %+v, %v, want a canceled change", canceled, err)
	}

	got, err = h.Changes.Cancel(t.Context(), h.Actor, c.ID, "stop")
	wantChangeErr(t, "second Cancel()", got, err, agent.ErrChangeFinished)
	got, err = h.Changes.Cancel(t.Context(), h.Actor, "no-such-change", "stop")
	wantChangeErr(t, "Cancel() of an unknown ID", got, err, agent.ErrChangeNotFound)
}

// wantChangeErr fails t unless err wraps want. c is the change that the call
// answered, for the message.
func wantChangeErr(t *testing.T, what string, c agent.Change, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s = %+v, %v, want %v", what, c, err, want)
	}
}
