package jobs

import (
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/identity"
)

// operationLabels names the routes of this adapter. The key is the pattern of
// the route.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, "/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}"): apidesc.Name(
		"getJob",
		"jobs",
	).WithETag().On(apidesc.KindUnit).WithBodies(apidesc.NoBody, jobSchema),
	apidesc.Pattern(http.MethodGet, jobsPath): apidesc.Name("listJobs", "jobs").On(apidesc.KindUnit).WithQuery(
		apidesc.StringParam("unit", "Keeps the jobs of the unit with this unit ID."),
		apidesc.StringParam("status", "Keeps the jobs in this state. One of the states must match.").Repeat(),
		apidesc.StringParam("purpose", "Keeps the jobs with this purpose. The only value is drift-check."),
		apidesc.StringParam("trigger", "Keeps the jobs that this run trigger started."),
		apidesc.IntegerParam("pageSize", "The largest number of jobs on the page."),
		apidesc.StringParam("pageToken", "The nextPageToken of the page before, for the same query."),
	).WithBodies(apidesc.NoBody, jobListSchema),
	apidesc.Pattern(http.MethodGet, logPath): apidesc.Name("getJobLog", "jobs").On(apidesc.KindUnit).WithQuery(
		apidesc.IntegerParam("after", "The seq of the last entry that the client has. The stream starts after it."),
	).WithBodies(apidesc.NoBody, apidesc.Typed[logEntry]("JobLogEntry")),
	apidesc.Pattern(http.MethodPost, "/api/dustpan/v1/namespaces/{namespace}/jobs"): apidesc.Name(
		"startJob",
		"jobs",
	).WithStatus(http.StatusCreated).WithETag().On(apidesc.KindUnit).WithBodies(apidesc.Typed[startBody](
		"StartJobRequest",
		apidesc.Named[unitRequest]("JobUnitRequest"),
		apidesc.Named[locationRequest]("JobLocationRequest"),
	), jobSchema),
	apidesc.Pattern(http.MethodPost, approvalsPath): apidesc.Name(
		"approveJob",
		"jobs",
	).WithStatus(http.StatusCreated).WithETag().On(apidesc.KindUnit).WithBodies(apidesc.Typed[approveBody](
		"ApproveJobRequest",
		apidesc.Named[bindingBody]("JobPlanBinding"),
	), jobSchema),
}

// jobDeclarations name the types inside the document of a job.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant slices.
var jobDeclarations = []apidesc.Declaration{
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
	apidesc.Named[triggerResponse]("JobTrigger"),
	apidesc.Named[deliveryResponse]("JobDelivery"),
	apidesc.Named[closedResponse]("JobClosed"),
}

// jobSchema is the body that the routes answer with for a job.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant structs.
var jobSchema = apidesc.Typed[jobResponse]("Job", jobDeclarations...)

// jobListSchema is the body of the job list.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant structs.
var jobListSchema = apidesc.Typed[listJobsResponse](
	"ListJobsResponse", append([]apidesc.Declaration{apidesc.Named[jobResponse]("Job")}, jobDeclarations...)...,
)

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for i := range h.routes {
		route := &h.routes[i]

		// The wrapper reads the namespace of the path before the reads on
		// the unit, for each route that is not public.
		var namespaceActions []string

		actions := make([]string, 0, len(route.Reads)+len(route.Actions)+len(route.Filters)+len(route.Writes)+1)

		if route.Scope != ScopePublic {
			namespaceActions = []string{string(identity.ActionNamespaceRead)}
			actions = append(actions, namespaceActions...)
		}

		for _, action := range slices.Concat(route.Reads, route.Actions, route.Filters, route.Writes) {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method:           route.Method,
			Path:             route.Path,
			Actions:          actions,
			NamespaceActions: namespaceActions,
			Public:           route.Scope == ScopePublic,
			IdempotencyKey:   route.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}

// userAgent returns the User-Agent header of a request, for the audit record.
func userAgent(r *http.Request) string {
	return apidesc.Header(r, "User-Agent")
}
