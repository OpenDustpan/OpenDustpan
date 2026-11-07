package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// JobAgentDenied means that the rules of the agent refuse the job.
	JobAgentDenied = "job.agent-denied"
	// JobAlreadyApproved means that the caller approved this phase already.
	JobAlreadyApproved = "job.already-approved"
	// JobAuthStale means that the approval of a destroy has no fresh MFA step.
	JobAuthStale = "job.auth-stale"
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
)

// jobSpecs are the codes of a request to start a job that the job
// service refuses.
func jobSpecs() []Spec {
	return []Spec{
		{Code: JobAgentDenied, Meaning: "the rules of the agent refuse the job", Args: nil},
		{Code: JobAlreadyApproved, Meaning: "the caller approved this phase already", Args: nil},
		{Code: JobAuthStale, Meaning: "the approval of a destroy has no fresh MFA step", Args: nil},
		{Code: JobConflict, Meaning: "the job changed since the caller read it", Args: nil},
		{Code: JobFinished, Meaning: "the job ended already", Args: nil},
		{Code: JobFrozen, Meaning: "a freeze stops the job", Args: nil},
		{Code: JobGateEnforced, Meaning: "an enforced gate refuses the job", Args: nil},
		{Code: JobInvalid, Meaning: "the request to start the job is not valid", Args: nil},
		{Code: JobLimit, Meaning: "the job is over a limit of the job service", Args: nil},
		{
			Code:    JobNotFound,
			Meaning: "the job does not exist in the namespace, or the caller cannot read it",
			Args:    nil,
		},
		{Code: JobNotAwaitingApproval, Meaning: "the job waits for no approval", Args: nil},
		{Code: JobPlanChanged, Meaning: "the plan of the approval is not the stored plan", Args: nil},
		{Code: JobPlanStale, Meaning: "the stored plan is too old, or was redacted", Args: nil},
		{Code: JobPolicyDenied, Meaning: "the policy refuses the job", Args: nil},
		{Code: JobSelfApproval, Meaning: "the person who started the job cannot approve it", Args: nil},
		{Code: JobUnsupported, Meaning: "the server cannot run this kind of job", Args: nil},
	}
}
