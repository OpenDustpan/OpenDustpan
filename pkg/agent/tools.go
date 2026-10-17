package agent

import (
	"errors"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Errors of a Toolset.
var (
	// ErrUnknownTool reports a call of a tool that the catalog does not
	// have.
	ErrUnknownTool = errors.New("unknown agent tool")
	// ErrInvalidArguments reports a call whose arguments do not match the
	// parameters of the tool.
	ErrInvalidArguments = errors.New("agent tool arguments are not valid")
	// ErrAuditFailed reports a tool call whose audit entry could not be
	// written. No tool call happens without its entry.
	ErrAuditFailed = errors.New("the audit entry of the agent tool call could not be written")
)

// ToolName is the name of a tool, as the model and an MCP client see it.
type ToolName string

// The tools of the catalog. A tool is a task, not one API route: it can read
// several objects, and it answers a shape that a model can use.
const (
	ToolSearchUnits   ToolName = "search_units"
	ToolDescribeUnit  ToolName = "describe_unit"
	ToolReadState     ToolName = "read_state"
	ToolObserveUnit   ToolName = "observe_unit"
	ToolListHistory   ToolName = "list_history"
	ToolReadRunOutput ToolName = "read_run_output"
	ToolReadAuditLog  ToolName = "read_audit_log"
	ToolExplainAccess ToolName = "explain_access"
	ToolSearchDocs    ToolName = "search_docs"
	ToolProposeChange ToolName = "propose_change"
	ToolGetChange     ToolName = "get_change"
	ToolCancelChange  ToolName = "cancel_change"
)

// ObjectKind tells which object the action of a tool applies to.
type ObjectKind string

// The object kinds of a tool.
const (
	// ObjectScope: the tool lists objects. It filters with
	// Authorizer.ListObjects for its action, never with one check per
	// object.
	ObjectScope ObjectKind = "scope"
	// ObjectUnit: the unit that the argument "unit" names.
	ObjectUnit ObjectKind = "unit"
	// ObjectNamespace: the namespace that the argument "namespace" names.
	ObjectNamespace ObjectKind = "namespace"
	// ObjectCaller: the caller itself.
	ObjectCaller ObjectKind = "caller"
	// ObjectChangeUnit: the unit of the change that the argument "change"
	// names.
	ObjectChangeUnit ObjectKind = "change-unit"
)

// Effect tells what a tool does.
type Effect string

// The effects of a tool.
const (
	// EffectRead reads and changes nothing.
	EffectRead Effect = "read"
	// EffectPropose starts a change through the Changes port.
	EffectPropose Effect = "propose"
	// EffectCancel cancels a change through the Changes port.
	EffectCancel Effect = "cancel"
)

// The default and maximum counts of the tools that list.
const (
	DefaultSearchLimit  = 20
	MaxSearchLimit      = 100
	DefaultHistoryLimit = 20
	MaxHistoryLimit     = 100
	DefaultAuditLimit   = 50
	MaxAuditLimit       = 200
	DefaultObserveLimit = 50
	MaxObserveLimit     = 200
	DefaultDocsLimit    = 5
	MaxDocsLimit        = 20
)

// The maximum lengths of the string parameters, in bytes.
const (
	MaxQueryBytes  = 200
	MaxNameBytes   = 512
	MaxReasonBytes = 2000
)

// MaxEdits bounds the file edits of one proposed change.
const MaxEdits = 20

// ParamType is the JSON type of a parameter.
type ParamType string

// The types of a parameter.
const (
	ParamString  ParamType = "string"
	ParamInteger ParamType = "integer"
	ParamBoolean ParamType = "boolean"
	ParamArray   ParamType = "array"
)

// Param is one parameter of a tool. An adapter makes the JSON Schema that
// its model or MCP client needs from it. Enum lists the only values of a
// string parameter, or is empty. Max is the maximum of an integer, the
// maximum length in bytes of a string, or the maximum count of an array. It
// is 0 for a boolean only. The Toolset refuses arguments outside these
// bounds with ErrInvalidArguments before any check.
type Param struct {
	Name        string
	Type        ParamType
	Required    bool
	Description string
	Enum        []string
	Max         int
}

// ToolSpec describes one tool. Action is the action that the server checks
// on the object that Object names, for the person of the call. Every call
// also checks ActionAgentUse on the namespace of the object. SecretAction is
// the action that a call which asks for secret values in plain text checks
// in addition, before the secret gate. It is empty for a tool that never
// answers a secret value. StartActions is true for the tool whose actions
// come from the job model for the change that it starts (see
// Changes.StartActions): its Action is then empty.
type ToolSpec struct {
	Name         ToolName
	Description  string
	Action       identity.Action
	StartActions bool
	SecretAction identity.Action
	Object       ObjectKind
	Effect       Effect
	Params       []Param
}
