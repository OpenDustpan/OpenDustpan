package agenttest

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// ToolsetHarness is one fresh Toolset with its ports for one case of the
// toolset suite. The secret gate is closed until a case calls Gate.
type ToolsetHarness struct {
	Toolset agent.Toolset
	// Unit has one resource with the secret value Secret.
	Unit   identity.Object
	Secret string
	// Reader acts for a person who holds identity.ActionUnitRead,
	// identity.ActionUnitReadSecrets and every start action on Unit, and
	// agent:use on its namespace. Its channel is agent.ChannelAgent.
	Reader agent.Actor
	// NoAgentUse acts for a person who holds the same actions on Unit, but
	// not agent:use on its namespace.
	NoAgentUse agent.Actor
	// Outside is an outside agent (agent.ChannelMCP) that acts for the
	// person of Reader, with an access token of that person, in an MCP
	// session.
	Outside agent.Actor
	// Service acts for a service principal (one that cannot sign in to the
	// console) that holds every action of Reader.
	Service agent.Actor
	// ApproveConsent approves the consent id as its person does in the
	// console: a console session with a fresh MFA step.
	ApproveConsent func(t *testing.T, id string)
	// Records returns the tool call records that the Toolset wrote, in
	// order.
	Records func() []agent.ToolCallRecord
	// FailAudit makes every later write of a record fail.
	FailAudit func(t *testing.T)
	// Gate sets the stages of the secret gate for later calls. The Toolset
	// takes the channel from the actor, not from s.
	Gate func(t *testing.T, s agent.SecretAccess)
	// MarkRevealed records that the run of actor read secret values in
	// plain text (Run.ReadPlainSecrets).
	MarkRevealed func(t *testing.T, actor agent.Actor)
}

// ToolsetFactory returns a fresh harness for one case.
type ToolsetFactory func(t *testing.T) ToolsetHarness

// RunToolset runs the toolset suite against the Toolset that f makes.
func RunToolset(t *testing.T, f ToolsetFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h *ToolsetHarness)
	}{
		{name: "specs are the catalog", run: caseToolsetSpecs},
		{name: "read writes a record", run: caseToolsetRead},
		{name: "call without agent:use", run: caseToolsetNoAgentUse},
		{name: "audit failure fails the call", run: caseToolsetAuditFails},
		{name: "unknown tool", run: caseToolsetUnknown},
		{name: "arguments out of bounds", run: caseToolsetArguments},
		{name: "secret gate closed", run: caseToolsetGateClosed},
		{name: "secret gate open", run: caseToolsetGateOpen},
		{name: "outside agent needs a consent", run: caseToolsetConsent},
		{name: "an approved consent reveals once", run: caseToolsetConsentOnce},
		{name: "a consent is bound to its session", run: caseToolsetConsentSession},
		{name: "no propose after a consent reveal, also with the token", run: caseToolsetConsentBlocksPropose},
		{name: "no propose after a reveal", run: caseToolsetProposeAfterReveal},
		{name: "service principal cannot propose or read secrets", run: caseToolsetServicePrincipal},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			h := f(t)
			c.run(t, &h)
		})
	}
}

// OpenGate returns a secret gate of a self-hosted server with every stage
// open.
func OpenGate() agent.SecretAccess {
	return agent.SecretAccess{
		Hosted: false, StartFlag: true, WindowAccepted: true, AccountOptIn: false,
		NamespaceOptIn: true, UserOptIn: true, Channel: agent.ChannelAgent, UseConsent: false,
	}
}

// call runs tool with args, given as Go values, for actor.
func call(t *testing.T, h *ToolsetHarness, actor agent.Actor, tool agent.ToolName, args map[string]any) (
	agent.ToolOutput, error,
) {
	t.Helper()

	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal the arguments: %v", err)
	}

	tc := agent.ToolCall{ID: "call-1", Name: tool, Arguments: raw}

	return h.Toolset.Call(t.Context(), actor, fmt.Sprintf("agenttest/%s/%d", tool, len(h.Records())), tc)
}

// lastRecord returns the last record and fails t unless there is one more
// record than before.
func lastRecord(t *testing.T, h *ToolsetHarness, before int) agent.ToolCallRecord {
	t.Helper()

	recs := h.Records()
	if len(recs) != before+1 {
		t.Fatalf("the call wrote %d records, want 1", len(recs)-before)
	}

	return recs[len(recs)-1]
}

func caseToolsetSpecs(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	got, want := h.Toolset.Specs(), agent.Catalog()
	if len(got) != len(want) {
		t.Fatalf("Specs() has %d tools, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i].Name != want[i].Name || got[i].Action != want[i].Action {
			t.Fatalf("Specs()[%d] = %s %s, want %s %s", i, got[i].Name, got[i].Action, want[i].Name, want[i].Action)
		}
	}
}

func caseToolsetRead(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	before := len(h.Records())

	out, err := call(t, h, h.Reader, agent.ToolDescribeUnit, map[string]any{"unit": h.Unit.String()})
	if err != nil || len(out.Content) == 0 || len(out.Revealed) != 0 {
		t.Fatalf("describe_unit = %+v, %v, want a masked result", out, err)
	}

	rec := lastRecord(t, h, before)
	if rec.Outcome != audit.OutcomeSuccess || rec.Tool != agent.ToolDescribeUnit || rec.Object != h.Unit.String() {
		t.Fatalf("record = %+v, want a success of describe_unit on the unit", rec)
	}

	if !slices.Equal(rec.Actions, []identity.Action{identity.ActionUnitRead}) {
		t.Fatalf("record actions = %v, want unit:read after agent:use", rec.Actions)
	}

	if rec.Actor.Principal.Subject != h.Reader.Principal.Subject || rec.Actor.Agent != h.Reader.Agent {
		t.Fatalf("record actor = %+v, want the person and the agent of the call", rec.Actor)
	}

	wantDigest(t, rec, h.Unit.String())
}

// wantDigest fails t unless rec holds the digest of the arguments of a call
// on unit, and no argument.
func wantDigest(t *testing.T, rec agent.ToolCallRecord, unit string) {
	t.Helper()

	want, err := agent.ArgsDigest(json.RawMessage(fmt.Sprintf(`{"unit":%q}`, unit)))
	if err != nil || rec.ArgsDigest != want {
		t.Fatalf("record digest = %q, want %q: the SHA-256 of the arguments in canonical JSON", rec.ArgsDigest, want)
	}
}

func caseToolsetNoAgentUse(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	before := len(h.Records())

	out, err := call(t, h, h.NoAgentUse, agent.ToolDescribeUnit, map[string]any{"unit": h.Unit.String()})
	if !errors.Is(err, identity.ErrDenied) || len(out.Content) != 0 {
		t.Fatalf("describe_unit without agent:use = %+v, %v, want ErrDenied and no content", out, err)
	}

	if rec := lastRecord(t, h, before); rec.Outcome != audit.OutcomeDenied {
		t.Fatalf("record = %+v, want the outcome denied", rec)
	}
}

func caseToolsetAuditFails(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	h.FailAudit(t)

	for _, tool := range []agent.ToolName{agent.ToolDescribeUnit, agent.ToolReadState} {
		out, err := call(t, h, h.Reader, tool, map[string]any{"unit": h.Unit.String()})
		if !errors.Is(err, agent.ErrAuditFailed) || len(out.Content) != 0 {
			t.Fatalf("%s with a failed audit write = %+v, %v, want ErrAuditFailed and no content", tool, out, err)
		}
	}
}

func caseToolsetUnknown(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	before := len(h.Records())

	if _, err := call(t, h, h.Reader, "run_shell", map[string]any{}); !errors.Is(err, agent.ErrUnknownTool) {
		t.Fatalf("run_shell = %v, want ErrUnknownTool", err)
	}

	if rec := lastRecord(t, h, before); rec.Outcome != audit.OutcomeFailure {
		t.Fatalf("record = %+v, want the outcome failure", rec)
	}
}

func caseToolsetArguments(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	unit := h.Unit.String()
	tests := []struct {
		name string
		tool agent.ToolName
		args map[string]any
	}{
		{"missing required unit", agent.ToolDescribeUnit, map[string]any{}},
		{"unit of the wrong type", agent.ToolDescribeUnit, map[string]any{"unit": 7}},
		{
			"string above its maximum",
			agent.ToolSearchUnits,
			map[string]any{"query": strings.Repeat("a", agent.MaxQueryBytes+1)},
		},
		{
			"integer above its maximum",
			agent.ToolListHistory,
			map[string]any{"unit": unit, "limit": agent.MaxHistoryLimit + 1},
		},
		{"integer below 1", agent.ToolListHistory, map[string]any{"unit": unit, "limit": 0}},
		{"value outside the enum", agent.ToolObserveUnit, map[string]any{"unit": unit, "what": "secrets"}},
		{"unknown parameter", agent.ToolDescribeUnit, map[string]any{"unit": unit, "command": "ls"}},
		{"secret values with a comparison", agent.ToolReadState, map[string]any{
			"unit": unit, "compareTo": "1", "secretValues": true,
		}},
	}

	for _, tt := range tests {
		before := len(h.Records())

		if _, err := call(t, h, h.Reader, tt.tool, tt.args); !errors.Is(err, agent.ErrInvalidArguments) {
			t.Fatalf("%s: %s = %v, want ErrInvalidArguments", tt.name, tt.tool, err)
		}

		lastRecord(t, h, before)
	}
}

func readSecrets(t *testing.T, h *ToolsetHarness, actor agent.Actor) (agent.ToolOutput, agent.ToolCallRecord) {
	t.Helper()

	return readSecretsWith(t, h, actor, "")
}

// readSecretsWith reads the secret values of the unit for actor, with the
// consent consentID when it is not empty.
func readSecretsWith(t *testing.T, h *ToolsetHarness, actor agent.Actor, consentID string) (
	agent.ToolOutput, agent.ToolCallRecord,
) {
	t.Helper()

	before := len(h.Records())
	args := map[string]any{"unit": h.Unit.String(), "secretValues": true}

	if consentID != "" {
		args[agent.ConsentParam] = consentID
	}

	out, err := call(t, h, actor, agent.ToolReadState, args)
	if err != nil {
		t.Fatalf("read_state with secret values = %v, want no error: a refusal answers masked values", err)
	}

	return out, lastRecord(t, h, before)
}

// stateOf returns the StateResult of out.
func stateOf(t *testing.T, out agent.ToolOutput) agent.StateResult {
	t.Helper()

	var res agent.StateResult
	if err := json.Unmarshal(out.Content, &res); err != nil {
		t.Fatalf("read_state answered %q, want a StateResult: %v", out.Content, err)
	}

	return res
}

// wantRefused fails t unless out is a masked read that stage refused.
func wantRefused(t *testing.T, h *ToolsetHarness, what string, out agent.ToolOutput, stage string) {
	t.Helper()

	res := stateOf(t, out)

	switch {
	case out.ClosedStage != stage || res.ClosedStage != stage:
		t.Fatalf("%s: closed stage %q (result %q), want %q", what, out.ClosedStage, res.ClosedStage, stage)
	case len(out.Revealed) != 0 || res.SecretValues || strings.Contains(string(out.Content), h.Secret):
		t.Fatalf("%s = %+v, want masked values only", what, out)
	default:
	}
}

// wantConsent fails t unless out is refused for its consent and names a
// consent in its output and in its StateResult, and returns the ID.
func wantConsent(t *testing.T, h *ToolsetHarness, what string, out agent.ToolOutput) string {
	t.Helper()

	wantRefused(t, h, what, out, agent.StageConsent)

	res := stateOf(t, out)

	switch {
	case out.ConsentID == "" || res.ConsentID != out.ConsentID:
		t.Fatalf("%s: consent %q (result %q), want one consent ID in both", what, out.ConsentID, res.ConsentID)
	case !strings.HasSuffix(out.ConsentURL, agent.ConsentPath(out.ConsentID)) || res.ConsentURL != out.ConsentURL:
		t.Fatalf("%s: consent URL %q (result %q), want the console page of the consent", what, out.ConsentURL,
			res.ConsentURL)
	default:
	}

	return out.ConsentID
}

// wantRevealed fails t unless out holds the secret value.
func wantRevealed(t *testing.T, h *ToolsetHarness, what string, out agent.ToolOutput) {
	t.Helper()

	if out.ClosedStage != "" || !slices.Contains(out.Revealed, h.Secret) || !stateOf(t, out).SecretValues {
		t.Fatalf("%s = %+v, want the secret value", what, out)
	}
}

func caseToolsetGateClosed(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	out, rec := readSecrets(t, h, h.Reader)

	switch {
	case out.ClosedStage == "" || len(out.Revealed) != 0 || strings.Contains(string(out.Content), h.Secret):
		t.Fatalf("read_state with a closed gate = %+v, want masked values and the closed stage", out)
	case rec.Outcome != audit.OutcomeDenied || rec.ClosedStage == "" || rec.RevealCount != 0:
		t.Fatalf("record = %+v, want denied with the closed stage and no value", rec)
	default:
	}
}

func caseToolsetGateOpen(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	h.Gate(t, OpenGate())

	out, rec := readSecrets(t, h, h.Reader)

	switch {
	case out.ClosedStage != "" || !slices.Contains(out.Revealed, h.Secret) ||
		!strings.Contains(string(out.Content), h.Secret):
		t.Fatalf("read_state with an open gate = %+v, want the value and its entry in Revealed", out)
	case rec.Outcome != audit.OutcomeSuccess || rec.RevealCount < 1 ||
		!slices.Contains(rec.Actions, identity.ActionUnitReadSecrets):
		t.Fatalf("record = %+v, want a success that counts the value and names unit:read-secrets", rec)
	default:
	}
}

func caseToolsetConsent(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	// A stage before the consent is closed: no consent is made, because the
	// person could not use it.
	out, _ := readSecrets(t, h, h.Outside)
	wantRefused(t, h, "read_state over MCP with the gate closed", out, agent.StageFlag)

	if out.ConsentID != "" || stateOf(t, out).ConsentID != "" {
		t.Fatalf("read_state over MCP with the gate closed made the consent %q, want none", out.ConsentID)
	}

	h.Gate(t, OpenGate())

	out, rec := readSecrets(t, h, h.Outside)
	wantConsent(t, h, "read_state over MCP with no consent", out)

	if rec.Outcome != audit.OutcomeDenied || rec.ClosedStage != agent.StageConsent || rec.RevealCount != 0 {
		t.Fatalf("record = %+v, want denied at the consent stage", rec)
	}
}

func caseToolsetConsentOnce(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	h.Gate(t, OpenGate())

	out, _ := readSecrets(t, h, h.Outside)
	id := wantConsent(t, h, "first read", out)

	out, _ = readSecretsWith(t, h, h.Outside, id)
	if got := wantConsent(t, h, "read before the approval", out); got != id {
		t.Fatalf("read before the approval named consent %q, want the pending consent %q", got, id)
	}

	h.ApproveConsent(t, id)

	out, rec := readSecretsWith(t, h, h.Outside, id)
	wantRevealed(t, h, "read with the approved consent", out)

	if rec.Outcome != audit.OutcomeSuccess || rec.RevealCount < 1 {
		t.Fatalf("record = %+v, want a success that counts the value", rec)
	}

	out, _ = readSecretsWith(t, h, h.Outside, id)
	if got := wantConsent(t, h, "second read with the used consent", out); got == id {
		t.Fatalf("second read named the used consent %q, want a new consent", id)
	}
}

func caseToolsetConsentSession(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	h.Gate(t, OpenGate())

	out, _ := readSecrets(t, h, h.Outside)
	id := wantConsent(t, h, "first read", out)
	h.ApproveConsent(t, id)

	other := h.Outside
	other.Agent.SessionID += "-other"

	out, _ = readSecretsWith(t, h, other, id)
	wantConsent(t, h, "read with the consent of another session", out)

	// The refused use did not use the consent.
	out, _ = readSecretsWith(t, h, h.Outside, id)
	wantRevealed(t, h, "read in the session of the consent", out)
}

func caseToolsetConsentBlocksPropose(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	h.Gate(t, OpenGate())

	out, _ := readSecrets(t, h, h.Outside)
	id := wantConsent(t, h, "first read", out)
	h.ApproveConsent(t, id)

	out, _ = readSecretsWith(t, h, h.Outside, id)
	wantRevealed(t, h, "read with the approved consent", out)

	newSession := h.Outside
	newSession.Agent.SessionID += "-new"

	wantProposeRefused(t, h, h.Outside, "in the session of the reveal", agent.ErrProposeAfterReveal)
	wantProposeRefused(t, h, newSession, "in a new session with the token", agent.ErrProposeAfterReveal)
}

// wantProposeRefused fails t unless propose_change for actor fails with want
// and writes a denied record.
func wantProposeRefused(t *testing.T, h *ToolsetHarness, actor agent.Actor, what string, want error) {
	t.Helper()

	before := len(h.Records())
	args := map[string]any{"unit": h.Unit.String(), "operation": "apply", "reason": "Scale out."}

	if _, err := call(t, h, actor, agent.ToolProposeChange, args); !errors.Is(err, want) {
		t.Fatalf("propose_change %s = %v, want %v", what, err, want)
	}

	if rec := lastRecord(t, h, before); rec.Outcome != audit.OutcomeDenied {
		t.Fatalf("record of propose_change %s = %+v, want the outcome denied", what, rec)
	}
}

func caseToolsetServicePrincipal(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	h.Gate(t, OpenGate())

	wantProposeRefused(t, h, h.Service, "by a service principal", agent.ErrServicePrincipal)

	before := len(h.Records())
	args := map[string]any{"unit": h.Unit.String(), "secretValues": true}

	out, err := call(t, h, h.Service, agent.ToolReadState, args)
	if !errors.Is(err, agent.ErrServicePrincipal) || len(out.Content) != 0 || out.ConsentID != "" {
		t.Fatalf("secret read by a service principal = %+v, %v, want ErrServicePrincipal at once", out, err)
	}

	if rec := lastRecord(t, h, before); rec.Outcome != audit.OutcomeDenied {
		t.Fatalf("record = %+v, want the outcome denied", rec)
	}

	if _, err := call(t, h, h.Service, agent.ToolDescribeUnit, map[string]any{"unit": h.Unit.String()}); err != nil {
		t.Fatalf("describe_unit by a service principal = %v, want a masked read", err)
	}
}

func caseToolsetProposeAfterReveal(t *testing.T, h *ToolsetHarness) {
	t.Helper()

	h.MarkRevealed(t, h.Reader)
	wantProposeRefused(t, h, h.Reader, "after a reveal of the run", agent.ErrProposeAfterReveal)
}
