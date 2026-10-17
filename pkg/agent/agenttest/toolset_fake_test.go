package agenttest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/agent/agenttest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// fakeSecretValue is the secret value of the unit of the fake. It is no
// credential of any system.
const fakeSecretValue = "agenttest-fake-value-17"

func TestToolsetSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunToolset(t, func(t *testing.T) agenttest.ToolsetHarness {
		t.Helper()

		f := &fakeToolset{
			mu: sync.Mutex{}, unit: mustUnit(t, "web"), secret: fakeSecretValue,
			agentUse: map[string]bool{"ana": true}, records: nil, failAudit: false,
			gate: agent.SecretAccess{
				Hosted: false, StartFlag: false, WindowAccepted: false, AccountOptIn: false,
				NamespaceOptIn: false, UserOptIn: false, Channel: agent.ChannelAgent, UseConsent: false,
			},
			revealed: map[agent.Ref]bool{},
		}

		return agenttest.ToolsetHarness{
			Toolset:      f,
			Unit:         f.unit,
			Secret:       f.secret,
			Reader:       actor("ana"),
			NoAgentUse:   actor("ben"),
			Records:      f.recorded,
			FailAudit:    func(*testing.T) { f.set(func() { f.failAudit = true }) },
			Gate:         func(_ *testing.T, s agent.SecretAccess) { f.set(func() { f.gate = s }) },
			MarkRevealed: func(_ *testing.T, a agent.Actor) { f.set(func() { f.revealed[a.Agent] = true }) },
		}
	})
}

// fakeToolset is a Toolset in memory on one unit. Subjects in agentUse hold
// agent:use, and every subject holds the actions of the tools. It answers
// small fixed results. It is not an implementation: it keeps the order of
// the checks and the audit rules of the port.
type fakeToolset struct {
	mu        sync.Mutex
	unit      identity.Object
	secret    string
	agentUse  map[string]bool
	records   []agent.ToolCallRecord
	failAudit bool
	gate      agent.SecretAccess
	revealed  map[agent.Ref]bool
}

func (*fakeToolset) Specs() []agent.ToolSpec { return agent.Catalog() }

func (f *fakeToolset) Call(_ context.Context, a agent.Actor, _ string, c agent.ToolCall) (agent.ToolOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	digest, err := agent.ArgsDigest(c.Arguments)
	if err != nil {
		digest = ""
	}

	rec := agent.ToolCallRecord{
		Actor: a, Tool: c.Name, Actions: nil, Namespace: audit.ServerScope, Object: "", ArgsDigest: digest,
		Outcome: audit.OutcomeFailure, Status: http.StatusBadRequest, ClosedStage: "", RevealCount: 0, At: time.Now(),
	}

	out, err := f.run(a, c, &rec)

	if f.failAudit {
		return noOutput(), fmt.Errorf("%w: the audit store is down", agent.ErrAuditFailed)
	}

	f.records = append(f.records, rec)

	return out, err
}

// run runs the call and fills rec with its outcome.
func (f *fakeToolset) run(a agent.Actor, c agent.ToolCall, rec *agent.ToolCallRecord) (agent.ToolOutput, error) {
	spec, ok := agent.SpecFor(c.Name)
	if !ok {
		return noOutput(), agent.ErrUnknownTool
	}

	args, err := agent.ValidateArguments(spec, c.Arguments)
	if err != nil {
		return noOutput(), fmt.Errorf("call %s: %w", c.Name, err)
	}

	rec.Namespace, rec.Object = f.unit.Namespace(), args.String("unit")

	if !f.agentUse[a.Principal.Subject] {
		rec.Outcome, rec.Status = audit.OutcomeDenied, http.StatusForbidden

		return noOutput(), fmt.Errorf("agent:use: %w", identity.ErrDenied)
	}

	if spec.Effect == agent.EffectPropose && f.revealed[a.Agent] {
		rec.Outcome, rec.Status = audit.OutcomeDenied, http.StatusForbidden

		return noOutput(), agent.ErrProposeAfterReveal
	}

	rec.Actions = []identity.Action{spec.Action}
	rec.Outcome, rec.Status = audit.OutcomeSuccess, http.StatusOK

	if c.Name != agent.ToolReadState || !args.Bool("secretValues") {
		return content(map[string]string{"ref": rec.Object}), nil
	}

	rec.Actions = append(rec.Actions, identity.ActionUnitReadSecrets)

	gate := f.gate
	gate.Channel = a.Agent.Channel

	if err := gate.Check(); err != nil {
		rec.Outcome, rec.ClosedStage = audit.OutcomeDenied, agent.SecretStage(err)
		out := content(map[string]string{"ref": rec.Object, "password": "[masked]", "closedStage": rec.ClosedStage})
		out.ClosedStage = rec.ClosedStage

		return out, nil
	}

	rec.RevealCount = 1
	out := content(map[string]string{"ref": rec.Object, "password": f.secret})
	out.Revealed = []string{f.secret}

	return out, nil
}

func (f *fakeToolset) recorded() []agent.ToolCallRecord {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.records)
}

func (f *fakeToolset) set(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	change()
}

func content(v map[string]string) agent.ToolOutput {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("marshal a fixed result: %w", err))
	}

	return agent.ToolOutput{Content: raw, Revealed: nil, ClosedStage: ""}
}

func noOutput() agent.ToolOutput {
	return agent.ToolOutput{Content: nil, Revealed: nil, ClosedStage: ""}
}
