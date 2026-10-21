package jobs

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// operationLabels names the routes of this adapter. The key is the pattern of
// the route.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, "/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}"): apidesc.Name(
		"getJob",
		"jobs",
	).On(apidesc.KindUnit).WithBodies(apidesc.NoBody, apidesc.Typed[jobResponse](
		"Job",
		apidesc.Named[unitResponse]("JobUnit"),
		apidesc.Named[locationResponse]("JobLocation"),
		apidesc.Named[stepResponse]("JobStep"),
		apidesc.Named[phaseResponse]("JobPhase"),
		apidesc.Named[stepRecordResponse]("JobStepRecord"),
		apidesc.Named[logRef]("JobLog"),
		apidesc.Named[planResponse]("JobPlan"),
		apidesc.Named[planPartResponse]("JobPlanPart"),
		apidesc.Named[gateResponse]("JobGate"),
		apidesc.Named[approvalResponse]("JobApproval"),
		apidesc.Named[breakGlassBody]("JobBreakGlass"),
		apidesc.Named[deliveryResponse]("JobDelivery"),
		apidesc.Named[closedResponse]("JobClosed"),
	)),
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		actions := make([]string, 0, len(route.Reads))
		for _, action := range route.Reads {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method:           route.Method,
			Path:             route.Path,
			Actions:          actions,
			NamespaceActions: nil,
			Public:           route.Scope == ScopePublic,
			IdempotencyKey:   false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
