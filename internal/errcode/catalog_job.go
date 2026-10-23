package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// JobAgentDenied means that the rules of the agent refuse the job.
	JobAgentDenied = "job.agent-denied"
	// JobFrozen means that a freeze stops the job.
	JobFrozen = "job.frozen"
	// JobGateEnforced means that an enforced gate refuses the job.
	JobGateEnforced = "job.gate-enforced"
	// JobInvalid means that the request to start the job is not valid.
	JobInvalid = "job.invalid"
	// JobNotFound means that the job does not exist, or that the caller cannot
	// read it.
	JobNotFound = "job.not-found"
	// JobLimit means that the job is over a limit of the job service.
	JobLimit = "job.limit"
	// JobPolicyDenied means that the policy refuses the job.
	JobPolicyDenied = "job.policy-denied"
	// JobUnsupported means that the server cannot run this kind of job.
	JobUnsupported = "job.unsupported"
)

// jobSpecs are the codes of a request to start a job that the job
// service refuses.
func jobSpecs() []Spec {
	return []Spec{
		{Code: JobAgentDenied, Meaning: "the rules of the agent refuse the job", Args: nil},
		{Code: JobFrozen, Meaning: "a freeze stops the job", Args: nil},
		{Code: JobGateEnforced, Meaning: "an enforced gate refuses the job", Args: nil},
		{Code: JobInvalid, Meaning: "the request to start the job is not valid", Args: nil},
		{Code: JobLimit, Meaning: "the job is over a limit of the job service", Args: nil},
		{
			Code:    JobNotFound,
			Meaning: "the job does not exist in the namespace, or the caller cannot read it",
			Args:    nil,
		},
		{Code: JobPolicyDenied, Meaning: "the policy refuses the job", Args: nil},
		{Code: JobUnsupported, Meaning: "the server cannot run this kind of job", Args: nil},
	}
}
