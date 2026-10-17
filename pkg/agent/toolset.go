package agent

import (
	"context"
	"encoding/json"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Toolset runs the tools of the catalog. The loop of the built-in agent, the
// MCP endpoint and the stdio proxy (through the MCP endpoint) all call one
// Toolset, so every channel gets the same tools, the same authorization and
// the same audit entries. Each implementation runs the conformance suite in
// package agenttest.
type Toolset interface {
	// Specs returns the catalog: the same specs, in the same order, as
	// Catalog.
	Specs() []ToolSpec
	// Call runs one tool call for actor. Before it reads or changes
	// anything, it checks the arguments with ValidateArguments, then
	// ActionAgentUse on the namespace of the object, then the action of
	// the tool (or Changes.StartActions) on the object, for the person of
	// actor as the caller resolved it just before the call. It
	// writes one ToolCallRecord through the ToolAuditor for the call
	// whatever the outcome, and fails the call when that write fails. key
	// makes a call with an effect other than EffectRead safe to repeat: two
	// calls with one key give one change.
	//
	// It returns ErrUnknownTool, ErrInvalidArguments, an error that wraps
	// identity.ErrDenied, ErrAuditFailed, ErrProposeAfterReveal for a
	// propose by a run with Run.ReadPlainSecrets or in an MCP session that
	// used a consent (ConsentStore.SessionRevealed), and the errors of the
	// Changes port. A secret read that the gate refuses is no error: the
	// output holds the masked values and the closed stage.
	Call(ctx context.Context, actor Actor, key string, call ToolCall) (ToolOutput, error)
}

// ToolOutput is the result of one tool call. Content is the JSON of the
// result types of this package. Revealed lists the secret values in plain
// text that Content holds, or is empty. The caller never records Content
// with a revealed value in it: the loop seals it until the next model step
// (see SealedReveal) and records Mask(Content, Revealed). ClosedStage names
// the stage of the secret gate that refused a secret read, or is empty.
type ToolOutput struct {
	Content     json.RawMessage
	Revealed    []string
	ClosedStage string
}

// ToolCallRecord is the audit record of one tool call. The ToolAuditor
// writes it as an entry of the kind ToolAuditKind. It holds no argument, no
// result and no prompt: ArgsDigest is the SHA-256 of the arguments in
// canonical JSON, in hex.
type ToolCallRecord struct {
	Actor Actor
	Tool  ToolName
	// Actions are the actions that the call checked after ActionAgentUse,
	// in order.
	Actions []identity.Action
	// Namespace is the namespace of the object, or audit.ServerScope for a
	// call on the caller.
	Namespace namespace.Name
	// Object is the reference of the object, or the namespace of a list.
	Object     string
	ArgsDigest string
	Outcome    audit.Outcome
	// Status is the HTTP status that the same result has in the API.
	Status int
	// ClosedStage is the closed stage of a secret read that the gate
	// refused, or empty.
	ClosedStage string
	// RevealCount is the number of secret values in plain text in the
	// result.
	RevealCount int
	At          time.Time
}

// ToolAuditKind is the audit entry kind of a tool call.
const ToolAuditKind audit.Kind = "agent-tool"

// ToolAuditor writes the audit records of tool calls. The server implements
// it on the audit store, in the namespace of the record, and also writes
// the reveal entry of a call with RevealCount above 0.
type ToolAuditor interface {
	// RecordToolCall writes rec. An error means that no entry is written,
	// and the Toolset then fails the call with ErrAuditFailed.
	RecordToolCall(ctx context.Context, rec ToolCallRecord) error
}
