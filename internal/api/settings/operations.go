package settings

import (
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/identity"
)

// tagSettings is the tag of every operation of this adapter.
const tagSettings = "unit-settings"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route.
func operationLabels() map[string]apidesc.Label {
	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, unitSettingsPath): apidesc.Name("getUnitSettings", tagSettings).
			On(apidesc.KindUnit).WithETag().WithBodies(apidesc.NoBody, settingsResponseSchema()),
		apidesc.Pattern(http.MethodPut, unitSettingsPath): apidesc.Name("replaceUnitSettings", tagSettings).
			On(apidesc.KindUnit).
			WithETag().WithIfMatchRequired().WithBodies(settingsSchema(), settingsResponseSchema()),
		apidesc.Pattern(http.MethodDelete, unitSettingsPath): apidesc.Name("resetUnitSettings", tagSettings).
			On(apidesc.KindUnit).WithIfMatchRequired().WithBodies(apidesc.NoBody, apidesc.NoBody),
		apidesc.Pattern(http.MethodGet, suggestionsPath): apidesc.Name("getUnitSettingsSuggestions", tagSettings).
			On(apidesc.KindUnit).WithBodies(apidesc.NoBody, suggestionsSchema()),
		apidesc.Pattern(http.MethodGet, defaultsPath): apidesc.Name("getUnitDefaults", tagSettings).
			On(apidesc.KindNamespace).WithETag().WithBodies(apidesc.NoBody, defaultsResponseSchema()),
		apidesc.Pattern(http.MethodPut, defaultsPath): apidesc.Name("replaceUnitDefaults", tagSettings).
			On(apidesc.KindNamespace).WithETag().WithIfMatchRequired().
			WithBodies(defaultsRequestSchema(), defaultsResponseSchema()),
		apidesc.Pattern(http.MethodPost, bulkPath): apidesc.Name("bulkReplaceUnitSettings", tagSettings).
			On(apidesc.KindNamespace).WithBodies(bulkSchema(), bulkResponseSchema()),
	}
}

// The bodies that several routes share.

func settingsSchema() apidesc.Body {
	return apidesc.Typed[settingsBody]("UnitSettingsFields", requestParts()...)
}

func settingsResponseSchema() apidesc.Body {
	return apidesc.Typed[settingsResponse]("UnitSettings", append(requestParts(),
		apidesc.Named[settingsBody]("UnitSettingsFields"),
		apidesc.Named[effectiveBody]("UnitSettingsEffective"),
		apidesc.Named[stringField]("UnitSettingsStringField"),
		apidesc.Named[listField]("UnitSettingsListField"),
		apidesc.Named[boolField]("UnitSettingsBoolField"),
		apidesc.Named[stepsField]("UnitSettingsStepsField"),
	)...)
}

func suggestionsSchema() apidesc.Body {
	return apidesc.Typed[suggestionsResponse]("UnitSettingsSuggestions",
		apidesc.Named[suggestionBody]("UnitSettingsSuggestion"))
}

func defaultsRequestSchema() apidesc.Body { return apidesc.Typed[defaultsBody]("UnitDefaultsRequest") }

func defaultsResponseSchema() apidesc.Body { return apidesc.Typed[defaultsResponse]("UnitDefaults") }

func bulkSchema() apidesc.Body {
	return apidesc.Typed[bulkBody]("BulkUnitSettingsRequest", append(requestParts(),
		apidesc.Named[bulkUnit]("BulkUnitSettingsUnit"))...)
}

func bulkResponseSchema() apidesc.Body {
	return apidesc.Typed[bulkResponse]("BulkUnitSettingsResult",
		apidesc.Named[bulkResult]("BulkUnitSettingsItem"), apidesc.Named[bulkError]("BulkUnitSettingsError"))
}

// requestParts declares the nested types of the settings body and the value
// sets of its string types. A set can grow, so each is an open enum: a client
// keeps a value that it does not know.
func requestParts() []apidesc.Declaration {
	return []apidesc.Declaration{
		apidesc.Named[codeLocationBody]("UnitSettingsCodeLocation"),
		apidesc.Named[writeBackBody]("UnitSettingsWriteBack"),
		apidesc.Named[reviewRequestsBody]("UnitSettingsReviewRequests"),
		apidesc.Named[stepBody]("UnitSettingsStep"),
		apidesc.Named[outputBody]("UnitSettingsStepOutput"),
	}
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for i := range h.routes {
		route := &h.routes[i]

		// The wrapper reads the namespace of the path before the actions on the
		// object, for each route that is not public.
		var namespaceActions []string

		actions := make([]string, 0, len(route.Reads)+len(route.Actions)+len(route.Extra)+1)

		if route.Scope != ScopePublic {
			namespaceActions = []string{string(identity.ActionNamespaceRead)}
			actions = append(actions, namespaceActions...)
		}

		for _, action := range slices.Concat(route.Reads, route.Actions, route.Extra) {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method:           route.Method,
			Path:             route.Path,
			Actions:          actions,
			NamespaceActions: namespaceActions,
			Public:           route.Scope == ScopePublic,
			IdempotencyKey:   route.Keyed,
		})
	}

	return apidesc.Collect(rows, operationLabels())
}
