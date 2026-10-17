package agenttest

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
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
	// Records returns the tool call records that the Toolset wrote, in
	// order.
	Records func() []agent.ToolCallRecord
	// FailAudit makes every later write of a record fail.
	FailAudit func(t *testing.T)
	// Gate sets the stages of the secret gate for later calls. The Toolset
	// takes the channel from the actor, not from s.
	Gate func(t *testing.T, s agent.SecretAccess)
	// MarkRevealed records that the run or the MCP session of actor read
	// secret values in plain text.
	MarkRevealed func(t *testing.T, actor agent.Actor)
}

// ToolsetFactory returns a fresh harness for one case.
type ToolsetFactory func(t *testing.T) ToolsetHarness

// RunToolset runs the toolset suite against the Toolset that f makes.
func RunToolset(t *testing.T, f ToolsetFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h ToolsetHarness)
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
		{name: "no propose after a reveal", run: caseToolsetProposeAfterReveal},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
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
func call(t *testing.T, h ToolsetHarness, actor agent.Actor, tool agent.ToolName, args map[string]any) (
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
func lastRecord(t *testing.T, h ToolsetHarness, before int) agent.ToolCallRecord {
	t.Helper()

	recs := h.Records()
	if len(recs) != before+1 {
		t.Fatalf("the call wrote %d records, want 1", len(recs)-before)
	}

	return recs[len(recs)-1]
}

func caseToolsetSpecs(t *testing.T, h ToolsetHarness) {
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

func caseToolsetRead(t *testing.T, h ToolsetHarness) {
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

func caseToolsetNoAgentUse(t *testing.T, h ToolsetHarness) {
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

func caseToolsetAuditFails(t *testing.T, h ToolsetHarness) {
	t.Helper()

	h.FailAudit(t)

	for _, tool := range []agent.ToolName{agent.ToolDescribeUnit, agent.ToolReadState} {
		out, err := call(t, h, h.Reader, tool, map[string]any{"unit": h.Unit.String()})
		if !errors.Is(err, agent.ErrAuditFailed) || len(out.Content) != 0 {
			t.Fatalf("%s with a failed audit write = %+v, %v, want ErrAuditFailed and no content", tool, out, err)
		}
	}
}

func caseToolsetUnknown(t *testing.T, h ToolsetHarness) {
	t.Helper()

	before := len(h.Records())

	if _, err := call(t, h, h.Reader, "run_shell", map[string]any{}); !errors.Is(err, agent.ErrUnknownTool) {
		t.Fatalf("run_shell = %v, want ErrUnknownTool", err)
	}

	if rec := lastRecord(t, h, before); rec.Outcome != audit.OutcomeFailure {
		t.Fatalf("record = %+v, want the outcome failure", rec)
	}
}

func caseToolsetArguments(t *testing.T, h ToolsetHarness) {
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

func readSecrets(t *testing.T, h ToolsetHarness, actor agent.Actor) (agent.ToolOutput, agent.ToolCallRecord) {
	t.Helper()

	before := len(h.Records())

	out, err := call(t, h, actor, agent.ToolReadState, map[string]any{"unit": h.Unit.String(), "secretValues": true})
	if err != nil {
		t.Fatalf("read_state with secret values = %v, want no error: a refusal answers masked values", err)
	}

	return out, lastRecord(t, h, before)
}

func caseToolsetGateClosed(t *testing.T, h ToolsetHarness) {
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

func caseToolsetGateOpen(t *testing.T, h ToolsetHarness) {
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

func caseToolsetConsent(t *testing.T, h ToolsetHarness) {
	t.Helper()

	h.Gate(t, OpenGate())

	outside := h.Reader
	outside.Agent = agent.Ref{Channel: agent.ChannelMCP, RunID: "", SessionID: "mcp-session-1"}

	out, rec := readSecrets(t, h, outside)
	if out.ClosedStage == "" || len(out.Revealed) != 0 || rec.Outcome != audit.OutcomeDenied {
		t.Fatalf("read_state over MCP with no consent = %+v, record %+v, want masked values and denied", out, rec)
	}
}

func caseToolsetProposeAfterReveal(t *testing.T, h ToolsetHarness) {
	t.Helper()

	h.MarkRevealed(t, h.Reader)

	before := len(h.Records())
	args := map[string]any{"unit": h.Unit.String(), "operation": "apply", "reason": "Scale out."}

	if _, err := call(t, h, h.Reader, agent.ToolProposeChange, args); !errors.Is(err, agent.ErrProposeAfterReveal) {
		t.Fatalf("propose_change after a reveal = %v, want ErrProposeAfterReveal", err)
	}

	if rec := lastRecord(t, h, before); rec.Outcome != audit.OutcomeDenied {
		t.Fatalf("record = %+v, want the outcome denied", rec)
	}
}
