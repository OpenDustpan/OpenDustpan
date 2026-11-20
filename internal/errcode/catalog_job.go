package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// JobAgentDenied means that the rules of the agent refuse the job.
	JobAgentDenied = "job.agent-denied"
	// JobAlreadyApproved means that the caller approved this phase already.
	JobAlreadyApproved = "job.already-approved"
	// JobConflict means that the job changed since the caller read it.
	JobConflict = "job.conflict"
	// JobFinished means that the job ended already.
	JobFinished = "job.finished"
	// JobFrozen means that a freeze stops the job.
	JobFrozen = "job.frozen"
	// JobGateEnforced means that an enforced gate refuses the job.
	JobGateEnforced = "job.gate-enforced"
	// JobInvalid means that the request to start the job is not valid.
	JobInvalid = "job.invalid"
	// JobNoRequirement means that no requirement of the phase holds the approver.
	JobNoRequirement = "job.no-requirement"
	// JobNotAwaitingApproval means that the job waits for no approval.
	JobNotAwaitingApproval = "job.not-awaiting-approval"
	// JobNotFound means that the job does not exist, or that the caller cannot
	// read it.
	JobNotFound = "job.not-found"
	// JobLimit means that the job is over a limit of the job service.
	JobLimit = "job.limit"
	// JobPlanChanged means that the plan of the approval is not the stored plan.
	JobPlanChanged = "job.plan-changed"
	// JobPlanStale means that the stored plan is too old, or was redacted.
	JobPlanStale = "job.plan-stale"
	// JobPolicyDenied means that the policy refuses the job.
	JobPolicyDenied = "job.policy-denied"
	// JobSelfApproval means that the person who started the job cannot approve it.
	JobSelfApproval = "job.self-approval"
	// JobUnsupported means that the server cannot run this kind of job.
	JobUnsupported = "job.unsupported"
	// JobWrongState means that the state of the job does not allow the request.
	JobWrongState = "job.wrong-state"
)

// jobSpecs are the codes of a request to start a job that the job
// service refuses. No code of the area has arguments.
func jobSpecs() []Spec {
	meanings := [][2]string{
		{JobAgentDenied, "the rules of the agent refuse the job"},
		{JobAlreadyApproved, "the caller approved this phase already"},
		{JobConflict, "the job changed since the caller read it"},
		{JobFinished, "the job ended already"},
		{JobFrozen, "a freeze stops the job"},
		{JobGateEnforced, "an enforced gate refuses the job"},
		{JobInvalid, "the request to start the job is not valid"},
		{JobLimit, "the job is over a limit of the job service"},
		{JobNotFound, "the job does not exist in the namespace, or the caller cannot read it"},
		{JobNoRequirement, "no requirement of the phase holds the approver"},
		{JobNotAwaitingApproval, "the job waits for no approval"},
		{JobPlanChanged, "the plan of the approval is not the stored plan"},
		{JobPlanStale, "the stored plan is too old, or was redacted"},
		{JobPolicyDenied, "the policy refuses the job"},
		{JobSelfApproval, "the person who started the job cannot approve it"},
		{JobUnsupported, "the server cannot run this kind of job"},
		{JobWrongState, "the state of the job does not allow the request"},
	}

	out := make([]Spec, 0, len(meanings))
	for _, m := range meanings {
		out = append(out, Spec{Code: m[0], Meaning: m[1], Args: nil})
	}

	return out
}
