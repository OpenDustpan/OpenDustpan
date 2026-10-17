package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

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
)

// ToolName is the name of a tool, as the model and an MCP client see it.
type ToolName string

// The tools of the catalog. A tool is a task, not one API route: it can read
// several objects, and it answers a shape that a model can use.
const (
	ToolSearchUnits   ToolName = "search_units"
	ToolDescribeUnit  ToolName = "describe_unit"
	ToolReadState     ToolName = "read_state"
	ToolCompareStates ToolName = "compare_states"
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

// The default limits of the tools that list.
const (
	defaultSearchLimit  = 20
	defaultHistoryLimit = 20
	defaultAuditLimit   = 50
)

// ParamType is the JSON type of a parameter.
type ParamType string

// The types of a parameter.
const (
	ParamString  ParamType = "string"
	ParamInteger ParamType = "integer"
	ParamBoolean ParamType = "boolean"
	ParamObject  ParamType = "object"
)

// Param is one parameter of a tool. An adapter makes the JSON Schema that
// its model or MCP client needs from it.
type Param struct {
	Name        string
	Type        ParamType
	Required    bool
	Description string
}

// ToolSpec describes one tool. Action is the action that the server checks
// on the object that Object names, for the actor of the call. Every call
// also checks ActionAgentUse on the namespace of the object. SecretAction is
// the action that a call which asks for secret values in plain text checks
// in addition, before the secret gate. It is empty for a tool that never
// answers a secret value. ProposeChange checks more: the Changes port checks
// the action of the kind of change.
type ToolSpec struct {
	Name         ToolName
	Description  string
	Action       identity.Action
	SecretAction identity.Action
	Object       ObjectKind
	Effect       Effect
	Params       []Param
}

// Toolset runs the tools of the catalog. The loop of the built-in agent, the
// MCP endpoint and the stdio proxy (through the MCP endpoint) all call one
// Toolset, so every channel gets the same tools, the same authorization and
// the same audit entries.
type Toolset interface {
	// Specs returns the catalog: the same specs, in the same order, as
	// Catalog.
	Specs() []ToolSpec
	// Call runs one tool call for actor. It checks ActionAgentUse and the
	// action of the tool before it reads or changes anything, and writes one
	// audit entry for the call whatever the outcome. key makes a call with
	// an effect other than EffectRead safe to repeat: two calls with one key
	// give one change. It returns ErrUnknownTool, ErrInvalidArguments, an
	// error that wraps identity.ErrDenied, ErrSecretsClosed for a secret
	// read that the gate refuses, and the errors of the Changes port.
	Call(ctx context.Context, actor Actor, key string, call ToolCall) (json.RawMessage, error)
}

// Catalog returns the tools that every channel offers, in a fixed order. The
// caller owns the returned slice.
func Catalog() []ToolSpec {
	return slices.Concat(findTools(), unitTools(), historyTools(), changeTools())
}

// findTools returns the tools that find objects and read the audit log, access and docs.
func findTools() []ToolSpec {
	return []ToolSpec{
		{
			Name: ToolSearchUnits,
			Description: "Find units (stacks and workspaces) by name, tool, namespace, collection, label, " +
				"owner or resource type. Answers only the units that you can read.",
			Action:       identity.ActionUnitRead,
			SecretAction: "",
			Object:       ObjectScope,
			Effect:       EffectRead,
			Params: []Param{
				{Name: "query", Type: ParamString, Required: false, Description: "Words to find in names and labels."},
				{Name: "namespace", Type: ParamString, Required: false, Description: "Find only in this namespace."},
				{Name: "tool", Type: ParamString, Required: false, Description: "Find only units of this tool."},
				{
					Name:        "resourceType",
					Type:        ParamString,
					Required:    false,
					Description: "Find units with this resource type.",
				},
				limitParam("units", defaultSearchLimit),
			},
		},
		{
			Name:         ToolReadAuditLog,
			Description:  "Read the audit entries of a namespace, newest first. Filter by unit, subject, action or time.",
			Action:       identity.ActionNamespaceReadAudit,
			SecretAction: "",
			Object:       ObjectNamespace,
			Effect:       EffectRead,
			Params: []Param{
				{Name: "namespace", Type: ParamString, Required: true, Description: "The namespace."},
				{Name: "unit", Type: ParamString, Required: false, Description: "Only the entries of this unit."},
				{Name: "subject", Type: ParamString, Required: false, Description: "Only the entries of this subject."},
				{
					Name:        "fromMs",
					Type:        ParamInteger,
					Required:    false,
					Description: "The start of the time range, in Unix milliseconds.",
				},
				limitParam("entries", defaultAuditLimit),
			},
		},
		{
			Name:         ToolExplainAccess,
			Description:  "Show who can do what on a unit, and through which grant, group or role.",
			Action:       identity.ActionUnitReadAccess,
			SecretAction: "",
			Object:       ObjectUnit,
			Effect:       EffectRead,
			Params: []Param{
				unitParam(),
				{Name: "subject", Type: ParamString, Required: false, Description: "Only the access of this subject."},
			},
		},
		{
			Name:         ToolSearchDocs,
			Description:  "Find pages in the OpenDustpan documentation of this server version.",
			Action:       identity.ActionCallerRead,
			SecretAction: "",
			Object:       ObjectCaller,
			Effect:       EffectRead,
			Params: []Param{
				{Name: "query", Type: ParamString, Required: true, Description: "The words to find."},
			},
		},
	}
}

// unitTools returns the tools that read one unit.
func unitTools() []ToolSpec {
	return []ToolSpec{
		{
			Name: ToolDescribeUnit,
			Description: "Get the summary of one unit: its context output, docs, links, owners, on-call, " +
				"lock, last change, outputs with secret values masked, and resource counts.",
			Action:       identity.ActionUnitRead,
			SecretAction: "",
			Object:       ObjectUnit,
			Effect:       EffectRead,
			Params:       []Param{unitParam()},
		},
		{
			Name: ToolReadState,
			Description: "Read the resources of a unit at one version, with secret values masked. " +
				"Filter by resource type or address to keep the answer short.",
			Action:       identity.ActionUnitRead,
			SecretAction: identity.ActionUnitReadSecrets,
			Object:       ObjectUnit,
			Effect:       EffectRead,
			Params: []Param{
				unitParam(),
				versionParam("The version to read. The default is the latest version."),
				{Name: "filter", Type: ParamString, Required: false, Description: "A resource type or address prefix."},
				{
					Name:        "secretValues",
					Type:        ParamBoolean,
					Required:    false,
					Description: "Ask for secret values in plain text. The server refuses unless every opt-in is set.",
				},
			},
		},
		{
			Name:         ToolCompareStates,
			Description:  "Compare two versions of a unit: resources added, deleted and changed, with secret values masked.",
			Action:       identity.ActionUnitRead,
			SecretAction: "",
			Object:       ObjectUnit,
			Effect:       EffectRead,
			Params: []Param{
				unitParam(),
				{Name: "from", Type: ParamString, Required: true, Description: "The older version."},
				versionParam("The newer version. The default is the latest version."),
			},
		},
	}
}

// historyTools returns the tools that read the history of one unit.
func historyTools() []ToolSpec {
	return []ToolSpec{
		{
			Name:         ToolListHistory,
			Description:  "List the changes of a unit, newest first: updates, state versions, jobs, locks and who made each.",
			Action:       identity.ActionUnitRead,
			SecretAction: "",
			Object:       ObjectUnit,
			Effect:       EffectRead,
			Params: []Param{
				unitParam(),
				limitParam("entries", defaultHistoryLimit),
			},
		},
		{
			Name:         ToolReadRunOutput,
			Description:  "Read the events and the output of one update or job of a unit, with secret values masked.",
			Action:       identity.ActionUnitRead,
			SecretAction: "",
			Object:       ObjectUnit,
			Effect:       EffectRead,
			Params: []Param{
				unitParam(),
				{Name: "run", Type: ParamString, Required: true, Description: "The update or job, from list_history."},
				{
					Name:        "errorsOnly",
					Type:        ParamBoolean,
					Required:    false,
					Description: "Answer only the events of failures.",
				},
			},
		},
	}
}

// changeTools returns the tools that propose, read and cancel changes.
func changeTools() []ToolSpec {
	return []ToolSpec{
		{
			Name: ToolProposeChange,
			Description: "Propose a change to a unit. It becomes a review request or a job that waits for approval, " +
				"unless every opt-in for direct changes is set. Give the reason in plain words.",
			Action:       identity.ActionUnitWrite,
			SecretAction: "",
			Object:       ObjectUnit,
			Effect:       EffectPropose,
			Params: []Param{
				unitParam(),
				{
					Name:        "kind",
					Type:        ParamString,
					Required:    true,
					Description: "The kind of change, from describe_unit.",
				},
				{Name: "spec", Type: ParamObject, Required: true, Description: "The change, in the shape of its kind."},
				{Name: "reason", Type: ParamString, Required: true, Description: "Why the change is necessary."},
				{
					Name:        "mode",
					Type:        ParamString,
					Required:    false,
					Description: "review-request, approve-first or direct.",
				},
			},
		},
		{
			Name:         ToolGetChange,
			Description:  "Get the state of a change: waiting for review or approval, running, done or failed.",
			Action:       identity.ActionUnitRead,
			SecretAction: "",
			Object:       ObjectChangeUnit,
			Effect:       EffectRead,
			Params:       []Param{changeParam()},
		},
		{
			Name:         ToolCancelChange,
			Description:  "Cancel a change that did not start to apply.",
			Action:       identity.ActionUnitWrite,
			SecretAction: "",
			Object:       ObjectChangeUnit,
			Effect:       EffectCancel,
			Params:       []Param{changeParam()},
		},
	}
}

func unitParam() Param {
	return Param{
		Name:        "unit",
		Type:        ParamString,
		Required:    true,
		Description: "The unit reference: <tool>:<namespace>/<name>.",
	}
}

func versionParam(description string) Param {
	return Param{Name: "version", Type: ParamString, Required: false, Description: description}
}

func limitParam(what string, def int) Param {
	return Param{
		Name:        "limit",
		Type:        ParamInteger,
		Required:    false,
		Description: fmt.Sprintf("The maximum number of %s. The default is %d.", what, def),
	}
}

func changeParam() Param {
	return Param{Name: "change", Type: ParamString, Required: true, Description: "The change ID."}
}
