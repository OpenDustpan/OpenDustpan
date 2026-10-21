package jobs

import (
	"net/http"
	"slices"

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
	).WithETag(),
	apidesc.Pattern(http.MethodPost, "/api/dustpan/v1/namespaces/{namespace}/jobs"): apidesc.Name(
		"startJob",
		"jobs",
	).WithETag(),
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		actions := make([]string, 0, len(route.Reads)+len(route.Writes))
		for _, action := range slices.Concat(route.Reads, route.Writes) {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: route.Method, Path: route.Path, Actions: actions, Public: route.Scope == ScopePublic,
			IdempotencyKey: route.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
