package drift

import (
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/identity"
)

// tagDrift is the tag of every operation of this adapter.
const tagDrift = "drift"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, driftPath): apidesc.Name("getUnitDrift", tagDrift).
		On(apidesc.KindUnit).
		WithBodies(apidesc.NoBody, driftDocument),
	apidesc.Pattern(http.MethodPost, driftChecksURL): apidesc.Name("startDriftCheck", tagDrift).
		On(apidesc.KindUnit).
		WithBodies(startRequest, checkDocument),
}

// The bodies of the routes.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	driftDocument = apidesc.Typed[driftResponse](
		"UnitDrift", driftEnums(),
		apidesc.Named[unitResponse]("DriftUnit"),
		apidesc.Named[resourceResponse]("DriftResource"),
	)
	checkDocument = apidesc.Typed[checkResponse](
		"DriftCheck", apidesc.Named[unitResponse]("DriftUnit"),
	)
	startRequest = apidesc.Typed[startBody](
		"StartDriftCheckRequest", apidesc.Named[locationRequest]("DriftCheckLocation"),
	)
)

// driftEnums declares the value sets of the string types of the drift API. A set
// can grow, so each is an open enum: a client keeps a value that it does not
// know. The values are the constants of the domain package.
func driftEnums() apidesc.Declaration {
	return apidesc.DeclareOpenEnum[drift.Status]("DriftStatus",
		string(drift.StatusDrifted), string(drift.StatusInSync), string(drift.StatusUnknown))
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		// The wrapper reads the namespace of the path before the reads on the
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
			Method: route.Method, Path: route.Path, Actions: actions, NamespaceActions: namespaceActions,
			Public: route.Scope == ScopePublic, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
