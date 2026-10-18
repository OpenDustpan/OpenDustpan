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

		stores := &fakeGateStores{
			mu: sync.Mutex{}, windows: nil, latest: map[string]string{}, maintenance: false,
			consents: map[string]agent.Consent{}, installation: nil, optIns: map[string]agent.PersonOptIns{},
		}
		f := &fakeToolset{
			mu: sync.Mutex{}, unit: mustUnit(t, "web"), secret: fakeSecretValue,
			agentUse: map[string]bool{"ana": true, "svc": true}, records: nil, failAudit: false,
			gate: agent.SecretAccess{
				Hosted: false, StartFlag: false, WindowAccepted: false, AccountOptIn: false,
				NamespaceOptIn: false, UserOptIn: false, Channel: agent.ChannelAgent, UseConsent: false,
			},
			revealed: map[agent.Ref]bool{}, consents: fakeConsents{stores},
		}

		outside := actor("ana")
		outside.Principal.TokenID = "token-ana-1"
		outside.Agent = agent.Ref{Channel: agent.ChannelMCP, RunID: "", SessionID: "mcp-session-1"}

		return agenttest.ToolsetHarness{
			Toolset:    f,
			Unit:       f.unit,
			Secret:     f.secret,
			Reader:     actor("ana"),
			NoAgentUse: actor("ben"),
			Outside:    outside,
			Service:    serviceActor("svc"),
			ApproveConsent: func(t *testing.T, id string) {
				t.Helper()

				c, err := f.consents.Get(t.Context(), id)
				if err != nil {
					t.Fatalf("Get() = %v", err)
				}

				_, err = f.consents.Approve(t.Context(), id, agenttest.Accept(c.Subject, time.Now()),
					agenttest.TestEntry("acme", c.Subject, string(agent.ActionAgentUse)))
				if err != nil {
					t.Fatalf("Approve() = %v", err)
				}
			},
			Records:      f.recorded,
			FailAudit:    func(*testing.T) { f.set(func() { f.failAudit = true }) },
			Gate:         func(_ *testing.T, s agent.SecretAccess) { f.set(func() { f.gate = s }) },
			MarkRevealed: func(_ *testing.T, a agent.Actor) { f.set(func() { f.revealed[a.Agent] = true }) },
		}
	})
}

// fakeConsoleURL is the console URL of the fake.
const fakeConsoleURL = "https://console.example"

// fakeToolset is a Toolset in memory on one unit. Subjects in agentUse hold
// agent:use, and every subject holds the actions of the tools. It answers
// small fixed results, and keeps its consents in a fake ConsentStore. It is
// not an implementation: it keeps the order of the checks, the consent rules
// and the audit rules of the port.
type fakeToolset struct {
	mu        sync.Mutex
	unit      identity.Object
	secret    string
	agentUse  map[string]bool
	records   []agent.ToolCallRecord
	failAudit bool
	gate      agent.SecretAccess
	revealed  map[agent.Ref]bool
	consents  agent.ConsentStore
}

func (*fakeToolset) Specs() []agent.ToolSpec { return agent.Catalog() }

func (f *fakeToolset) Call(ctx context.Context, a agent.Actor, _ string, c agent.ToolCall) (agent.ToolOutput, error) {
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

	out, err := f.run(ctx, a, c, &rec)

	if f.failAudit {
		return noOutput(), fmt.Errorf("%w: the audit store is down", agent.ErrAuditFailed)
	}

	f.records = append(f.records, rec)

	return out, err
}

// run runs the call and fills rec with its outcome.
func (f *fakeToolset) run(ctx context.Context, a agent.Actor, c agent.ToolCall, rec *agent.ToolCallRecord) (
	agent.ToolOutput, error,
) {
	spec, ok := agent.SpecFor(c.Name)
	if !ok {
		return noOutput(), agent.ErrUnknownTool
	}

	args, err := agent.ValidateArguments(spec, c.Arguments)
	if err != nil {
		return noOutput(), fmt.Errorf("call %s: %w", c.Name, err)
	}

	rec.Namespace, rec.Object = f.unit.Namespace(), args.String("unit")
	secretRead := spec.SecretAction != "" && args.Bool("secretValues")

	if err := f.refuse(a, spec.Effect == agent.EffectPropose || secretRead); err != nil {
		rec.Outcome, rec.Status = audit.OutcomeDenied, http.StatusForbidden

		return noOutput(), err
	}

	if spec.Effect == agent.EffectPropose {
		return f.propose(ctx, a, rec)
	}

	rec.Actions = []identity.Action{spec.Action}
	rec.Outcome, rec.Status = audit.OutcomeSuccess, http.StatusOK

	switch {
	case secretRead:
		rec.Actions = append(rec.Actions, spec.SecretAction)

		return f.readSecrets(ctx, a, args.String(agent.ConsentParam), rec)
	case c.Name == agent.ToolReadState:
		return state(f.result(rec.Object, fakeMasked, "", "")), nil
	default:
		return content(map[string]string{"ref": rec.Object}), nil
	}
}

// refuse returns the error of a call that a refuses before any read: a
// propose or a secret read (gated) by a service principal, and a call
// without agent:use.
func (f *fakeToolset) refuse(a agent.Actor, gated bool) error {
	switch {
	case gated && a.ServicePrincipal:
		return agent.ErrServicePrincipal
	case !f.agentUse[a.Principal.Subject]:
		return fmt.Errorf("agent:use: %w", identity.ErrDenied)
	default:
		return nil
	}
}

// propose refuses a propose after a reveal of the run, the session or the
// token, and otherwise answers a fixed change.
func (f *fakeToolset) propose(ctx context.Context, a agent.Actor, rec *agent.ToolCallRecord) (
	agent.ToolOutput, error,
) {
	revealed, err := f.consents.Revealed(ctx, a.Principal.TokenID, a.Agent.SessionID)
	if err != nil {
		return noOutput(), fmt.Errorf("read the reveal mark: %w", err)
	}

	if f.revealed[a.Agent] || (a.Agent.Channel != agent.ChannelAgent && revealed) {
		rec.Outcome, rec.Status = audit.OutcomeDenied, http.StatusForbidden

		return noOutput(), agent.ErrProposeAfterReveal
	}

	rec.Actions = []identity.Action{agent.ActionUnitWriteBack}
	rec.Outcome, rec.Status = audit.OutcomeSuccess, http.StatusOK

	return content(map[string]string{"ref": rec.Object, "change": "job-1"}), nil
}

// readSecrets checks the gate and, for an outside agent, the consent
// consentID, and answers the plain or the masked values.
func (f *fakeToolset) readSecrets(ctx context.Context, a agent.Actor, consentID string, rec *agent.ToolCallRecord) (
	agent.ToolOutput, error,
) {
	gate := f.gate
	gate.Channel, gate.UseConsent = a.Agent.Channel, false

	pending := ""

	if gate.Channel != agent.ChannelAgent && agent.SecretStage(gate.Check()) == agent.StageConsent {
		var err error

		gate.UseConsent, pending, err = f.useConsent(ctx, a, consentID, rec.Object)
		if err != nil {
			return noOutput(), err
		}
	}

	if err := gate.Check(); err != nil {
		rec.Outcome, rec.ClosedStage = audit.OutcomeDenied, agent.SecretStage(err)
		res := f.result(rec.Object, fakeMasked, rec.ClosedStage, pending)

		return state(res, withConsent(res.ConsentID, res.ConsentURL)), nil
	}

	rec.RevealCount = 1
	out := state(f.result(rec.Object, f.secret, "", ""))
	out.Revealed = []string{f.secret}

	return out, nil
}

// useConsent uses the consent id for the call of a on unit. It answers true
// for a use, and otherwise the consent that the person approves next: the
// same one while it waits for approval, or a new one.
func (f *fakeToolset) useConsent(ctx context.Context, a agent.Actor, id, unit string) (bool, string, error) {
	u := agent.ConsentUse{
		ID: id, Subject: a.Principal.Subject, TokenID: a.Principal.TokenID, SessionID: a.Agent.SessionID,
		Tool: agent.ToolReadState, Unit: unit, At: time.Now(),
	}

	if id != "" {
		c, err := f.consents.Lookup(ctx, u)

		switch {
		case err == nil && f.consents.Use(ctx, u) == nil:
			return true, "", nil
		case err == nil && c.ApprovedAt.IsZero() && u.At.Sub(c.CreatedAt) <= agent.ConsentTTL:
			return false, c.ID, nil
		default:
		}
	}

	c, err := f.consents.Create(ctx, agent.Consent{
		ID: "", Subject: u.Subject, TokenID: u.TokenID, SessionID: u.SessionID, Tool: u.Tool, Unit: u.Unit,
		CreatedAt: u.At, ApprovedAt: time.Time{}, UsedAt: time.Time{},
	})
	if err != nil {
		return false, "", fmt.Errorf("make a consent: %w", err)
	}

	return false, c.ID, nil
}

// fakeMasked is the masked value of the fake.
const fakeMasked = "[masked]"

// result returns the StateResult of the unit with value as the secret
// value: fakeMasked or the plain value.
func (*fakeToolset) result(ref string, value any, stage, consentID string) agent.StateResult {
	plain := value != fakeMasked

	url := ""
	if consentID != "" {
		url = fakeConsoleURL + agent.ConsentPath(consentID)
	}

	return agent.StateResult{
		Ref: ref, Version: "1", CompareTo: "",
		Resources: []agent.Resource{{
			Ref: ref + "/db", Address: "db", Type: "database", Attributes: map[string]any{"password": value},
		}},
		Changes: nil, Truncated: false, SecretValues: plain, ClosedStage: stage, ConsentID: consentID, ConsentURL: url,
	}
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

func content(v any) agent.ToolOutput {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("marshal a fixed result: %w", err))
	}

	return agent.ToolOutput{Content: raw, Revealed: nil, ClosedStage: "", ConsentID: "", ConsentURL: ""}
}

// state returns the output of res, changed by each option.
func state(res agent.StateResult, opts ...func(o *agent.ToolOutput)) agent.ToolOutput {
	out := content(res)
	out.ClosedStage = res.ClosedStage

	for _, opt := range opts {
		opt(&out)
	}

	return out
}

func withConsent(id, url string) func(o *agent.ToolOutput) {
	return func(o *agent.ToolOutput) { o.ConsentID, o.ConsentURL = id, url }
}

func noOutput() agent.ToolOutput {
	return agent.ToolOutput{Content: nil, Revealed: nil, ClosedStage: "", ConsentID: "", ConsentURL: ""}
}
