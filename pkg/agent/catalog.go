package agent

import (
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
)

// Catalog returns the tools that every channel offers, in a fixed order. The
// caller owns the returned slice.
func Catalog() []ToolSpec {
	return slices.Concat(findTools(), unitTools(), historyTools(), changeTools())
}

// SpecFor returns the spec of the tool name, and false for a name that the
// catalog does not have.
func SpecFor(name ToolName) (ToolSpec, bool) {
	catalog := Catalog()
	for i := range catalog {
		if catalog[i].Name == name {
			return catalog[i], true
		}
	}

	var zero ToolSpec

	return zero, false
}

// findTools returns the tools that find objects and read the audit log,
// access and docs.
func findTools() []ToolSpec {
	return []ToolSpec{
		readTool(ToolSearchUnits,
			"Find units (stacks and workspaces) by name, tool, namespace, collection, label, "+
				"owner or resource type. Answers only the units that you can read.",
			identity.ActionUnitRead, ObjectScope,
			stringParam("query", false, "Words to find in names and labels.", MaxQueryBytes),
			stringParam("namespace", false, "Find only in this namespace.", MaxNameBytes),
			enumParam("tool", false, "Find only units of this tool.", "pulumi", "opentofu"),
			stringParam("resourceType", false, "Find units with this resource type.", MaxNameBytes),
			limitParam("units", DefaultSearchLimit, MaxSearchLimit),
		),
		readTool(ToolReadAuditLog,
			"Read the audit entries of a namespace, newest first. Filter by unit, subject, action or time.",
			identity.ActionNamespaceReadAudit, ObjectNamespace,
			stringParam("namespace", true, "The namespace.", MaxNameBytes),
			stringParam("unit", false, "Only the entries of this unit.", MaxNameBytes),
			stringParam("subject", false, "Only the entries of this subject.", MaxNameBytes),
			stringParam("action", false, "Only the entries of this action.", MaxNameBytes),
			intParam("fromMs", "The start of the time range, in Unix milliseconds.", maxUnixMs),
			limitParam("entries", DefaultAuditLimit, MaxAuditLimit),
		),
		readTool(ToolExplainAccess,
			"Show who can do what on a unit, and through which grant, group or role.",
			identity.ActionUnitReadAccess, ObjectUnit,
			unitParam(),
			stringParam("subject", false, "Only the access of this subject.", MaxNameBytes),
		),
		readTool(ToolSearchDocs,
			"Find pages in the OpenDustpan documentation of this server version.",
			identity.ActionCallerRead, ObjectCaller,
			stringParam("query", true, "The words to find.", MaxQueryBytes),
			limitParam("pages", DefaultDocsLimit, MaxDocsLimit),
		),
	}
}

// unitTools returns the tools that read one unit.
func unitTools() []ToolSpec {
	readState := readTool(ToolReadState,
		"Read the resources of a unit at one version, with secret values masked. With compareTo, "+
			"answer the resources added, deleted and changed since that older version instead. "+
			"Filter by resource type or address to keep the answer short.",
		identity.ActionUnitRead, ObjectUnit,
		unitParam(),
		versionParam("version", "The version to read. The default is the latest version."),
		versionParam("compareTo", "An older version. The answer is the difference from it to version."),
		stringParam("filter", false, "A resource type or address prefix.", MaxNameBytes),
		boolParam("secretValues",
			"Ask for secret values in plain text. The server refuses unless every opt-in is set, "+
				"and refuses it with compareTo."),
	)
	readState.SecretAction = identity.ActionUnitReadSecrets

	return []ToolSpec{
		readTool(ToolDescribeUnit,
			"Get the summary of one unit: its context output, docs, links, owners, on-call, lock, "+
				"last change, outputs with secret values masked, resource counts, and the executors "+
				"that a change can use.",
			identity.ActionUnitRead, ObjectUnit,
			unitParam(),
		),
		readState,
		readTool(ToolObserveUnit,
			"Read the live status of the resources of a unit from its cloud, read-only: events of "+
				"workloads and the health of targets. Secret values are masked.",
			ActionUnitObserve, ObjectUnit,
			unitParam(),
			stringParam("resource", false, "A resource address or address prefix.", MaxNameBytes),
			enumParam("what", false, "events, health or all. The default is all.", "events", "health", "all"),
			limitParam("items", DefaultObserveLimit, MaxObserveLimit),
		),
	}
}

// historyTools returns the tools that read the history of one unit.
func historyTools() []ToolSpec {
	return []ToolSpec{
		readTool(ToolListHistory,
			"List the changes of a unit, newest first: updates, state versions, jobs, locks and who made each.",
			identity.ActionUnitRead, ObjectUnit,
			unitParam(),
			limitParam("entries", DefaultHistoryLimit, MaxHistoryLimit),
		),
		readTool(ToolReadRunOutput,
			"Read the events and the log of one update or job of a unit, with secret values masked.",
			ActionJobReadLogs, ObjectUnit,
			unitParam(),
			stringParam("run", true, "The update or job, from list_history.", MaxNameBytes),
			boolParam("errorsOnly", "Answer only the events of failures."),
		),
	}
}

// changeTools returns the tools that propose, read and cancel changes.
func changeTools() []ToolSpec {
	return []ToolSpec{
		{
			Name: ToolProposeChange,
			Description: "Propose a change to a unit. It becomes a review request, or a job that waits for " +
				"the confirmation of the person who asked, unless every opt-in for direct changes is set. " +
				"Give the reason in plain words.",
			Action:       "",
			StartActions: true,
			SecretAction: "",
			Object:       ObjectUnit,
			Effect:       EffectPropose,
			Params: []Param{
				unitParam(),
				enumParam("operation", true, "What the change does.",
					string(OperationPlan), string(OperationApply), string(OperationDestroy), string(OperationRefresh)),
				enumParam("via", false, "The executor of the change, from describe_unit. The default fits the unit.",
					string(ExecutorWriteBack), string(ExecutorRunner)),
				{
					Name:        "edits",
					Type:        ParamArray,
					Required:    false,
					Description: "The file edits of a write-back: {path, keys}, where keys maps a key path to its new JSON value.",
					Enum:        nil,
					Max:         MaxEdits,
				},
				stringParam("reason", true, "Why the change is necessary.", MaxReasonBytes),
				enumParam("mode", false, "How the change goes in. The default is a review request or approve-first.",
					string(ModeReviewRequest), string(ModeApproveFirst), string(ModeDirect)),
			},
		},
		readTool(ToolGetChange,
			"Get the state of a change: waiting for confirmation or review, running, done or failed.",
			ActionJobRead, ObjectChangeUnit,
			changeParam(),
		),
		{
			Name:         ToolCancelChange,
			Description:  "Cancel a change that did not end.",
			Action:       ActionJobCancel,
			StartActions: false,
			SecretAction: "",
			Object:       ObjectChangeUnit,
			Effect:       EffectCancel,
			Params: []Param{
				changeParam(),
				stringParam("reason", false, "Why the change stops.", MaxReasonBytes),
			},
		},
	}
}

// maxUnixMs bounds a time parameter: the year 9999 in Unix milliseconds.
const maxUnixMs = 253402300799999

func readTool(name ToolName, description string, action identity.Action, obj ObjectKind, params ...Param) ToolSpec {
	return ToolSpec{
		Name:         name,
		Description:  description,
		Action:       action,
		StartActions: false,
		SecretAction: "",
		Object:       obj,
		Effect:       EffectRead,
		Params:       params,
	}
}

func stringParam(name string, required bool, description string, maxBytes int) Param {
	return Param{Name: name, Type: ParamString, Required: required, Description: description, Enum: nil, Max: maxBytes}
}

func enumParam(name string, required bool, description string, values ...string) Param {
	longest := 0
	for _, v := range values {
		longest = max(longest, len(v))
	}

	return Param{
		Name: name, Type: ParamString, Required: required, Description: description, Enum: values, Max: longest,
	}
}

func intParam(name, description string, maximum int) Param {
	return Param{Name: name, Type: ParamInteger, Required: false, Description: description, Enum: nil, Max: maximum}
}

func boolParam(name, description string) Param {
	return Param{Name: name, Type: ParamBoolean, Required: false, Description: description, Enum: nil, Max: 0}
}

func unitParam() Param {
	return stringParam("unit", true, "The unit reference: <tool>:<namespace>/<name>.", MaxNameBytes)
}

func versionParam(name, description string) Param {
	return stringParam(name, false, description, MaxNameBytes)
}

func limitParam(what string, def, maximum int) Param {
	return intParam(
		"limit",
		fmt.Sprintf("The maximum number of %s. The default is %d, the maximum %d.", what, def, maximum),
		maximum,
	)
}

func changeParam() Param {
	return stringParam("change", true, "The change ID.", MaxNameBytes)
}
