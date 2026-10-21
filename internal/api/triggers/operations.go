package triggers

import (
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/cron"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// tagTriggers is the tag of every operation of this adapter.
const tagTriggers = "triggers"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, unitTriggersPath): apidesc.Name("listUnitTriggers", tagTriggers).
		On(apidesc.KindUnit).WithBodies(apidesc.NoBody, triggerListSchema),
	apidesc.Pattern(http.MethodPost, unitTriggersPath): apidesc.Name("createUnitTrigger", tagTriggers).
		On(apidesc.KindUnit).WithBodies(saveSchema, triggerSchema),
	apidesc.Pattern(http.MethodGet, triggerPath): apidesc.Name("getTrigger", tagTriggers).
		On(apidesc.KindUnit).WithBodies(apidesc.NoBody, triggerSchema),
	apidesc.Pattern(http.MethodPut, triggerPath): apidesc.Name("replaceTrigger", tagTriggers).
		On(apidesc.KindUnit).WithBodies(saveSchema, triggerSchema),
	apidesc.Pattern(http.MethodDelete, triggerPath): apidesc.Name("deleteTrigger", tagTriggers).
		On(apidesc.KindUnit).WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodPost, firePath): apidesc.Name("fireTrigger", tagTriggers).
		On(apidesc.KindUnit).WithBodies(fireSchema, fireResultSchema),
}

// The bodies that several routes share.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	saveSchema = apidesc.Typed[saveBody]("SaveTriggerRequest", append(triggerEnums(),
		apidesc.Named[frequencyBody]("TriggerFrequency"),
		apidesc.Named[sourceBody]("TriggerSourceRequest"),
	)...)
	triggerSchema = apidesc.Typed[triggerResponse]("Trigger", append(triggerEnums(),
		apidesc.Named[unitResponse]("TriggerUnit"),
		apidesc.Named[frequencyBody]("TriggerFrequency"),
	)...)
	triggerListSchema = apidesc.Typed[listResponse]("TriggerList", append(triggerEnums(),
		apidesc.Named[triggerResponse]("Trigger"),
		apidesc.Named[unitResponse]("TriggerUnit"),
		apidesc.Named[frequencyBody]("TriggerFrequency"),
	)...)
	fireSchema       = apidesc.Typed[fireBody]("FireTriggerRequest")
	fireResultSchema = apidesc.Typed[fireResponse]("FireTriggerResult", apidesc.Named[jobRef]("TriggerJob"))
)

// triggerEnums declares the value sets of the string types of the trigger API.
// A set can grow, so each is an open enum: a client keeps a value that it does
// not know. The values are the constants of the domain packages.
func triggerEnums() []apidesc.Declaration {
	return []apidesc.Declaration{
		apidesc.DeclareOpenEnum[trigger.Kind]("TriggerKind",
			string(trigger.KindReviewRequestPush), string(trigger.KindSchedule), string(trigger.KindFollows)),
		apidesc.DeclareOpenEnum[cron.Preset]("TriggerFrequencyPreset",
			string(cron.PresetHourly), string(cron.PresetEvery6Hours), string(cron.PresetDaily),
			string(cron.PresetWeekly)),
	}
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		// The wrapper reads the namespace of the path before the actions on the
		// unit, for each route that is not public.
		var namespaceActions []string

		actions := make([]string, 0, len(route.Reads)+len(route.Actions)+1)

		if route.Scope != ScopePublic {
			namespaceActions = []string{string(identity.ActionNamespaceRead)}
			actions = append(actions, namespaceActions...)
		}

		for _, action := range slices.Concat(route.Reads, route.Actions) {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method:           route.Method,
			Path:             route.Path,
			Actions:          actions,
			NamespaceActions: namespaceActions,
			Public:           route.Scope == ScopePublic,
			IdempotencyKey:   false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
